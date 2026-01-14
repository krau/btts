package userclient

import (
	"context"
	"path/filepath"
	"sync"
	"time"

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
	"github.com/krau/btts/utils"
	"github.com/ncruces/go-sqlite3/gormlite"
)

var uc *UserClient

func GetUserClient() *UserClient {
	if uc == nil {
		panic("UserClient is not initialized, call NewUserClient first")
	}
	return uc
}

type UserClient struct {
	TClient           *gotgproto.Client
	logger            *zap.Logger
	GlobalIgnoreUsers []int64
	ectx              *ext.Context // created by TClient.CreateContext()
	mu                sync.Mutex
}

func (u *UserClient) GetContext() *ext.Context {
	if u.ectx == nil {
		u.ectx = u.TClient.CreateContext()
	}
	return u.ectx
}

func (u *UserClient) StartWatch(ctx context.Context) {
	// 启动时同步错过的消息
	if err := u.SyncMissedUpdates(ctx); err != nil {
		log.FromContext(ctx).Error("Failed to sync missed updates", "error", err)
	}
	disp := u.TClient.Dispatcher
	disp.AddHandlerToGroup(handlers.NewAnyUpdate(func(ctx *ext.Context, u *ext.Update) error {
		switch update := u.UpdateClass.(type) {
		case *tg.UpdateDeleteChannelMessages:
			chatID := update.GetChannelID()
			if !database.Watching(chatID) {
				return dispatcher.SkipCurrentGroup
			}
			return dispatcher.ContinueGroups
		case *tg.UpdateChannelParticipant:
			chatID := update.GetChannelID()
			if chatID == 0 || !database.Watching(chatID) {
				return dispatcher.SkipCurrentGroup
			}
			_, ok1 := update.GetPrevParticipant()
			now, ok2 := update.GetNewParticipant()
			var userId int64
			if ok1 && !ok2 {
				// user left
				userId = update.GetUserID()
			} else if ok1 && ok2 {
				switch now.(type) {
				case *tg.ChannelParticipantBanned:
					// user was banned
					userId = update.GetUserID()
				case *tg.ChannelParticipantLeft:
					// user left
					userId = update.GetUserID()
				}
			}
			if userId == 0 {
				return dispatcher.SkipCurrentGroup
			}
			user, err := database.GetUserInfo(ctx, chatID)
			if err != nil {
				log.FromContext(ctx).Error("Failed to get user info", "chat_id", chatID, "error", err)
				return dispatcher.SkipCurrentGroup
			}
			database.RemoveMemberFromIndexChat(ctx, chatID, user)
			return dispatcher.SkipCurrentGroup
		default:
			return dispatcher.SkipCurrentGroup
		}
	}), 1)
	// 添加状态更新 handler
	disp.AddHandlerToGroup(handlers.NewAnyUpdate(func(ctx *ext.Context, u *ext.Update) error {
		uc.updateStateFromUpdates(ctx, u.UpdateClass)
		return dispatcher.ContinueGroups
	}), 1)
	disp.AddHandlerToGroup(handlers.NewAnyUpdate(DeleteHandler), 1)
	disp.AddHandlerToGroup(handlers.NewMessage(filters.Message.All, func(ctx *ext.Context, u *ext.Update) error {
		if u.EffectiveMessage == nil || u.EffectiveMessage.Message == nil {
			return dispatcher.SkipCurrentGroup
		}
		if u.EffectiveMessage.IsService {
			return dispatcher.SkipCurrentGroup
		}
		if u.Entities == nil || u.Entities.Short {
			u = ext.GetNewUpdate(ctx, ctx.Raw, ctx.Self.ID, ctx.PeerStorage, u.Entities, u.UpdateClass)
		}
		chatID := u.EffectiveChat().GetID()
		if chatID == 0 {
			if u.Entities == nil || !u.Entities.Short || !u.EffectiveChat().IsAUser() {
				log.FromContext(ctx).Error("Unexpected zero chat ID", "entities", u.Entities, "update", u)
				return dispatcher.SkipCurrentGroup
			}
			pu := utils.GetUpdatePeerUser(u)
			if pu == nil {
				log.FromContext(ctx).Error("Failed to get PeerUser from update", "update", u)
				return dispatcher.SkipCurrentGroup
			}
			chatID = pu.GetUserID()
		}
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

func (u *UserClient) AddGlobalIgnoreUser(userID int64) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.GlobalIgnoreUsers = append(u.GlobalIgnoreUsers, userID)
}

func (u *UserClient) RemoveGlobalIgnoreUser(userID int64) {
	u.mu.Lock()
	defer u.mu.Unlock()
	for i, id := range u.GlobalIgnoreUsers {
		if id == userID {
			u.GlobalIgnoreUsers = append(u.GlobalIgnoreUsers[:i], u.GlobalIgnoreUsers[i+1:]...)
			break
		}
	}
}

func NewUserClient(ctx context.Context) (*UserClient, error) {
	log.FromContext(ctx).Debug("Initializing user client")
	if uc != nil {
		return uc, nil
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
				AuthConversator:  &terminalAuthConversator{},
				Logger:           tclientLog,
				Context:          ctx,
				DisableCopyright: true,
				Middlewares:      middlewares.NewDefaultMiddlewares(ctx, 5*time.Minute),
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
		}{&UserClient{
			TClient:           tclient,
			logger:            tclientLog,
			GlobalIgnoreUsers: make([]int64, 0),
			ectx:              tclient.CreateContext(),
		}, nil})
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-res:
		if r.err != nil {
			return nil, r.err
		}
		uc = r.client
		return uc, nil
	}
}
