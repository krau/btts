package bot

import (
	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
)

func SyncPeersHandler(ctx *ext.Context, update *ext.Update) error {
	if !CheckPermission(ctx, update) {
		return dispatcher.EndGroups
	}
	if err := bi.UserClient.SyncPeers(ctx); err != nil {
		ctx.Reply(update, ext.ReplyTextString("Failed to synchronize peers: "+err.Error()), nil)
		return dispatcher.EndGroups
	}
	ctx.Reply(update, ext.ReplyTextString("Peers synchronized successfully"), nil)
	return dispatcher.EndGroups
}
