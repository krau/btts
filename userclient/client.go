package userclient

import (
	"context"
	"path/filepath"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	lumberjack "gopkg.in/natefinch/lumberjack.v2"

	"github.com/celestix/gotgproto"
	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/dispatcher/handlers"
	"github.com/celestix/gotgproto/dispatcher/handlers/filters"
	"github.com/celestix/gotgproto/ext"
	"github.com/celestix/gotgproto/sessionMaker"
	"github.com/charmbracelet/log"
	"github.com/gotd/td/tg"
	"github.com/krau/btts/config"
	"github.com/krau/btts/database"
	"github.com/krau/btts/middlewares"
	"github.com/ncruces/go-sqlite3/gormlite"
)

var UC *UserClient

type UserClient struct {
	TClient *gotgproto.Client
	logger  *zap.Logger
}

func (u *UserClient) StartWatch(ctx context.Context) {
	disp := u.TClient.Dispatcher
	disp.AddHandlerToGroup(handlers.NewAnyUpdate(func(ctx *ext.Context, u *ext.Update) error {
		switch update := u.UpdateClass.(type) {
		case *tg.UpdateDeleteChannelMessages:
			chatID := update.GetChannelID()
			if !database.Watching(chatID) {
				return dispatcher.SkipCurrentGroup
			}
			return dispatcher.ContinueGroups
		default:
			return dispatcher.SkipCurrentGroup
		}
	}), 1)
	disp.AddHandlerToGroup(handlers.NewAnyUpdate(DeleteHandler), 1)
	disp.AddHandlerToGroup(handlers.NewMessage(filters.Message.All, func(ctx *ext.Context, u *ext.Update) error {
		chatID := u.EffectiveChat().GetID()
		if !database.Watching(chatID) {
			return dispatcher.SkipCurrentGroup
		}
		return dispatcher.ContinueGroups
	}), 2)
	disp.AddHandlerToGroup(handlers.NewMessage(filters.Message.All, WatchHandler), 2)
}

func (u *UserClient) Close() error {
	if u.logger != nil {
		return u.logger.Sync()
	}
	return nil
}

func NewUserClient(ctx context.Context) (*UserClient, error) {
	log.FromContext(ctx).Debug("Initializing user client")
	if UC != nil {
		return UC, nil
	}
	res := make(chan struct {
		client *UserClient
		err    error
	})
	go func() {
		tclientLog := zap.New(zapcore.NewCore(
			zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
			zapcore.AddSync(&lumberjack.Logger{
				Filename:   filepath.Join("data", "logs", "client.jsonl"),
				MaxBackups: 3,
				MaxAge:     7,
			}),
			zap.DebugLevel,
		))
		tclient, err := gotgproto.NewClient(
			config.C.AppID,
			config.C.AppHash,
			gotgproto.ClientTypePhone(""),
			&gotgproto.ClientOpts{
				Session:          sessionMaker.SqlSession(gormlite.Open("data/session_user.db")),
				AuthConversator:  &termialAuthConversator{},
				Logger:           tclientLog,
				Context:          ctx,
				DisableCopyright: true,
				Middlewares:      middlewares.FloodWait(),
			},
		)
		if err != nil {
			res <- struct {
				client *UserClient
				err    error
			}{nil, err}
		}
		res <- struct {
			client *UserClient
			err    error
		}(struct {
			client *UserClient
			err    error
		}{&UserClient{TClient: tclient,
			logger: tclientLog}, nil})
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-res:
		if r.err != nil {
			return nil, r.err
		}
		UC = r.client
		return UC, nil
	}
}
