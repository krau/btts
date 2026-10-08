package database

import (
	"context"

	"github.com/charmbracelet/log"
	"github.com/ncruces/go-sqlite3/gormlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var db *gorm.DB

func InitDatabase(ctx context.Context) error {
	if db != nil {
		return nil
	}
	log.FromContext(ctx).Debug("Initializing database")
	openDb, err := gorm.Open(gormlite.Open("data/data.db"), &gorm.Config{
		PrepareStmt: true,
		Logger:      logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return err
	}
	sqlDB, err := openDb.DB()
	if err != nil {
		return err
	}
	// Serialize read-to-write transactions so SQLite cannot reject their lock upgrade.
	sqlDB.SetMaxOpenConns(1)
	db = openDb
	if err := db.WithContext(ctx).AutoMigrate(&UserInfo{}, &IndexChat{}, &SubBot{}, &ApiKey{}, &UpdatesState{}, &updatesChannelState{}); err != nil {
		return err
	}
	chats, err := GetAllIndexChats(ctx)
	if err != nil {
		return err
	}
	for _, chat := range chats {
		if chat.Watching {
			watchedChatsID[chat.ChatID] = struct{}{}
		}
		allChatIDs = append(allChatIDs, chat.ChatID)
	}
	return nil
}
