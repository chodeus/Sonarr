package queue

import (
	"testing"
)

func TestNewServiceReturnsNonNilEmpty(t *testing.T) {
	t.Parallel()
	s := NewService()
	if s == nil {
		t.Fatal("NewService returned nil")
	}
	got := s.GetQueue()
	if got == nil {
		t.Errorf("GetQueue on fresh service = nil, want empty slice")
	}
	if len(got) != 0 {
		t.Errorf("GetQueue len = %d, want 0", len(got))
	}
}

func TestGetQueueOnNilReceiverReturnsEmpty(t *testing.T) {
	t.Parallel()
	var s *Service
	got := s.GetQueue()
	if got == nil {
		t.Errorf("GetQueue on nil receiver = nil, want empty slice")
	}
	if len(got) != 0 {
		t.Errorf("GetQueue on nil receiver len = %d, want 0", len(got))
	}
}

func TestFindOnNilReceiverReturnsNil(t *testing.T) {
	t.Parallel()
	var s *Service
	if got := s.Find(1); got != nil {
		t.Errorf("Find on nil receiver = %v, want nil", got)
	}
}

func TestFindOnEmptyCacheReturnsNil(t *testing.T) {
	t.Parallel()
	s := NewService()
	if got := s.Find(1); got != nil {
		t.Errorf("Find on empty cache = %v, want nil", got)
	}
}

// seedForTest is an in-package test helper that bypasses the
// (sub-arc 11b) SetQueue write path so the read-only-surface
// tests can pin GetQueue / Find against a deterministic cache.
func seedForTest(rows ...Queue) *Service {
	return &Service{queue: append([]Queue{}, rows...)}
}

func TestFindReturnsMatchingRow(t *testing.T) {
	t.Parallel()
	s := seedForTest(
		Queue{ID: 11, Title: "Show.S01E01"},
		Queue{ID: 22, Title: "Show.S01E02"},
		Queue{ID: 33, Title: "Show.S01E03"},
	)
	got := s.Find(22)
	if got == nil {
		t.Fatal("Find(22) = nil, want non-nil")
	}
	if got.ID != 22 || got.Title != "Show.S01E02" {
		t.Errorf("Find(22) = %+v", got)
	}
}

func TestFindMissReturnsNil(t *testing.T) {
	t.Parallel()
	s := seedForTest(
		Queue{ID: 11},
		Queue{ID: 22},
	)
	if got := s.Find(99); got != nil {
		t.Errorf("Find(99) = %+v, want nil", got)
	}
}

func TestFindReturnsDefensiveCopy(t *testing.T) {
	t.Parallel()
	s := seedForTest(Queue{ID: 11, Title: "Original"})
	got := s.Find(11)
	if got == nil {
		t.Fatal("Find returned nil")
	}
	got.Title = "Mutated"
	again := s.Find(11)
	if again.Title != "Original" {
		t.Errorf("cache leaked: second Find returned %q, want 'Original'", again.Title)
	}
}

func TestGetQueueReturnsDefensiveCopy(t *testing.T) {
	t.Parallel()
	s := seedForTest(
		Queue{ID: 11, Title: "First"},
		Queue{ID: 22, Title: "Second"},
	)
	snapshot := s.GetQueue()
	if len(snapshot) != 2 {
		t.Fatalf("snapshot len = %d, want 2", len(snapshot))
	}
	snapshot[0].Title = "Mutated"
	again := s.GetQueue()
	if again[0].Title != "First" {
		t.Errorf("cache leaked: second snapshot[0].Title = %q, want 'First'", again[0].Title)
	}
}

func TestGetQueuePreservesOrder(t *testing.T) {
	t.Parallel()
	s := seedForTest(
		Queue{ID: 11},
		Queue{ID: 22},
		Queue{ID: 33},
	)
	got := s.GetQueue()
	if len(got) != 3 || got[0].ID != 11 || got[1].ID != 22 || got[2].ID != 33 {
		t.Errorf("GetQueue order = %v", got)
	}
}

// --- SetQueue (slice 3.BD) -----------------------------------------

func TestSetQueueReplacesCache(t *testing.T) {
	t.Parallel()
	s := seedForTest(Queue{ID: 11}, Queue{ID: 22})
	s.SetQueue([]Queue{{ID: 100}, {ID: 200}, {ID: 300}})
	got := s.GetQueue()
	if len(got) != 3 {
		t.Fatalf("len(GetQueue) = %d, want 3", len(got))
	}
	for i, want := range []int{100, 200, 300} {
		if got[i].ID != want {
			t.Errorf("entry %d ID = %d, want %d", i, got[i].ID, want)
		}
	}
}

func TestSetQueueNilClearsCache(t *testing.T) {
	t.Parallel()
	s := seedForTest(Queue{ID: 11}, Queue{ID: 22})
	s.SetQueue(nil)
	got := s.GetQueue()
	if got == nil {
		t.Errorf("GetQueue after nil-SetQueue = nil, want empty slice")
	}
	if len(got) != 0 {
		t.Errorf("len after nil-SetQueue = %d, want 0", len(got))
	}
}

func TestSetQueueEmptyClearsCache(t *testing.T) {
	t.Parallel()
	s := seedForTest(Queue{ID: 11})
	s.SetQueue([]Queue{})
	if got := s.GetQueue(); len(got) != 0 {
		t.Errorf("len after empty-SetQueue = %d, want 0", len(got))
	}
}

func TestSetQueueCopiesInput(t *testing.T) {
	t.Parallel()
	s := NewService()
	input := []Queue{{ID: 11, Title: "Original"}}
	s.SetQueue(input)
	// Mutate the input — the cache should be unaffected.
	input[0].Title = "Mutated"
	got := s.GetQueue()
	if got[0].Title != "Original" {
		t.Errorf("cache mutated by input change: got %q, want 'Original'", got[0].Title)
	}
}

func TestSetQueueOnNilReceiverIsNoOp(t *testing.T) {
	t.Parallel()
	var s *Service
	// Must not panic.
	s.SetQueue([]Queue{{ID: 1}})
}

// --- Remove (slice 3.BD) -------------------------------------------

func TestRemoveExistingRowReturnsTrue(t *testing.T) {
	t.Parallel()
	s := seedForTest(Queue{ID: 11}, Queue{ID: 22}, Queue{ID: 33})
	if !s.Remove(22) {
		t.Errorf("Remove(22) returned false; want true")
	}
	got := s.GetQueue()
	if len(got) != 2 || got[0].ID != 11 || got[1].ID != 33 {
		t.Errorf("after Remove(22) cache = %v, want [11 33]", got)
	}
}

func TestRemoveMissingRowReturnsFalse(t *testing.T) {
	t.Parallel()
	s := seedForTest(Queue{ID: 11}, Queue{ID: 22})
	if s.Remove(99) {
		t.Errorf("Remove(99) returned true; want false")
	}
	if got := s.GetQueue(); len(got) != 2 {
		t.Errorf("cache modified by failed Remove: len = %d, want 2", len(got))
	}
}

func TestRemoveOnEmptyCacheReturnsFalse(t *testing.T) {
	t.Parallel()
	s := NewService()
	if s.Remove(1) {
		t.Errorf("Remove on empty cache returned true; want false")
	}
}

func TestRemoveOnNilReceiverReturnsFalse(t *testing.T) {
	t.Parallel()
	var s *Service
	if s.Remove(1) {
		t.Errorf("Remove on nil receiver returned true; want false")
	}
}

func TestRemoveFirstRowPreservesOrder(t *testing.T) {
	t.Parallel()
	s := seedForTest(Queue{ID: 11}, Queue{ID: 22}, Queue{ID: 33})
	s.Remove(11)
	got := s.GetQueue()
	if len(got) != 2 || got[0].ID != 22 || got[1].ID != 33 {
		t.Errorf("after Remove(first) cache = %v, want [22 33]", got)
	}
}

func TestRemoveLastRowPreservesOrder(t *testing.T) {
	t.Parallel()
	s := seedForTest(Queue{ID: 11}, Queue{ID: 22}, Queue{ID: 33})
	s.Remove(33)
	got := s.GetQueue()
	if len(got) != 2 || got[0].ID != 11 || got[1].ID != 22 {
		t.Errorf("after Remove(last) cache = %v, want [11 22]", got)
	}
}
