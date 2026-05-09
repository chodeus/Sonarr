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
