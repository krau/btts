package userclient

import (
	"context"

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
