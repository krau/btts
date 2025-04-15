package bot

import (
	"context"

	"github.com/charmbracelet/log"
	"github.com/duke-git/lancet/v2/slice"
	"github.com/gotd/td/tg"
	"github.com/krau/btts/config"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/dispatcher/handlers"
	"github.com/celestix/gotgproto/dispatcher/handlers/filters"
	"github.com/celestix/gotgproto/ext"
)

func CheckPermission(ctx *ext.Context, update *ext.Update) bool {
	userID := update.GetUserChat().GetID()
	if userID == BotInstance.UserClient.TClient.Self.ID {
		return true
	}
	if !slice.Contain(config.C.Admins, userID) {
		return false
	}
	return true
}

func CheckPermissionsHandler(ctx *ext.Context, update *ext.Update) error {
	userID := update.GetUserChat().GetID()
	if userID == BotInstance.UserClient.TClient.Self.ID {
		return dispatcher.ContinueGroups
	}
	if !slice.Contain(config.C.Admins, userID) {
		return dispatcher.EndGroups
	}
	return dispatcher.ContinueGroups
}

func (b *Bot) RegisterHandlers(ctx context.Context) {
	disp := b.Client.Dispatcher
	disp.AddHandler(handlers.NewCommand("start", StartHandler))
	disp.AddHandler(handlers.NewCommand("help", StartHandler))
	disp.AddHandler(handlers.NewCommand("search", SearchHandler))
	disp.AddHandler(handlers.NewCommand("add", AddHandler))
	disp.AddHandler(handlers.NewCommand("del", DelHandler))
	disp.AddHandler(handlers.NewCommand("pub", PubHandler))
	disp.AddHandler(handlers.NewCommand("unpub", UnPubHandler))
	disp.AddHandler(handlers.NewCommand("watch", WatchHandler))
	disp.AddHandler(handlers.NewCommand("unwatch", UnWatchHandler))
	disp.AddHandler(handlers.NewCommand("watchdel", WatchDelHandler))
	disp.AddHandler(handlers.NewCommand("unwatchdel", UnWatchDelHandler))
	disp.AddHandler(handlers.NewCommand("ls", ListHandler))
	disp.AddHandler(handlers.NewCommand("dl", DownloadHandler))
	disp.AddHandler(handlers.NewCallbackQuery(filters.CallbackQuery.Prefix("search"), SearchCallbackHandler))
	disp.AddHandler(handlers.NewCallbackQuery(filters.CallbackQuery.Prefix("filter"), FilterCallbackHandler))
	disp.AddHandler(handlers.NewMessage(filters.Message.ChatType(filters.ChatTypeUser), SearchHandler))

	_, err := b.Client.API().BotsSetBotCommands(ctx, &tg.BotsSetBotCommandsRequest{
		Scope: &tg.BotCommandScopeDefault{},
		Commands: []tg.BotCommand{
			{Command: "start", Description: "Start the bot"},
			{Command: "help", Description: "Help"},
			{Command: "search", Description: "Search for a message"},
		},
	})
	if err != nil {
		log.FromContext(ctx).Error("Failed to set bot commands", "error", err)
	}
	if peer := b.Client.PeerStorage.GetInputPeerById(b.UserClient.TClient.Self.ID); peer != nil {
		if _, err = b.Client.API().BotsSetBotCommands(ctx, &tg.BotsSetBotCommandsRequest{
			Scope: &tg.BotCommandScopePeer{
				Peer: peer,
			},
			Commands: []tg.BotCommand{
				{Command: "ls", Description: "List all watched chats"},
				{Command: "add", Description: "Add a message to the index"},
				{Command: "del", Description: "Delete a message from the index"},
				{Command: "pub", Description: "Publish a message to the index"},
				{Command: "unpub", Description: "Unpublish a message from the index"},
				{Command: "watch", Description: "Watch a chat"},
				{Command: "unwatch", Description: "Unwatch a chat"},
				{Command: "watchdel", Description: "Delete a watched chat delete event"},
				{Command: "unwatchdel", Description: "Unwatch a chat delete event"},
			},
		}); err != nil {
			log.FromContext(ctx).Error("Failed to set bot commands", "error", err)
		}
	}
}
