package bot

import (
	"fmt"

	"github.com/krau/btts/database"
	"github.com/krau/btts/utils"
	"github.com/krau/mygotg/dispatcher"
	"github.com/krau/mygotg/ext"
)

func EnrichEnableHandler(ctx *ext.Context, update *ext.Update) error {
	if !CheckPermission(ctx, update) {
		return dispatcher.EndGroups
	}
	chatDB, err := utils.GetChatDBFromUpdateArgs(ctx, update)
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString(fmt.Sprintf("Usage: /enrichable <chat_id>\n%s", err.Error())), nil)
		return dispatcher.EndGroups
	}
	chatDB.NoEnrich = false
	if err := database.UpsertIndexChat(ctx, chatDB); err != nil {
		ctx.Reply(update, ext.ReplyTextString("Failed to enable enrichment"), nil)
		return dispatcher.EndGroups
	}
	ctx.Reply(update, ext.ReplyTextString("Media enrichment enabled for chat"), nil)
	return dispatcher.EndGroups
}

func EnrichDisableHandler(ctx *ext.Context, update *ext.Update) error {
	if !CheckPermission(ctx, update) {
		return dispatcher.EndGroups
	}
	chatDB, err := utils.GetChatDBFromUpdateArgs(ctx, update)
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString(fmt.Sprintf("Usage: /unenrichable <chat_id>\n%s", err.Error())), nil)
		return dispatcher.EndGroups
	}
	chatDB.NoEnrich = true
	if err := database.UpsertIndexChat(ctx, chatDB); err != nil {
		ctx.Reply(update, ext.ReplyTextString("Failed to disable enrichment"), nil)
		return dispatcher.EndGroups
	}
	ctx.Reply(update, ext.ReplyTextString("Media enrichment disabled for chat"), nil)
	return dispatcher.EndGroups
}
