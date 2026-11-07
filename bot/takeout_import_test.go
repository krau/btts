package bot

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/krau/btts/database"
	"github.com/krau/btts/types"
	"github.com/krau/btts/userclient"
	"github.com/krau/mygotg/ext"
)

const takeoutImportTestChildEnv = "BTTS_TAKEOUT_IMPORT_TEST_CHILD"

type fakeTakeoutImporter struct {
	startRange  int
	startCursor int
	pages       []userclient.TakeoutChatPage
}

func (f *fakeTakeoutImporter) TakeoutImportChat(
	ctx context.Context,
	_ tg.InputPeerClass,
	_ int64,
	startRange, startCursor int,
	handler func(context.Context, userclient.TakeoutChatPage) error,
) error {
	f.startRange = startRange
	f.startCursor = startCursor
	for _, page := range f.pages {
		if err := handler(ctx, page); err != nil {
			return err
		}
	}
	return nil
}

type fakeTakeoutEnricher struct {
	enrichable bool
	needs      bool
	documents  []*types.MessageDocument
	enriched   []int64
	enqueued   []int64
}

func (f *fakeTakeoutEnricher) EnrichableMedia(string, string, types.MessageType) bool {
	return f.enrichable
}

func (f *fakeTakeoutEnricher) NeedsEnrichment(int64, *types.MessageDocument) bool {
	return f.needs
}

func (f *fakeTakeoutEnricher) EnqueueDocument(_ context.Context, _ int64, doc *types.MessageDocument) error {
	f.enqueued = append(f.enqueued, doc.ID)
	return nil
}

func (f *fakeTakeoutEnricher) EnrichNow(_ context.Context, _ int64, doc *types.MessageDocument, _ []byte, _ string) error {
	f.enriched = append(f.enriched, doc.ID)
	return nil
}

func (f *fakeTakeoutEnricher) AddDocumentsOnly(_ context.Context, _ int64, docs []*types.MessageDocument) error {
	f.documents = append(f.documents, docs...)
	return nil
}

func textChannelMessage(id int, text string) *tg.Message {
	msg := &tg.Message{
		ID:      id,
		PeerID:  &tg.PeerChannel{ChannelID: 1},
		Date:    1700000000 + id,
		Message: text,
	}
	msg.SetPost(true)
	return msg
}

func photoChannelMessage(id int) *tg.Message {
	msg := &tg.Message{
		ID:     id,
		PeerID: &tg.PeerChannel{ChannelID: 1},
		Date:   1700000000 + id,
	}
	msg.SetPost(true)
	msg.SetMedia(&tg.MessageMediaPhoto{})
	return msg
}

func TestTakeoutImportPages(t *testing.T) {
	if os.Getenv(takeoutImportTestChildEnv) != "1" {
		child := exec.Command(os.Args[0], "-test.run=^TestTakeoutImportPages$")
		child.Env = append(os.Environ(), takeoutImportTestChildEnv+"=1")
		if output, err := child.CombinedOutput(); err != nil {
			t.Fatalf("isolated takeout test: %v\n%s", err, output)
		}
		return
	}
	t.Chdir(t.TempDir())
	if err := os.Mkdir("data", 0o700); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := database.InitDatabase(ctx); err != nil {
		t.Fatal(err)
	}
	extCtx := &ext.Context{Context: ctx, Self: &tg.User{ID: 42}}
	peer := &tg.InputPeerChannel{ChannelID: 1}

	// Text page without enrichment: documents go through AddDocuments and the
	// page cursor is persisted.
	imp, _, err := database.StartImport(ctx, 10, "takeout", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	adder := &recordingAdder{}
	importer := &fakeTakeoutImporter{pages: []userclient.TakeoutChatPage{{
		Messages: []*tg.Message{textChannelMessage(20, "first"), textChannelMessage(19, "second")},
		Range:    3,
		Cursor:   0,
		Download: func(context.Context, tg.MessageMediaClass, io.Writer) error { return nil },
	}}}
	processed, finalRange, finalCursor, err := importHistoryTakeout(
		ctx, extCtx, extCtx, importer, adder, peer, 10, 1, 5, imp.Generation, 0, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if importer.startRange != 1 || importer.startCursor != 5 {
		t.Fatalf("resume position = %d/%d, want 1/5", importer.startRange, importer.startCursor)
	}
	if processed != 2 || finalRange != 3 || finalCursor != 0 {
		t.Fatalf("result = %d/%d/%d, want 2/3/0", processed, finalRange, finalCursor)
	}
	if len(adder.batches) != 1 || len(adder.batches[0]) != 2 {
		t.Fatalf("unexpected indexed batches: %+v", adder.batches)
	}
	stored, err := database.GetImport(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if stored.RangeIndex != 3 || stored.Cursor != 0 || stored.LeaseAt == 0 {
		t.Fatalf("unexpected stored progress: %+v", stored)
	}

	// Media page with inline enrichment: AddDocumentsOnly is used and the
	// downloaded bytes are enriched immediately.
	imp, _, err = database.StartImport(ctx, 11, "takeout", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	enricher := &fakeTakeoutEnricher{enrichable: true, needs: true}
	importer = &fakeTakeoutImporter{pages: []userclient.TakeoutChatPage{{
		Messages: []*tg.Message{photoChannelMessage(30)},
		Range:    1,
		Download: func(_ context.Context, _ tg.MessageMediaClass, w io.Writer) error {
			_, err := w.Write([]byte("image-bytes"))
			return err
		},
	}}}
	if _, _, _, err := importHistoryTakeout(
		ctx, extCtx, extCtx, importer, adder, peer, 11, 0, 0, imp.Generation, 0, enricher,
	); err != nil {
		t.Fatal(err)
	}
	if len(enricher.documents) != 1 || enricher.documents[0].ID != 30 {
		t.Fatalf("enriched documents = %+v, want message 30", enricher.documents)
	}
	if len(enricher.enriched) != 1 || enricher.enriched[0] != 30 {
		t.Fatalf("inline enrichments = %v, want [30]", enricher.enriched)
	}
	if len(enricher.enqueued) != 0 {
		t.Fatalf("unexpected fallback jobs: %v", enricher.enqueued)
	}

	// Download failure falls back to the durable queue instead of aborting.
	imp, _, err = database.StartImport(ctx, 12, "takeout", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	fallback := &fakeTakeoutEnricher{enrichable: true, needs: true}
	importer = &fakeTakeoutImporter{pages: []userclient.TakeoutChatPage{{
		Messages: []*tg.Message{photoChannelMessage(31)},
		Range:    1,
		Download: func(context.Context, tg.MessageMediaClass, io.Writer) error {
			return errors.New("download failed")
		},
	}}}
	if _, _, _, err := importHistoryTakeout(
		ctx, extCtx, extCtx, importer, adder, peer, 12, 0, 0, imp.Generation, 0, fallback,
	); err != nil {
		t.Fatal(err)
	}
	if len(fallback.enriched) != 0 {
		t.Fatalf("unexpected enrichments: %v", fallback.enriched)
	}
	if len(fallback.enqueued) != 1 || fallback.enqueued[0] != 31 {
		t.Fatalf("fallback queue = %v, want [31]", fallback.enqueued)
	}
	if got, err := database.GetImport(ctx, 12); err != nil || got.RangeIndex != 1 {
		t.Fatalf("fallback progress: %+v, %v", got, err)
	}
}
