package meili

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/meilisearch/meilisearch-go"
)

// Meilisearch 的删除接口只接受部分查询参数, 传入其它参数(如 primaryKey)会返回 400
func TestDeleteDocumentsRejectsUnknownQueryParams(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/indexes/btts/documents/delete-batch", "/indexes/btts/documents/delete":
		default:
			http.NotFound(w, r)
			return
		}
		for key := range r.URL.Query() {
			if key != "customMetadata" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"message":"Unknown parameter ` + key + `","code":"bad_request"}`))
				return
			}
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"taskUid":1,"indexUid":"btts","status":"enqueued","type":"documentDeletion"}`))
	}))
	defer srv.Close()

	m := &Meilisearch{
		Client: meilisearch.New(srv.URL),
		Index:  "btts",
	}
	if err := m.DeleteDocuments(context.Background(), 1, []int{2, 3}); err != nil {
		t.Fatalf("DeleteDocuments: %v", err)
	}
	if err := m.DeleteIndex(context.Background(), 1); err != nil {
		t.Fatalf("DeleteIndex: %v", err)
	}
}
