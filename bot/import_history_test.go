package bot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/krau/btts/database"
	"github.com/krau/btts/types"
	"github.com/krau/mygotg/ext"
)

const importHistoryTestChildEnv = "BTTS_IMPORT_HISTORY_TEST_CHILD"

// historyInvoker serves descending message pages from an in-memory history.
type historyInvoker struct {
	total   int
	mu      sync.Mutex
	offsets []int
}

func (h *historyInvoker) Invoke(_ context.Context, input bin.Encoder, output bin.Decoder) error {
	req, ok := input.(*tg.MessagesGetHistoryRequest)
	if !ok {
		return fmt.Errorf("unexpected request %T", input)
	}
	box, ok := output.(*tg.MessagesMessagesBox)
	if !ok {
		return fmt.Errorf("unexpected response %T", output)
	}
	h.mu.Lock()
	h.offsets = append(h.offsets, req.OffsetID)
	h.mu.Unlock()

	limit := req.Limit
	if limit <= 0 {
		limit = 100
	}
	highest := req.OffsetID
	if highest == 0 || highest > h.total {
		highest = h.total + 1
	}
	msgs := make([]tg.MessageClass, 0, limit)
	for id := highest - 1; id > 0 && len(msgs) < limit; id-- {
		msg := &tg.Message{
			ID:      id,
			PeerID:  &tg.PeerChannel{ChannelID: 1},
			Date:    1700000000 + id,
			Message: fmt.Sprintf("message %d", id),
		}
		msg.SetPost(true)
		msgs = append(msgs, msg)
	}
	box.Messages = &tg.MessagesMessagesSlice{Count: h.total, Messages: msgs}
	return nil
}

func (h *historyInvoker) requestedOffsets() []int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]int(nil), h.offsets...)
}

type fakeHistoryAPI struct {
	client *tg.Client
}

func (f fakeHistoryAPI) API() *tg.Client { return f.client }

type recordingAdder struct {
	mu      sync.Mutex
	batches [][]*types.MessageDocument
	calls   int
	failAt  int
}

func (r *recordingAdder) AddDocuments(_ context.Context, _ int64, docs []*types.MessageDocument) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.failAt > 0 && r.calls == r.failAt {
		return errors.New("index unavailable")
	}
	r.batches = append(r.batches, append([]*types.MessageDocument(nil), docs...))
	return nil
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestImportHistoryPaginationAndCursor(t *testing.T) {
	// The database singleton is process-global; run this test in a child
	// process like the other database-backed bot tests.
	if os.Getenv(importHistoryTestChildEnv) != "1" {
		child := exec.Command(os.Args[0], "-test.run=^TestImportHistoryPaginationAndCursor$")
		child.Env = append(os.Environ(), importHistoryTestChildEnv+"=1")
		if output, err := child.CombinedOutput(); err != nil {
			t.Fatalf("isolated import test: %v\n%s", err, output)
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

	imp, _, err := database.StartImport(ctx, 1, "raw", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	invoker := &historyInvoker{total: 250}
	adder := &recordingAdder{}
	processed, cursor, err := importHistory(
		ctx, extCtx, extCtx, fakeHistoryAPI{client: tg.NewClient(invoker)}, adder,
		peer, 1, 0, imp.Generation, 0, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if processed != 250 || cursor != 1 {
		t.Fatalf("got processed=%d cursor=%d, want 250/1", processed, cursor)
	}
	sizes := make([]int, len(adder.batches))
	for i, batch := range adder.batches {
		sizes[i] = len(batch)
	}
	if len(adder.batches) != 3 || len(adder.batches[0]) != 100 || len(adder.batches[1]) != 100 || len(adder.batches[2]) != 50 {
		t.Fatalf("unexpected batch sizes: %v", sizes)
	}
	if adder.batches[0][0].ID != 250 || adder.batches[0][0].Message != "message 250" {
		t.Fatalf("unexpected first document: %+v", adder.batches[0][0])
	}
	if adder.batches[0][0].Priority != 1 {
		t.Fatalf("imported document priority = %d, want 1", adder.batches[0][0].Priority)
	}
	// The gotd iterator probes once more after a short batch, so the final
	// request is an empty page at the last cursor.
	if want := []int{0, 151, 51, 1}; !equalInts(invoker.requestedOffsets(), want) {
		t.Fatalf("requested offsets = %v, want %v", invoker.requestedOffsets(), want)
	}
	stored, err := database.GetImport(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Cursor != 1 || stored.Completed || stored.LeaseAt == 0 {
		t.Fatalf("unexpected stored state: %+v", stored)
	}

	// Resume from the persisted cursor: Telegram returns nothing older.
	if err := database.StopImport(ctx, 1, imp.Generation); err != nil {
		t.Fatal(err)
	}
	resumedImp, resumed, err := database.StartImport(ctx, 1, "raw", time.Hour)
	if err != nil || !resumed {
		t.Fatalf("resume: %+v, %v", resumedImp, err)
	}
	resumeInvoker := &historyInvoker{total: 250}
	resumeAdder := &recordingAdder{}
	processed, cursor, err = importHistory(
		ctx, extCtx, extCtx, fakeHistoryAPI{client: tg.NewClient(resumeInvoker)}, resumeAdder,
		peer, 1, resumedImp.Cursor, resumedImp.Generation, 0, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if processed != 0 || cursor != 1 || len(resumeAdder.batches) != 0 {
		t.Fatalf("resume reindexed messages: processed=%d cursor=%d batches=%d", processed, cursor, len(resumeAdder.batches))
	}
	if want := []int{1}; !equalInts(resumeInvoker.requestedOffsets(), want) {
		t.Fatalf("resume offsets = %v, want %v", resumeInvoker.requestedOffsets(), want)
	}

	// A failing index write persists the previous batch cursor and stops.
	imp, _, err = database.StartImport(ctx, 2, "raw", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	failInvoker := &historyInvoker{total: 250}
	failAdder := &recordingAdder{failAt: 2}
	failCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	processed, cursor, err = importHistory(
		failCtx, extCtx, extCtx, fakeHistoryAPI{client: tg.NewClient(failInvoker)}, failAdder,
		peer, 2, 0, imp.Generation, 0, nil,
	)
	if err == nil {
		t.Fatal("expected import error")
	}
	if processed != 100 || cursor != 151 {
		t.Fatalf("failed run: processed=%d cursor=%d, want 100/151", processed, cursor)
	}
	failed, err := database.GetImport(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Cursor != 151 {
		t.Fatalf("failed run did not persist cursor: %+v", failed)
	}
}
