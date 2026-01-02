package types

import "fmt"

type MessageType int

const (
	MessageTypeText MessageType = iota
	MessageTypePhoto
	MessageTypeVideo
	MessageTypeDocument
	MessageTypeVoice
	MessageTypeAudio
	MessageTypePoll
	MessageTypeStory
)

var MessageTypeToEmoji = map[MessageType]string{
	MessageTypeText:     "💬",
	MessageTypePhoto:    "🖼️",
	MessageTypeVideo:    "🎥",
	MessageTypeDocument: "📄",
	MessageTypeVoice:    "🎙️",
	MessageTypeAudio:    "🎵",
	MessageTypePoll:     "📊",
	MessageTypeStory:    "🪟",
}

var MessageTypeToString = map[MessageType]string{
	MessageTypeText:     "text",
	MessageTypePhoto:    "photo",
	MessageTypeVideo:    "video",
	MessageTypeDocument: "document",
	MessageTypeVoice:    "voice",
	MessageTypeAudio:    "audio",
	MessageTypePoll:     "poll",
	MessageTypeStory:    "story",
}

var MessageTypeToDisplayString = map[MessageType]string{
	MessageTypeText:     "文本",
	MessageTypePhoto:    "图片",
	MessageTypeVideo:    "视频",
	MessageTypeDocument: "文件",
	MessageTypeVoice:    "语音",
	MessageTypeAudio:    "音频",
	MessageTypePoll:     "投票",
	MessageTypeStory:    "动态",
}

var MessageTypeFromString = map[string]MessageType{
	"text":     MessageTypeText,
	"photo":    MessageTypePhoto,
	"video":    MessageTypeVideo,
	"document": MessageTypeDocument,
	"voice":    MessageTypeVoice,
	"audio":    MessageTypeAudio,
	"poll":     MessageTypePoll,
	"story":    MessageTypeStory,
}

var (
	StickerFileNames = []string{"sticker.webp", "sticker.webm"}
)

const (
	PER_SEARCH_LIMIT = 12
)

type MessageDocument struct {
	// Telegram MessageID
	ID   int64 `json:"id"`
	Type int   `json:"type"`
	// The original text of the message
	Message string `json:"message"`
	// The ID of the user who sent the message
	UserID    int64 `json:"user_id"`
	ChatID    int64 `json:"chat_id"`
	Timestamp int64 `json:"timestamp"`
}

type SearchHit struct {
	MessageDocument
	Formatted struct {
		ID        string `json:"id"`
		Type      string `json:"type"`
		Message   string `json:"message"`
		UserID    string `json:"user_id"`
		ChatID    string `json:"chat_id"`
		Timestamp string `json:"timestamp"`
	} `json:"_formatted"`
}

func (s SearchHit) MessageLink() string {
	return fmt.Sprintf("https://t.me/c/%d/%d", s.ChatID, s.ID)
}

type MessageSearchResponse struct {
	Hits               []SearchHit `json:"hits,omitempty"`
	ProcessingTimeMs   int64       `json:"processingTimeMs,omitempty"`
	Offset             int64       `json:"offset,omitempty"`
	Limit              int64       `json:"limit,omitempty"`
	EstimatedTotalHits int64       `json:"estimatedTotalHits,omitempty"`
	SemanticHitCount   int64       `json:"semanticHitCount,omitempty"`
	Raw                any
}
