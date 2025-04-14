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
	"github.com/krau/btts/utils"
	"github.com/meilisearch/meilisearch-go"
)

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

func (e *Engine) Search(ctx context.Context, chatID int64, query string, offset, limit int64) (*types.MessageSearchResponse, error) {
	indexName := fmt.Sprintf("btts_%d", chatID)
	if limit == 0 {
		limit = 10
	}
	if offset == 0 {
		offset = 0
	}
	resp, err := e.Client.Index(indexName).SearchWithContext(ctx, query, &meilisearch.SearchRequest{
		Offset: offset,
		Limit:  limit,
		AttributesToSearchOn: []string{
			"message",
		},
		AttributesToCrop: []string{
			"message",
		},
	})
	if err != nil {
		return nil, err
	}
	hisBytes, err := sonic.Marshal(resp.Hits)
	if err != nil {
		return nil, err
	}
	var hits []types.SearchHit
	err = sonic.Unmarshal(hisBytes, &hits)
	if err != nil {
		return nil, err
	}
	return &types.MessageSearchResponse{
		Raw:                resp,
		Hits:               hits,
		EstimatedTotalHits: resp.EstimatedTotalHits,
		ProcessingTimeMs:   resp.ProcessingTimeMs,
		Offset:             resp.Offset,
		Limit:              resp.Limit,
	}, nil
}

func (e *Engine) MultiSearch(ctx context.Context, chatIDs []int64, query string, offset, limit int64) (*types.MessageSearchResponse, error) {
	if limit == 0 {
		limit = 10
	}
	if offset == 0 {
		offset = 0
	}
	multiQueries := make([]*meilisearch.SearchRequest, len(chatIDs))
	for i, chatID := range chatIDs {
		indexName := fmt.Sprintf("btts_%d", chatID)
		multiQueries[i] = &meilisearch.SearchRequest{
			IndexUID: indexName,
			Query:    query,
			AttributesToSearchOn: []string{
				"message",
			},
			AttributesToCrop: []string{
				"message",
			},
		}
	}
	resp, err := e.Client.MultiSearchWithContext(ctx, &meilisearch.MultiSearchRequest{
		Federation: &meilisearch.MultiSearchFederation{
			Offset: offset,
			Limit:  limit,
		},
		Queries: multiQueries,
	})
	if err != nil {
		return nil, err
	}
	hisBytes, err := sonic.Marshal(resp.Hits)
	if err != nil {
		return nil, err
	}
	var hits []types.SearchHit
	err = sonic.Unmarshal(hisBytes, &hits)
	if err != nil {
		return nil, err
	}
	return &types.MessageSearchResponse{
		Raw:                resp,
		Hits:               hits,
		EstimatedTotalHits: resp.EstimatedTotalHits,
		ProcessingTimeMs:   resp.ProcessingTimeMs,
		Offset:             resp.Offset,
		Limit:              resp.Limit,
	}, nil
}

func (e *Engine) CreateIndex(ctx context.Context, chatID int64) error {
	indexName := fmt.Sprintf("btts_%d", chatID)
	_, err := e.Client.CreateIndexWithContext(ctx, &meilisearch.IndexConfig{
		Uid:        indexName,
		PrimaryKey: "id",
	})
	if err != nil {
		return err
	}
	index := e.Client.Index(indexName)
	_, err = index.UpdateSettingsWithContext(ctx, &meilisearch.Settings{
		FilterableAttributes: []string{
			"user_id",
			"type",
		},
		SortableAttributes: []string{
			"timestamp",
			"id",
		},
		SearchableAttributes: []string{
			"message",
			"user_id",
		},
	})
	if err != nil {
		return err
	}
	if config.C.Engine.Embedder.Name != "" {
		embedSettings := config.C.Engine.Embedder
		embedder := meilisearch.Embedder{
			Source:           embedSettings.Source,
			APIKey:           embedSettings.ApiKey,
			Dimensions:       embedSettings.Dimensions,
			DocumentTemplate: embedSettings.DocumentTemplate,
			URL:              embedSettings.URL,
		}
		if embedSettings.Source == "rest" {
			embedder.Request = map[string]any{
				"input": []any{
					"{{text}}", "{{..}}",
				},
				"model": embedSettings.Model,
			}
			embedder.Response = map[string]any{
				"data": []any{
					map[string]any{
						"embedding": "{{embedding}}",
					},
					"{{..}}",
				},
			}
		} else {
			embedder.Model = embedSettings.Model
		}
		_, err = index.UpdateEmbeddersWithContext(ctx, map[string]meilisearch.Embedder{
			config.C.Engine.Embedder.Name: embedder,
		})
	}
	return err
}

func (e *Engine) DeleteIndex(ctx context.Context, chatID int64) error {
	indexName := fmt.Sprintf("btts_%d", chatID)
	_, err := e.Client.DeleteIndexWithContext(ctx, indexName)
	return err
}

func (e *Engine) AddDocuments(ctx context.Context, chatID int64, docs []*types.MessageDocument) error {
	docs = slice.Compact(docs)
	for i := range docs {
		docs[i].ChatID = chatID
	}
	jsonData, err := sonic.Marshal(docs)
	if err != nil {
		return err
	}
	if len(docs) == 0 {
		return nil
	}
	indexName := fmt.Sprintf("btts_%d", chatID)
	_, err = e.Client.Index(indexName).UpdateDocumentsWithContext(ctx, jsonData, "id")
	return err
}

func (e *Engine) AddDocumentsFromMessages(ctx context.Context, chatID int64, messages []*tg.Message) error {
	docs := make([]*types.MessageDocument, 0)
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
			text, mt := utils.ExtraMessageMediaText(media)
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
	return e.AddDocuments(ctx, chatID, docs)
}
