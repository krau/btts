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
	"github.com/celestix/gotgproto/types"
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
	disp.AddHandler(handlers.NewCommand("addsub", AddSubHandler))
	disp.AddHandler(handlers.NewCommand("delsub", DelSubHandler))
	disp.AddHandler(handlers.NewCommand("lssub", ListSubHandler))
	disp.AddHandler(handlers.NewCallbackQuery(filters.CallbackQuery.Prefix("search"), SearchCallbackHandler))
	disp.AddHandler(handlers.NewCallbackQuery(filters.CallbackQuery.Prefix("filter"), FilterCallbackHandler))
	disp.AddHandler(handlers.NewCallbackQuery(filters.CallbackQuery.Prefix("select"), SelectCallbackHandler))
	disp.AddHandler(handlers.NewMessage(filters.Message.ChatType(filters.ChatTypeUser), SearchHandler))
	disp.AddHandler(handlers.NewMessage(func(m *types.Message) bool {
		if m == nil || m.ReplyToMessage == nil || m.ReplyToMessage.FromID == nil {
			return false
		}
		peer := m.ReplyToMessage.FromID
		switch p := peer.(type) {
		case *tg.PeerUser:
			return p.GetUserID() == b.Client.Self.ID
		default:
			return false
		}
	}, SearchHandler))

	_, err := b.Client.API().BotsSetBotCommands(ctx, &tg.BotsSetBotCommandsRequest{
		Scope: &tg.BotCommandScopeDefault{},
		Commands: []tg.BotCommand{
			{Command: "search", Description: "搜索消息"},
			{Command: "ls", Description: "列出已索引聊天"},
			{Command: "start", Description: "开始使用"},
			{Command: "help", Description: "帮助"},
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
				{Command: "search", Description: "搜索消息"},
				{Command: "ls", Description: "列出已索引聊天"},
				{Command: "start", Description: "开始使用"},
				{Command: "help", Description: "帮助"},
				{Command: "add", Description: "添加聊天到索引"},
				{Command: "del", Description: "删除聊天索引"},
				{Command: "pub", Description: "将一个聊天设为公开"},
				{Command: "unpub", Description: "将一个聊天设为私有"},
				{Command: "watch", Description: "监听一个聊天"},
				{Command: "unwatch", Description: "取消监听一个聊天"},
				{Command: "watchdel", Description: "监听一个聊天的删除事件"},
				{Command: "unwatchdel", Description: "取消监听一个聊天的删除事件"},
				{Command: "addsub", Description: "添加子 bot"},
				{Command: "delsub", Description: "删除子 bot"},
				{Command: "lssub", Description: "列出子 bot"},
				{Command: "dl", Description: "下载消息"},
			},
		}); err != nil {
			log.FromContext(ctx).Error("Failed to set bot commands", "error", err)
		}
	}
}
