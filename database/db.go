package database

import (
	"context"

	"github.com/charmbracelet/log"
	"github.com/ncruces/go-sqlite3/gormlite"
	"gorm.io/gorm"
)

var db *gorm.DB

func InitDatabase(ctx context.Context) error {
	log.FromContext(ctx).Debug("Initializing database")
	openDb, err := gorm.Open(gormlite.Open("data/data.db"), &gorm.Config{
		PrepareStmt: true,
	})
	if err != nil {
		return err
	}
	db = openDb
	if err := db.AutoMigrate(&UserInfo{}, &IndexChat{}); err != nil {
		return err
	}
	return nil
}
