package meili

import (
	"context"
	"fmt"

	"github.com/bytedance/sonic"
	"github.com/charmbracelet/log"
	"github.com/duke-git/lancet/v2/slice"
	"github.com/krau/btts/types"
	"github.com/meilisearch/meilisearch-go"
)

type Meilisearch struct {
	Client meilisearch.ServiceManager
}

// AddDocuments implements engine.Searcher.
func (m *Meilisearch) AddDocuments(ctx context.Context, chatID int64, docs []*types.MessageDocument) error {
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
	primaryKey := "id"
	_, err = m.Client.Index(indexName).UpdateDocumentsWithContext(ctx, jsonData, &primaryKey)
	return err
}

// CreateIndex implements engine.Searcher.
func (m *Meilisearch) CreateIndex(ctx context.Context, chatID int64) error {
	indexName := fmt.Sprintf("btts_%d", chatID)
	_, err := m.Client.CreateIndexWithContext(ctx, &meilisearch.IndexConfig{
		Uid:        indexName,
		PrimaryKey: "id",
	})
	if err != nil {
		return err
	}
	index := m.Client.Index(indexName)
	_, err = index.UpdateSettingsWithContext(ctx, &meilisearch.Settings{
		FilterableAttributes: []string{
			"user_id",
			"type",
			"timestamp",
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
	// [TODO] Embedder support
	// if config.C.Engine.Embedder.Name != "" {
	// 	embedSettings := config.C.Engine.Embedder
	// 	embedder := meilisearch.Embedder{
	// 		Source:           meilisearch.EmbedderSource(embedSettings.Source),
	// 		APIKey:           embedSettings.ApiKey,
	// 		Dimensions:       embedSettings.Dimensions,
	// 		DocumentTemplate: embedSettings.DocumentTemplate,
	// 		URL:              embedSettings.URL,
	// 	}
	// 	if embedSettings.Source == "rest" {
	// 		embedder.Request = map[string]any{
	// 			"input": []any{
	// 				"{{text}}", "{{..}}",
	// 			},
	// 			"model": embedSettings.Model,
	// 		}
	// 		embedder.Response = map[string]any{
	// 			"data": []any{
	// 				map[string]any{
	// 					"embedding": "{{embedding}}",
	// 				},
	// 				"{{..}}",
	// 			},
	// 		}
	// 		embedder.Headers = map[string]string{
	// 			"Content-Type": "application/json",
	// 		}
	// 	} else {
	// 		embedder.Model = embedSettings.Model
	// 	}
	// 	_, err = index.UpdateEmbeddersWithContext(ctx, map[string]meilisearch.Embedder{
	// 		config.C.Engine.Embedder.Name: embedder,
	// 	})
	// }
	return err
}

// DeleteDocuments implements engine.Searcher.
func (m *Meilisearch) DeleteDocuments(ctx context.Context, chatID int64, ids []int) error {
	indexName := fmt.Sprintf("btts_%d", chatID)
	ids = slice.Compact(ids)
	idsStr := make([]string, len(ids))
	for i, id := range ids {
		idsStr[i] = fmt.Sprintf("%d", id)
	}
	_, err := m.Client.Index(indexName).DeleteDocumentsWithContext(ctx, idsStr)
	return err
}

// DeleteIndex implements engine.Searcher.
func (m *Meilisearch) DeleteIndex(ctx context.Context, chatID int64) error {
	indexName := fmt.Sprintf("btts_%d", chatID)
	_, err := m.Client.DeleteIndexWithContext(ctx, indexName)
	return err
}

// GetDocuments implements engine.Searcher.
func (m *Meilisearch) GetDocuments(ctx context.Context, chatID int64, ids []int) ([]*types.MessageDocument, error) {
	var resp meilisearch.DocumentsResult
	idsStr := slice.Map(ids, func(i int, item int) string {
		return fmt.Sprintf("%d", item)
	})
	err := m.Index(chatID).GetDocumentReader().GetDocumentsWithContext(ctx, &meilisearch.DocumentsQuery{
		Ids: idsStr,
	}, &resp)
	if err != nil {
		return nil, err
	}
	hitBytes, err := sonic.Marshal(resp.Results)
	if err != nil {
		return nil, err
	}
	var hits []*types.MessageDocument
	err = sonic.Unmarshal(hitBytes, &hits)
	if err != nil {
		return nil, err
	}
	return hits, nil
}

// Search implements engine.Searcher.
func (m *Meilisearch) Search(ctx context.Context, req types.SearchRequest) (*types.MessageSearchResponse, error) {
	if len(req.ChatIDs) > 0 {
		return m.multiSearch(ctx, req)
	}
	if req.ChatID == 0 {
		return nil, fmt.Errorf("ChatID is required")
	}
	indexName := fmt.Sprintf("btts_%d", req.ChatID)
	limit := req.Limit
	offset := req.Offset
	if limit == 0 {
		limit = types.PER_SEARCH_LIMIT
	}
	if offset == 0 {
		offset = 0
	}
	request := &meilisearch.SearchRequest{
		Offset: offset,
		Limit:  limit,
		AttributesToSearchOn: []string{
			"message",
		},
		AttributesToCrop: []string{
			"message",
		},
	}
	if expr := req.FilterExpression(); expr != "" {
		request.Filter = expr
	}
	log.FromContext(ctx).Debug("Searching", "index", indexName, "query", req.Query, "offset", offset, "filter", request.Filter)
	resp, err := m.Client.Index(indexName).SearchWithContext(ctx, req.Query, request)
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

func (m *Meilisearch) Index(chatID int64) meilisearch.IndexManager {
	indexName := fmt.Sprintf("btts_%d", chatID)
	index := m.Client.Index(indexName)
	return index
}

func (m *Meilisearch) multiSearch(ctx context.Context, req types.SearchRequest) (*types.MessageSearchResponse, error) {
	limit := req.Limit
	offset := req.Offset
	if limit == 0 {
		limit = types.PER_SEARCH_LIMIT
	}
	if offset == 0 {
		offset = 0
	}
	multiQueries := make([]*meilisearch.SearchRequest, len(req.ChatIDs))
	for i, chatID := range req.ChatIDs {
		indexName := fmt.Sprintf("btts_%d", chatID)
		queryRequest := &meilisearch.SearchRequest{
			IndexUID: indexName,
			Query:    req.Query,
			AttributesToSearchOn: []string{
				"message",
			},
			AttributesToCrop: []string{
				"message",
			},
		}
		if expr := req.FilterExpression(); expr != "" {
			queryRequest.Filter = expr
		}
		multiQueries[i] = queryRequest
	}
	log.FromContext(ctx).Debug("Searching", "query", req.Query, "offset", offset, "chats", req.ChatIDs)
	resp, err := m.Client.MultiSearchWithContext(ctx, &meilisearch.MultiSearchRequest{
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

// func (e *Engine) UpdateIndexSettings(ctx context.Context, chatID int64) error {
// 	indexName := fmt.Sprintf("btts_%d", chatID)
// 	index := e.Client.Index(indexName)
// 	_, err := index.UpdateSettingsWithContext(ctx, &meilisearch.Settings{
// 		FilterableAttributes: []string{
// 			"user_id",
// 			"type",
// 			"timestamp",
// 		},
// 		SortableAttributes: []string{
// 			"timestamp",
// 			"id",
// 		},
// 		SearchableAttributes: []string{
// 			"message",
// 			"user_id",
// 		},
// 	})
// 	return err
// }
