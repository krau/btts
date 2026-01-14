package userclient

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/log"
	"github.com/gotd/td/tg"
	"github.com/krau/btts/database"
	"github.com/krau/btts/engine"
)

// SyncMissedUpdates 在客户端启动时同步错过的消息
func (u *UserClient) SyncMissedUpdates(ctx context.Context) error {
	logger := log.FromContext(ctx)
	logger.Info("Syncing missed updates...")

	// 获取当前保存的状态
	state, err := database.GetUpdatesState(ctx)
	if err != nil {
		return fmt.Errorf("failed to get updates state: %w", err)
	}

	// 如果状态为空（首次启动），则只获取当前状态
	if state.Pts == 0 && state.Qts == 0 && state.Date == 0 {
		if err := u.fetchCurrentState(ctx); err != nil {
			return fmt.Errorf("failed to fetch current state: %w", err)
		}
		logger.Info("Initial state saved")
		return nil
	}

	// 调用 updates.getDifference 获取错过的消息
	api := u.TClient.API()
	totalMessages := 0
	totalUpdates := 0

	for {
		req := &tg.UpdatesGetDifferenceRequest{
			Pts:  state.Pts,
			Date: state.Date,
			Qts:  state.Qts,
		}
		req.SetPtsLimit(5000)
		req.SetQtsLimit(5000)
		diff, err := api.UpdatesGetDifference(ctx, req)
		if err != nil {
			return fmt.Errorf("failed to get difference: %w", err)
		}

		switch d := diff.(type) {
		case *tg.UpdatesDifferenceEmpty:
			// 没有新的更新
			if totalMessages > 0 || totalUpdates > 0 {
				logger.Info("Sync completed", "messages", totalMessages, "updates", totalUpdates)
			} else {
				logger.Info("No missed updates")
			}
			state.Date = d.Date
			state.Seq = d.Seq
			if err := database.UpdateUpdatesState(ctx, state); err != nil {
				logger.Error("Failed to update state", "error", err)
			}
			return nil

		case *tg.UpdatesDifference:
			// 完整的差异，处理并返回
			totalMessages += len(d.NewMessages)
			totalUpdates += len(d.OtherUpdates)
			if err := u.processDifference(ctx, d.NewMessages, d.OtherUpdates); err != nil {
				logger.Error("Failed to process difference", "error", err)
			}
			// 更新状态
			state.Pts = d.State.Pts
			state.Qts = d.State.Qts
			state.Date = d.State.Date
			state.Seq = d.State.Seq
			if err := database.UpdateUpdatesState(ctx, state); err != nil {
				logger.Error("Failed to update state", "error", err)
			}
			logger.Info("Sync completed", "messages", totalMessages, "updates", totalUpdates)
			return nil

		case *tg.UpdatesDifferenceSlice:
			// 差异太大，分片返回
			totalMessages += len(d.NewMessages)
			totalUpdates += len(d.OtherUpdates)
			if err := u.processDifference(ctx, d.NewMessages, d.OtherUpdates); err != nil {
				logger.Error("Failed to process difference slice", "error", err)
			}
			// 更新中间状态并继续获取
			state.Pts = d.IntermediateState.Pts
			state.Qts = d.IntermediateState.Qts
			state.Date = d.IntermediateState.Date
			state.Seq = d.IntermediateState.Seq
			if err := database.UpdateUpdatesState(ctx, state); err != nil {
				logger.Error("Failed to update intermediate state", "error", err)
			}
		case *tg.UpdatesDifferenceTooLong:
			// 差异太大，需要重新获取状态
			logger.Warn("Difference too long, resetting state")
			state.Pts = d.Pts
			if err := database.UpdateUpdatesState(ctx, state); err != nil {
				logger.Error("Failed to update state", "error", err)
			}
			return nil
		}
		// https://core.telegram.org/api/updates#recovering-gaps
		// sleep 0.5s to wait the new updates to be ready
		time.Sleep(500 * time.Millisecond)
	}
}

// fetchCurrentState 获取当前的 updates state（首次启动时）
func (u *UserClient) fetchCurrentState(ctx context.Context) error {
	api := u.TClient.API()

	stateResp, err := api.UpdatesGetState(ctx)
	if err != nil {
		return fmt.Errorf("failed to get state: %w", err)
	}

	state := &database.UpdatesState{
		ID:   1,
		Pts:  stateResp.Pts,
		Qts:  stateResp.Qts,
		Date: stateResp.Date,
		Seq:  stateResp.Seq,
	}

	return database.UpdateUpdatesState(ctx, state)
}

// processDifference 处理 getDifference 返回的消息和更新
func (u *UserClient) processDifference(ctx context.Context, newMessages []tg.MessageClass, otherUpdates []tg.UpdateClass) error {
	logger := log.FromContext(ctx)
	ectx := u.GetContext()

	// 按 chatID 分组消息
	messagesByChat := make(map[int64][]*tg.Message)

	for _, msgClass := range newMessages {
		msg, ok := msgClass.(*tg.Message)
		if !ok {
			continue
		}

		// 检查是否是被监听的聊天
		chatID := u.getChatIDFromMessage(msg)
		if chatID == 0 || !database.Watching(chatID) {
			continue
		}

		messagesByChat[chatID] = append(messagesByChat[chatID], msg)
	}

	// 批量添加每个聊天的消息到索引
	for chatID, messages := range messagesByChat {
		docs := engine.DocumentsFromMessages(ctx, messages, chatID, ectx.Self.ID, ectx, false)
		if len(docs) > 0 {
			if err := engine.GetEngine().AddDocuments(ctx, chatID, docs); err != nil {
				logger.Error("Failed to add documents", "error", err, "chat_id", chatID, "count", len(docs))
			} else {
				logger.Info("Indexed missed messages", "chat_id", chatID, "count", len(docs))
			}
		}
	}

	// 处理其他更新（如删除消息）
	for _, updateClass := range otherUpdates {
		switch update := updateClass.(type) {
		case *tg.UpdateDeleteChannelMessages:
			chatID := update.GetChannelID()
			if database.Watching(chatID) {
				chatDB, err := database.GetIndexChat(ctx, chatID)
				if err != nil {
					logger.Error("Failed to get chat", "error", err, "chat_id", chatID)
					continue
				}
				if !chatDB.NoDelete {
					if err := engine.GetEngine().DeleteDocuments(ctx, chatID, update.GetMessages()); err != nil {
						logger.Error("Failed to delete documents", "error", err, "chat_id", chatID)
					}
				}
			}
		case *tg.UpdateChannelTooLong:
			// 某个 channel 的更新太多，需要单独处理
			chatID := update.GetChannelID()
			if database.Watching(chatID) {
				logger.Info("Channel has too many updates, syncing separately", "chat_id", chatID)
				// 在后台异步处理
				go func() {
					if err := u.syncChannelDifference(context.Background(), chatID); err != nil {
						logger.Error("Failed to sync channel difference", "error", err, "chat_id", chatID)
					}
				}()
			}
		}
	}

	return nil
}

// syncChannelDifference 同步某个 channel 的错过消息
func (u *UserClient) syncChannelDifference(ctx context.Context, channelID int64) error {
	logger := log.FromContext(ctx).With("channel_id", channelID)
	logger.Info("Syncing channel difference")

	chatDB, err := database.GetIndexChat(ctx, channelID)
	if err != nil {
		return fmt.Errorf("failed to get chat: %w", err)
	}

	api := u.TClient.API()

	// 从 peer storage 获取 input peer
	peerStorage := u.TClient.PeerStorage
	inputPeer := peerStorage.GetInputPeerById(channelID)
	if inputPeer == nil {
		return fmt.Errorf("peer not found in storage: %d", channelID)
	}

	// 构造 channel input
	inputChannel, ok := inputPeer.(*tg.InputPeerChannel)
	if !ok {
		return fmt.Errorf("peer is not a channel: %d", channelID)
	}

	channelInput := &tg.InputChannel{
		ChannelID:  inputChannel.ChannelID,
		AccessHash: inputChannel.AccessHash,
	}

	pts := chatDB.Pts
	if pts == 0 {
		logger.Warn("No pts found, skipping sync")
		return nil
	}

	totalMessages := 0

	for {
		diff, err := api.UpdatesGetChannelDifference(ctx, &tg.UpdatesGetChannelDifferenceRequest{
			Channel: channelInput,
			Filter:  &tg.ChannelMessagesFilterEmpty{},
			Pts:     pts,
			Limit:   100,
		})
		if err != nil {
			return fmt.Errorf("failed to get channel difference: %w", err)
		}

		switch d := diff.(type) {
		case *tg.UpdatesChannelDifferenceEmpty:
			if totalMessages > 0 {
				logger.Info("Channel sync completed", "messages", totalMessages)
			} else {
				logger.Info("No missed channel updates")
			}
			pts = d.Pts
			if err := database.UpdateChannelPts(ctx, channelID, pts); err != nil {
				logger.Error("Failed to update channel pts", "error", err)
			}
			return nil

		case *tg.UpdatesChannelDifference:
			totalMessages += len(d.NewMessages)
			if err := u.processDifference(ctx, d.NewMessages, d.OtherUpdates); err != nil {
				logger.Error("Failed to process channel difference", "error", err)
			}
			pts = d.Pts
			if err := database.UpdateChannelPts(ctx, channelID, pts); err != nil {
				logger.Error("Failed to update channel pts", "error", err)
			}
			if d.Final {
				logger.Info("Channel sync completed", "messages", totalMessages)
				return nil
			}
			// 继续获取
			time.Sleep(100 * time.Millisecond)

		case *tg.UpdatesChannelDifferenceTooLong:
			logger.Warn("Channel difference too long, may need manual intervention")
			return nil
		}
	}
}

// getChatIDFromMessage 从消息中提取 chatID
func (u *UserClient) getChatIDFromMessage(msg *tg.Message) int64 {
	peerID := msg.GetPeerID()
	switch peer := peerID.(type) {
	case *tg.PeerUser:
		return peer.UserID
	case *tg.PeerChat:
		return peer.ChatID
	case *tg.PeerChannel:
		return peer.ChannelID
	default:
		return 0
	}
}

type PtsUpdate interface {
	GetPts() int
}

// updateStateFromUpdates 从实时更新中更新状态
func (u *UserClient) updateStateFromUpdates(ctx context.Context, update tg.UpdateClass) {
	logger := log.FromContext(ctx)

	state, err := database.GetUpdatesState(ctx)
	if err != nil {
		logger.Error("Failed to get updates state", "error", err)
		return
	}

	updated := false

	// 根据不同类型的更新来更新相应的状态
	switch update.(type) {
	case *tg.UpdateNewMessage, *tg.UpdateEditMessage, *tg.UpdateDeleteMessages:
		// 这些更新会影响 pts
		if ptsUpdate, ok := update.(PtsUpdate); ok {
			pts := ptsUpdate.GetPts()
			if pts > state.Pts {
				state.Pts = pts
				updated = true
			}
		} else {
			logger.Warn("Update does not have pts", "update", update)
		}
	case *tg.UpdateNewChannelMessage, *tg.UpdateEditChannelMessage:
		// Channel 消息有自己的 pts
		if ptsUpdate, ok := update.(PtsUpdate); ok {
			pts := ptsUpdate.GetPts()
			var channelID int64
			switch u := update.(type) {
			case *tg.UpdateNewChannelMessage:
				if msg, ok := u.Message.(*tg.Message); ok {
					if peer, ok := msg.PeerID.(*tg.PeerChannel); ok {
						channelID = peer.ChannelID
					}
				}
			case *tg.UpdateEditChannelMessage:
				if msg, ok := u.Message.(*tg.Message); ok {
					if peer, ok := msg.PeerID.(*tg.PeerChannel); ok {
						channelID = peer.ChannelID
					}
				}
			}
			if channelID != 0 && database.Indexed(channelID) {
				if err := database.UpdateChannelPts(ctx, channelID, pts); err != nil {
					logger.Error("Failed to update channel pts", "error", err, "channel_id", channelID)
				}
			}
		} else {
			logger.Warn("Update does not have pts", "update", update)
		}
	}

	if updated {
		if err := database.UpdateUpdatesState(ctx, state); err != nil {
			logger.Error("Failed to update state", "error", err)
		}
	}
}
