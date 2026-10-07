package plugin

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/krau/mygotg/ext"
	"github.com/krau/mygotg/storage"
	"github.com/krau/mygotg/types"
)

type repeatTestAPI struct {
	mu              sync.Mutex
	active          int
	maxActive       int
	deletes         int
	forwards        int
	randomIDs       []int64
	edits           []string
	deleteErr       error
	editErr         error
	forwardErrors   map[int]error
	cancel          context.CancelFunc
	cancelAtForward int
	requests        chan string
	responses       chan struct{}
	delay           time.Duration
}

func (f *repeatTestAPI) Invoke(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
	f.mu.Lock()
	f.active++
	f.maxActive = max(f.maxActive, f.active)
	var (
		response bin.Encoder
		err      error
		kind     string
		cancel   context.CancelFunc
	)
	switch req := input.(type) {
	case *tg.ChannelsDeleteMessagesRequest:
		f.deletes++
		kind = "delete"
		err = f.deleteErr
		response = &tg.MessagesAffectedMessages{}
	case *tg.MessagesForwardMessagesRequest:
		f.forwards++
		kind = "forward"
		if len(req.ID) != 1 || req.ID[0] != 90 || len(req.RandomID) != 1 {
			err = fmt.Errorf("unexpected forward IDs: %v, random IDs: %v", req.ID, req.RandomID)
		} else {
			f.randomIDs = append(f.randomIDs, req.RandomID[0])
			err = f.forwardErrors[f.forwards]
		}
		if f.forwards == f.cancelAtForward {
			cancel = f.cancel
		}
		response = &tg.Updates{}
	case *tg.MessagesEditMessageRequest:
		f.edits = append(f.edits, req.Message)
		kind = "edit"
		err = f.editErr
		response = &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateEditChannelMessage{
			Message: &tg.Message{ID: req.ID, PeerID: &tg.PeerChannel{ChannelID: testCleanChatID}, Message: req.Message},
		}}}
	default:
		err = fmt.Errorf("unexpected request %T", input)
	}
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.active--
		f.mu.Unlock()
	}()
	if cancel != nil {
		cancel()
	}
	if f.requests != nil {
		select {
		case f.requests <- kind:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if f.responses != nil {
		select {
		case <-f.responses:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if f.delay != 0 {
		time.Sleep(f.delay)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	return encodeInto(output, response)
}

func newRepeatTestEnv(ctx context.Context, f *repeatTestAPI, args ...string) (*Context, *ext.Update) {
	peers := storage.NewPeerStorage(nil, true)
	peers.AddPeer(testCleanChatID, 42, storage.TypeChannel, "")
	channel := &tg.Channel{ID: testCleanChatID, AccessHash: 42, Megagroup: true}
	entities := &tg.Entities{Channels: map[int64]*tg.Channel{channel.ID: channel}}
	pluginCtx := &Context{
		Context: ext.NewContext(ctx, tg.NewClient(f), peers, &tg.User{ID: 1}, nil, entities, false),
		Args:    args,
		Cmd:     "re",
	}
	update := testCleanMessageUpdate(entities, 100, ",re")
	update.EffectiveMessage.ReplyToMessage = types.ConstructMessage(&tg.Message{
		ID: 90, PeerID: &tg.PeerChannel{ChannelID: testCleanChatID}, Message: "source",
	})
	return pluginCtx, update
}

func TestRepeatSerialRPCsAndCompleteCount(t *testing.T) {
	f := &repeatTestAPI{delay: 2 * time.Millisecond}
	ctx, update := newRepeatTestEnv(context.Background(), f, "100")
	if err := RepeatHandler(ctx, update); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.maxActive != 1 {
		t.Fatalf("concurrent RPCs sharing ext.Context.random: %d", f.maxActive)
	}
	if f.deletes != 1 || f.forwards != 100 || f.active != 0 {
		t.Fatalf("incomplete repeat: deletes=%d forwards=%d active=%d", f.deletes, f.forwards, f.active)
	}
	seen := make(map[int64]struct{}, len(f.randomIDs))
	for _, id := range f.randomIDs {
		if _, ok := seen[id]; ok {
			t.Fatalf("duplicate forward RandomID: %d", id)
		}
		seen[id] = struct{}{}
	}
}

func TestRepeatWaitsForIndependentRPCResponses(t *testing.T) {
	clientCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	f := &repeatTestAPI{requests: make(chan string, 4), responses: make(chan struct{})}
	ctx, update := newRepeatTestEnv(clientCtx, f, "3")
	done := make(chan error, 1)
	go func() { done <- RepeatHandler(ctx, update) }()
	for i := range 4 {
		select {
		case <-f.requests:
		case err := <-done:
			t.Fatalf("handler returned before response %d: %v", i+1, err)
		case <-clientCtx.Done():
			t.Fatal("RPC request did not arrive")
		}
		select {
		case err := <-done:
			t.Fatalf("handler returned while response %d was withheld: %v", i+1, err)
		default:
		}
		select {
		case f.responses <- struct{}{}:
		case <-clientCtx.Done():
			t.Fatal("independent response delivery blocked")
		}
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-clientCtx.Done():
		t.Fatal("handler did not complete after all responses")
	}
}

func TestRepeatReturnsErrorsAndContinuesCount(t *testing.T) {
	deleteErr := errors.New("delete failed")
	forwardErr := errors.New("forward failed")
	f := &repeatTestAPI{deleteErr: deleteErr, forwardErrors: map[int]error{2: forwardErr}}
	ctx, update := newRepeatTestEnv(context.Background(), f, "3")
	err := RepeatHandler(ctx, update)
	if !errors.Is(err, deleteErr) || !errors.Is(err, forwardErr) {
		t.Fatalf("missing RPC errors: %v", err)
	}
	if f.forwards != 3 {
		t.Fatalf("ordinary RPC error interrupted repeat count: %d", f.forwards)
	}
}

func TestRepeatCancellationStopsFurtherRequests(t *testing.T) {
	clientCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &repeatTestAPI{cancel: cancel, cancelAtForward: 2}
	ctx, update := newRepeatTestEnv(clientCtx, f, "100")
	if err := RepeatHandler(ctx, update); !errors.Is(err, context.Canceled) {
		t.Fatalf("missing cancellation: %v", err)
	}
	if f.deletes != 1 || f.forwards != 2 {
		t.Fatalf("requests continued after cancellation: deletes=%d forwards=%d", f.deletes, f.forwards)
	}
}

func TestRepeatUsageAndInvalidCountReturnEditErrors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		arg     string
		noReply bool
	}{
		{name: "usage", noReply: true},
		{name: "not a number", arg: "x"},
		{name: "zero", arg: "0"},
		{name: "too many", arg: "101"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			editErr := errors.New("edit failed")
			f := &repeatTestAPI{editErr: editErr}
			ctx, update := newRepeatTestEnv(context.Background(), f, tc.arg)
			if tc.noReply {
				update.EffectiveMessage.ReplyToMessage = nil
			}
			if err := RepeatHandler(ctx, update); !errors.Is(err, editErr) {
				t.Fatalf("missing edit error: %v", err)
			}
			if len(f.edits) != 1 || f.deletes != 0 || f.forwards != 0 {
				t.Fatalf("invalid command performed repeat: edits=%d deletes=%d forwards=%d", len(f.edits), f.deletes, f.forwards)
			}
		})
	}
}

func TestRepeatDefaultAndMinimumCount(t *testing.T) {
	for _, args := range [][]string{nil, {"1"}} {
		f := &repeatTestAPI{}
		ctx, update := newRepeatTestEnv(context.Background(), f, args...)
		if err := RepeatHandler(ctx, update); err != nil {
			t.Fatal(err)
		}
		if f.deletes != 1 || f.forwards != 1 {
			t.Fatalf("unexpected single repeat: deletes=%d forwards=%d", f.deletes, f.forwards)
		}
	}
}

func TestRepeatAlreadyCanceledDoesNotRequest(t *testing.T) {
	clientCtx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &repeatTestAPI{}
	ctx, update := newRepeatTestEnv(clientCtx, f, "100")
	if err := RepeatHandler(ctx, update); !errors.Is(err, context.Canceled) {
		t.Fatalf("missing cancellation: %v", err)
	}
	if f.deletes != 0 || f.forwards != 0 {
		t.Fatalf("requests after cancellation: deletes=%d forwards=%d", f.deletes, f.forwards)
	}
}
