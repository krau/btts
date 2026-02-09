package bot

import (
	"fmt"

	"github.com/krau/btts/database"
	"github.com/krau/btts/utils"
	"github.com/krau/mygotg/dispatcher"
	"github.com/krau/mygotg/ext"
)

func PubHandler(ctx *ext.Context, update *ext.Update) error {
	if !CheckPermission(ctx, update) {
		return dispatcher.EndGroups
	}
	chatDB, err := utils.GetChatDBFromUpdateArgs(ctx, update)
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString(fmt.Sprintf("Usage: /pub <chat_id>\n%s", err.Error())), nil)
		return dispatcher.EndGroups
	}
	chatDB.Public = true
	if err := database.UpsertIndexChat(ctx, chatDB); err != nil {
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
	chatDB, err := utils.GetChatDBFromUpdateArgs(ctx, update)
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString(fmt.Sprintf("Usage: /unpub <chat_id>\n%s", err.Error())), nil)
		return dispatcher.EndGroups
	}
	chatDB.Public = false
	if err := database.UpsertIndexChat(ctx, chatDB); err != nil {
		ctx.Reply(update, ext.ReplyTextString("Failed to unpub chat"), nil)
		return dispatcher.EndGroups
	}
	ctx.Reply(update, ext.ReplyTextString("Unpub chat"), nil)
	return dispatcher.EndGroups
}
