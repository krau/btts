package bot

import (
	"context"
	"time"

	"github.com/charmbracelet/log"
	"github.com/krau/btts/config"
	"github.com/krau/btts/engine"
	"github.com/krau/btts/middlewares"
	"github.com/krau/btts/subbot"
	"github.com/krau/btts/userclient"
	"github.com/krau/mygotg"
	"github.com/krau/mygotg/ext"
	"github.com/krau/mygotg/session"
	"github.com/ncruces/go-sqlite3/gormlite"
)

var bi *Bot // Bot Instance

func GetBot() *Bot {
	if bi == nil {
		panic("Bot is not initialized, call NewBot first")
	}
	return bi
}

type Bot struct {
	Client     *mygotg.Client
	UserClient *userclient.UserClient
	Engine     engine.Searcher
	ectx       *ext.Context // created by Client.CreateContext()
}

func (b *Bot) GetContext() *ext.Context {
	if b.ectx == nil {
		b.ectx = b.Client.CreateContext()
	}
	return b.ectx
}

func (b *Bot) Start(ctx context.Context) {
	log := log.FromContext(ctx)
	log.Info("Starting bot...")

	b.RegisterHandlers(ctx)

	b.UserClient.StartWatch(ctx)
	err := subbot.StartStored(ctx)
	if err != nil {
		log.Errorf("Failed to start sub bots: %v", err)
	}
	for _, sb := range subbot.GetAll() {
		b.UserClient.AddGlobalIgnoreUser(sb.ID)
	}

	log.Info("Bot started.")
	<-ctx.Done()
	log.Info("Exiting...")
	if err := b.UserClient.Close(); err != nil {
		log.Errorf("Failed to close user client: %v", err)
	}
	for _, sb := range subbot.GetAll() {
		sb.Stop()
	}
}

func (b *Bot) GetUsername() string {
	if b.Client == nil {
		return ""
	}
	return b.Client.Self.Username
}

func NewBot(ctx context.Context, userClient *userclient.UserClient, engine engine.Searcher) (*Bot, error) {
	log := log.FromContext(ctx)
	log.Debug("Initializing bot")
	if bi != nil {
		return bi, nil
	}
	res := make(chan struct {
		client *mygotg.Client
		err    error
	})
	go func() {
		tclient, err := mygotg.NewClient(
			config.C.AppID,
			config.C.AppHash,
			mygotg.ClientTypeBot(config.C.BotToken),
			&mygotg.ClientOpts{
				AutoFetchReply:   true,
				Session:          session.SqlSession(gormlite.Open("data/session_bot.db")),
				DisableCopyright: true,
				Middlewares:      middlewares.NewDefaultMiddlewares(ctx, 5*time.Minute),
				Context:          ctx,
			},
		)
		res <- struct {
			client *mygotg.Client
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
			ectx:       r.client.CreateContext(),
		}
		bi = b
		if b.Client.Self.ID == 0 {
			log.Fatalf("Failed to get bot ID")
		}
		userClient.AddGlobalIgnoreUser(b.Client.Self.ID)
		return bi, nil
	}
}
