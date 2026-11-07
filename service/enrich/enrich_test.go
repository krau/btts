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
