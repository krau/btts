package bleve

import (
	"context"
	"os"
	"testing"

	"github.com/krau/btts/types"
)

func TestBleveTypeFilter(t *testing.T) {
	// 创建临时测试目录
	tmpDir := "test_indexes"
	defer os.RemoveAll(tmpDir)

	// 创建 Bleve 搜索器
	searcher, err := NewBleveSearcher(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create searcher: %v", err)
	}
	defer searcher.Close()

	ctx := context.Background()
	chatID := int64(12345)

	// 创建索引
	if err := searcher.CreateIndex(ctx, chatID); err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	// 添加测试文档
	docs := []*types.MessageDocument{
		{
			ID:        1,
			Message:   "这是一条文本消息",
			Type:      int(types.MessageTypeText),
			UserID:    100,
			ChatID:    chatID,
			Timestamp: 1000,
		},
		{
			ID:        2,
			Message:   "这是一张图片",
			Type:      int(types.MessageTypePhoto),
			UserID:    100,
			ChatID:    chatID,
			Timestamp: 2000,
		},
		{
			ID:        3,
			Message:   "这是一个视频",
			Type:      int(types.MessageTypeVideo),
			UserID:    100,
			ChatID:    chatID,
			Timestamp: 3000,
		},
		{
			ID:        4,
			Message:   "另一条文本消息",
			Type:      int(types.MessageTypeText),
			UserID:    200,
			ChatID:    chatID,
			Timestamp: 4000,
		},
	}

	if err := searcher.AddDocuments(ctx, chatID, docs); err != nil {
		t.Fatalf("Failed to add documents: %v", err)
	}

	// 等待索引完成 - Bleve 可能需要一点时间来刷新索引
	// 先测试能否检索到所有文档
	t.Run("VerifyAllDocuments", func(t *testing.T) {
		req := types.SearchRequest{
			ChatID: chatID,
			Query:  "",
		}
		resp, err := searcher.Search(ctx, req)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		t.Logf("Total documents indexed: %d", len(resp.Hits))
		for _, hit := range resp.Hits {
			t.Logf("Doc ID=%d, Type=%d, Message=%s", hit.ID, hit.Type, hit.Message)
		}
		if len(resp.Hits) != 4 {
			t.Errorf("Expected 4 documents, got %d", len(resp.Hits))
		}
	})

	// 测试1: 只过滤文本类型
	t.Run("FilterTextOnly", func(t *testing.T) {
		req := types.SearchRequest{
			ChatID:      chatID,
			Query:       "",
			TypeFilters: []types.MessageType{types.MessageTypeText},
		}
		resp, err := searcher.Search(ctx, req)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		t.Logf("Filter text only: got %d hits", len(resp.Hits))
		for _, hit := range resp.Hits {
			t.Logf("  Doc ID=%d, Type=%d, Message=%s", hit.ID, hit.Type, hit.Message)
		}
		if len(resp.Hits) != 2 {
			t.Errorf("Expected 2 text messages, got %d", len(resp.Hits))
		}
		for _, hit := range resp.Hits {
			if hit.Type != int(types.MessageTypeText) {
				t.Errorf("Expected type %d, got %d", types.MessageTypeText, hit.Type)
			}
		}
	})

	// 测试2: 过滤图片类型
	t.Run("FilterPhotoOnly", func(t *testing.T) {
		req := types.SearchRequest{
			ChatID:      chatID,
			Query:       "",
			TypeFilters: []types.MessageType{types.MessageTypePhoto},
		}
		resp, err := searcher.Search(ctx, req)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		if len(resp.Hits) != 1 {
			t.Errorf("Expected 1 photo message, got %d", len(resp.Hits))
		}
		if len(resp.Hits) > 0 && resp.Hits[0].Type != int(types.MessageTypePhoto) {
			t.Errorf("Expected type %d, got %d", types.MessageTypePhoto, resp.Hits[0].Type)
		}
	})

	// 测试3: 过滤多种类型
	t.Run("FilterMultipleTypes", func(t *testing.T) {
		req := types.SearchRequest{
			ChatID:      chatID,
			Query:       "",
			TypeFilters: []types.MessageType{types.MessageTypePhoto, types.MessageTypeVideo},
		}
		resp, err := searcher.Search(ctx, req)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		if len(resp.Hits) != 2 {
			t.Errorf("Expected 2 messages (photo+video), got %d", len(resp.Hits))
		}
	})

	// 测试4: 文本搜索 + 类型过滤
	t.Run("TextSearchWithTypeFilter", func(t *testing.T) {
		req := types.SearchRequest{
			ChatID:      chatID,
			Query:       "消息",
			TypeFilters: []types.MessageType{types.MessageTypeText},
		}
		resp, err := searcher.Search(ctx, req)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		if len(resp.Hits) != 2 {
			t.Errorf("Expected 2 text messages with '消息', got %d", len(resp.Hits))
		}
	})

	// 测试5: 用户过滤 + 类型过滤
	t.Run("UserFilterWithTypeFilter", func(t *testing.T) {
		req := types.SearchRequest{
			ChatID:      chatID,
			Query:       "",
			UserFilters: []int64{100},
			TypeFilters: []types.MessageType{types.MessageTypeText},
		}
		resp, err := searcher.Search(ctx, req)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		if len(resp.Hits) != 1 {
			t.Errorf("Expected 1 text message from user 100, got %d", len(resp.Hits))
		}
	})
}
