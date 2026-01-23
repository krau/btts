package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/gotd/td/tg"
	"github.com/krau/btts/config"
	"github.com/krau/btts/engine/meili"
	"github.com/krau/btts/types"
	"github.com/krau/btts/utils"
	"github.com/krau/mygotg/ext"
	"github.com/meilisearch/meilisearch-go"
)

type Searcher interface {
	CreateIndex(ctx context.Context, chatID int64) error
	DeleteIndex(ctx context.Context, chatID int64) error
	AddDocuments(ctx context.Context, chatID int64, docs []*types.MessageDocument) error
	DeleteDocuments(ctx context.Context, chatID int64, messageIds []int) error
	Search(ctx context.Context, req types.SearchRequest) (*types.SearchResponse, error)
	GetDocuments(ctx context.Context, chatID int64, messageIds []int) ([]*types.MessageDocument, error)
}

var _ Searcher = (*meili.Meilisearch)(nil)

// var _ Searcher = (*bleve.BleveSearcher)(nil)

var instance Searcher

func GetEngine() Searcher {
	if instance == nil {
		panic("Engine not initialized, call NewEngine first")
	}
	return instance
}

// selfID is the userclient's telegram id
func NewEngine(ctx context.Context) (Searcher, error) {
	if instance != nil {
		return instance, nil
	}
	log.FromContext(ctx).Debug("Initializing searcher", "engine_type", config.C.Engine.Type)

	var err error
	engineType := strings.ToLower(config.C.Engine.Type)
	if engineType == "" {
		engineType = "meilisearch" // 默认使用 Meilisearch
	}

	switch engineType {
	case "meilisearch":
		sm := meilisearch.New(config.C.Engine.Url, meilisearch.WithAPIKey(config.C.Engine.Key))
		_, err = sm.HealthWithContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("meilisearch health check failed: %w", err)
		}
		instance = &meili.Meilisearch{
			Client: sm,
			Index:  config.C.Engine.Index,
		}
		log.FromContext(ctx).Info("Meilisearch engine initialized")

	case "bleve":
		// indexPath := config.C.Engine.Path
		// if indexPath == "" {
		// 	indexPath = "data/bleve_indexes" // 默认路径
		// }
		// instance, err = bleve.NewBleveSearcher(indexPath)
		// if err != nil {
		// 	return nil, fmt.Errorf("failed to initialize bleve: %w", err)
		// }
		// log.FromContext(ctx).Info("Bleve engine initialized", "index_path", indexPath)
		panic("not impl")
	default:
		return nil, fmt.Errorf("unsupported engine type: %s (supported: meilisearch, bleve)", config.C.Engine.Type)
	}

	return instance, nil
}

func DocumentsFromMessages(ctx context.Context, messages []*tg.Message, chatID, self int64, ectx *ext.Context, downloadMedia bool) []*types.MessageDocument {
	docs := make([]*types.MessageDocument, 0, len(messages))
	for _, message := range messages {
		var userID int64

		chatPeer := message.GetPeerID()
		switch chatPeer := chatPeer.(type) {
		case *tg.PeerUser:
			if message.GetOut() {
				userID = self
			} else {
				userID = chatPeer.GetUserID()
			}
		case *tg.PeerChannel:
			if message.GetPost() {
				userID = chatPeer.GetChannelID()
			} else {
				if message.GetOut() {
					userID = self
				} else {
					inputPeer := message.FromID
					switch inp := inputPeer.(type) {
					case *tg.PeerChat:
						userID = inp.GetChatID()
					case *tg.PeerUser:
						userID = inp.GetUserID()
					case *tg.PeerChannel:
						userID = inp.GetChannelID()
					}
				}
			}
		}
		if userID == 0 {
			log.FromContext(ctx).Debug("UserID is 0, skipping message", "message_id", message.GetID())
			continue
		}

		var msb strings.Builder
		var messageType types.MessageType
		var ocred string
		media, ok := message.GetMedia()
		if ok {
			result := utils.ExtractMessageMediaText(ctx, ectx, media, downloadMedia)
			if result != nil {
				msb.WriteString(result.Text)
				ocred = result.Ocred
				messageType = result.Type
			}
		}
		msb.WriteString(message.GetMessage())
		messageText := msb.String()
		if messageText == "" && ocred == "" {
			continue
		}
		docs = append(docs, &types.MessageDocument{
			ID:        int64(message.GetID()),
			Message:   messageText,
			Ocred:     ocred,
			Type:      int(messageType),
			UserID:    userID,
			ChatID:    chatID,
			Timestamp: int64(message.GetDate()),
		})
	}
	return docs
}
