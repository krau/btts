package database

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/telegram/updates"
	"gorm.io/gorm"
)

func setupInitializedUpdatesDB(t *testing.T) *sql.DB {
	t.Helper()
	oldDB, oldWatched, oldAll := db, watchedChatsID, allChatIDs
	db = nil
	watchedChatsID = make(map[int64]struct{})
	allChatIDs = nil
	t.Cleanup(func() { db, watchedChatsID, allChatIDs = oldDB, oldWatched, oldAll })
	t.Chdir(t.TempDir())
	if err := os.Mkdir("data", 0700); err != nil {
		t.Fatal(err)
	}
	if err := InitDatabase(context.Background()); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return sqlDB
}

func reopenInitializedUpdatesDB(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	db = nil
	if err := InitDatabase(context.Background()); err != nil {
		t.Fatal(err)
	}
	reopened, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
}

func pauseAccountRead(t *testing.T, ctx context.Context) (<-chan struct{}, func()) {
	t.Helper()
	read := make(chan struct{})
	release := make(chan struct{})
	var pauseOnce, releaseOnce sync.Once
	resume := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(resume)
	if err := db.Callback().Query().After("gorm:query").Register("test:pause_account_read", func(tx *gorm.DB) {
		if tx.Statement.Schema == nil || tx.Statement.Schema.Name != "UpdatesState" {
			return
		}
		pauseOnce.Do(func() {
			close(read)
			select {
			case <-release:
			case <-ctx.Done():
				tx.AddError(ctx.Err())
			}
		})
	}); err != nil {
		t.Fatal(err)
	}
	return read, resume
}

func TestInitDatabaseSerializesRecoveryCursorTransactions(t *testing.T) {
	sqlDB := setupInitializedUpdatesDB(t)
	storage := NewUpdatesStorage()
	initial := updates.State{Pts: 10, Qts: 2, Date: 30, Seq: 4}
	advanced := updates.State{Pts: 20, Qts: 3, Date: 40, Seq: 5}
	if err := storage.SetState(context.Background(), 100, initial); err != nil {
		t.Fatal(err)
	}
	if err := storage.SetChannelPts(context.Background(), 100, 10, 5); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	read, resumeAccount := pauseAccountRead(t, ctx)
	written := make(chan struct{})
	releaseWriter := make(chan struct{})
	var releaseOnce sync.Once
	resumeWriter := func() { releaseOnce.Do(func() { close(releaseWriter) }) }
	t.Cleanup(resumeWriter)
	if err := db.Callback().Create().After("gorm:create").Before("gorm:commit_or_rollback_transaction").Register("test:pause_channel_commit", func(tx *gorm.DB) {
		if tx.Statement.Schema == nil || tx.Statement.Schema.Name != "updatesChannelState" || tx.Error != nil {
			return
		}
		close(written)
		select {
		case <-releaseWriter:
		case <-ctx.Done():
			tx.AddError(ctx.Err())
		}
	}); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	t.Cleanup(func() {
		resumeAccount()
		resumeWriter()
		cancel()
		workers.Wait()
	})
	accountDone := make(chan error, 1)
	workers.Add(1)
	go func() {
		defer workers.Done()
		accountDone <- storage.SetState(ctx, 100, advanced)
	}()
	select {
	case <-read:
	case err := <-accountDone:
		t.Fatalf("account transaction ended before reading: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	waits := sqlDB.Stats().WaitCount
	channelDone := make(chan error, 1)
	workers.Add(1)
	go func() {
		defer workers.Done()
		channelDone <- storage.SetChannelPts(ctx, 100, 10, 9)
	}()
	// With multiple connections the channel write owns SQLite's reserved lock;
	// with one connection it queues before entering its transaction.
barrier:
	for {
		select {
		case <-written:
			break barrier
		case err := <-channelDone:
			t.Fatalf("channel transaction ended before serialization barrier: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		default:
			if sqlDB.Stats().WaitCount > waits {
				break barrier
			}
			runtime.Gosched()
		}
	}
	resumeAccount()
	select {
	case err := <-accountDone:
		if err != nil {
			t.Fatalf("read-to-write account transaction failed beside channel write: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	resumeWriter()
	select {
	case err := <-channelDone:
		if err != nil {
			t.Fatalf("queued channel cursor failed: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	workers.Wait()
	reopenInitializedUpdatesDB(t, sqlDB)
	requireAccountState(t, storage, 100, advanced, true)
	requireChannelPts(t, storage, 100, 10, 9, true)
}

func TestInitDatabaseQueuedRecoveryWritesCanBeCanceled(t *testing.T) {
	sqlDB := setupInitializedUpdatesDB(t)
	storage := NewUpdatesStorage()
	initial := updates.State{Pts: 10, Date: 30}
	advanced := updates.State{Pts: 20, Date: 40}
	if err := storage.SetState(context.Background(), 100, initial); err != nil {
		t.Fatal(err)
	}
	if err := storage.SetChannelPts(context.Background(), 100, 10, 5); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	read, resumeAccount := pauseAccountRead(t, ctx)
	queuedCtx, cancelQueued := context.WithCancel(ctx)
	t.Cleanup(cancelQueued)
	var workers sync.WaitGroup
	t.Cleanup(func() {
		resumeAccount()
		cancel()
		workers.Wait()
	})
	accountDone := make(chan error, 1)
	workers.Add(1)
	go func() {
		defer workers.Done()
		accountDone <- storage.SetState(ctx, 100, advanced)
	}()
	select {
	case <-read:
	case err := <-accountDone:
		t.Fatalf("account transaction ended before reading: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	waits := sqlDB.Stats().WaitCount
	queuedAccountDone, queuedChannelDone := make(chan error, 1), make(chan error, 1)
	workers.Add(2)
	go func() {
		defer workers.Done()
		queuedAccountDone <- storage.SetState(queuedCtx, 200, updates.State{Pts: 99})
	}()
	go func() {
		defer workers.Done()
		queuedChannelDone <- storage.SetChannelPts(queuedCtx, 100, 10, 99)
	}()
	// Observe both operations actually queuing before canceling their context.
	for sqlDB.Stats().WaitCount < waits+2 {
		select {
		case err := <-queuedAccountDone:
			t.Fatalf("queued account write completed while another transaction held the connection: %v", err)
		case err := <-queuedChannelDone:
			t.Fatalf("queued channel write completed while another transaction held the connection: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		default:
			runtime.Gosched()
		}
	}
	cancelQueued()
	for _, done := range []<-chan error{queuedAccountDone, queuedChannelDone} {
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("queued write ignored cancellation: %v", err)
			}
		case <-ctx.Done():
			t.Fatal("canceled write remained blocked behind account transaction")
		}
	}
	resumeAccount()
	select {
	case err := <-accountDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	workers.Wait()
	reopenInitializedUpdatesDB(t, sqlDB)
	requireAccountState(t, storage, 100, advanced, true)
	requireAccountState(t, storage, 200, updates.State{}, false)
	requireChannelPts(t, storage, 100, 10, 5, true)
}

func TestInitDatabaseChannelIterationCanPersistCursors(t *testing.T) {
	setupInitializedUpdatesDB(t)
	storage := NewUpdatesStorage()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, channelID := range []int64{10, 20} {
		if err := storage.SetChannelPts(ctx, 100, channelID, 5); err != nil {
			t.Fatal(err)
		}
	}
	if err := storage.ForEachChannels(ctx, 100, func(ctx context.Context, channelID int64, pts int) error {
		return storage.SetChannelPts(ctx, 100, channelID, pts+1)
	}); err != nil {
		t.Fatalf("channel callback could not acquire the connection: %v", err)
	}
	requireChannelPts(t, storage, 100, 10, 6, true)
	requireChannelPts(t, storage, 100, 20, 6, true)
}
