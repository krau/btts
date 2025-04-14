package types

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

var (
	StickerFileNames = []string{"sticker.webp", "sticker.webm"}
)

type MessageDocument struct {
	// Telegram MessageID
	ID   int64 `json:"id"`
	Type int   `json:"type"`
	// The original text of the message
	Message string `json:"message"`
	// The ID of the user who sent the message
	UserID    int64 `json:"user_id"`
	Timestamp int64 `json:"timestamp"`
}

type SearchHit struct {
	MessageDocument
	Formatted struct {
		ID        string `json:"id"`
		Type      string `json:"type"`
		Message   string `json:"message"`
		UserID    string `json:"user_id"`
		Timestamp string `json:"timestamp"`
	} `json:"_formatted"`
}