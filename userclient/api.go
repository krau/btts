package userclient

import (
	"context"

	"github.com/celestix/gotgproto/types"
	"github.com/gotd/td/tg"
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
