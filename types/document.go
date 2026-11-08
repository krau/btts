package types

import "github.com/gotd/td/tg"

// IsStickerDocument reports whether a document is a sticker of any kind,
// including masks and custom emoji. Animated GIFs also carry
// DocumentAttributeAnimated, but are not stickers.
func IsStickerDocument(attributes []tg.DocumentAttributeClass) bool {
	for _, attribute := range attributes {
		switch attribute.(type) {
		case *tg.DocumentAttributeSticker, *tg.DocumentAttributeCustomEmoji:
			return true
		}
	}
	return false
}
