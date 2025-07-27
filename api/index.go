package api

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/krau/btts/database"
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
