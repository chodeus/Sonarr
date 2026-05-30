package housekeeping_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/housekeeping"
)

type stubLogTrimmer struct {
	gotNow  time.Time
	called  int
	removed int64
	err     error
}

func (s *stubLogTrimmer) Trim(_ context.Context, now time.Time) (int64, error) {
	s.called++
	s.gotNow = now
	return s.removed, s.err
}

func TestNewTrimLogDatabase_NilTrimmer(t *testing.T) {
	t.Parallel()
	if _, err := housekeeping.NewTrimLogDatabase(nil, nil); err == nil {
		t.Fatal("expected error on nil trimmer")
	}
}

func TestTrimLogDatabase_Clean_CallsTrim(t *testing.T) {
	t.Parallel()
	trimmer := &stubLogTrimmer{removed: 12}
	task, err := housekeeping.NewTrimLogDatabase(trimmer, nil)
	if err != nil {
		t.Fatalf("NewTrimLogDatabase: %v", err)
	}
	if task.Name() != "TrimLogDatabase" {
		t.Errorf("Name() = %q, want TrimLogDatabase", task.Name())
	}
	if err := task.Clean(context.Background()); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if trimmer.called != 1 {
		t.Errorf("Trim called %d times, want 1", trimmer.called)
	}
	if trimmer.gotNow.IsZero() || trimmer.gotNow.Location() != time.UTC {
		t.Errorf("Trim now = %v, want a non-zero UTC time", trimmer.gotNow)
	}
}

func TestTrimLogDatabase_Clean_PropagatesError(t *testing.T) {
	t.Parallel()
	trimmer := &stubLogTrimmer{err: errors.New("db locked")}
	task, _ := housekeeping.NewTrimLogDatabase(trimmer, nil)
	if err := task.Clean(context.Background()); err == nil {
		t.Fatal("expected Clean to propagate the trimmer error")
	}
}
