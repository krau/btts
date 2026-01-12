package meili

import (
	"context"
	"fmt"
	"sync"

	"github.com/bytedance/sonic"
	"github.com/charmbracelet/log"
	"github.com/duke-git/lancet/v2/slice"
	"github.com/krau/btts/types"
	"github.com/krau/btts/utils"
	"github.com/meilisearch/meilisearch-go"
)

type Meilisearch struct {
	Client meilisearch.ServiceManager
	Index  string
	mu     sync.Mutex
}

// AddDocuments implements engine.Searcher.
func (m *Meilisearch) AddDocuments(ctx context.Context, chatID int64, docs []*types.MessageDocumentV1) error {
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
	primaryKey := "id"
	_, err = m.Client.Index(m.Index).UpdateDocumentsWithContext(ctx, jsonData, &primaryKey)
	return err
}

// CreateIndex implements engine.Searcher.
func (m *Meilisearch) CreateIndex(ctx context.Context, chatID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// 如果已存在则跳过
	index := m.Client.Index(m.Index)
	_, err := index.FetchInfoWithContext(ctx)
	if err == nil {
		return nil
	}
	// 否则创建索引, 并更新配置
	_, err = m.Client.CreateIndexWithContext(ctx, &meilisearch.IndexConfig{
		Uid:        m.Index,
		PrimaryKey: "id",
	})
	if err != nil {
		return err
	}
	_, err = index.UpdateSettingsWithContext(ctx, &meilisearch.Settings{
		FilterableAttributes: []string{
			"user_id",
			"chat_id",
			"type",
			"timestamp",
		},
		SortableAttributes: []string{
			"timestamp",
			"id",
			"chat_id",
		},
		SearchableAttributes: []string{
			"message", "ocred", "aigenerated",
		},
	})
	return err
}

// DeleteDocuments implements engine.Searcher.
func (m *Meilisearch) DeleteDocuments(ctx context.Context, chatID int64, ids []int) error {
	ids = slice.Compact(ids)
	cantorIds := make([]uint64, len(ids))
	for i, id := range ids {
		cantorIds[i] = utils.CantorPair(uint64(chatID), uint64(id))
	}
	idsStr := make([]string, len(cantorIds))
	for i, id := range cantorIds {
		idsStr[i] = fmt.Sprintf("%d", id)
	}
	_, err := m.Client.Index(m.Index).DeleteDocumentsWithContext(ctx, idsStr)
	return err
}

// DeleteIndex implements engine.Searcher.
func (m *Meilisearch) DeleteIndex(ctx context.Context, chatID int64) error {
	// 删除索引相当于删除属于这个chat的所有文档
	if _, err := m.Client.Index(m.Index).DeleteDocumentsByFilterWithContext(ctx, fmt.Sprintf("chat_id = %d", chatID)); err != nil {
		return err
	}
	return nil
}

// GetDocuments implements engine.Searcher.
func (m *Meilisearch) GetDocuments(ctx context.Context, chatID int64, ids []int) ([]*types.MessageDocumentV1, error) {
	cantorIds := make([]uint64, len(ids))
	for i, id := range ids {
		cantorIds[i] = utils.CantorPair(uint64(chatID), uint64(id))
	}
	idsStr := slice.Map(cantorIds, func(i int, item uint64) string {
		return fmt.Sprintf("%d", item)
	})
	var resp meilisearch.DocumentsResult
	err := m.Client.Index(m.Index).GetDocumentsWithContext(ctx, &meilisearch.DocumentsQuery{
		Ids: idsStr,
	}, &resp)
	if err != nil {
		return nil, err
	}
	hitBytes, err := sonic.Marshal(resp.Results)
	if err != nil {
		return nil, err
	}
	var hits []*types.MessageDocumentV1
	err = sonic.Unmarshal(hitBytes, &hits)
	if err != nil {
		return nil, err
	}
	return hits, nil
}

// Search implements engine.Searcher.
func (m *Meilisearch) Search(ctx context.Context, req types.SearchRequest) (*types.MessageSearchResponseV1, error) {
	if req.ChatID == 0 && len(req.ChatIDs) == 0 {
		return nil, fmt.Errorf("ChatID is required")
	}
	limit := req.Limit
	offset := req.Offset
	if limit == 0 {
		limit = types.PerSearchLimit
	}
	if offset == 0 {
		offset = 0
	}
	searchOnAttrs := []string{
		"message",
	}
	if req.Ocred {
		searchOnAttrs = append(searchOnAttrs, "ocred")
	}
	if req.AIGenerated {
		searchOnAttrs = append(searchOnAttrs, "aigenerated")
	}
	request := &meilisearch.SearchRequest{
		Offset:               offset,
		Limit:                limit,
		AttributesToSearchOn: searchOnAttrs,
		AttributesToCrop:     searchOnAttrs,
	}
	if expr := req.FilterExpression(); expr != "" {
		request.Filter = expr
	}
	log.FromContext(ctx).Debug("Searching", "query", req.Query, "offset", offset, "filter", request.Filter)
	resp, err := m.Client.Index(m.Index).SearchWithContext(ctx, req.Query, request)
	if err != nil {
		return nil, err
	}
	hisBytes, err := sonic.Marshal(resp.Hits)
	if err != nil {
		return nil, err
	}
	var hits []types.SearchHitV1
	err = sonic.Unmarshal(hisBytes, &hits)
	if err != nil {
		return nil, err
	}
	return &types.MessageSearchResponseV1{
		Raw:                resp,
		Hits:               hits,
		EstimatedTotalHits: resp.EstimatedTotalHits,
		ProcessingTimeMs:   resp.ProcessingTimeMs,
		Offset:             resp.Offset,
		Limit:              resp.Limit,
	}, nil
}

// func (m *Meilisearch) multiSearch(ctx context.Context, req types.SearchRequest) (*types.MessageSearchResponseV1, error) {
// 	limit := req.Limit
// 	offset := req.Offset
// 	if limit == 0 {
// 		limit = types.PerSearchLimit
// 	}
// 	if offset == 0 {
// 		offset = 0
// 	}
// 	searchOnAttrs := []string{
// 		"message",
// 	}
// 	if req.Ocred {
// 		searchOnAttrs = append(searchOnAttrs, "ocred")
// 	}
// 	if req.AIGenerated {
// 		searchOnAttrs = append(searchOnAttrs, "aigenerated")
// 	}
// 	multiQueries := make([]*meilisearch.SearchRequest, len(req.ChatIDs))
// 	for i := range req.ChatIDs {
// 		queryRequest := &meilisearch.SearchRequest{
// 			IndexUID:             m.Index,
// 			Query:                req.Query,
// 			AttributesToSearchOn: searchOnAttrs,
// 			AttributesToCrop:     searchOnAttrs,
// 		}
// 		if expr := req.FilterExpression(); expr != "" {
// 			queryRequest.Filter = expr
// 		}
// 		multiQueries[i] = queryRequest
// 	}
// 	log.FromContext(ctx).Debug("Searching", "query", req.Query, "offset", offset, "chats", req.ChatIDs)
// 	resp, err := m.Client.MultiSearchWithContext(ctx, &meilisearch.MultiSearchRequest{
// 		Federation: &meilisearch.MultiSearchFederation{
// 			Offset: offset,
// 			Limit:  limit,
// 		},
// 		Queries: multiQueries,
// 	})
// 	if err != nil {
// 		return nil, err
// 	}
// 	hisBytes, err := sonic.Marshal(resp.Hits)
// 	if err != nil {
// 		return nil, err
// 	}
// 	var hits []types.SearchHitV1
// 	err = sonic.Unmarshal(hisBytes, &hits)
// 	if err != nil {
// 		return nil, err
// 	}
// 	return &types.MessageSearchResponseV1{
// 		Raw:                resp,
// 		Hits:               hits,
// 		EstimatedTotalHits: resp.EstimatedTotalHits,
// 		ProcessingTimeMs:   resp.ProcessingTimeMs,
// 		Offset:             resp.Offset,
// 		Limit:              resp.Limit,
// 	}, nil
// }

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
