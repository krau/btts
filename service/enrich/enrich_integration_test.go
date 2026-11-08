//go:build integration

package enrich

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/krau/btts/config"
	"github.com/krau/btts/engine/meili"
	"github.com/krau/btts/types"
	"github.com/meilisearch/meilisearch-go"
)

// fakeEmbedder maps semantically identical text (query prefix vs document
// prefix) to the same deterministic vector, so the real vector index can be
// exercised without loading an image-capable model.
type fakeEmbedder struct {
	dims int
}

func (f fakeEmbedder) Dimensions() int { return f.dims }

func (f fakeEmbedder) EmbedText(_ context.Context, text string) ([]float32, error) {
	return fakeVector("text:"+semanticKey(text), f.dims), nil
}

func (f fakeEmbedder) EmbedImageWithText(_ context.Context, _ string, data []byte) ([]float32, error) {
	return fakeVector("image:"+fmt.Sprintf("%x", sha256.Sum256(data)), f.dims), nil
}

func (f fakeEmbedder) EmbedImagesWithText(_ context.Context, _ string, data [][]byte) ([][]float32, error) {
	out := make([][]float32, len(data))
	for i, frame := range data {
		out[i] = fakeVector("image:"+fmt.Sprintf("%x", sha256.Sum256(frame)), f.dims)
	}
	return out, nil
}

func (f fakeEmbedder) EmbedImagesWithTexts(_ context.Context, _ []string, data [][]byte) ([][]float32, error) {
	out := make([][]float32, len(data))
	for i, frame := range data {
		out[i] = fakeVector("image:"+fmt.Sprintf("%x", sha256.Sum256(frame)), f.dims)
	}
	return out, nil
}

func (f fakeEmbedder) EmbedAudio(_ context.Context, data []byte) ([]float32, error) {
	return fakeVector("audio:"+fmt.Sprintf("%x", sha256.Sum256(data)), f.dims), nil
}

func semanticKey(text string) string {
	text = strings.TrimPrefix(text, "task: search result | query: ")
	text = strings.TrimPrefix(text, "title: none | text: ")
	return strings.TrimSpace(text)
}

func fakeVector(seed string, dims int) []float32 {
	sum := sha256.Sum256([]byte(seed))
	vector := make([]float32, dims)
	var norm float64
	for i := range vector {
		value := float64(binary.BigEndian.Uint16(sum[(2*i)%len(sum):]))/65535*2 - 1
		vector[i] = float32(value)
		norm += value * value
	}
	norm = math.Sqrt(norm)
	for i := range vector {
		vector[i] /= float32(norm)
	}
	return vector
}

type fakeFetcher struct {
	data []byte
	mime string
}

func (f fakeFetcher) Fetch(context.Context, int64, int, string) ([]byte, string, error) {
	return f.data, f.mime, nil
}

func testPNG(t *testing.T, c color.Color) []byte {
	t.Helper()
	const size = 64
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func testVideo(t *testing.T, ffmpeg string) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sample.mp4")
	cmd := exec.Command(ffmpeg,
		"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=red:s=320x240:d=12",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=12",
		"-c:v", "mpeg4", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest",
		"-y", path,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate sample video: %v\n%s", err, output)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func integrationEnrichConfig(url, key, base string) config.AppConfig {
	cfg := config.AppConfig{}
	cfg.Engine.Url = url
	cfg.Engine.Key = key
	cfg.Engine.Index = base
	cfg.Enrich.Embedder.MediaName = "it_media"
	cfg.Enrich.Embedder.Dimensions = 768
	cfg.Enrich.Embedder.MediaEnable = true
	cfg.Enrich.Embedder.MediaMinScore = 0
	cfg.Enrich.Embedder.QueryTimeout = 60 * time.Second
	cfg.Enrich.WaitIndexTasks = true
	cfg.Enrich.Documents.PDFToText = "pdftotext"
	cfg.Enrich.Documents.MaxSizeMB = 20
	cfg.Enrich.Documents.MaxChars = 12000
	return cfg
}

func setupIntegrationEnrich(t *testing.T, cfg config.AppConfig, emb embedder, pdf *pdfExtractor) *Engine {
	t.Helper()
	client := meilisearch.New(cfg.Engine.Url, meilisearch.WithAPIKey(cfg.Engine.Key))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := client.HealthWithContext(ctx); err != nil {
		t.Fatalf("meilisearch health: %v", err)
	}
	mediaIndexName := fmt.Sprintf("%s_media_%s_%d",
		cfg.Engine.Index, cfg.Enrich.Embedder.MediaName, cfg.Enrich.Embedder.Dimensions)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_, _ = client.DeleteIndexWithContext(cleanupCtx, cfg.Engine.Index)
		_, _ = client.DeleteIndexWithContext(cleanupCtx, mediaIndexName)
	})

	inner := &meili.Meilisearch{Client: client, Index: cfg.Engine.Index, WaitTasks: true}
	inner.SetExtraFields([]string{"tags"}, []string{"tags", "extracted"})
	if err := inner.CreateIndex(ctx, 0); err != nil {
		t.Fatalf("create main index: %v", err)
	}
	media := newMediaIndex(cfg)
	if err := media.ensure(ctx); err != nil {
		t.Fatalf("create media index: %v", err)
	}
	return &Engine{
		inner:        inner,
		cfg:          cfg,
		embedder:     emb,
		media:        media,
		pdf:          pdf,
		maxAttempts:  1,
		retryBackoff: time.Second,
		version:      "integration",
	}
}

func realIntegrationEmbedder(t *testing.T, url, model string, dims int) embedder {
	t.Helper()
	cfg := config.AppConfig{}
	cfg.Enrich.Embedder.URL = url
	cfg.Enrich.Embedder.Model = model
	cfg.Enrich.Embedder.Dimensions = dims
	cfg.Enrich.Embedder.Timeout = 120 * time.Second
	cfg.Enrich.Embedder.NumCtx = 8192
	client, err := newOllamaClient(cfg)
	if err != nil {
		t.Fatalf("create ollama client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := client.Health(ctx); err != nil {
		t.Fatalf("ollama health: %v", err)
	}
	installed, err := client.HasModel(ctx)
	if err != nil {
		t.Fatalf("ollama model check: %v", err)
	}
	if !installed {
		t.Fatalf("model %q is not installed", model)
	}
	return client
}

func waitForIntegration(t *testing.T, timeout time.Duration, what string, fn func() bool) {
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

func TestIntegrationHybridAndImageSearch(t *testing.T) {
	url := os.Getenv("BTTS_TEST_MEILI_URL")
	if url == "" {
		t.Skip("BTTS_TEST_MEILI_URL is not set")
	}
	key := os.Getenv("BTTS_TEST_MEILI_KEY")
	client := meilisearch.New(url, meilisearch.WithAPIKey(key))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if _, err := client.HealthWithContext(ctx); err != nil {
		t.Fatalf("meilisearch health: %v", err)
	}

	base := fmt.Sprintf("btts_it_enrich_%d", time.Now().UnixNano())
	mediaIndexName := fmt.Sprintf("%s_media_%s_%d", base, "it_media", 8)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_, _ = client.DeleteIndexWithContext(cleanupCtx, base)
		_, _ = client.DeleteIndexWithContext(cleanupCtx, mediaIndexName)
	})

	cfg := config.AppConfig{}
	cfg.Engine.Url = url
	cfg.Engine.Key = key
	cfg.Engine.Index = base
	cfg.Enrich.Embedder.MediaName = "it_media"
	cfg.Enrich.Embedder.Dimensions = 8
	cfg.Enrich.Embedder.MediaEnable = true
	cfg.Enrich.Embedder.MediaMinScore = 0
	cfg.Enrich.Embedder.QueryTimeout = 5 * time.Second
	cfg.Enrich.WaitIndexTasks = true

	inner := &meili.Meilisearch{Client: client, Index: base, WaitTasks: true}
	inner.SetExtraFields([]string{"tags"}, []string{"tags", "extracted"})
	if err := inner.CreateIndex(ctx, 0); err != nil {
		t.Fatalf("create main index: %v", err)
	}
	media := newMediaIndex(cfg)
	if err := media.ensure(ctx); err != nil {
		t.Fatalf("create media index: %v", err)
	}
	e := &Engine{
		inner:        inner,
		cfg:          cfg,
		embedder:     fakeEmbedder{dims: 8},
		media:        media,
		maxAttempts:  1,
		retryBackoff: time.Second,
		version:      "integration",
	}
	caps := e.Capabilities()
	if !caps.Semantic || !caps.ImageSearch || !caps.Enrichment || !caps.TagFilters {
		t.Fatalf("unexpected capabilities: %+v", caps)
	}

	pngA := testPNG(t, color.RGBA{R: 255, A: 255})
	pngB := testPNG(t, color.RGBA{B: 255, A: 255})
	docs := []*types.MessageDocument{
		{ID: 1, ChatID: 1, UserID: 1, Timestamp: 100, Type: int(types.MessageTypePhoto),
			Message: "sunset cat", HasMedia: true, MediaMIME: "image/png"},
		{ID: 2, ChatID: 1, UserID: 2, Timestamp: 101, Type: int(types.MessageTypeText),
			Message: "sunset cat notes"},
		{ID: 3, ChatID: 1, UserID: 3, Timestamp: 102, Type: int(types.MessageTypePhoto),
			Message: "blue car", HasMedia: true, MediaMIME: "image/png"},
	}
	if err := inner.AddDocuments(ctx, 1, docs); err != nil {
		t.Fatalf("add main documents: %v", err)
	}
	waitForIntegration(t, 10*time.Second, "main documents", func() bool {
		got, err := inner.GetDocuments(ctx, 1, []int{1, 2, 3})
		return err == nil && len(got) == 3
	})
	if err := e.EnrichNow(ctx, 1, docs[0], pngA, "image/png"); err != nil {
		t.Fatalf("enrich image A: %v", err)
	}
	if err := e.EnrichNow(ctx, 1, docs[2], pngB, "image/png"); err != nil {
		t.Fatalf("enrich image B: %v", err)
	}

	var resp *types.SearchResponse
	var err error
	waitForIntegration(t, 15*time.Second, "image vectors", func() bool {
		resp, err = e.SearchImage(ctx, types.SearchRequest{ChatID: 1, Limit: 10}, pngA)
		return err == nil && len(resp.Hits) > 0 && resp.Hits[0].ID == 1
	})
	waitForIntegration(t, 15*time.Second, "second image vector", func() bool {
		page, err := e.SearchImage(ctx, types.SearchRequest{ChatID: 1, Limit: 10}, pngB)
		return err == nil && len(page.Hits) > 0 && page.Hits[0].ID == 3
	})
	// Captions must not exist as independent text vectors.
	captions, err := e.Search(ctx, types.SearchRequest{
		ChatID: 1, Query: "sunset cat", Mode: "media", Modality: "text", Limit: 10,
	})
	if err != nil {
		t.Fatalf("caption text search: %v", err)
	}
	if len(captions.Hits) != 0 {
		t.Fatalf("captions were vectorized as text: %+v", captions.Hits)
	}

	hybrid, err := e.Search(ctx, types.SearchRequest{ChatID: 1, Query: "sunset cat", Limit: 10})
	if err != nil {
		t.Fatalf("hybrid search: %v", err)
	}
	if len(hybrid.Hits) < 2 || hybrid.Hits[0].ID != 1 {
		t.Fatalf("hybrid hits = %+v, want document 1 first", hybrid.Hits)
	}
	seen := make(map[int64]bool, len(hybrid.Hits))
	for _, hit := range hybrid.Hits {
		if seen[hit.ID] {
			t.Fatalf("duplicate message %d in hybrid results", hit.ID)
		}
		seen[hit.ID] = true
	}
	if !seen[2] {
		t.Fatalf("text-only keyword hit missing from hybrid results: %+v", hybrid.Hits)
	}

	imageResp, err := e.SearchImage(ctx, types.SearchRequest{ChatID: 1, Limit: 10}, pngA)
	if err != nil {
		t.Fatalf("image search: %v", err)
	}
	if len(imageResp.Hits) == 0 || imageResp.Hits[0].ID != 1 {
		t.Fatalf("image search hits = %+v, want document 1 first", imageResp.Hits)
	}

	if err := inner.UpdateDocuments(ctx, 1, []*types.MessageDocument{{ID: 1, Tags: []string{"cat"}}}); err != nil {
		t.Fatalf("set tags: %v", err)
	}
	waitForIntegration(t, 10*time.Second, "tag filter", func() bool {
		tagged, err := e.Search(ctx, types.SearchRequest{ChatID: 1, Tags: []string{"cat"}, Limit: 10})
		return err == nil && len(tagged.Hits) == 1 && tagged.Hits[0].ID == 1
	})

	// The worker path fetches the media through MediaFetcher and waits for the
	// main document to become visible.
	doc4 := &types.MessageDocument{ID: 4, ChatID: 1, UserID: 4, Timestamp: 103,
		Type: int(types.MessageTypePhoto), Message: "green tree", HasMedia: true, MediaMIME: "image/png"}
	if err := inner.AddDocuments(ctx, 1, []*types.MessageDocument{doc4}); err != nil {
		t.Fatalf("add fetched document: %v", err)
	}
	waitForIntegration(t, 10*time.Second, "fetched main document", func() bool {
		got, err := inner.GetDocuments(ctx, 1, []int{4})
		return err == nil && len(got) == 1
	})
	green := testPNG(t, color.RGBA{G: 255, A: 255})
	e.fetcher = fakeFetcher{data: green, mime: "image/png"}
	if err := e.process(ctx, jobFromDocument(1, doc4, e.version)); err != nil {
		t.Fatalf("process fetched media: %v", err)
	}
	waitForIntegration(t, 10*time.Second, "fetched media vectors", func() bool {
		fetched, err := e.SearchImage(ctx, types.SearchRequest{ChatID: 1, Limit: 10}, green)
		return err == nil && len(fetched.Hits) > 0 && fetched.Hits[0].ID == 4
	})
}

func firstRunes(text string, limit int) string {
	runes := []rune(strings.Join(strings.Fields(text), " "))
	if len(runes) > limit {
		runes = runes[:limit]
	}
	return string(runes)
}

type pdfFixture struct {
	name  string
	data  []byte
	query string
}

func loadPDFFixtures(t *testing.T, ctx context.Context, dir string, extractor *pdfExtractor, count int) []pdfFixture {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("PDF fixture directory unavailable: %v", err)
	}
	fixtures := make([]pdfFixture, 0, count)
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".pdf") {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.Size() > 2<<20 {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		text, err := extractor.Extract(ctx, data)
		if err != nil || len([]rune(text)) < 500 {
			continue
		}
		fixtures = append(fixtures, pdfFixture{name: entry.Name(), data: data, query: firstRunes(text, 200)})
		if len(fixtures) == count {
			break
		}
	}
	if len(fixtures) < count {
		t.Skipf("need %d text PDFs in %s, found %d", count, dir, len(fixtures))
	}
	return fixtures
}

func TestIntegrationRealMediaPipeline(t *testing.T) {
	meiliURL := os.Getenv("BTTS_TEST_MEILI_URL")
	ollamaURL := os.Getenv("BTTS_TEST_OLLAMA_URL")
	model := os.Getenv("BTTS_TEST_OLLAMA_MODEL")
	if meiliURL == "" || ollamaURL == "" || model == "" {
		t.Skip("BTTS_TEST_MEILI_URL, BTTS_TEST_OLLAMA_URL and BTTS_TEST_OLLAMA_MODEL are not set")
	}
	caps := ollamaModelCapabilities(t, ollamaURL, model)
	if !slices.Contains(caps, "vision") || !slices.Contains(caps, "audio") {
		t.Skipf("model %q lacks vision/audio capabilities (%v)", model, caps)
	}
	dims := ollamaDims(t)
	base := fmt.Sprintf("btts_it_real_media_%d", time.Now().UnixNano())
	cfg := integrationEnrichConfig(meiliURL, os.Getenv("BTTS_TEST_MEILI_KEY"), base)
	cfg.Enrich.Embedder.Dimensions = dims
	cfg.Enrich.Embedder.AudioEnable = true
	cfg.Enrich.Embedder.VideoEnable = true
	cfg.Enrich.Embedder.VideoFrames = 3
	emb := realIntegrationEmbedder(t, ollamaURL, model, dims)
	e := setupIntegrationEnrich(t, cfg, emb, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	redPNG := testPNG(t, color.RGBA{R: 255, A: 255})
	bluePNG := testPNG(t, color.RGBA{B: 255, A: 255})
	samples := make([]int16, audioSampleRate*3)
	for i := range samples {
		samples[i] = int16(8000 * math.Sin(2*math.Pi*440*float64(i)/audioSampleRate))
	}
	docs := []*types.MessageDocument{
		{ID: 1, ChatID: 1, UserID: 1, Timestamp: 100, Type: int(types.MessageTypePhoto),
			Message: "red square", HasMedia: true, MediaMIME: "image/png"},
		{ID: 2, ChatID: 1, UserID: 2, Timestamp: 101, Type: int(types.MessageTypePhoto),
			Message: "blue square", HasMedia: true, MediaMIME: "image/png"},
		{ID: 3, ChatID: 1, UserID: 3, Timestamp: 102, Type: int(types.MessageTypeAudio),
			Message: "sine tone", HasMedia: true, MediaMIME: "audio/wav"},
		{ID: 4, ChatID: 1, UserID: 4, Timestamp: 103, Type: int(types.MessageTypeVideo),
			Message: "red video", HasMedia: true, MediaMIME: "video/mp4", MediaName: "red.mp4"},
		{ID: 5, ChatID: 1, UserID: 5, Timestamp: 104, Type: int(types.MessageTypeText),
			Message: "pure text about a red square"},
	}
	if err := e.inner.AddDocuments(ctx, 1, docs); err != nil {
		t.Fatalf("add documents: %v", err)
	}
	waitForIntegration(t, 15*time.Second, "main documents", func() bool {
		got, err := e.inner.GetDocuments(ctx, 1, []int{1, 2, 3, 4, 5})
		return err == nil && len(got) == 5
	})
	if err := e.EnrichNow(ctx, 1, docs[0], redPNG, "image/png"); err != nil {
		t.Fatalf("enrich red image: %v", err)
	}
	if err := e.EnrichNow(ctx, 1, docs[1], bluePNG, "image/png"); err != nil {
		t.Fatalf("enrich blue image: %v", err)
	}
	if err := e.EnrichNow(ctx, 1, docs[2], wavBytes(samples), "audio/wav"); err != nil {
		t.Fatalf("enrich audio: %v", err)
	}
	ffmpeg, err := findFFmpeg()
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	if err := e.EnrichNow(ctx, 1, docs[3], testVideo(t, ffmpeg), "video/mp4"); err != nil {
		t.Fatalf("enrich video: %v", err)
	}

	waitForIntegration(t, 60*time.Second, "real image vectors", func() bool {
		resp, err := e.Search(ctx, types.SearchRequest{ChatID: 1, Query: "red square", Mode: "media", Limit: 10})
		return err == nil && len(resp.Hits) > 0 && resp.Hits[0].ID == 1
	})
	waitForIntegration(t, 60*time.Second, "real audio vectors", func() bool {
		resp, err := e.Search(ctx, types.SearchRequest{ChatID: 1, Query: "sine tone", Mode: "media", Limit: 10})
		return err == nil && len(resp.Hits) > 0 && resp.Hits[0].ID == 3
	})
	imageResp, err := e.SearchImage(ctx, types.SearchRequest{ChatID: 1, Limit: 10}, redPNG)
	if err != nil {
		t.Fatalf("real image search: %v", err)
	}
	if len(imageResp.Hits) == 0 || imageResp.Hits[0].ID != 1 {
		t.Fatalf("real image search hits = %+v, want document 1 first", imageResp.Hits)
	}

	// Text query targeting images only must skip captions and pure text.
	textToImage, err := e.Search(ctx, types.SearchRequest{
		ChatID: 1, Query: "a red square", Modality: "image", Mode: "media", Limit: 10,
	})
	if err != nil {
		t.Fatalf("text to image: %v", err)
	}
	if len(textToImage.Hits) == 0 || textToImage.Hits[0].ID != 1 {
		t.Fatalf("text-to-image hits = %+v, want document 1 first", textToImage.Hits)
	}

	// Pure text messages have no vector document.
	pureText, err := e.Search(ctx, types.SearchRequest{
		ChatID: 1, Query: "pure text about a red square", Modality: "text", Mode: "media", Limit: 10,
	})
	if err != nil {
		t.Fatalf("pure text media search: %v", err)
	}
	if slices.Contains(hitIDs(pureText), int64(5)) {
		t.Fatalf("pure text message leaked into media index: %v", hitIDs(pureText))
	}

	// Text query targeting audio vectors.
	textToAudio, err := e.Search(ctx, types.SearchRequest{
		ChatID: 1, Query: "sine tone", Modality: "audio", Mode: "media", Limit: 10,
	})
	if err != nil {
		t.Fatalf("text to audio: %v", err)
	}
	if len(textToAudio.Hits) == 0 || textToAudio.Hits[0].ID != 3 {
		t.Fatalf("text-to-audio hits = %+v, want document 3 first", textToAudio.Hits)
	}

	// Audio query against audio and video soundtracks.
	audioQuery, err := e.SearchAudio(ctx, types.SearchRequest{
		ChatID: 1, Modality: "audio", Limit: 10,
	}, wavBytes(samples))
	if err != nil {
		t.Fatalf("audio to audio: %v", err)
	}
	if len(audioQuery.Hits) == 0 || audioQuery.Hits[0].ID != 3 {
		t.Fatalf("audio-to-audio hits = %+v, want document 3 first", audioQuery.Hits)
	}
	audioToVideo, err := e.SearchAudio(ctx, types.SearchRequest{
		ChatID: 1, Modality: "video", Limit: 10,
	}, wavBytes(samples))
	if err != nil {
		t.Fatalf("audio to video: %v", err)
	}
	if len(audioToVideo.Hits) == 0 || audioToVideo.Hits[0].ID != 4 {
		t.Fatalf("audio-to-video hits = %+v, want document 4 first", audioToVideo.Hits)
	}
	// Image and text queries targeting video.
	imageToVideo, err := e.SearchImage(ctx, types.SearchRequest{
		ChatID: 1, Modality: "video", Limit: 10,
	}, redPNG)
	if err != nil {
		t.Fatalf("image to video: %v", err)
	}
	if len(imageToVideo.Hits) == 0 || imageToVideo.Hits[0].ID != 4 {
		t.Fatalf("image-to-video hits = %+v, want document 4 first", imageToVideo.Hits)
	}
	textToVideo, err := e.Search(ctx, types.SearchRequest{
		ChatID: 1, Query: "red video", Modality: "video", Mode: "media", Limit: 10,
	})
	if err != nil {
		t.Fatalf("text to video: %v", err)
	}
	if len(textToVideo.Hits) == 0 || textToVideo.Hits[0].ID != 4 {
		t.Fatalf("text-to-video hits = %+v, want document 4 first", textToVideo.Hits)
	}
}

func hitIDs(resp *types.SearchResponse) []int64 {
	ids := make([]int64, 0, len(resp.Hits))
	for _, hit := range resp.Hits {
		ids = append(ids, hit.ID)
	}
	return ids
}

func TestIntegrationPDFSemanticSearch(t *testing.T) {
	meiliURL := os.Getenv("BTTS_TEST_MEILI_URL")
	ollamaURL := os.Getenv("BTTS_TEST_OLLAMA_URL")
	model := os.Getenv("BTTS_TEST_OLLAMA_MODEL")
	if meiliURL == "" || ollamaURL == "" || model == "" {
		t.Skip("BTTS_TEST_MEILI_URL, BTTS_TEST_OLLAMA_URL and BTTS_TEST_OLLAMA_MODEL are not set")
	}
	pdfDir := os.Getenv("BTTS_TEST_PDF_DIR")
	if pdfDir == "" {
		pdfDir = "/tmp/pdfs"
	}
	extractor := newPDFExtractor("pdftotext", 12000)
	if !extractor.available() {
		t.Skip("pdftotext is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	fixtures := loadPDFFixtures(t, ctx, pdfDir, extractor, 2)

	dims := ollamaDims(t)
	base := fmt.Sprintf("btts_it_real_pdf_%d", time.Now().UnixNano())
	cfg := integrationEnrichConfig(meiliURL, os.Getenv("BTTS_TEST_MEILI_KEY"), base)
	cfg.Enrich.Embedder.Dimensions = dims
	emb := realIntegrationEmbedder(t, ollamaURL, model, dims)
	e := setupIntegrationEnrich(t, cfg, emb, extractor)

	docs := make([]*types.MessageDocument, 0, len(fixtures))
	for i, fixture := range fixtures {
		docs = append(docs, &types.MessageDocument{
			ID:        int64(i + 1),
			ChatID:    1,
			UserID:    int64(i + 1),
			Timestamp: int64(100 + i),
			Type:      int(types.MessageTypeDocument),
			Message:   fixture.name,
			HasMedia:  true,
			MediaMIME: "application/pdf",
			MediaName: fixture.name,
		})
	}
	if err := e.inner.AddDocuments(ctx, 1, docs); err != nil {
		t.Fatalf("add PDF documents: %v", err)
	}
	ids := make([]int, 0, len(docs))
	for _, doc := range docs {
		ids = append(ids, int(doc.ID))
	}
	waitForIntegration(t, 15*time.Second, "PDF main documents", func() bool {
		got, err := e.inner.GetDocuments(ctx, 1, ids)
		return err == nil && len(got) == len(docs)
	})
	for i, fixture := range fixtures {
		if err := e.EnrichNow(ctx, 1, docs[i], fixture.data, "application/pdf"); err != nil {
			t.Fatalf("enrich PDF %s: %v", fixture.name, err)
		}
	}
	for i, fixture := range fixtures {
		wantID := int64(i + 1)
		waitForIntegration(t, 90*time.Second, "PDF vectors "+fixture.name, func() bool {
			resp, err := e.Search(ctx, types.SearchRequest{ChatID: 1, Query: fixture.query, Mode: "media", Limit: 10})
			return err == nil && len(resp.Hits) > 0 && resp.Hits[0].ID == wantID
		})
	}
	for i, fixture := range fixtures {
		wantID := int64(i + 1)
		waitForIntegration(t, 60*time.Second, "PDF keyword text "+fixture.name, func() bool {
			resp, err := e.Search(ctx, types.SearchRequest{ChatID: 1, Query: firstRunes(fixture.query, 20), Limit: 10})
			if err != nil {
				return false
			}
			for _, hit := range resp.Hits {
				if hit.ID == wantID && hit.Extracted != "" {
					return true
				}
			}
			return false
		})
	}
}
