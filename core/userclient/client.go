package userclient

import (
	"context"
	"path/filepath"

	_ "github.com/ncruces/go-sqlite3/embed"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	lumberjack "gopkg.in/natefinch/lumberjack.v2"

	"github.com/celestix/gotgproto"
	"github.com/celestix/gotgproto/sessionMaker"
	"github.com/krau/btts/config"
	"github.com/krau/btts/core/middlewares"
	"github.com/ncruces/go-sqlite3/gormlite"
)

type UserClient struct {
	TClient *gotgproto.Client
	logger  *zap.Logger
}

func (u *UserClient) Close() error {
	if u.logger != nil {
		return u.logger.Sync()
	}
	return nil
}

func NewUserClient(ctx context.Context) (*UserClient, error) {
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
		return r.client, nil
	}
}
