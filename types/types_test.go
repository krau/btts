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
			name: "Type filters only",
			request: SearchRequest{
				UserFilters: []int64{},
				TypeFilters: []MessageType{MessageTypeText, MessageTypePhoto},
			},
			expected: "type IN [0,1]",
		},
		{
			name: "Both filters",
			request: SearchRequest{
				UserFilters: []int64{123, 456},
				TypeFilters: []MessageType{MessageTypeText, MessageTypePhoto},
			},
			expected: "user_id IN [123,456] AND type IN [0,1]",
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
