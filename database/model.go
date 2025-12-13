package database

import (
	"context"
	"slices"

	"github.com/charmbracelet/log"
	"gorm.io/gorm"
)

type UserInfo struct {
	ChatID    int64 `gorm:"primaryKey"`
	Username  string
	FirstName string
	LastName  string

	IndexChats []IndexChat `gorm:"many2many:index_chat_members;constraint:OnDelete:CASCADE;joinForeignKey:UserChatID;joinReferences:IndexChatID" json:"index_chats"`
}

func (u *UserInfo) FullName() string {
	if u.FirstName == "" {
		return u.LastName
	}
	if u.LastName == "" {
		return u.FirstName
	}
	return u.FirstName + " " + u.LastName
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

	Members []UserInfo `gorm:"many2many:index_chat_members;constraint:OnDelete:CASCADE;joinForeignKey:IndexChatID;joinReferences:UserChatID" json:"members"`
}

type SubBot struct {
	BotID int64 `gorm:"primaryKey"`
	Token string
	// which chats this bot can search
	ChatIDs []int64 `gorm:"serializer:json;type:json"`
}

// ApiKey 表示 Web API 的子 API Key
// KeyHash 使用 sha256 的十六进制字符串，Chats 通过关联表描述可访问聊天
type ApiKey struct {
	ID      uint        `gorm:"primaryKey"`
	Name    string      `json:"name"`
	KeyHash string      `gorm:"uniqueIndex;size:64" json:"-"`
	Chats   []IndexChat `gorm:"many2many:api_key_chats;constraint:OnDelete:CASCADE;joinForeignKey:ApiKeyID;joinReferences:IndexChatID" json:"chats"`
}

// ChatIDs 返回当前 ApiKey 可访问的聊天 ID 列表
func (a *ApiKey) ChatIDs() []int64 {
	res := make([]int64, 0)
	if a == nil {
		return res
	}
	for _, ch := range a.Chats {
		res = append(res, ch.ChatID)
	}
	return res
}

// public + user joined chats
func (s *SubBot) UserCanSearchChats(ctx context.Context, userId int64) []int64 {
	logger := log.FromContext(ctx)
	chats := make([]int64, 0)
	for _, id := range s.ChatIDs {
		chat, err := GetIndexChat(ctx, id)
		if err != nil {
			logger.Errorf("UserCanSearchChats: failed to get index chat %d: %v", id, err)
			continue
		}
		if chat.Public {
			chats = append(chats, id)
			continue
		}
		isMember, err := IsMemberInIndexChat(ctx, id, userId)
		if err != nil {
			logger.Errorf("UserCanSearchChats: failed to check membership for chat %d user %d: %v", id, userId, err)
			continue
		}
		if isMember {
			chats = append(chats, id)
		}
	}
	return chats
}

func (ic *IndexChat) AfterSave(tx *gorm.DB) error {
	if ic.ChatID == 0 {
		log.FromContext(tx.Statement.Context).Warnf("AfterSave IndexChat: chat_id is 0")
		return nil
	}
	watchedChatsIDMu.Lock()
	defer watchedChatsIDMu.Unlock()
	if ic.Watching {
		watchedChatsID[ic.ChatID] = struct{}{}
	} else {
		delete(watchedChatsID, ic.ChatID)
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
	watchedChatsIDMu.Lock()
	defer watchedChatsIDMu.Unlock()
	delete(watchedChatsID, ic.ChatID)
	allChatIDsMu.Lock()
	defer allChatIDsMu.Unlock()
	if idx := slices.Index(allChatIDs, ic.ChatID); idx != -1 {
		allChatIDs = slices.Delete(allChatIDs, idx, idx+1)
	}
	return nil
}
