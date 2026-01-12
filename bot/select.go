package bot

import (
	"fmt"
	"strconv"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/gotd/td/tg"
	"github.com/krau/btts/database"
	"github.com/krau/btts/utils/cache"
)

func SelectCallbackHandler(ctx *ext.Context, update *ext.Update) error {
	args := update.Args()
	chatID, err := strconv.Atoi(args[1])
	if err != nil {
		ctx.AnswerCallback(&tg.MessagesSetBotCallbackAnswerRequest{
			Alert:   true,
			QueryID: update.CallbackQuery.GetQueryID(),
			Message: "Invalid chat ID",
		})
		return dispatcher.EndGroups
	}
	chatDB, err := database.GetIndexChat(ctx, int64(chatID))
	if err != nil {
		ctx.AnswerCallback(&tg.MessagesSetBotCallbackAnswerRequest{
			Alert:   true,
			QueryID: update.CallbackQuery.GetQueryID(),
			Message: "Failed to get chat",
		})
		return dispatcher.EndGroups
	}
	cahceKey := strconv.Itoa(update.CallbackQuery.GetMsgID())
	log.FromContext(ctx).Debug("Cache key", "key", cahceKey)
	if err := cache.Set(cahceKey, int64(chatID), cache.DefaultTTL); err != nil {
		ctx.AnswerCallback(&tg.MessagesSetBotCallbackAnswerRequest{
			Alert:   true,
			QueryID: update.CallbackQuery.GetQueryID(),
			Message: "Failed to set cache",
		})
		return dispatcher.EndGroups
	}
	ctx.EditMessage(update.EffectiveChat().GetID(), &tg.MessagesEditMessageRequest{
		ID:      update.CallbackQuery.MsgID,
		Message: fmt.Sprintf("回复该消息以搜索聊天 %s", chatDB.Title),
	})
	return dispatcher.EndGroups
}
