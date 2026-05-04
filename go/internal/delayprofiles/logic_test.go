package delayprofiles

import (
	"reflect"
	"testing"
)

func TestAllForTag(t *testing.T) {
	profiles := []Profile{
		{ID: 1, Tags: []int{1, 2}},
		{ID: 2, Tags: []int{2, 3}},
		{ID: 3, Tags: nil},
	}
	got := AllForTag(profiles, 2)
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 2 {
		t.Errorf("AllForTag = %+v, want [Id=1, Id=2]", got)
	}

	if got := AllForTag(profiles, 99); got != nil {
		t.Errorf("AllForTag for unknown tag should be nil/empty, got %+v", got)
	}
}

func TestAllForTags_EmptyTagsAlwaysIncluded(t *testing.T) {
	profiles := []Profile{
		{ID: 1, Tags: []int{1}},
		{ID: 2, Tags: nil}, // empty Tags = catch-all default
		{ID: 3, Tags: []int{2, 3}},
	}
	got := AllForTags(profiles, []int{2})
	if len(got) != 2 {
		t.Fatalf("expected 2 profiles (Id=2 catch-all + Id=3 by tag), got %d: %+v", len(got), got)
	}
	if got[0].ID != 2 || got[1].ID != 3 {
		t.Errorf("got %+v, want [Id=2, Id=3]", got)
	}
}

func TestBestForTags(t *testing.T) {
	profiles := []Profile{
		{ID: 5, Tags: []int{1}, Order: 3},
		{ID: 4, Tags: nil, Order: 2}, // catch-all
		{ID: 3, Tags: []int{2}, Order: 1},
	}
	t.Run("matches by tag — picks lowest Order", func(t *testing.T) {
		best, ok := BestForTags(profiles, []int{2})
		if !ok || best.ID != 3 {
			t.Errorf("got %+v ok=%v, want Id=3", best, ok)
		}
	})

	t.Run("no tag match falls through to catch-all", func(t *testing.T) {
		best, ok := BestForTags(profiles, []int{99})
		if !ok || best.ID != 4 {
			t.Errorf("got %+v ok=%v, want Id=4 (catch-all)", best, ok)
		}
	})

	t.Run("empty profiles returns ok=false", func(t *testing.T) {
		_, ok := BestForTags(nil, []int{1})
		if ok {
			t.Errorf("expected ok=false for empty profiles")
		}
	})

	t.Run("no catch-all and no tag match returns ok=false", func(t *testing.T) {
		picky := []Profile{
			{ID: 1, Tags: []int{1}, Order: 1},
			{ID: 2, Tags: []int{2}, Order: 2},
		}
		_, ok := BestForTags(picky, []int{99})
		if ok {
			t.Errorf("expected ok=false when no profile matches")
		}
	})
}

func TestComputeReorder_MoveToFront(t *testing.T) {
	// Move Id=3 to the front (afterID=nil).
	profiles := []Profile{
		{ID: 1, Order: 0}, // pinned default
		{ID: 2, Order: 1},
		{ID: 3, Order: 2},
		{ID: 4, Order: 3},
	}
	got := ComputeReorder(profiles, 3, nil)
	// Id=1 stays at Order=0. Id=3 moves to Order=1; the others
	// shift up.
	want := map[int]int{1: 0, 3: 1, 2: 2, 4: 3}
	for _, p := range got {
		if w, ok := want[p.ID]; ok && p.Order != w {
			t.Errorf("after reorder: Id=%d Order=%d, want %d", p.ID, p.Order, w)
		}
	}
}

func TestComputeReorder_MoveAfter(t *testing.T) {
	// Move Id=2 after Id=4.
	profiles := []Profile{
		{ID: 1, Order: 0},
		{ID: 2, Order: 1},
		{ID: 3, Order: 2},
		{ID: 4, Order: 3},
	}
	got := ComputeReorder(profiles, 2, ptr(4))
	// After the move, Id=2 should sit immediately after Id=4 in
	// Order ascending. The exact Order numbers are an
	// implementation detail; assert relative ordering instead.
	idsByOrder := make([]int, len(got))
	for i, p := range got {
		idsByOrder[i] = p.ID
	}
	wantOrder := []int{1, 3, 4, 2}
	if !reflect.DeepEqual(idsByOrder, wantOrder) {
		t.Errorf("idsByOrder = %v, want %v", idsByOrder, wantOrder)
	}
}

func TestComputeReorder_UnknownIDReturnsUnchanged(t *testing.T) {
	profiles := []Profile{
		{ID: 1, Order: 0},
		{ID: 2, Order: 1},
	}
	got := ComputeReorder(profiles, 99, nil)
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 2 {
		t.Errorf("got %+v, want unchanged", got)
	}
}

func TestComputeReorder_DefaultIsPinned(t *testing.T) {
	// Try to move Id=1 (the default). It should stay put per the
	// `if Id == 1: continue` skip.
	profiles := []Profile{
		{ID: 1, Order: 0},
		{ID: 2, Order: 1},
		{ID: 3, Order: 2},
	}
	got := ComputeReorder(profiles, 1, ptr(3))
	// Id=1 should still be at Order=0.
	for _, p := range got {
		if p.ID == 1 && p.Order != 0 {
			t.Errorf("Id=1 was moved (Order=%d); should stay at Order=0", p.Order)
		}
	}
}

func ptr[T any](v T) *T { return &v }
