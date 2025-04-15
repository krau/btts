package database

import (
	"context"

	"github.com/charmbracelet/log"
	"github.com/ncruces/go-sqlite3/gormlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var db *gorm.DB

var WatchedChatsID = make(map[int64]struct{})

func Watching(chatID int64) bool {
	_, ok := WatchedChatsID[chatID]
	return ok
}

func InitDatabase(ctx context.Context) error {
	log.FromContext(ctx).Debug("Initializing database")
	openDb, err := gorm.Open(gormlite.Open("data/data.db"), &gorm.Config{
		PrepareStmt: true,
		Logger:      logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return err
	}
	db = openDb
	if err := db.AutoMigrate(&UserInfo{}, &IndexChat{}, &SubBot{}); err != nil {
		return err
	}
	chats, err := GetAllIndexChats(ctx)
	if err != nil {
		return err
	}
	for _, chat := range chats {
		if chat.Watching {
			WatchedChatsID[chat.ChatID] = struct{}{}
		}
	}
	return nil
}
