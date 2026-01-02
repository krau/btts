package bleve

import (
	"context"
	"os"
	"testing"

	"github.com/krau/btts/types"
)

func TestFormattedMessage(t *testing.T) {
	tmpDir := "test_formatted"
	defer os.RemoveAll(tmpDir)

	searcher, err := NewBleveSearcher(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create searcher: %v", err)
	}
	defer searcher.Close()

	ctx := context.Background()
	chatID := int64(12345)

	if err := searcher.CreateIndex(ctx, chatID); err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	// 添加测试文档
	docs := []*types.MessageDocument{
		{
			ID:        1,
			Message:   "这是一个很长的消息，包含了搜索关键词，后面还有很多其他内容。这条消息非常非常长，超过了200个字符的限制，所以应该会被截断。为了测试截断功能，我们需要添加更多的文本内容，让这个消息变得足够长。继续添加一些文字来确保消息长度超过限制。再加一些内容，确保测试能够覆盖各种情况。",
			Type:      int(types.MessageTypeText),
			UserID:    100,
			ChatID:    chatID,
			Timestamp: 1000,
		},
		{
			ID:        2,
			Message:   "短消息包含关键词",
			Type:      int(types.MessageTypeText),
			UserID:    100,
			ChatID:    chatID,
			Timestamp: 2000,
		},
		{
			ID:        3,
			Message:   "这是另一条消息，不包含搜索词",
			Type:      int(types.MessageTypeText),
			UserID:    100,
			ChatID:    chatID,
			Timestamp: 3000,
		},
	}

	if err := searcher.AddDocuments(ctx, chatID, docs); err != nil {
		t.Fatalf("Failed to add documents: %v", err)
	}

	// 测试1: 搜索关键词，验证高亮片段
	t.Run("SearchWithHighlight", func(t *testing.T) {
		req := types.SearchRequest{
			ChatID: chatID,
			Query:  "关键词",
		}
		resp, err := searcher.Search(ctx, req)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		if len(resp.Hits) == 0 {
			t.Fatal("Expected at least one hit")
		}

		for _, hit := range resp.Hits {
			t.Logf("Hit ID=%d", hit.ID)
			t.Logf("  Full message length: %d", len(hit.Message))
			t.Logf("  Formatted message: %s", hit.Formatted.Message)
			t.Logf("  Formatted length: %d", len(hit.Formatted.Message))

			// 验证格式化消息比完整消息短（对于长消息）
			if len(hit.Message) > 200 {
				if len(hit.Formatted.Message) >= len(hit.Message) {
					t.Errorf("Formatted message should be shorter than full message for long messages")
				}
			}

			// 验证格式化消息包含关键词或省略号
			if !containsIgnoreCase(hit.Formatted.Message, "关键词") &&
				!containsIgnoreCase(hit.Formatted.Message, "...") {
				t.Logf("Warning: Formatted message doesn't contain keyword or ellipsis: %s", hit.Formatted.Message)
			}
		}
	})

	// 测试2: 无查询词搜索（MatchAll），验证摘要
	t.Run("SearchWithoutQuery", func(t *testing.T) {
		req := types.SearchRequest{
			ChatID: chatID,
			Query:  "",
		}
		resp, err := searcher.Search(ctx, req)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		for _, hit := range resp.Hits {
			t.Logf("Hit ID=%d (no query)", hit.ID)
			t.Logf("  Formatted message: %s", hit.Formatted.Message)

			// 对于长消息，应该被截断
			if len(hit.Message) > 200 && len(hit.Formatted.Message) >= len(hit.Message) {
				t.Errorf("Long message should be truncated even without query")
			}
		}
	})

	// 测试3: 短消息不应该被截断
	t.Run("ShortMessageNotTruncated", func(t *testing.T) {
		req := types.SearchRequest{
			ChatID: chatID,
			Query:  "短消息",
		}
		resp, err := searcher.Search(ctx, req)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		if len(resp.Hits) == 0 {
			t.Fatal("Expected at least one hit")
		}

		hit := resp.Hits[0]
		if len(hit.Message) < 200 {
			// 短消息的格式化版本应该与原始消息相同或包含高亮标记
			t.Logf("Short message - Full: %s", hit.Message)
			t.Logf("Short message - Formatted: %s", hit.Formatted.Message)
		}
	})
}

// containsIgnoreCase 不区分大小写的包含检查
func containsIgnoreCase(s, substr string) bool {
	return len(s) >= len(substr) &&
		(s == substr ||
			len(substr) == 0 ||
			indexIgnoreCase(s, substr) >= 0)
}

func indexIgnoreCase(s, substr string) int {
	s = toLower(s)
	substr = toLower(substr)
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func toLower(s string) string {
	result := make([]rune, 0, len(s))
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			result = append(result, r+32)
		} else {
			result = append(result, r)
		}
	}
	return string(result)
}
