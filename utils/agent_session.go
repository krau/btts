package utils

import (
	"fmt"

	"github.com/krau/btts/utils/cache"
)

func agentConversationKey(botID, chatID int64, messageID int) string {
	return fmt.Sprintf("agent-session:%d:%d:%d", botID, chatID, messageID)
}

// SetAgentConversation links a bot result message to an agent conversation.
// The link expires with the same TTL as a cached search query.
func SetAgentConversation(botID, chatID int64, messageID int, conversationID string) {
	_ = cache.Set(agentConversationKey(botID, chatID, messageID), conversationID, cache.DefaultTTL)
}

// GetAgentConversation returns the conversation linked to a bot result message.
func GetAgentConversation(botID, chatID int64, messageID int) (string, bool) {
	return cache.Get[string](agentConversationKey(botID, chatID, messageID))
}
