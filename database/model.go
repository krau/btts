package database

import (
	"slices"

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
	ChatID   int64  `gorm:"primaryKey" json:"chat_id"`
	Title    string `json:"title"`
	Username string `json:"username"`
	Type     int    `json:"type"`
	Watching bool   `gorm:"default:true" json:"watching"`
	NoDelete bool   `json:"no_delete"`
	Public   bool   `gorm:"default:false" json:"public"`
}

func (ic *IndexChat) AfterSave(tx *gorm.DB) error {
	if ic.ChatID == 0 {
		log.FromContext(tx.Statement.Context).Warnf("AfterSave IndexChat: chat_id is 0")
		return nil
	}
	if ic.Watching {
		WatchedChatsID[ic.ChatID] = struct{}{}
	} else {
		delete(WatchedChatsID, ic.ChatID)
	}
	allChatIDsMu.Lock()
	defer allChatIDsMu.Unlock()
	if !slices.Contains(allChatIDs, ic.ChatID) {
		allChatIDs = append(allChatIDs, ic.ChatID)
	}
	return nil
}

func (ic *IndexChat) BeforeDelete(tx *gorm.DB) error {
	if ic.ChatID == 0 {
		log.FromContext(tx.Statement.Context).Warnf("BeforeDelete IndexChat: chat_id is 0")
		return nil
	}
	delete(WatchedChatsID, ic.ChatID)
	allChatIDsMu.Lock()
	defer allChatIDsMu.Unlock()
	if idx := slices.Index(allChatIDs, ic.ChatID); idx != -1 {
		allChatIDs = slices.Delete(allChatIDs, idx, idx+1)
	}
	return nil
}

type SubBot struct {
	BotID int64 `gorm:"primaryKey"`
	Token string
	// which chats this bot can search
	ChatIDs []int64 `gorm:"serializer:json;type:json"`
}
