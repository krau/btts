package subbot

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/duke-git/lancet/v2/slice"
	"github.com/gotd/td/telegram/message/entity"
	"github.com/gotd/td/telegram/message/inline"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/tg"
	"github.com/krau/btts/config"
	"github.com/krau/btts/database"
	"github.com/krau/btts/engine"
	"github.com/krau/btts/types"
	"github.com/krau/btts/userclient"
	"github.com/krau/btts/utils"
	"github.com/krau/btts/utils/cache"
)

func CheckAdmin(ctx *ext.Context, update *ext.Update) bool {
	userID := update.GetUserChat().GetID()
	if userID == userclient.GetUserClient().TClient.Self.ID {
		return true
	}
	if slice.Contain(config.C.Admins, userID) {
		return true
	}
	return false
}

func StartHandler(ctx *ext.Context, update *ext.Update) error {
	myChats := make([]*database.IndexChat, 0)
	sb, err := database.GetSubBot(ctx, ctx.Self.ID)
	if err != nil {
		log.FromContext(ctx).Errorf("Failed to get sub bot: %v", err)
		return dispatcher.EndGroups
	}
	if len(sb.ChatIDs) == 0 {
		ctx.Reply(update, ext.ReplyTextString("Yet Another Bot For Telegram Search..."), nil)
		return dispatcher.EndGroups
	}
	for _, chatID := range sb.ChatIDs {
		chat, err := database.GetIndexChat(ctx, chatID)
		if err != nil {
			log.FromContext(ctx).Errorf("Failed to get index chat: %v", err)
			continue
		}
		if chat.Public {
			myChats = append(myChats, chat)
		}
	}
	if len(myChats) == 0 {
		ctx.Reply(update, ext.ReplyTextString("Yet Another Bot For Telegram Search..."), nil)
		return dispatcher.EndGroups
	}
	helpTextStyling := make([]styling.StyledTextOption, 0)
	helpTextStyling = append(helpTextStyling, styling.Bold("发送任意搜索词以搜索以下聊天的消息:\n"))
	for _, chat := range myChats {
		if chat.Username != "" {
			helpTextStyling = append(helpTextStyling, styling.TextURL(chat.Title, "https://t.me/"+chat.Username))
		} else {
			helpTextStyling = append(helpTextStyling, styling.TextURL(chat.Title, "https://t.me/c/"+strconv.FormatInt(chat.ChatID, 10)))
		}
		helpTextStyling = append(helpTextStyling, styling.Plain("\n"))
	}
	ctx.Reply(update, ext.ReplyTextStyledTextArray(helpTextStyling), nil)
	return dispatcher.EndGroups
}

func SearchHandler(ctx *ext.Context, update *ext.Update) error {
	query := strings.TrimPrefix(strings.TrimPrefix(update.EffectiveMessage.GetMessage(), "/search"), "@"+ctx.Self.Username)
	if query == "" {
		ctx.Reply(update, ext.ReplyTextString("Usage: Send query in PM, or use /search <query> in group"), nil)
		return dispatcher.EndGroups
	}
	sbModel, err := database.GetSubBot(ctx, ctx.Self.ID)
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString("Error: "+err.Error()), nil)
		return dispatcher.EndGroups
	}
	if len(sbModel.ChatIDs) == 0 {
		ctx.Reply(update, ext.ReplyTextString("This bot not indexed any chats"), nil)
		return dispatcher.EndGroups
	}

	userId := update.GetUserChat().GetID()
	chatIDs := make([]int64, 0)

	isChannel := update.GetChannel() != nil
	var channelID int64
	if isChannel {
		channelID = update.GetChannel().GetID()
	}

	if CheckAdmin(ctx, update) {
		// admin 可以搜索所有聊天
		chatIDs = sbModel.ChatIDs
	} else {
		// 非 admin 只能搜索公开聊天，或者当前频道（群组）聊天
		// 以及用户所在的频道(群组)
		for _, chatID := range sbModel.ChatIDs {
			chat, err := database.GetIndexChat(ctx, chatID)
			if err != nil {
				log.FromContext(ctx).Errorf("Failed to get index chat: %v", err)
				continue
			}
			if chat.Public {
				// 公开聊天
				chatIDs = append(chatIDs, chatID)
			} else if isChannel {
				if chatID == channelID {
					// 当前聊天
					chatIDs = append(chatIDs, chatID)
				}
			} else {
				// 在私聊中, 检测用户是哪些聊天的成员
				isMember, err := database.IsMemberInIndexChat(ctx, chatID, userId)
				if err != nil {
					log.FromContext(ctx).Errorf("Failed to check member in index chat: %v", err)
					continue
				}
				if isMember {
					chatIDs = append(chatIDs, chatID)
				}
			}
		}
	}

	if len(chatIDs) == 0 {
		ctx.Reply(update, ext.ReplyTextString("No indexed chats found"), nil)
		return dispatcher.EndGroups
	}

	// 以防万一去个重...
	chatIDs = slice.Unique(chatIDs)

	req := types.SearchRequest{
		ChatIDs: chatIDs,
		Query:   query,
	}
	resp, err := engine.GetEngine().Search(ctx, req)
	if err != nil {
		log.FromContext(ctx).Errorf("Failed to search: %v", err)
		ctx.Reply(update, ext.ReplyTextString("Failed to search"), nil)
		return dispatcher.EndGroups
	}
	if len(resp.Hits) == 0 {
		ctx.Reply(update, ext.ReplyTextString("No results found"), nil)
		return dispatcher.EndGroups
	}
	markup, err := utils.BuildSearchReplyMarkup(ctx, 1, req)
	if err != nil {
		log.FromContext(ctx).Errorf("Failed to build reply markup: %v", err)
		return dispatcher.EndGroups
	}
	ctx.Reply(update, ext.ReplyTextStyledTextArray(utils.BuildResultStyling(ctx, resp)), &ext.ReplyOpts{
		Markup: markup,
	})
	return dispatcher.EndGroups
}

func SearchCallbackHandler(ctx *ext.Context, update *ext.Update) error {
	args := update.Args()
	cacheid := args[2]
	data, ok := cache.Get[types.SearchRequest](cacheid)
	if !ok {
		ctx.AnswerCallback(&tg.MessagesSetBotCallbackAnswerRequest{
			QueryID:   update.CallbackQuery.GetQueryID(),
			Message:   "查询已过期",
			Alert:     true,
			CacheTime: 60,
		})
		return dispatcher.EndGroups
	}
	sbModel, err := database.GetSubBot(ctx, ctx.Self.ID)
	if err != nil {
		log.FromContext(ctx).Errorf("Failed to get sub bot: %v", err)
		return dispatcher.EndGroups
	}
	if !slice.Equal(data.ChatIDs, sbModel.ChatIDs) {
		ctx.AnswerCallback(&tg.MessagesSetBotCallbackAnswerRequest{
			QueryID:   update.CallbackQuery.GetQueryID(),
			Message:   "Permission Denied",
			Alert:     true,
			CacheTime: 60,
		})
		return dispatcher.EndGroups
	}
	page, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		ctx.AnswerCallback(&tg.MessagesSetBotCallbackAnswerRequest{
			QueryID:   update.CallbackQuery.GetQueryID(),
			Message:   "Invalid page number",
			Alert:     true,
			CacheTime: 60,
		})
		return dispatcher.EndGroups
	}
	if page < 1 {
		ctx.AnswerCallback(&tg.MessagesSetBotCallbackAnswerRequest{
			QueryID:   update.CallbackQuery.GetQueryID(),
			Message:   "没有更多结果了",
			CacheTime: 5,
		})
		return dispatcher.EndGroups
	}
	offset := (page - 1) * types.PerSearchLimit
	data.Offset = offset
	resp, err := engine.GetEngine().Search(ctx, data)
	if err != nil {
		log.FromContext(ctx).Errorf("Failed to search: %v", err)
		ctx.AnswerCallback(&tg.MessagesSetBotCallbackAnswerRequest{
			QueryID:   update.CallbackQuery.GetQueryID(),
			Message:   "Search Error",
			Alert:     true,
			CacheTime: 60,
		})
		return dispatcher.EndGroups
	}
	if len(resp.Hits) == 0 {
		ctx.AnswerCallback(&tg.MessagesSetBotCallbackAnswerRequest{
			QueryID:   update.CallbackQuery.GetQueryID(),
			Message:   "没有更多结果了",
			CacheTime: 5,
		})
		return dispatcher.EndGroups
	}
	eb := entity.Builder{}
	if err := styling.Perform(&eb, utils.BuildResultStyling(ctx, resp)...); err != nil {
		log.FromContext(ctx).Errorf("Failed to build styling: %v", err)
		ctx.AnswerCallback(&tg.MessagesSetBotCallbackAnswerRequest{
			QueryID:   update.CallbackQuery.GetQueryID(),
			Message:   "Styling Error",
			Alert:     true,
			CacheTime: 60,
		})
		return dispatcher.EndGroups
	}
	editReq := &tg.MessagesEditMessageRequest{
		ID: update.CallbackQuery.MsgID,
	}
	text, entities := eb.Complete()
	editReq.SetEntities(entities)
	editReq.SetMessage(text)
	markup, err := utils.BuildSearchReplyMarkup(ctx, page, data)
	if err != nil {
		log.FromContext(ctx).Errorf("Failed to build reply markup: %v", err)
		ctx.AnswerCallback(&tg.MessagesSetBotCallbackAnswerRequest{
			QueryID:   update.CallbackQuery.GetQueryID(),
			Message:   "Failed to build reply markup",
			Alert:     true,
			CacheTime: 60,
		})
		return dispatcher.EndGroups
	}
	editReq.SetReplyMarkup(markup)
	if _, err := ctx.EditMessage(update.EffectiveChat().GetID(), editReq); err != nil {
		log.FromContext(ctx).Errorf("Failed to edit message: %v", err)
	}

	return dispatcher.EndGroups
}

func FilterCallbackHandler(ctx *ext.Context, update *ext.Update) error {
	args := update.Args()
	dataid := args[2]
	data, ok := cache.Get[types.SearchRequest](dataid)
	if !ok {
		ctx.AnswerCallback(&tg.MessagesSetBotCallbackAnswerRequest{
			QueryID:   update.CallbackQuery.GetQueryID(),
			Message:   "查询已过期",
			Alert:     true,
			CacheTime: 60,
		})
		return dispatcher.EndGroups
	}
	sbModel, err := database.GetSubBot(ctx, ctx.Self.ID)
	if err != nil {
		log.FromContext(ctx).Errorf("Failed to get sub bot: %v", err)
		return dispatcher.EndGroups
	}
	if !slice.Equal(data.ChatIDs, sbModel.ChatIDs) {
		ctx.AnswerCallback(&tg.MessagesSetBotCallbackAnswerRequest{
			QueryID:   update.CallbackQuery.GetQueryID(),
			Message:   "Permission Denied",
			Alert:     true,
			CacheTime: 60,
		})
		return dispatcher.EndGroups
	}
	toswitch, err := strconv.Atoi(args[1])
	if err != nil {
		ctx.AnswerCallback(&tg.MessagesSetBotCallbackAnswerRequest{
			QueryID:   update.CallbackQuery.GetQueryID(),
			Message:   "Invalid filter",
			Alert:     true,
			CacheTime: 60,
		})
		return dispatcher.EndGroups
	}
	oldFilter := data.TypeFilters
	if oldFilter == nil {
		oldFilter = make([]types.MessageType, 0)
	}
	newFilter := make([]types.MessageType, 0)
	// 如果已经存在，则删除, 否则添加

	toSwitchType := types.MessageType(toswitch)
	found := false
	for _, filter := range oldFilter {
		if filter == toSwitchType {
			found = true
			continue
		}
		newFilter = append(newFilter, filter)
	}

	if !found {
		newFilter = append(newFilter, toSwitchType)
	}

	data.TypeFilters = newFilter
	// 重新触发搜索, 从第一页开始
	data.Offset = 0
	resp, err := engine.GetEngine().Search(ctx, data)
	if err != nil {
		log.FromContext(ctx).Errorf("Failed to search: %v", err)
		ctx.AnswerCallback(&tg.MessagesSetBotCallbackAnswerRequest{
			QueryID:   update.CallbackQuery.GetQueryID(),
			Message:   "Search Error",
			Alert:     true,
			CacheTime: 60,
		})
		return dispatcher.EndGroups
	}
	if len(resp.Hits) == 0 {
		ctx.AnswerCallback(&tg.MessagesSetBotCallbackAnswerRequest{
			QueryID:   update.CallbackQuery.GetQueryID(),
			Message:   "无结果",
			CacheTime: 5,
		})
		return dispatcher.EndGroups
	}
	eb := entity.Builder{}
	if err := styling.Perform(&eb, utils.BuildResultStyling(ctx, resp)...); err != nil {
		log.FromContext(ctx).Errorf("Failed to build styling: %v", err)
		ctx.AnswerCallback(&tg.MessagesSetBotCallbackAnswerRequest{
			QueryID:   update.CallbackQuery.GetQueryID(),
			Message:   "Styling Error",
			Alert:     true,
			CacheTime: 60,
		})
		return dispatcher.EndGroups
	}
	editReq := &tg.MessagesEditMessageRequest{
		ID: update.CallbackQuery.MsgID,
	}
	text, entities := eb.Complete()
	editReq.SetEntities(entities)
	editReq.SetMessage(text)
	markup, err := utils.BuildSearchReplyMarkup(ctx, 1, data)
	if err != nil {
		log.FromContext(ctx).Errorf("Failed to build reply markup: %v", err)
		ctx.AnswerCallback(&tg.MessagesSetBotCallbackAnswerRequest{
			QueryID:   update.CallbackQuery.GetQueryID(),
			Message:   "Failed to build reply markup",
			Alert:     true,
			CacheTime: 60,
		})
		return dispatcher.EndGroups
	}
	editReq.SetReplyMarkup(markup)
	if _, err := ctx.EditMessage(update.EffectiveChat().GetID(), editReq); err != nil {
		log.FromContext(ctx).Errorf("Failed to edit message: %v", err)
	}
	cache.Set(dataid, data, cache.DefaultTTL)
	return dispatcher.EndGroups
}

func InlineQueryHandler(ctx *ext.Context, update *ext.Update) error {
	userID := update.InlineQuery.GetUserID()
	logger := log.FromContext(ctx)
	// 检查权限
	var chatIds []int64
	sb, err := database.GetSubBot(ctx, ctx.Self.ID)
	if err != nil {
		logger.Errorf("Failed to get sub bot: %v", err)
		return dispatcher.EndGroups
	}
	if userID == userclient.GetUserClient().TClient.Self.ID ||
		slice.Contain(config.C.Admins, userID) {
		// admin
		chatIds = sb.ChatIDs
	} else {
		chatIds = sb.UserCanSearchChats(ctx, userID)
	}
	if len(chatIds) == 0 {
		return dispatcher.EndGroups
	}
	query := update.InlineQuery.GetQuery()
	resp, err := engine.GetEngine().Search(ctx,
		types.SearchRequest{
			Limit:   48,
			Query:   query,
			ChatIDs: chatIds,
		})
	if err != nil {
		logger.Errorf("Failed to search: %v", err)
		return dispatcher.EndGroups
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
