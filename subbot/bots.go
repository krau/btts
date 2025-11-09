package subbot

import (
	"context"
	"fmt"
	"time"

	"github.com/celestix/gotgproto"
	"github.com/celestix/gotgproto/dispatcher/handlers"
	"github.com/celestix/gotgproto/dispatcher/handlers/filters"
	"github.com/celestix/gotgproto/sessionMaker"
	"github.com/celestix/gotgproto/types"
	"github.com/charmbracelet/log"
	"github.com/gotd/td/tg"
	"github.com/krau/btts/config"
	"github.com/krau/btts/database"
	"github.com/krau/btts/middlewares"
	"github.com/krau/btts/utils"
	"github.com/ncruces/go-sqlite3/gormlite"
	"golang.org/x/sync/errgroup"
)

type SubBot struct {
	Client *gotgproto.Client
	ID     int64
	Name   string
}

func (s *SubBot) Start() {
	disp := s.Client.Dispatcher
	disp.AddHandler(handlers.NewCommand("start", StartHandler))
	disp.AddHandler(handlers.NewCommand("help", StartHandler))
	disp.AddHandler(handlers.NewCommand("search", SearchHandler))
	disp.AddHandler(handlers.NewCallbackQuery(filters.CallbackQuery.Prefix("search"), SearchCallbackHandler))
	disp.AddHandler(handlers.NewCallbackQuery(filters.CallbackQuery.Prefix("filter"), FilterCallbackHandler))
	disp.AddHandler(handlers.NewMessage(filters.Message.ChatType(filters.ChatTypeUser), SearchHandler))
	disp.AddHandler(handlers.NewMessage(func(m *types.Message) bool {
		// 处理在群组中回复 bot 消息的情况
		if m == nil || m.ReplyToMessage == nil || m.ReplyToMessage.FromID == nil {
			return false
		}
		peer := m.ReplyToMessage.FromID
		switch p := peer.(type) {
		case *tg.PeerUser:
			// bot 被回复
			return p.GetUserID() == s.Client.Self.ID
		default:
			return false
		}
	}, SearchHandler))
	disp.AddHandlerToGroup(handlers.NewInlineQuery(filters.InlineQuery.All, InlineQueryHandler), 1)
}

func (s *SubBot) Stop() {
	if s.Client != nil {
		s.Client.Stop()
	}
}

var subBots = make(map[int64]*SubBot)

type SubBotKey struct{}

func NewSubBot(ctx context.Context, token string, chats []int64) (*SubBot, error) {
	session := utils.MD5Hash(token)
	ctx = context.WithValue(ctx, SubBotKey{}, session)
	log := log.FromContext(ctx)
	log.Debugf("Initializing sub bot %s", session)
	res := make(chan struct {
		client *gotgproto.Client
		err    error
	})
	go func() {
		tclient, err := gotgproto.NewClient(
			config.C.AppID,
			config.C.AppHash,
			gotgproto.ClientTypeBot(token),
			&gotgproto.ClientOpts{
				Session:          sessionMaker.SqlSession(gormlite.Open(fmt.Sprintf("data/session_%s.db", session))),
				DisableCopyright: true,
				Context:          ctx,
				Middlewares:      middlewares.NewDefaultMiddlewares(ctx, 5*time.Minute),
				AutoFetchReply:   true,
			},
		)
		if err != nil {
			log.Errorf("Failed to create sub bot: %v", err)
			res <- struct {
				client *gotgproto.Client
				err    error
			}{nil, err}
			return
		}
		if tclient.Self.ID == 0 {
			log.Errorf("Failed to get sub bot ID")
			res <- struct {
				client *gotgproto.Client
				err    error
			}{nil, fmt.Errorf("failed to get sub bot ID")}
			return
		}
		err = database.UpsertSubBot(ctx, &database.SubBot{
			Token:   token,
			ChatIDs: chats,
			BotID:   tclient.Self.ID,
		})
		if err != nil {
			log.Errorf("Failed to upsert sub bot: %v", err)
			res <- struct {
				client *gotgproto.Client
				err    error
			}{nil, err}
			return
		}
		tclient.API().BotsSetBotCommands(ctx, &tg.BotsSetBotCommandsRequest{
			Scope: &tg.BotCommandScopeDefault{},
			Commands: []tg.BotCommand{
				{Command: "search", Description: "搜索消息"},
				{Command: "start", Description: "开始使用"},
				{Command: "help", Description: "帮助"},
			},
		})
		res <- struct {
			client *gotgproto.Client
			err    error
		}{client: tclient, err: nil}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-res:
		if r.err != nil {
			log.Errorf("Failed to create sub bot: %v", r.err)
			return nil, r.err
		}
		if r.client == nil || r.client.Self.Username == "" {
			log.Errorf("Failed to get sub bot username")
			return nil, fmt.Errorf("failed to get sub bot username")
		}
		log.Debugf("Sub bot %s created", r.client.Self.Username)
		b := &SubBot{
			Client: r.client,
			ID:     r.client.Self.ID,
			Name:   r.client.Self.Username,
		}
		subBots[r.client.Self.ID] = b
		return b, nil
	}
}

func GetSubBot(ctx context.Context, botID int64) (*SubBot, error) {
	log := log.FromContext(ctx)
	bot, ok := subBots[botID]
	if !ok {
		log.Errorf("Sub bot %d not found", botID)
		return nil, fmt.Errorf("sub bot %d not found", botID)
	}
	return bot, nil
}

func DelSubBot(ctx context.Context, botID int64) error {
	log := log.FromContext(ctx)
	log.Debugf("Deleting sub bot %d", botID)
	bot, ok := subBots[botID]
	if !ok {
		log.Errorf("Sub bot %d not found", botID)
		return fmt.Errorf("sub bot %d not found", botID)
	}
	bot.Stop()
	delete(subBots, botID)
	err := database.DeleteSubBot(ctx, botID)
	if err != nil {
		log.Errorf("Failed to delete sub bot: %v", err)
		return err
	}
	log.Debugf("Sub bot %d deleted", botID)
	return nil
}

func GetAll(ctx context.Context) []*SubBot {
	var subBotsList []*SubBot
	for _, bot := range subBots {
		subBotsList = append(subBotsList, bot)
	}
	return subBotsList
}

func StartStored(ctx context.Context) (map[int64]*SubBot, error) {
	bots, err := database.GetAllSubBots(ctx)
	if err != nil {
		log.FromContext(ctx).Errorf("Failed to get sub bots: %v", err)
		return nil, err
	}
	eg, ectx := errgroup.WithContext(ctx)
	for _, bot := range bots {
		eg.Go(func() error {
			if bot.Token == "" {
				return fmt.Errorf("sub bot %d has no token", bot.BotID)
			}
			log.FromContext(ectx).Debugf("Starting sub bot %d", bot.BotID)
			subBot, err := NewSubBot(ectx, bot.Token, bot.ChatIDs)
			if err != nil {
				return fmt.Errorf("failed to start sub bot %d: %v", bot.BotID, err)
			}
			subBot.Start()
			subBots[subBot.ID] = subBot
			log.FromContext(ectx).Debugf("Sub bot %s started", subBot.Name)
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	return subBots, nil
}
