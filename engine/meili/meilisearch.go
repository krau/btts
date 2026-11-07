package meili

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/duke-git/lancet/v2/retry"
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
	AIGenerated string   `json:"aigenerated"`
	Tags        []string `json:"tags,omitempty"`
	TagsText    string   `json:"tags_text,omitempty"`
	Extracted   string   `json:"extracted,omitempty"`
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
		ID          string `json:"id"`
		Type        string `json:"type"`
		Message     string `json:"message"`
		Ocred       string `json:"ocred"`
		AIGenerated string `json:"aigenerated"`
		Extracted   string `json:"extracted"`
		UserID      string `json:"user_id"`
		MessageID   string `json:"message_id"`
		ChatID      string `json:"chat_id"`
		Timestamp   string `json:"timestamp"`
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
			Tags:        h.Tags,
			TagsText:    h.TagsText,
			Extracted:   h.Extracted,
			UserID:      h.UserID,
			ChatID:      h.ChatID,
			Timestamp:   h.Timestamp,
		},
		Formatted: types.SearchHitFormatted{
			ID:          h.Formatted.MessageID,
			Type:        h.Formatted.Type,
			Message:     h.Formatted.Message,
			Ocred:       h.Formatted.Ocred,
			AIGenerated: h.Formatted.AIGenerated,
			Extracted:   h.Formatted.Extracted,
			UserID:      h.Formatted.UserID,
			ChatID:      h.Formatted.ChatID,
			Timestamp:   h.Formatted.Timestamp,
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
			Tags:        doc.Tags,
			TagsText:    doc.TagsText,
			Extracted:   doc.Extracted,
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
			Tags:        doc.Tags,
			TagsText:    doc.TagsText,
			Extracted:   doc.Extracted,
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
	// WaitTasks blocks CreateIndex until the settings task is applied. Document
	// writes never wait; readers that need read-after-write retry instead.
	WaitTasks bool
	// Extra fields are configured by the enrichment wrapper at startup, before
	// any request is served.
	extraFilterable []string
	extraSearchable []string
	mu              sync.Mutex
}

// SetExtraFields extends the index schema with optional enrichment fields.
func (m *Meilisearch) SetExtraFields(filterable, searchable []string) {
	m.extraFilterable = append([]string(nil), filterable...)
	m.extraSearchable = append([]string(nil), searchable...)
}

func (m *Meilisearch) tagsEnabled() bool {
	return slices.Contains(m.extraFilterable, "tags")
}

// Capabilities implements engine.Searcher.
func (m *Meilisearch) Capabilities() types.Capabilities {
	return types.Capabilities{TagFilters: m.tagsEnabled()}
}

// Enrichment implements engine.Searcher.
func (m *Meilisearch) Enrichment() types.MediaEnricher { return nil }

// SearchImage implements engine.Searcher.
func (m *Meilisearch) SearchImage(context.Context, types.SearchRequest, []byte) (*types.SearchResponse, error) {
	return nil, fmt.Errorf("image search is not supported by the keyword engine")
}

// SearchAudio implements engine.Searcher.
func (m *Meilisearch) SearchAudio(context.Context, types.SearchRequest, []byte) (*types.SearchResponse, error) {
	return nil, fmt.Errorf("audio search is not supported by the keyword engine")
}

// AddDocuments implements engine.Searcher. It uses the partial update
// endpoint so a re-add preserves enrichment fields (tags/extracted) that are
// omitted from the incoming message document.
func (m *Meilisearch) AddDocuments(ctx context.Context, chatID int64, docs []*types.MessageDocument) error {
	docs = slice.Compact(docs)
	if len(docs) == 0 {
		return nil
	}
	for i := range docs {
		docs[i].ChatID = chatID
	}
	jsonData, err := json.Marshal(docsFromMessages(docs))
	if err != nil {
		return err
	}
	retry.Retry(func() error {
		_, err = m.Client.Index(m.Index).UpdateDocumentsWithContext(ctx, jsonData, &meilisearch.DocumentOptions{
			PrimaryKey: new("id"),
		})
		return err
	}, retry.Context(ctx), retry.RetryTimes(10), retry.RetryWithExponentialWithJitterBackoff(time.Second*3, 2, time.Second*2))
	return err
}

type meiliUpdateDocument struct {
	ID        string   `json:"id"`
	Tags      []string `json:"tags,omitempty"`
	TagsText  string   `json:"tags_text,omitempty"`
	Extracted string   `json:"extracted,omitempty"`
}

// UpdateDocuments implements engine.Searcher. It performs a partial update of
// enrichment fields and never recreates a document deleted in the meantime.
func (m *Meilisearch) UpdateDocuments(ctx context.Context, chatID int64, docs []*types.MessageDocument) error {
	docs = slice.Compact(docs)
	updates := make([]*meiliUpdateDocument, 0, len(docs))
	for _, doc := range docs {
		if doc == nil {
			continue
		}
		update := &meiliUpdateDocument{
			ID:        fmt.Sprintf("%d_%d", chatID, doc.ID),
			Tags:      doc.Tags,
			TagsText:  doc.TagsText,
			Extracted: doc.Extracted,
		}
		updates = append(updates, update)
	}
	if len(updates) == 0 {
		return nil
	}
	jsonData, err := json.Marshal(updates)
	if err != nil {
		return err
	}
	retry.Retry(func() error {
		_, err = m.Client.Index(m.Index).UpdateDocumentsWithContext(ctx, jsonData, &meilisearch.DocumentOptions{
			PrimaryKey:   new("id"),
			SkipCreation: true,
		})
		return err
	}, retry.Context(ctx), retry.RetryTimes(10), retry.RetryWithExponentialWithJitterBackoff(time.Second*3, 2, time.Second*2))
	return err
}

func (m *Meilisearch) waitTask(ctx context.Context, info *meilisearch.TaskInfo) error {
	if info == nil || !m.WaitTasks {
		return nil
	}
	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	task, err := m.Client.WaitForTaskWithContext(waitCtx, info.TaskUID, 500*time.Millisecond)
	if err != nil {
		return fmt.Errorf("wait meilisearch task %d: %w", info.TaskUID, err)
	}
	if task.Status != meilisearch.TaskStatusSucceeded {
		return fmt.Errorf("meilisearch task %d ended with status %s", info.TaskUID, task.Status)
	}
	return nil
}

// CreateIndex implements engine.Searcher. 对于 Meilisearch 实现，这里创建/配置的是共享索引 m.Index，chatID 参数不会被使用。
func (m *Meilisearch) CreateIndex(ctx context.Context, _ int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// 如果已存在则跳过
	index := m.Client.Index(m.Index)
	_, err := index.FetchInfoWithContext(ctx)
	if err == nil {
		currentSettings, err := index.GetSettingsWithContext(ctx)
		if err != nil {
			return err
		}
		expectedSettings := m.meiliExpectedSettings()
		if !meiliSettingsEqual(currentSettings, expectedSettings) {
			log.FromContext(ctx).Info("Updating index settings to match expected configuration")
			task, err := index.UpdateSettingsWithContext(ctx, expectedSettings)
			if err != nil {
				return err
			}
			return m.waitTask(ctx, task)
		}
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
	task, err := index.UpdateSettingsWithContext(ctx, m.meiliExpectedSettings())
	if err != nil {
		return err
	}
	return m.waitTask(ctx, task)
}

func (m *Meilisearch) meiliExpectedSettings() *meilisearch.Settings {
	filterable := append([]string{
		"user_id",
		"chat_id",
		"type",
		"timestamp",
	}, m.extraFilterable...)
	searchable := append([]string{
		"message", "ocred", "aigenerated",
	}, m.extraSearchable...)
	settings := &meilisearch.Settings{
		FilterableAttributes: filterable,
		SortableAttributes: []string{
			"timestamp",
			"id",
			"chat_id",
			"message_id",
		},
		SearchableAttributes: searchable,
	}
	return settings
}

func meiliSettingsEqual(current *meilisearch.Settings, expected *meilisearch.Settings) bool {
	if current == nil || expected == nil {
		return false
	}
	if !sameStringSet(current.FilterableAttributes, expected.FilterableAttributes) ||
		!sameStringSet(current.SortableAttributes, expected.SortableAttributes) ||
		!sameStringSet(current.SearchableAttributes, expected.SearchableAttributes) {
		return false
	}
	if len(current.Embedders) != len(expected.Embedders) {
		return false
	}
	for name, expectedEmbedder := range expected.Embedders {
		currentEmbedder, ok := current.Embedders[name]
		if !ok || !sameEmbedder(currentEmbedder, expectedEmbedder) {
			return false
		}
	}
	return true
}

func sameEmbedder(a, b meilisearch.Embedder) bool {
	left, err := json.Marshal(a)
	if err != nil {
		return false
	}
	right, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return bytes.Equal(left, right)
}

func sameStringSet(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	ac := append([]string(nil), a...)
	bc := append([]string(nil), b...)
	sort.Strings(ac)
	sort.Strings(bc)
	for i := range ac {
		if ac[i] != bc[i] {
			return false
		}
	}
	return true
}

// DeleteDocuments implements engine.Searcher.
func (m *Meilisearch) DeleteDocuments(ctx context.Context, chatID int64, ids []int) error {
	ids = slice.Compact(ids)
	docIds := make([]string, 0, len(ids))
	for _, id := range ids {
		docIds = append(docIds, fmt.Sprintf("%d_%d", chatID, id))
	}
	// 删除接口不支持 primaryKey 查询参数, 只能传 customMetadata
	_, err := m.Client.Index(m.Index).DeleteDocumentsWithContext(ctx, docIds, nil)
	return err
}

// DeleteIndex implements engine.Searcher.
func (m *Meilisearch) DeleteIndex(ctx context.Context, chatID int64) error {
	// 删除索引相当于删除属于这个chat的所有文档
	if _, err := m.Client.Index(m.Index).DeleteDocumentsByFilterWithContext(ctx, fmt.Sprintf("chat_id = %d", chatID), nil); err != nil {
		return err
	}
	return nil
}

// GetDocuments implements engine.Searcher.
func (m *Meilisearch) GetDocuments(ctx context.Context, chatID int64, messageIds []int) ([]*types.MessageDocument, error) {
	if len(messageIds) == 0 {
		return nil, nil
	}
	docIds := make([]string, 0, len(messageIds))
	for _, id := range messageIds {
		docIds = append(docIds, fmt.Sprintf("%d_%d", chatID, id))
	}
	var resp meilisearch.DocumentsResult
	// Meilisearch defaults to 20 documents; hydration can request up to 1000.
	err := m.Client.Index(m.Index).GetDocumentsWithContext(ctx, &meilisearch.DocumentsQuery{
		Ids:   docIds,
		Limit: int64(len(docIds)),
	}, &resp)
	if err != nil {
		return nil, err
	}
	hitBytes, err := json.Marshal(resp.Results)
	if err != nil {
		return nil, err
	}
	var hits []*MeilisearchMessageDocument
	err = json.Unmarshal(hitBytes, &hits)
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
	if len(req.Tags) > 0 && !m.tagsEnabled() {
		return nil, fmt.Errorf("tag filters require enrichment to be enabled")
	}
	searchOnAttrs := append([]string{"message"}, m.extraSearchable...)
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
		ShowRankingScore:     true,
	}
	if expr, err := req.FilterExpression(); err != nil {
		return nil, err
	} else {
		request.Filter = expr
	}
	if req.Query == "" {
		// 按时间排序
		request.Sort = []string{"timestamp:desc"}
	}
	log.FromContext(ctx).Info("Searching", "query", req.Query, "offset", offset, "filter", request.Filter)
	resp, err := m.Client.Index(m.Index).SearchWithContext(ctx, req.Query, request)
	if err != nil {
		return nil, err
	}
	hisBytes, err := json.Marshal(resp.Hits)
	if err != nil {
		return nil, err
	}
	var hits []*MeiliSearchHit
	err = json.Unmarshal(hisBytes, &hits)
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
