package enrich

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/krau/btts/config"
)

// ollamaClient talks to Ollama's native embedding API, which supports text,
// images and audio in one request. The OpenAI-compatible endpoint is only used
// by Meilisearch to embed document text.
type ollamaClient struct {
	baseURL      string
	model        string
	dims         int
	numCtx       int
	client       *http.Client
	warmupClient *http.Client
}

type ollamaEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
	Error      string      `json:"error"`
}

func newOllamaClient(cfg config.AppConfig) (*ollamaClient, error) {
	base := strings.TrimRight(cfg.Enrich.Embedder.URL, "/")
	switch {
	case strings.HasSuffix(base, "/api/embed"):
		base = strings.TrimSuffix(base, "/api/embed")
	case strings.HasSuffix(base, "/v1/embeddings"):
		base = strings.TrimSuffix(base, "/v1/embeddings")
	}
	if base == "" {
		return nil, fmt.Errorf("embedder url is empty")
	}
	return &ollamaClient{
		baseURL:      base,
		model:        cfg.Enrich.Embedder.Model,
		dims:         cfg.Enrich.Embedder.Dimensions,
		numCtx:       cfg.Enrich.Embedder.NumCtx,
		client:       &http.Client{Timeout: cfg.Enrich.Embedder.Timeout},
		warmupClient: &http.Client{Timeout: 2 * time.Minute},
	}, nil
}

type ollamaTagsResponse struct {
	Models []struct {
		Name  string `json:"name"`
		Model string `json:"model"`
	} `json:"models"`
}

// HasModel checks that the configured model is installed without loading it.
func (c *ollamaClient) HasModel(ctx context.Context) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/tags", nil)
	if err != nil {
		return false, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("ollama tags: %s", resp.Status)
	}
	var tags ollamaTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return false, err
	}
	want := strings.TrimSuffix(c.model, ":latest")
	for _, model := range tags.Models {
		if model.Name == c.model || model.Model == c.model ||
			strings.TrimSuffix(model.Name, ":latest") == want ||
			strings.TrimSuffix(model.Model, ":latest") == want {
			return true, nil
		}
	}
	return false, nil
}

func (c *ollamaClient) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/version", nil)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama health check: %s", resp.Status)
	}
	return nil
}

func (c *ollamaClient) Warmup(ctx context.Context) error {
	_, err := c.embedWith(ctx, "warmup", c.warmupClient)
	return err
}

func (c *ollamaClient) Dimensions() int { return c.dims }

func (c *ollamaClient) EmbedText(ctx context.Context, text string) ([]float32, error) {
	return c.embed(ctx, text)
}

func (c *ollamaClient) EmbedImage(ctx context.Context, data []byte) ([]float32, error) {
	return c.embed(ctx, map[string]any{"image": base64.StdEncoding.EncodeToString(data)})
}

func (c *ollamaClient) EmbedImageWithText(ctx context.Context, text string, data []byte) ([]float32, error) {
	input := map[string]any{"image": base64.StdEncoding.EncodeToString(data)}
	if text != "" {
		input["text"] = text
	}
	return c.embed(ctx, input)
}

// EmbedImagesWithText embeds frames in one Ollama batch request. Each input
// item still produces its own vector in the returned slice.
func (c *ollamaClient) EmbedImagesWithText(ctx context.Context, text string, data [][]byte) ([][]float32, error) {
	if len(data) == 0 {
		return nil, nil
	}
	inputs := make([]map[string]any, len(data))
	for i, frame := range data {
		item := map[string]any{"image": base64.StdEncoding.EncodeToString(frame)}
		if text != "" {
			item["text"] = text
		}
		inputs[i] = item
	}
	embeddings, err := c.embedBatch(ctx, inputs)
	if err != nil {
		return nil, err
	}
	if len(embeddings) != len(data) {
		return nil, fmt.Errorf("ollama embed returned %d embeddings for %d inputs", len(embeddings), len(data))
	}
	return embeddings, nil
}

func (c *ollamaClient) EmbedAudio(ctx context.Context, data []byte) ([]float32, error) {
	return c.embed(ctx, map[string]any{"audio": base64.StdEncoding.EncodeToString(data)})
}

func (c *ollamaClient) embed(ctx context.Context, input any) ([]float32, error) {
	return c.embedWith(ctx, input, c.client)
}

func (c *ollamaClient) embedBatch(ctx context.Context, input any) ([][]float32, error) {
	return c.embedBatchWith(ctx, input, c.client)
}

func (c *ollamaClient) embedWith(ctx context.Context, input any, client *http.Client) ([]float32, error) {
	embeddings, err := c.embedBatchWith(ctx, input, client)
	if err != nil {
		return nil, err
	}
	if len(embeddings) == 0 || len(embeddings[0]) == 0 {
		return nil, fmt.Errorf("ollama embed returned no embeddings")
	}
	return embeddings[0], nil
}

func (c *ollamaClient) embedBatchWith(ctx context.Context, input any, client *http.Client) ([][]float32, error) {
	body := map[string]any{
		"model":    c.model,
		"input":    input,
		"truncate": true,
	}
	if c.dims > 0 {
		body["dimensions"] = c.dims
	}
	if c.numCtx > 0 {
		body["options"] = map[string]any{"num_ctx": c.numCtx}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/embed", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama embed: %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	var out ollamaEmbedResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode ollama embed response: %w", err)
	}
	if out.Error != "" {
		return nil, fmt.Errorf("ollama embed: %s", out.Error)
	}
	if len(out.Embeddings) == 0 {
		return nil, fmt.Errorf("ollama embed returned no embeddings")
	}
	for i, embedding := range out.Embeddings {
		if len(embedding) == 0 {
			return nil, fmt.Errorf("ollama embed returned empty embedding %d", i)
		}
	}
	return out.Embeddings, nil
}
