package bot

import (
	"strconv"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/krau/btts/database"
)

func WatchHandler(ctx *ext.Context, update *ext.Update) error {
	if !CheckPermission(ctx, update) {
		return dispatcher.EndGroups
	}
	args := update.Args()
	if len(args) < 2 {
		ctx.Reply(update, ext.ReplyTextString("Usage: /watch <chat_id>"), nil)
		return dispatcher.EndGroups
	}
	chatID, err := strconv.Atoi(args[1])
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString("Invalid chat ID"), nil)
		return dispatcher.EndGroups
	}
	if err := database.WatchIndexChat(ctx, int64(chatID)); err != nil {
		ctx.Reply(update, ext.ReplyTextString("Failed to watch chat"), nil)
		return dispatcher.EndGroups
	}
	ctx.Reply(update, ext.ReplyTextString("Watching chat"), nil)
	return dispatcher.EndGroups
}


func UnWatchHandler(ctx *ext.Context, update *ext.Update) error {
	if !CheckPermission(ctx, update) {
		return dispatcher.EndGroups
	}
	args := update.Args()
	if len(args) < 2 {
		ctx.Reply(update, ext.ReplyTextString("Usage: /unwatch <chat_id>"), nil)
		return dispatcher.EndGroups
	}
	chatID, err := strconv.Atoi(args[1])
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString("Invalid chat ID"), nil)
		return dispatcher.EndGroups
	}
	if err := database.UnwatchIndexChat(ctx, int64(chatID)); err != nil {
		ctx.Reply(update, ext.ReplyTextString("Failed to unwatch chat"), nil)
		return dispatcher.EndGroups
	}
	ctx.Reply(update, ext.ReplyTextString("Unwatched chat"), nil)
	return dispatcher.EndGroups
}