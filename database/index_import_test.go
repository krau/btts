package database

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestImportStateMachine(t *testing.T) {
	setupUpdatesTestDB(t)
	ctx := context.Background()

	imp, resumed, err := StartImport(ctx, 42, "raw", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if resumed {
		t.Fatal("fresh import reported as resumed")
	}
	if _, _, err := StartImport(ctx, 42, "raw", time.Hour); !errors.Is(err, ErrImportInProgress) {
		t.Fatalf("concurrent claim: got %v, want ErrImportInProgress", err)
	}
	if err := UpdateImportProgress(ctx, 42, imp.Generation, 3, 7, false); err != nil {
		t.Fatal(err)
	}
	if err := StopImport(ctx, 42, imp.Generation); err != nil {
		t.Fatal(err)
	}

	resumedImp, resumed, err := StartImport(ctx, 42, "raw", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !resumed {
		t.Fatal("stopped import was not resumed")
	}
	if resumedImp.Generation == imp.Generation {
		t.Fatal("generation was not renewed")
	}
	if resumedImp.RangeIndex != 3 || resumedImp.Cursor != 7 {
		t.Fatalf("cursor not preserved: %+v", resumedImp)
	}
	if err := UpdateImportProgress(ctx, 42, imp.Generation, 0, 9, false); !errors.Is(err, ErrImportSuperseded) {
		t.Fatalf("stale generation write: got %v, want ErrImportSuperseded", err)
	}
	if err := StopImport(ctx, 42, resumedImp.Generation); err != nil {
		t.Fatal(err)
	}
	if _, _, err := StartImport(ctx, 42, "takeout", time.Hour); !errors.Is(err, ErrImportModeMismatch) {
		t.Fatalf("mode mismatch: got %v, want ErrImportModeMismatch", err)
	}
	if err := UpdateImportProgress(ctx, 42, resumedImp.Generation, 3, 7, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := StartImport(ctx, 42, "raw", time.Hour); !errors.Is(err, ErrImportCompleted) {
		t.Fatalf("completed import: got %v, want ErrImportCompleted", err)
	}
}

func TestImportLeaseResetAndChatDeletion(t *testing.T) {
	setupUpdatesTestDB(t)
	ctx := context.Background()

	imp, _, err := StartImport(ctx, 7, "raw", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := StartImport(ctx, 7, "raw", time.Hour); !errors.Is(err, ErrImportInProgress) {
		t.Fatalf("got %v, want ErrImportInProgress", err)
	}
	if err := ResetIncompleteImports(ctx); err != nil {
		t.Fatal(err)
	}
	resumedImp, resumed, err := StartImport(ctx, 7, "raw", time.Hour)
	if err != nil || !resumed {
		t.Fatalf("after reset: resumed=%v err=%v", resumed, err)
	}
	if resumedImp.Generation == imp.Generation {
		t.Fatal("generation was not renewed after reset")
	}

	if err := DeleteIndexChat(ctx, 7); err != nil {
		t.Fatal(err)
	}
	fresh, resumed, err := StartImport(ctx, 7, "raw", time.Hour)
	if err != nil || resumed {
		t.Fatalf("after delete: resumed=%v err=%v", resumed, err)
	}
	if fresh.Cursor != 0 || fresh.RangeIndex != 0 {
		t.Fatalf("new import reused old progress: %+v", fresh)
	}
}

func TestLegacyIndexedChatIsCompleted(t *testing.T) {
	setupUpdatesTestDB(t)
	ctx := context.Background()
	if err := UpsertIndexChat(ctx, &IndexChat{ChatID: 99, Watching: true}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := StartImport(ctx, 99, "raw", time.Hour); !errors.Is(err, ErrImportCompleted) {
		t.Fatalf("legacy chat: got %v, want ErrImportCompleted", err)
	}
}
