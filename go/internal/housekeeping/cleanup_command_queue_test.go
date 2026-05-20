package housekeeping_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/commands"
	"sonarr.tv/sonarr/internal/housekeeping"
)

type stubCommandCleaner struct {
	gotCutoff time.Time
	got       int64
	err       error
	callCnt   int
}

func (s *stubCommandCleaner) CleanCommands(_ context.Context, cutoff time.Time) (int64, error) {
	s.callCnt++
	s.gotCutoff = cutoff
	if s.err != nil {
		return 0, s.err
	}
	return s.got, nil
}

func TestCleanupCommandQueue_NilCleaner(t *testing.T) {
	t.Parallel()
	if _, err := housekeeping.NewCleanupCommandQueue(nil, slog.Default()); err == nil {
		t.Errorf("expected error on nil cleaner")
	}
}

func TestCleanupCommandQueue_PassesCutoffAndLogs(t *testing.T) {
	t.Parallel()
	cleaner := &stubCommandCleaner{got: 4}
	task, err := housekeeping.NewCleanupCommandQueue(cleaner, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := task.Name(); got != "CleanupCommandQueue" {
		t.Errorf("Name() = %q", got)
	}

	before := time.Now().UTC()
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	after := time.Now().UTC()

	if cleaner.callCnt != 1 {
		t.Errorf("CleanCommands calls = %d, want 1", cleaner.callCnt)
	}
	// cutoff must be `now - 24h` for some `now` between before and after.
	earliestCutoff := before.Add(-housekeeping.CleanupCommandQueueTTL)
	latestCutoff := after.Add(-housekeeping.CleanupCommandQueueTTL)
	if cleaner.gotCutoff.Before(earliestCutoff) || cleaner.gotCutoff.After(latestCutoff) {
		t.Errorf("cutoff = %v, want between %v and %v", cleaner.gotCutoff, earliestCutoff, latestCutoff)
	}
}

func TestCleanupCommandQueue_ZeroRowsIsStillNilErr(t *testing.T) {
	t.Parallel()
	cleaner := &stubCommandCleaner{got: 0}
	task, err := housekeeping.NewCleanupCommandQueue(cleaner, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := task.Clean(t.Context()); err != nil {
		t.Errorf("Clean (zero rows): %v", err)
	}
}

func TestCleanupCommandQueue_CleanerErrorPropagates(t *testing.T) {
	t.Parallel()
	cleaner := &stubCommandCleaner{err: errors.New("repo down")}
	task, _ := housekeeping.NewCleanupCommandQueue(cleaner, slog.Default())
	err := task.Clean(t.Context())
	if err == nil {
		t.Fatal("expected error from cleaner")
	}
	if !errors.Is(err, cleaner.err) {
		t.Errorf("error chain missing source: %v", err)
	}
}

// Compile-time assertion: *commands.Service satisfies the
// CommandQueueCleaner interface.
var _ housekeeping.CommandQueueCleaner = (*commands.Service)(nil)
