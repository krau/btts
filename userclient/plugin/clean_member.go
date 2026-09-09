package plugin

import (
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/krau/mygotg/ext"
	"github.com/krau/mygotg/types"
)

const (
	cleanMemberMinDays       = 7               // 按天数清理时的最小天数
	cleanMemberKickDuration  = 5 * time.Minute // 临时封禁时长, 到期自动解封
	cleanMemberSessionTTL    = 5 * time.Minute // 交互会话超时时间
	cleanMemberPageSize      = 200             // 每次拉取成员的数量
	cleanMemberMaxFloodRetry = 3               // FloodWait 重试次数
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
	err := forEachCleanMember(ctx, chat, func(user *tg.User) (bool, error) {
		matched, err := shouldCleanMember(ctx, chat, user, session.mode, session.day)
		if err != nil {
			return false, err
		}
		if !matched {
			return false, nil
		}
		count++
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
	if onlySearch {
		report(fmt.Sprintf("查找到了 %d 人。", count))
	} else {
		report(fmt.Sprintf("成功清理了 %d 人。", count))
	}
}

func shouldCleanMember(ctx *ext.Context, chat types.EffectiveChat, user *tg.User, mode string, day int) (bool, error) {
	switch mode {
	case "1":
		status, ok := user.Status.(*tg.UserStatusOffline)
		if !ok {
			return false, nil
		}
		return time.Unix(int64(status.WasOnline), 0).Before(time.Now().AddDate(0, 0, -day)), nil
	case "2":
		if user.AccessHash == 0 {
			return false, nil
		}
		last, err := lastCleanMemberMessage(ctx, chat, user)
		if err != nil {
			if tgerr.Is(err, "PEER_ID_INVALID") {
				return false, nil
			}
			return false, err
		}
		if last == nil {
			return false, nil
		}
		return time.Unix(int64(last.Date), 0).Before(time.Now().AddDate(0, 0, -day)), nil
	case "3":
		if user.AccessHash == 0 {
			return false, nil
		}
		count, err := countCleanMemberMessages(ctx, chat, user)
		if err != nil {
			if tgerr.Is(err, "PEER_ID_INVALID") {
				return false, nil
			}
			return false, err
		}
		return count < day, nil
	case "4":
		return user.Deleted, nil
	case "5":
		return true, nil
	default:
		return false, nil
	}
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
