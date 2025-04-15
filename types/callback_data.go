package types

type SearchRequest struct {
	ChatID      int64         `json:"chat_id"`
	Query       string        `json:"query"`
	ChatIDs     []int64       `json:"chat_ids"`
	TypeFilters []MessageType `json:"type_filters"`
	Limit       int64         `json:"limit"`
	Offset      int64         `json:"offset"`
}
