package types

import (
	"fmt"

	"github.com/duke-git/lancet/v2/slice"
)

type SearchRequest struct {
	ChatID      int64         `json:"chat_id"`
	Query       string        `json:"query"`
	ChatIDs     []int64       `json:"chat_ids"`
	TypeFilters []MessageType `json:"type_filters"`
	UserFilters []int64       `json:"user_filters"`
	Ocred       bool          `json:"ocred"`
	AIGenerated bool          `json:"ai_generated"`
	Limit       int64         `json:"limit"`
	Offset      int64         `json:"offset"`
}

func (r SearchRequest) FilterExpression() string {
	if len(r.UserFilters) == 0 && len(r.TypeFilters) == 0 {
		if len(r.ChatIDs) > 0 {
			return fmt.Sprintf("chat_id IN [%s]", slice.Join(r.ChatIDs, ","))
		}
		return fmt.Sprintf("chat_id = %d", r.ChatID)
	}
	var filters []string
	if len(r.UserFilters) > 0 {
		userFilter := fmt.Sprintf("user_id IN [%s]", slice.Join(r.UserFilters, ","))
		filters = append(filters, userFilter)
	}
	if len(r.TypeFilters) > 0 {
		typeFilter := fmt.Sprintf("type IN [%s]", slice.Join(r.TypeFilters, ","))
		filters = append(filters, typeFilter)
	}
	if len(filters) == 0 {
		return ""
	}
	if len(filters) == 1 {
		return filters[0]
	}
	return slice.Join(filters, " AND ")
}
