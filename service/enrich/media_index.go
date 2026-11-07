package enrich

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/krau/btts/config"
	"github.com/krau/btts/types"
	"github.com/meilisearch/meilisearch-go"
)

type mediaDocument struct {
	ID        string `json:"id"`
	ChatID    int64  `json:"chat_id"`
	MessageID int64  `json:"message_id"`
	UserID    int64  `json:"user_id"`
	Type      int    `json:"type"`
	Timestamp int64  `json:"timestamp"`
	// Modality is the vector kind: text, image or audio.
	Modality string `json:"modality"`
	// MediaType is the source media: image, audio, video or document.
	MediaType string         `json:"media_type"`
	Loc       float64        `json:"loc,omitempty"`
	LocEnd    float64        `json:"loc_end,omitempty"`
	Text      string         `json:"text,omitempty"`
	Vectors   map[string]any `json:"_vectors,omitempty"`
}

type mediaHit struct {
	mediaDocument
}

type mediaIndex struct {
	client    meilisearch.ServiceManager
	name      string
	embedder  string
	dims      int
	minScore  float64
	waitTasks bool
	mu        sync.Mutex
	ensured   bool
}

func newMediaIndex(cfg config.AppConfig) *mediaIndex {
	return &mediaIndex{
		client: meilisearch.New(cfg.Engine.Url, meilisearch.WithAPIKey(cfg.Engine.Key)),
		// Keep vectors of different dimensions in separate indexes.
		name:      fmt.Sprintf("%s_media_%s_%d", cfg.Engine.Index, cfg.Enrich.Embedder.MediaName, cfg.Enrich.Embedder.Dimensions),
		embedder:  cfg.Enrich.Embedder.MediaName,
		dims:      cfg.Enrich.Embedder.Dimensions,
		minScore:  cfg.Enrich.Embedder.MediaMinScore,
		waitTasks: cfg.Enrich.WaitIndexTasks,
	}
}

func (m *mediaIndex) ensure(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ensured {
		return nil
	}
	index := m.client.Index(m.name)
	if _, err := index.FetchInfoWithContext(ctx); err != nil {
		if _, err := m.client.CreateIndexWithContext(ctx, &meilisearch.IndexConfig{
			Uid:        m.name,
			PrimaryKey: "id",
		}); err != nil {
			return fmt.Errorf("create media index: %w", err)
		}
	}
	settings := &meilisearch.Settings{
		FilterableAttributes: []string{"chat_id", "message_id", "user_id", "type", "timestamp", "modality", "media_type"},
		SortableAttributes:   []string{"timestamp"},
		SearchableAttributes: []string{"text"},
		Embedders: map[string]meilisearch.Embedder{
			m.embedder: {
				Source:     meilisearch.UserProvidedEmbedderSource,
				Dimensions: m.dims,
			},
		},
	}
	task, err := index.UpdateSettingsWithContext(ctx, settings)
	if err != nil {
		return fmt.Errorf("configure media index: %w", err)
	}
	if task != nil && m.waitTasks {
		waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		done, err := m.client.WaitForTaskWithContext(waitCtx, task.TaskUID, 500*time.Millisecond)
		if err != nil {
			return fmt.Errorf("wait media index settings: %w", err)
		}
		if done.Status != meilisearch.TaskStatusSucceeded {
			return fmt.Errorf("media index settings task %d ended with status %s", task.TaskUID, done.Status)
		}
	}
	m.ensured = true
	return nil
}

func (m *mediaIndex) add(ctx context.Context, docs []*mediaDocument) error {
	if len(docs) == 0 {
		return nil
	}
	payload, err := json.Marshal(docs)
	if err != nil {
		return err
	}
	_, err = m.client.Index(m.name).AddDocumentsWithContext(ctx, payload, &meilisearch.DocumentOptions{
		PrimaryKey: new("id"),
	})
	return err
}

func (m *mediaIndex) deleteMessages(ctx context.Context, chatID int64, ids []int) error {
	if len(ids) == 0 {
		return nil
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, fmt.Sprintf("%d", id))
	}
	filter := fmt.Sprintf("chat_id = %d AND message_id IN [%s]", chatID, strings.Join(parts, ","))
	_, err := m.client.Index(m.name).DeleteDocumentsByFilterWithContext(ctx, filter, nil)
	return err
}

func (m *mediaIndex) deleteChat(ctx context.Context, chatID int64) error {
	_, err := m.client.Index(m.name).DeleteDocumentsByFilterWithContext(ctx, fmt.Sprintf("chat_id = %d", chatID), nil)
	return err
}

// mediaTargetFilter maps a target modality and the query vector kind to a
// media index filter. Text queries may target any vector of a video, while
// image/audio queries target the matching vector kind only.
func mediaTargetFilter(target, queryKind string) string {
	switch strings.ToLower(target) {
	case "text":
		return `modality = "text"`
	case "image":
		return `media_type = "image" AND modality = "image"`
	case "audio":
		return `media_type = "audio" AND modality = "audio"`
	case "video":
		filter := `media_type = "video"`
		switch queryKind {
		case "image":
			filter += ` AND modality = "image"`
		case "audio":
			filter += ` AND modality = "audio"`
		}
		return filter
	default:
		return ""
	}
}

func (m *mediaIndex) search(ctx context.Context, req types.SearchRequest, vector []float32, limit int64, target, queryKind string) ([]mediaHit, int64, error) {
	if limit <= 0 {
		limit = types.PerSearchLimit
	}
	filter, err := req.FilterExpression()
	if err != nil {
		return nil, 0, err
	}
	if targetFilter := mediaTargetFilter(target, queryKind); targetFilter != "" {
		if filter == "" {
			filter = targetFilter
		} else {
			filter = "(" + filter + ") AND " + targetFilter
		}
	}
	request := &meilisearch.SearchRequest{
		Vector:                vector,
		Limit:                 limit,
		Filter:                filter,
		ShowRankingScore:      true,
		RankingScoreThreshold: m.minScore,
		Hybrid: &meilisearch.SearchRequestHybrid{
			Embedder:      m.embedder,
			SemanticRatio: 1,
		},
	}
	resp, err := m.client.Index(m.name).SearchWithContext(ctx, "", request)
	if err != nil {
		return nil, 0, err
	}
	raw, err := json.Marshal(resp.Hits)
	if err != nil {
		return nil, 0, err
	}
	var hits []mediaHit
	if err := json.Unmarshal(raw, &hits); err != nil {
		return nil, 0, err
	}
	total := resp.EstimatedTotalHits
	if total == 0 {
		total = resp.TotalHits
	}
	return hits, total, nil
}
