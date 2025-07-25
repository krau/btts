package database

import "sync"

var (
	watchedChatsID   = make(map[int64]struct{})
	watchedChatsIDMu = &sync.RWMutex{}
	allChatIDs       = make([]int64, 0)
	allChatIDsMu     = &sync.RWMutex{}
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
