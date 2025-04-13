package bot

import (
	"github.com/duke-git/lancet/v2/slice"
	"github.com/krau/btts/config"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/dispatcher/handlers"
	"github.com/celestix/gotgproto/dispatcher/handlers/filters"
	"github.com/celestix/gotgproto/ext"
)

func CheckPermissionsHandler(ctx *ext.Context, update *ext.Update) error {
	userID := update.GetUserChat().GetID()
	if userID == BotInstance.UserClient.TClient.Self.ID {
		return dispatcher.ContinueGroups
	}
	if !slice.Contain(config.C.Admins, userID) {
		return dispatcher.EndGroups
	}
	return dispatcher.ContinueGroups
}

func (b *Bot) RegisterHandlers() error {
	disp := b.Client.Dispatcher
	disp.AddHandler(handlers.NewCommand("start", StartHandler))
	disp.AddHandler(handlers.NewCommand("help", StartHandler))
	disp.AddHandler(handlers.NewCommand("search", SearchHandler))

	// admins
	disp.AddHandler(handlers.NewMessage(filters.Message.All, CheckPermissionsHandler))
	disp.AddHandler(handlers.NewCommand("add", AddHandler))
	return nil
}
