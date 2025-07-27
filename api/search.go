package api

import (
	"strconv"
	"strings"

	"github.com/duke-git/lancet/v2/slice"
	"github.com/gofiber/fiber/v2"
	"github.com/krau/btts/database"
	"github.com/krau/btts/engine"
	"github.com/krau/btts/types"
)

// SearchOnChatByGet 使用GET方法在指定聊天中搜索消息
//
//	@Summary		在指定聊天中搜索消息 (GET方法)
//	@Description	使用GET方法在指定聊天中搜索消息，支持分页和过滤
//	@Tags			Search
//	@Accept			json
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Param			chat_id	path		int												true	"聊天ID"
//	@Param			q		query		string											true	"搜索查询字符串"
//	@Param			offset	query		int												false	"偏移量，默认为0"		default(0)
//	@Param			limit	query		int												false	"限制数量，默认为10"	default(10)
//	@Param			users	query		string											false	"用户ID列表，逗号分隔"	example("123456,789012")
//	@Param			types	query		string											false	"消息类型列表，逗号分隔"	example("text,photo,video")	Enums(text,photo,video,document,voice,audio,poll,story)
//	@Success		200		{object}	map[string]interface{}							"成功响应"
//	@Success		200		{object}	object{status=string,results=SearchResponse}	"成功响应示例"
//	@Failure		400		{object}	map[string]string								"请求参数错误"
//	@Failure		401		{object}	map[string]string								"未授权"
//	@Failure		500		{object}	map[string]string								"服务器内部错误"
//	@Router			/index/{chat_id}/search [get]
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
		msgTypes := slice.Map(strings.Split(msgTypeStr, ","), func(i int, msgType string) types.MessageType {
			return types.MessageTypeFromString[msgType]
		})
		if len(msgTypes) > 0 {
			req.TypeFilters = msgTypes
		}
	}
	results, err := engine.GetEngine().Search(c.Context(), req)
	if err != nil {
		return &fiber.Error{Code: fiber.StatusInternalServerError, Message: err.Error()}
	}
	return ResponseSearch(c, results)
}

// SearchOnChatByPost 使用POST方法在指定聊天中搜索消息
//
//	@Summary		在指定聊天中搜索消息 (POST方法)
//	@Description	使用POST方法在指定聊天中搜索消息，支持更复杂的搜索参数
//	@Tags			Search
//	@Accept			json
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Param			chat_id	path		int												true	"聊天ID"
//	@Param			request	body		SearchOnChatByPostRequest						true	"搜索请求参数"
//	@Success		200		{object}	map[string]interface{}							"成功响应"
//	@Success		200		{object}	object{status=string,results=SearchResponse}	"成功响应示例"
//	@Failure		400		{object}	map[string]string								"请求参数错误"
//	@Failure		401		{object}	map[string]string								"未授权"
//	@Failure		500		{object}	map[string]string								"服务器内部错误"
//	@Router			/index/{chat_id}/search [post]
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
		if msgTypes := slice.Map(request.Types, func(i int, msgType string) types.MessageType {
			return types.MessageTypeFromString[msgType]
		}); len(msgTypes) > 0 {
			req.TypeFilters = msgTypes
		}
	}
	results, err := engine.GetEngine().Search(c.Context(), req)
	if err != nil {
		return &fiber.Error{Code: fiber.StatusInternalServerError, Message: err.Error()}
	}
	return ResponseSearch(c, results)

}

// SearchOnMultiChatByPost 在多个聊天中搜索消息
//
//	@Summary		在多个聊天中搜索消息
//	@Description	在指定的多个聊天中搜索消息，如果未指定聊天ID则搜索所有聊天
//	@Tags			Search
//	@Accept			json
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Param			request	body		SearchOnMultiChatByPostRequest					true	"多聊天搜索请求参数"
//	@Success		200		{object}	map[string]interface{}							"成功响应"
//	@Success		200		{object}	object{status=string,results=SearchResponse}	"成功响应示例"
//	@Failure		400		{object}	map[string]string								"请求参数错误"
//	@Failure		401		{object}	map[string]string								"未授权"
//	@Failure		500		{object}	map[string]string								"服务器内部错误"
//	@Router			/index/multi-search [post]
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
		if msgTypes := slice.Map(request.Types, func(i int, msgType string) types.MessageType {
			return types.MessageTypeFromString[msgType]
		}); len(msgTypes) > 0 {
			req.TypeFilters = msgTypes
		}
	}
	results, err := engine.GetEngine().Search(c.Context(), req)
	if err != nil {
		return &fiber.Error{Code: fiber.StatusInternalServerError, Message: err.Error()}
	}
	return ResponseSearch(c, results)
}
