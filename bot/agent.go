package bot

import (
	"errors"
	"strconv"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/krau/btts/database"
	"github.com/krau/btts/service/agent"
	"github.com/krau/btts/utils"
	"github.com/krau/mygotg/dispatcher"
	"github.com/krau/mygotg/ext"
	"github.com/rs/xid"
)

func agentUserID(update *ext.Update) int64 {
	if user := update.GetUserChat(); user != nil {
		return user.GetID()
	}
	if user := update.EffectiveUser(); user != nil {
		return user.GetID()
	}
	return 0
}

func resolveAgentScope(ctx *ext.Context, update *ext.Update) (agent.Scope, error) {
	userID := agentUserID(update)
	scope := agent.Scope{UserID: strconv.FormatInt(userID, 10)}

	// A group/channel query is intentionally limited to the current chat.
	if channel := update.GetChannel(); channel != nil {
		chatID := channel.GetID()
		if _, err := database.GetIndexChat(ctx, chatID); err != nil {
			return scope, errors.New("this chat is not indexed")
		}
		scope.AllowedChatIDs = []int64{chatID}
		scope.CurrentChatID = chatID
		return scope, nil
	}
	if chat := update.GetChat(); chat != nil {
		chatID := chat.GetID()
		if _, err := database.GetIndexChat(ctx, chatID); err != nil {
			return scope, errors.New("this chat is not indexed")
		}
		scope.AllowedChatIDs = []int64{chatID}
		scope.CurrentChatID = chatID
		return scope, nil
	}

	all, err := database.GetAllIndexChats(ctx)
	if err != nil {
		return scope, err
	}
	if CheckPermission(ctx, update) {
		for _, chat := range all {
			scope.AllowedChatIDs = append(scope.AllowedChatIDs, chat.ChatID)
		}
		return scope, nil
	}

	// Non-admin private query: public chats plus private chats the user belongs to.
	for _, chat := range all {
		if chat.Public {
			scope.AllowedChatIDs = append(scope.AllowedChatIDs, chat.ChatID)
			continue
		}
		member, err := database.IsMemberInIndexChat(ctx, chat.ChatID, userID)
		if err != nil {
			log.FromContext(ctx).Warnf("Check agent membership for chat %d: %v", chat.ChatID, err)
			continue
		}
		if member {
			scope.AllowedChatIDs = append(scope.AllowedChatIDs, chat.ChatID)
		}
	}
	return scope, nil
}

func agentWait(ctx *ext.Context, update *ext.Update) int {
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

func replyAgentText(ctx *ext.Context, update *ext.Update, messageID int, text string) int {
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

func agentAttachments(ctx *ext.Context, update *ext.Update) ([]agent.Attachment, error) {
	data, mime, err := utils.DownloadQuestionPhoto(ctx, update, 20<<20)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	return []agent.Attachment{{Data: data, MIME: mime}}, nil
}

func truncateAgentAnswer(answer string) string {
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

func handleAgentQuery(ctx *ext.Context, update *ext.Update, query string) error {
	svc := agent.Default()
	if svc == nil {
		ctx.Reply(update, ext.ReplyTextString("Search agent is not enabled"), nil)
		return dispatcher.EndGroups
	}
	scope, err := resolveAgentScope(ctx, update)
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString("Search agent: "+err.Error()), nil)
		return dispatcher.EndGroups
	}
	if len(scope.AllowedChatIDs) == 0 {
		ctx.Reply(update, ext.ReplyTextString("No indexed chats available for the agent"), nil)
		return dispatcher.EndGroups
	}
	scope.ConversationID = agentConversationID(ctx, update)

	waitID := agentWait(ctx, update)
	attachments, err := agentAttachments(ctx, update)
	if err != nil {
		replyAgentText(ctx, update, waitID, "Failed to read attached image: "+err.Error())
		return dispatcher.EndGroups
	}
	answer, err := svc.AnswerWithAttachments(ctx, query, scope, attachments)
	if err != nil {
		log.FromContext(ctx).Errorf("Agent query failed: %v", err)
		replyAgentText(ctx, update, waitID, "Search agent failed: "+err.Error())
		return dispatcher.EndGroups
	}
	resultID := replyAgentText(ctx, update, waitID, truncateAgentAnswer(answer))
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
	return handleAgentQuery(ctx, update, query)
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
	return handleAgentQuery(ctx, update, query)
}
