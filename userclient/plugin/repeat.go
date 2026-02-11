package plugin

import (
	"strconv"
	"sync"

	"github.com/gotd/td/tg"
	"github.com/krau/mygotg/ext"
)

// RepeatHandler repeats(forwards) the message to the current chat and deletes the command message.([prefix]re [count])
func RepeatHandler(ctx *Context, u *ext.Update) error {
	count := 1
	chatId := u.EffectiveChat().GetID()
	usage := "Usage: re <count> reply to a message to repeat it."
	replyMessage := u.EffectiveMessage.ReplyToMessage
	if replyMessage == nil {
		ctx.EditMessage(chatId, &tg.MessagesEditMessageRequest{
			ID:      u.EffectiveMessage.GetID(),
			Message: usage,
		})
		return nil
	}
	if len(ctx.Args) > 0 {
		var err error
		count, err = strconv.Atoi(ctx.Args[0])
		if err != nil || count < 1 || count > 100 {
			ctx.EditMessage(u.EffectiveChat().GetID(), &tg.MessagesEditMessageRequest{
				ID:      u.EffectiveMessage.GetID(),
				Message: "Invalid count, must be between 1 and 100",
			})
			return nil
		}
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		ctx.DeleteMessages(chatId, []int{u.EffectiveMessage.GetID()})
	})
	for i := 0; i < count; i++ {
		wg.Go(func() {
			req := &tg.MessagesForwardMessagesRequest{
				ID: []int{replyMessage.GetID()},
			}
			ctx.ForwardMessages(chatId, chatId, req)
		})
	}
	return nil
}
