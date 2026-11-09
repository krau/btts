package bot

import (
	"errors"
	"strconv"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/gotd/td/tg"
	"github.com/krau/btts/database"
	"github.com/krau/btts/service/agent"
	"github.com/krau/mygotg/dispatcher"
	"github.com/krau/mygotg/ext"
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

func replyAgentText(ctx *ext.Context, update *ext.Update, messageID int, text string) {
	if messageID != 0 {
		if _, err := ctx.EditMessage(update.EffectiveChat().GetID(), &tg.MessagesEditMessageRequest{
			ID:      messageID,
			Message: text,
		}); err == nil {
			return
		}
		_ = ctx.DeleteMessages(update.EffectiveChat().GetID(), []int{messageID})
	}
	ctx.Reply(update, ext.ReplyTextString(text), nil)
}

func truncateAgentAnswer(answer string) string {
	const maxRunes = 3900
	runes := []rune(answer)
	if len(runes) <= maxRunes {
		return answer
	}
	return string(runes[:maxRunes]) + "\n…（回答过长，已截断）"
}

func AskHandler(ctx *ext.Context, update *ext.Update) error {
	query := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(update.EffectiveMessage.GetMessage(), "/ask"), "@"+ctx.Self.Username))
	if query == "" {
		ctx.Reply(update, ext.ReplyTextString("Usage: /ask <question or search request>"), nil)
		return dispatcher.EndGroups
	}
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
	waitID := agentWait(ctx, update)
	answer, err := svc.Answer(ctx, query, scope)
	if err != nil {
		log.FromContext(ctx).Errorf("Agent query failed: %v", err)
		replyAgentText(ctx, update, waitID, "Search agent failed: "+err.Error())
		return dispatcher.EndGroups
	}
	replyAgentText(ctx, update, waitID, truncateAgentAnswer(answer))
	return dispatcher.EndGroups
}
