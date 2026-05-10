package aggregation_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/aggregation"
	"sonarr.tv/sonarr/internal/decisionengine"
)

// stubAugmenter is a per-call-tracking Augmenter for verifying the
// orchestrator's iteration order + error-tolerance contract.
type stubAugmenter struct {
	name   string
	err    error
	called int
	// mutate applies an in-place change so tests can verify the
	// Subject reference is shared between augmenters.
	mutate func(*decisionengine.Subject)
}

func (s *stubAugmenter) String() string { return s.name }

func (s *stubAugmenter) Aggregate(_ context.Context, sub *decisionengine.Subject) error {
	s.called++
	if s.mutate != nil {
		s.mutate(sub)
	}
	return s.err
}

func TestNewServiceRejectsNilAugmenter(t *testing.T) {
	t.Parallel()
	_, err := aggregation.NewService([]aggregation.Augmenter{nil}, slog.Default())
	if err == nil {
		t.Fatal("nil augmenter accepted; want error")
	}
}

func TestNewServiceNoAugmentersIsNoop(t *testing.T) {
	t.Parallel()
	svc, err := aggregation.NewService(nil, slog.Default())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	sub := decisionengine.NewSubject()
	got := svc.Augment(context.Background(), sub)
	if got != sub {
		t.Errorf("Augment returned %v, want input pointer %v", got, sub)
	}
}

func TestNewServiceNilLogUsesDefault(t *testing.T) {
	t.Parallel()
	_, err := aggregation.NewService(nil, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
}

func TestAugmentNilSubjectReturnsNil(t *testing.T) {
	t.Parallel()
	a := &stubAugmenter{name: "A"}
	svc, err := aggregation.NewService([]aggregation.Augmenter{a}, slog.Default())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if got := svc.Augment(context.Background(), nil); got != nil {
		t.Errorf("Augment(nil) = %v, want nil", got)
	}
	if a.called != 0 {
		t.Errorf("augmenter called %d times on nil subject; want 0", a.called)
	}
}

// All augmenters run in order, even when an earlier one errors. The
// Subject pointer is shared (in-place mutations stick).
func TestAugmentRunsEveryAugmenterAndPropagatesMutations(t *testing.T) {
	t.Parallel()
	a := &stubAugmenter{name: "A", err: errors.New("a failed")}
	b := &stubAugmenter{
		name: "B",
		mutate: func(s *decisionengine.Subject) {
			s.MappedSeasonNumber = 99
		},
	}
	c := &stubAugmenter{name: "C"}
	svc, err := aggregation.NewService([]aggregation.Augmenter{a, b, c}, slog.Default())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	sub := decisionengine.NewSubject()
	got := svc.Augment(context.Background(), sub)

	if got != sub {
		t.Errorf("Augment returned different pointer: %v vs %v", got, sub)
	}
	if a.called != 1 {
		t.Errorf("a.called = %d, want 1", a.called)
	}
	if b.called != 1 {
		t.Errorf("b.called = %d (a's error didn't halt pipeline as expected)", b.called)
	}
	if c.called != 1 {
		t.Errorf("c.called = %d, want 1", c.called)
	}
	if sub.MappedSeasonNumber != 99 {
		t.Errorf("MappedSeasonNumber = %d, want 99 (b's mutation lost)", sub.MappedSeasonNumber)
	}
}

func TestAugmentNilServiceReturnsSubjectUnchanged(t *testing.T) {
	t.Parallel()
	var svc *aggregation.Service
	sub := decisionengine.NewSubject()
	sub.MappedSeasonNumber = 7
	got := svc.Augment(context.Background(), sub)
	if got != sub || got.MappedSeasonNumber != 7 {
		t.Errorf("nil receiver path mutated subject")
	}
}
