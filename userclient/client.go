package userclient

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	lumberjack "gopkg.in/natefinch/lumberjack.v2"

	"github.com/charmbracelet/log"
	"github.com/gotd/td/tg"
	"github.com/krau/btts/config"
	"github.com/krau/btts/database"
	"github.com/krau/btts/middlewares"
	"github.com/krau/btts/userclient/plugin"
	"github.com/krau/btts/utils"
	"github.com/krau/mygotg"
	"github.com/krau/mygotg/dispatcher"
	"github.com/krau/mygotg/dispatcher/handlers"
	"github.com/krau/mygotg/dispatcher/handlers/filters"
	"github.com/krau/mygotg/ext"
	"github.com/krau/mygotg/session"
	"github.com/krau/mygotg/types"
	"github.com/ncruces/go-sqlite3/gormlite"
)

var uc *UserClient
var clientMu sync.Mutex

func GetUserClient() *UserClient {
	clientMu.Lock()
	defer clientMu.Unlock()
	if uc == nil {
		panic("UserClient is not initialized, call NewUserClient first")
	}
	return uc
}

type UserClient struct {
	TClient           *mygotg.Client
	logger            *zap.Logger
	GlobalIgnoreUsers []int64
	ectx              *ext.Context // created by TClient.CreateContext()
	mu                sync.Mutex
	noUpdates         bool
}

func (u *UserClient) GetContext() *ext.Context {
	if u.ectx == nil {
		u.ectx = u.TClient.CreateContext()
	}
	return u.ectx
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
	disp.AddHandlerToGroup(handlers.NewAnyUpdate(DeleteHandler), 1)
	disp.AddHandlerToGroup(handlers.NewMessage(filters.Message.All, func(ctx *ext.Context, u *ext.Update) error {
		if u.EffectiveMessage == nil || u.EffectiveMessage.Message == nil {
			return dispatcher.SkipCurrentGroup
		}
		if u.EffectiveMessage.IsService {
			return dispatcher.SkipCurrentGroup
		}
		// 对于实体较短的更新，重新获取完整更新以确保包含必要的实体信息
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

	// plugins
	disp.AddHandlerToGroup(handlers.NewMessage(func(m *types.Message) bool {
		if m == nil {
			return false
		}
		if !config.C.Plugin.Enable {
			return false
		}
		return true
	}, func(ctx *ext.Context, u *ext.Update) error {
		if err := plugin.Dispatcher(ctx, u); err != nil {
			log.FromContext(ctx).Error("Plugin dispatcher error", "error", err)
		}
		return dispatcher.SkipCurrentGroup
	}), 3)
}

func (u *UserClient) Close() error {
	clientMu.Lock()
	defer clientMu.Unlock()
	if u.TClient != nil {
		u.TClient.Stop()
	}
	if uc == u {
		uc = nil
	}
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

// Option customises how the user client is created.
type Option func(*clientConfig)

type clientConfig struct {
	noUpdates bool
}

// WithNoUpdates disables updates for export-only clients; it cannot reuse an update-enabled client.
func WithNoUpdates() Option {
	return func(c *clientConfig) { c.noUpdates = true }
}

func NewUserClient(ctx context.Context, options ...Option) (*UserClient, error) {
	log.FromContext(ctx).Debug("Initializing user client")
	clientMu.Lock()
	defer clientMu.Unlock()
	cfg := &clientConfig{}
	for _, option := range options {
		option(cfg)
	}
	if uc != nil {
		if uc.noUpdates != cfg.noUpdates {
			return nil, fmt.Errorf("user client already initialized with NoUpdates=%t; requested NoUpdates=%t", uc.noUpdates, cfg.noUpdates)
		}
		return uc, nil
	}
	clientLogLevel := zap.InfoLevel
	if config.C.ClientLogLevel != "" {
		if err := clientLogLevel.UnmarshalText([]byte(config.C.ClientLogLevel)); err != nil {
			return nil, fmt.Errorf("invalid client_log_level: %w", err)
		}
	}
	res := make(chan struct {
		client *UserClient
		err    error
	}, 1)
	go func() {
		tclientLog := zap.New(zapcore.NewCore(
			zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
			zapcore.AddSync(&lumberjack.Logger{
				Filename:   filepath.Join("data", "logs", "client.jsonl"),
				MaxBackups: 7,
				MaxSize:    10,
				MaxAge:     7,
			}),
			clientLogLevel,
		))
		tclient, err := mygotg.NewClient(
			config.C.AppID,
			config.C.AppHash,
			mygotg.ClientTypePhone(""),
			&mygotg.ClientOpts{
				Session:          session.SqlSession(gormlite.Open("data/session_user.db")),
				AuthConversator:  &terminalAuthConversator{},
				Logger:           tclientLog,
				Context:          ctx,
				DisableCopyright: true,
				Middlewares:      middlewares.NewDefaultMiddlewares(ctx, 5*time.Minute),
				AutoFetchReply:   true,
				// Consumers must register before recovery can advance persisted cursors.
				DeferUpdateRecovery: true,
				// Export-only clients must not buffer unconsumed updates.
				NoUpdates: cfg.noUpdates,
				// Persist cursors across restarts.
				UpdateStateStorage: database.NewUpdatesStorage(),
			},
		)
		if err != nil {
			res <- struct {
				client *UserClient
				err    error
			}{nil, err}
			return
		}
		res <- struct {
			client *UserClient
			err    error
		}{&UserClient{
			TClient:           tclient,
			logger:            tclientLog,
			GlobalIgnoreUsers: make([]int64, 0),
			ectx:              tclient.CreateContext(),
			noUpdates:         cfg.noUpdates,
		}, nil}
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
