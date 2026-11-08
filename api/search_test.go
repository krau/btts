package api

import (
	"encoding/base64"
	"testing"

	"github.com/krau/btts/types"
)

func TestValidateSearchRequest(t *testing.T) {
	all := types.Capabilities{TagFilters: true, Semantic: true, ImageSearch: true}
	cases := []struct {
		name string
		req  types.SearchRequest
		caps types.Capabilities
		ok   bool
	}{
		{"plain", types.SearchRequest{Query: "x"}, types.Capabilities{}, true},
		{"tags disabled", types.SearchRequest{Query: "x", Tags: []string{"a"}}, types.Capabilities{}, false},
		{"tags enabled", types.SearchRequest{Query: "x", Tags: []string{"a"}}, all, true},
		{"media disabled", types.SearchRequest{Query: "x", Mode: "media"}, types.Capabilities{}, false},
		{"media enabled", types.SearchRequest{Query: "x", Mode: "media"}, all, true},
		{"media without query", types.SearchRequest{Mode: "media"}, all, false},
		{"tags with media", types.SearchRequest{Query: "x", Mode: "media", Tags: []string{"a"}}, all, false},
		{"invalid mode", types.SearchRequest{Query: "x", Mode: "semantic"}, all, false},
		{"invalid tag match", types.SearchRequest{Query: "x", TagMatch: "some"}, all, false},
		{"valid tag match", types.SearchRequest{Query: "x", Tags: []string{"a"}, TagMatch: "all"}, all, true},
		{"valid modality", types.SearchRequest{Query: "x", Modality: "video"}, all, true},
		{"invalid modality", types.SearchRequest{Query: "x", Modality: "frames"}, all, false},
	}
	for _, tc := range cases {
		err := validateSearchRequest(tc.req, tc.caps, false)
		if (err == nil) != tc.ok {
			t.Errorf("%s: err=%v, want ok=%v", tc.name, err, tc.ok)
		}
	}
	if err := validateSearchRequest(types.SearchRequest{Mode: "media", Modality: "image"}, types.Capabilities{Semantic: true}, true); err != nil {
		t.Fatalf("media query without text: %v", err)
	}
}

func TestDecodeSearchImage(t *testing.T) {
	payload := []byte("fake image bytes")
	padded := base64.StdEncoding.EncodeToString(payload)
	raw := base64.RawStdEncoding.EncodeToString(payload)

	for name, input := range map[string]string{
		"padded":   padded,
		"raw":      raw,
		"data URL": "data:image/png;base64," + padded,
	} {
		got, err := decodeSearchMedia(input)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(got) != string(payload) {
			t.Fatalf("%s: got %q", name, got)
		}
	}

	for name, input := range map[string]string{
		"invalid": "not-base64%%%",
		"empty":   "",
	} {
		if _, err := decodeSearchMedia(input); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}
}
