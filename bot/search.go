package bot

import (
	"strconv"
	"strings"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/gotd/td/telegram/message/entity"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/tg"
	"github.com/krau/btts/database"
	"github.com/krau/btts/types"
	"github.com/krau/btts/utils"
	"github.com/krau/btts/utils/cache"
)

func SearchHandler(ctx *ext.Context, update *ext.Update) error {
	query := strings.TrimPrefix(strings.TrimPrefix(update.EffectiveMessage.GetMessage(), "/search"), "@"+ctx.Self.Username)
	if query == "" {
		ctx.Reply(update, ext.ReplyTextString("Usage: Send query in PM, or use /search <query> in group"), nil)
		return dispatcher.EndGroups
	}
	isChannel := false
	if update.GetChannel() != nil {
		isChannel = true
	}
	if isChannel {
		channelID := update.GetChannel().GetID()
		if _, err := database.GetIndexChat(ctx, channelID); err != nil {
			ctx.Reply(update, ext.ReplyTextString("This chat is not indexed"), nil)
			return dispatcher.EndGroups
		}

		resp, err := BotInstance.Engine.Search(ctx, types.SearchRequest{
			ChatID: channelID,
			Query:  query,
		})
		if err != nil {
			ctx.Reply(update, ext.ReplyTextString("Error: "+err.Error()), nil)
			return dispatcher.EndGroups
		}
		if len(resp.Hits) == 0 {
			ctx.Reply(update, ext.ReplyTextString("No results found"), nil)
			return dispatcher.EndGroups
		}
		markup, err := utils.BuildSearchReplyMarkup(ctx, 1, types.SearchRequest{
			ChatID: channelID,
			Query:  query,
		})
		if err != nil {
			log.FromContext(ctx).Errorf("Failed to build reply markup: %v", err)
			return dispatcher.EndGroups
		}
		ctx.Reply(update, ext.ReplyTextStyledTextArray(utils.BuildResultStyling(ctx, resp)), &ext.ReplyOpts{
			Markup: markup})
		return dispatcher.EndGroups
	}
	var err error
	var chats []*database.IndexChat
	if CheckPermission(ctx, update) {
		chats, err = database.GetAllIndexChats(ctx)
	} else {
		chats, err = database.GetAllPublicIndexChats(ctx)
	}
	if err != nil {
		log.FromContext(ctx).Errorf("Failed to get index chats: %v", err)
		ctx.Reply(update, ext.ReplyTextString("Error Happened"), nil)
		return dispatcher.EndGroups
	}
	if len(chats) == 0 {
		ctx.Reply(update, ext.ReplyTextString("No index chats found"), nil)
		return dispatcher.EndGroups
	}
	chatIDs := make([]int64, len(chats))
	for i, chat := range chats {
		chatIDs[i] = chat.ChatID
	}
	resp, err := BotInstance.Engine.Search(ctx, types.SearchRequest{
		ChatIDs: chatIDs,
		Query:   query,
	})
	if err != nil {
		log.FromContext(ctx).Errorf("Failed to search: %v", err)
		ctx.Reply(update, ext.ReplyTextString("Error Happened"), nil)
		return dispatcher.EndGroups
	}
	if len(resp.Hits) == 0 {
		ctx.Reply(update, ext.ReplyTextString("No results found"), nil)
		return dispatcher.EndGroups
	}
	markup, err := utils.BuildSearchReplyMarkup(ctx, 1, types.SearchRequest{
		ChatIDs: chatIDs,
		Query:   query,
	})
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
	offset := (page - 1) * types.PER_SEARCH_LIMIT
	data.Offset = offset
	resp, err := BotInstance.Engine.Search(ctx, data)
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
	resp, err := BotInstance.Engine.Search(ctx, data)
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
	cache.Set(dataid, data)
	return dispatcher.EndGroups
}
