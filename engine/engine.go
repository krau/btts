package engine

import (
	"context"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/gotd/td/tg"
	"github.com/krau/btts/config"
	"github.com/krau/btts/engine/meili"
	"github.com/krau/btts/types"
	"github.com/krau/btts/utils"
	"github.com/meilisearch/meilisearch-go"
)

type Searcher interface {
	CreateIndex(ctx context.Context, chatID int64) error
	DeleteIndex(ctx context.Context, chatID int64) error
	AddDocuments(ctx context.Context, chatID int64, docs []*types.MessageDocument) error
	DeleteDocuments(ctx context.Context, chatID int64, ids []int) error
	Search(ctx context.Context, req types.SearchRequest) (*types.MessageSearchResponse, error)
	GetDocuments(ctx context.Context, chatID int64, ids []int) ([]*types.MessageDocument, error)
}

var _ Searcher = (*meili.Meilisearch)(nil)

var instance Searcher

func GetEngine() Searcher {
	if instance == nil {
		panic("Engine not initialized, call NewEngine first")
	}
	return instance
}

// selfID is the userclient's telegram id
func NewEngine(ctx context.Context, selfID int64) (Searcher, error) {
	if instance != nil {
		return instance, nil
	}
	log.FromContext(ctx).Debug("Initializing searcher")
	sm := meilisearch.New(config.C.Engine.Url, meilisearch.WithAPIKey(config.C.Engine.Key))
	_, err := sm.HealthWithContext(ctx)
	if err != nil {
		return nil, err
	}
	instance = &meili.Meilisearch{
		Client: sm,
		SelfID: selfID,
	}
	return instance, nil
}

func DocumentsFromMessages(ctx context.Context, messages []*tg.Message, self int64) []*types.MessageDocument {
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

		var messageSB strings.Builder
		var messageType types.MessageType
		media, ok := message.GetMedia()
		if ok {
			text, mt := utils.ExtractMessageMediaText(media)
			if text != "" {
				messageSB.WriteString(text)
			}
			messageType = mt
		}
		messageSB.WriteString(message.GetMessage())
		messageText := messageSB.String()
		if messageText == "" {
			continue
		}
		docs = append(docs, &types.MessageDocument{
			ID:        int64(message.GetID()),
			Message:   messageText,
			Type:      int(messageType),
			UserID:    userID,
			Timestamp: int64(message.GetDate()),
		})
	}
	return docs
}
