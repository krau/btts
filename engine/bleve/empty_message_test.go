package bleve

import (
	"context"
	"os"
	"testing"

	"github.com/krau/btts/types"
)

func TestEmptyMessageHandling(t *testing.T) {
	tmpDir := "test_empty"
	defer os.RemoveAll(tmpDir)

	searcher, err := NewBleveSearcher(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create searcher: %v", err)
	}
	defer searcher.Close()

	ctx := context.Background()
	chatID := int64(99999)

	if err := searcher.CreateIndex(ctx, chatID); err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	// 添加包含空消息和正常消息的文档
	docs := []*types.MessageDocument{
		{
			ID:        1,
			Message:   "",
			Type:      int(types.MessageTypeText),
			UserID:    100,
			ChatID:    chatID,
			Timestamp: 1000,
		},
		{
			ID:        2,
			Message:   "   ",
			Type:      int(types.MessageTypeText),
			UserID:    100,
			ChatID:    chatID,
			Timestamp: 2000,
		},
		{
			ID:        3,
			Message:   "正常消息",
			Type:      int(types.MessageTypeText),
			UserID:    100,
			ChatID:    chatID,
			Timestamp: 3000,
		},
		{
			ID:        4,
			Message:   "这是一条很长的消息，包含了一些中文字符，用来测试 UTF-8 边界处理。在截断时应该避免在多字节字符中间切割。这条消息应该被正确截断而不会产生无效的 UTF-8 序列。",
			Type:      int(types.MessageTypeText),
			UserID:    100,
			ChatID:    chatID,
			Timestamp: 4000,
		},
	}

	if err := searcher.AddDocuments(ctx, chatID, docs); err != nil {
		t.Fatalf("Failed to add documents: %v", err)
	}

	// 测试搜索所有文档
	t.Run("SearchAll", func(t *testing.T) {
		req := types.SearchRequest{
			ChatID: chatID,
			Query:  "",
		}
		resp, err := searcher.Search(ctx, req)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		t.Logf("Found %d hits", len(resp.Hits))
		for _, hit := range resp.Hits {
			t.Logf("  ID=%d, Message='%s', Formatted.Message='%s'",
				hit.ID, hit.Message, hit.Formatted.Message)

			// 验证 formatted.message 不为空
			if hit.Formatted.Message == "" {
				t.Errorf("Hit ID=%d has empty formatted message", hit.ID)
			}
		}
	})

	// 测试搜索特定词
	t.Run("SearchWithQuery", func(t *testing.T) {
		req := types.SearchRequest{
			ChatID: chatID,
			Query:  "消息",
		}
		resp, err := searcher.Search(ctx, req)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		t.Logf("Found %d hits with query '消息'", len(resp.Hits))
		for _, hit := range resp.Hits {
			t.Logf("  ID=%d, Formatted.Message='%s'", hit.ID, hit.Formatted.Message)

			// 验证 formatted.message 不为空
			if hit.Formatted.Message == "" {
				t.Errorf("Hit ID=%d has empty formatted message", hit.ID)
			}

			// 验证 formatted message 是有效的 UTF-8
			if !isValidUTF8(hit.Formatted.Message) {
				t.Errorf("Hit ID=%d has invalid UTF-8 in formatted message", hit.ID)
			}
		}
	})
}

func TestUTF8BoundaryAdjustment(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		pos      int
		backward bool
		wantPos  int
	}{
		{
			name:     "ASCII at boundary",
			text:     "hello world",
			pos:      5,
			backward: false,
			wantPos:  5,
		},
		{
			name:     "Chinese character start",
			text:     "你好世界",
			pos:      3, // '好' 的起始位置
			backward: false,
			wantPos:  3,
		},
		{
			name:     "Middle of Chinese character",
			text:     "你好世界",
			pos:      4, // '好' 的中间
			backward: true,
			wantPos:  3, // 应该调整到 '好' 的起始
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := adjustToValidUTF8Boundary(tt.text, tt.pos, tt.backward)
			if got != tt.wantPos {
				t.Errorf("adjustToValidUTF8Boundary(%q, %d, %v) = %d, want %d",
					tt.text, tt.pos, tt.backward, got, tt.wantPos)
			}
		})
	}
}

func isValidUTF8(s string) bool {
	for _, r := range s {
		if r == '\uFFFD' { // Unicode replacement character
			return false
		}
	}
	return true
}
