package engine

import (
	"context"
	"testing"

	"github.com/gotd/td/tg"
	"github.com/krau/btts/types"
)

type fixedEnricher struct {
	enrichable bool
}

func (f fixedEnricher) EnrichableMedia(string, string, types.MessageType) bool { return f.enrichable }
func (f fixedEnricher) NeedsEnrichment(int64, *types.MessageDocument) bool     { return f.enrichable }
func (f fixedEnricher) EnqueueDocument(context.Context, int64, *types.MessageDocument) error {
	return nil
}
func (f fixedEnricher) EnrichNow(context.Context, int64, *types.MessageDocument, []byte, string) error {
	return nil
}
func (f fixedEnricher) AddDocumentsOnly(context.Context, int64, []*types.MessageDocument) error {
	return nil
}

func photoMessage(id int) *tg.Message {
	msg := &tg.Message{
		ID:     id,
		PeerID: &tg.PeerChannel{ChannelID: 100},
		Date:   1700000000 + id,
	}
	msg.SetPost(true)
	msg.SetMedia(&tg.MessageMediaPhoto{})
	return msg
}

func TestDocumentsFromMessagesKeepsEnrichableMediaOnly(t *testing.T) {
	ctx := context.Background()
	msg := photoMessage(1)

	docs := DocumentsFromMessages(ctx, []*tg.Message{msg}, 100, 42, nil, false, fixedEnricher{enrichable: true})
	if len(docs) != 1 {
		t.Fatalf("enrichable media: got %d docs", len(docs))
	}
	doc := docs[0]
	if doc.Message != "" {
		t.Fatalf("media-only message must not carry placeholder text, got %q", doc.Message)
	}
	if !doc.HasMedia || doc.MediaMIME != "image/jpeg" || doc.Type != int(types.MessageTypePhoto) {
		t.Fatalf("unexpected media metadata: %+v", doc)
	}

	if dropped := DocumentsFromMessages(ctx, []*tg.Message{msg}, 100, 42, nil, false, fixedEnricher{}); len(dropped) != 0 {
		t.Fatalf("non-enrichable media should be dropped, got %+v", dropped)
	}
	if dropped := DocumentsFromMessages(ctx, []*tg.Message{msg}, 100, 42, nil, false, nil); len(dropped) != 0 {
		t.Fatalf("disabled enrichment should drop media-only messages, got %+v", dropped)
	}

	text := &tg.Message{ID: 2, PeerID: &tg.PeerChannel{ChannelID: 100}, Date: 1700000002, Message: "hello"}
	text.SetPost(true)
	docs = DocumentsFromMessages(ctx, []*tg.Message{text}, 100, 42, nil, false, nil)
	if len(docs) != 1 || docs[0].Message != "hello" || docs[0].HasMedia {
		t.Fatalf("text message: %+v", docs)
	}
}

func TestMarkImported(t *testing.T) {
	docs := []*types.MessageDocument{{ID: 1}, nil, {ID: 3, Priority: 0}}
	MarkImported(docs)
	if docs[0].Priority != 1 || docs[2].Priority != 1 {
		t.Fatalf("priority not set: %+v", docs)
	}
}
