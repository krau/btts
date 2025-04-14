package bot

import (
	"context"

	"github.com/celestix/gotgproto"
	"github.com/celestix/gotgproto/sessionMaker"
	"github.com/charmbracelet/log"
	"github.com/krau/btts/config"
	"github.com/krau/btts/engine"
	"github.com/krau/btts/middlewares"
	"github.com/krau/btts/userclient"
	"github.com/ncruces/go-sqlite3/gormlite"
)

var BotInstance *Bot

type Bot struct {
	Client     *gotgproto.Client
	UserClient *userclient.UserClient
	Engine     *engine.Engine
}

func (b *Bot) Start(ctx context.Context) {
	log := log.FromContext(ctx)
	log.Info("Starting bot...")

	if err := b.RegisterHandlers(ctx); err != nil {
		log.Errorf("Failed to register handlers: %v", err)
		return
	}

	b.UserClient.StartWatch(ctx)

	<-ctx.Done()
	log.Info("Exiting...")
	if err := b.UserClient.Close(); err != nil {
		log.Errorf("Failed to close user client: %v", err)
	}
}

func NewBot(ctx context.Context, userClient *userclient.UserClient, engine *engine.Engine) (*Bot, error) {
	log := log.FromContext(ctx)
	log.Debug("Initializing bot")
	if BotInstance != nil {
		return BotInstance, nil
	}
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
				Context:          ctx,
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
			Engine:     engine,
		}
		BotInstance = b
		return BotInstance, nil
	}
}
