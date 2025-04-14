package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/charmbracelet/log"
	"github.com/duke-git/lancet/v2/slice"
	"github.com/gotd/td/tg"
	"github.com/krau/btts/config"
	"github.com/krau/btts/types"
	"github.com/krau/btts/utils/tgutil"
	"github.com/meilisearch/meilisearch-go"
)

type MessageDocument struct {
	// Telegram MessageID
	ID   int64 `json:"id"`
	Type int   `json:"type"`
	// The original text of the message
	Message string `json:"message"`
	// The ID of the user who sent the message
	UserID    int64 `json:"user_id"`
	Timestamp int64 `json:"timestamp"`
}

type Engine struct {
	Client meilisearch.ServiceManager
	SelfID int64
}

var EgineInstance *Engine

func NewEngine(ctx context.Context, selfID int64) (*Engine, error) {
	log.FromContext(ctx).Debug("Initializing MeiliSearch engine")
	if EgineInstance != nil {
		return EgineInstance, nil
	}
	sm := meilisearch.New(config.C.Engine.Url, meilisearch.WithAPIKey(config.C.Engine.Key))
	_, err := sm.HealthWithContext(ctx)
	if err != nil {
		return nil, err
	}
	EgineInstance = &Engine{
		Client: sm,
		SelfID: selfID,
	}
	return EgineInstance, nil
}

func (e *Engine) DeleteDocuments(ctx context.Context, chatID int64, ids []int) error {
	indexName := fmt.Sprintf("btts_%d", chatID)
	ids = slice.Compact(ids)
	idsStr := make([]string, len(ids))
	for i, id := range ids {
		idsStr[i] = fmt.Sprintf("%d", id)
	}
	_, err := e.Client.Index(indexName).DeleteDocumentsWithContext(ctx, idsStr)
	return err
}

func (e *Engine) AddDocuments(ctx context.Context, chatID int64, docs []*MessageDocument) error {
	indexName := fmt.Sprintf("btts_%d", chatID)
	docs = slice.Compact(docs)
	jsonData, err := sonic.Marshal(docs)
	if err != nil {
		return err
	}
	_, err = e.Client.Index(indexName).UpdateDocumentsWithContext(ctx, jsonData, "id")
	return err
}

func (e *Engine) AddDocumentsFromMessages(ctx context.Context, chatID int64, messages []*tg.Message) error {
	docs := make([]*MessageDocument, 0)
	for _, message := range messages {
		var userID int64

		chatPeer := message.GetPeerID()
		switch chatPeer := chatPeer.(type) {
		case *tg.PeerUser:
			if message.GetOut() {
				userID = e.SelfID
			} else {
				userID = chatPeer.GetUserID()
			}
		case *tg.PeerChannel:
			if message.GetPost() {
				userID = chatPeer.GetChannelID()
			} else {
				if message.GetOut() {
					userID = e.SelfID
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
			text, mt := tgutil.ExtraMessageMediaText(media)
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
		docs = append(docs, &MessageDocument{
			ID:        int64(message.GetID()),
			Message:   messageText,
			Type:      int(messageType),
			UserID:    userID,
			Timestamp: int64(message.GetDate()),
		})
	}
	return e.AddDocuments(ctx, chatID, docs)
}
