package subbot

import (
	"errors"
	"strconv"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/krau/btts/config"
	"github.com/krau/btts/database"
	aiagent "github.com/krau/btts/service/agent"
	"github.com/krau/btts/userclient"
	"github.com/krau/btts/utils"
	"github.com/krau/mygotg/dispatcher"
	"github.com/krau/mygotg/ext"
	"github.com/rs/xid"
)

func agentUserID(update *ext.Update) int64 {
	if user := update.EffectiveUser(); user != nil {
		return user.GetID()
	}
	if user := update.GetUserChat(); user != nil {
		return user.GetID()
	}
	return 0
}

func agentScope(ctx *ext.Context, update *ext.Update) (aiagent.Scope, error) {
	sb, err := database.GetSubBot(ctx, ctx.Self.ID)
	if err != nil {
		return aiagent.Scope{}, err
	}
	userID := agentUserID(update)
	scope := aiagent.Scope{UserID: strconv.FormatInt(userID, 10)}
	admin := userID != 0 && (userID == userclient.GetUserClient().TClient.Self.ID || containsID(config.C.Admins, userID))
	currentChatID := int64(0)
	if channel := update.GetChannel(); channel != nil {
		currentChatID = channel.GetID()
	} else if chat := update.GetChat(); chat != nil {
		currentChatID = chat.GetID()
	}
	for _, chatID := range sb.ChatIDs {
		chat, err := database.GetIndexChat(ctx, chatID)
		if err != nil {
			continue
		}
		allow := admin || chat.Public
		if !allow && currentChatID != 0 && chatID == currentChatID {
			allow = true
		}
		if !allow && currentChatID == 0 && !admin {
			member, err := database.IsMemberInIndexChat(ctx, chatID, userID)
			if err == nil && member {
				allow = true
			}
		}
		if allow {
			scope.AllowedChatIDs = append(scope.AllowedChatIDs, chatID)
		}
	}
	if len(scope.AllowedChatIDs) == 0 {
		return scope, errors.New("no indexed chats available")
	}
	return scope, nil
}

func containsID(ids []int64, target int64) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

func askWait(ctx *ext.Context, update *ext.Update) int {
	msg, err := ctx.Reply(update, ext.ReplyTextString("🤔 正在分析搜索意图并检索，请稍候..."), nil)
	if err != nil {
		log.FromContext(ctx).Debugf("Send agent wait message: %v", err)
		return 0
	}
	if msg == nil {
		return 0
	}
	return msg.ID
}

func replyAskText(ctx *ext.Context, update *ext.Update, messageID int, text string) int {
	chatID := update.EffectiveChat().GetID()
	if messageID != 0 {
		if err := utils.EditRichMessage(ctx, chatID, messageID, text); err == nil {
			return messageID
		}
		_ = ctx.DeleteMessages(chatID, []int{messageID})
	}
	replyTo := 0
	if update.EffectiveMessage != nil {
		replyTo = update.EffectiveMessage.ID
	}
	if message, err := utils.SendRichMessage(ctx, chatID, replyTo, text); err == nil && message != nil {
		return message.ID
	}
	if message, err := ctx.Reply(update, ext.ReplyTextString(text), nil); err == nil && message != nil {
		return message.ID
	}
	return 0
}

func askAttachments(ctx *ext.Context, update *ext.Update) ([]aiagent.Attachment, error) {
	data, mime, err := utils.DownloadQuestionPhoto(ctx, update, 20<<20)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	return []aiagent.Attachment{{Data: data, MIME: mime}}, nil
}

func truncateAskAnswer(answer string) string {
	const maxRunes = 3900
	runes := []rune(answer)
	if len(runes) <= maxRunes {
		return answer
	}
	return string(runes[:maxRunes]) + "\n…（回答过长，已截断）"
}

func agentConversationID(ctx *ext.Context, update *ext.Update) string {
	msg := update.EffectiveMessage
	if msg != nil && msg.ReplyToMessage != nil {
		if conversationID, ok := utils.GetAgentConversation(ctx.Self.ID, update.EffectiveChat().GetID(), msg.ReplyToMessage.ID); ok {
			return conversationID
		}
	}
	return xid.New().String()
}

func handleAskQuery(ctx *ext.Context, update *ext.Update, query string) error {
	svc := aiagent.Default()
	if svc == nil {
		ctx.Reply(update, ext.ReplyTextString("Search agent is not enabled"), nil)
		return dispatcher.EndGroups
	}
	scope, err := agentScope(ctx, update)
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString("Search agent: "+err.Error()), nil)
		return dispatcher.EndGroups
	}
	scope.ConversationID = agentConversationID(ctx, update)

	waitID := askWait(ctx, update)
	attachments, err := askAttachments(ctx, update)
	if err != nil {
		replyAskText(ctx, update, waitID, "Failed to read attached image: "+err.Error())
		return dispatcher.EndGroups
	}
	answer, err := svc.AnswerWithAttachments(ctx, query, scope, attachments)
	if err != nil {
		log.FromContext(ctx).Errorf("Agent query failed: %v", err)
		replyAskText(ctx, update, waitID, "Search agent failed: "+err.Error())
		return dispatcher.EndGroups
	}
	resultID := replyAskText(ctx, update, waitID, truncateAskAnswer(answer))
	if resultID != 0 {
		utils.SetAgentConversation(ctx.Self.ID, update.EffectiveChat().GetID(), resultID, scope.ConversationID)
	}
	return dispatcher.EndGroups
}

func AskHandler(ctx *ext.Context, update *ext.Update) error {
	query := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(update.EffectiveMessage.GetMessage(), "/ask"), "@"+ctx.Self.Username))
	if query == "" {
		ctx.Reply(update, ext.ReplyTextString("Usage: /ask <question or search request>"), nil)
		return dispatcher.EndGroups
	}
	return handleAskQuery(ctx, update, query)
}

// AgentReplyHandler continues an agent conversation when the user replies to
// one of the bot's agent result messages.
func AgentReplyHandler(ctx *ext.Context, update *ext.Update) error {
	msg := update.EffectiveMessage
	if msg == nil || msg.ReplyToMessage == nil {
		return nil
	}
	if _, ok := utils.GetAgentConversation(ctx.Self.ID, update.EffectiveChat().GetID(), msg.ReplyToMessage.ID); !ok {
		return nil
	}
	query := strings.TrimSpace(msg.GetMessage())
	if query == "" || strings.HasPrefix(query, "/") {
		return nil
	}
	return handleAskQuery(ctx, update, query)
}
