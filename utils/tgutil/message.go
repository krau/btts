package tgutil

import (
	"strings"

	"github.com/gotd/td/tg"
	"github.com/krau/btts/types"
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
			case *tg.DocumentAttributeFilename:
				messageSB.WriteString(attr.GetFileName() + " ")
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
