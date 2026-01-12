package types

import "testing"

func TestSearchRequestFilterExpression(t *testing.T) {
	tests := []struct {
		name     string
		request  SearchRequest
		expected string
	}{
		{
			name: "No filters",
			request: SearchRequest{
				UserFilters: []int64{},
				TypeFilters: []MessageType{},
			},
			expected: "",
		},
		{
			name: "User filters only",
			request: SearchRequest{
				UserFilters: []int64{123, 456},
				TypeFilters: []MessageType{},
			},
			expected: "user_id IN [123,456]",
		},
		{
			name: "User One filter only",
			request: SearchRequest{
				UserFilters: []int64{123},
				TypeFilters: []MessageType{},
			},
			expected: "user_id = 123",
		},
		{
			name: "Type filters only",
			request: SearchRequest{
				UserFilters: []int64{},
				TypeFilters: []MessageType{MessageTypeText, MessageTypePhoto},
			},
			expected: "type IN [0,1]",
		},
		{
			name: "One Chat filters only",
			request: SearchRequest{
				ChatID: 123456789,
			},
			expected: "chat_id = 123456789",
		},
		{
			name: "Multiple Chat filters only",
			request: SearchRequest{
				ChatIDs: []int64{123, 456, 789},
			},
			expected: "chat_id IN [123,456,789]",
		},
		{
			name: "All filters",
			request: SearchRequest{
				UserFilters: []int64{123, 456},
				TypeFilters: []MessageType{MessageTypeText, MessageTypePhoto},
				ChatID:      0,
				ChatIDs:     []int64{123, 456},
			},
			expected: "chat_id IN [123,456] AND user_id IN [123,456] AND type IN [0,1]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.request.FilterExpression()
			if result != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, result)
			}
		})
	}

}
