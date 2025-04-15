package bot

import (
	"fmt"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/krau/btts/database"
)

func ListHandler(ctx *ext.Context, update *ext.Update) error {
	if !CheckPermission(ctx, update) {
		return dispatcher.EndGroups
	}
	chats, err := database.GetAllIndexChats(ctx)
	if err != nil {
		log.FromContext(ctx).Error("Failed to list chats", "error", err)
		ctx.Reply(update, ext.ReplyTextString("Failed to list chats"), nil)
		return dispatcher.EndGroups
	}
	if len(chats) == 0 {
		ctx.Reply(update, ext.ReplyTextString("No chats"), nil)
		return dispatcher.EndGroups
	}
	var chatsStyling []styling.StyledTextOption
	chatsStyling = append(chatsStyling, styling.Plain(fmt.Sprintf("已添加 %d 个聊天\n\n", len(chats))))
	for _, chat := range chats {
		chatsStyling = append(chatsStyling, styling.Code(fmt.Sprintf("%d", chat.ChatID)))
		chatsStyling = append(chatsStyling, styling.Plain(fmt.Sprintf(" - %s\n", chat.Title)))
		chatsStyling = append(chatsStyling, styling.Plain(fmt.Sprintf("Watching: %t , Public: %t , WatchDelete: %t\n\n", chat.Watching, chat.Public, !chat.NoDelete)))
	}
	ctx.Reply(update, ext.ReplyTextStyledTextArray(chatsStyling), nil)
	return dispatcher.EndGroups
}
