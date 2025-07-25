package utils

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/celestix/gotgproto/ext"
	"github.com/duke-git/lancet/v2/slice"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/tg"
	"github.com/krau/btts/database"
	"github.com/krau/btts/types"
	"github.com/krau/btts/utils/cache"
	"github.com/rs/xid"
)

func ExtraMessageMediaText(media tg.MessageMediaClass) (string, types.MessageType) {
	var messageType types.MessageType
	var messageSB strings.Builder
	switch m := media.(type) {
	case *tg.MessageMediaPhoto:
		messageType = types.MessageTypePhoto
	case *tg.MessageMediaDocument:
		doc, ok := m.Document.AsNotEmpty()
		if !ok {
			return "", messageType
		}
		messageType = types.MessageTypeDocument
		for _, attr := range doc.GetAttributes() {
			switch attr := attr.(type) {
			case *tg.DocumentAttributeHasStickers:
				return "", messageType
			case *tg.DocumentAttributeFilename:
				filename := attr.GetFileName()
				if slice.Contain(types.StickerFileNames, filename) {
					return "", messageType
				}
				messageSB.WriteString(filename + " ")
			case *tg.DocumentAttributeAudio:
				title, ok := attr.GetTitle()
				if ok {
					messageSB.WriteString(title + " ")
				}
				messageType = types.MessageTypeAudio
			case *tg.DocumentAttributeVideo:
				messageType = types.MessageTypeVideo
			}
		}
	case *tg.MessageMediaPoll:
		messageType = types.MessageTypePoll
		poll := m.GetPoll()
		messageSB.WriteString(poll.GetQuestion().Text)
		for _, option := range poll.GetAnswers() {
			messageSB.WriteString(" " + option.Text.GetText())
		}
	case *tg.MessageMediaStory:
		story, ok := m.GetStory()
		if !ok {
			return "", messageType
		}
		switch story := story.(type) {
		case *tg.StoryItem:
			messageType = types.MessageTypeStory
			caption, ok := story.GetCaption()
			if ok {
				messageSB.WriteString(caption + " ")
			}
		default:
			return "", messageType
		}
	}
	return messageSB.String(), messageType
}

func BuildSearchReplyMarkup(ctx context.Context, currentPage int64, data types.SearchRequest) (*tg.ReplyInlineMarkup, error) {
	cacheid := xid.New().String()
	if err := cache.Set(cacheid, data); err != nil {
		return nil, err
	}
	mtbuttons := make([]tg.KeyboardButtonClass, 0)
	for i := range len(types.MessageTypeToEmoji) {
		text := types.MessageTypeToEmoji[types.MessageType(i)]
		if data.TypeFilters != nil && slice.Contain(data.TypeFilters, types.MessageType(i)) {
			text += " √"
		}
		mtbuttons = append(mtbuttons, &tg.KeyboardButtonCallback{
			Text: text,
			Data: fmt.Appendf(nil, "filter %d %s", i, cacheid),
		})
	}

	messageTypeFilterRow1 := &tg.KeyboardButtonRow{
		Buttons: mtbuttons[:4],
	}
	messageTypeFilterRow2 := &tg.KeyboardButtonRow{
		Buttons: mtbuttons[4:],
	}

	return &tg.ReplyInlineMarkup{
		Rows: []tg.KeyboardButtonRow{
			*messageTypeFilterRow1,
			*messageTypeFilterRow2,
			{
				Buttons: []tg.KeyboardButtonClass{
					&tg.KeyboardButtonCallback{
						Text: "上一页",
						Data: fmt.Appendf(nil, "search %d %s", currentPage-1, cacheid),
					},
					&tg.KeyboardButtonCallback{
						Text: fmt.Sprintf("第%d页", currentPage),
						Data: fmt.Append(nil, "noop"),
					},
					&tg.KeyboardButtonCallback{
						Text: "下一页",
						Data: fmt.Appendf(nil, "search %d %s", currentPage+1, cacheid),
					},
				},
			},
		},
	}, nil
}

func BuildResultStyling(ctx context.Context, resp *types.MessageSearchResponse, botUsername ...string) []styling.StyledTextOption {
	var resultStyling []styling.StyledTextOption

	resultStyling = append(resultStyling, styling.Plain(fmt.Sprintf("找到约 %d 条结果, 耗时 %dms\n", resp.EstimatedTotalHits, resp.ProcessingTimeMs)))

	for _, hit := range resp.Hits {

		chatDisplay := hit.Formatted.ChatID
		chat, err := database.GetIndexChat(ctx, hit.ChatID)
		if err == nil {
			if chat.Title != "" {
				chatDisplay = chat.Title
			}
		}
		senderInfo := func() string {
			if hit.UserID == hit.ChatID {
				// 频道消息或私聊的对方
				return chatDisplay
			}
			userDisplay := hit.Formatted.UserID
			user, err := database.GetUserInfo(ctx, hit.UserID)
			if err == nil {
				userDisplay = func() string {
					userDisplay = user.FirstName
					if user.LastName != "" {
						if userDisplay != "" {
							userDisplay += " "
						}
						userDisplay += user.LastName
					}
					return userDisplay
				}()
			}
			return fmt.Sprintf("%s | %s", userDisplay, chatDisplay)
		}()

		resultStyling = append(resultStyling, styling.Bold(fmt.Sprintf("\n%s", senderInfo)))

		timeStr := time.Unix(hit.Timestamp, 0).Format("06-01-02 15:04:05")
		resultStyling = append(resultStyling, styling.Plain(fmt.Sprintf(" [%s]\n", timeStr)))

		msgLink := func() string {
			if chat.Type == int(database.ChatTypeChannel) || botUsername == nil {
				return fmt.Sprintf("https://t.me/c/%d/%d", hit.ChatID, hit.ID)
			}
			return fmt.Sprintf("https://t.me/%s/?start=fav_%d_%d", botUsername[0], hit.ChatID, hit.ID)
		}()
		hitFormattedMsg := types.MessageTypeToEmoji[types.MessageType(hit.Type)] + " " + strings.ReplaceAll(hit.Formatted.Message, "\n", " ")
		resultStyling = append(resultStyling, styling.TextURL(hitFormattedMsg, msgLink))
	}

	return resultStyling
}

func GetChatDBFromUpdateArgs(ctx *ext.Context, update *ext.Update) (*database.IndexChat, error) {
	args := update.Args()
	if len(args) < 2 {
		return nil, errors.New("args not enough")
	}
	chatID, err := strconv.Atoi(args[1])
	if err != nil {
		return nil, fmt.Errorf("invalid chat ID")
	}
	chatDB, err := database.GetIndexChat(ctx, int64(chatID))
	if err != nil {
		return nil, fmt.Errorf("failed to get chat DB: %w", err)
	}
	if chatDB == nil {
		return nil, fmt.Errorf("chat not found")
	}
	return chatDB, nil
}
