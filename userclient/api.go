package userclient

import (
	"context"

	"github.com/gotd/td/tg"
)

func (u *UserClient) ForwardMessagesToFav(ctx context.Context, fromID int64, messageIDs []int) error {
	uctx := u.TClient.CreateContext()
	req := &tg.MessagesForwardMessagesRequest{
		ID: messageIDs,
	}
	if _, err := uctx.ForwardMessages(fromID, uctx.Self.ID, req); err != nil {
		return err
	}
	return nil
}
