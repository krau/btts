package bot

import (
	"fmt"
	"strconv"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/duke-git/lancet/v2/strutil"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/tg"
	"github.com/krau/btts/database"
)

func ListHandler(ctx *ext.Context, update *ext.Update) error {
	hasPermission := CheckPermission(ctx, update)
	var chats []*database.IndexChat
	var err error
	if hasPermission {
		chats, err = database.GetAllIndexChats(ctx)
	} else {
		chats, err = database.GetAllPublicIndexChats(ctx)
	}
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
	selectButtonRow := make([]tg.KeyboardButtonRow, 0)
	buttons := make([]tg.KeyboardButtonClass, 0, 2)
	for i, chat := range chats {
		chatsStyling = append(chatsStyling, styling.Code(fmt.Sprintf("%d", chat.ChatID)))
		chatsStyling = append(chatsStyling, styling.Plain(fmt.Sprintf(" - %s\n", chat.Title)))
		if hasPermission {
			chatsStyling = append(chatsStyling, styling.Plain(fmt.Sprintf("Watching: %t , Public: %t , WatchDelete: %t\n\n", chat.Watching, chat.Public, !chat.NoDelete)))
		}
		button := &tg.KeyboardButtonCallback{
			Text: func() string {
				if chat.Title != "" {
					return strutil.Ellipsis(chat.Title, 10)
				}
				return strconv.Itoa(int(chat.ChatID))
			}(),
			Data: fmt.Appendf(nil, "select %d", chat.ChatID),
		}
		buttons = append(buttons, button)
		if len(buttons) == 2 || i == len(chats)-1 {
			selectButtonRow = append(selectButtonRow, tg.KeyboardButtonRow{
				Buttons: buttons,
			})
			buttons = make([]tg.KeyboardButtonClass, 0, 2)
		}
	}
	chatsStyling = append(chatsStyling, styling.Plain("\n点击按钮选择一个聊天进行搜索"))
	ctx.Reply(update, ext.ReplyTextStyledTextArray(chatsStyling), &ext.ReplyOpts{
		Markup: &tg.ReplyInlineMarkup{
			Rows: selectButtonRow,
		},
	})
	return dispatcher.EndGroups
}
