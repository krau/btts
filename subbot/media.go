package subbot

import (
	"bytes"
	"errors"

	"github.com/charmbracelet/log"
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

func searchByPhoto(ctx *ext.Context, update *ext.Update, photo *tg.MessageMediaPhoto, req types.SearchRequest) bool {
	searcher := engine.GetEngine()
	if !searcher.Capabilities().ImageSearch {
		ctx.Reply(update, ext.ReplyTextString("Image search is not enabled"), nil)
		return true
	}
	var buf bytes.Buffer
	if _, err := ctx.DownloadMedia(photo, ext.DownloadOutputStream{Writer: &buf}, nil); err != nil {
		log.FromContext(ctx).Errorf("Failed to download query photo: %v", err)
		ctx.Reply(update, ext.ReplyTextString("Failed to read the query photo"), nil)
		return true
	}
	req.ImageData = buf.Bytes()
	resp, err := searcher.SearchImage(ctx, req, req.ImageData)
	if err != nil {
		log.FromContext(ctx).Errorf("Image search failed: %v", err)
		ctx.Reply(update, ext.ReplyTextString("Image search is not available: "+err.Error()), nil)
		return true
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
