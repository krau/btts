package bot

import (
	"fmt"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/gotd/td/telegram/message/inline"
	"github.com/gotd/td/tg"
	"github.com/krau/btts/database"
	"github.com/krau/btts/types"
)

func InlineQueryHandler(ctx *ext.Context, update *ext.Update) error {
	if !CheckPermission(ctx, update) {
		return dispatcher.EndGroups
	}
	query := update.InlineQuery.GetQuery()
	resp, err := bi.Engine.Search(ctx,
		types.SearchRequest{
			Query:    query,
			Limit:    48,
			AllChats: true,
		})
	if err != nil {
		return err
	}
	results := make([]inline.ResultOption, 0, len(resp.Hits))
	for _, hit := range resp.Hits {
		userName := hit.Formatted.UserID
		user, err := database.GetUserInfo(ctx, hit.UserID)
		if err == nil {
			userName = user.FullName()
		}
		title := fmt.Sprintf("%s [%s]", userName, types.MessageTypeToDisplayString[types.MessageType(hit.Type)])
		results = append(results, inline.Article(
			title, inline.MessageText(hit.Message).Row(
				&tg.KeyboardButtonURL{
					Text: userName,
					URL:  hit.MessageLink(),
				},
			),
		).Description(hit.Formatted.Message))
	}
	if len(results) == 0 {
		results = append(results, inline.Article(
			"No Results", inline.MessageText(fmt.Sprintf("No results found for query '%s'", query)),
		).Description("Try different keywords"))
	}
	_, err = ctx.Sender.Inline(update.InlineQuery).Private(true).
		Set(ctx, results...)
	return err
}
