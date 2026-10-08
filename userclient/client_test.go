package userclient

import (
	"context"
	"testing"

	"github.com/krau/btts/config"
)

func TestNewUserClientRejectsCachedModeMismatch(t *testing.T) {
	previous := uc
	t.Cleanup(func() { uc = previous })
	for _, noUpdates := range []bool{false, true} {
		uc = &UserClient{noUpdates: noUpdates}
		var matching, conflicting []Option
		if noUpdates {
			matching = []Option{WithNoUpdates()}
		} else {
			conflicting = []Option{WithNoUpdates()}
		}
		cached, err := NewUserClient(context.Background(), matching...)
		if err != nil || cached != uc {
			t.Fatalf("same-mode cache reuse (NoUpdates=%t): client=%p, error=%v", noUpdates, cached, err)
		}
		cached, err = NewUserClient(context.Background(), conflicting...)
		if err == nil || cached != nil {
			t.Fatalf("mode mismatch must reject cached client (NoUpdates=%t): client=%p, error=%v", noUpdates, cached, err)
		}
		if uc == nil {
			t.Fatal("mode mismatch invalidated existing client")
		}
	}
}

func TestCloseReleasesCachedClient(t *testing.T) {
	previous := uc
	t.Cleanup(func() { uc = previous })
	uc = &UserClient{noUpdates: true}
	if err := uc.Close(); err != nil {
		t.Fatal(err)
	}
	if uc != nil {
		t.Fatal("closed client remains available for reuse")
	}
}

func TestNewUserClientRejectsInvalidLogLevelBeforeLogin(t *testing.T) {
	previousClient, previousLevel := uc, config.C.ClientLogLevel
	t.Cleanup(func() { uc, config.C.ClientLogLevel = previousClient, previousLevel })
	uc = nil
	config.C.ClientLogLevel = "verbose"
	client, err := NewUserClient(context.Background())
	if err == nil || client != nil || uc != nil {
		t.Fatalf("invalid log level started a client: client=%p cached=%p err=%v", client, uc, err)
	}
}
