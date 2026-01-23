package userclient

import (
	"context"

	"github.com/charmbracelet/log"
	"github.com/gotd/td/telegram/query/dialogs"
	"github.com/gotd/td/tg"
	"github.com/krau/btts/database"
	"github.com/krau/mygotg/storage"
	"github.com/krau/mygotg/types"
)

func (u *UserClient) ForwardMessagesToFav(ctx context.Context, fromID int64, messageIDs []int) error {
	if u.ectx == nil {
		u.ectx = u.TClient.CreateContext()
	}
	req := &tg.MessagesForwardMessagesRequest{
		ID: messageIDs,
	}
	if _, err := u.ectx.ForwardMessages(fromID, u.ectx.Self.ID, req); err != nil {
		return err
	}
	return nil
}

func (u *UserClient) ReplyMessage(ctx context.Context, chatID int64, messageID int, text string) (*types.Message, error) {
	if u.ectx == nil {
		u.ectx = u.TClient.CreateContext()
	}
	replyReq := &tg.InputReplyToMessage{
		ReplyToMsgID: messageID,
	}
	req := &tg.MessagesSendMessageRequest{Message: text}
	req.SetReplyTo(replyReq)
	return u.ectx.SendMessage(chatID, req)
}

func (u *UserClient) ForwardMessages(ctx context.Context, fromChatID, toChatID int64, messageID []int) error {
	if u.ectx == nil {
		u.ectx = u.TClient.CreateContext()
	}
	req := &tg.MessagesForwardMessagesRequest{
		ID: messageID,
	}
	_, err := u.ectx.ForwardMessages(fromChatID, toChatID, req)
	return err
}

func (u *UserClient) SyncPeers(ctx context.Context) error {
	api := u.TClient.API()
	peerStorage := u.TClient.PeerStorage
	log.Info("Synchronizing peers...")
	return dialogs.NewQueryBuilder(api).GetDialogs().BatchSize(50).ForEach(ctx, func(ctx context.Context, e dialogs.Elem) error {
		for cid, channel := range e.Entities.Channels() {
			peerStorage.AddPeer(cid, channel.AccessHash, storage.TypeChannel, channel.Username)
			chatDB, err := database.GetIndexChat(ctx, cid)
			if err != nil {
				continue
			}
			chatDB.Title = channel.GetTitle()
			chatDB.Username = channel.Username
			chatDB.Type = int(database.ChatTypeChannel)
			chatDB.ChatID = cid
			if err := database.UpsertIndexChat(ctx, chatDB); err != nil {
				log.Warnf("Failed to upsert index chat: %v", err)
			}
		}
		for uid, user := range e.Entities.Users() {
			peerStorage.AddPeer(uid, user.AccessHash, storage.TypeUser, user.Username)
		}
		for gid := range e.Entities.Chats() {
			peerStorage.AddPeer(gid, storage.DefaultAccessHash, storage.TypeChat, storage.DefaultUsername)
		}
		return nil
	})
}
