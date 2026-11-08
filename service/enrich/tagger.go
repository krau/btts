package enrich

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"sort"
	"strings"

	"github.com/krau/btts/config"
)

type taggerClient struct {
	url       string
	token     string
	threshold float64
	maxTags   int
	client    *http.Client
}

type taggerResponse struct {
	PredictedTags []string           `json:"predicted_tags"`
	Scores        map[string]float64 `json:"scores"`
}

func newTaggerClient(cfg config.AppConfig) *taggerClient {
	if cfg.Enrich.Tagger.URL == "" {
		return nil
	}
	return &taggerClient{
		url:       strings.TrimRight(cfg.Enrich.Tagger.URL, "/"),
		token:     cfg.Enrich.Tagger.Token,
		threshold: cfg.Enrich.Tagger.Threshold,
		maxTags:   cfg.Enrich.Tagger.MaxTags,
		client:    &http.Client{Timeout: cfg.Enrich.Tagger.Timeout},
	}
}

func (t *taggerClient) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.url+"/health", nil)
	if err != nil {
		return err
	}
	if t.token != "" {
		req.Header.Set("Authorization", "Bearer "+t.token)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("tagger health check: %s", resp.Status)
	}
	return nil
}

func (t *taggerClient) TagImage(ctx context.Context, data []byte) ([]string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "image")
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(data); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	raw, err := t.post(ctx, "/predict", writer.FormDataContentType(), &body)
	if err != nil {
		return nil, err
	}
	var out taggerResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode tagger response: %w", err)
	}
	return t.filterTags(out), nil
}

// TagImages calls konatagger's batch endpoint. The server accepts at most its
// configured max_batch_size (normally 16), so callers should chunk larger sets.
func (t *taggerClient) TagImages(ctx context.Context, data [][]byte) ([][]string, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for i, image := range data {
		part, err := writer.CreateFormFile("file", fmt.Sprintf("image_%d", i))
		if err != nil {
			return nil, err
		}
		if _, err := part.Write(image); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	raw, err := t.post(ctx, "/predict/batch", writer.FormDataContentType(), &body)
	if err != nil {
		return nil, err
	}
	var out struct {
		Results []taggerResponse `json:"results"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode tagger batch response: %w", err)
	}
	if len(out.Results) != len(data) {
		return nil, fmt.Errorf("tagger batch returned %d results for %d images", len(out.Results), len(data))
	}
	results := make([][]string, len(out.Results))
	for i := range out.Results {
		results[i] = t.filterTags(out.Results[i])
	}
	return results, nil
}

func (t *taggerClient) post(ctx context.Context, path, contentType string, body io.Reader) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	if t.token != "" {
		req.Header.Set("Authorization", "Bearer "+t.token)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tagger: %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}

func (t *taggerClient) filterTags(out taggerResponse) []string {
	type scored struct {
		tag   string
		score float64
	}
	tags := make([]scored, 0, len(out.PredictedTags))
	seen := make(map[string]struct{}, len(out.PredictedTags))
	for _, tag := range out.PredictedTags {
		tag = normalizeTag(tag)
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		score := out.Scores[tag]
		if score == 0 {
			score = out.Scores[strings.ReplaceAll(tag, " ", "_")]
		}
		if score < t.threshold {
			continue
		}
		seen[tag] = struct{}{}
		tags = append(tags, scored{tag: tag, score: score})
	}
	sort.SliceStable(tags, func(i, j int) bool { return tags[i].score > tags[j].score })
	if len(tags) > t.maxTags {
		tags = tags[:t.maxTags]
	}
	result := make([]string, len(tags))
	for i, tag := range tags {
		result[i] = tag.tag
	}
	return result
}

func normalizeTag(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	tag = strings.ReplaceAll(tag, "_", " ")
	tag = strings.Join(strings.Fields(tag), " ")
	return tag
}
