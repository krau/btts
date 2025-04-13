package bot

import (
	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
)

func StartHandler(ctx *ext.Context, update *ext.Update) error {
	ctx.Reply(update, ext.ReplyTextString("Yet Another Bot For Telegram Search..."), nil)
	return dispatcher.EndGroups
}
