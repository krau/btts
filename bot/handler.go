package bot

import (
	"context"

	"github.com/charmbracelet/log"
	"github.com/duke-git/lancet/v2/slice"
	"github.com/gotd/td/tg"
	"github.com/krau/btts/config"

	"github.com/celestix/gotgproto/dispatcher/handlers"
	"github.com/celestix/gotgproto/dispatcher/handlers/filters"
	"github.com/celestix/gotgproto/ext"
	"github.com/celestix/gotgproto/types"
)

func CheckPermission(ctx *ext.Context, update *ext.Update) bool {
	userID := update.GetUserChat().GetID()
	if userID == bi.UserClient.TClient.Self.ID {
		return true
	}
	if slice.Contain(config.C.Admins, userID) {
		return true
	}
	return false
}

type commandHandler struct {
	handlerFunc func(ctx *ext.Context, update *ext.Update) error
	cmd         string
	help        string
}

var commandHandlers = []commandHandler{
	{StartHandler, "start", "开始使用"},
	{SearchHandler, "search", "搜索消息"},
	{ListHandler, "ls", "列出已索引聊天"},
	{AddHandler, "add", "添加聊天到索引"},
	{DelHandler, "del", "删除聊天索引"},
	{PubHandler, "pub", "将一个聊天设为公开"},
	{UnPubHandler, "unpub", "将一个聊天设为私有"},
	{WatchHandler, "watch", "监听一个聊天"},
	{UnWatchHandler, "unwatch", "取消监听一个聊天"},
	{WatchDelHandler, "watchdel", "监听一个聊天的删除事件"},
	{UnWatchDelHandler, "unwatchdel", "取消监听一个聊天的删除事件"},
	{DownloadHandler, "dl", "下载消息"},
	{AddSubHandler, "addsub", "添加子 bot"},
	{DelSubHandler, "delsub", "删除子 bot"},
	{ListSubHandler, "lssub", "列出子 bot"},
	{GenApiKeyHandler, "genapikey", "生成随机 api key"},
	{AddApiKeyHandler, "addapikey", "添加子 api key"},
	{DelApiKeyHandler, "delapikey", "删除子 api key"},
	{ListApiKeyHandler, "lsapikey", "列出子 api key"},
	{SetApiKeyHandler, "setapikey", "设置子 api key 作用域"},
	{StartHandler, "help", "帮助"},
}

func (b *Bot) RegisterHandlers(ctx context.Context) {
	disp := b.Client.Dispatcher
	for _, cmdHandler := range commandHandlers {
		disp.AddHandler(handlers.NewCommand(cmdHandler.cmd, cmdHandler.handlerFunc))
	}
	disp.AddHandler(handlers.NewCommand("syncpeers", SyncPeersHandler))
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
	disp.AddHandlerToGroup(handlers.NewInlineQuery(filters.InlineQuery.All, InlineQueryHandler), 1)

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
		var botCmds []tg.BotCommand
		for _, cmdHandler := range commandHandlers {
			botCmds = append(botCmds, tg.BotCommand{
				Command:     cmdHandler.cmd,
				Description: cmdHandler.help,
			})
		}
		if _, err = b.Client.API().BotsSetBotCommands(ctx, &tg.BotsSetBotCommandsRequest{
			Scope: &tg.BotCommandScopePeer{
				Peer: peer,
			},
			Commands: botCmds,
		}); err != nil {
			log.FromContext(ctx).Error("Failed to set bot commands", "error", err)
		}
	}
}
