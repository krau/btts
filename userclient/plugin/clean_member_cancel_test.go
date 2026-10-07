package plugin

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/krau/mygotg/types"
)

type floodWaitCleanMemberAPI struct {
	requested chan struct{}
	calls     int
}

func (f *floodWaitCleanMemberAPI) Invoke(_ context.Context, input bin.Encoder, _ bin.Decoder) error {
	if _, ok := input.(*tg.ChannelsEditBannedRequest); !ok {
		return fmt.Errorf("unexpected RPC %T", input)
	}
	f.calls++
	f.requested <- struct{}{}
	return tgerr.New(420, "FLOOD_WAIT_3600")
}

func TestCleanMemberFloodWaitCancellationStopsRetry(t *testing.T) {
	clientCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &floodWaitCleanMemberAPI{requested: make(chan struct{}, 1)}
	ctx, _ := newCleanMemberTestEnv(newFakeCleanMemberAPI(0))
	ctx.Context = clientCtx
	ctx.Raw = tg.NewClient(f)
	chat := &types.Channel{ID: testCleanChatID, AccessHash: 42, Megagroup: true}
	done := make(chan error, 1)
	go func() {
		_, err := kickCleanMember(ctx, chat, &tg.User{ID: 1001, AccessHash: 42})
		done <- err
	}()
	select {
	case <-f.requested:
	case <-time.After(3 * time.Second):
		t.Fatal("ban request did not arrive")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("FloodWait lost cancellation: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("FloodWait did not stop on cancellation")
	}
	if f.calls != 1 {
		t.Fatalf("retried after cancellation: %d calls", f.calls)
	}
}
