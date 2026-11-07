package types

import "context"

// Capabilities describes the optional features of the active search engine.
// Callers must check these flags instead of asserting on concrete engine types.
type Capabilities struct {
	// ImageSearch reports whether SearchImage can run.
	ImageSearch bool
	// TagFilters reports whether messages carry tags and can be filtered by them.
	TagFilters bool
	// Semantic reports whether media vectors are available for hybrid/media search.
	Semantic bool
	// Enrichment reports whether a media enrichment pipeline is attached.
	Enrichment bool
}

// MediaEnricher is the media enrichment pipeline attached to a Searcher.
// A nil MediaEnricher means enrichment is disabled.
type MediaEnricher interface {
	// EnrichableMedia reports whether media with this MIME/name will be enriched.
	// Document builders keep media-only messages only when this returns true.
	EnrichableMedia(mime, name string, messageType MessageType) bool
	// NeedsEnrichment reports whether a document would do any media work.
	NeedsEnrichment(chatID int64, doc *MessageDocument) bool
	// EnqueueDocument stores a document in the durable queue.
	EnqueueDocument(ctx context.Context, chatID int64, doc *MessageDocument) error
	// EnrichNow processes media bytes downloaded by another transport.
	EnrichNow(ctx context.Context, chatID int64, doc *MessageDocument, data []byte, mime string) error
	// AddDocumentsOnly writes main documents without enqueueing media jobs.
	AddDocumentsOnly(ctx context.Context, chatID int64, docs []*MessageDocument) error
}
