package database

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/gotd/td/telegram/updates"
	"github.com/ncruces/go-sqlite3/gormlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func openUpdatesTestDB(t *testing.T, path string) *gorm.DB {
	t.Helper()
	conn, err := gorm.Open(gormlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := conn.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return conn
}

func setupUpdatesTestDB(t *testing.T) string {
	t.Helper()
	oldDB, oldWatched, oldAll := db, watchedChatsID, allChatIDs
	db = nil
	watchedChatsID = make(map[int64]struct{})
	allChatIDs = nil
	t.Cleanup(func() { db, watchedChatsID, allChatIDs = oldDB, oldWatched, oldAll })
	path := filepath.Join(t.TempDir(), "updates.db")
	db = openUpdatesTestDB(t, path)
	if err := db.AutoMigrate(&UserInfo{}, &IndexChat{}, &UpdatesState{}, &updatesChannelState{}); err != nil {
		t.Fatal(err)
	}
	return path
}

func requireChannelPts(t *testing.T, storage *UpdatesStorage, userID, channelID int64, pts int, found bool) {
	t.Helper()
	got, ok, err := storage.GetChannelPts(context.Background(), userID, channelID)
	if err != nil || got != pts || ok != found {
		t.Fatalf("channel (%d, %d): got (%d, %v, %v), want (%d, %v, nil)", userID, channelID, got, ok, err, pts, found)
	}
}

func collectChannels(t *testing.T, storage *UpdatesStorage, userID int64) map[int64]int {
	t.Helper()
	channels := make(map[int64]int)
	if err := storage.ForEachChannels(context.Background(), userID, func(_ context.Context, id int64, pts int) error {
		channels[id] = pts
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return channels
}

func seedLegacyChannel(t *testing.T, channelID int64, pts int) {
	t.Helper()
	if !db.Migrator().HasColumn(&IndexChat{}, "pts") {
		if err := db.Exec("ALTER TABLE index_chats ADD COLUMN pts INTEGER DEFAULT 0").Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := UpsertIndexChat(context.Background(), &IndexChat{ChatID: channelID, Watching: true}); err != nil {
		t.Fatal(err)
	}
	if err := db.Table("index_chats").Where("chat_id = ?", channelID).Update("pts", pts).Error; err != nil {
		t.Fatal(err)
	}
}

func requireAccountState(t *testing.T, storage *UpdatesStorage, userID int64, want updates.State, found bool) {
	t.Helper()
	got, ok, err := storage.GetState(context.Background(), userID)
	if err != nil || got != want || ok != found {
		t.Fatalf("account %d: got (%+v, %v, %v), want (%+v, %v, nil)", userID, got, ok, err, want, found)
	}
}

func TestUpdatesStorageDeletedChatRetainsCursorAfterRestart(t *testing.T) {
	path := setupUpdatesTestDB(t)
	ctx := context.Background()
	storage := NewUpdatesStorage()
	if err := UpsertIndexChat(ctx, &IndexChat{ChatID: 10, Watching: true}); err != nil {
		t.Fatal(err)
	}
	if err := storage.SetChannelPts(ctx, 100, 10, 20); err != nil {
		t.Fatal(err)
	}
	if err := DeleteIndexChat(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if err := storage.SetChannelPts(ctx, 100, 10, 25); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	db = openUpdatesTestDB(t, path)
	storage = NewUpdatesStorage()
	requireChannelPts(t, storage, 100, 10, 25, true)
	if got := collectChannels(t, storage, 100); !reflect.DeepEqual(got, map[int64]int{10: 25}) {
		t.Fatalf("restart channels: %v", got)
	}
	chats, err := GetAllIndexChats(ctx)
	if err != nil || len(chats) != 0 || Indexed(10) || Watching(10) {
		t.Fatalf("deleted chat leaked into indexing: chats=%v err=%v", chats, err)
	}
}

func TestUpdatesStorageAccountIsolationAndIndexingPolicy(t *testing.T) {
	setupUpdatesTestDB(t)
	ctx := context.Background()
	storage := NewUpdatesStorage()
	if err := storage.SetChannelPts(ctx, 100, 10, 5); err != nil {
		t.Fatal(err)
	}
	if err := storage.SetChannelPts(ctx, 200, 10, 0); err != nil {
		t.Fatal(err)
	}
	requireChannelPts(t, storage, 100, 10, 5, true)
	requireChannelPts(t, storage, 200, 10, 0, true)
	requireChannelPts(t, storage, 300, 10, 0, false)
	if got := collectChannels(t, storage, 200); !reflect.DeepEqual(got, map[int64]int{10: 0}) {
		t.Fatalf("zero channel cursor was lost: %v", got)
	}
	if chats, err := GetAllIndexChats(ctx); err != nil || len(chats) != 0 || len(AllChatIDs()) != 0 {
		t.Fatalf("non-index cursor created a chat: chats=%v err=%v", chats, err)
	}
	if err := UpsertIndexChat(ctx, &IndexChat{ChatID: 10, Watching: true}); err != nil {
		t.Fatal(err)
	}
	chat, err := GetIndexChat(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.SetChannelPts(ctx, 100, 10, 9); err != nil {
		t.Fatal(err)
	}
	// A metadata save read before the manager advanced must not reset its cursor.
	chat.Title, chat.Watching = "renamed", false
	if err := UpsertIndexChat(ctx, chat); err != nil {
		t.Fatal(err)
	}
	if Watching(10) {
		t.Fatal("unwatch did not change indexing policy")
	}
	requireChannelPts(t, storage, 100, 10, 9, true)
	if got := collectChannels(t, storage, 100); !reflect.DeepEqual(got, map[int64]int{10: 9}) {
		t.Fatalf("unwatched account channel must remain recoverable for non-index consumers: %v", got)
	}
}

func TestUpdatesStorageLegacyMigration(t *testing.T) {
	legacy := updates.State{Pts: 5, Qts: 2, Date: 10, Seq: 3}
	current := updates.State{Pts: 30, Qts: 7, Date: 40, Seq: 9}
	for _, tc := range []struct {
		name    string
		current *updates.State
		want    updates.State
	}{
		{name: "missing account", want: legacy},
		{name: "zero placeholder", current: &updates.State{}, want: legacy},
		{name: "meaningful account wins", current: &current, want: current},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupUpdatesTestDB(t)
			if err := db.Create(&UpdatesState{ID: 1, Pts: legacy.Pts, Qts: legacy.Qts, Date: legacy.Date, Seq: legacy.Seq}).Error; err != nil {
				t.Fatal(err)
			}
			if tc.current != nil {
				if err := db.Create(&UpdatesState{ID: 100, Pts: tc.current.Pts, Qts: tc.current.Qts, Date: tc.current.Date, Seq: tc.current.Seq}).Error; err != nil {
					t.Fatal(err)
				}
			}
			seedLegacyChannel(t, 10, 5)
			seedLegacyChannel(t, 20, 6)
			seedLegacyChannel(t, 30, 7)
			storage := NewUpdatesStorage()
			if err := storage.SetChannelPts(context.Background(), 100, 20, 40); err != nil {
				t.Fatal(err)
			}
			if err := storage.SetChannelPts(context.Background(), 100, 30, 0); err != nil {
				t.Fatal(err)
			}
			requireAccountState(t, storage, 100, tc.want, true)
			requireChannelPts(t, storage, 100, 10, 5, true)
			requireChannelPts(t, storage, 100, 20, 40, true)
			requireChannelPts(t, storage, 100, 30, 0, true)
			var count int64
			if err := db.Model(&UpdatesState{}).Where("id = 1").Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("legacy account residue: count=%d err=%v", count, err)
			}
			var legacyPts int
			if err := db.Table("index_chats").Where("chat_id = 10").Select("pts").Scan(&legacyPts).Error; err != nil || legacyPts != 0 {
				t.Fatalf("legacy channel column not consumed: pts=%d err=%v", legacyPts, err)
			}
			requireAccountState(t, storage, 100, tc.want, true)
			requireAccountState(t, storage, 200, updates.State{}, false)
			requireChannelPts(t, storage, 200, 10, 0, false)
			if got := collectChannels(t, storage, 200); len(got) != 0 {
				t.Fatalf("legacy cursors migrated to a second account: %v", got)
			}
		})
	}
}

func TestUpdatesStorageSetStateMigratesLegacyChannels(t *testing.T) {
	setupUpdatesTestDB(t)
	seedLegacyChannel(t, 10, 5)
	storage := NewUpdatesStorage()
	state := updates.State{Pts: 30, Date: 40}
	if err := storage.SetState(context.Background(), 100, state); err != nil {
		t.Fatal(err)
	}
	requireAccountState(t, storage, 100, state, true)
	requireChannelPts(t, storage, 100, 10, 5, true)
}

func TestUpdatesStorageMigrationRollsBackOnError(t *testing.T) {
	setupUpdatesTestDB(t)
	seedLegacyChannel(t, 10, 5)
	if err := db.Create(&UpdatesState{ID: 1, Pts: 7}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TRIGGER reject_channel_migration BEFORE INSERT ON updates_channel_states
		BEGIN SELECT RAISE(ABORT, 'migration rejected'); END`).Error; err != nil {
		t.Fatal(err)
	}
	storage := NewUpdatesStorage()
	if _, _, err := storage.GetState(context.Background(), 100); err == nil {
		t.Fatal("migration error was suppressed")
	}
	var legacy UpdatesState
	if err := db.First(&legacy, 1).Error; err != nil || legacy.Pts != 7 {
		t.Fatalf("legacy account removed on failed migration: state=%+v err=%v", legacy, err)
	}
	var count int64
	if err := db.Model(&UpdatesState{}).Where("id = 100").Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("partial account migration: count=%d err=%v", count, err)
	}
	var pts int
	if err := db.Table("index_chats").Select("pts").Where("chat_id = 10").Scan(&pts).Error; err != nil || pts != 5 {
		t.Fatalf("legacy cursor consumed on failed migration: pts=%d err=%v", pts, err)
	}
	if err := db.Exec("DROP TRIGGER reject_channel_migration").Error; err != nil {
		t.Fatal(err)
	}
	requireAccountState(t, storage, 100, updates.State{Pts: 7}, true)
	requireChannelPts(t, storage, 100, 10, 5, true)
}

func TestUpdatesStorageErrorsAndIncrementalState(t *testing.T) {
	setupUpdatesTestDB(t)
	storage := NewUpdatesStorage()
	ctx := context.Background()
	setters := []func(context.Context, int64) error{
		func(ctx context.Context, id int64) error { return storage.SetPts(ctx, id, 8) },
		func(ctx context.Context, id int64) error { return storage.SetQts(ctx, id, 9) },
		func(ctx context.Context, id int64) error { return storage.SetDate(ctx, id, 10) },
		func(ctx context.Context, id int64) error { return storage.SetSeq(ctx, id, 11) },
		func(ctx context.Context, id int64) error { return storage.SetDateSeq(ctx, id, 12, 13) },
	}
	for _, set := range setters {
		if err := set(ctx, 100); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("missing account setter: %v", err)
		}
	}
	if err := storage.SetState(ctx, 100, updates.State{Pts: 1}); err != nil {
		t.Fatal(err)
	}
	for _, set := range setters {
		if err := set(ctx, 100); err != nil {
			t.Fatal(err)
		}
	}
	requireAccountState(t, storage, 100, updates.State{Pts: 8, Qts: 9, Date: 12, Seq: 13}, true)
	if err := storage.SetChannelPts(ctx, 100, 10, 5); err != nil {
		t.Fatal(err)
	}
	callbackErr := errors.New("callback rejected")
	if err := storage.ForEachChannels(ctx, 100, func(context.Context, int64, int) error { return callbackErr }); !errors.Is(err, callbackErr) {
		t.Fatalf("callback error was lost: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := storage.GetState(canceled, 100); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetState ignored cancellation: %v", err)
	}
	if err := storage.SetState(canceled, 100, updates.State{Pts: 99}); !errors.Is(err, context.Canceled) {
		t.Fatalf("SetState ignored cancellation: %v", err)
	}
	if _, _, err := storage.GetChannelPts(canceled, 100, 10); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetChannelPts ignored cancellation: %v", err)
	}
	if err := storage.SetChannelPts(canceled, 100, 10, 99); !errors.Is(err, context.Canceled) {
		t.Fatalf("SetChannelPts ignored cancellation: %v", err)
	}
	if err := storage.ForEachChannels(canceled, 100, func(context.Context, int64, int) error {
		t.Fatal("callback called after cancellation")
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("ForEachChannels ignored cancellation: %v", err)
	}
	requireAccountState(t, storage, 100, updates.State{Pts: 8, Qts: 9, Date: 12, Seq: 13}, true)
	requireChannelPts(t, storage, 100, 10, 5, true)
}

func TestUpdatesStorageInitSchemaKeepsLegacyColumn(t *testing.T) {
	oldDB, oldWatched, oldAll := db, watchedChatsID, allChatIDs
	db = nil
	watchedChatsID = make(map[int64]struct{})
	allChatIDs = nil
	t.Cleanup(func() { db, watchedChatsID, allChatIDs = oldDB, oldWatched, oldAll })
	t.Chdir(t.TempDir())
	if err := os.Mkdir("data", 0700); err != nil {
		t.Fatal(err)
	}
	db = openUpdatesTestDB(t, "data/data.db")
	if err := db.AutoMigrate(&UserInfo{}, &IndexChat{}, &SubBot{}, &ApiKey{}, &UpdatesState{}); err != nil {
		t.Fatal(err)
	}
	seedLegacyChannel(t, 10, 5)
	if err := db.Create(&UpdatesState{ID: 1, Pts: 7}).Error; err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	db = nil
	if err := InitDatabase(context.Background()); err != nil {
		t.Fatal(err)
	}
	sqlDB, err = db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if !db.Migrator().HasColumn(&IndexChat{}, "pts") {
		t.Fatal("schema migration dropped the legacy SQLite column")
	}
	storage := NewUpdatesStorage()
	requireAccountState(t, storage, 100, updates.State{Pts: 7}, true)
	requireChannelPts(t, storage, 100, 10, 5, true)
	if err := storage.SetChannelPts(context.Background(), 100, 20, 0); err != nil {
		t.Fatal(err)
	}
	requireChannelPts(t, storage, 100, 20, 0, true)
}
