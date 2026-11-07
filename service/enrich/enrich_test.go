package enrich

import (
	"testing"

	"github.com/krau/btts/types"
)

func hit(id int64, timestamp int64) types.SearchHit {
	return types.SearchHit{MessageDocument: types.MessageDocument{
		ID:        id,
		ChatID:    1,
		Timestamp: timestamp,
	}}
}

func TestFuseHitsReciprocalRank(t *testing.T) {
	text := []types.SearchHit{hit(1, 1), hit(2, 2)}
	media := []types.SearchHit{hit(2, 2), hit(3, 3)}
	got := fuseHits(text, media)
	if len(got) != 3 {
		t.Fatalf("got %d hits, want 3", len(got))
	}
	// Message 2 appears in both lists, so its summed RRF score wins.
	if got[0].ID != 2 || got[1].ID != 1 || got[2].ID != 3 {
		t.Fatalf("unexpected order: %v, %v, %v", got[0].ID, got[1].ID, got[2].ID)
	}
}

func TestMediaTargetFilter(t *testing.T) {
	cases := []struct {
		target, queryKind, want string
	}{
		{"", "text", ""},
		{"all", "image", ""},
		{"text", "text", `modality = "text"`},
		{"image", "text", `media_type = "image" AND modality = "image"`},
		{"audio", "text", `media_type = "audio" AND modality = "audio"`},
		{"video", "text", `media_type = "video"`},
		{"video", "image", `media_type = "video" AND modality = "image"`},
		{"video", "audio", `media_type = "video" AND modality = "audio"`},
	}
	for _, tc := range cases {
		if got := mediaTargetFilter(tc.target, tc.queryKind); got != tc.want {
			t.Errorf("target=%q query=%q: got %q, want %q", tc.target, tc.queryKind, got, tc.want)
		}
	}
}

func TestNormalizeTargetModality(t *testing.T) {
	cases := map[string]string{
		"":        "",
		" all ":   "all",
		"IMAGE":   "image",
		"audio":   "audio",
		"video":   "video",
		"text":    "text",
		"unknown": "",
	}
	for input, want := range cases {
		if got := normalizeTargetModality(input); got != want {
			t.Errorf("%q: got %q, want %q", input, got, want)
		}
	}
}

func TestModalityTypeFilters(t *testing.T) {
	if got := modalityTypeFilters("image"); len(got) != 2 {
		t.Fatalf("image filters = %v", got)
	}
	if got := modalityTypeFilters("audio"); len(got) != 3 {
		t.Fatalf("audio filters = %v", got)
	}
	if got := modalityTypeFilters("video"); len(got) != 2 {
		t.Fatalf("video filters = %v", got)
	}
	if got := modalityTypeFilters("text"); len(got) != 0 {
		t.Fatalf("text filters = %v", got)
	}
}

func TestPaginate(t *testing.T) {
	hits := []types.SearchHit{hit(1, 1), hit(2, 2), hit(3, 3)}
	cases := []struct {
		offset, limit int64
		want          []int64
	}{
		{0, 2, []int64{1, 2}},
		{1, 2, []int64{2, 3}},
		{2, 5, []int64{3}},
		{5, 2, nil},
		{-2, 2, []int64{1, 2}},
	}
	for _, tc := range cases {
		got := paginate(hits, tc.offset, tc.limit)
		if len(got) != len(tc.want) {
			t.Fatalf("offset=%d limit=%d: got %d hits, want %d", tc.offset, tc.limit, len(got), len(tc.want))
		}
		for i := range tc.want {
			if got[i].ID != tc.want[i] {
				t.Fatalf("offset=%d limit=%d: got ID %d at %d, want %d", tc.offset, tc.limit, got[i].ID, i, tc.want[i])
			}
		}
	}
}
