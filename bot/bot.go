package bot

import (
	"context"
	"time"

	"github.com/celestix/gotgproto"
	"github.com/celestix/gotgproto/sessionMaker"
	"github.com/charmbracelet/log"
	"github.com/krau/btts/config"
	"github.com/krau/btts/engine"
	"github.com/krau/btts/middlewares"
	"github.com/krau/btts/subbot"
	"github.com/krau/btts/userclient"
	"github.com/ncruces/go-sqlite3/gormlite"
)

var bi *Bot // Bot Instance

type Bot struct {
	Client     *gotgproto.Client
	UserClient *userclient.UserClient
	Engine     *engine.Engine
}

func (b *Bot) Start(ctx context.Context) {
	log := log.FromContext(ctx)
	log.Info("Starting bot...")

	b.RegisterHandlers(ctx)

	b.UserClient.StartWatch(ctx)
	sbs, err := subbot.StartStored(ctx)
	if err != nil {
		log.Errorf("Failed to start sub bots: %v", err)
	}
	for _, sb := range sbs {
		b.UserClient.AddGlobalIgnoreUser(sb.ID)
	}

	log.Info("Bot started.")
	<-ctx.Done()
	log.Info("Exiting...")
	if err := b.UserClient.Close(); err != nil {
		log.Errorf("Failed to close user client: %v", err)
	}
}

func (b *Bot) GetUsername() string {
	if b.Client == nil {
		return ""
	}
	return b.Client.Self.Username
}

func NewBot(ctx context.Context, userClient *userclient.UserClient, engine *engine.Engine) (*Bot, error) {
	log := log.FromContext(ctx)
	log.Debug("Initializing bot")
	if bi != nil {
		return bi, nil
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
				AutoFetchReply:   true,
				Session:          sessionMaker.SqlSession(gormlite.Open("data/session_bot.db")),
				DisableCopyright: true,
				Middlewares:      middlewares.NewDefaultMiddlewares(ctx, 5*time.Minute),
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
		bi = b
		if b.Client.Self.ID == 0 {
			log.Fatalf("Failed to get bot ID")
		}
		userClient.AddGlobalIgnoreUser(b.Client.Self.ID)
		return bi, nil
	}
}
