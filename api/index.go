package api

import (
	"errors"

	"github.com/bytedance/sonic"
	"github.com/gofiber/fiber/v2"
	"github.com/krau/btts/database"
	"github.com/krau/btts/engine"
	"github.com/krau/btts/types"
	"github.com/meilisearch/meilisearch-go"
	"gorm.io/gorm"
)

// GetIndexed 获取所有已索引的聊天
//
//	@Summary		获取所有已索引的聊天
//	@Description	获取系统中所有已索引的聊天列表
//	@Tags			Chat
//	@Accept			json
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Success		200	{object}	map[string]interface{}								"成功响应"
//	@Success		200	{object}	object{status=string,chats=[]database.IndexChat}	"成功响应示例"
//	@Failure		401	{object}	map[string]string									"未授权"
//	@Failure		404	{object}	map[string]string									"未找到已索引的聊天"
//	@Failure		500	{object}	map[string]string									"服务器内部错误"
//	@Router			/indexed [get]
func GetIndexed(c *fiber.Ctx) error {
	chats, err := database.GetAllIndexChats(c.Context())
	if err != nil {
		return &fiber.Error{Code: fiber.StatusInternalServerError, Message: err.Error()}
	}
	if len(chats) == 0 {
		return &fiber.Error{Code: fiber.StatusNotFound, Message: "No indexed chats found"}
	}
	return c.JSON(fiber.Map{
		"status": "success",
		"chats":  chats,
	})
}

// GetIndexInfo 获取指定聊天的索引信息
//
//	@Summary		获取指定聊天的索引信息
//	@Description	根据聊天ID获取该聊天的索引详细信息
//	@Tags			Chat
//	@Accept			json
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Param			chat_id	path		int												true	"聊天ID"
//	@Success		200		{object}	map[string]interface{}							"成功响应"
//	@Success		200		{object}	object{status=string,index=database.IndexChat}	"成功响应示例"
//	@Failure		400		{object}	map[string]string								"聊天ID是必需的"
//	@Failure		401		{object}	map[string]string								"未授权"
//	@Failure		404		{object}	map[string]string								"未找到指定聊天的索引"
//	@Failure		500		{object}	map[string]string								"服务器内部错误"
//	@Router			/index/{chat_id} [get]
func GetIndexInfo(c *fiber.Ctx) error {
	chatID, err := c.ParamsInt("chat_id")
	if err != nil {
		return &fiber.Error{Code: fiber.StatusBadRequest, Message: "Chat ID is required"}
	}
	indexChat, err := database.GetIndexChat(c.Context(), int64(chatID))
	if err != nil {
		code := fiber.StatusInternalServerError
		msg := err.Error()
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

// FetchMessages 从索引中获取指定消息
//
//	@Summary		从索引中获取指定消息
//	@Description	根据消息ID列表从索引中获取消息内容
//	@Tags			Chat
//	@Accept			json
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Param			chat_id	path		int												true	"聊天ID"
//	@Param			request	body		FetchMessagesRequest								true	"请求参数"
//	@Success		200		{object}	map[string]interface{}							"成功响应"
//	@Success		200		{object}	object{status=string,messages=[]types.SearchHit}	"成功响应示例"
//	@Failure		400		{object}	map[string]string								"请求参数错误"
//	@Failure		401		{object}	map[string]string								"未授权"
//	@Failure		404		{object}	map[string]string								"未找到指定聊天的索引"
//	@Failure		500		{object}	map[string]string								"服务器内部错误"
//	@Router			/index/{chat_id}/msgs/fetch [post]
func FetchMessages(c *fiber.Ctx) error {
	chatID, err := c.ParamsInt("chat_id")
	if err != nil {
		return &fiber.Error{Code: fiber.StatusBadRequest, Message: "Chat ID is required"}
	}
	request := new(FetchMessagesRequest)
	if err := c.BodyParser(request); err != nil {
		return &fiber.Error{Code: fiber.StatusBadRequest, Message: "Invalid request body"}
	}
	if err := validate.StructCtx(c.Context(), request); err != nil {
		return &fiber.Error{Code: fiber.StatusBadRequest, Message: "Validation failed: " + err.Error()}
	}
	indexManager := engine.GetEngine().Index(int64(chatID))
	if indexManager == nil {
		return &fiber.Error{Code: fiber.StatusNotFound, Message: "Index not found for the specified chat"}
	}
	var resp meilisearch.DocumentsResult
	// [TODO] 不知道为什么传给 meilisearch 的 ids 总是空的
	err = indexManager.GetDocumentReader().GetDocumentsWithContext(c.Context(), &meilisearch.DocumentsQuery{
		Limit:  20,
		Offset: 0,
		Ids:    request.IDs,
	}, &resp)
	if err != nil {
		return &fiber.Error{Code: fiber.StatusInternalServerError, Message: "Failed to fetch messages: " + err.Error()}
	}
	hitBytes, err := sonic.Marshal(resp.Results)
	if err != nil {
		return &fiber.Error{Code: fiber.StatusInternalServerError, Message: "Failed to marshal response: " + err.Error()}
	}
	var hits []types.SearchHit
	err = sonic.Unmarshal(hitBytes, &hits)
	if err != nil {
		return &fiber.Error{Code: fiber.StatusInternalServerError, Message: "Failed to unmarshal response: " + err.Error()}
	}
	searchResp := &types.MessageSearchResponse{
		Raw:                &resp,
		Hits:               hits,
		EstimatedTotalHits: resp.Total,
		Offset:             resp.Offset,
		Limit:              resp.Limit,
		ProcessingTimeMs:   0,
	}
	return ResponseSearch(c, searchResp)
}
