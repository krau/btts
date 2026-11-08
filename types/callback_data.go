package types

import (
	"fmt"
	"strings"

	"github.com/duke-git/lancet/v2/slice"
)

type SearchRequest struct {
	ChatID      int64         `json:"chat_id"`
	Query       string        `json:"query"`
	ChatIDs     []int64       `json:"chat_ids"`
	AllChats    bool          `json:"all_chats"`
	TypeFilters []MessageType `json:"type_filters"`
	UserFilters []int64       `json:"user_filters"`
	Tags        []string      `json:"tags,omitempty"`
	TagMatch    string        `json:"tag_match,omitempty"` // "any" (default) or "all"
	Mode        string        `json:"mode,omitempty"`      // ""/hybrid, "keyword", "media"
	// Modality restricts media vectors to a target: "", "all", "text", "image", "audio", "video".
	Modality          string `json:"modality,omitempty"`
	DisableOcred      bool   `json:"disable_ocred"`      // 不搜索 OCR 文字
	EnableAIGenerated bool   `json:"enable_aigenerated"` // [TODO] 搜索 AI 生成的内容(not implemented yet)
	Limit             int64  `json:"limit"`
	Offset            int64  `json:"offset"`
	// ImageData and AudioData keep the original query media in the callback
	// cache so pagination can repeat the same media search.
	ImageData []byte `json:"-"`
	AudioData []byte `json:"-"`
}

func (r SearchRequest) FilterExpression() (string, error) {
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

	if !r.AllChats {
		if r.ChatID != 0 {
			filters = append(filters, fmt.Sprintf("chat_id = %d", r.ChatID))
		} else if len(r.ChatIDs) > 0 {
			addInt64Filter("chat_id", r.ChatIDs)
		} else {
			return "", fmt.Errorf("either ChatID or ChatIDs must be set when AllChats is false")
		}
	}

	addInt64Filter("user_id", r.UserFilters)

	if len(r.Tags) > 0 {
		seen := make(map[string]struct{}, len(r.Tags))
		tagFilters := make([]string, 0, len(r.Tags))
		for _, tag := range r.Tags {
			tag = strings.TrimSpace(tag)
			if tag == "" {
				continue
			}
			if _, ok := seen[tag]; ok {
				continue
			}
			seen[tag] = struct{}{}
			tagFilters = append(tagFilters, fmt.Sprintf("tags = %q", tag))
		}
		if len(tagFilters) > 0 {
			sep := " OR "
			if strings.EqualFold(r.TagMatch, "all") {
				sep = " AND "
			}
			filters = append(filters, "("+strings.Join(tagFilters, sep)+")")
		}
	}

	if len(r.TypeFilters) > 0 {
		typeStrs := slice.Map(r.TypeFilters, func(_ int, item MessageType) string { return fmt.Sprintf("%d", item) })
		filters = append(filters, fmt.Sprintf("type IN [%s]", slice.Join(typeStrs, ",")))
	}

	switch len(filters) {
	case 0:
		return "", nil
	case 1:
		return filters[0], nil
	default:
		return slice.Join(filters, " AND "), nil
	}
}
