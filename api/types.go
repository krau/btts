package api

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/krau/btts/database"
	"github.com/krau/btts/types"
	"gorm.io/gorm"
)

// SearchOnChatByPostRequest 单聊天搜索请求
type SearchOnChatByPostRequest struct {
	Query  string   `json:"query" validate:"required" example:"search text"` // 搜索查询字符串
	Offset int64    `json:"offset" default:"0" example:"0"`                  // 偏移量，用于分页
	Limit  int64    `json:"limit" default:"10" example:"10"`                 // 限制数量，用于分页
	Users  []int64  `json:"users,omitempty" example:"123456,789012"`         // 用户ID过滤列表，可选
	Types  []string `json:"types,omitempty" example:"text,photo"`            // 消息类型过滤列表，可选值：text,photo,video,document,voice,audio,poll,story
}

// SearchOnMultiChatByPostRequest 多聊天搜索请求
type SearchOnMultiChatByPostRequest struct {
	ChatIDs []int64  `json:"chat_ids" example:"777000,114514"`                // 聊天ID列表，如果为空则搜索所有聊天
	Query   string   `json:"query" validate:"required" example:"search text"` // 搜索查询字符串
	Offset  int64    `json:"offset" default:"0" example:"0"`                  // 偏移量，用于分页
	Limit   int64    `json:"limit" default:"10" example:"10"`                 // 限制数量，用于分页
	Users   []int64  `json:"users,omitempty" example:"123456,789012"`         // 用户ID过滤列表，可选
	Types   []string `json:"types,omitempty" example:"text,photo"`            // 消息类型过滤列表，可选值：text,photo,video,document,voice,audio,poll,story
}

type SearchResponse struct {
	Hits               []SearchHit `json:"hits,omitempty"`
	ProcessingTimeMs   int64       `json:"processingTimeMs,omitempty"`
	Offset             int64       `json:"offset,omitempty"`
	Limit              int64       `json:"limit,omitempty"`
	EstimatedTotalHits int64       `json:"estimatedTotalHits,omitempty"`
	SemanticHitCount   int64       `json:"semanticHitCount,omitempty"`
}

type SearchHit struct {
	ID           int64  `json:"id"` // Telegram MessageID
	Type         string `json:"type"`
	Message      string `json:"message"`                  // The original text of the message
	UserID       int64  `json:"user_id"`                  // The ID of the user who sent the message
	ChatID       int64  `json:"chat_id"`                  // The ID of the chat where the message was sent
	UserFullName string `json:"user_full_name,omitempty"` // The full name of the user who sent the message, if available
	ChatTitle    string `json:"chat_title,omitempty"`     // The title of the chat, if available
	Timestamp    int64  `json:"timestamp"`
	Formatted    struct {
		ID        string `json:"id"`
		Type      string `json:"type"`
		Message   string `json:"message"`
		UserID    string `json:"user_id"`
		ChatID    string `json:"chat_id"`
		Timestamp string `json:"timestamp"`
	} `json:"_formatted"`
}

func ResponseSearch(c *fiber.Ctx, rawResp *types.MessageSearchResponse) error {
	if rawResp == nil {
		return &fiber.Error{Code: fiber.StatusInternalServerError, Message: "Search response is nil"}
	}
	if len(rawResp.Hits) == 0 {
		return &fiber.Error{Code: fiber.StatusNotFound, Message: "No results found"}
	}
	resp := &SearchResponse{
		Hits:               make([]SearchHit, len(rawResp.Hits)),
		ProcessingTimeMs:   rawResp.ProcessingTimeMs,
		Offset:             rawResp.Offset,
		Limit:              rawResp.Limit,
		EstimatedTotalHits: rawResp.EstimatedTotalHits,
		SemanticHitCount:   rawResp.SemanticHitCount,
	}
	for i, hit := range rawResp.Hits {
		UserFullName := ""
		ChatTitle := ""
		user, err := database.GetUserInfo(c.Context(), hit.UserID)
		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return &fiber.Error{Code: fiber.StatusInternalServerError, Message: "Failed to retrieve user info"}
			}
			UserFullName = strconv.FormatInt(hit.UserID, 10)
		} else {
			UserFullName = strings.TrimSpace(fmt.Sprintf("%s %s", user.FirstName, user.LastName))
		}
		chat, err := database.GetIndexChat(c.Context(), hit.ChatID)
		if err != nil {
			return &fiber.Error{Code: fiber.StatusInternalServerError, Message: "Failed to retrieve chat info"}
		}
		ChatTitle = strings.TrimSpace(chat.Title)
		resp.Hits[i] = SearchHit{
			ID:           hit.ID,
			Type:         types.MessageTypeToString[types.MessageType(hit.Type)],
			Message:      hit.Message,
			UserID:       hit.UserID,
			UserFullName: UserFullName,
			ChatID:       hit.ChatID,
			ChatTitle:    ChatTitle,
			Timestamp:    hit.Timestamp,
			Formatted:    hit.Formatted,
		}
	}
	return c.JSON(fiber.Map{
		"status":  "success",
		"results": resp,
	})

}

type ReplyMessageRequest struct {
	ChatID    int64  `json:"chat_id" validate:"required" example:"123456789"`            // 聊天ID
	MessageID int    `json:"message_id" validate:"required" example:"987654321"`         // 消息ID
	Text      string `json:"text" validate:"required" example:"This is a reply message"` // 回复内容
}

type ForwardMessagesRequest struct {
	FromChatID int64 `json:"from_chat_id" validate:"required" example:"123456789"`  // 来源聊天ID
	ToChatID   int64 `json:"to_chat_id" validate:"required" example:"987654321"`    // 目标聊天ID
	MessageIDs []int `json:"message_ids" validate:"required" example:"123,456,789"` // 消息ID列表
}

type StreamFileRequest struct {
	ChatID    int64 `json:"chat_id" validate:"required" example:"123456789"`    // 聊天ID
	MessageID int   `json:"message_id" validate:"required" example:"987654321"` // 消息ID
}
