package bot

import (
	"fmt"

	"github.com/krau/btts/database"
	"github.com/krau/btts/utils"
	"github.com/krau/mygotg/dispatcher"
	"github.com/krau/mygotg/ext"
)

func OcrHandler(ctx *ext.Context, update *ext.Update) error {
	if !CheckPermission(ctx, update) {
		return dispatcher.EndGroups
	}
	chatDB, err := utils.GetChatDBFromUpdateArgs(ctx, update)
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString(fmt.Sprintf("Usage: /ocrable <chat_id>\n%s", err.Error())), nil)
		return dispatcher.EndGroups
	}
	chatDB.NoOcr = false
	if err := database.UpsertIndexChat(ctx, chatDB); err != nil {
		ctx.Reply(update, ext.ReplyTextString("Failed to enable OCR"), nil)
		return dispatcher.EndGroups
	}
	ctx.Reply(update, ext.ReplyTextString("OCR enabled"), nil)
	return dispatcher.EndGroups
}

func UnOcrHandler(ctx *ext.Context, update *ext.Update) error {
	if !CheckPermission(ctx, update) {
		return dispatcher.EndGroups
	}
	chatDB, err := utils.GetChatDBFromUpdateArgs(ctx, update)
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString(fmt.Sprintf("Usage: /unocrable <chat_id>\n%s", err.Error())), nil)
		return dispatcher.EndGroups
	}
	chatDB.NoOcr = true
	if err := database.UpsertIndexChat(ctx, chatDB); err != nil {
		ctx.Reply(update, ext.ReplyTextString("Failed to disable OCR"), nil)
		return dispatcher.EndGroups
	}
	ctx.Reply(update, ext.ReplyTextString("OCR disabled"), nil)
	return dispatcher.EndGroups
}
