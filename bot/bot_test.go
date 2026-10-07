package bot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
	"github.com/krau/btts/database"
	"github.com/krau/btts/userclient"
	"github.com/krau/mygotg"
	"github.com/krau/mygotg/session"
)

type failedRecoveryStorage struct {
	updates.StateStorage
	err error
}

func (s failedRecoveryStorage) GetState(context.Context, int64) (updates.State, bool, error) {
	return updates.State{}, false, s.err
}

func newLifecycleClient(t *testing.T, ctx context.Context, recovery bool, storage updates.StateStorage, ready chan<- struct{}) (*mygotg.Client, <-chan error) {
	t.Helper()
	exited := make(chan error, 1)
	client, err := mygotg.NewClient(1, "synthetic", mygotg.ClientTypePhone("synthetic"), &mygotg.ClientOpts{
		Context: ctx, InMemory: true, Session: session.SimpleSession(), DisableCopyright: true,
		NoUpdates: !recovery, DeferUpdateRecovery: recovery, UpdateStateStorage: storage,
		RunMiddleware: func(_ func(context.Context, func(context.Context) error) error, ctx context.Context, run func(context.Context) error) error {
			err := run(ctx)
			exited <- err
			return err
		},
		Middlewares: []telegram.Middleware{telegram.MiddlewareFunc(func(tg.Invoker) telegram.InvokeFunc {
			return func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				switch input.(type) {
				case *tg.UsersGetUsersRequest:
					output.(*tg.UserClassVector).Elems = []tg.UserClass{&tg.User{ID: 42, Self: true, AccessHash: 1042}}
				case *tg.UpdatesGetStateRequest:
					*output.(*tg.UpdatesState) = tg.UpdatesState{Pts: 10, Date: 1}
				case *tg.UpdatesGetDifferenceRequest:
					output.(*tg.UpdatesDifferenceBox).Difference = &tg.UpdatesDifferenceEmpty{Date: 1}
					if ready != nil {
						select {
						case ready <- struct{}{}:
						case <-ctx.Done():
							return ctx.Err()
						}
					}
				case *tg.BotsSetBotCommandsRequest:
					*output.(*tg.BoolBox) = tg.BoolBox{Bool: &tg.BoolTrue{}}
				default:
					return fmt.Errorf("unexpected synthetic RPC %T", input)
				}
				return nil
			}
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Stop)
	return client, exited
}

func TestStartRecoveryFailureStopsClients(t *testing.T) {
	// The production database singleton must not leak into other tests.
	if os.Getenv("BTTS_RECOVERY_TEST_CHILD") != "1" {
		child := exec.Command(os.Args[0], "-test.run=^TestStartRecoveryFailureStopsClients$")
		child.Env = append(os.Environ(), "BTTS_RECOVERY_TEST_CHILD=1")
		if output, err := child.CombinedOutput(); err != nil {
			t.Fatalf("isolated bot lifecycle: %v\n%s", err, output)
		}
		return
	}
	t.Chdir(t.TempDir())
	if err := os.Mkdir("data", 0700); err != nil {
		t.Fatal(err)
	}
	if err := database.InitDatabase(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, fail := range []bool{true, false} {
		t.Run(fmt.Sprintf("recovery_failure=%t", fail), func(t *testing.T) {
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			var logs bytes.Buffer
			ctx := log.WithContext(parent, log.NewWithOptions(&logs, log.Options{Level: log.DebugLevel}))
			failure := errors.New("state read failed")
			var storage updates.StateStorage = database.NewUpdatesStorage()
			if fail {
				storage = failedRecoveryStorage{StateStorage: storage, err: failure}
			}
			ready := make(chan struct{}, 1)
			user, userExited := newLifecycleClient(t, ctx, true, storage, ready)
			client, botExited := newLifecycleClient(t, ctx, false, nil, nil)
			b := &Bot{Client: client, UserClient: &userclient.UserClient{TClient: user}}
			done := make(chan error, 1)
			go func() { done <- b.Start(ctx) }()
			if !fail {
				select {
				case <-ready:
				case err := <-done:
					t.Fatalf("successful startup returned early: %v", err)
				case <-time.After(5 * time.Second):
					t.Fatal("recovery did not start")
				}
				select {
				case err := <-done:
					t.Fatalf("bot stopped before cancellation: %v", err)
				default:
				}
				cancel()
			}
			select {
			case err := <-done:
				if fail && !errors.Is(err, failure) {
					t.Fatalf("startup lost original recovery error: %v", err)
				}
				if !fail && err != nil {
					t.Fatalf("normal shutdown failed: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("bot startup/shutdown did not return")
			}
			if started := strings.Contains(logs.String(), "Bot started."); started == fail {
				t.Fatalf("incorrect startup log: %s", logs.String())
			}
			for _, exited := range []<-chan error{userExited, botExited} {
				select {
				case <-exited:
				case <-time.After(5 * time.Second):
					t.Fatal("client survived bot exit")
				}
			}
		})
	}
}
