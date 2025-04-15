package bot

import (
	"fmt"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/krau/btts/database"
	"github.com/krau/btts/utils"
)

func WatchHandler(ctx *ext.Context, update *ext.Update) error {
	if !CheckPermission(ctx, update) {
		return dispatcher.EndGroups
	}
	chatDB, err := utils.GetChatDBFromUpdateArgs(ctx, update)
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString(fmt.Sprintf("Usage: /watch <chat_id>\n%s", err.Error())), nil)
		return dispatcher.EndGroups
	}
	chatDB.Watching = true
	if err := database.UpsertIndexChat(ctx, chatDB); err != nil {
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
	chatDB, err := utils.GetChatDBFromUpdateArgs(ctx, update)
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString(fmt.Sprintf("Usage: /unwatch <chat_id>\n%s", err.Error())), nil)
		return dispatcher.EndGroups
	}
	chatDB.Watching = false
	if err := database.UpsertIndexChat(ctx, chatDB); err != nil {
		ctx.Reply(update, ext.ReplyTextString("Failed to unwatch chat"), nil)
		return dispatcher.EndGroups
	}
	ctx.Reply(update, ext.ReplyTextString("Unwatched chat"), nil)
	return dispatcher.EndGroups
}

func WatchDelHandler(ctx *ext.Context, update *ext.Update) error {
	if !CheckPermission(ctx, update) {
		return dispatcher.EndGroups
	}
	chatDB, err := utils.GetChatDBFromUpdateArgs(ctx, update)
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString(fmt.Sprintf("Usage: /watchdel <chat_id>\n%s", err.Error())), nil)
		return dispatcher.EndGroups
	}
	chatDB.NoDelete = false
	if err := database.UpsertIndexChat(ctx, chatDB); err != nil {
		log.FromContext(ctx).Error("Failed to update chat", "error", err)
		ctx.Reply(update, ext.ReplyTextString("Failed to update chat"), nil)
		return dispatcher.EndGroups
	}
	ctx.Reply(update, ext.ReplyTextString("Watched chat delete event"), nil)
	return dispatcher.EndGroups
}

func UnWatchDelHandler(ctx *ext.Context, update *ext.Update) error {
	if !CheckPermission(ctx, update) {
		return dispatcher.EndGroups
	}
	chatDB, err := utils.GetChatDBFromUpdateArgs(ctx, update)
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString(fmt.Sprintf("Usage: /unwatchdel <chat_id>\n%s", err.Error())), nil)
		return dispatcher.EndGroups
	}
	chatDB.NoDelete = true
	if err := database.UpsertIndexChat(ctx, chatDB); err != nil {
		log.FromContext(ctx).Error("Failed to update chat", "error", err)
		ctx.Reply(update, ext.ReplyTextString("Failed to update chat"), nil)
		return dispatcher.EndGroups
	}
	ctx.Reply(update, ext.ReplyTextString("Unwatched chat delete event"), nil)
	return dispatcher.EndGroups
}
