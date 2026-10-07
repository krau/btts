package plugin

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/gotd/td/tg"
	"github.com/krau/mygotg/ext"
)

// RepeatHandler forwards the replied-to message 1–100 times and deletes the command message.
func RepeatHandler(ctx *Context, u *ext.Update) error {
	count := 1
	chatId := u.EffectiveChat().GetID()
	usage := "Usage: re <count> reply to a message to repeat it."
	replyMessage := u.EffectiveMessage.ReplyToMessage
	if replyMessage == nil {
		_, err := ctx.EditMessage(chatId, &tg.MessagesEditMessageRequest{
			ID:      u.EffectiveMessage.GetID(),
			Message: usage,
		})
		return err
	}
	if len(ctx.Args) > 0 {
		var err error
		count, err = strconv.Atoi(ctx.Args[0])
		if err != nil || count < 1 || count > 100 {
			_, err := ctx.EditMessage(chatId, &tg.MessagesEditMessageRequest{
				ID:      u.EffectiveMessage.GetID(),
				Message: "Invalid count, must be between 1 and 100",
			})
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var result error
	if err := ctx.DeleteMessages(chatId, []int{u.EffectiveMessage.GetID()}); err != nil {
		result = fmt.Errorf("repeat: delete command: %w", err)
	}
	// ForwardMessages uses the context's non-concurrent random generator.
	for i := range count {
		if err := ctx.Err(); err != nil {
			return errors.Join(result, err)
		}
		_, err := ctx.ForwardMessages(chatId, chatId, &tg.MessagesForwardMessagesRequest{
			ID: []int{replyMessage.GetID()},
		})
		if err != nil {
			result = errors.Join(result, fmt.Errorf("repeat: forward %d/%d: %w", i+1, count, err))
		}
	}
	return result
}
