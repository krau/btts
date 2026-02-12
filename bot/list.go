package bot

import (
	"fmt"
	"strconv"

	"github.com/charmbracelet/log"
	"github.com/duke-git/lancet/v2/strutil"
	"github.com/gotd/td/telegram/message/entity"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/tg"
	"github.com/krau/btts/database"
	"github.com/krau/mygotg/dispatcher"
	"github.com/krau/mygotg/ext"
)

const listPageSize = 16

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
	chatsStyling, markup := buildListPage(chats, 1, hasPermission)
	ctx.Reply(update, ext.ReplyTextStyledTextArray(chatsStyling), &ext.ReplyOpts{
		Markup: markup,
	})
	return dispatcher.EndGroups
}

func ListCallbackHandler(ctx *ext.Context, update *ext.Update) error {
	args := update.Args()
	if len(args) < 2 {
		ctx.AnswerCallback(&tg.MessagesSetBotCallbackAnswerRequest{
			QueryID: update.CallbackQuery.GetQueryID(),
			Message: "Invalid page",
			Alert:   true,
		})
		return dispatcher.EndGroups
	}
	page, err := strconv.Atoi(args[1])
	if err != nil {
		ctx.AnswerCallback(&tg.MessagesSetBotCallbackAnswerRequest{
			QueryID: update.CallbackQuery.GetQueryID(),
			Message: "Invalid page",
			Alert:   true,
		})
		return dispatcher.EndGroups
	}
	hasPermission := CheckPermission(ctx, update)
	var chats []*database.IndexChat
	if hasPermission {
		chats, err = database.GetAllIndexChats(ctx)
	} else {
		chats, err = database.GetAllPublicIndexChats(ctx)
	}
	if err != nil {
		log.FromContext(ctx).Error("Failed to list chats", "error", err)
		ctx.AnswerCallback(&tg.MessagesSetBotCallbackAnswerRequest{
			QueryID: update.CallbackQuery.GetQueryID(),
			Message: "Failed to list chats",
			Alert:   true,
		})
		return dispatcher.EndGroups
	}
	if len(chats) == 0 {
		ctx.EditMessage(update.EffectiveChat().GetID(), &tg.MessagesEditMessageRequest{
			ID:      update.CallbackQuery.MsgID,
			Message: "No chats",
		})
		return dispatcher.EndGroups
	}
	chatsStyling, markup := buildListPage(chats, page, hasPermission)
	eb := entity.Builder{}
	if err := styling.Perform(&eb, chatsStyling...); err != nil {
		log.FromContext(ctx).Errorf("Failed to build styling: %v", err)
		ctx.AnswerCallback(&tg.MessagesSetBotCallbackAnswerRequest{
			QueryID: update.CallbackQuery.GetQueryID(),
			Message: "Styling Error",
			Alert:   true,
		})
		return dispatcher.EndGroups
	}
	text, entities := eb.Complete()
	editReq := &tg.MessagesEditMessageRequest{ID: update.CallbackQuery.MsgID}
	editReq.SetMessage(text)
	editReq.SetEntities(entities)
	editReq.SetReplyMarkup(markup)
	if _, err := ctx.EditMessage(update.EffectiveChat().GetID(), editReq); err != nil {
		log.FromContext(ctx).Errorf("Failed to edit message: %v", err)
	}
	return dispatcher.EndGroups
}

func buildListPage(chats []*database.IndexChat, page int, hasPermission bool) ([]styling.StyledTextOption, *tg.ReplyInlineMarkup) {
	if page < 1 {
		page = 1
	}
	totalPages := (len(chats) + listPageSize - 1) / listPageSize
	if totalPages == 0 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}
	start := (page - 1) * listPageSize
	end := start + listPageSize
	if end > len(chats) {
		end = len(chats)
	}
	pageChats := chats[start:end]

	chatsStyling := make([]styling.StyledTextOption, 0)
	chatsStyling = append(chatsStyling, styling.Plain(fmt.Sprintf("已添加 %d 个聊天（第 %d/%d 页）\n\n", len(chats), page, totalPages)))
	for _, chat := range pageChats {
		chatsStyling = append(chatsStyling, styling.Code(fmt.Sprintf("%d", chat.ChatID)))
		chatsStyling = append(chatsStyling, styling.Plain(fmt.Sprintf(" - %s\n", chat.Title)))
		if hasPermission {
			chatsStyling = append(chatsStyling, styling.Plain(fmt.Sprintf("Watching: %t , Public: %t , WatchDelete: %t , OCR: %t\n", chat.Watching, chat.Public, !chat.NoDelete, !chat.NoOcr)))
		}
	}
	chatsStyling = append(chatsStyling, styling.Plain("\n点击按钮选择一个聊天进行搜索"))

	selectButtonRow := make([]tg.KeyboardButtonRow, 0)
	buttons := make([]tg.KeyboardButtonClass, 0)
	for i, chat := range pageChats {
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
		if len(buttons) == 4 || i == len(pageChats)-1 {
			selectButtonRow = append(selectButtonRow, tg.KeyboardButtonRow{
				Buttons: buttons,
			})
			buttons = make([]tg.KeyboardButtonClass, 0)
		}
	}
	if totalPages > 1 {
		navButtons := make([]tg.KeyboardButtonClass, 0)
		if page > 1 {
			navButtons = append(navButtons, &tg.KeyboardButtonCallback{
				Text: "⬅️",
				Data: fmt.Appendf(nil, "list %d", page-1),
			})
		}
		navButtons = append(navButtons, &tg.KeyboardButtonCallback{
			Text: fmt.Sprintf("%d/%d", page, totalPages),
			Data: fmt.Appendf(nil, "list %d", page),
		})
		if page < totalPages {
			navButtons = append(navButtons, &tg.KeyboardButtonCallback{
				Text: "➡️",
				Data: fmt.Appendf(nil, "list %d", page+1),
			})
		}
		selectButtonRow = append(selectButtonRow, tg.KeyboardButtonRow{Buttons: navButtons})
	}

	return chatsStyling, &tg.ReplyInlineMarkup{Rows: selectButtonRow}
}
