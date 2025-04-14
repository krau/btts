package types

type SearchCallbackData struct {
	ChatID int64  `json:"chat_id"`
	Query  string `json:"query"`
}
