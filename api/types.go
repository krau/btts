package api

// SearchOnChatByPostRequest 单聊天搜索请求
type SearchOnChatByPostRequest struct {
	Query  string   `json:"query" validate:"required" example:"search text"` // 搜索查询字符串
	Offset int64    `json:"offset" default:"0" example:"0"`                  // 偏移量，用于分页
	Limit  int64    `json:"limit" default:"10" example:"10"`                 // 限制数量，用于分页
	Users  []int64  `json:"users,omitempty" example:"123456,789012"`         // 用户ID过滤列表，可选
	Types  []string `json:"types,omitempty" example:"text,photo"`            // 消息类型过滤列表，可选值：text,photo,video,document,voice,audio,poll,story
}

// SearchOnMultiChatByPostRequest 多聊天搜索请求
type SearchOnMultiChatByPostRequest struct {
	ChatIDs []int64  `json:"chat_ids" example:"777000,114514"` // 聊天ID列表，如果为空则搜索所有聊天
	Query   string   `json:"query" validate:"required" example:"search text"`   // 搜索查询字符串
	Offset  int64    `json:"offset" default:"0" example:"0"`                    // 偏移量，用于分页
	Limit   int64    `json:"limit" default:"10" example:"10"`                   // 限制数量，用于分页
	Users   []int64  `json:"users,omitempty" example:"123456,789012"`           // 用户ID过滤列表，可选
	Types   []string `json:"types,omitempty" example:"text,photo"`              // 消息类型过滤列表，可选值：text,photo,video,document,voice,audio,poll,story
}
