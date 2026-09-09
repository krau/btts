package plugin

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/krau/mygotg/ext"
	"github.com/krau/mygotg/types"
)

const (
	cleanMemberMinDays       = 7               // 按天数清理时的最小天数
	cleanMemberKickDuration  = 5 * time.Minute // 临时封禁时长, 到期自动解封
	cleanMemberSessionTTL    = 5 * time.Minute // 交互会话超时时间
	cleanMemberExportTTL     = 5 * time.Minute // 导出列表指令的有效期
	cleanMemberPageSize      = 200             // 每次拉取成员的数量
	cleanMemberMaxFloodRetry = 3               // FloodWait 重试次数
	cleanMemberInlineLimit   = 20              // 命中人数不超过该值时直接在消息中列出
)

const (
	cleanMemberModePrompt = "请选择清理模式：\n\n" +
		"1. 按未上线时间清理\n" +
		"2. 按未发言时间清理（大群慎用）\n" +
		"3. 按发言数清理\n" +
		"4. 清理死号\n" +
		"5. 清理所有人（大群慎用）"
	cleanMemberDaysPrompt    = "请输入清理天数："
	cleanMemberCountPrompt   = "清理发言少于多少条的群成员："
	cleanMemberConfirmPrompt = "查找还是清理？"
	cleanMemberWorkingText   = "遍历成员中。。。"
	cleanMemberExportCmd     = "导出"
	cleanMemberExportHint    = "人数较多，发送「导出」以文件形式获取列表。"
)

type cleanMemberStep int

const (
	cleanMemberStepMode cleanMemberStep = iota
	cleanMemberStepDays
	cleanMemberStepCount
	cleanMemberStepConfirm
)

type cleanMemberSession struct {
	chatID    int64
	step      cleanMemberStep
	mode      string
	day       int
	promptID  int
	expiresAt time.Time
}

var (
	cleanMemberSessionsMu sync.Mutex
	cleanMemberSessions   = make(map[int64]*cleanMemberSession)
)

func setCleanMemberSession(session *cleanMemberSession) {
	session.expiresAt = time.Now().Add(cleanMemberSessionTTL)
	cleanMemberSessionsMu.Lock()
	defer cleanMemberSessionsMu.Unlock()
	cleanMemberSessions[session.chatID] = session
}

func getCleanMemberSession(chatID int64) *cleanMemberSession {
	cleanMemberSessionsMu.Lock()
	defer cleanMemberSessionsMu.Unlock()
	session, ok := cleanMemberSessions[chatID]
	if !ok {
		return nil
	}
	if time.Now().After(session.expiresAt) {
		delete(cleanMemberSessions, chatID)
		return nil
	}
	return session
}

func deleteCleanMemberSession(chatID int64) {
	cleanMemberSessionsMu.Lock()
	defer cleanMemberSessionsMu.Unlock()
	delete(cleanMemberSessions, chatID)
}

type cleanMemberHit struct {
	id       int64
	username string
	name     string
	detail   string
}

type cleanMemberExport struct {
	chatID    int64
	promptID  int
	hits      []cleanMemberHit
	expiresAt time.Time
}

var (
	cleanMemberExportsMu sync.Mutex
	cleanMemberExports   = make(map[int64]*cleanMemberExport)
)

func setCleanMemberExport(export *cleanMemberExport) {
	export.expiresAt = time.Now().Add(cleanMemberExportTTL)
	cleanMemberExportsMu.Lock()
	defer cleanMemberExportsMu.Unlock()
	cleanMemberExports[export.chatID] = export
}

func getCleanMemberExport(chatID int64) *cleanMemberExport {
	cleanMemberExportsMu.Lock()
	defer cleanMemberExportsMu.Unlock()
	export, ok := cleanMemberExports[chatID]
	if !ok {
		return nil
	}
	if time.Now().After(export.expiresAt) {
		delete(cleanMemberExports, chatID)
		return nil
	}
	return export
}

func deleteCleanMemberExport(chatID int64) {
	cleanMemberExportsMu.Lock()
	defer cleanMemberExportsMu.Unlock()
	delete(cleanMemberExports, chatID)
}

// CleanMemberHandler 按多种方式清理群成员. 用法: [prefix]clean_member
func CleanMemberHandler(ctx *Context, u *ext.Update) error {
	msg := u.EffectiveMessage
	chat := u.EffectiveChat()
	if msg == nil || chat.GetID() == 0 {
		return nil
	}
	if !isCleanMemberGroup(chat) {
		return editCleanMemberMessage(ctx.Context, chat, msg.GetID(), "该命令仅支持在群组中使用")
	}
	isAdmin, err := checkCleanMemberAdmin(ctx.Context, chat)
	if err != nil {
		if tgerr.Is(err, "CHAT_ADMIN_REQUIRED") {
			return editCleanMemberMessage(ctx.Context, chat, msg.GetID(), "您不是群管理员，无法使用此命令")
		}
		log.FromContext(ctx).Error("Failed to check clean_member admin", "chat_id", chat.GetID(), "error", err)
		return editCleanMemberMessage(ctx.Context, chat, msg.GetID(), "检查权限失败，无法使用此命令")
	}
	if !isAdmin {
		return editCleanMemberMessage(ctx.Context, chat, msg.GetID(), "您不是群管理员，无法使用此命令")
	}
	deleteCleanMemberExport(chat.GetID())
	setCleanMemberSession(&cleanMemberSession{
		chatID:   chat.GetID(),
		step:     cleanMemberStepMode,
		promptID: msg.GetID(),
	})
	return editCleanMemberMessage(ctx.Context, chat, msg.GetID(), cleanMemberModePrompt)
}

// handleCleanMemberSession 处理进行中的 clean_member 会话, 返回该消息是否已被消费
func handleCleanMemberSession(ctx *ext.Context, u *ext.Update) (bool, error) {
	msg := u.EffectiveMessage
	chat := u.EffectiveChat()
	if msg == nil || chat.GetID() == 0 {
		return false, nil
	}
	// 忽略对提示消息的编辑, 否则会被当作会话输入
	if msg.EditDate != 0 || msg.IsService {
		return false, nil
	}
	session := getCleanMemberSession(chat.GetID())
	if session == nil {
		return false, nil
	}
	// Dispatcher 仅处理自己发出的消息, 无需校验发送者
	text := strings.TrimSpace(msg.GetMessage())
	deleteCleanMemberMessage(ctx, chat, msg.GetID())
	switch session.step {
	case cleanMemberStepMode:
		switch text {
		case "1", "2":
			session.mode = text
			session.step = cleanMemberStepDays
			setCleanMemberSession(session)
			return true, editCleanMemberMessage(ctx, chat, session.promptID, cleanMemberDaysPrompt)
		case "3":
			session.mode = text
			session.step = cleanMemberStepCount
			setCleanMemberSession(session)
			return true, editCleanMemberMessage(ctx, chat, session.promptID, cleanMemberCountPrompt)
		case "4", "5":
			session.mode = text
			session.step = cleanMemberStepConfirm
			setCleanMemberSession(session)
			return true, editCleanMemberMessage(ctx, chat, session.promptID, cleanMemberConfirmPrompt)
		default:
			deleteCleanMemberSession(session.chatID)
			return true, editCleanMemberMessage(ctx, chat, session.promptID, "清理模式错误")
		}
	case cleanMemberStepDays:
		day, err := strconv.Atoi(text)
		if err != nil {
			deleteCleanMemberSession(session.chatID)
			return true, editCleanMemberMessage(ctx, chat, session.promptID, "清理天数错误")
		}
		session.day = max(day, cleanMemberMinDays)
		session.step = cleanMemberStepConfirm
		setCleanMemberSession(session)
		return true, editCleanMemberMessage(ctx, chat, session.promptID, cleanMemberConfirmPrompt)
	case cleanMemberStepCount:
		count, err := strconv.Atoi(text)
		if err != nil || count < 0 {
			deleteCleanMemberSession(session.chatID)
			return true, editCleanMemberMessage(ctx, chat, session.promptID, "发言条数错误")
		}
		session.day = count
		session.step = cleanMemberStepConfirm
		setCleanMemberSession(session)
		return true, editCleanMemberMessage(ctx, chat, session.promptID, cleanMemberConfirmPrompt)
	case cleanMemberStepConfirm:
		deleteCleanMemberSession(session.chatID)
		if err := editCleanMemberMessage(ctx, chat, session.promptID, cleanMemberWorkingText); err != nil {
			return true, err
		}
		go processCleanMember(ctx, chat, session, text == "查找")
		return true, nil
	}
	return false, nil
}

func isCleanMemberGroup(chat types.EffectiveChat) bool {
	switch c := chat.(type) {
	case *types.Chat:
		return true
	case *types.Channel:
		return c.Megagroup
	default:
		return false
	}
}

// checkCleanMemberAdmin 检查当前账号是否为群管理员或群主
func checkCleanMemberAdmin(ctx *ext.Context, chat types.EffectiveChat) (bool, error) {
	switch c := chat.(type) {
	case *types.Channel:
		res, err := ctx.Raw.ChannelsGetParticipant(ctx, &tg.ChannelsGetParticipantRequest{
			Channel:     c.GetInputChannel(),
			Participant: &tg.InputPeerSelf{},
		})
		if err != nil {
			return false, err
		}
		switch res.Participant.(type) {
		case *tg.ChannelParticipantCreator, *tg.ChannelParticipantAdmin:
			return true, nil
		default:
			return false, nil
		}
	case *types.Chat:
		res, err := ctx.Raw.MessagesGetFullChat(ctx, c.GetID())
		if err != nil {
			return false, err
		}
		full, ok := res.FullChat.(*tg.ChatFull)
		if !ok {
			return false, nil
		}
		participants, ok := full.Participants.(*tg.ChatParticipants)
		if !ok {
			return false, nil
		}
		for _, p := range participants.Participants {
			switch v := p.(type) {
			case *tg.ChatParticipantCreator:
				if ctx.Self != nil && v.UserID == ctx.Self.ID {
					return true, nil
				}
			case *tg.ChatParticipantAdmin:
				if ctx.Self != nil && v.UserID == ctx.Self.ID {
					return true, nil
				}
			}
		}
		return false, nil
	default:
		return false, nil
	}
}

func editCleanMemberMessage(ctx *ext.Context, chat types.EffectiveChat, msgID int, text string) error {
	peer := chat.GetInputPeer()
	if peer == nil {
		return fmt.Errorf("clean_member: invalid chat peer")
	}
	_, err := ctx.Raw.MessagesEditMessage(ctx, &tg.MessagesEditMessageRequest{
		Peer:    peer,
		ID:      msgID,
		Message: text,
	})
	return err
}

func deleteCleanMemberMessage(ctx *ext.Context, chat types.EffectiveChat, msgID int) {
	switch c := chat.(type) {
	case *types.Channel:
		_, _ = ctx.Raw.ChannelsDeleteMessages(ctx, &tg.ChannelsDeleteMessagesRequest{
			Channel: c.GetInputChannel(),
			ID:      []int{msgID},
		})
	default:
		_, _ = ctx.Raw.MessagesDeleteMessages(ctx, &tg.MessagesDeleteMessagesRequest{
			Revoke: true,
			ID:     []int{msgID},
		})
	}
}

func processCleanMember(ctx *ext.Context, chat types.EffectiveChat, session *cleanMemberSession, onlySearch bool) {
	report := func(text string) {
		if err := editCleanMemberMessage(ctx, chat, session.promptID, text); err != nil {
			log.FromContext(ctx).Error("Failed to edit clean_member message", "chat_id", chat.GetID(), "error", err)
		}
	}
	count := 0
	unresolved := 0
	hits := make([]cleanMemberHit, 0)
	err := forEachCleanMember(ctx, chat, func(user *tg.User) (bool, error) {
		matched, undetermined, detail, err := shouldCleanMember(ctx, chat, user, session.mode, session.day)
		if err != nil {
			return false, err
		}
		if !matched {
			if undetermined {
				unresolved++
			}
			return false, nil
		}
		count++
		hits = append(hits, newCleanMemberHit(user, detail))
		if onlySearch {
			return false, nil
		}
		return kickCleanMember(ctx, chat, user)
	})
	if err != nil {
		switch {
		case tgerr.Is(err, "CHAT_ADMIN_REQUIRED"):
			report("你好像并不拥有封禁用户权限。")
		default:
			if _, ok := tgerr.AsFloodWait(err); ok {
				report("处理失败，您已受到 TG 服务器限制。")
			} else {
				log.FromContext(ctx).Error("Failed to process clean_member", "chat_id", chat.GetID(), "error", err)
				report("处理失败: " + err.Error())
			}
		}
		return
	}
	var text string
	if onlySearch {
		text = fmt.Sprintf("查找到了 %d 人。", count)
	} else {
		text = fmt.Sprintf("成功清理了 %d 人。", count)
	}
	if unresolved > 0 {
		text += fmt.Sprintf("另有 %d 人最近上线时间不明确，未处理。", unresolved)
	}
	if len(hits) > 0 {
		if len(hits) <= cleanMemberInlineLimit {
			text += "\n\n" + formatCleanMemberHits(hits)
		} else {
			setCleanMemberExport(&cleanMemberExport{
				chatID:   chat.GetID(),
				promptID: session.promptID,
				hits:     hits,
			})
			text += "\n\n" + cleanMemberExportHint
		}
	}
	report(text)
}

// cleanMemberInactiveDaysRange 返回该状态可确定的最小/最大未上线天数, ok 为 false 表示无法判断.
// 未公开精确上线时间时 Telegram 只返回近似值, 参考 https://telegram.org/faq :
// recently 1 秒-3 天, within a week 3-7 天, within a month 7-30 天, a long time ago 超过 30 天.
func cleanMemberInactiveDaysRange(status tg.UserStatusClass) (minDays, maxDays int, ok bool) {
	switch status.(type) {
	case *tg.UserStatusOnline:
		return 0, 0, true
	case *tg.UserStatusRecently:
		return 0, 3, true
	case *tg.UserStatusLastWeek:
		return 3, 7, true
	case *tg.UserStatusLastMonth:
		return 7, 30, true
	case *tg.UserStatusEmpty:
		return 30, math.MaxInt, true
	default:
		return 0, 0, false
	}
}

func shouldCleanMember(ctx *ext.Context, chat types.EffectiveChat, user *tg.User, mode string, day int) (matched bool, unresolved bool, detail string, err error) {
	switch mode {
	case "1":
		if offline, ok := user.Status.(*tg.UserStatusOffline); ok {
			return time.Unix(int64(offline.WasOnline), 0).Before(time.Now().AddDate(0, 0, -day)), false,
				cleanMemberStatusDetail(user.Status), nil
		}
		minDays, maxDays, ok := cleanMemberInactiveDaysRange(user.Status)
		if !ok {
			return false, false, "", nil
		}
		// 只有能确定未上线时间超过 day 天才清理, 介于两者之间的记为待定
		return minDays >= day, minDays < day && maxDays >= day, cleanMemberStatusDetail(user.Status), nil
	case "2":
		if user.AccessHash == 0 {
			return false, false, "", nil
		}
		last, err := lastCleanMemberMessage(ctx, chat, user)
		if err != nil {
			if tgerr.Is(err, "PEER_ID_INVALID") {
				return false, false, "", nil
			}
			return false, false, "", err
		}
		if last == nil {
			return false, false, "", nil
		}
		detail := "最后发言: " + time.Unix(int64(last.Date), 0).Format("2006-01-02 15:04")
		return time.Unix(int64(last.Date), 0).Before(time.Now().AddDate(0, 0, -day)), false, detail, nil
	case "3":
		if user.AccessHash == 0 {
			return false, false, "", nil
		}
		count, err := countCleanMemberMessages(ctx, chat, user)
		if err != nil {
			if tgerr.Is(err, "PEER_ID_INVALID") {
				return false, false, "", nil
			}
			return false, false, "", err
		}
		return count < day, false, fmt.Sprintf("发言数: %d", count), nil
	case "4":
		return user.Deleted, false, "已注销账号", nil
	case "5":
		return true, false, "全部成员", nil
	default:
		return false, false, "", nil
	}
}

// cleanMemberStatusDetail 描述用户的最近上线状态
func cleanMemberStatusDetail(status tg.UserStatusClass) string {
	switch s := status.(type) {
	case *tg.UserStatusOffline:
		return "最后上线: " + time.Unix(int64(s.WasOnline), 0).Format("2006-01-02 15:04")
	case *tg.UserStatusOnline:
		return "在线"
	case *tg.UserStatusRecently:
		return "最后上线: 3 天内"
	case *tg.UserStatusLastWeek:
		return "最后上线: 3-7 天前"
	case *tg.UserStatusLastMonth:
		return "最后上线: 7-30 天前"
	case *tg.UserStatusEmpty:
		return "最后上线: 超过 30 天"
	default:
		return ""
	}
}

func newCleanMemberHit(user *tg.User, detail string) cleanMemberHit {
	name := strings.TrimSpace(user.FirstName + " " + user.LastName)
	if name == "" {
		name = "未知"
	}
	return cleanMemberHit{
		id:       user.ID,
		username: user.Username,
		name:     name,
		detail:   detail,
	}
}

func formatCleanMemberHits(hits []cleanMemberHit) string {
	var b strings.Builder
	for i, hit := range hits {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%d. %s", i+1, hit.name)
		if hit.username != "" {
			fmt.Fprintf(&b, " (@%s)", hit.username)
		}
		fmt.Fprintf(&b, " | ID: %d", hit.id)
		if hit.detail != "" {
			fmt.Fprintf(&b, " | %s", hit.detail)
		}
	}
	return b.String()
}

// buildCleanMemberListCSV 生成命中用户列表文件内容
func buildCleanMemberListCSV(hits []cleanMemberHit) []byte {
	var buf bytes.Buffer
	buf.WriteString("\xEF\xBB\xBF") // UTF-8 BOM, 便于 Excel 识别中文
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"用户ID", "用户名", "姓名", "详情"})
	for _, hit := range hits {
		username := hit.username
		if username != "" {
			username = "@" + username
		}
		_ = w.Write([]string{strconv.FormatInt(hit.id, 10), username, hit.name, hit.detail})
	}
	w.Flush()
	return buf.Bytes()
}

func sendCleanMemberFile(ctx *ext.Context, chat types.EffectiveChat, hits []cleanMemberHit) error {
	fileName := "clean_member_" + time.Now().Format("20060102_1504") + ".csv"
	file, err := uploader.NewUploader(ctx.Raw).FromBytes(ctx, fileName, buildCleanMemberListCSV(hits))
	if err != nil {
		return err
	}
	_, err = ctx.Raw.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{
		Peer:     chat.GetInputPeer(),
		RandomID: rand.Int64(),
		Message:  fmt.Sprintf("共 %d 人", len(hits)),
		Media: &tg.InputMediaUploadedDocument{
			File:     file,
			MimeType: "text/csv",
			Attributes: []tg.DocumentAttributeClass{
				&tg.DocumentAttributeFilename{FileName: fileName},
			},
		},
	})
	return err
}

// handleCleanMemberExport 处理导出列表的指令, 返回该消息是否已被消费
func handleCleanMemberExport(ctx *ext.Context, u *ext.Update) (bool, error) {
	msg := u.EffectiveMessage
	chat := u.EffectiveChat()
	if msg == nil || chat.GetID() == 0 || msg.EditDate != 0 || msg.IsService {
		return false, nil
	}
	if strings.TrimSpace(msg.GetMessage()) != cleanMemberExportCmd {
		return false, nil
	}
	export := getCleanMemberExport(chat.GetID())
	if export == nil {
		return false, nil
	}
	if err := sendCleanMemberFile(ctx, chat, export.hits); err != nil {
		_ = editCleanMemberMessage(ctx, chat, export.promptID, "导出失败: "+err.Error())
		return true, err
	}
	deleteCleanMemberExport(chat.GetID())
	deleteCleanMemberMessage(ctx, chat, msg.GetID())
	return true, nil
}

func lastCleanMemberMessage(ctx *ext.Context, chat types.EffectiveChat, user *tg.User) (*tg.Message, error) {
	res, err := searchCleanMemberMessages(ctx, chat, user)
	if err != nil {
		return nil, err
	}
	for _, m := range cleanMemberMessages(res) {
		if msg, ok := m.(*tg.Message); ok {
			return msg, nil
		}
	}
	return nil, nil
}

func countCleanMemberMessages(ctx *ext.Context, chat types.EffectiveChat, user *tg.User) (int, error) {
	res, err := searchCleanMemberMessages(ctx, chat, user)
	if err != nil {
		return 0, err
	}
	switch r := res.(type) {
	case *tg.MessagesChannelMessages:
		return r.Count, nil
	case *tg.MessagesMessagesSlice:
		return r.Count, nil
	case *tg.MessagesMessages:
		return len(r.Messages), nil
	default:
		return 0, nil
	}
}

func searchCleanMemberMessages(ctx *ext.Context, chat types.EffectiveChat, user *tg.User) (tg.MessagesMessagesClass, error) {
	return ctx.Raw.MessagesSearch(ctx, &tg.MessagesSearchRequest{
		Peer:   chat.GetInputPeer(),
		Q:      "",
		Filter: &tg.InputMessagesFilterEmpty{},
		FromID: &tg.InputPeerUser{UserID: user.ID, AccessHash: user.AccessHash},
		Limit:  1,
	})
}

func cleanMemberMessages(res tg.MessagesMessagesClass) []tg.MessageClass {
	switch r := res.(type) {
	case *tg.MessagesChannelMessages:
		return r.Messages
	case *tg.MessagesMessagesSlice:
		return r.Messages
	case *tg.MessagesMessages:
		return r.Messages
	default:
		return nil
	}
}

// kickCleanMember 临时封禁(踢出)成员, 返回该成员是否确实被移出群组
func kickCleanMember(ctx *ext.Context, chat types.EffectiveChat, user *tg.User) (bool, error) {
	untilDate := int(time.Now().Add(cleanMemberKickDuration).Unix())
	for attempt := 0; ; attempt++ {
		var err error
		switch c := chat.(type) {
		case *types.Channel:
			_, err = ctx.Raw.ChannelsEditBanned(ctx, &tg.ChannelsEditBannedRequest{
				Channel:     c.GetInputChannel(),
				Participant: &tg.InputPeerUser{UserID: user.ID, AccessHash: user.AccessHash},
				BannedRights: tg.ChatBannedRights{
					UntilDate:    untilDate,
					ViewMessages: true,
					SendMessages: true,
					SendMedia:    true,
					SendStickers: true,
					SendGifs:     true,
					SendGames:    true,
					SendInline:   true,
					EmbedLinks:   true,
				},
			})
		case *types.Chat:
			_, err = ctx.Raw.MessagesDeleteChatUser(ctx, &tg.MessagesDeleteChatUserRequest{
				ChatID: c.GetID(),
				UserID: &tg.InputUser{UserID: user.ID, AccessHash: user.AccessHash},
			})
		default:
			return false, nil
		}
		if err == nil {
			return true, nil
		}
		if wait, ok := tgerr.AsFloodWait(err); ok && attempt < cleanMemberMaxFloodRetry {
			time.Sleep(wait + time.Duration(500+rand.IntN(500))*time.Millisecond)
			continue
		}
		if tgerr.IsCode(err, 400) {
			return false, nil
		}
		return false, err
	}
}

// forEachCleanMember 遍历群成员, 回调返回该成员是否已被移出群组
func forEachCleanMember(ctx *ext.Context, chat types.EffectiveChat, fn func(*tg.User) (bool, error)) error {
	switch c := chat.(type) {
	case *types.Channel:
		return forEachChannelCleanMember(ctx, c, fn)
	case *types.Chat:
		return forEachBasicChatCleanMember(ctx, c, fn)
	default:
		return fmt.Errorf("clean_member: unsupported chat type")
	}
}

func forEachChannelCleanMember(ctx *ext.Context, chat *types.Channel, fn func(*tg.User) (bool, error)) error {
	offset := 0
	for {
		res, err := ctx.Raw.ChannelsGetParticipants(ctx, &tg.ChannelsGetParticipantsRequest{
			Channel: chat.GetInputChannel(),
			Filter:  &tg.ChannelParticipantsSearch{Q: ""},
			Offset:  offset,
			Limit:   cleanMemberPageSize,
			Hash:    0,
		})
		if err != nil {
			return err
		}
		page, ok := res.(*tg.ChannelsChannelParticipants)
		if !ok || len(page.Participants) == 0 {
			return nil
		}
		users := make(map[int64]*tg.User, len(page.Users))
		for _, u := range page.Users {
			if user, ok := u.(*tg.User); ok {
				users[user.ID] = user
			}
		}
		removed := 0
		for _, p := range page.Participants {
			user := users[channelParticipantUserID(p)]
			if user == nil {
				continue
			}
			ok, err := fn(user)
			if err != nil {
				return err
			}
			if ok {
				removed++
			}
		}
		// 被移出的成员会从成员列表中消失, 偏移量需扣除这部分, 否则会跳过成员
		offset += len(page.Participants) - removed
	}
}

func forEachBasicChatCleanMember(ctx *ext.Context, chat *types.Chat, fn func(*tg.User) (bool, error)) error {
	res, err := ctx.Raw.MessagesGetFullChat(ctx, chat.GetID())
	if err != nil {
		return err
	}
	full, ok := res.FullChat.(*tg.ChatFull)
	if !ok {
		return nil
	}
	participants, ok := full.Participants.(*tg.ChatParticipants)
	if !ok {
		return nil
	}
	users := make(map[int64]*tg.User, len(res.Users))
	for _, u := range res.Users {
		if user, ok := u.(*tg.User); ok {
			users[user.ID] = user
		}
	}
	for _, p := range participants.Participants {
		var userID int64
		switch v := p.(type) {
		case *tg.ChatParticipant:
			userID = v.UserID
		case *tg.ChatParticipantAdmin:
			userID = v.UserID
		case *tg.ChatParticipantCreator:
			userID = v.UserID
		}
		user := users[userID]
		if user == nil {
			continue
		}
		if _, err := fn(user); err != nil {
			return err
		}
	}
	return nil
}

func channelParticipantUserID(p tg.ChannelParticipantClass) int64 {
	switch v := p.(type) {
	case *tg.ChannelParticipant:
		return v.UserID
	case *tg.ChannelParticipantSelf:
		return v.UserID
	case *tg.ChannelParticipantCreator:
		return v.UserID
	case *tg.ChannelParticipantAdmin:
		return v.UserID
	case *tg.ChannelParticipantBanned:
		if u, ok := v.Peer.(*tg.PeerUser); ok {
			return u.UserID
		}
	case *tg.ChannelParticipantLeft:
		if u, ok := v.Peer.(*tg.PeerUser); ok {
			return u.UserID
		}
	}
	return 0
}
