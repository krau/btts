package bot

import (
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/krau/mygotg/dispatcher"
	"github.com/krau/mygotg/ext"
)

func StartHandler(ctx *ext.Context, update *ext.Update) error {
	if len(update.Args()) < 2 {
		ctx.Reply(update, ext.ReplyTextString("Yet Another Bot For Telegram Search..."), nil)
		return dispatcher.EndGroups
	}
	log.FromContext(ctx).Debugf("StartHandler: %v", update.Args())
	payload := update.Args()[1]
	action := strings.Split(payload, "_")[0]
	args := strings.Split(payload, "_")[1:]
	if !CheckPermission(ctx, update) {
		ctx.Reply(update, ext.ReplyTextString("Yet Another Bot For Telegram Search..."), nil)
		return dispatcher.EndGroups
	}
	switch action {
	case "fav":
		if len(args) != 2 {
			log.FromContext(ctx).Errorf("Invalid payload: %s", payload)
			return dispatcher.EndGroups
		}
		chatIDStr := args[0]
		messageIDStr := args[1]
		chatID, err := strconv.ParseInt(chatIDStr, 10, 64)
		if err != nil {
			ctx.Reply(update, ext.ReplyTextString("Invalid chat ID"), nil)
			return dispatcher.EndGroups
		}
		messageID, err := strconv.ParseInt(messageIDStr, 10, 64)
		if err != nil {
			ctx.Reply(update, ext.ReplyTextString("Invalid message ID"), nil)
			return dispatcher.EndGroups
		}
		if err := bi.UserClient.ForwardMessagesToFav(ctx, chatID, []int{int(messageID)}); err != nil {
			log.FromContext(ctx).Errorf("Failed to forward message: %v", err)
			ctx.Reply(update, ext.ReplyTextString("Failed to forward message"), nil)
			return dispatcher.EndGroups
		}
		msg, err := ctx.Reply(update, ext.ReplyTextString("Message forwarded to favorites"), nil)
		if err == nil {
			go func() {
				time.Sleep(3 * time.Second)
				ctx.DeleteMessages(update.EffectiveChat().GetID(), []int{msg.GetID()})
			}()
		}
		ctx.DeleteMessages(update.EffectiveChat().GetID(), []int{update.EffectiveMessage.GetID()})
	default:
		ctx.Reply(update, ext.ReplyTextString("Unknown action: "+action), nil)
		return dispatcher.EndGroups
	}
	return dispatcher.EndGroups
}
