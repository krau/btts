package userclient

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/celestix/gotgproto/storage"
	"github.com/charmbracelet/log"
	"github.com/gotd/td/telegram/takeout"
	"github.com/gotd/td/tg"
	"github.com/krau/btts/database"
	"github.com/krau/btts/engine"
)

// TakeoutProgressCallback 进度回调接口
type TakeoutProgressCallback func(stage string, current, total int, message string)

type TakeoutConfig struct {
	MessageUsers      bool
	MessageChats      bool
	MessageMegagroups bool
	MessageChannels   bool
}

// TakeoutExport 使用 Takeout API 导出所有聊天的消息到索引
func (u *UserClient) TakeoutExport(ctx context.Context, enableWatching bool, cfg TakeoutConfig, progressCallback TakeoutProgressCallback) error {
	logger := log.FromContext(ctx)
	// 1. 初始化 Takeout session
	if progressCallback != nil {
		progressCallback("init", 0, 1, "Initializing Takeout session...")
	}

	// 使用 takeout.Run 自动管理 takeout session 生命周期
	tcfg := takeout.Config{
		Contacts:          false,                 // 不导出联系人
		MessageUsers:      cfg.MessageUsers,      // 导出私聊消息
		MessageChats:      cfg.MessageChats,      // 导出群组消息
		MessageMegagroups: cfg.MessageMegagroups, // 导出超级群消息
		MessageChannels:   cfg.MessageChannels,   // 导出频道消息
		Files:             false,                 // 不下载文件（可选）
	}

	err := takeout.Run(ctx, u.TClient.API().Invoker(), tcfg, func(ctx context.Context, client *takeout.Client) error {
		logger.Info("Takeout session initialized", "takeout_id", client.ID())

		// 2. 获取所有对话
		if progressCallback != nil {
			progressCallback("dialogs", 0, 1, "Fetching dialogs...")
		}

		dialogs, err := u.getAllDialogs(ctx, client, progressCallback)
		if err != nil {
			return fmt.Errorf("failed to get dialogs: %w", err)
		}

		exportDialogs, err := u.filterDialogsByTakeoutConfig(ctx, client, dialogs, cfg)
		if err != nil {
			return fmt.Errorf("failed to filter dialogs: %w", err)
		}

		logger.Info("Dialogs ready to export", "all", len(dialogs), "export", len(exportDialogs))
		if progressCallback != nil {
			// 解决 dialogs 阶段 total 恒为 1：split ranges 常常只有 1 个，改为在 dialogs 完成后用实际对话数刷新。
			progressCallback("dialogs", len(exportDialogs), len(exportDialogs),
				fmt.Sprintf("Dialogs ready: %d (filtered from %d)", len(exportDialogs), len(dialogs)))
			// 提前设置导出阶段 total，避免 UI 长时间停留在 1。
			progressCallback("export", 0, len(exportDialogs), "Starting export...")
		}

		// 3. 导出每个对话的消息
		totalMessages := 0
		successChats := 0
		failedChats := 0

		for i, dialog := range exportDialogs {
			chatID := u.getPeerID(dialog.Peer)
			if chatID == 0 {
				continue
			}

			chatTitle := u.getChatTitle(ctx, dialog)
			if progressCallback != nil {
				progressCallback("export", i+1, len(exportDialogs), fmt.Sprintf("Exporting: %s", chatTitle))
			}

			logger.Info("Exporting chat",
				"progress", fmt.Sprintf("%d/%d", i+1, len(exportDialogs)),
				"chat_id", chatID,
				"title", chatTitle,
			)

			messageCount, err := u.exportChatHistory(ctx, client, dialog, chatID, enableWatching)
			if err != nil {
				logger.Error("Failed to export chat", "chat_id", chatID, "error", err)
				failedChats++
				continue
			}

			totalMessages += messageCount
			successChats++

			// 避免请求过快
			time.Sleep(200 * time.Millisecond)
		}

		if progressCallback != nil {
			progressCallback("complete", len(exportDialogs), len(exportDialogs),
				fmt.Sprintf("Completed: %d messages from %d chats", totalMessages, successChats))
		}

		logger.Info("Takeout export completed",
			"total_messages", totalMessages,
			"success_chats", successChats,
			"failed_chats", failedChats,
		)

		return nil
	})

	if err != nil {
		return fmt.Errorf("takeout export failed: %w", err)
	}

	return nil
}

func (u *UserClient) filterDialogsByTakeoutConfig(
	ctx context.Context,
	client *takeout.Client,
	dialogs []*tg.Dialog,
	cfg TakeoutConfig,
) ([]*tg.Dialog, error) {
	logger := log.FromContext(ctx)
	if len(dialogs) == 0 {
		return dialogs, nil
	}

	// 缓存 channelID -> isMegagroup
	channelKindCache := make(map[int64]bool)

	filtered := make([]*tg.Dialog, 0, len(dialogs))
	api := tg.NewClient(client)
	for _, d := range dialogs {
		if d == nil || d.Peer == nil {
			continue
		}

		switch p := d.Peer.(type) {
		case *tg.PeerUser:
			if cfg.MessageUsers {
				filtered = append(filtered, d)
			}
		case *tg.PeerChat:
			if cfg.MessageChats {
				filtered = append(filtered, d)
			}
		case *tg.PeerChannel:
			// 同时禁用频道与超级群：直接跳过，无需额外请求。
			if !cfg.MessageChannels && !cfg.MessageMegagroups {
				continue
			}
			// 同时启用：直接允许，无需区分。
			if cfg.MessageChannels && cfg.MessageMegagroups {
				filtered = append(filtered, d)
				continue
			}

			isMega, ok := channelKindCache[p.ChannelID]
			if !ok {
				var err error
				isMega, err = u.isMegagroup(ctx, api, p.ChannelID)
				if err != nil {
					// 分类失败时，保守处理：只要用户禁用了其中任一类，就不导出 PeerChannel。
					logger.Warn("Failed to classify peer channel, skipping due to scope", "channel_id", p.ChannelID, "error", err)
					continue
				}
				channelKindCache[p.ChannelID] = isMega
			}

			if isMega {
				if cfg.MessageMegagroups {
					filtered = append(filtered, d)
				}
				continue
			}
			if cfg.MessageChannels {
				filtered = append(filtered, d)
			}
		default:
			// 未知 peer 类型，跳过
		}
	}

	return filtered, nil
}

func (u *UserClient) isMegagroup(ctx context.Context, client *tg.Client, channelID int64) (bool, error) {
	peer := u.TClient.PeerStorage.GetInputPeerById(channelID)
	ipc, ok := peer.(*tg.InputPeerChannel)
	if !ok || ipc.AccessHash == 0 {
		return false, fmt.Errorf("missing access hash for channel %d", channelID)
	}
	chatsClass, err := client.ChannelsGetChannels(ctx, []tg.InputChannelClass{
		&tg.InputChannel{ChannelID: channelID, AccessHash: ipc.AccessHash},
	})
	if err != nil {
		return false, err
	}

	var chats []tg.ChatClass
	switch c := chatsClass.(type) {
	case *tg.MessagesChats:
		chats = c.Chats
	case *tg.MessagesChatsSlice:
		chats = c.Chats
	default:
		return false, fmt.Errorf("unexpected chats type %T", chatsClass)
	}

	for _, chatClass := range chats {
		if ch, ok := chatClass.(*tg.Channel); ok && ch.ID == channelID {
			return ch.Megagroup, nil
		}
	}

	return false, fmt.Errorf("channel %d not found in response", channelID)
}

// getSplitRanges 获取消息范围分片
func (u *UserClient) getSplitRanges(ctx context.Context, client *takeout.Client) ([]tg.MessageRange, error) {
	api := tg.NewClient(client)
	ranges, err := api.MessagesGetSplitRanges(ctx)
	if err != nil {
		return nil, err
	}

	return ranges, nil
}

// getChatTitle 从 Dialog 获取聊天标题（用于日志显示）
func (u *UserClient) getChatTitle(ctx context.Context, dialog *tg.Dialog) string {
	// 这里只是临时获取标题用于显示，实际元数据从消息响应中获取
	chatID := u.getPeerID(dialog.Peer)
	chat, err := database.GetIndexChat(ctx, chatID)
	if err == nil && chat.Title != "" {
		return chat.Title
	}
	return fmt.Sprintf("Chat %d", chatID)
}

// getAllDialogs 获取所有对话（使用 split ranges）
func (u *UserClient) getAllDialogs(ctx context.Context, client *takeout.Client, progressCallback TakeoutProgressCallback) ([]*tg.Dialog, error) {
	logger := log.FromContext(ctx)

	// 1. 获取 split ranges
	ranges, err := u.getSplitRanges(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("failed to get split ranges: %w", err)
	}

	if len(ranges) == 0 {
		logger.Warn("No split ranges returned, using fallback method")
		return u.getAllDialogsWithoutRanges(ctx, client)
	}

	logger.Info("Got split ranges for dialogs", "count", len(ranges))

	var allDialogs []*tg.Dialog
	peerStorage := u.TClient.PeerStorage

	// 2. 遍历每个 range
	for rangeIdx, msgRange := range ranges {
		if progressCallback != nil {
			progressCallback("dialogs", rangeIdx+1, len(ranges),
				fmt.Sprintf("Fetching dialogs range %d/%d", rangeIdx+1, len(ranges)))
		}

		var offsetDate int
		var offsetID int
		var offsetPeer tg.InputPeerClass = &tg.InputPeerEmpty{}

		// 3. 在当前 range 内分页
		for {
			innerReq := &tg.MessagesGetDialogsRequest{
				OffsetDate: offsetDate,
				OffsetID:   offsetID,
				OffsetPeer: offsetPeer,
				Limit:      100,
			}

			// 使用 InvokeWithMessagesRange 包装请求
			req := &tg.InvokeWithMessagesRangeRequest{
				Range: msgRange,
				Query: innerReq,
			}

			var result tg.MessagesDialogsBox
			if err := client.Invoke(ctx, req, &result); err != nil {
				logger.Error("Failed to get dialogs in range", "range", msgRange, "error", err)
				break // 继续下一个 range
			}

			var dialogs []tg.DialogClass
			var messages []tg.MessageClass
			var users []tg.UserClass
			var chats []tg.ChatClass

			switch d := result.Dialogs.(type) {
			case *tg.MessagesDialogs:
				dialogs = d.Dialogs
				messages = d.Messages
				users = d.Users
				chats = d.Chats
			case *tg.MessagesDialogsSlice:
				dialogs = d.Dialogs
				messages = d.Messages
				users = d.Users
				chats = d.Chats
			case *tg.MessagesDialogsNotModified:
				// 没有更多对话
				break
			}

			if len(dialogs) == 0 {
				break
			}

			// 写入 peerStorage 以便后续生成 InputPeer
			for _, userClass := range users {
				if user, ok := userClass.(*tg.User); ok {
					peerStorage.AddPeer(user.ID, user.AccessHash, storage.TypeUser, user.Username)
				}
			}
			for _, chatClass := range chats {
				switch chat := chatClass.(type) {
				case *tg.Chat:
					peerStorage.AddPeer(chat.ID, storage.DefaultAccessHash, storage.TypeChat, storage.DefaultUsername)
				case *tg.Channel:
					peerStorage.AddPeer(chat.ID, chat.AccessHash, storage.TypeChannel, chat.Username)
				}
			}

			// 提取 *tg.Dialog 类型
			for _, d := range dialogs {
				if dialog, ok := d.(*tg.Dialog); ok {
					allDialogs = append(allDialogs, dialog)
				}
			}

			// 更新分页参数
			if len(messages) > 0 {
				lastMsg := messages[len(messages)-1]
				if msg, ok := lastMsg.(*tg.Message); ok {
					offsetDate = msg.Date
					offsetID = msg.ID
					offsetPeer = peerStorage.GetInputPeerById(u.getPeerID(msg.PeerID))
					if offsetPeer == nil {
						offsetPeer = &tg.InputPeerEmpty{}
					}
				}
			}

			if len(dialogs) < 100 {
				break // 当前 range 已耗尽
			}

			time.Sleep(100 * time.Millisecond)
		}
	}

	return allDialogs, nil
}

// getAllDialogsWithoutRanges 备用方法：不使用 split ranges 获取对话（兼容性）
func (u *UserClient) getAllDialogsWithoutRanges(ctx context.Context, client *takeout.Client) ([]*tg.Dialog, error) {
	var allDialogs []*tg.Dialog
	var offsetDate int
	var offsetID int
	var offsetPeer tg.InputPeerClass = &tg.InputPeerEmpty{}
	peerStorage := u.TClient.PeerStorage
	api := tg.NewClient(client)

	for {
		result, err := api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
			OffsetDate: offsetDate,
			OffsetID:   offsetID,
			OffsetPeer: offsetPeer,
			Limit:      100,
		})
		if err != nil {
			return nil, err
		}

		var dialogs []tg.DialogClass
		var messages []tg.MessageClass
		var users []tg.UserClass
		var chats []tg.ChatClass

		switch d := result.(type) {
		case *tg.MessagesDialogs:
			dialogs = d.Dialogs
			messages = d.Messages
			users = d.Users
			chats = d.Chats
		case *tg.MessagesDialogsSlice:
			dialogs = d.Dialogs
			messages = d.Messages
			users = d.Users
			chats = d.Chats
		case *tg.MessagesDialogsNotModified:
			return allDialogs, nil
		}

		if len(dialogs) == 0 {
			break
		}

		for _, userClass := range users {
			if user, ok := userClass.(*tg.User); ok {
				peerStorage.AddPeer(user.ID, user.AccessHash, storage.TypeUser, user.Username)
			}
		}
		for _, chatClass := range chats {
			switch chat := chatClass.(type) {
			case *tg.Chat:
				peerStorage.AddPeer(chat.ID, storage.DefaultAccessHash, storage.TypeChat, storage.DefaultUsername)
			case *tg.Channel:
				peerStorage.AddPeer(chat.ID, chat.AccessHash, storage.TypeChannel, chat.Username)
			}
		}

		for _, d := range dialogs {
			if dialog, ok := d.(*tg.Dialog); ok {
				allDialogs = append(allDialogs, dialog)
			}
		}

		if len(messages) > 0 {
			lastMsg := messages[len(messages)-1]
			if msg, ok := lastMsg.(*tg.Message); ok {
				offsetDate = msg.Date
				offsetID = msg.ID
				offsetPeer = peerStorage.GetInputPeerById(u.getPeerID(msg.PeerID))
				if offsetPeer == nil {
					offsetPeer = &tg.InputPeerEmpty{}
				}
			}
		}

		if len(dialogs) < 100 {
			break
		}

		time.Sleep(100 * time.Millisecond)
	}

	return allDialogs, nil
}

// exportChatHistory 导出单个聊天的历史消息（使用 split ranges）
func (u *UserClient) exportChatHistory(ctx context.Context, client *takeout.Client, dialog *tg.Dialog, chatID int64, enableWatching bool) (int, error) {
	logger := log.FromContext(ctx)

	// 获取或创建索引
	eng := engine.GetEngine()
	if err := eng.CreateIndex(ctx, chatID); err != nil {
		logger.Debug("Index may already exist", "chat_id", chatID)
	}

	// 获取 split ranges
	ranges, err := u.getSplitRanges(ctx, client)
	if err != nil {
		logger.Warn("Failed to get split ranges, using fallback", "error", err)
		return u.exportChatHistoryWithoutRanges(ctx, client, dialog, chatID, enableWatching)
	}

	if len(ranges) == 0 {
		logger.Debug("No split ranges for history, using fallback")
		return u.exportChatHistoryWithoutRanges(ctx, client, dialog, chatID, enableWatching)
	}

	logger.Debug("Got split ranges for history", "chat_id", chatID, "count", len(ranges))

	totalMessages := 0
	metadataUpdated := false
	batchSize := 100

	// 遍历每个 range
	for rangeIdx, msgRange := range ranges {
		var offsetID int

		// 在当前 range 内分页
		for {
			innerReq := &tg.MessagesGetHistoryRequest{
				Peer:      u.peerToInputPeer(dialog.Peer),
				OffsetID:  offsetID,
				Limit:     batchSize,
				AddOffset: 0,
			}

			// 使用 InvokeWithMessagesRange 包装请求
			req := &tg.InvokeWithMessagesRangeRequest{
				Range: msgRange,
				Query: innerReq,
			}

			var result tg.MessagesMessagesBox
			if err := client.Invoke(ctx, req, &result); err != nil {
				logger.Error("Failed to get history in range", "range", msgRange, "error", err)
				break // 继续下一个 range
			}

			var messages []*tg.Message
			var users []tg.UserClass
			var chats []tg.ChatClass

			switch m := result.Messages.(type) {
			case *tg.MessagesMessages:
				messages = u.extractMessages(m.Messages)
				users = m.Users
				chats = m.Chats
			case *tg.MessagesMessagesSlice:
				messages = u.extractMessages(m.Messages)
				users = m.Users
				chats = m.Chats
			case *tg.MessagesChannelMessages:
				messages = u.extractMessages(m.Messages)
				users = m.Users
				chats = m.Chats
			case *tg.MessagesMessagesNotModified:
				break
			}

			if len(messages) == 0 {
				break // 当前 range 已耗尽
			}

			// 第一次获取消息时更新 IndexChat 元数据
			if !metadataUpdated {
				if err := u.updateChatMetadataFromEntities(ctx, dialog, chatID, users, chats, enableWatching); err != nil {
					logger.Warn("Failed to update chat metadata", "chat_id", chatID, "error", err)
				}
				metadataUpdated = true
			}

			// 更新用户信息
			if err := u.updateUsersInfo(ctx, users); err != nil {
				logger.Warn("Failed to update users info", "error", err)
			}

			// 获取 ext.Context 来调用 DocumentsFromMessages
			ectx := u.GetContext()

			// 转换为文档并批量索引
			docs := engine.DocumentsFromMessages(ctx, messages, chatID, ectx.Self.ID, ectx, false)
			if len(docs) > 0 {
				if err := eng.AddDocuments(ctx, chatID, docs); err != nil {
					logger.Error("Failed to add documents", "chat_id", chatID, "error", err)
				} else {
					totalMessages += len(docs)
					logger.Debug("Indexed messages", "chat_id", chatID, "count", len(docs), "range", rangeIdx+1)
				}
			}

			// 更新偏移量
			offsetID = messages[len(messages)-1].ID

			if len(messages) < batchSize {
				break // 当前 range 已耗尽
			}

			time.Sleep(100 * time.Millisecond)
		}
	}

	if totalMessages > 0 {
		logger.Info("Chat export completed", "chat_id", chatID, "messages", totalMessages)
	}

	return totalMessages, nil
}

// exportChatHistoryWithoutRanges 备用方法：不使用 split ranges 导出聊天历史（兼容性）
func (u *UserClient) exportChatHistoryWithoutRanges(ctx context.Context, client *takeout.Client, dialog *tg.Dialog, chatID int64, enableWatching bool) (int, error) {
	logger := log.FromContext(ctx)

	// 获取或创建索引
	eng := engine.GetEngine()
	if err := eng.CreateIndex(ctx, chatID); err != nil {
		logger.Debug("Index may already exist", "chat_id", chatID)
	}

	// 分页获取消息
	var offsetID int
	totalMessages := 0
	batchSize := 100
	metadataUpdated := false
	api := tg.NewClient(client)

	for {
		result, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
			Peer:      u.peerToInputPeer(dialog.Peer),
			OffsetID:  offsetID,
			Limit:     batchSize,
			AddOffset: 0,
		})
		if err != nil {
			return totalMessages, fmt.Errorf("failed to get history: %w", err)
		}

		var messages []*tg.Message
		var users []tg.UserClass
		var chats []tg.ChatClass

		switch m := result.(type) {
		case *tg.MessagesMessages:
			messages = u.extractMessages(m.Messages)
			users = m.Users
			chats = m.Chats
		case *tg.MessagesMessagesSlice:
			messages = u.extractMessages(m.Messages)
			users = m.Users
			chats = m.Chats
		case *tg.MessagesChannelMessages:
			messages = u.extractMessages(m.Messages)
			users = m.Users
			chats = m.Chats
		case *tg.MessagesMessagesNotModified:
			break
		}

		if len(messages) == 0 {
			break
		}

		if !metadataUpdated {
			if err := u.updateChatMetadataFromEntities(ctx, dialog, chatID, users, chats, enableWatching); err != nil {
				logger.Warn("Failed to update chat metadata", "chat_id", chatID, "error", err)
			}
			metadataUpdated = true
		}

		if err := u.updateUsersInfo(ctx, users); err != nil {
			logger.Warn("Failed to update users info", "error", err)
		}

		ectx := u.GetContext()
		docs := engine.DocumentsFromMessages(ctx, messages, chatID, ectx.Self.ID, ectx, false)
		if len(docs) > 0 {
			if err := eng.AddDocuments(ctx, chatID, docs); err != nil {
				logger.Error("Failed to add documents", "chat_id", chatID, "error", err)
			} else {
				totalMessages += len(docs)
				logger.Debug("Indexed messages", "chat_id", chatID, "count", len(docs))
			}
		}

		offsetID = messages[len(messages)-1].ID

		if len(messages) < batchSize {
			break
		}

		time.Sleep(100 * time.Millisecond)
	}

	if totalMessages > 0 {
		logger.Info("Chat export completed", "chat_id", chatID, "messages", totalMessages)
	}

	return totalMessages, nil
}

// extractMessages 从 MessageClass 数组中提取 *tg.Message
func (u *UserClient) extractMessages(messageClasses []tg.MessageClass) []*tg.Message {
	messages := make([]*tg.Message, 0, len(messageClasses))
	for _, mc := range messageClasses {
		if msg, ok := mc.(*tg.Message); ok {
			messages = append(messages, msg)
		}
	}
	return messages
}

// getPeerID 从 PeerClass 获取 chatID
func (u *UserClient) getPeerID(peer tg.PeerClass) int64 {
	switch p := peer.(type) {
	case *tg.PeerUser:
		return p.UserID
	case *tg.PeerChat:
		return p.ChatID
	case *tg.PeerChannel:
		return p.ChannelID
	default:
		return 0
	}
}

// peerToInputPeer 将 PeerClass 转换为 InputPeerClass
func (u *UserClient) peerToInputPeer(peer tg.PeerClass) tg.InputPeerClass {
	peerStorage := u.TClient.PeerStorage

	switch p := peer.(type) {
	case *tg.PeerUser:
		if inputPeer := peerStorage.GetInputPeerById(p.UserID); inputPeer != nil {
			return inputPeer
		}
		return &tg.InputPeerUser{UserID: p.UserID}
	case *tg.PeerChat:
		return &tg.InputPeerChat{ChatID: p.ChatID}
	case *tg.PeerChannel:
		if inputPeer := peerStorage.GetInputPeerById(p.ChannelID); inputPeer != nil {
			return inputPeer
		}
		return &tg.InputPeerChannel{ChannelID: p.ChannelID}
	default:
		return &tg.InputPeerEmpty{}
	}
}

// updateChatMetadataFromEntities 从 Telegram 实体中更新聊天元数据
func (u *UserClient) updateChatMetadataFromEntities(ctx context.Context, dialog *tg.Dialog, chatID int64, users []tg.UserClass, chats []tg.ChatClass, enableWatching bool) error {
	chat, err := database.GetIndexChat(ctx, chatID)
	if err != nil {
		// 不存在则创建新记录
		chat = &database.IndexChat{
			ChatID:   chatID,
			Watching: enableWatching,
		}
	}
	// 已有记录不改变 watching 状态
	// 根据 peer 类型从实体中获取元数据
	switch peer := dialog.Peer.(type) {
	case *tg.PeerUser:
		// 从 users 中查找
		for _, userClass := range users {
			if user, ok := userClass.(*tg.User); ok && user.ID == peer.UserID {
				chat.Username = user.Username
				chat.Title = strings.TrimSpace(user.FirstName + " " + user.LastName)
				chat.Type = int(database.ChatTypePrivate)
				break
			}
		}
	case *tg.PeerChat:
		// 从 chats 中查找普通群组
		for _, chatClass := range chats {
			if c, ok := chatClass.(*tg.Chat); ok && c.ID == peer.ChatID {
				chat.Title = c.Title
				chat.Type = int(database.ChatTypeGroup)
				break
			}
		}
	case *tg.PeerChannel:
		// 从 chats 中查找频道/超级群
		for _, chatClass := range chats {
			if channel, ok := chatClass.(*tg.Channel); ok && channel.ID == peer.ChannelID {
				chat.Title = channel.Title
				chat.Username = channel.Username
				chat.Type = int(database.ChatTypeChannel)
				break
			}
		}
	}

	return database.UpsertIndexChat(ctx, chat)
}

// updateUsersInfo 更新用户信息
func (u *UserClient) updateUsersInfo(ctx context.Context, users []tg.UserClass) error {
	for _, userClass := range users {
		if user, ok := userClass.(*tg.User); ok {
			userDB, err := database.GetUserInfo(ctx, user.ID)
			if err == nil {
				// 已存在，更新信息
				userDB.FirstName = user.FirstName
				userDB.LastName = user.LastName
				userDB.Username = user.Username
				if err := database.UpsertUserInfo(ctx, userDB); err != nil {
					return err
				}
				continue
			}
			// 不存在，创建新记录
			userInfo := &database.UserInfo{
				ChatID:    user.ID,
				FirstName: user.FirstName,
				LastName:  user.LastName,
				Username:  user.Username,
			}
			if err := database.UpsertUserInfo(ctx, userInfo); err != nil {
				return err
			}
		}
	}
	return nil
}

// [TODO] TakeoutInitDelay 一般是需要在新设备在线超过 24 小时后才允许使用 Takeout
// 暂不实现
// func (u *UserClient) takeoutRunWithRetry(
// 	ctx context.Context,
// 	cfg takeout.Config,
// 	progressCallback TakeoutProgressCallback,
// 	f func(ctx context.Context, client *takeout.Client) error,
// ) error {
// 	// 官方文档：initTakeoutSession 可能返回 TAKEOUT_INIT_DELAY_%d。
// 	// gotd/td 的 takeout.Run 不做等待重试，这里补齐。
// 	const maxAttempts = 10
// 	invoker := u.TClient.API().Invoker()

// 	var lastErr error
// 	for attempt := 1; attempt <= maxAttempts; attempt++ {
// 		lastErr = takeout.Run(ctx, invoker, cfg, f)
// 		if lastErr == nil {
// 			return nil
// 		}

// 		// 等待 takeout 安全确认窗口。
// 		if tg.IsTakeoutInitDelay(lastErr) {
// 			wait := 60 * time.Second
// 			var rpcErr *tgerr.Error
// 			if errors.As(lastErr, &rpcErr) && rpcErr.Argument > 0 {
// 				wait = time.Duration(rpcErr.Argument) * time.Second
// 			}

// 			if progressCallback != nil {
// 				progressCallback("init", 0, 1, fmt.Sprintf("Takeout init delayed, waiting %s...", wait.Round(time.Second)))
// 			}

// 			t := time.NewTimer(wait)
// 			select {
// 			case <-ctx.Done():
// 				t.Stop()
// 				return ctx.Err()
// 			case <-t.C:
// 				continue
// 			}
// 		}

// 		// 某些情况下会话/任务冲突，简单退避重试。
// 		if tg.IsTaskAlreadyExists(lastErr) {
// 			wait := time.Duration(attempt*5) * time.Second
// 			if progressCallback != nil {
// 				progressCallback("init", 0, 1, fmt.Sprintf("Takeout task already exists, retrying in %s...", wait))
// 			}
// 			t := time.NewTimer(wait)
// 			select {
// 			case <-ctx.Done():
// 				t.Stop()
// 				return ctx.Err()
// 			case <-t.C:
// 				continue
// 			}
// 		}

// 		return lastErr
// 	}

// 	return lastErr
// }
