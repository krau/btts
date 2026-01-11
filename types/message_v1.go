package types

import "fmt"

type MessageDocumentV1 struct {
	// Cantor paired ID of (chat_id, message_id)
	// [NOTE] Cantor 需要两个非负整数
	ID   int64 `json:"id"`
	Type int   `json:"type"`
	// The original text of the message
	Message string `json:"message"`
	// The OCRed text of the message
	Ocred string `json:"ocred"`
	// The AI generated text of the message(summarization, caption, tagging, etc.)
	AIGenerated string `json:"aigenerated"`
	// The ID of the user who sent the message
	UserID int64 `json:"user_id"`
	ChatID int64 `json:"chat_id"`
	// Telegram MessageID
	MessageID int64 `json:"message_id"`
	Timestamp int64 `json:"timestamp"`
}

type SearchHitV1 struct {
	MessageDocument
	Formatted struct {
		ID        string `json:"id"`
		Type      string `json:"type"`
		Message   string `json:"message"`
		Ocred     string `json:"ocred"`
		UserID    string `json:"user_id"`
		ChatID    string `json:"chat_id"`
		MessageID string `json:"message_id"`
		Timestamp string `json:"timestamp"`
	} `json:"_formatted"`
}

func (s SearchHitV1) MessageLink() string {
	return fmt.Sprintf("https://t.me/c/%d/%d", s.ChatID, s.ID)
}

type MessageSearchResponseV1 struct {
	Hits               []SearchHitV1 `json:"hits,omitempty"`
	ProcessingTimeMs   int64         `json:"processingTimeMs,omitempty"`
	Offset             int64         `json:"offset,omitempty"`
	Limit              int64         `json:"limit,omitempty"`
	EstimatedTotalHits int64         `json:"estimatedTotalHits,omitempty"`
	SemanticHitCount   int64         `json:"semanticHitCount,omitempty"`
	Raw                any
}
