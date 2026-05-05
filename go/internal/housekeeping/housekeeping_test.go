package housekeeping_test

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/housekeeping"
)

// fakeTask is a minimal Task that records invocation count and
// optionally returns an error.
type fakeTask struct {
	name     string
	err      error
	calls    atomic.Int32
	delegate func(ctx context.Context) error
}

func (f *fakeTask) Name() string { return f.name }
func (f *fakeTask) Clean(ctx context.Context) error {
	f.calls.Add(1)
	if f.delegate != nil {
		return f.delegate(ctx)
	}
	return f.err
}

func TestNewService_NilTaskRejected(t *testing.T) {
	t.Parallel()
	if _, err := housekeeping.NewService([]housekeeping.Task{nil}, slog.Default()); err == nil {
		t.Errorf("NewService(nil task): want error")
	}
}

func TestService_Clean_RunsAllTasksInOrder(t *testing.T) {
	t.Parallel()
	var order []string
	a := &fakeTask{
		name:     "A",
		delegate: func(_ context.Context) error { order = append(order, "A"); return nil },
	}
	b := &fakeTask{
		name:     "B",
		delegate: func(_ context.Context) error { order = append(order, "B"); return nil },
	}
	c := &fakeTask{
		name:     "C",
		delegate: func(_ context.Context) error { order = append(order, "C"); return nil },
	}
	svc, err := housekeeping.NewService([]housekeeping.Task{a, b, c}, slog.Default())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := svc.Clean(t.Context()); err != nil {
		t.Errorf("Clean: %v", err)
	}
	want := []string{"A", "B", "C"}
	if len(order) != 3 || order[0] != want[0] || order[1] != want[1] || order[2] != want[2] {
		t.Errorf("order: %v, want %v", order, want)
	}
}

func TestService_Clean_PerTaskFailureContinues(t *testing.T) {
	t.Parallel()
	want := errors.New("kaboom")
	a := &fakeTask{name: "A"}
	b := &fakeTask{name: "B", err: want}
	c := &fakeTask{name: "C"}
	svc, err := housekeeping.NewService([]housekeeping.Task{a, b, c}, slog.Default())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	got := svc.Clean(t.Context())
	if !errors.Is(got, want) {
		t.Errorf("Clean: %v, want wrapping %v", got, want)
	}
	if a.calls.Load() != 1 || b.calls.Load() != 1 || c.calls.Load() != 1 {
		t.Errorf("calls: A=%d B=%d C=%d, want all 1", a.calls.Load(), b.calls.Load(), c.calls.Load())
	}
}

func TestService_Clean_NoTasks(t *testing.T) {
	t.Parallel()
	svc, err := housekeeping.NewService(nil, slog.Default())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := svc.Clean(t.Context()); err != nil {
		t.Errorf("Clean(empty): %v", err)
	}
}

func TestService_Clean_FirstErrorReturned(t *testing.T) {
	t.Parallel()
	first := errors.New("first")
	second := errors.New("second")
	a := &fakeTask{name: "A", err: first}
	b := &fakeTask{name: "B", err: second}
	svc, err := housekeeping.NewService([]housekeeping.Task{a, b}, slog.Default())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	got := svc.Clean(t.Context())
	if !errors.Is(got, first) {
		t.Errorf("Clean: %v, want wrapping first %v", got, first)
	}
	if errors.Is(got, second) {
		t.Errorf("Clean: %v, should not wrap second %v", got, second)
	}
}
