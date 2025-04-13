package bot

import (
	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
)

func SearchHandler(ctx *ext.Context, update *ext.Update) error {
	return dispatcher.EndGroups
}
