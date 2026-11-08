package database

import (
	"slices"
	"sync"
)

var (
	watchedChatsID      = make(map[int64]struct{})
	watchedChatsIDMu    = &sync.RWMutex{}
	allChatIDs          = make([]int64, 0)
	allChatIDsMu        = &sync.RWMutex{}
	enrichDisabledChats = make(map[int64]struct{})
	enrichDisabledMu    = &sync.RWMutex{}
)

func Watching(chatID int64) bool {
	watchedChatsIDMu.RLock()
	defer watchedChatsIDMu.RUnlock()
	_, ok := watchedChatsID[chatID]
	return ok
}

func AllChatIDs() []int64 {
	allChatIDsMu.RLock()
	defer allChatIDsMu.RUnlock()
	copied := make([]int64, len(allChatIDs))
	copy(copied, allChatIDs)
	return copied
}

func Indexed(chatID int64) bool {
	allChatIDsMu.RLock()
	defer allChatIDsMu.RUnlock()
	return slices.Contains(allChatIDs, chatID)
}

// IsEnrichDisabled reports whether media enrichment is disabled for a chat.
func IsEnrichDisabled(chatID int64) bool {
	enrichDisabledMu.RLock()
	defer enrichDisabledMu.RUnlock()
	_, ok := enrichDisabledChats[chatID]
	return ok
}
