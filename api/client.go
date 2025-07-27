package api

import (
	"fmt"
	"mime"
	"path"

	"github.com/gofiber/fiber/v2"
	"github.com/krau/btts/service"
	"github.com/krau/btts/userclient"
)

// ReplyMessage 回复指定消息
//
//	@Summary		回复指定消息
//	@Description	向指定聊天中的指定消息发送回复
//	@Tags			Client
//	@Accept			json
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Param			request	body		ReplyMessageRequest									true	"回复消息请求参数"
//	@Success		200		{object}	map[string]interface{}								"成功响应"
//	@Success		200		{object}	object{status=string,message=string,data=object}	"成功响应示例"
//	@Failure		400		{object}	map[string]string									"请求参数错误"
//	@Failure		401		{object}	map[string]string									"未授权"
//	@Failure		500		{object}	map[string]string									"服务器内部错误"
//	@Router			/client/reply [post]
func ReplyMessage(c *fiber.Ctx) error {
	var req ReplyMessageRequest
	if err := c.BodyParser(&req); err != nil {
		return &fiber.Error{Code: fiber.StatusBadRequest, Message: "Invalid request body"}
	}
	if err := validate.StructCtx(c.Context(), &req); err != nil {
		return &fiber.Error{Code: fiber.StatusBadRequest, Message: "Validation failed: " + err.Error()}
	}
	msg, err := userclient.GetUserClient().ReplyMessage(c.Context(), req.ChatID, req.MessageID, req.Text)
	if err != nil {
		return &fiber.Error{Code: fiber.StatusInternalServerError, Message: err.Error()}
	}
	return c.JSON(fiber.Map{
		"status":  "success",
		"message": "Reply sent successfully",
		"data":    msg,
	})
}

// ForwardMessages 转发消息
//
//	@Summary		转发消息
//	@Description	将指定聊天中的消息转发到目标聊天
//	@Tags			Client
//	@Accept			json
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Param			request	body		ForwardMessagesRequest							true	"转发消息请求参数"
//	@Success		200		{object}	map[string]interface{}							"成功响应"
//	@Success		200		{object}	object{status=string,message=string}			"成功响应示例"
//	@Failure		400		{object}	map[string]string								"请求参数错误"
//	@Failure		401		{object}	map[string]string								"未授权"
//	@Failure		500		{object}	map[string]string								"服务器内部错误"
//	@Router			/client/forward [post]
func ForwardMessages(c *fiber.Ctx) error {
	var req ForwardMessagesRequest
	if err := c.BodyParser(&req); err != nil {
		return &fiber.Error{Code: fiber.StatusBadRequest, Message: "Invalid request body"}
	}
	if err := validate.StructCtx(c.Context(), &req); err != nil {
		return &fiber.Error{Code: fiber.StatusBadRequest, Message: "Validation failed: " + err.Error()}
	}
	if err := userclient.GetUserClient().ForwardMessages(c.Context(), req.FromChatID, req.ToChatID, req.MessageIDs); err != nil {
		return &fiber.Error{Code: fiber.StatusInternalServerError, Message: err.Error()}
	}
	return c.JSON(fiber.Map{
		"status":  "success",
		"message": "Message forwarded successfully",
	})
}

// StreamFile 获取文件流
//
//	@Description	获取指定聊天中指定消息的文件流
//	@Tags			Client
//	@Accept			json
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Param			chat_id		query	int64	true	"聊天ID"
//	@Param			message_id	query	int	true	"消息ID"
//	@Success		200			{file}	file	"文件流"
//	@Failure		400			{object}	map[string]string	"请求参数错误"
//	@Failure		401			{object}	map[string]string	"未授权"
//	@Failure		500			{object}	map[string]string	"服务器内部错误"
//	@Router			/client/filestream [get]
func StreamFile(c *fiber.Ctx) error {
	chatID := c.QueryInt("chat_id", 0)
	messageID := c.QueryInt("message_id", 0)
	if chatID <= 0 || messageID <= 0 {
		return &fiber.Error{Code: fiber.StatusBadRequest, Message: "Invalid chat_id or message_id"}
	}
	file, err := service.GetTGFileReader(c.Context(), int64(chatID), messageID)
	if err != nil {
		return &fiber.Error{Code: fiber.StatusInternalServerError, Message: err.Error()}
	}
	if file.Size <= 0 {
		file.Size = -1
	}
	mt := mime.TypeByExtension(path.Ext(file.Name))
	if mt == "" {
		mt = "application/octet-stream"
	}
	c.Set("Content-Type", mt)
	c.Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", file.Name))
	if file.Size > 0 {
		c.Set("Content-Length", fmt.Sprintf("%d", file.Size))
	}
	return c.SendStream(file, int(file.Size))
}
