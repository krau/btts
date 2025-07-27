package bot

import (
	"strconv"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/krau/btts/database"
)

func DelHandler(ctx *ext.Context, update *ext.Update) error {
	if !CheckPermission(ctx, update) {
		return dispatcher.EndGroups
	}
	args := update.Args()
	if len(args) < 2 {
		ctx.Reply(update, ext.ReplyTextString("Usage: /del <chat_id>"), nil)
		return dispatcher.EndGroups
	}
	chatID, err := strconv.Atoi(args[1])
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString("Invalid chat ID"), nil)
		return dispatcher.EndGroups
	}
	if err := database.DeleteIndexChat(ctx, int64(chatID)); err != nil {
		log.FromContext(ctx).Error("Failed to delete chat", "chat_id", chatID, "error", err)
		ctx.Reply(update, ext.ReplyTextString("Failed to delete chat"), nil)
		return dispatcher.EndGroups
	}
	if err := bi.Engine.DeleteIndex(ctx, int64(chatID)); err != nil {
		log.FromContext(ctx).Error("Failed to delete index", "chat_id", chatID, "error", err)
		ctx.Reply(update, ext.ReplyTextString("Failed to delete index"), nil)
		return dispatcher.EndGroups
	}
	ctx.Reply(update, ext.ReplyTextString("Deleted chat and index"), nil)
	return dispatcher.EndGroups
}
