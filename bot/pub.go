package bot

import (
	"strconv"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/krau/btts/database"
)

func PubHandler(ctx *ext.Context, update *ext.Update) error {
	if !CheckPermission(ctx, update) {
		return dispatcher.EndGroups
	}
	args := update.Args()
	if len(args) < 2 {
		ctx.Reply(update, ext.ReplyTextString("Usage: /pub <chat_id>"), nil)
		return dispatcher.EndGroups
	}
	chatID, err := strconv.Atoi(args[1])
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString("Invalid chat ID"), nil)
		return dispatcher.EndGroups
	}
	if err := database.UpdateIndexChatPublic(ctx, int64(chatID), true); err != nil {
		ctx.Reply(update, ext.ReplyTextString("Failed to pub chat"), nil)
		return dispatcher.EndGroups
	}
	ctx.Reply(update, ext.ReplyTextString("Pub chat"), nil)
	return dispatcher.EndGroups
}

func UnPubHandler(ctx *ext.Context, update *ext.Update) error {
	if !CheckPermission(ctx, update) {
		return dispatcher.EndGroups
	}
	args := update.Args()
	if len(args) < 2 {
		ctx.Reply(update, ext.ReplyTextString("Usage: /unpub <chat_id>"), nil)
		return dispatcher.EndGroups
	}
	chatID, err := strconv.Atoi(args[1])
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString("Invalid chat ID"), nil)
		return dispatcher.EndGroups
	}
	if err := database.UpdateIndexChatPublic(ctx, int64(chatID), false); err != nil {
		ctx.Reply(update, ext.ReplyTextString("Failed to unpub chat"), nil)
		return dispatcher.EndGroups
	}
	ctx.Reply(update, ext.ReplyTextString("Unpub chat"), nil)
	return dispatcher.EndGroups
}
