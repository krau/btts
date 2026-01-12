package meili

import (
	"context"
	"fmt"
	"sync"

	"github.com/bytedance/sonic"
	"github.com/charmbracelet/log"
	"github.com/duke-git/lancet/v2/slice"
	"github.com/krau/btts/types"
	"github.com/meilisearch/meilisearch-go"
)

// meilisearch 内部使用 chat_id_message_id 作为文档 ID
// 在接口中传递的 id 列表始终为 Telegram MessageID
type MeilisearchMessageDocument struct {
	// "chatid_messageid"
	ID   string `json:"id"`
	Type int    `json:"type"`
	// The original text of the message
	Message string `json:"message"`
	// The OCRed text of the message
	Ocred string `json:"ocred"`
	// The AI generated text of the message(summarization, caption, tagging, etc.)
	AIGenerated string `json:"aigenerated"`
	// The ID of the user who sent the message
	UserID int64 `json:"user_id"`
	ChatID int64 `json:"chat_id"`
	// Telegram MessageID
	MessageID int64 `json:"message_id"`
	Timestamp int64 `json:"timestamp"`
}

type MeiliSearchHit struct {
	MeilisearchMessageDocument
	Formatted struct {
		ID        string `json:"id"`
		Type      string `json:"type"`
		Message   string `json:"message"`
		Ocred     string `json:"ocred"`
		UserID    string `json:"user_id"`
		MessageID string `json:"message_id"`
		ChatID    string `json:"chat_id"`
		Timestamp string `json:"timestamp"`
	} `json:"_formatted"`
}

func (h *MeiliSearchHit) ToSearchHit() types.SearchHit {
	return types.SearchHit{
		MessageDocument: types.MessageDocument{
			ID:          h.MessageID,
			Type:        h.Type,
			Message:     h.Message,
			Ocred:       h.Ocred,
			AIGenerated: h.AIGenerated,
			UserID:      h.UserID,
			ChatID:      h.ChatID,
			Timestamp:   h.Timestamp,
		},
		Formatted: types.SearchHitFormatted{
			ID:        h.Formatted.MessageID,
			Type:      h.Formatted.Type,
			Message:   h.Formatted.Message,
			Ocred:     h.Formatted.Ocred,
			UserID:    h.Formatted.UserID,
			ChatID:    h.Formatted.ChatID,
			Timestamp: h.Formatted.Timestamp,
		},
	}
}

func docsFromMessages(docs []*types.MessageDocument) []*MeilisearchMessageDocument {
	meiliDocs := make([]*MeilisearchMessageDocument, len(docs))
	for i, doc := range docs {
		meiliDocs[i] = &MeilisearchMessageDocument{
			ID:          fmt.Sprintf("%d_%d", doc.ChatID, doc.ID),
			Type:        doc.Type,
			Message:     doc.Message,
			Ocred:       doc.Ocred,
			AIGenerated: doc.AIGenerated,
			UserID:      doc.UserID,
			ChatID:      doc.ChatID,
			MessageID:   doc.ID,
			Timestamp:   doc.Timestamp,
		}
	}
	return meiliDocs
}

func docsToMessages(docs []*MeilisearchMessageDocument) []*types.MessageDocument {
	messageDocs := make([]*types.MessageDocument, len(docs))
	for i, doc := range docs {
		messageDocs[i] = &types.MessageDocument{
			ID:          doc.MessageID,
			Type:        doc.Type,
			Message:     doc.Message,
			Ocred:       doc.Ocred,
			AIGenerated: doc.AIGenerated,
			UserID:      doc.UserID,
			ChatID:      doc.ChatID,
			Timestamp:   doc.Timestamp,
		}
	}
	return messageDocs
}

func hitsToSearchHits(hits []*MeiliSearchHit) []types.SearchHit {
	searchHits := make([]types.SearchHit, len(hits))
	for i, hit := range hits {
		sh := hit.ToSearchHit()
		searchHits[i] = sh
	}
	return searchHits
}

type Meilisearch struct {
	Client meilisearch.ServiceManager
	Index  string
	mu     sync.Mutex
}

// AddDocuments implements engine.Searcher.
func (m *Meilisearch) AddDocuments(ctx context.Context, chatID int64, docs []*types.MessageDocument) error {
	docs = slice.Compact(docs)
	for i := range docs {
		docs[i].ChatID = chatID
	}
	jsonData, err := sonic.Marshal(docsFromMessages(docs))
	if err != nil {
		return err
	}
	primaryKey := "id"
	_, err = m.Client.Index(m.Index).UpdateDocumentsWithContext(ctx, jsonData, &primaryKey)
	return err
}

// CreateIndex implements engine.Searcher. 对于 Meilisearch 实现，这里创建/配置的是共享索引 m.Index，chatID 参数不会被使用。
func (m *Meilisearch) CreateIndex(ctx context.Context, _ int64) error {
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
			"message_id",
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
	docIds := make([]string, 0, len(ids))
	for i, id := range ids {
		docIds[i] = fmt.Sprintf("%d_%d", chatID, id)
	}
	_, err := m.Client.Index(m.Index).DeleteDocumentsWithContext(ctx, docIds)
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
func (m *Meilisearch) GetDocuments(ctx context.Context, chatID int64, messageIds []int) ([]*types.MessageDocument, error) {
	docIds := make([]string, 0, len(messageIds))
	for i, id := range messageIds {
		docIds[i] = fmt.Sprintf("%d_%d", chatID, id)
	}
	var resp meilisearch.DocumentsResult
	err := m.Client.Index(m.Index).GetDocumentsWithContext(ctx, &meilisearch.DocumentsQuery{
		Ids: docIds,
	}, &resp)
	if err != nil {
		return nil, err
	}
	hitBytes, err := sonic.Marshal(resp.Results)
	if err != nil {
		return nil, err
	}
	var hits []*MeilisearchMessageDocument
	err = sonic.Unmarshal(hitBytes, &hits)
	if err != nil {
		return nil, err
	}
	return docsToMessages(hits), nil
}

// Search implements engine.Searcher.
func (m *Meilisearch) Search(ctx context.Context, req types.SearchRequest) (*types.SearchResponse, error) {
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
	if !req.DisableOcred {
		searchOnAttrs = append(searchOnAttrs, "ocred")
	}
	if req.EnableAIGenerated {
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
	log.FromContext(ctx).Info("Searching", "query", req.Query, "offset", offset, "filter", request.Filter)
	resp, err := m.Client.Index(m.Index).SearchWithContext(ctx, req.Query, request)
	if err != nil {
		return nil, err
	}
	hisBytes, err := sonic.Marshal(resp.Hits)
	if err != nil {
		return nil, err
	}
	var hits []*MeiliSearchHit
	err = sonic.Unmarshal(hisBytes, &hits)
	if err != nil {
		return nil, err
	}
	return &types.SearchResponse{
		Raw:                resp,
		Hits:               hitsToSearchHits(hits),
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
