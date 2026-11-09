package subbot

import (
	"bytes"
	"errors"

	"github.com/charmbracelet/log"
	"github.com/gotd/td/telegram/message/entity"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/tg"
	"github.com/krau/btts/engine"
	"github.com/krau/btts/types"
	"github.com/krau/btts/utils"
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

func executeSearch(ctx *ext.Context, req types.SearchRequest) (*types.SearchResponse, error) {
	searcher := engine.GetEngine()
	switch {
	case len(req.ImageData) > 0:
		return searcher.SearchImage(ctx, req, req.ImageData)
	case len(req.AudioData) > 0:
		return searcher.SearchAudio(ctx, req, req.AudioData)
	default:
		return searcher.Search(ctx, req)
	}
}

func replySearchResults(ctx *ext.Context, update *ext.Update, req types.SearchRequest, resp *types.SearchResponse) {
	if resp == nil || len(resp.Hits) == 0 {
		ctx.Reply(update, ext.ReplyTextString("No results found"), nil)
		return
	}
	markup, err := utils.BuildSearchReplyMarkup(ctx, 1, req)
	if err != nil {
		log.FromContext(ctx).Errorf("Failed to build reply markup: %v", err)
		return
	}
	if _, err := ctx.Reply(update, ext.ReplyTextStyledTextArray(utils.BuildResultStyling(ctx, resp)), &ext.ReplyOpts{
		Markup: markup,
	}); err != nil && !errors.Is(err, dispatcher.EndGroups) {
		log.FromContext(ctx).Errorf("Reply search results: %v", err)
	}
}

func imageSearchWait(ctx *ext.Context, update *ext.Update) int {
	msg, err := ctx.Reply(update, ext.ReplyTextString("🔎 正在搜索，请稍候..."), nil)
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
	if _, err := ctx.EditMessage(update.EffectiveChat().GetID(), &tg.MessagesEditMessageRequest{
		ID:      messageID,
		Message: text,
	}); err != nil {
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
	if err := styling.Perform(&eb, utils.BuildResultStyling(ctx, resp)...); err != nil {
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
	searcher := engine.GetEngine()
	if !searcher.Capabilities().ImageSearch {
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
	resp, err := searcher.SearchImage(ctx, req, req.ImageData)
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
	replySearchResults(ctx, update, req, resp)
	return true
}

func searchByReplyPhoto(ctx *ext.Context, update *ext.Update, req types.SearchRequest) bool {
	photo, ok := replyPhoto(update)
	if !ok {
		return false
	}
	return searchByPhoto(ctx, update, photo, req)
}
