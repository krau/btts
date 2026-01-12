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

func (r *SearchRequest) FilterExpression() string {
	var filters []string

	addInt64Filter := func(field string, ids []int64) {
		if len(ids) == 0 {
			return
		}
		if len(ids) == 1 {
			filters = append(filters, fmt.Sprintf("%s = %d", field, ids[0]))
			return
		}
		idStrs := slice.Map(ids, func(_ int, item int64) string { return fmt.Sprintf("%d", item) })
		filters = append(filters, fmt.Sprintf("%s IN [%s]", field, slice.Join(idStrs, ",")))
	}

	if r.ChatID != 0 {
		filters = append(filters, fmt.Sprintf("chat_id = %d", r.ChatID))
	} else {
		addInt64Filter("chat_id", r.ChatIDs)
	}

	addInt64Filter("user_id", r.UserFilters)

	if len(r.TypeFilters) > 0 {
		typeStrs := slice.Map(r.TypeFilters, func(_ int, item MessageType) string { return fmt.Sprintf("%d", item) })
		filters = append(filters, fmt.Sprintf("type IN [%s]", slice.Join(typeStrs, ",")))
	}

	switch len(filters) {
	case 0:
		return ""
	case 1:
		return filters[0]
	default:
		return slice.Join(filters, " AND ")
	}
}
