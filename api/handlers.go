package api

import (
	"errors"
	"strconv"
	"strings"

	"github.com/duke-git/lancet/v2/slice"
	"github.com/gofiber/fiber/v2"
	"github.com/krau/btts/database"
	"github.com/krau/btts/engine"
	"github.com/krau/btts/types"
	"gorm.io/gorm"
)

func GetIndexed(c *fiber.Ctx) error {
	chats, err := database.GetAllIndexChats(c.Context())
	if err != nil {
		return &fiber.Error{Code: fiber.StatusInternalServerError, Message: "Failed to retrieve indexed chats"}
	}
	if len(chats) == 0 {
		return &fiber.Error{Code: fiber.StatusNotFound, Message: "No indexed chats found"}
	}
	return c.JSON(fiber.Map{
		"status": "success",
		"chats":  chats,
	})
}

func GetIndexInfo(c *fiber.Ctx) error {
	chatID, err := c.ParamsInt("chat_id")
	if err != nil {
		return &fiber.Error{Code: fiber.StatusBadRequest, Message: "Chat ID is required"}
	}
	indexChat, err := database.GetIndexChat(c.Context(), int64(chatID))
	if err != nil {
		code := fiber.StatusInternalServerError
		msg := "Failed to retrieve index info"
		if errors.Is(err, gorm.ErrRecordNotFound) {
			code = fiber.StatusNotFound
			msg = "Index not found for the specified chat"
		}
		return &fiber.Error{Code: code, Message: msg}
	}
	if indexChat == nil {
		return &fiber.Error{Code: fiber.StatusNotFound, Message: "Index not found for the specified chat"}
	}
	return c.JSON(fiber.Map{
		"status": "success",
		"index":  indexChat,
	})
}

func SearchOnChatByGet(c *fiber.Ctx) error {
	chatID, err := c.ParamsInt("chat_id")
	if err != nil {
		return &fiber.Error{Code: fiber.StatusBadRequest, Message: "Chat ID is required"}
	}
	query := c.Query("q")
	if query == "" {
		return &fiber.Error{Code: fiber.StatusBadRequest, Message: "Query parameter 'q' is required"}
	}
	offset := c.QueryInt("offset")
	limit := c.QueryInt("limit", 10)

	req := types.SearchRequest{
		ChatID: int64(chatID),
		Query:  query,
		Offset: int64(offset),
		Limit:  int64(limit),
	}
	if users := c.Query("users"); users != "" {
		userIDs := slice.Compact(slice.Map(strings.Split(users, ","), func(i int, userId string) int64 {
			userID, err := strconv.ParseInt(userId, 10, 64)
			if err != nil {
				return 0
			}
			return userID
		}))
		if len(userIDs) > 0 {
			req.UserFilters = userIDs
		}
	}
	if msgTypeStr := c.Query("types"); msgTypeStr != "" {
		msgTypes := slice.Compact(slice.Map(strings.Split(msgTypeStr, ","), func(i int, msgType string) types.MessageType {
			return types.MessageTypeFromString[msgType]
		}))
		if len(msgTypes) > 0 {
			req.TypeFilters = msgTypes
		}
	}
	results, err := engine.Instance.Search(c.Context(), req)
	if err != nil {
		return &fiber.Error{Code: fiber.StatusInternalServerError, Message: "Failed to search index"}
	}
	return c.JSON(fiber.Map{
		"status":  "success",
		"results": results,
	})
}

func SearchOnChatByPost(c *fiber.Ctx) error {
	chatID, err := c.ParamsInt("chat_id")
	if err != nil {
		return &fiber.Error{Code: fiber.StatusBadRequest, Message: "Chat ID is required"}
	}
	request := new(SearchOnChatByPostRequest)
	if err := c.BodyParser(request); err != nil {
		return &fiber.Error{Code: fiber.StatusBadRequest, Message: "Invalid request body"}
	}
	if err := validate.StructCtx(c.Context(), request); err != nil {
		return &fiber.Error{Code: fiber.StatusBadRequest, Message: "Validation failed: " + err.Error()}
	}

	req := types.SearchRequest{
		ChatID:      int64(chatID),
		Query:       request.Query,
		Offset:      request.Offset,
		Limit:       request.Limit,
		UserFilters: request.Users,
	}
	if len(request.Types) > 0 {
		if msgTypes := slice.Compact(slice.Map(request.Types, func(i int, msgType string) types.MessageType {
			return types.MessageTypeFromString[msgType]
		})); len(msgTypes) > 0 {
			req.TypeFilters = msgTypes
		}
	}
	results, err := engine.Instance.Search(c.Context(), req)
	if err != nil {
		return &fiber.Error{Code: fiber.StatusInternalServerError, Message: "Failed to search index"}
	}
	return c.JSON(fiber.Map{
		"status":  "success",
		"results": results,
	})

}

func SearchOnMultiChatByPost(c *fiber.Ctx) error {
	request := new(SearchOnMultiChatByPostRequest)
	if err := c.BodyParser(request); err != nil {
		return &fiber.Error{Code: fiber.StatusBadRequest, Message: "Invalid request body"}
	}
	if err := validate.StructCtx(c.Context(), request); err != nil {
		return &fiber.Error{Code: fiber.StatusBadRequest, Message: "Validation failed: " + err.Error()}
	}

	req := types.SearchRequest{
		ChatIDs:     request.ChatIDs,
		Query:       request.Query,
		Offset:      request.Offset,
		Limit:       request.Limit,
		UserFilters: request.Users,
	}
	if len(request.ChatIDs) == 0 {
		req.ChatIDs = database.AllChatIDs()
	}
	if len(request.Types) > 0 {
		if msgTypes := slice.Compact(slice.Map(request.Types, func(i int, msgType string) types.MessageType {
			return types.MessageTypeFromString[msgType]
		})); len(msgTypes) > 0 {
			req.TypeFilters = msgTypes
		}
	}
	results, err := engine.Instance.Search(c.Context(), req)
	if err != nil {
		return &fiber.Error{Code: fiber.StatusInternalServerError, Message: "Failed to search index"}
	}
	return c.JSON(fiber.Map{
		"status":  "success",
		"results": results,
	})
}
