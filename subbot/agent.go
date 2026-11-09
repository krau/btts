package subbot

import (
	"errors"
	"strconv"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/gotd/td/tg"
	"github.com/krau/btts/config"
	"github.com/krau/btts/database"
	aiagent "github.com/krau/btts/service/agent"
	"github.com/krau/btts/userclient"
	"github.com/krau/mygotg/dispatcher"
	"github.com/krau/mygotg/ext"
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

func replyAskText(ctx *ext.Context, update *ext.Update, messageID int, text string) {
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

func truncateAskAnswer(answer string) string {
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
	waitID := askWait(ctx, update)
	answer, err := svc.Answer(ctx, query, scope)
	if err != nil {
		log.FromContext(ctx).Errorf("Agent query failed: %v", err)
		replyAskText(ctx, update, waitID, "Search agent failed: "+err.Error())
		return dispatcher.EndGroups
	}
	replyAskText(ctx, update, waitID, truncateAskAnswer(answer))
	return dispatcher.EndGroups
}
