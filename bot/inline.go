package bot

import (
	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/duke-git/lancet/v2/slice"
	"github.com/gotd/td/telegram/message/inline"
	"github.com/gotd/td/tg"
	"github.com/krau/btts/config"
	"github.com/krau/btts/database"
	"github.com/krau/btts/types"
)

func InlineQueryHandler(ctx *ext.Context, update *ext.Update) error {
	userID := update.InlineQuery.GetUserID()
	if userID == bi.UserClient.TClient.Self.ID {
		return dispatcher.EndGroups
	}
	if slice.Contain(config.C.Admins, userID) {
		return dispatcher.EndGroups
	}

	query := update.InlineQuery.GetQuery()
	resp, err := bi.Engine.Search(ctx,
		types.SearchRequest{Query: query,
			ChatIDs:     database.AllChatIDs(),
			TypeFilters: []types.MessageType{types.MessageTypeText}})
	if err != nil {
		return err
	}
	results := make([]inline.ResultOption, 0, len(resp.Hits))
	for _, hit := range resp.Hits {
		title := hit.Formatted.UserID
		user, err := database.GetUserInfo(ctx, hit.UserID)
		if err == nil {
			title = user.FullName()
		}
		results = append(results, inline.Article(
			title, inline.MessageText(hit.Formatted.Message).Row(
				&tg.KeyboardButtonURL{
					Text: title,
					URL:  hit.MessageLink(),
				},
			),
		))
	}
	_, err = ctx.Sender.Inline(update.InlineQuery).Private(true).
		Set(ctx, results...)
	return err
}
