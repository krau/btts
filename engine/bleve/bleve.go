package bleve

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/blevesearch/bleve/v2/search/query"
	"github.com/charmbracelet/log"
	"github.com/duke-git/lancet/v2/slice"
	"github.com/krau/btts/types"
)

type BleveSearcher struct {
	indexPath string
	indexes   map[int64]bleve.Index
}

// NewBleveSearcher 创建一个新的 Bleve 搜索器
func NewBleveSearcher(indexPath string) (*BleveSearcher, error) {
	if err := os.MkdirAll(indexPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create index directory: %w", err)
	}
	return &BleveSearcher{
		indexPath: indexPath,
		indexes:   make(map[int64]bleve.Index),
	}, nil
}

// getIndex 获取或打开指定 chatID 的索引
func (b *BleveSearcher) getIndex(chatID int64) (bleve.Index, error) {
	if idx, ok := b.indexes[chatID]; ok {
		return idx, nil
	}

	indexName := fmt.Sprintf("btts_%d", chatID)
	indexFullPath := filepath.Join(b.indexPath, indexName)

	idx, err := bleve.Open(indexFullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open index: %w", err)
	}

	b.indexes[chatID] = idx
	return idx, nil
}

// createIndexMapping 创建索引映射配置
func createIndexMapping() *mapping.IndexMappingImpl {
	messageMapping := bleve.NewDocumentMapping()

	// id 字段 - 数值类型
	idFieldMapping := bleve.NewNumericFieldMapping()
	messageMapping.AddFieldMappingsAt("id", idFieldMapping)

	// message 字段 - 文本类型，可搜索
	messageFieldMapping := bleve.NewTextFieldMapping()
	messageMapping.AddFieldMappingsAt("message", messageFieldMapping)

	// type 字段 - 数值类型，可过滤
	typeFieldMapping := bleve.NewNumericFieldMapping()
	messageMapping.AddFieldMappingsAt("type", typeFieldMapping)

	// user_id 字段 - 数值类型，可过滤和搜索
	userIDFieldMapping := bleve.NewNumericFieldMapping()
	messageMapping.AddFieldMappingsAt("user_id", userIDFieldMapping)

	// chat_id 字段 - 数值类型
	chatIDFieldMapping := bleve.NewNumericFieldMapping()
	messageMapping.AddFieldMappingsAt("chat_id", chatIDFieldMapping)

	// timestamp 字段 - 数值类型，可排序
	timestampFieldMapping := bleve.NewNumericFieldMapping()
	messageMapping.AddFieldMappingsAt("timestamp", timestampFieldMapping)

	indexMapping := bleve.NewIndexMapping()
	indexMapping.AddDocumentMapping("message", messageMapping)
	indexMapping.DefaultMapping = messageMapping

	return indexMapping
}

// CreateIndex 创建新索引
func (b *BleveSearcher) CreateIndex(ctx context.Context, chatID int64) error {
	indexName := fmt.Sprintf("btts_%d", chatID)
	indexFullPath := filepath.Join(b.indexPath, indexName)

	// 如果索引已存在，直接返回
	if _, err := os.Stat(indexFullPath); err == nil {
		log.FromContext(ctx).Debug("Index already exists", "chat_id", chatID)
		idx, err := bleve.Open(indexFullPath)
		if err == nil {
			b.indexes[chatID] = idx
		}
		return err
	}

	indexMapping := createIndexMapping()
	idx, err := bleve.New(indexFullPath, indexMapping)
	if err != nil {
		return fmt.Errorf("failed to create index: %w", err)
	}

	b.indexes[chatID] = idx
	log.FromContext(ctx).Debug("Created new index", "chat_id", chatID)
	return nil
}

// DeleteIndex 删除索引
func (b *BleveSearcher) DeleteIndex(ctx context.Context, chatID int64) error {
	indexName := fmt.Sprintf("btts_%d", chatID)
	indexFullPath := filepath.Join(b.indexPath, indexName)

	// 关闭索引
	if idx, ok := b.indexes[chatID]; ok {
		if err := idx.Close(); err != nil {
			log.FromContext(ctx).Warn("Failed to close index", "chat_id", chatID, "error", err)
		}
		delete(b.indexes, chatID)
	}

	// 删除索引目录
	if err := os.RemoveAll(indexFullPath); err != nil {
		return fmt.Errorf("failed to delete index directory: %w", err)
	}

	log.FromContext(ctx).Debug("Deleted index", "chat_id", chatID)
	return nil
}

// AddDocuments 添加或更新文档
func (b *BleveSearcher) AddDocuments(ctx context.Context, chatID int64, docs []*types.MessageDocument) error {
	if len(docs) == 0 {
		return nil
	}

	docs = slice.Compact(docs)
	for i := range docs {
		docs[i].ChatID = chatID
	}

	idx, err := b.getIndex(chatID)
	if err != nil {
		return err
	}

	batch := idx.NewBatch()
	for _, doc := range docs {
		docID := fmt.Sprintf("%d", doc.ID)
		if err := batch.Index(docID, doc); err != nil {
			log.FromContext(ctx).Warn("Failed to add document to batch", "doc_id", docID, "error", err)
			continue
		}
	}

	if err := idx.Batch(batch); err != nil {
		return fmt.Errorf("failed to execute batch: %w", err)
	}

	return nil
}

// DeleteDocuments 删除文档
func (b *BleveSearcher) DeleteDocuments(ctx context.Context, chatID int64, ids []int) error {
	if len(ids) == 0 {
		return nil
	}

	ids = slice.Compact(ids)
	idx, err := b.getIndex(chatID)
	if err != nil {
		return err
	}

	batch := idx.NewBatch()
	for _, id := range ids {
		docID := fmt.Sprintf("%d", id)
		batch.Delete(docID)
	}

	if err := idx.Batch(batch); err != nil {
		return fmt.Errorf("failed to execute batch: %w", err)
	}

	log.FromContext(ctx).Debug("Deleted documents", "chat_id", chatID, "count", len(ids))
	return nil
}

// GetDocuments 获取文档
func (b *BleveSearcher) GetDocuments(ctx context.Context, chatID int64, ids []int) ([]*types.MessageDocument, error) {
	if len(ids) == 0 {
		return []*types.MessageDocument{}, nil
	}

	idx, err := b.getIndex(chatID)
	if err != nil {
		return nil, err
	}

	docs := make([]*types.MessageDocument, 0, len(ids))
	for _, id := range ids {
		docID := fmt.Sprintf("%d", id)
		doc := &types.MessageDocument{}

		// 通过搜索单个文档来获取
		q := query.NewDocIDQuery([]string{docID})
		req := bleve.NewSearchRequest(q)
		req.Fields = []string{"*"}
		req.Size = 1

		result, err := idx.Search(req)
		if err != nil {
			log.FromContext(ctx).Warn("Failed to search document", "doc_id", docID, "error", err)
			continue
		}

		if len(result.Hits) == 0 {
			log.FromContext(ctx).Warn("Document not found", "doc_id", docID)
			continue
		}

		hit := result.Hits[0]
		if idVal, ok := hit.Fields["id"].(float64); ok {
			doc.ID = int64(idVal)
		}
		if message, ok := hit.Fields["message"].(string); ok {
			doc.Message = message
		}
		if typeVal, ok := hit.Fields["type"].(float64); ok {
			doc.Type = int(typeVal)
		}
		if userID, ok := hit.Fields["user_id"].(float64); ok {
			doc.UserID = int64(userID)
		}
		if timestamp, ok := hit.Fields["timestamp"].(float64); ok {
			doc.Timestamp = int64(timestamp)
		}
		doc.ChatID = chatID

		docs = append(docs, doc)
	}

	return docs, nil
}

// buildQuery 构建查询
func (b *BleveSearcher) buildQuery(req types.SearchRequest) query.Query {
	var mustQueries []query.Query

	// 文本搜索 - 如果有查询文本，作为必须条件
	if req.Query != "" {
		matchQuery := query.NewMatchQuery(req.Query)
		matchQuery.SetField("message")
		mustQueries = append(mustQueries, matchQuery)
	}

	// 用户过滤 - 作为必须条件（OR 组合多个用户）
	if len(req.UserFilters) > 0 {
		userQueries := make([]query.Query, 0, len(req.UserFilters))
		for _, userID := range req.UserFilters {
			userFloat := float64(userID)
			// 使用包含性查询来匹配精确值
			numQuery := query.NewNumericRangeInclusiveQuery(&userFloat, &userFloat, boolPtr(true), boolPtr(true))
			numQuery.SetField("user_id")
			userQueries = append(userQueries, numQuery)
		}
		if len(userQueries) > 0 {
			disjunctionQuery := query.NewDisjunctionQuery(userQueries)
			mustQueries = append(mustQueries, disjunctionQuery)
		}
	}

	// 类型过滤 - 作为必须条件（OR 组合多个类型）
	if len(req.TypeFilters) > 0 {
		typeQueries := make([]query.Query, 0, len(req.TypeFilters))
		for _, msgType := range req.TypeFilters {
			typeFloat := float64(msgType)
			// 使用包含性查询来匹配精确值
			numQuery := query.NewNumericRangeInclusiveQuery(&typeFloat, &typeFloat, boolPtr(true), boolPtr(true))
			numQuery.SetField("type")
			typeQueries = append(typeQueries, numQuery)
		}
		if len(typeQueries) > 0 {
			disjunctionQuery := query.NewDisjunctionQuery(typeQueries)
			mustQueries = append(mustQueries, disjunctionQuery)
		}
	}

	// 组合查询
	if len(mustQueries) == 0 {
		// 没有任何条件，返回所有文档
		return query.NewMatchAllQuery()
	}
	if len(mustQueries) == 1 {
		// 只有一个条件
		return mustQueries[0]
	}
	// 多个条件用 AND 连接
	return query.NewConjunctionQuery(mustQueries)
}

// boolPtr 返回布尔值指针
func boolPtr(b bool) *bool {
	return &b
}

// Search 执行搜索
func (b *BleveSearcher) Search(ctx context.Context, req types.SearchRequest) (*types.MessageSearchResponse, error) {
	if len(req.ChatIDs) > 0 {
		return b.multiSearch(ctx, req)
	}

	if req.ChatID == 0 {
		return nil, fmt.Errorf("ChatID is required")
	}

	idx, err := b.getIndex(req.ChatID)
	if err != nil {
		return nil, err
	}

	limit := req.Limit
	offset := req.Offset
	if limit == 0 {
		limit = types.PerSearchLimit
	}
	if offset == 0 {
		offset = 0
	}

	q := b.buildQuery(req)
	searchRequest := bleve.NewSearchRequest(q)
	searchRequest.Size = int(limit)
	searchRequest.From = int(offset)
	searchRequest.SortBy([]string{"-timestamp", "-id"})
	searchRequest.Fields = []string{"*"}

	log.FromContext(ctx).Debug("Searching",
		"chat_id", req.ChatID,
		"query", req.Query,
		"type_filters", req.TypeFilters,
		"user_filters", req.UserFilters,
		"offset", offset)

	searchResult, err := idx.Search(searchRequest)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	log.FromContext(ctx).Debug("Search completed",
		"total_hits", searchResult.Total,
		"returned_hits", len(searchResult.Hits),
		"took", searchResult.Took)

	hits := make([]types.SearchHit, 0, len(searchResult.Hits))
	for _, hit := range searchResult.Hits {
		searchHit := types.SearchHit{}

		if idStr, ok := hit.Fields["id"].(float64); ok {
			searchHit.ID = int64(idStr)
		}
		if message, ok := hit.Fields["message"].(string); ok {
			searchHit.Message = message
		}
		if typeVal, ok := hit.Fields["type"].(float64); ok {
			searchHit.Type = int(typeVal)
		}
		if userID, ok := hit.Fields["user_id"].(float64); ok {
			searchHit.UserID = int64(userID)
		}
		if chatID, ok := hit.Fields["chat_id"].(float64); ok {
			searchHit.ChatID = int64(chatID)
		}
		if timestamp, ok := hit.Fields["timestamp"].(float64); ok {
			searchHit.Timestamp = int64(timestamp)
		}

		// 设置格式化字段
		searchHit.Formatted.ID = strconv.FormatInt(searchHit.ID, 10)
		searchHit.Formatted.Type = strconv.Itoa(searchHit.Type)

		// 提取格式化消息，如果为空则使用原始消息
		formattedMsg := extractFormattedMessage(searchHit.Message, req.Query)
		if formattedMsg == "" {
			formattedMsg = searchHit.Message
		}
		// 再次确保不为空（防止原始消息也为空的极端情况）
		if formattedMsg == "" {
			formattedMsg = "[Empty Message]"
		}
		searchHit.Formatted.Message = formattedMsg

		searchHit.Formatted.UserID = strconv.FormatInt(searchHit.UserID, 10)
		searchHit.Formatted.ChatID = strconv.FormatInt(searchHit.ChatID, 10)
		searchHit.Formatted.Timestamp = strconv.FormatInt(searchHit.Timestamp, 10)

		hits = append(hits, searchHit)
	}

	return &types.MessageSearchResponse{
		Raw:                searchResult,
		Hits:               hits,
		EstimatedTotalHits: int64(searchResult.Total),
		ProcessingTimeMs:   int64(searchResult.Took.Milliseconds()),
		Offset:             offset,
		Limit:              limit,
	}, nil
}

// multiSearch 跨多个聊天搜索
func (b *BleveSearcher) multiSearch(ctx context.Context, req types.SearchRequest) (*types.MessageSearchResponse, error) {
	limit := req.Limit
	offset := req.Offset
	if limit == 0 {
		limit = types.PerSearchLimit
	}
	if offset == 0 {
		offset = 0
	}

	log.FromContext(ctx).Debug("Multi-searching", "query", req.Query, "offset", offset, "chats", req.ChatIDs)

	allHits := []types.SearchHit{}
	totalHits := int64(0)
	totalProcessingTime := int64(0)

	// 对每个聊天进行搜索，并收集结果
	for _, chatID := range req.ChatIDs {
		singleReq := req
		singleReq.ChatID = chatID
		singleReq.ChatIDs = nil
		singleReq.Limit = limit + offset // [TODO] better pagination across chats
		singleReq.Offset = 0

		result, err := b.Search(ctx, singleReq)
		if err != nil {
			log.FromContext(ctx).Warn("Failed to search in chat", "chat_id", chatID, "error", err)
			continue
		}

		allHits = append(allHits, result.Hits...)
		totalHits += result.EstimatedTotalHits
		totalProcessingTime += result.ProcessingTimeMs
	}

	// 按时间戳降序排序
	slice.SortBy(allHits, func(a, b types.SearchHit) bool {
		return a.Timestamp > b.Timestamp
	})

	// 分页处理
	start := int(offset)
	end := int(offset + limit)
	if start > len(allHits) {
		start = len(allHits)
	}
	if end > len(allHits) {
		end = len(allHits)
	}
	paginatedHits := allHits[start:end]

	return &types.MessageSearchResponse{
		Raw:                nil,
		Hits:               paginatedHits,
		EstimatedTotalHits: totalHits,
		ProcessingTimeMs:   totalProcessingTime,
		Offset:             offset,
		Limit:              limit,
	}, nil
}

// Close 关闭所有索引
func (b *BleveSearcher) Close() error {
	for chatID, idx := range b.indexes {
		if err := idx.Close(); err != nil {
			log.Error("Failed to close index", "chat_id", chatID, "error", err)
		}
	}
	b.indexes = make(map[int64]bleve.Index)
	return nil
}

// extractFormattedMessage 从搜索结果中提取格式化的消息片段
func extractFormattedMessage(fullMessage string, query string) string {
	const maxSnippetLength = 200 // 最大片段长度

	// 0. 如果消息为空，返回空字符串（调用方应该检查）
	fullMessage = strings.TrimSpace(fullMessage)
	if fullMessage == "" {
		return ""
	}

	// 1. 如果消息很短，直接返回
	if len(fullMessage) <= maxSnippetLength {
		return fullMessage
	}

	// 2. 如果有查询词，找到包含查询词的片段
	if query != "" {
		// 简单查找（不区分大小写）
		lowerMessage := strings.ToLower(fullMessage)
		lowerQuery := strings.ToLower(query)

		// 分词处理：尝试查找完整查询词或其中的词
		queryWords := strings.Fields(query)
		bestIdx := -1
		bestWord := query

		// 首先尝试查找完整查询
		if idx := strings.Index(lowerMessage, lowerQuery); idx != -1 {
			bestIdx = idx
		} else if len(queryWords) > 0 {
			// 查找查询词中的任意一个词
			for _, word := range queryWords {
				if len(word) < 2 {
					continue
				}
				lowerWord := strings.ToLower(word)
				if idx := strings.Index(lowerMessage, lowerWord); idx != -1 {
					bestIdx = idx
					bestWord = word
					break
				}
			}
		}

		if bestIdx != -1 {
			// 找到查询词位置，提取上下文
			contextBefore := 50
			contextAfter := 150

			// 计算起始位置（确保不会在 UTF-8 字符中间切割）
			start := bestIdx - contextBefore
			if start < 0 {
				start = 0
			} else {
				// 向前调整到有效的 UTF-8 字符边界
				start = adjustToValidUTF8Boundary(fullMessage, start, true)
			}

			// 计算结束位置
			end := bestIdx + len(bestWord) + contextAfter
			if end > len(fullMessage) {
				end = len(fullMessage)
			} else {
				// 向后调整到有效的 UTF-8 字符边界
				end = adjustToValidUTF8Boundary(fullMessage, end, false)
			}

			snippet := fullMessage[start:end]
			if start > 0 {
				snippet = "..." + snippet
			}
			if end < len(fullMessage) {
				snippet = snippet + "..."
			}

			// 再次检查是否为空
			snippet = strings.TrimSpace(snippet)
			if snippet != "" {
				return snippet
			}
		}
	}

	// 3. 默认返回消息开头部分
	return truncateString(fullMessage, maxSnippetLength)
}

// adjustToValidUTF8Boundary 调整索引到有效的 UTF-8 字符边界
// backward=true 表示向前调整，false 表示向后调整
func adjustToValidUTF8Boundary(s string, pos int, backward bool) int {
	if pos <= 0 {
		return 0
	}
	if pos >= len(s) {
		return len(s)
	}

	// 检查当前位置是否是有效的 UTF-8 边界
	for i := 0; i < 4 && pos > 0 && pos < len(s); i++ {
		// 如果当前字节是 UTF-8 字符的起始字节，则这是一个有效边界
		if (s[pos] & 0xC0) != 0x80 {
			return pos
		}
		// 调整位置
		if backward {
			pos--
		} else {
			pos++
		}
	}
	return pos
}

// truncateString 截断字符串到指定长度，如果被截断则添加省略号
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}

	// 尝试在合适的位置截断（空格、标点等）
	truncated := s[:maxLen]

	// 查找最后一个空格或标点
	lastSpace := strings.LastIndexAny(truncated, " \t\n.,;!?，。；！？")
	if lastSpace > maxLen/2 {
		truncated = truncated[:lastSpace]
	}

	return truncated + "..."
}
