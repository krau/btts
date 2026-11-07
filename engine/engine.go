package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"

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
	UpdateDocuments(ctx context.Context, chatID int64, docs []*types.MessageDocument) error
	DeleteDocuments(ctx context.Context, chatID int64, messageIds []int) error
	Search(ctx context.Context, req types.SearchRequest) (*types.SearchResponse, error)
	SearchImage(ctx context.Context, req types.SearchRequest, data []byte) (*types.SearchResponse, error)
	GetDocuments(ctx context.Context, chatID int64, messageIds []int) ([]*types.MessageDocument, error)
	Capabilities() types.Capabilities
	// Enrichment returns the media enrichment pipeline, or nil when disabled.
	Enrichment() types.MediaEnricher
}

var _ Searcher = (*meili.Meilisearch)(nil)

// var _ Searcher = (*bleve.BleveSearcher)(nil)

var (
	instanceMu sync.RWMutex
	instance   Searcher
)

func GetEngine() Searcher {
	instanceMu.RLock()
	defer instanceMu.RUnlock()
	if instance == nil {
		panic("Engine not initialized, call NewEngine first")
	}
	return instance
}

// SetEngine replaces the global engine, e.g. with an enrichment wrapper.
func SetEngine(searcher Searcher) {
	instanceMu.Lock()
	defer instanceMu.Unlock()
	instance = searcher
}

// selfID is the userclient's telegram id
func NewEngine(ctx context.Context) (Searcher, error) {
	instanceMu.RLock()
	current := instance
	instanceMu.RUnlock()
	if current != nil {
		return current, nil
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
		searcher := &meili.Meilisearch{
			Client:    sm,
			Index:     config.C.Engine.Index,
			WaitTasks: config.C.Enrich.WaitIndexTasks,
		}
		SetEngine(searcher)
		log.FromContext(ctx).Info("Meilisearch engine initialized")

	case "bleve":
		panic("not impl")
	default:
		return nil, fmt.Errorf("unsupported engine type: %s (supported: meilisearch, bleve)", config.C.Engine.Type)
	}

	instanceMu.RLock()
	defer instanceMu.RUnlock()
	return instance, nil
}

// DocumentsFromMessages converts Telegram messages into index documents.
// Media-only messages are kept only when enricher reports that their media
// will be enriched; otherwise they would be unsearchable noise.
func DocumentsFromMessages(ctx context.Context,
	messages []*tg.Message,
	chatID, self int64,
	ectx *ext.Context,
	enableOcr bool,
	enricher types.MediaEnricher) []*types.MessageDocument {
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
		var mediaMIME, mediaName string
		media, ok := message.GetMedia()
		if ok {
			result := utils.ExtractMessageMediaText(ctx, ectx, media, enableOcr)
			if result != nil {
				msb.WriteString(result.Text)
				ocred = result.Ocred
				messageType = result.Type
			}
			mediaMIME, mediaName = MediaMeta(media)
		}
		hasMedia := mediaMIME != "" && enricher != nil &&
			enricher.EnrichableMedia(mediaMIME, mediaName, messageType)
		msb.WriteString(message.GetMessage())
		messageText := msb.String()
		if messageText == "" && ocred == "" && !hasMedia {
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
			HasMedia:  hasMedia,
			MediaMIME: mediaMIME,
			MediaName: mediaName,
		})
	}
	return docs
}

// MarkImported marks documents from a cold-history import as background work.
func MarkImported(docs []*types.MessageDocument) {
	for _, doc := range docs {
		if doc != nil {
			doc.Priority = 1
		}
	}
}

// MediaMeta returns the MIME type and original file name of a message's media.
// Stickers and animations return empty values so they are never downloaded.
func MediaMeta(media tg.MessageMediaClass) (mime, name string) {
	switch m := media.(type) {
	case *tg.MessageMediaPhoto:
		return "image/jpeg", ""
	case *tg.MessageMediaDocument:
		docClass, ok := m.GetDocument()
		if !ok {
			return "", ""
		}
		doc, ok := docClass.AsNotEmpty()
		if !ok {
			return "", ""
		}
		mime = doc.GetMimeType()
		for _, attr := range doc.GetAttributes() {
			switch a := attr.(type) {
			case *tg.DocumentAttributeAnimated, *tg.DocumentAttributeSticker:
				return "", ""
			case *tg.DocumentAttributeFilename:
				name = a.GetFileName()
			case *tg.DocumentAttributeAudio:
				if a.GetVoice() && mime == "" {
					mime = "audio/ogg"
				}
			}
		}
		return types.EffectiveMIME(mime, name), name
	}
	return "", ""
}
