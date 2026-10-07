package plugin

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/krau/mygotg/types"
)

type cleanMemberPageAPI struct {
	*fakeCleanMemberAPI
	pages   [][]int64
	offsets []int
}

func (f *cleanMemberPageAPI) Invoke(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
	req, ok := input.(*tg.ChannelsGetParticipantsRequest)
	if !ok {
		return f.fakeCleanMemberAPI.Invoke(ctx, input, output)
	}
	f.offsets = append(f.offsets, req.Offset)
	var ids []int64
	if index := len(f.offsets) - 1; index < len(f.pages) {
		ids = f.pages[index]
	}
	participants := make([]tg.ChannelParticipantClass, 0, len(ids))
	users := make([]tg.UserClass, 0, len(ids))
	for _, id := range ids {
		participants = append(participants, &tg.ChannelParticipant{UserID: id})
		users = append(users, f.user(id))
	}
	return encodeInto(output, &tg.ChannelsChannelParticipants{
		Count: len(ids), Participants: participants, Users: users,
	})
}

func runCleanMemberPages(f *cleanMemberPageAPI, fn func(*tg.User) (bool, error)) error {
	ctx, _ := newCleanMemberTestEnv(f.fakeCleanMemberAPI)
	ctx.Raw = tg.NewClient(f)
	return forEachChannelCleanMember(ctx, &types.Channel{
		ID: testCleanChatID, AccessHash: 42, Megagroup: true,
	}, fn)
}

func TestCleanMemberRepeatedReorderedPage(t *testing.T) {
	f := &cleanMemberPageAPI{
		fakeCleanMemberAPI: newFakeCleanMemberAPI(0),
		pages:              [][]int64{{1001, 1002, 1003}, {1003, 1001, 1002}},
	}
	visits := map[int64]int{}
	err := runCleanMemberPages(f, func(user *tg.User) (bool, error) {
		visits[user.ID]++
		return false, nil
	})
	if err == nil || !strings.Contains(err.Error(), "未返回新成员") {
		t.Fatalf("repeated page was not rejected: %v", err)
	}
	if !slices.Equal(f.offsets, []int{0, 3}) {
		t.Fatalf("repeated page caused extra requests: %v", f.offsets)
	}
	for _, id := range []int64{1001, 1002, 1003} {
		if visits[id] != 1 {
			t.Fatalf("duplicate side effects for %d: %d", id, visits[id])
		}
	}
}

func TestCleanMemberStaleRemovedPage(t *testing.T) {
	f := &cleanMemberPageAPI{
		fakeCleanMemberAPI: newFakeCleanMemberAPI(0),
		pages:              [][]int64{{1001, 1002}, {1002, 1001}},
	}
	visits := map[int64]int{}
	err := runCleanMemberPages(f, func(user *tg.User) (bool, error) {
		visits[user.ID]++
		return true, nil
	})
	if err == nil || !strings.Contains(err.Error(), "未返回新成员") {
		t.Fatalf("stale removed page was not rejected: %v", err)
	}
	if !slices.Equal(f.offsets, []int{0, 0}) {
		t.Fatalf("stale page should abort at unchanged offset: %v", f.offsets)
	}
	if visits[1001] != 1 || visits[1002] != 1 {
		t.Fatalf("removed members processed again: %v", visits)
	}
}

func TestCleanMemberMixedStaleAndNewPage(t *testing.T) {
	f := &cleanMemberPageAPI{
		fakeCleanMemberAPI: newFakeCleanMemberAPI(0),
		pages:              [][]int64{{1001, 1002}, {1002, 1003}, {1003}},
	}
	visits := map[int64]int{}
	err := runCleanMemberPages(f, func(user *tg.User) (bool, error) {
		visits[user.ID]++
		return user.ID == 1002, nil
	})
	if err == nil || !strings.Contains(err.Error(), "未返回新成员") {
		t.Fatalf("no-new-member page was not rejected: %v", err)
	}
	if !slices.Equal(f.offsets, []int{0, 1, 2}) {
		t.Fatalf("removed stale member changed offset correction: %v", f.offsets)
	}
	for _, id := range []int64{1001, 1002, 1003} {
		if visits[id] != 1 {
			t.Fatalf("duplicate side effects for %d: %d", id, visits[id])
		}
	}
}

func TestCleanMemberAllRemovedPagesProgress(t *testing.T) {
	f := &cleanMemberPageAPI{fakeCleanMemberAPI: newFakeCleanMemberAPI(0)}
	for page := range 3 {
		ids := make([]int64, cleanMemberPageSize)
		for i := range ids {
			ids[i] = int64(1000 + page*cleanMemberPageSize + i)
		}
		f.pages = append(f.pages, ids)
	}
	visits := map[int64]int{}
	if err := runCleanMemberPages(f, func(user *tg.User) (bool, error) {
		visits[user.ID]++
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.offsets, []int{0, 0, 0, 0}) {
		t.Fatalf("all-removed pages did not retain offset zero: %v", f.offsets)
	}
	if len(visits) != 3*cleanMemberPageSize {
		t.Fatalf("skipped removed members: %d", len(visits))
	}
	for id, count := range visits {
		if count != 1 {
			t.Fatalf("duplicate side effects for %d: %d", id, count)
		}
	}
}

func TestCleanMemberDuplicateParticipantWithinPage(t *testing.T) {
	f := &cleanMemberPageAPI{
		fakeCleanMemberAPI: newFakeCleanMemberAPI(0),
		pages:              [][]int64{{1001, 1001, 1002}},
	}
	visits := map[int64]int{}
	if err := runCleanMemberPages(f, func(user *tg.User) (bool, error) {
		visits[user.ID]++
		return false, nil
	}); err != nil {
		t.Fatal(err)
	}
	if visits[1001] != 1 || visits[1002] != 1 {
		t.Fatalf("duplicate participant processed again: %v", visits)
	}
}

func TestCleanMemberPageCallbackError(t *testing.T) {
	f := &cleanMemberPageAPI{
		fakeCleanMemberAPI: newFakeCleanMemberAPI(0),
		pages:              [][]int64{{1001, 1002}},
	}
	calls := 0
	want := fmt.Errorf("callback failed")
	if err := runCleanMemberPages(f, func(*tg.User) (bool, error) {
		calls++
		return false, want
	}); err != want {
		t.Fatalf("lost callback error: %v", err)
	}
	if calls != 1 || len(f.offsets) != 1 {
		t.Fatalf("callback error did not stop pagination: callbacks=%d offsets=%v", calls, f.offsets)
	}
}

func TestCleanMemberEarlierPageRepeats(t *testing.T) {
	f := &cleanMemberPageAPI{
		fakeCleanMemberAPI: newFakeCleanMemberAPI(0),
		pages:              [][]int64{{1001, 1002}, {1003, 1004}, {1002, 1001}},
	}
	visits := map[int64]int{}
	err := runCleanMemberPages(f, func(user *tg.User) (bool, error) {
		visits[user.ID]++
		return false, nil
	})
	if err == nil || !strings.Contains(err.Error(), "未返回新成员") {
		t.Fatalf("earlier page was not rejected: %v", err)
	}
	if !slices.Equal(f.offsets, []int{0, 2, 4}) {
		t.Fatalf("earlier page caused extra requests: %v", f.offsets)
	}
	for _, id := range []int64{1001, 1002, 1003, 1004} {
		if visits[id] != 1 {
			t.Fatalf("duplicate side effects for %d: %d", id, visits[id])
		}
	}
}
