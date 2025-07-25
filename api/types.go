package api

type SearchOnChatByPostRequest struct {
	Query  string   `json:"query" validate:"required"`
	Offset int64    `json:"offset" default:"0"`
	Limit  int64    `json:"limit" default:"10"`
	Users  []int64  `json:"users,omitempty"`
	Types  []string `json:"types,omitempty"`
}

type SearchOnMultiChatByPostRequest struct {
	ChatIDs []int64  `json:"chat_ids"`
	Query   string   `json:"query" validate:"required"`
	Offset  int64    `json:"offset" default:"0"`
	Limit   int64    `json:"limit" default:"10"`
	Users   []int64  `json:"users,omitempty"`
	Types   []string `json:"types,omitempty"`
}
