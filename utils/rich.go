package utils

import (
	"github.com/gotd/td/tg"
	"github.com/krau/mygotg/ext"
	"github.com/krau/mygotg/types"
)

// EditRichMessage edits an existing message using Telegram's rich message
// format so Markdown-style agent output is rendered instead of shown literally.
func EditRichMessage(ctx *ext.Context, chatID int64, messageID int, text string) error {
	_, err := ctx.EditMessage(chatID, &tg.MessagesEditMessageRequest{
		ID:          messageID,
		RichMessage: &tg.InputRichMessageMarkdown{Markdown: text},
	})
	return err
}

// SendRichMessage sends a new rich message, optionally as a reply.
func SendRichMessage(ctx *ext.Context, chatID int64, replyTo int, text string) (*types.Message, error) {
	request := &tg.MessagesSendMessageRequest{
		RichMessage: &tg.InputRichMessageMarkdown{Markdown: text},
	}
	if replyTo != 0 {
		request.SetReplyTo(&tg.InputReplyToMessage{ReplyToMsgID: replyTo})
	}
	return ctx.SendMessage(chatID, request)
}
