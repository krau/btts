package plugin

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/krau/btts/config"
	"github.com/krau/mygotg/ext"
	"github.com/krau/mygotg/types"
)

const testCleanChatID = int64(1234567890)

type fakeCleanMemberAPI struct {
	mu       sync.Mutex
	members  []int64
	deleted  map[int64]bool
	offline  map[int64]int // userID -> was_online unix ts, missing means no exact status
	status   map[int64]tg.UserStatusClass
	lastMsg  map[int64]int // userID -> last message date
	msgCount map[int64]int // userID -> message count
	banned   []int64
	failBan  map[int64]bool
	edits    []string

	uploadParts  map[int64]map[int][]byte
	sentMedia    []*tg.InputMediaUploadedDocument
	mediaCaption string
}

func newFakeCleanMemberAPI(n int) *fakeCleanMemberAPI {
	f := &fakeCleanMemberAPI{
		deleted:     map[int64]bool{},
		offline:     map[int64]int{},
		status:      map[int64]tg.UserStatusClass{},
		lastMsg:     map[int64]int{},
		msgCount:    map[int64]int{},
		failBan:     map[int64]bool{},
		uploadParts: map[int64]map[int][]byte{},
	}
	for i := 1; i <= n; i++ {
		f.members = append(f.members, int64(1000+i))
	}
	return f
}

func (f *fakeCleanMemberAPI) user(id int64) *tg.User {
	u := &tg.User{
		ID:         id,
		AccessHash: 700000 + id,
		FirstName:  fmt.Sprintf("用户%d", id),
		Deleted:    f.deleted[id],
	}
	if status, ok := f.status[id]; ok {
		u.Status = status
	} else if ts, ok := f.offline[id]; ok {
		u.Status = &tg.UserStatusOffline{WasOnline: ts}
	} else {
		u.Status = &tg.UserStatusRecently{}
	}
	return u
}

func (f *fakeCleanMemberAPI) removeMember(id int64) {
	for i, m := range f.members {
		if m == id {
			f.members = append(f.members[:i], f.members[i+1:]...)
			return
		}
	}
}

func (f *fakeCleanMemberAPI) lastEdit() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.edits) == 0 {
		return ""
	}
	return f.edits[len(f.edits)-1]
}

func (f *fakeCleanMemberAPI) bannedIDs() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), f.banned...)
}

func (f *fakeCleanMemberAPI) uploadedFile() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, parts := range f.uploadParts {
		indexes := make([]int, 0, len(parts))
		for i := range parts {
			indexes = append(indexes, i)
		}
		sort.Ints(indexes)
		var out []byte
		for _, i := range indexes {
			out = append(out, parts[i]...)
		}
		return out
	}
	return nil
}

func (f *fakeCleanMemberAPI) sentMediaCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sentMedia)
}

func encodeInto(output bin.Decoder, res bin.Encoder) error {
	buf := &bin.Buffer{}
	if err := res.Encode(buf); err != nil {
		return err
	}
	return output.Decode(buf)
}

func (f *fakeCleanMemberAPI) Invoke(_ context.Context, input bin.Encoder, output bin.Decoder) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch req := input.(type) {
	case *tg.ChannelsGetParticipantRequest:
		return encodeInto(output, &tg.ChannelsChannelParticipant{
			Participant: &tg.ChannelParticipantCreator{UserID: 1},
		})
	case *tg.ChannelsGetParticipantsRequest:
		start := min(req.Offset, len(f.members))
		end := min(start+req.Limit, len(f.members))
		ids := f.members[start:end]
		participants := make([]tg.ChannelParticipantClass, 0, len(ids))
		users := make([]tg.UserClass, 0, len(ids))
		for _, id := range ids {
			participants = append(participants, &tg.ChannelParticipant{UserID: id, Date: 1})
			users = append(users, f.user(id))
		}
		return encodeInto(output, &tg.ChannelsChannelParticipants{
			Count:        len(f.members),
			Participants: participants,
			Users:        users,
		})
	case *tg.ChannelsEditBannedRequest:
		uid := req.Participant.(*tg.InputPeerUser).UserID
		if f.failBan[uid] {
			return tgerr.New(400, "USER_ADMIN_INVALID")
		}
		f.banned = append(f.banned, uid)
		f.removeMember(uid)
		return encodeInto(output, &tg.Updates{})
	case *tg.MessagesSearchRequest:
		uid := req.FromID.(*tg.InputPeerUser).UserID
		res := &tg.MessagesChannelMessages{Count: f.msgCount[uid]}
		if date, ok := f.lastMsg[uid]; ok {
			res.Messages = []tg.MessageClass{&tg.Message{
				ID:     1,
				Date:   date,
				PeerID: &tg.PeerChannel{ChannelID: testCleanChatID},
			}}
		}
		return encodeInto(output, res)
	case *tg.MessagesGetFullChatRequest:
		participants := make([]tg.ChatParticipantClass, 0, len(f.members))
		users := make([]tg.UserClass, 0, len(f.members))
		for _, id := range f.members {
			participants = append(participants, &tg.ChatParticipant{UserID: id, Date: 1})
			users = append(users, f.user(id))
		}
		return encodeInto(output, &tg.MessagesChatFull{
			FullChat: &tg.ChatFull{Participants: &tg.ChatParticipants{
				ChatID:       req.ChatID,
				Participants: participants,
			}},
			Users: users,
		})
	case *tg.MessagesDeleteChatUserRequest:
		uid := req.UserID.(*tg.InputUser).UserID
		f.banned = append(f.banned, uid)
		f.removeMember(uid)
		return encodeInto(output, &tg.Updates{})
	case *tg.MessagesEditMessageRequest:
		f.edits = append(f.edits, req.Message)
		return encodeInto(output, &tg.Updates{})
	case *tg.UploadSaveFilePartRequest:
		if f.uploadParts[req.FileID] == nil {
			f.uploadParts[req.FileID] = map[int][]byte{}
		}
		f.uploadParts[req.FileID][req.FilePart] = append([]byte(nil), req.Bytes...)
		return encodeInto(output, &tg.BoolTrue{})
	case *tg.MessagesSendMediaRequest:
		media, ok := req.Media.(*tg.InputMediaUploadedDocument)
		if !ok {
			return fmt.Errorf("unexpected media %T", req.Media)
		}
		f.sentMedia = append(f.sentMedia, media)
		f.mediaCaption = req.Message
		return encodeInto(output, &tg.Updates{
			Updates: []tg.UpdateClass{
				&tg.UpdateNewChannelMessage{
					Message: &tg.Message{
						ID:     1,
						PeerID: &tg.PeerChannel{ChannelID: testCleanChatID},
					},
					Pts:      1,
					PtsCount: 1,
				},
			},
		})
	case *tg.ChannelsDeleteMessagesRequest:
		return encodeInto(output, &tg.MessagesAffectedMessages{})
	}
	return fmt.Errorf("unexpected request %T", input)
}

func newCleanMemberTestEnv(f *fakeCleanMemberAPI) (*ext.Context, *tg.Entities) {
	channel := &tg.Channel{ID: testCleanChatID, AccessHash: 42, Megagroup: true, Title: "test"}
	entities := &tg.Entities{Channels: map[int64]*tg.Channel{channel.ID: channel}}
	ctx := ext.NewContext(
		context.Background(),
		tg.NewClient(f),
		nil,
		&tg.User{ID: 1},
		nil,
		entities,
		false,
	)
	return ctx, entities
}

func testCleanMessageUpdate(entities *tg.Entities, id int, text string) *ext.Update {
	raw := &tg.Message{
		ID:      id,
		PeerID:  &tg.PeerChannel{ChannelID: testCleanChatID},
		Out:     true,
		Message: text,
	}
	return &ext.Update{
		EffectiveMessage: types.ConstructMessage(raw),
		Entities:         entities,
		UpdateClass:      &tg.UpdateNewChannelMessage{Message: raw},
	}
}

func waitCleanMemberDone(t *testing.T, f *fakeCleanMemberAPI) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		edit := f.lastEdit()
		if strings.HasPrefix(edit, "成功清理了") || strings.HasPrefix(edit, "查找到了") ||
			strings.HasPrefix(edit, "处理失败") || strings.HasPrefix(edit, "你好像") ||
			edit == "清理模式错误" {
			return edit
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("clean_member did not finish, last edit: %q", f.lastEdit())
	return ""
}

func runCleanMemberFlow(t *testing.T, f *fakeCleanMemberAPI, replies ...string) string {
	t.Helper()
	config.C.Plugin.Prefixes = []string{","}
	ctx, entities := newCleanMemberTestEnv(f)
	if err := Dispatcher(ctx, testCleanMessageUpdate(entities, 100, ",clean_member")); err != nil {
		t.Fatalf("command: %v", err)
	}
	if got := f.lastEdit(); got != cleanMemberModePrompt {
		t.Fatalf("expected mode prompt, got %q", got)
	}
	for i, reply := range replies {
		if err := Dispatcher(ctx, testCleanMessageUpdate(entities, 101+i, reply)); err != nil {
			t.Fatalf("reply %q: %v", reply, err)
		}
	}
	return waitCleanMemberDone(t, f)
}

func TestCleanMemberMode5CleansEveryone(t *testing.T) {
	f := newFakeCleanMemberAPI(500)
	got := runCleanMemberFlow(t, f, "5", "清理")
	if got != "成功清理了 500 人。\n\n"+cleanMemberExportHint {
		t.Fatalf("unexpected result: %q", got)
	}
	if len(f.bannedIDs()) != 500 {
		t.Fatalf("expected 500 bans, got %d", len(f.bannedIDs()))
	}
	seen := map[int64]bool{}
	for _, id := range f.bannedIDs() {
		if seen[id] {
			t.Fatalf("user %d banned twice", id)
		}
		seen[id] = true
	}
	if len(f.members) != 0 {
		t.Fatalf("expected empty group, %d left", len(f.members))
	}
}

func TestCleanMemberSearchOnly(t *testing.T) {
	f := newFakeCleanMemberAPI(300)
	got := runCleanMemberFlow(t, f, "5", "查找")
	if got != "查找到了 300 人。\n\n"+cleanMemberExportHint {
		t.Fatalf("unexpected result: %q", got)
	}
	if len(f.bannedIDs()) != 0 {
		t.Fatalf("search only must not ban, got %d bans", len(f.bannedIDs()))
	}
}

func TestCleanMemberMode4DeletedAccounts(t *testing.T) {
	f := newFakeCleanMemberAPI(10)
	f.deleted[1001] = true
	f.deleted[1007] = true
	got := runCleanMemberFlow(t, f, "4", "清理")
	want := "成功清理了 2 人。\n\n" +
		"1. 用户1001 | ID: 1001 | 已注销账号\n" +
		"2. 用户1007 | ID: 1007 | 已注销账号"
	if got != want {
		t.Fatalf("unexpected result: %q", got)
	}
	banned := f.bannedIDs()
	if len(banned) != 2 || banned[0] != 1001 || banned[1] != 1007 {
		t.Fatalf("unexpected banned list: %v", banned)
	}
}

func TestCleanMemberMode1LastSeen(t *testing.T) {
	f := newFakeCleanMemberAPI(8)
	f.offline[1001] = int(time.Now().AddDate(0, 0, -40).Unix()) // 精确: 40 天未上线
	f.offline[1002] = int(time.Now().AddDate(0, 0, -1).Unix())  // 精确: 1 天未上线
	f.status[1003] = &tg.UserStatusEmpty{}                      // 很久以前 (> 30 天)
	f.status[1004] = &tg.UserStatusLastMonth{}                  // 7-30 天
	f.status[1005] = &tg.UserStatusLastWeek{}                   // 3-7 天
	f.status[1006] = &tg.UserStatusRecently{}                   // < 3 天
	f.status[1007] = &tg.UserStatusOnline{}                     // 在线
	got := runCleanMemberFlow(t, f, "1", "14", "清理")
	// 14 天: 精确 40 天 与 >30 天的桶可以确定; 7-30 天桶不确定
	if !strings.HasPrefix(got, "成功清理了 2 人。另有 1 人最近上线时间不明确，未处理。\n\n1. ") {
		t.Fatalf("unexpected result: %q", got)
	}
	if !strings.Contains(got, "用户1003 | ID: 1003 | 最后上线: 超过 30 天") {
		t.Fatalf("inline list missing expected hit: %q", got)
	}
	banned := f.bannedIDs()
	if len(banned) != 2 || banned[0] != 1001 || banned[1] != 1003 {
		t.Fatalf("unexpected banned list: %v", banned)
	}
}

func TestCleanMemberMode1MinDaysAndBuckets(t *testing.T) {
	f := newFakeCleanMemberAPI(4)
	f.status[1001] = &tg.UserStatusEmpty{}
	f.status[1002] = &tg.UserStatusLastMonth{}
	f.status[1003] = &tg.UserStatusLastWeek{}
	f.status[1004] = &tg.UserStatusRecently{}
	got := runCleanMemberFlow(t, f, "1", "1", "清理") // 输入 1 天, 被提升为最小值 7
	// 7 天: >30 天 与 7-30 天 的桶可以确定; 3-7 天桶不确定
	if !strings.HasPrefix(got, "成功清理了 2 人。另有 1 人最近上线时间不明确，未处理。\n\n1. ") {
		t.Fatalf("unexpected result: %q", got)
	}
	if !strings.Contains(got, "用户1002 | ID: 1002 | 最后上线: 7-30 天前") {
		t.Fatalf("inline list missing expected hit: %q", got)
	}
	banned := f.bannedIDs()
	if len(banned) != 2 || banned[0] != 1001 || banned[1] != 1002 {
		t.Fatalf("unexpected banned list: %v", banned)
	}
}

func TestCleanMemberMode2LastMessage(t *testing.T) {
	f := newFakeCleanMemberAPI(4)
	f.lastMsg[1001] = int(time.Now().AddDate(0, 0, -40).Unix()) // 应清理
	f.lastMsg[1002] = int(time.Now().AddDate(0, 0, -1).Unix())  // 不清理
	// 1003, 1004 从未发言, 不清理(与参考实现一致)
	got := runCleanMemberFlow(t, f, "2", "30", "清理")
	if !strings.HasPrefix(got, "成功清理了 1 人。\n\n1. 用户1001 | ID: 1001 | 最后发言: ") {
		t.Fatalf("unexpected result: %q", got)
	}
	if banned := f.bannedIDs(); len(banned) != 1 || banned[0] != 1001 {
		t.Fatalf("unexpected banned list: %v", banned)
	}
}

func TestCleanMemberMode3MessageCount(t *testing.T) {
	f := newFakeCleanMemberAPI(5)
	f.msgCount[1001] = 0
	f.msgCount[1002] = 3
	f.msgCount[1003] = 9
	f.msgCount[1004] = 1
	f.msgCount[1005] = 10
	got := runCleanMemberFlow(t, f, "3", "5", "清理")
	if !strings.HasPrefix(got, "成功清理了 3 人。\n\n1. 用户1001 | ID: 1001 | 发言数: 0\n") {
		t.Fatalf("unexpected result: %q", got)
	}
	banned := f.bannedIDs()
	if len(banned) != 3 || banned[0] != 1001 || banned[1] != 1002 || banned[2] != 1004 {
		t.Fatalf("unexpected banned list: %v", banned)
	}
}

func TestCleanMemberInvalidMode(t *testing.T) {
	f := newFakeCleanMemberAPI(1)
	got := runCleanMemberFlow(t, f, "9")
	if got != "清理模式错误" {
		t.Fatalf("unexpected result: %q", got)
	}
	if len(f.bannedIDs()) != 0 {
		t.Fatalf("must not ban on invalid mode")
	}
}

func TestCleanMemberBasicGroup(t *testing.T) {
	f := newFakeCleanMemberAPI(5)
	f.deleted[1002] = true
	f.deleted[1004] = true
	chat := types.Chat(tg.Chat{ID: 999, Title: "basic"})
	ctx := ext.NewContext(context.Background(), tg.NewClient(f), nil, &tg.User{ID: 1}, nil, nil, false)
	count := 0
	err := forEachBasicChatCleanMember(ctx, &chat, func(user *tg.User) (bool, error) {
		if !user.Deleted {
			return false, nil
		}
		count++
		return kickCleanMember(ctx, &chat, user)
	})
	if err != nil {
		t.Fatalf("iterate basic chat: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 deleted accounts, got %d", count)
	}
	banned := f.bannedIDs()
	if len(banned) != 2 || banned[0] != 1002 || banned[1] != 1004 {
		t.Fatalf("unexpected banned list: %v", banned)
	}
	if len(f.members) != 3 {
		t.Fatalf("expected 3 members left, got %d", len(f.members))
	}
}

func TestCleanMemberUnremovableMembersTerminate(t *testing.T) {
	f := newFakeCleanMemberAPI(300)
	for _, id := range f.members {
		f.failBan[id] = true // 全员无法被移除(如全是管理员), 必须终止而不是死循环
	}
	got := runCleanMemberFlow(t, f, "5", "清理")
	if got != "成功清理了 300 人。\n\n"+cleanMemberExportHint {
		t.Fatalf("unexpected result: %q", got)
	}
	if len(f.bannedIDs()) != 0 {
		t.Fatalf("expected no successful bans, got %d", len(f.bannedIDs()))
	}
	if len(f.members) != 300 {
		t.Fatalf("expected group unchanged, %d left", len(f.members))
	}
}

func TestCleanMemberExportFile(t *testing.T) {
	f := newFakeCleanMemberAPI(500)
	got := runCleanMemberFlow(t, f, "5", "清理")
	if got != "成功清理了 500 人。\n\n"+cleanMemberExportHint {
		t.Fatalf("unexpected result: %q", got)
	}
	if f.sentMediaCount() != 0 {
		t.Fatalf("file must not be sent before 导出")
	}
	config.C.Plugin.Prefixes = []string{","}
	ctx, entities := newCleanMemberTestEnv(f)
	// 其它消息不应触发导出, 也不应被消费
	if err := Dispatcher(ctx, testCleanMessageUpdate(entities, 900, "你好")); err != nil {
		t.Fatal(err)
	}
	if f.sentMediaCount() != 0 {
		t.Fatalf("unrelated message triggered export")
	}
	if err := Dispatcher(ctx, testCleanMessageUpdate(entities, 901, cleanMemberExportCmd)); err != nil {
		t.Fatal(err)
	}
	if f.sentMediaCount() != 1 {
		t.Fatalf("expected 1 sent media, got %d", f.sentMediaCount())
	}
	data := f.uploadedFile()
	if !bytes.HasPrefix(data, []byte("\xEF\xBB\xBF")) {
		t.Fatalf("csv missing utf-8 bom")
	}
	records, err := csv.NewReader(bytes.NewReader(data[3:])).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(records) != 501 {
		t.Fatalf("expected 501 csv rows, got %d", len(records))
	}
	if records[0][0] != "用户ID" || records[0][3] != "详情" {
		t.Fatalf("unexpected header: %v", records[0])
	}
	if records[1][0] != "1001" || records[1][2] != "用户1001" || records[1][3] != "全部成员" {
		t.Fatalf("unexpected first row: %v", records[1])
	}
	// 导出后状态被清除
	if err := Dispatcher(ctx, testCleanMessageUpdate(entities, 902, cleanMemberExportCmd)); err != nil {
		t.Fatal(err)
	}
	if f.sentMediaCount() != 1 {
		t.Fatalf("export state should be consumed")
	}
}

func TestCleanMemberSessionIgnoresEdits(t *testing.T) {
	f := newFakeCleanMemberAPI(1)
	config.C.Plugin.Prefixes = []string{","}
	ctx, entities := newCleanMemberTestEnv(f)
	if err := Dispatcher(ctx, testCleanMessageUpdate(entities, 100, ",clean_member")); err != nil {
		t.Fatal(err)
	}
	// 提示消息自身的编辑不应被当作会话输入
	edited := testCleanMessageUpdate(entities, 100, cleanMemberModePrompt)
	edited.EffectiveMessage.EditDate = 12345
	if err := Dispatcher(ctx, edited); err != nil {
		t.Fatal(err)
	}
	if got := f.lastEdit(); got != cleanMemberModePrompt {
		t.Fatalf("session consumed an edit update: %q", got)
	}
	if err := Dispatcher(ctx, testCleanMessageUpdate(entities, 101, "5")); err != nil {
		t.Fatal(err)
	}
	if got := f.lastEdit(); got != cleanMemberConfirmPrompt {
		t.Fatalf("session broken after edit update: %q", got)
	}
}
