package bot

import (
	"context"

	"github.com/celestix/gotgproto"
	"github.com/celestix/gotgproto/sessionMaker"
	"github.com/charmbracelet/log"
	"github.com/krau/btts/config"
	"github.com/krau/btts/core/middlewares"
	"github.com/krau/btts/core/userclient"
	_ "github.com/ncruces/go-sqlite3/embed"
	"github.com/ncruces/go-sqlite3/gormlite"
)

type Bot struct {
	Client     *gotgproto.Client
	UserClient *userclient.UserClient
}

func (b *Bot) Start(ctx context.Context) {
	log.FromContext(ctx).Info("Starting bot...")
	<-ctx.Done()
}

func NewBot(ctx context.Context, userClient *userclient.UserClient) (*Bot, error) {
	res := make(chan struct {
		client *gotgproto.Client
		err    error
	})
	go func() {
		tclient, err := gotgproto.NewClient(
			config.C.AppID,
			config.C.AppHash,
			gotgproto.ClientTypeBot(config.C.BotToken),
			&gotgproto.ClientOpts{
				Session:          sessionMaker.SqlSession(gormlite.Open("data/session_bot.db")),
				DisableCopyright: true,
				Middlewares:      middlewares.FloodWait(),
			},
		)
		res <- struct {
			client *gotgproto.Client
			err    error
		}{client: tclient, err: err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-res:
		if r.err != nil {
			return nil, r.err
		}
		b := &Bot{
			Client:     r.client,
			UserClient: userClient,
		}
		return b, nil
	}
}
