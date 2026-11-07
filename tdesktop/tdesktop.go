// Package tdesktop reads the JSON export produced by Telegram Desktop.
package tdesktop

import (
	"encoding/json"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/krau/btts/types"
)

// Export is either a single-chat result.json or a full-account export.
type Export struct {
	Chat
	Chats json.RawMessage `json:"chats"`
}

// Chat is one exported conversation.
type Chat struct {
	ID       int64     `json:"id"`
	Name     string    `json:"name"`
	Type     string    `json:"type"`
	Messages []Message `json:"messages"`
}

// Message is one message in a Telegram Desktop JSON export.
type Message struct {
	ID           int             `json:"id"`
	Type         string          `json:"type"`
	Date         string          `json:"date"`
	DateUnix     string          `json:"date_unixtime"`
	From         string          `json:"from"`
	FromID       string          `json:"from_id"`
	Text         json.RawMessage `json:"text"`
	TextEntities []TextEntity    `json:"text_entities"`
	Photo        string          `json:"photo"`
	File         string          `json:"file"`
	MediaType    string          `json:"media_type"`
	MimeType     string          `json:"mime_type"`
	Title        string          `json:"title"`
	Performer    string          `json:"performer"`
	Duration     int             `json:"duration_seconds"`
	ReplyTo      int             `json:"reply_to_message_id"`
}

// TextEntity is a formatted piece of message text.
type TextEntity struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Load reads result.json, or a directory containing it, into chats.
func Load(path string) ([]Chat, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		path = filepath.Join(path, "result.json")
	}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("result.json not found; export the history as JSON: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var root Export
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("decode Telegram Desktop export: %w", err)
	}

	chats := make([]Chat, 0, 2)
	if root.ID != 0 || len(root.Messages) > 0 {
		chats = append(chats, root.Chat)
	}
	chats = append(chats, decodeChatList(root.Chats)...)
	if len(chats) == 0 {
		return nil, fmt.Errorf("no chats found in %s", path)
	}
	return chats, nil
}

func decodeChatList(raw json.RawMessage) []Chat {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var wrapped struct {
		List []Chat `json:"list"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil && len(wrapped.List) > 0 {
		return wrapped.List
	}
	var list []Chat
	if err := json.Unmarshal(raw, &list); err == nil {
		return list
	}
	return nil
}

// NameForDisplay returns a fallback title for nameless exported chats.
func (c Chat) NameForDisplay() string {
	if strings.TrimSpace(c.Name) != "" {
		return c.Name
	}
	switch c.Type {
	case "saved_messages":
		return "Saved Messages"
	case "replies":
		return "Replies"
	default:
		return fmt.Sprintf("Chat %d", c.ID)
	}
}

// PlainText returns the message text with formatting entities flattened.
func (m Message) PlainText() string {
	if len(m.Text) > 0 && string(m.Text) != "null" {
		var text string
		if err := json.Unmarshal(m.Text, &text); err == nil {
			return text
		}
		var parts []any
		if err := json.Unmarshal(m.Text, &parts); err == nil {
			var b strings.Builder
			for _, part := range parts {
				switch p := part.(type) {
				case string:
					b.WriteString(p)
				case map[string]any:
					if s, ok := p["text"].(string); ok {
						b.WriteString(s)
					}
				}
			}
			return b.String()
		}
	}
	if len(m.TextEntities) > 0 {
		var b strings.Builder
		for _, entity := range m.TextEntities {
			b.WriteString(entity.Text)
		}
		return b.String()
	}
	return ""
}

// Timestamp returns date_unixtime, falling back to the ISO date field.
func (m Message) Timestamp() int64 {
	if m.DateUnix != "" {
		if value, err := strconv.ParseInt(strings.TrimSpace(m.DateUnix), 10, 64); err == nil {
			return value
		}
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05"} {
		if value, err := time.Parse(layout, m.Date); err == nil {
			return value.Unix()
		}
	}
	return 0
}

// FromUserID returns the numeric id for from_id values like "user123".
func (m Message) FromUserID() (int64, bool) {
	fromID := strings.ToLower(strings.TrimSpace(m.FromID))
	if !strings.HasPrefix(fromID, "user") {
		return 0, false
	}
	digits := strings.TrimLeftFunc(fromID, func(r rune) bool {
		return r < '0' || r > '9'
	})
	value, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || value == 0 {
		return 0, false
	}
	return value, true
}

// UserID parses from_id ("user123", "channel123", "chat123"). It returns
// fallback when no sender id is present.
func (m Message) UserID(fallback int64) int64 {
	if m.FromID == "" {
		return fallback
	}
	digits := strings.TrimLeftFunc(m.FromID, func(r rune) bool {
		return r < '0' || r > '9'
	})
	if value, err := strconv.ParseInt(digits, 10, 64); err == nil {
		return value
	}
	return fallback
}

// Media returns the relative media path, MIME type, filename, display type,
// and whether the message has a supported media attachment.
func (m Message) Media() (path, mimeType, name string, msgType types.MessageType, ok bool) {
	mediaType := strings.ToLower(strings.TrimSpace(m.MediaType))
	if mediaType == "sticker" {
		return "", "", "", types.MessageTypeText, false
	}

	switch mediaType {
	case "video_file", "video_message", "animation":
		path, msgType = m.File, types.MessageTypeVideo
	case "voice_message":
		path, msgType = m.File, types.MessageTypeVoice
	case "audio_file":
		path, msgType = m.File, types.MessageTypeAudio
	}
	if m.Photo != "" {
		path, msgType = m.Photo, types.MessageTypePhoto
	}
	if path == "" && m.File != "" {
		path, msgType = m.File, types.MessageTypeDocument
	}
	if path == "" {
		return "", "", "", types.MessageTypeText, false
	}

	mimeType = m.MimeType
	if mimeType == "" {
		mimeType = mimeForPath(path, mediaType)
	}
	mimeType = strings.TrimSpace(strings.Split(mimeType, ";")[0])
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		msgType = types.MessageTypePhoto
	case strings.HasPrefix(mimeType, "video/"):
		msgType = types.MessageTypeVideo
	case strings.HasPrefix(mimeType, "audio/"):
		if msgType != types.MessageTypeVoice {
			msgType = types.MessageTypeAudio
		}
	case mimeType == "application/pdf":
		msgType = types.MessageTypeDocument
	}
	name = filepath.Base(strings.ReplaceAll(path, "\\", "/"))
	return path, mimeType, name, msgType, true
}

func mimeForPath(path, mediaType string) string {
	switch mediaType {
	case "photo":
		return "image/jpeg"
	case "voice_message":
		return "audio/ogg"
	}
	ext := strings.ToLower(filepath.Ext(strings.ReplaceAll(path, "\\", "/")))
	if value := mime.TypeByExtension(ext); value != "" {
		return value
	}
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".mp4":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	case ".webm":
		return "video/webm"
	case ".ogg", ".opus":
		return "audio/ogg"
	case ".mp3":
		return "audio/mpeg"
	case ".m4a":
		return "audio/mp4"
	case ".pdf":
		return "application/pdf"
	default:
		return ""
	}
}
