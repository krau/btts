package database

import (
	"github.com/charmbracelet/log"
	"gorm.io/gorm"
)

type UserInfo struct {
	ChatID    int64 `gorm:"primaryKey"`
	Username  string
	FirstName string
	LastName  string
}

type ChatType int

const (
	ChatTypePrivate ChatType = iota
	ChatTypeChannel
)

type IndexChat struct {
	ChatID   int64 `gorm:"primaryKey"`
	Title    string
	Username string
	Type     int
	Watching bool `gorm:"default:true"`
	NoDelete bool
	Public   bool `gorm:"default:false"`
}

func (ic *IndexChat) AfterSave(tx *gorm.DB) error {
	log.FromContext(tx.Statement.Context).Debug("AfterSave IndexChat", "chat_id", ic.ChatID, "watching", ic.Watching)
	if ic.Watching {
		WatchedChatsID[ic.ChatID] = struct{}{}
	} else {
		delete(WatchedChatsID, ic.ChatID)
	}
	return nil
}

func (ic *IndexChat) BeforeDelete(tx *gorm.DB) error {
	if ic.ChatID == 0 {
		log.FromContext(tx.Statement.Context).Warnf("BeforeDelete IndexChat: chat_id is 0")
		return nil
	}
	log.FromContext(tx.Statement.Context).Debug("BeforeDelete IndexChat", "chat_id", ic.ChatID)
	delete(WatchedChatsID, ic.ChatID)
	return nil
}
