package enrich

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/log"
	"github.com/krau/btts/config"
	"github.com/krau/btts/database"
	"github.com/krau/btts/engine"
	"github.com/krau/btts/types"
	"github.com/krau/btts/utils"
)

// MediaFetcher downloads a message's media. It is implemented by the Telegram
// client in cmd, keeping this package free of Telegram dependencies.
type MediaFetcher interface {
	Fetch(ctx context.Context, chatID int64, messageID int, mimeHint string) ([]byte, string, error)
}

// embedder is the multimodal embedding client contract.
type embedder interface {
	Dimensions() int
	EmbedText(ctx context.Context, text string) ([]float32, error)
	EmbedImageWithText(ctx context.Context, text string, data []byte) ([]float32, error)
	EmbedAudio(ctx context.Context, data []byte) ([]float32, error)
}

type Engine struct {
	inner        engine.Searcher
	fetcher      MediaFetcher
	cfg          config.AppConfig
	embedder     embedder
	tagger       *taggerClient
	media        *mediaIndex
	pdf          *pdfExtractor
	maxAttempts  int
	retryBackoff time.Duration
	version      string
}

var _ engine.Searcher = (*Engine)(nil)

// Wrap decorates a Searcher with optional text/media enrichment. It returns
// the original searcher unchanged when enrichment is disabled.
func Wrap(ctx context.Context, inner engine.Searcher, fetcher MediaFetcher) (engine.Searcher, error) {
	cfg := config.C
	if !cfg.Enrich.Enable {
		return inner, nil
	}
	version := enrichVersion(cfg)
	enrichEngine := &Engine{
		inner:        inner,
		fetcher:      fetcher,
		cfg:          cfg,
		maxAttempts:  cfg.Enrich.MaxAttempts,
		retryBackoff: cfg.Enrich.RetryBackoff,
		version:      version,
	}
	logger := log.FromContext(ctx)

	if cfg.Enrich.Embedder.Enable {
		client, err := newOllamaClient(cfg)
		if err != nil {
			return nil, fmt.Errorf("create embedder client: %w", err)
		}
		healthCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = client.Health(healthCtx)
		if err == nil {
			var hasModel bool
			hasModel, err = client.HasModel(healthCtx)
			if err == nil && !hasModel {
				err = fmt.Errorf("embedding model %q is not installed in Ollama; run: ollama pull %s", cfg.Enrich.Embedder.Model, cfg.Enrich.Embedder.Model)
			}
		}
		cancel()
		if err != nil {
			return nil, fmt.Errorf("embedder is enabled but unavailable: %w", err)
		}
		if cfg.Enrich.Embedder.Warmup {
			warmCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			start := time.Now()
			err = client.Warmup(warmCtx)
			cancel()
			if err != nil {
				return nil, fmt.Errorf("warm up embedder: %w", err)
			}
			logger.Infof("Embedding model warmed up in %s", time.Since(start).Round(time.Millisecond))
		}
		enrichEngine.embedder = client
		if cfg.Enrich.Embedder.AudioEnable || cfg.Enrich.Embedder.VideoEnable {
			if _, err := exec.LookPath("ffmpeg"); err != nil {
				logger.Warn("ffmpeg not found, audio/video vectors disabled")
				enrichEngine.cfg.Enrich.Embedder.AudioEnable = false
				enrichEngine.cfg.Enrich.Embedder.VideoEnable = false
			}
		}
		if cfg.Enrich.Embedder.MediaEnable {
			enrichEngine.media = newMediaIndex(cfg)
			ensureCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
			err = enrichEngine.media.ensure(ensureCtx)
			cancel()
			if err != nil {
				logger.Errorf("Media index unavailable, continuing with text only: %v", err)
				enrichEngine.media = nil
			} else if err := enrichEngine.media.purgeLegacyTextVectors(ctx); err != nil {
				logger.Warnf("Purge legacy caption vectors: %v", err)
			}
		}
	}

	if cfg.Enrich.Tagger.Enable {
		client := newTaggerClient(cfg)
		if client == nil {
			logger.Warn("Tagger is enabled but enrich.tagger.url is empty")
		} else {
			healthCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			err := client.Health(healthCtx)
			cancel()
			if err != nil {
				logger.Errorf("Tagger unavailable, image tags disabled: %v", err)
			} else {
				enrichEngine.tagger = client
			}
		}
	}

	if cfg.Enrich.Documents.Enable {
		extractor := newPDFExtractor(cfg.Enrich.Documents.PDFToText, cfg.Enrich.Documents.MaxChars)
		if !extractor.available() {
			logger.Errorf("PDF text extraction disabled: %s not found", cfg.Enrich.Documents.PDFToText)
		} else {
			enrichEngine.pdf = extractor
		}
	}

	if configurer, ok := inner.(interface {
		SetExtraFields(filterable, searchable []string)
	}); ok {
		configurer.SetExtraFields([]string{"tags"}, []string{"tags", "extracted"})
	}
	if err := inner.CreateIndex(ctx, 0); err != nil {
		return nil, fmt.Errorf("configure main index for enrichment: %w", err)
	}
	if err := database.PrepareEnrichJobs(ctx, version); err != nil {
		logger.Errorf("Prepare durable enrichment queue: %v", err)
	}
	if err := database.PruneDoneEnrichJobs(ctx, time.Now().Add(-30*24*time.Hour).Unix()); err != nil {
		logger.Warnf("Prune completed enrichment jobs: %v", err)
	}
	if counts, err := database.EnrichJobCounts(ctx); err == nil {
		logger.Infof("Durable enrichment queue: pending=%d running=%d done=%d dead=%d",
			counts[database.EnrichJobPending], counts[database.EnrichJobRunning],
			counts[database.EnrichJobDone], counts[database.EnrichJobDead])
	}
	workers := cfg.Enrich.Workers
	for range workers {
		go enrichEngine.worker(ctx)
	}
	logger.Infof("Enrichment enabled (embedder=%t tagger=%t media=%t pdf=%t video=%t workers=%d)",
		enrichEngine.embedder != nil, enrichEngine.tagger != nil, enrichEngine.media != nil,
		enrichEngine.pdf != nil, enrichEngine.cfg.Enrich.Embedder.VideoEnable, workers)
	return enrichEngine, nil
}

func enrichVersion(cfg config.AppConfig) string {
	// v3 stops indexing media captions as separate text vectors. Old jobs are
	// re-enriched so legacy text documents can be purged.
	return fmt.Sprintf("v3|%t/%s/%d/%s|%t/%s/%g/%d|%t/%s/%d|%t/%t|%t/%d",
		cfg.Enrich.Embedder.Enable,
		cfg.Enrich.Embedder.Model,
		cfg.Enrich.Embedder.Dimensions,
		cfg.Enrich.Embedder.MediaName,
		cfg.Enrich.Tagger.Enable,
		cfg.Enrich.Tagger.URL,
		cfg.Enrich.Tagger.Threshold,
		cfg.Enrich.Tagger.MaxTags,
		cfg.Enrich.Documents.Enable,
		cfg.Enrich.Documents.PDFToText,
		cfg.Enrich.Documents.MaxChars,
		cfg.Enrich.Embedder.MediaEnable,
		cfg.Enrich.Embedder.AudioEnable,
		cfg.Enrich.Embedder.VideoEnable,
		cfg.Enrich.Embedder.VideoFrames,
	)
}

func (e *Engine) Capabilities() types.Capabilities {
	caps := types.Capabilities{
		TagFilters: true,
		Enrichment: true,
	}
	if e.embedder != nil && e.media != nil {
		caps.Semantic = true
		caps.ImageSearch = true
		caps.AudioSearch = true
	}
	return caps
}

func (e *Engine) Enrichment() types.MediaEnricher { return e }

func (e *Engine) CreateIndex(ctx context.Context, chatID int64) error {
	if err := e.inner.CreateIndex(ctx, chatID); err != nil {
		return err
	}
	if e.media != nil {
		return e.media.ensure(ctx)
	}
	return nil
}

func (e *Engine) DeleteIndex(ctx context.Context, chatID int64) error {
	if err := e.inner.DeleteIndex(ctx, chatID); err != nil {
		return err
	}
	if err := database.DeleteEnrichJobsByChat(ctx, chatID); err != nil {
		log.FromContext(ctx).Warnf("Delete enrichment jobs for chat %d: %v", chatID, err)
	}
	if e.media != nil {
		return e.media.deleteChat(ctx, chatID)
	}
	return nil
}

func (e *Engine) AddDocuments(ctx context.Context, chatID int64, docs []*types.MessageDocument) error {
	if err := e.inner.AddDocuments(ctx, chatID, docs); err != nil {
		return err
	}
	if e.embedder == nil && e.tagger == nil && e.pdf == nil {
		return nil
	}
	jobs := make([]*database.EnrichJob, 0, len(docs))
	for _, doc := range docs {
		if doc == nil || !doc.HasMedia {
			continue
		}
		job := jobFromDocument(chatID, doc, e.version)
		if !e.jobNeedsWork(job) {
			continue
		}
		job.Priority = doc.Priority
		jobs = append(jobs, job)
	}
	return database.EnqueueEnrichJobs(ctx, jobs)
}

// AddDocumentsOnly writes main documents without enqueueing media jobs.
// It is used by the takeout importer, which enriches media inline.
func (e *Engine) AddDocumentsOnly(ctx context.Context, chatID int64, docs []*types.MessageDocument) error {
	return e.inner.AddDocuments(ctx, chatID, docs)
}

func (e *Engine) UpdateDocuments(ctx context.Context, chatID int64, docs []*types.MessageDocument) error {
	return e.inner.UpdateDocuments(ctx, chatID, docs)
}

func (e *Engine) DeleteDocuments(ctx context.Context, chatID int64, ids []int) error {
	if err := e.inner.DeleteDocuments(ctx, chatID, ids); err != nil {
		return err
	}
	if e.media != nil {
		return e.media.deleteMessages(ctx, chatID, ids)
	}
	return nil
}

func (e *Engine) GetDocuments(ctx context.Context, chatID int64, ids []int) ([]*types.MessageDocument, error) {
	return e.inner.GetDocuments(ctx, chatID, ids)
}

func normalizeTargetModality(modality string) string {
	switch strings.ToLower(strings.TrimSpace(modality)) {
	case "":
		return ""
	case "all", "text", "image", "audio", "video":
		return strings.ToLower(strings.TrimSpace(modality))
	default:
		return ""
	}
}

func modalityTypeFilters(target string) []types.MessageType {
	switch target {
	case "image":
		// Photos are the common case; image files can also arrive as documents.
		return []types.MessageType{types.MessageTypePhoto, types.MessageTypeDocument}
	case "audio":
		return []types.MessageType{types.MessageTypeVoice, types.MessageTypeAudio, types.MessageTypeDocument}
	case "video":
		return []types.MessageType{types.MessageTypeVideo, types.MessageTypeDocument}
	default:
		return nil
	}
}

func applyTargetFilters(req *types.SearchRequest, target string) {
	if len(req.TypeFilters) == 0 {
		req.TypeFilters = modalityTypeFilters(target)
	}
}

func (e *Engine) Search(ctx context.Context, req types.SearchRequest) (*types.SearchResponse, error) {
	mode := strings.ToLower(req.Mode)
	target := normalizeTargetModality(req.Modality)
	applyTargetFilters(&req, target)
	// Pure text messages are keyword-only. Semantic vectors exist only for
	// media-rich messages and tags live on the main index.
	if mode == "keyword" || req.Query == "" || len(req.Tags) > 0 {
		return e.inner.Search(ctx, req)
	}
	if e.embedder == nil || e.media == nil {
		if mode == "media" {
			return nil, fmt.Errorf("media search is not enabled")
		}
		return e.inner.Search(ctx, req)
	}
	embedCtx, cancel := context.WithTimeout(ctx, e.cfg.Enrich.Embedder.QueryTimeout)
	vector, err := e.embedder.EmbedText(embedCtx, "task: search result | query: "+req.Query)
	cancel()
	if err != nil {
		log.FromContext(ctx).Warnf("Query embedding failed, falling back to keyword search: %v", err)
		return e.inner.Search(ctx, req)
	}
	offset := max(req.Offset, 0)
	limit := req.Limit
	if limit <= 0 {
		limit = types.PerSearchLimit
	}
	window := offset + limit
	if mode == "media" {
		hits, total, err := e.searchMediaHits(ctx, req, vector, window, target, "text")
		if err != nil {
			return nil, err
		}
		return &types.SearchResponse{
			Hits:               paginate(hits, offset, limit),
			EstimatedTotalHits: total,
			Offset:             offset,
			Limit:              limit,
		}, nil
	}
	textReq := req
	textReq.Offset = 0
	textReq.Limit = window
	textResp, err := e.inner.Search(ctx, textReq)
	if err != nil {
		return nil, err
	}
	mediaResp, mediaTotal, err := e.searchMediaHits(ctx, req, vector, window, target, "text")
	if err != nil {
		log.FromContext(ctx).Warnf("Media search failed: %v", err)
		return keywordPage(textResp, offset, limit), nil
	}
	merged := fuseHits(textResp.Hits, mediaResp)
	textResp.Hits = paginate(merged, offset, limit)
	// Fused totals are approximate: the two candidate lists overlap.
	textResp.EstimatedTotalHits = max(textResp.EstimatedTotalHits, mediaTotal)
	textResp.Offset = offset
	textResp.Limit = limit
	return textResp, nil
}

// SearchImage searches media vectors with an image query. The target defaults
// to images and may be set to text, audio, video or all via req.Modality.
func (e *Engine) SearchImage(ctx context.Context, req types.SearchRequest, data []byte) (*types.SearchResponse, error) {
	return e.searchMediaVector(ctx, req, "image", data)
}

// SearchAudio searches media vectors with an audio query. The target defaults
// to audio and may be set to text, image, video or all via req.Modality.
func (e *Engine) SearchAudio(ctx context.Context, req types.SearchRequest, data []byte) (*types.SearchResponse, error) {
	return e.searchMediaVector(ctx, req, "audio", data)
}

func (e *Engine) searchMediaVector(ctx context.Context, req types.SearchRequest, queryKind string, data []byte) (*types.SearchResponse, error) {
	if e.embedder == nil || e.media == nil {
		return nil, fmt.Errorf("media embedding is not enabled")
	}
	var vector []float32
	var err error
	switch queryKind {
	case "image":
		vector, err = e.embedder.EmbedImageWithText(ctx, req.Query, data)
	case "audio":
		vector, err = e.embedder.EmbedAudio(ctx, data)
	default:
		return nil, fmt.Errorf("unsupported media query kind %q", queryKind)
	}
	if err != nil {
		return nil, fmt.Errorf("embed query %s: %w", queryKind, err)
	}
	target := normalizeTargetModality(req.Modality)
	if target == "" {
		target = queryKind
	}
	applyTargetFilters(&req, target)
	offset := max(req.Offset, 0)
	limit := req.Limit
	if limit <= 0 {
		limit = types.PerSearchLimit
	}
	hits, total, err := e.searchMediaHits(ctx, req, vector, offset+limit, target, queryKind)
	if err != nil {
		return nil, err
	}
	return &types.SearchResponse{
		Hits:               paginate(hits, offset, limit),
		EstimatedTotalHits: total,
		Offset:             offset,
		Limit:              limit,
	}, nil
}

func (e *Engine) worker(ctx context.Context) {
	logger := log.FromContext(ctx)
	processed := 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		job, err := database.ClaimEnrichJob(ctx)
		if err != nil {
			logger.Errorf("Claim enrichment job: %v", err)
			_ = utils.SleepContext(ctx, 2*time.Second)
			continue
		}
		if job == nil {
			_ = utils.SleepContext(ctx, time.Second)
			continue
		}
		jobCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
		err = e.process(jobCtx, job)
		cancel()
		if err != nil {
			logger.Warnf("Enrich %d_%d failed (attempt %d): %v", job.ChatID, job.MessageID, job.Attempts+1, err)
			if job.Attempts+1 >= e.maxAttempts {
				logger.Errorf("Enrichment job %d_%d moved to dead letter", job.ChatID, job.MessageID)
			}
			if failErr := database.FailEnrichJob(ctx, job, err, e.maxAttempts, e.retryBackoff); failErr != nil {
				logger.Errorf("Record enrichment failure for %d_%d: %v", job.ChatID, job.MessageID, failErr)
			}
		} else if err := database.CompleteEnrichJob(ctx, job.ID); err != nil {
			logger.Errorf("Complete enrichment job %d: %v", job.ID, err)
		}
		processed++
		if processed%100 == 0 {
			if counts, err := database.EnrichJobCounts(ctx); err == nil {
				logger.Infof("Enrichment queue: pending=%d running=%d done=%d dead=%d",
					counts[database.EnrichJobPending], counts[database.EnrichJobRunning],
					counts[database.EnrichJobDone], counts[database.EnrichJobDead])
			}
		}
		if processed%1000 == 0 {
			if err := database.PruneDoneEnrichJobs(ctx, time.Now().Add(-30*24*time.Hour).Unix()); err != nil {
				logger.Warnf("Prune completed enrichment jobs: %v", err)
			}
		}
	}
}

func jobFromDocument(chatID int64, doc *types.MessageDocument, version string) *database.EnrichJob {
	return &database.EnrichJob{
		ChatID:    chatID,
		MessageID: int(doc.ID),
		UserID:    doc.UserID,
		MsgType:   doc.Type,
		Timestamp: doc.Timestamp,
		MIME:      types.MessageMIME(doc.MediaMIME, doc.MediaName, types.MessageType(doc.Type)),
		Name:      doc.MediaName,
		Version:   version,
		Priority:  doc.Priority,
	}
}

// EnrichableMedia reports whether media of this kind will be processed.
func (e *Engine) EnrichableMedia(mime, name string, messageType types.MessageType) bool {
	return e.jobNeedsWork(&database.EnrichJob{
		MIME:    mime,
		Name:    name,
		MsgType: int(messageType),
	})
}

func (e *Engine) jobNeedsWork(job *database.EnrichJob) bool {
	mime := types.MessageMIME(job.MIME, job.Name, types.MessageType(job.MsgType))
	isPDF := types.IsPDF(mime, job.Name)
	isImage := strings.HasPrefix(mime, "image/")
	isAudio := strings.HasPrefix(mime, "audio/") ||
		types.MessageType(job.MsgType) == types.MessageTypeVoice ||
		types.MessageType(job.MsgType) == types.MessageTypeAudio
	isVideo := strings.HasPrefix(mime, "video/") ||
		types.MessageType(job.MsgType) == types.MessageTypeVideo
	mediaVectors := e.embedder != nil && e.media != nil && e.cfg.Enrich.Embedder.MediaEnable
	switch {
	case isPDF:
		return e.pdf != nil
	case isImage:
		return (e.tagger != nil || mediaVectors) && types.IsSupportedImage(mime, job.Name)
	case isAudio:
		return mediaVectors && e.cfg.Enrich.Embedder.AudioEnable && types.IsSupportedAV(mime, job.Name)
	case isVideo:
		return mediaVectors &&
			(e.cfg.Enrich.Embedder.AudioEnable || e.cfg.Enrich.Embedder.VideoEnable) &&
			types.IsSupportedAV(mime, job.Name)
	default:
		return false
	}
}

func (e *Engine) getDocument(ctx context.Context, chatID int64, messageID int) *types.MessageDocument {
	for range 20 {
		docs, err := e.inner.GetDocuments(ctx, chatID, []int{messageID})
		if err == nil && len(docs) > 0 {
			return docs[0]
		}
		_ = utils.SleepContext(ctx, 500*time.Millisecond)
	}
	return nil
}

func (e *Engine) process(ctx context.Context, job *database.EnrichJob) error {
	if !e.jobNeedsWork(job) {
		return nil
	}
	if e.fetcher == nil {
		return fmt.Errorf("media fetcher is not configured")
	}
	doc := e.getDocument(ctx, job.ChatID, job.MessageID)
	if doc == nil {
		return fmt.Errorf("main document %d_%d not found", job.ChatID, job.MessageID)
	}
	data, mime, err := e.fetcher.Fetch(ctx, job.ChatID, job.MessageID, job.MIME)
	if err != nil {
		return fmt.Errorf("fetch media: %w", err)
	}
	return e.processData(ctx, job, doc, data, mime)
}

// EnrichNow processes media bytes that were downloaded by another transport,
// e.g. the takeout file downloader. It bypasses the durable queue.
func (e *Engine) EnrichNow(ctx context.Context, chatID int64, doc *types.MessageDocument, data []byte, mime string) error {
	job := jobFromDocument(chatID, doc, e.version)
	if !e.jobNeedsWork(job) {
		return nil
	}
	return e.processData(ctx, job, doc, data, mime)
}

// NeedsEnrichment reports whether a document would do any media work.
func (e *Engine) NeedsEnrichment(chatID int64, doc *types.MessageDocument) bool {
	return e.jobNeedsWork(jobFromDocument(chatID, doc, e.version))
}

// EnqueueDocument puts a document into the durable queue, used as a fallback
// when takeout media download fails.
func (e *Engine) EnqueueDocument(ctx context.Context, chatID int64, doc *types.MessageDocument) error {
	job := jobFromDocument(chatID, doc, e.version)
	if !e.jobNeedsWork(job) {
		return nil
	}
	job.Priority = 1
	return database.EnqueueEnrichJobs(ctx, []*database.EnrichJob{job})
}

func (e *Engine) processData(ctx context.Context, job *database.EnrichJob, doc *types.MessageDocument, data []byte, mime string) error {
	if mime == "" {
		mime = job.MIME
	}
	mime = types.MessageMIME(mime, job.Name, types.MessageType(job.MsgType))
	messageText, ocred := doc.Message, doc.Ocred

	isImage := strings.HasPrefix(mime, "image/")
	isAudio := strings.HasPrefix(mime, "audio/") ||
		types.MessageType(job.MsgType) == types.MessageTypeVoice ||
		types.MessageType(job.MsgType) == types.MessageTypeAudio
	isVideo := strings.HasPrefix(mime, "video/") ||
		types.MessageType(job.MsgType) == types.MessageTypeVideo
	isPDF := e.pdf != nil && types.IsPDF(mime, job.Name)
	mediaType := "document"
	switch {
	case isImage:
		mediaType = "image"
	case isAudio:
		mediaType = "audio"
	case isVideo:
		mediaType = "video"
	}

	var extracted string
	var enrichErrs []error
	update := &types.MessageDocument{ID: int64(job.MessageID)}
	if isPDF {
		maxBytes := int64(e.cfg.Enrich.Documents.MaxSizeMB) << 20
		if int64(len(data)) > maxBytes {
			enrichErrs = append(enrichErrs, fmt.Errorf("PDF exceeds %d bytes", maxBytes))
		} else {
			text, err := e.pdf.Extract(ctx, data)
			if err != nil {
				enrichErrs = append(enrichErrs, fmt.Errorf("extract PDF: %w", err))
			} else {
				extracted = text
			}
		}
		update.Extracted = extracted
	}

	var tags []string
	if e.tagger != nil && isImage {
		tagList, err := e.tagger.TagImage(ctx, data)
		if err != nil {
			enrichErrs = append(enrichErrs, fmt.Errorf("tag image: %w", err))
		} else {
			tags = tagList
			update.Tags = tags
		}
	}

	mediaDocs := make([]*mediaDocument, 0, 4)
	if e.embedder != nil && e.media != nil && e.cfg.Enrich.Embedder.MediaEnable {
		text := strings.TrimSpace(messageText + " " + ocred)
		if isPDF && extracted != "" {
			text = extracted
		}
		if isImage && len(tags) > 0 {
			text = strings.TrimSpace(text + " " + strings.Join(tags, " "))
		}
		if isImage {
			vector, err := e.embedder.EmbedImageWithText(ctx, text, data)
			if err != nil {
				enrichErrs = append(enrichErrs, fmt.Errorf("embed image: %w", err))
			} else {
				mediaDocs = append(mediaDocs, &mediaDocument{
					ID:        fmt.Sprintf("%d_%d_img", job.ChatID, job.MessageID),
					ChatID:    job.ChatID,
					MessageID: int64(job.MessageID),
					UserID:    job.UserID,
					Type:      job.MsgType,
					Timestamp: job.Timestamp,
					Modality:  "image",
					MediaType: mediaType,
					Text:      truncate(text, 500),
					Vectors:   map[string]any{e.cfg.Enrich.Embedder.MediaName: vector},
				})
			}
		}
		if isVideo && e.cfg.Enrich.Embedder.VideoEnable {
			frames, err := videoFrames(ctx, data, e.cfg.Enrich.Embedder.VideoFrames)
			if err != nil {
				enrichErrs = append(enrichErrs, fmt.Errorf("decode video frames: %w", err))
			}
			for i, frame := range frames {
				vector, err := e.embedder.EmbedImageWithText(ctx, text, frame.data)
				if err != nil {
					enrichErrs = append(enrichErrs, fmt.Errorf("embed video frame %d: %w", i, err))
					continue
				}
				mediaDocs = append(mediaDocs, &mediaDocument{
					ID:        fmt.Sprintf("%d_%d_video_%d", job.ChatID, job.MessageID, i),
					ChatID:    job.ChatID,
					MessageID: int64(job.MessageID),
					UserID:    job.UserID,
					Type:      job.MsgType,
					Timestamp: job.Timestamp,
					Modality:  "image",
					MediaType: "video",
					Loc:       frame.at,
					LocEnd:    frame.at,
					Text:      truncate(text, 500),
					Vectors:   map[string]any{e.cfg.Enrich.Embedder.MediaName: vector},
				})
			}
		}
		if isAudio || isVideo {
			if e.cfg.Enrich.Embedder.AudioEnable {
				windows, err := audioWindows(ctx, data, isVideo)
				if err != nil {
					enrichErrs = append(enrichErrs, fmt.Errorf("decode audio: %w", err))
				}
				for _, window := range windows {
					vector, err := e.embedder.EmbedAudio(ctx, window.wav)
					if err != nil {
						enrichErrs = append(enrichErrs, fmt.Errorf("embed audio: %w", err))
						continue
					}
					mediaDocs = append(mediaDocs, &mediaDocument{
						ID:        fmt.Sprintf("%d_%d_audio_%d", job.ChatID, job.MessageID, int(window.start*1000)),
						ChatID:    job.ChatID,
						MessageID: int64(job.MessageID),
						UserID:    job.UserID,
						Type:      job.MsgType,
						Timestamp: job.Timestamp,
						Modality:  "audio",
						MediaType: mediaType,
						Loc:       window.start,
						LocEnd:    window.end,
						Text:      truncate(text, 500),
						Vectors:   map[string]any{e.cfg.Enrich.Embedder.MediaName: vector},
					})
				}
			}
		}
		// Captions/OCR already live in the main keyword index. Only documents
		// whose body is text (PDF) need a separate semantic text vector.
		if isPDF && text != "" {
			fullText := truncate(text, 8000)
			vector, err := e.embedder.EmbedText(ctx, "title: none | text: "+fullText)
			if err != nil {
				enrichErrs = append(enrichErrs, fmt.Errorf("embed document text: %w", err))
			} else {
				mediaDocs = append(mediaDocs, &mediaDocument{
					ID:        fmt.Sprintf("%d_%d_text", job.ChatID, job.MessageID),
					ChatID:    job.ChatID,
					MessageID: int64(job.MessageID),
					UserID:    job.UserID,
					Type:      job.MsgType,
					Timestamp: job.Timestamp,
					Modality:  "text",
					MediaType: mediaType,
					Text:      truncate(text, 500),
					Vectors:   map[string]any{e.cfg.Enrich.Embedder.MediaName: vector},
				})
			}
		}
	}

	if len(update.Tags) > 0 || update.Extracted != "" {
		if err := e.inner.UpdateDocuments(ctx, job.ChatID, []*types.MessageDocument{update}); err != nil {
			return fmt.Errorf("update main document: %w", err)
		}
	}
	if len(mediaDocs) > 0 && e.media != nil {
		if err := e.media.add(ctx, mediaDocs); err != nil {
			return fmt.Errorf("add media vectors: %w", err)
		}
		// The message may have been deleted while it was being enriched. This
		// best-effort check removes vectors written after that delete.
		if exists, err := e.documentExists(ctx, job.ChatID, job.MessageID); err == nil && !exists {
			if err := e.media.deleteMessages(ctx, job.ChatID, []int{job.MessageID}); err != nil {
				log.FromContext(ctx).Warnf("Delete orphan media vectors for %d_%d: %v", job.ChatID, job.MessageID, err)
			}
		}
	}
	if len(enrichErrs) > 0 {
		return fmt.Errorf("enrich media for %d_%d: %w", job.ChatID, job.MessageID, errors.Join(enrichErrs...))
	}
	return nil
}

// maxMediaFetch bounds the adaptive candidate window for media searches.
const maxMediaFetch = 1000

// searchMediaHits returns at least need unique messages when the media index
// holds them. One message can produce several media documents (image, text and
// audio windows), so a raw hit window can deduplicate to fewer messages.
func (e *Engine) searchMediaHits(ctx context.Context, req types.SearchRequest, vector []float32, need int64, target, queryKind string) ([]types.SearchHit, int64, error) {
	if need <= 0 {
		need = types.PerSearchLimit
	}
	fetch := need
	var total int64
	for {
		raw, mediaTotal, err := e.media.search(ctx, req, vector, fetch, target, queryKind)
		if err != nil {
			return nil, 0, err
		}
		total = mediaTotal
		hits, err := e.hydrate(ctx, raw)
		if err != nil {
			return nil, 0, err
		}
		if int64(len(hits)) >= need || int64(len(raw)) < fetch || fetch >= maxMediaFetch {
			return hits, total, nil
		}
		fetch = min(fetch*2, maxMediaFetch)
	}
}

func (e *Engine) documentExists(ctx context.Context, chatID int64, messageID int) (bool, error) {
	docs, err := e.inner.GetDocuments(ctx, chatID, []int{messageID})
	if err != nil {
		return false, err
	}
	return len(docs) > 0, nil
}

func (e *Engine) hydrate(ctx context.Context, hits []mediaHit) ([]types.SearchHit, error) {
	if len(hits) == 0 {
		return nil, nil
	}
	byChat := make(map[int64][]int)
	for _, hit := range hits {
		byChat[hit.ChatID] = append(byChat[hit.ChatID], int(hit.MessageID))
	}
	docs := make(map[string]*types.MessageDocument)
	for chatID, ids := range byChat {
		found, err := e.inner.GetDocuments(ctx, chatID, ids)
		if err != nil {
			return nil, err
		}
		for _, doc := range found {
			docs[fmt.Sprintf("%d_%d", doc.ChatID, doc.ID)] = doc
		}
	}
	seen := make(map[string]struct{}, len(hits))
	out := make([]types.SearchHit, 0, len(hits))
	for _, hit := range hits {
		key := fmt.Sprintf("%d_%d", hit.ChatID, hit.MessageID)
		if _, ok := seen[key]; ok {
			continue
		}
		doc, ok := docs[key]
		if !ok {
			continue
		}
		seen[key] = struct{}{}
		formatted := types.SearchHitFormatted{
			ID:          fmt.Sprintf("%d", doc.ID),
			Type:        fmt.Sprintf("%d", doc.Type),
			Message:     doc.Message,
			Ocred:       doc.Ocred,
			AIGenerated: doc.AIGenerated,
			Extracted:   doc.Extracted,
			UserID:      fmt.Sprintf("%d", doc.UserID),
			ChatID:      fmt.Sprintf("%d", doc.ChatID),
			Timestamp:   fmt.Sprintf("%d", doc.Timestamp),
		}
		out = append(out, types.SearchHit{
			MessageDocument: *doc,
			Formatted:       formatted,
		})
	}
	return out, nil
}

// keywordPage trims a keyword candidate window to the requested page after a
// semantic failure.
func keywordPage(resp *types.SearchResponse, offset, limit int64) *types.SearchResponse {
	resp.Hits = paginate(resp.Hits, offset, limit)
	resp.Offset = offset
	resp.Limit = limit
	return resp
}

// rrfK dampens rank contributions in Reciprocal Rank Fusion.
const rrfK = 60.0

// fuseHits merges two ranked lists with Reciprocal Rank Fusion. A message that
// appears in both lists keeps one entry whose score is the sum of both ranks.
func fuseHits(textHits, mediaHits []types.SearchHit) []types.SearchHit {
	type entry struct {
		hit   types.SearchHit
		score float64
	}
	entries := make(map[string]*entry, len(textHits)+len(mediaHits))
	order := make([]*entry, 0, len(textHits)+len(mediaHits))
	add := func(hits []types.SearchHit) {
		for rank, hit := range hits {
			key := fmt.Sprintf("%d_%d", hit.ChatID, hit.ID)
			item, ok := entries[key]
			if !ok {
				item = &entry{hit: hit}
				entries[key] = item
				order = append(order, item)
			}
			item.score += 1 / (rrfK + float64(rank+1))
		}
	}
	add(textHits)
	add(mediaHits)
	sort.SliceStable(order, func(i, j int) bool {
		if order[i].score == order[j].score {
			return order[i].hit.Timestamp > order[j].hit.Timestamp
		}
		return order[i].score > order[j].score
	})
	out := make([]types.SearchHit, len(order))
	for i, item := range order {
		out[i] = item.hit
	}
	return out
}

func paginate(hits []types.SearchHit, offset, limit int64) []types.SearchHit {
	if offset < 0 {
		offset = 0
	}
	start := int(min(offset, int64(len(hits))))
	end := int(min(int64(start)+limit, int64(len(hits))))
	if end < start {
		end = start
	}
	return hits[start:end]
}

func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	text = text[:limit]
	for len(text) > 0 && !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text
}
