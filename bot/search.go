package bot

import (
	"bytes"
	"errors"
	"strconv"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/gotd/td/telegram/message/entity"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/tg"
	"github.com/krau/btts/database"
	"github.com/krau/btts/types"
	"github.com/krau/btts/utils"
	"github.com/krau/btts/utils/cache"
	"github.com/krau/mygotg/dispatcher"
	"github.com/krau/mygotg/ext"
)

func replyPhoto(update *ext.Update) (*tg.MessageMediaPhoto, bool) {
	if update.EffectiveMessage == nil || update.EffectiveMessage.ReplyToMessage == nil {
		return nil, false
	}
	media, ok := update.EffectiveMessage.ReplyToMessage.GetMedia()
	if !ok {
		return nil, false
	}
	photo, ok := media.(*tg.MessageMediaPhoto)
	return photo, ok
}

func currentPhoto(update *ext.Update) (*tg.MessageMediaPhoto, bool) {
	if update.EffectiveMessage == nil {
		return nil, false
	}
	media, ok := update.EffectiveMessage.GetMedia()
	if !ok {
		return nil, false
	}
	photo, ok := media.(*tg.MessageMediaPhoto)
	return photo, ok
}

func imageSearchWait(ctx *ext.Context, update *ext.Update) int {
	msg, err := ctx.Reply(update, ext.ReplyTextString("🔎 正在以图搜图，请稍候..."), nil)
	if err != nil {
		log.FromContext(ctx).Debugf("Send image search wait message: %v", err)
		return 0
	}
	if msg == nil {
		return 0
	}
	return msg.ID
}

func editImageSearchText(ctx *ext.Context, update *ext.Update, messageID int, text string) bool {
	if messageID == 0 {
		return false
	}
	_, err := ctx.EditMessage(update.EffectiveChat().GetID(), &tg.MessagesEditMessageRequest{
		ID:      messageID,
		Message: text,
	})
	if err != nil {
		log.FromContext(ctx).Debugf("Edit image search wait message: %v", err)
		return false
	}
	return true
}

func editImageSearchResults(ctx *ext.Context, update *ext.Update, messageID int, req types.SearchRequest, resp *types.SearchResponse) bool {
	if messageID == 0 {
		return false
	}
	markup, err := utils.BuildSearchReplyMarkup(ctx, 1, req)
	if err != nil {
		log.FromContext(ctx).Errorf("Failed to build reply markup: %v", err)
		return false
	}
	eb := entity.Builder{}
	if err := styling.Perform(&eb, utils.BuildResultStyling(ctx, resp, bi.GetUsername())...); err != nil {
		log.FromContext(ctx).Errorf("Failed to build image search results: %v", err)
		return false
	}
	text, entities := eb.Complete()
	editReq := &tg.MessagesEditMessageRequest{ID: messageID}
	editReq.SetMessage(text)
	editReq.SetEntities(entities)
	editReq.SetReplyMarkup(markup)
	if _, err := ctx.EditMessage(update.EffectiveChat().GetID(), editReq); err != nil {
		log.FromContext(ctx).Errorf("Edit image search results: %v", err)
		return false
	}
	return true
}

func replyImageSearchText(ctx *ext.Context, update *ext.Update, messageID int, text string) {
	if editImageSearchText(ctx, update, messageID, text) {
		return
	}
	if messageID != 0 {
		_ = ctx.DeleteMessages(update.EffectiveChat().GetID(), []int{messageID})
	}
	ctx.Reply(update, ext.ReplyTextString(text), nil)
}

func searchByPhoto(ctx *ext.Context, update *ext.Update, photo *tg.MessageMediaPhoto, req types.SearchRequest) bool {
	if !bi.Engine.Capabilities().ImageSearch {
		ctx.Reply(update, ext.ReplyTextString("Image search is not enabled"), nil)
		return true
	}
	waitMessageID := imageSearchWait(ctx, update)
	var buf bytes.Buffer
	if _, err := ctx.DownloadMedia(photo, ext.DownloadOutputStream{Writer: &buf}, nil); err != nil {
		log.FromContext(ctx).Errorf("Failed to download query photo: %v", err)
		replyImageSearchText(ctx, update, waitMessageID, "Failed to read the query photo")
		return true
	}
	req.ImageData = buf.Bytes()
	resp, err := bi.Engine.SearchImage(ctx, req, req.ImageData)
	if err != nil {
		log.FromContext(ctx).Errorf("Image search failed: %v", err)
		replyImageSearchText(ctx, update, waitMessageID, "Image search is not available: "+err.Error())
		return true
	}
	if resp == nil || len(resp.Hits) == 0 {
		replyImageSearchText(ctx, update, waitMessageID, "No results found")
		return true
	}
	if editImageSearchResults(ctx, update, waitMessageID, req, resp) {
		return true
	}
	if waitMessageID != 0 {
		_ = ctx.DeleteMessages(update.EffectiveChat().GetID(), []int{waitMessageID})
	}
	if err := replySearchResults(ctx, update, req, resp); err != nil && !errors.Is(err, dispatcher.EndGroups) {
		log.FromContext(ctx).Errorf("Reply image search results: %v", err)
	}
	return true
}

func searchByReplyPhoto(ctx *ext.Context, update *ext.Update, req types.SearchRequest) bool {
	photo, ok := replyPhoto(update)
	if !ok {
		return false
	}
	return searchByPhoto(ctx, update, photo, req)
}

func executeSearch(ctx *ext.Context, req types.SearchRequest) (*types.SearchResponse, error) {
	switch {
	case len(req.ImageData) > 0:
		return bi.Engine.SearchImage(ctx, req, req.ImageData)
	case len(req.AudioData) > 0:
		return bi.Engine.SearchAudio(ctx, req, req.AudioData)
	default:
		return bi.Engine.Search(ctx, req)
	}
}

func replySearchResults(ctx *ext.Context, update *ext.Update, req types.SearchRequest, resp *types.SearchResponse) error {
	if resp == nil || len(resp.Hits) == 0 {
		ctx.Reply(update, ext.ReplyTextString("No results found"), nil)
		return dispatcher.EndGroups
	}
	markup, err := utils.BuildSearchReplyMarkup(ctx, 1, req)
	if err != nil {
		log.FromContext(ctx).Errorf("Failed to build reply markup: %v", err)
		return dispatcher.EndGroups
	}
	ctx.Reply(update, ext.ReplyTextStyledTextArray(utils.BuildResultStyling(ctx, resp, bi.GetUsername())), &ext.ReplyOpts{
		Markup: markup,
	})
	return dispatcher.EndGroups
}

func SearchHandler(ctx *ext.Context, update *ext.Update) error {
	query := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(update.EffectiveMessage.GetMessage(), "/search"), "@"+ctx.Self.Username))
	photo, hasCurrentPhoto := currentPhoto(update)
	_, hasReplyPhoto := replyPhoto(update)
	if query == "" && !hasCurrentPhoto && !hasReplyPhoto {
		ctx.Reply(update, ext.ReplyTextString("Usage: Send a photo or query in PM, use /search <query> in group, or reply to a photo with /search"), nil)
		return dispatcher.EndGroups
	}
	isChannel := update.GetChannel() != nil

	if isChannel {
		// 在频道中时，只搜索当前频道
		channelID := update.GetChannel().GetID()
		if _, err := database.GetIndexChat(ctx, channelID); err != nil {
			ctx.Reply(update, ext.ReplyTextString("This chat is not indexed"), nil)
			return dispatcher.EndGroups
		}

		req := types.SearchRequest{
			ChatID: channelID,
			Query:  query,
		}
		if hasCurrentPhoto {
			if searchByPhoto(ctx, update, photo, req) {
				return dispatcher.EndGroups
			}
		}
		if query == "" {
			if searchByReplyPhoto(ctx, update, req) {
				return dispatcher.EndGroups
			}
		}
		resp, err := bi.Engine.Search(ctx, req)
		if err != nil {
			ctx.Reply(update, ext.ReplyTextString("Error: "+err.Error()), nil)
			return dispatcher.EndGroups
		}
		return replySearchResults(ctx, update, req, resp)
	}
	req := &types.SearchRequest{Query: query}

	var chats []*database.IndexChat
	if rm := update.EffectiveMessage.ReplyToMessage; rm != nil {
		if selectChatID, ok := cache.Get[int64](strconv.Itoa(rm.GetID())); ok {
			selectChat, err := database.GetIndexChat(ctx, selectChatID)
			if err != nil {
				ctx.Reply(update, ext.ReplyTextString("Failed to get chat"), nil)
				return dispatcher.EndGroups
			}
			chats = append(chats, selectChat)
		}
	}

	if len(chats) == 0 {
		if CheckPermission(ctx, update) {
			req.AllChats = true
		} else {
			var err error
			chats, err = database.GetAllPublicIndexChats(ctx)
			if err != nil {
				log.FromContext(ctx).Errorf("Failed to get index chats: %v", err)
				ctx.Reply(update, ext.ReplyTextString("Error Happened"), nil)
				return dispatcher.EndGroups
			}
		}
	}

	if len(chats) == 0 && !req.AllChats {
		ctx.Reply(update, ext.ReplyTextString("No index chats found"), nil)
		return dispatcher.EndGroups
	}

	switch {
	case !req.AllChats && len(chats) == 1:
		req.ChatID = chats[0].ChatID
	case !req.AllChats && len(chats) > 1:
		req.ChatIDs = make([]int64, 0, len(chats))
		for _, chat := range chats {
			req.ChatIDs = append(req.ChatIDs, chat.ChatID)
		}
	}
	if hasCurrentPhoto {
		if searchByPhoto(ctx, update, photo, *req) {
			return dispatcher.EndGroups
		}
	}
	if query == "" {
		if searchByReplyPhoto(ctx, update, *req) {
			return dispatcher.EndGroups
		}
	}
	resp, err := bi.Engine.Search(ctx, *req)
	if err != nil {
		log.FromContext(ctx).Errorf("Failed to search: %v", err)
		ctx.Reply(update, ext.ReplyTextString("Error Happened"), nil)
		return dispatcher.EndGroups
	}
	return replySearchResults(ctx, update, *req, resp)
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
	offset := (page - 1) * types.PerSearchLimit
	data.Offset = offset
	resp, err := executeSearch(ctx, data)
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
	if err := styling.Perform(&eb, utils.BuildResultStyling(ctx, resp, bi.GetUsername())...); err != nil {
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
	resp, err := executeSearch(ctx, data)
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
	if err := styling.Perform(&eb, utils.BuildResultStyling(ctx, resp, bi.GetUsername())...); err != nil {
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
