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
	ChatTypeChannel          // 频道和超级群
	ChatTypeGroup            // 普通群组
)

type IndexChat struct {
	ChatID   int64  `gorm:"primaryKey" json:"chat_id"`
	Title    string `json:"title"`
	Username string `json:"username"`
	Type     int    `json:"type"`
	Watching bool   `gorm:"default:true" json:"watching"`
	NoDelete bool   `json:"no_delete"`
	NoOcr    bool   `json:"no_ocr"`
	NoEnrich bool   `json:"no_enrich"`
	Public   bool   `gorm:"default:false" json:"public"`

	Members []UserInfo `gorm:"many2many:index_chat_members;constraint:OnDelete:CASCADE;joinForeignKey:IndexChatID;joinReferences:UserChatID" json:"members"`
}

// IndexImport tracks the resumable cold-history import of one chat. It is a
// separate row so chat metadata updates can never roll back import progress.
type IndexImport struct {
	ChatID int64 `gorm:"primaryKey"`
	// Generation is unique per import run; progress updates are scoped to it.
	Generation int64
	Mode       string
	RangeIndex int
	Cursor     int
	// LeaseAt is the unix timestamp of the last progress update, 0 when stopped.
	LeaseAt   int64
	Completed bool
}

// EnrichJob is a durable media-enrichment task. Only media-rich messages are
// queued; plain text messages never enter this table.
type EnrichJob struct {
	ID        uint  `gorm:"primaryKey;index:idx_enrich_claim,priority:4"`
	ChatID    int64 `gorm:"uniqueIndex:idx_enrich_chat_msg"`
	MessageID int   `gorm:"uniqueIndex:idx_enrich_chat_msg"`
	UserID    int64
	MsgType   int
	Timestamp int64
	MIME      string
	Name      string
	Version   string
	Priority  int    `gorm:"index:idx_enrich_claim,priority:2"`
	Status    string `gorm:"index:idx_enrich_claim,priority:1"`
	Attempts  int
	NextRun   int64 `gorm:"index:idx_enrich_claim,priority:3"` // unix seconds
	LastError string
	CreatedAt int64
	UpdatedAt int64
}

// UpdatesState 存储 Telegram updates 的状态信息，用于断线重连时获取错过的消息
type UpdatesState struct {
	ID   uint `gorm:"primaryKey"`
	Pts  int  `json:"pts"`  // Common message box sequence (private chats, basic groups)
	Qts  int  `json:"qts"`  // Secondary event sequence (secret chats, certain bot events)
	Date int  `json:"date"` // Unix timestamp
	Seq  int  `json:"seq"`  // Updates sequence number
}

// Channel cursors belong to the Telegram account, not the bot's indexing policy.
type updatesChannelState struct {
	UserID    int64 `gorm:"primaryKey;autoIncrement:false"`
	ChannelID int64 `gorm:"primaryKey;autoIncrement:false"`
	Pts       int
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
	updateIndexChatCaches(ic)
	return nil
}

func updateIndexChatCaches(ic *IndexChat) {
	if ic.ChatID == 0 {
		return
	}
	enrichDisabledMu.Lock()
	if ic.NoEnrich {
		enrichDisabledChats[ic.ChatID] = struct{}{}
	} else {
		delete(enrichDisabledChats, ic.ChatID)
	}
	enrichDisabledMu.Unlock()
	watchedChatsIDMu.Lock()
	if ic.Watching {
		watchedChatsID[ic.ChatID] = struct{}{}
	} else {
		delete(watchedChatsID, ic.ChatID)
	}
	watchedChatsIDMu.Unlock()
	allChatIDsMu.Lock()
	if !slices.Contains(allChatIDs, ic.ChatID) {
		allChatIDs = append(allChatIDs, ic.ChatID)
	}
	allChatIDsMu.Unlock()
}

func (ic *IndexChat) BeforeDelete(tx *gorm.DB) error {
	if ic.ChatID == 0 {
		log.FromContext(tx.Statement.Context).Warnf("BeforeDelete IndexChat: chat_id is 0")
		return nil
	}
	removeIndexChatCaches(ic.ChatID)
	return nil
}

func removeIndexChatCaches(chatID int64) {
	if chatID == 0 {
		return
	}
	enrichDisabledMu.Lock()
	delete(enrichDisabledChats, chatID)
	enrichDisabledMu.Unlock()
	watchedChatsIDMu.Lock()
	delete(watchedChatsID, chatID)
	watchedChatsIDMu.Unlock()
	allChatIDsMu.Lock()
	if idx := slices.Index(allChatIDs, chatID); idx != -1 {
		allChatIDs = slices.Delete(allChatIDs, idx, idx+1)
	}
	allChatIDsMu.Unlock()
}
