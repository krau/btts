package enrich

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/krau/btts/config"
)

func TestTaggerClientFiltersAndSorts(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/predict":
			if err := json.NewEncoder(w).Encode(map[string]any{
				"predicted_tags": []string{"Cat", " sunset ", "cat", "low"},
				"scores":         map[string]float64{"cat": 0.9, "sunset": 0.5, "low": 0.1},
			}); err != nil {
				t.Errorf("encode response: %v", err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := config.AppConfig{}
	cfg.Enrich.Tagger.URL = server.URL
	cfg.Enrich.Tagger.Threshold = 0.4
	cfg.Enrich.Tagger.MaxTags = 2
	cfg.Enrich.Tagger.Timeout = 5 * time.Second
	client := newTaggerClient(cfg)
	if client == nil {
		t.Fatal("client is nil")
	}
	if err := client.Health(context.Background()); err != nil {
		t.Fatalf("health: %v", err)
	}
	tags, err := client.TagImage(context.Background(), []byte("not really an image"))
	if err != nil {
		t.Fatalf("tag image: %v", err)
	}
	if want := []string{"cat", "sunset"}; !slices.Equal(tags, want) {
		t.Fatalf("tags = %v, want %v", tags, want)
	}
}
