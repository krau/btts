package bot

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/gotd/td/tg"
	"github.com/krau/btts/database"
	"github.com/krau/btts/utils"
	"gorm.io/gorm"
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

	chatArg := args[1]
	chatID, err := strconv.ParseInt(chatArg, 10, 64)
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString("Invalid chat ID"), nil)
		return dispatcher.EndGroups
	}

	logger := log.FromContext(ctx)

	// 尝试从数据库获取聊天
	chatDB, err := database.GetIndexChat(ctx, chatID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// 聊天不存在，自动创建
		logger.Infof("Chat %d not found in database, creating new index", chatID)

		utclient := bi.UserClient.TClient
		inputPeer := utclient.PeerStorage.GetInputPeerById(chatID)
		if inputPeer == nil {
			// 尝试通过 username 解析
			effChat, err := utclient.CreateContext().ResolveUsername(strings.TrimPrefix(chatArg, "@"))
			if err != nil {
				ctx.Reply(update, ext.ReplyTextString("Chat not found: "+err.Error()), nil)
				return dispatcher.EndGroups
			}
			inputPeer = effChat.GetInputPeer()
			chatID = effChat.GetID()
		}

		if inputPeer == nil {
			ctx.Reply(update, ext.ReplyTextString("Chat not found"), nil)
			return dispatcher.EndGroups
		}

		// 创建索引
		if err := bi.Engine.CreateIndex(ctx, chatID); err != nil {
			ctx.Reply(update, ext.ReplyTextString("Failed to create index: "+err.Error()), nil)
			return dispatcher.EndGroups
		}

		// 创建数据库记录
		chatDB = &database.IndexChat{
			ChatID:   chatID,
			Watching: true,
			Public:   false,
		}

		// 确定聊天类型
		switch inputPeer.(type) {
		case *tg.InputPeerChannel:
			chatDB.Type = int(database.ChatTypeChannel)
		case *tg.InputPeerUser:
			chatDB.Type = int(database.ChatTypePrivate)
		default:
			logger.Warnf("Unsupported chat type: %T", inputPeer)
			// 清理已创建的索引
			if err := bi.Engine.DeleteIndex(ctx, chatID); err != nil {
				logger.Errorf("Failed to delete index: %v", err)
			}
			ctx.Reply(update, ext.ReplyTextString("Unsupported chat type"), nil)
			return dispatcher.EndGroups
		}

		if err := database.UpsertIndexChat(ctx, chatDB); err != nil {
			logger.Errorf("Failed to create index chat: %v", err)
			// 清理已创建的索引
			if err := bi.Engine.DeleteIndex(ctx, chatID); err != nil {
				logger.Errorf("Failed to delete index: %v", err)
			}
			ctx.Reply(update, ext.ReplyTextString("Failed to create chat index"), nil)
			return dispatcher.EndGroups
		}

		ctx.Reply(update, ext.ReplyTextString("Chat index created and watching enabled"), nil)
		return dispatcher.EndGroups
	} else if err != nil {
		logger.Errorf("Failed to get index chat: %v", err)
		ctx.Reply(update, ext.ReplyTextString("Failed to get chat from database"), nil)
		return dispatcher.EndGroups
	}

	// 聊天已存在，只更新 Watching 状态
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
