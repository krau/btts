//go:build integration

package meili

import (
	"context"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/krau/btts/types"
	"github.com/meilisearch/meilisearch-go"
)

func integrationClient(t *testing.T) meilisearch.ServiceManager {
	t.Helper()
	url := os.Getenv("BTTS_TEST_MEILI_URL")
	if url == "" {
		t.Skip("BTTS_TEST_MEILI_URL is not set")
	}
	client := meilisearch.New(url, meilisearch.WithAPIKey(os.Getenv("BTTS_TEST_MEILI_KEY")))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := client.HealthWithContext(ctx); err != nil {
		t.Fatalf("meilisearch health: %v", err)
	}
	return client
}

func waitFor(t *testing.T, timeout time.Duration, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func findDocument(t *testing.T, docs []*types.MessageDocument, id int64) *types.MessageDocument {
	t.Helper()
	for _, doc := range docs {
		if doc.ID == id {
			return doc
		}
	}
	return nil
}

func hitIDs(resp *types.SearchResponse) []int64 {
	ids := make([]int64, 0, len(resp.Hits))
	for _, hit := range resp.Hits {
		ids = append(ids, hit.ID)
	}
	return ids
}

func TestIntegrationGetDocumentsBatch(t *testing.T) {
	client := integrationClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	index := fmt.Sprintf("btts_it_batch_%d", time.Now().UnixNano())
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_, _ = client.DeleteIndexWithContext(cleanupCtx, index)
	})
	m := &Meilisearch{Client: client, Index: index, WaitTasks: true}
	if err := m.CreateIndex(ctx, 0); err != nil {
		t.Fatalf("create index: %v", err)
	}
	docs := make([]*types.MessageDocument, 0, 25)
	ids := make([]int, 0, 25)
	for i := 1; i <= 25; i++ {
		docs = append(docs, &types.MessageDocument{
			ID: int64(i), Type: int(types.MessageTypeText), Message: fmt.Sprintf("message %d", i),
			UserID: int64(i), Timestamp: int64(i),
		})
		ids = append(ids, i)
	}
	if err := m.AddDocuments(ctx, 1, docs); err != nil {
		t.Fatalf("add documents: %v", err)
	}
	waitFor(t, 10*time.Second, "25 documents indexed", func() bool {
		got, err := m.GetDocuments(ctx, 1, ids)
		return err == nil && len(got) == 25
	})
}

func TestIntegrationDocumentLifecycle(t *testing.T) {
	client := integrationClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	index := fmt.Sprintf("btts_it_docs_%d", time.Now().UnixNano())
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_, _ = client.DeleteIndexWithContext(cleanupCtx, index)
	})

	m := &Meilisearch{Client: client, Index: index, WaitTasks: true}
	if m.Capabilities().TagFilters {
		t.Fatal("tag filters must be disabled before enrichment fields are configured")
	}
	m.SetExtraFields([]string{"tags"}, []string{"tags", "extracted"})
	if err := m.CreateIndex(ctx, 0); err != nil {
		t.Fatalf("create index: %v", err)
	}
	if !m.Capabilities().TagFilters || m.Capabilities().ImageSearch || m.Enrichment() != nil {
		t.Fatalf("unexpected capabilities: %+v", m.Capabilities())
	}
	settings, err := client.Index(index).GetSettingsWithContext(ctx)
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	for _, attr := range []string{"tags", "user_id", "chat_id", "type", "timestamp"} {
		if !slices.Contains(settings.FilterableAttributes, attr) {
			t.Fatalf("filterable attributes missing %q: %v", attr, settings.FilterableAttributes)
		}
	}
	for _, attr := range []string{"message", "tags", "extracted"} {
		if !slices.Contains(settings.SearchableAttributes, attr) {
			t.Fatalf("searchable attributes missing %q: %v", attr, settings.SearchableAttributes)
		}
	}

	docs := []*types.MessageDocument{
		{ID: 1, Type: int(types.MessageTypeText), Message: "hello cat", UserID: 1, Timestamp: 100},
		{ID: 2, Type: int(types.MessageTypePhoto), Message: "photo", UserID: 2, Timestamp: 200,
			Tags: []string{"cat", "sunset"}, Extracted: "pdf words"},
	}
	if err := m.AddDocuments(ctx, 1, docs); err != nil {
		t.Fatalf("add documents: %v", err)
	}
	waitFor(t, 10*time.Second, "documents indexed", func() bool {
		got, err := m.GetDocuments(ctx, 1, []int{1, 2})
		return err == nil && len(got) == 2
	})
	got, err := m.GetDocuments(ctx, 1, []int{2})
	if err != nil || len(got) != 1 {
		t.Fatalf("get document: %+v, %v", got, err)
	}
	if !slices.Equal(got[0].Tags, []string{"cat", "sunset"}) || got[0].Extracted != "pdf words" {
		t.Fatalf("enrichment fields missing: %+v", got[0])
	}

	var resp *types.SearchResponse
	waitFor(t, 10*time.Second, "keyword search", func() bool {
		resp, err = m.Search(ctx, types.SearchRequest{ChatID: 1, Query: "cat", Limit: 10})
		return err == nil && len(resp.Hits) == 2
	})
	if ids := hitIDs(resp); !slices.Contains(ids, int64(1)) || !slices.Contains(ids, int64(2)) {
		t.Fatalf("keyword hits = %v, want documents 1 and 2", ids)
	}
	if resp, err = m.Search(ctx, types.SearchRequest{ChatID: 1, Query: "pdf", Limit: 10}); err != nil {
		t.Fatalf("extracted search: %v", err)
	}
	if ids := hitIDs(resp); !slices.Equal(ids, []int64{2}) {
		t.Fatalf("extracted hits = %v, want [2]", ids)
	}

	if resp, err = m.Search(ctx, types.SearchRequest{ChatID: 1, Tags: []string{"cat", "sunset"}, TagMatch: "all", Limit: 10}); err != nil {
		t.Fatalf("tag filter: %v", err)
	}
	if ids := hitIDs(resp); !slices.Equal(ids, []int64{2}) {
		t.Fatalf("tag all hits = %v, want [2]", ids)
	}
	if resp, err = m.Search(ctx, types.SearchRequest{ChatID: 1, Tags: []string{"cat", "dog"}, TagMatch: "any", Limit: 10}); err != nil {
		t.Fatalf("tag any filter: %v", err)
	}
	if ids := hitIDs(resp); !slices.Equal(ids, []int64{2}) {
		t.Fatalf("tag any hits = %v, want [2]", ids)
	}
	if resp, err = m.Search(ctx, types.SearchRequest{ChatID: 1, Tags: []string{"cat", "dog"}, TagMatch: "all", Limit: 10}); err != nil {
		t.Fatalf("tag all miss: %v", err)
	}
	if ids := hitIDs(resp); len(ids) != 0 {
		t.Fatalf("tag all miss hits = %v, want none", ids)
	}

	// Partial updates preserve enrichment fields that are omitted by AddDocuments.
	if err := m.UpdateDocuments(ctx, 1, []*types.MessageDocument{{ID: 2, Tags: []string{"dog"}}}); err != nil {
		t.Fatalf("update tags: %v", err)
	}
	waitFor(t, 10*time.Second, "tags updated", func() bool {
		got, err := m.GetDocuments(ctx, 1, []int{2})
		return err == nil && len(got) == 1 && slices.Equal(got[0].Tags, []string{"dog"})
	})
	if err := m.AddDocuments(ctx, 1, []*types.MessageDocument{{ID: 2, Type: int(types.MessageTypePhoto), Message: "updated photo", UserID: 2, Timestamp: 200}}); err != nil {
		t.Fatalf("re-add document: %v", err)
	}
	waitFor(t, 10*time.Second, "document text updated", func() bool {
		got, err := m.GetDocuments(ctx, 1, []int{2})
		return err == nil && len(got) == 1 && got[0].Message == "updated photo"
	})
	got, err = m.GetDocuments(ctx, 1, []int{2})
	if err != nil || len(got) != 1 {
		t.Fatalf("get updated document: %+v, %v", got, err)
	}
	if !slices.Equal(got[0].Tags, []string{"dog"}) {
		t.Fatalf("re-add dropped enrichment fields: %+v", got[0])
	}

	// A partial update must not recreate a deleted document.
	if err := m.DeleteDocuments(ctx, 1, []int{2}); err != nil {
		t.Fatalf("delete document: %v", err)
	}
	waitFor(t, 10*time.Second, "document deleted", func() bool {
		got, err := m.GetDocuments(ctx, 1, []int{2})
		return err == nil && len(got) == 0
	})
	if err := m.UpdateDocuments(ctx, 1, []*types.MessageDocument{{ID: 2, Tags: []string{"ghost"}}}); err != nil {
		t.Fatalf("update deleted document: %v", err)
	}
	time.Sleep(time.Second)
	if got, err := m.GetDocuments(ctx, 1, []int{2}); err != nil || len(got) != 0 {
		t.Fatalf("deleted document was recreated: %+v, %v", got, err)
	}
}
