package types

import "testing"

func TestSearchRequestFilterExpression(t *testing.T) {
	tests := []struct {
		name      string
		request   SearchRequest
		expected  string
		expectErr bool
	}{
		{
			name: "No filters",
			request: SearchRequest{
				UserFilters: []int64{},
				TypeFilters: []MessageType{},
			},
			expected:  "",
			expectErr: true,
		},
		{
			name: "User filters only",
			request: SearchRequest{
				UserFilters: []int64{123, 456},
				TypeFilters: []MessageType{},
				AllChats:    true,
			},
			expected: "user_id IN [123,456]",
		},
		{
			name: "User One filter only",
			request: SearchRequest{
				UserFilters: []int64{123},
				TypeFilters: []MessageType{},
				AllChats:    true,
			},
			expected: "user_id = 123",
		},
		{
			name: "Type filters only",
			request: SearchRequest{
				UserFilters: []int64{},
				TypeFilters: []MessageType{MessageTypeText, MessageTypePhoto},
				AllChats:    true,
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
		{
			name: "Bad filters",
			request: SearchRequest{
				AllChats: false,
			},
			expected:  "",
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.request.FilterExpression()
			if tt.expectErr {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if result != tt.expected {
				t.Errorf("expected '%s', got '%s'", tt.expected, result)
			}
		})
	}

}
