package types

import (
	"fmt"
	"strings"
)

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
	PerSearchLimit = 12
)

type MessageDocument struct {
	// Telegram message ID
	ID   int64 `json:"id"`
	Type int   `json:"type"`
	// The original text of the message
	Message string `json:"message"`
	// The OCRed text of the message
	Ocred string `json:"ocred"`
	// [TODO] The AI generated text of the message(summarization, caption, tagging, etc.)
	AIGenerated string `json:"aigenerated"`
	// The ID of the user who sent the message
	UserID    int64 `json:"user_id"`
	ChatID    int64 `json:"chat_id"`
	Timestamp int64 `json:"timestamp"`
}

type SearchHit struct {
	MessageDocument
	Formatted SearchHitFormatted `json:"_formatted"`
}

type SearchHitFormatted struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Message     string `json:"message"`
	Ocred       string `json:"ocred"`
	AIGenerated string `json:"aigenerated"`
	UserID      string `json:"user_id"`
	ChatID      string `json:"chat_id"`
	Timestamp   string `json:"timestamp"`
}

func (s SearchHit) MessageLink() string {
	return fmt.Sprintf("https://t.me/c/%d/%d", s.ChatID, s.ID)
}

func (s SearchHit) FullFormattedText() string {
	return strings.TrimSpace(s.Formatted.Message + " " + s.Formatted.Ocred + " " + s.Formatted.AIGenerated)
}

func (s SearchHit) FullText() string {
	return strings.TrimSpace(s.Message + " " + s.Ocred + " " + s.AIGenerated)
}

type SearchResponse struct {
	Hits               []SearchHit `json:"hits,omitempty"`
	ProcessingTimeMs   int64       `json:"processingTimeMs,omitempty"`
	Offset             int64       `json:"offset,omitempty"`
	Limit              int64       `json:"limit,omitempty"`
	EstimatedTotalHits int64       `json:"estimatedTotalHits,omitempty"`
	SemanticHitCount   int64       `json:"semanticHitCount,omitempty"`
	Raw                any
}
