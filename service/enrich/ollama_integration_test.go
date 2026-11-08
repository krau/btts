//go:build integration

package enrich

import (
	"context"
	"encoding/json"
	"fmt"
	"image/color"
	"math"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/krau/btts/config"
)

func ollamaModelCapabilities(t *testing.T, baseURL, model string) []string {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(baseURL, "/")+"/api/show",
		strings.NewReader(fmt.Sprintf(`{"model":%q}`, model)))
	if err != nil {
		t.Logf("build model capabilities request: %v", err)
		return nil
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Logf("model capabilities unavailable: %v", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Logf("model capabilities status: %s", resp.Status)
		return nil
	}
	var out struct {
		Capabilities []string `json:"capabilities"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Logf("decode model capabilities: %v", err)
		return nil
	}
	return out.Capabilities
}

func ollamaDims(t *testing.T) int {
	t.Helper()
	if raw := os.Getenv("BTTS_TEST_OLLAMA_DIMENSIONS"); raw != "" {
		dims, err := strconv.Atoi(raw)
		if err != nil || dims <= 0 {
			t.Fatalf("invalid BTTS_TEST_OLLAMA_DIMENSIONS=%q", raw)
		}
		return dims
	}
	return 768
}

func TestIntegrationOllamaEmbedder(t *testing.T) {
	baseURL := os.Getenv("BTTS_TEST_OLLAMA_URL")
	model := os.Getenv("BTTS_TEST_OLLAMA_MODEL")
	if baseURL == "" || model == "" {
		t.Skip("BTTS_TEST_OLLAMA_URL and BTTS_TEST_OLLAMA_MODEL are not set")
	}
	dims := ollamaDims(t)
	cfg := config.AppConfig{}
	cfg.Enrich.Embedder.URL = baseURL
	cfg.Enrich.Embedder.Model = model
	cfg.Enrich.Embedder.Dimensions = dims
	cfg.Enrich.Embedder.Timeout = 120 * time.Second
	cfg.Enrich.Embedder.NumCtx = 8192

	client, err := newOllamaClient(cfg)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := client.Health(ctx); err != nil {
		t.Fatalf("health: %v", err)
	}
	installed, err := client.HasModel(ctx)
	if err != nil {
		t.Fatalf("has model: %v", err)
	}
	if !installed {
		t.Fatalf("model %q is not installed", model)
	}

	assertVector := func(name string, vector []float32) {
		t.Helper()
		if len(vector) != dims {
			t.Fatalf("%s dimensions = %d, want %d", name, len(vector), dims)
		}
		var norm float64
		for _, value := range vector {
			norm += float64(value * value)
		}
		if norm == 0 {
			t.Fatalf("%s embedding is all zeros", name)
		}
	}

	textVector, err := client.EmbedText(ctx, "hello world")
	if err != nil {
		t.Fatalf("embed text: %v", err)
	}
	assertVector("text", textVector)

	caps := ollamaModelCapabilities(t, baseURL, model)
	if !slices.Contains(caps, "vision") {
		t.Logf("model %q does not advertise vision; skipping image embedding", model)
	} else {
		imageVector, err := client.EmbedImageWithText(ctx, "a red square", testPNG(t, color.RGBA{R: 255, A: 255}))
		if err != nil {
			t.Fatalf("embed image: %v", err)
		}
		assertVector("image", imageVector)
	}
	if !slices.Contains(caps, "audio") {
		t.Logf("model %q does not advertise audio; skipping audio embedding", model)
	} else {
		samples := make([]int16, audioSampleRate*3)
		for i := range samples {
			samples[i] = int16(8000 * math.Sin(2*math.Pi*440*float64(i)/audioSampleRate))
		}
		audioVector, err := client.EmbedAudio(ctx, wavBytes(samples))
		if err != nil {
			t.Fatalf("embed audio: %v", err)
		}
		assertVector("audio", audioVector)
	}
}
