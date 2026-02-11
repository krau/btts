package api

import (
	"slices"

	"github.com/gofiber/fiber/v3"
)

const (
	ctxKeyAPIMaster = "api_master"
	ctxKeyAPIChats  = "api_key_chats"
)

func isMasterAPIKey(c fiber.Ctx) bool {
	if v := c.Locals(ctxKeyAPIMaster); v != nil {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

func getScopedChats(c fiber.Ctx) []int64 {
	if v := c.Locals(ctxKeyAPIChats); v != nil {
		if chats, ok := v.([]int64); ok {
			return chats
		}
	}
	return nil
}

// ensureChatAllowed 确保当前 API key 允许访问指定 chat
func ensureChatAllowed(c fiber.Ctx, chatID int64) error {
	if chatID == 0 || isMasterAPIKey(c) {
		return nil
	}
	allowed := getScopedChats(c)
	if len(allowed) == 0 {
		return &fiber.Error{Code: fiber.StatusForbidden, Message: "Chat not allowed for this API key"}
	}
	if slices.Contains(allowed, chatID) {
		return nil
	}
	return &fiber.Error{Code: fiber.StatusForbidden, Message: "Chat not allowed for this API key"}
}

// filterAllowedChats 过滤出当前 API key 允许访问的聊天列表
func filterAllowedChats(c fiber.Ctx, chatIDs []int64) ([]int64, error) {
	if isMasterAPIKey(c) {
		return chatIDs, nil
	}
	allowed := getScopedChats(c)
	if len(allowed) == 0 {
		return nil, &fiber.Error{Code: fiber.StatusForbidden, Message: "No chats allowed for this API key"}
	}
	if len(chatIDs) == 0 {
		// 未显式指定时，默认为当前 key 的作用域
		return allowed, nil
	}
	res := make([]int64, 0, len(chatIDs))
	for _, id := range chatIDs {
		if slices.Contains(allowed, id) {
			res = append(res, id)
		}
	}
	if len(res) == 0 {
		return nil, &fiber.Error{Code: fiber.StatusForbidden, Message: "No requested chats allowed for this API key"}
	}
	return res, nil
}
