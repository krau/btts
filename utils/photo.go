package utils

import (
	"fmt"

	"github.com/gotd/td/tg"
	"github.com/krau/mygotg/ext"
	"github.com/krau/mygotg/types"
)

// DownloadPhoto downloads the photo attached to the current update, if any.
// A nil slice and nil error mean the update has no photo.
func DownloadPhoto(ctx *ext.Context, update *ext.Update, limit int64) ([]byte, string, error) {
	if update == nil || update.EffectiveMessage == nil {
		return nil, "", nil
	}
	return downloadPhotoMessage(ctx, update.EffectiveMessage, limit)
}

// DownloadQuestionPhoto prefers the current message's photo, then falls back
// to the photo of the message it replies to. It is used by /ask.
func DownloadQuestionPhoto(ctx *ext.Context, update *ext.Update, limit int64) ([]byte, string, error) {
	data, mime, err := DownloadPhoto(ctx, update, limit)
	if err != nil || len(data) > 0 {
		return data, mime, err
	}
	if update == nil || update.EffectiveMessage == nil || update.EffectiveMessage.ReplyToMessage == nil {
		return nil, "", nil
	}
	return downloadPhotoMessage(ctx, update.EffectiveMessage.ReplyToMessage, limit)
}

func downloadPhotoMessage(ctx *ext.Context, message *types.Message, limit int64) ([]byte, string, error) {
	if message == nil {
		return nil, "", nil
	}
	media, ok := message.GetMedia()
	if !ok {
		return nil, "", nil
	}
	photo, ok := media.(*tg.MessageMediaPhoto)
	if !ok {
		return nil, "", nil
	}
	buf := &CappedBuffer{Limit: limit}
	if _, err := ctx.DownloadMedia(photo, ext.DownloadOutputStream{Writer: buf}, nil); err != nil {
		return nil, "", fmt.Errorf("download photo: %w", err)
	}
	return buf.Bytes(), "image/jpeg", nil
}
