package decisionengine_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/providerstatus"
	"sonarr.tv/sonarr/internal/series"
)

// stubBlockedView is the minimal providerstatus.BlockedView test
// double. Counts calls so the cache-TTL tests can assert refresh
// behaviour.
type stubBlockedView struct {
	rows  []providerstatus.BlockedProvider
	err   error
	calls int32
}

func (s *stubBlockedView) GetBlocked(_ context.Context) ([]providerstatus.BlockedProvider, error) {
	atomic.AddInt32(&s.calls, 1)
	return s.rows, s.err
}

func (s *stubBlockedView) callCount() int { return int(atomic.LoadInt32(&s.calls)) }

func newBlockedSubject(indexerID int, indexer string) *decisionengine.Subject {
	rel := &parser.ReleaseInfo{
		Title:     "Show.S01E01",
		IndexerID: indexerID,
		Indexer:   indexer,
		Guid:      "guid-1",
	}
	return &decisionengine.Subject{
		Release: rel,
		Series:  &series.Series{ID: 1, Title: "Show"},
	}
}

func TestBlockedIndexerSpec_NilBlockedViewErrors(t *testing.T) {
	if _, err := decisionengine.NewBlockedIndexerSpecification(nil, nil); err == nil {
		t.Fatalf("expected error for nil BlockedView")
	}
}

func TestBlockedIndexerSpec_AcceptsWhenIndexerNotBlocked(t *testing.T) {
	view := &stubBlockedView{rows: []providerstatus.BlockedProvider{
		{ProviderID: 99, EscalationLevel: 1},
	}}
	spec, err := decisionengine.NewBlockedIndexerSpecification(view, nil)
	if err != nil {
		t.Fatalf("NewBlockedIndexerSpecification: %v", err)
	}
	dec := spec.IsSatisfiedBy(context.Background(), newBlockedSubject(7, "ok"), decisionengine.NewReleaseDecisionInformation())
	if !dec.Accepted {
		t.Errorf("expected accept, got %+v", dec)
	}
}

func TestBlockedIndexerSpec_RejectsBlockedIndexerTemporarily(t *testing.T) {
	till := time.Now().UTC().Add(30 * time.Minute)
	view := &stubBlockedView{rows: []providerstatus.BlockedProvider{
		{ProviderID: 7, DisabledTill: &till, EscalationLevel: 2},
	}}
	spec, err := decisionengine.NewBlockedIndexerSpecification(view, nil)
	if err != nil {
		t.Fatalf("NewBlockedIndexerSpecification: %v", err)
	}
	dec := spec.IsSatisfiedBy(context.Background(), newBlockedSubject(7, "DOGnzb"), decisionengine.NewReleaseDecisionInformation())
	if dec.Accepted {
		t.Fatalf("expected reject, got %+v", dec)
	}
	if dec.Reason != decisionengine.ReasonIndexerDisabled {
		t.Errorf("reason = %v, want ReasonIndexerDisabled", dec.Reason)
	}
	// Spec.Kind() carries the Temporary tag (the orchestrator
	// translates SpecDecision into Rejection at the boundary; the
	// kind tagging is on the Specification, not on the SpecDecision).
	// Message should mention the indexer name + cooldown deadline.
	if dec.Message == "" {
		t.Errorf("expected non-empty message")
	}
}

func TestBlockedIndexerSpec_NilDisabledTillRendersUnknown(t *testing.T) {
	// DisabledTill nil but EscalationLevel > 0 — recorder shape allows
	// this transition shape briefly between RecordFailure and the
	// DisabledTill computation. Spec must still reject.
	view := &stubBlockedView{rows: []providerstatus.BlockedProvider{
		{ProviderID: 7, EscalationLevel: 1},
	}}
	spec, err := decisionengine.NewBlockedIndexerSpecification(view, nil)
	if err != nil {
		t.Fatalf("NewBlockedIndexerSpecification: %v", err)
	}
	dec := spec.IsSatisfiedBy(context.Background(), newBlockedSubject(7, "X"), decisionengine.NewReleaseDecisionInformation())
	if dec.Accepted {
		t.Fatalf("expected reject, got %+v", dec)
	}
}

func TestBlockedIndexerSpec_NilSubjectAccepts(t *testing.T) {
	view := &stubBlockedView{}
	spec, _ := decisionengine.NewBlockedIndexerSpecification(view, nil)
	dec := spec.IsSatisfiedBy(context.Background(), nil, decisionengine.NewReleaseDecisionInformation())
	if !dec.Accepted {
		t.Errorf("nil subject should accept")
	}
}

func TestBlockedIndexerSpec_ZeroIndexerIDAccepts(t *testing.T) {
	view := &stubBlockedView{rows: []providerstatus.BlockedProvider{
		{ProviderID: 0, EscalationLevel: 1},
	}}
	spec, _ := decisionengine.NewBlockedIndexerSpecification(view, nil)
	dec := spec.IsSatisfiedBy(context.Background(), newBlockedSubject(0, ""), decisionengine.NewReleaseDecisionInformation())
	if !dec.Accepted {
		t.Errorf("zero indexerID should accept")
	}
}

func TestBlockedIndexerSpec_BlockedViewErrorAcceptsDefensively(t *testing.T) {
	view := &stubBlockedView{err: errors.New("db gone")}
	spec, _ := decisionengine.NewBlockedIndexerSpecification(view, nil)
	dec := spec.IsSatisfiedBy(context.Background(), newBlockedSubject(7, "X"), decisionengine.NewReleaseDecisionInformation())
	if !dec.Accepted {
		t.Errorf("BlockedView error should accept defensively, got %+v", dec)
	}
}

func TestBlockedIndexerSpec_CachesBlockedViewWithinTTL(t *testing.T) {
	view := &stubBlockedView{rows: []providerstatus.BlockedProvider{{ProviderID: 7, EscalationLevel: 1}}}
	spec, _ := decisionengine.NewBlockedIndexerSpecification(view, nil)

	// First call → fetches once.
	_ = spec.IsSatisfiedBy(context.Background(), newBlockedSubject(7, "X"), decisionengine.NewReleaseDecisionInformation())
	// Second call within TTL → no refetch.
	_ = spec.IsSatisfiedBy(context.Background(), newBlockedSubject(7, "Y"), decisionengine.NewReleaseDecisionInformation())
	if got := view.callCount(); got != 1 {
		t.Errorf("expected 1 GetBlocked call within TTL, got %d", got)
	}
}

// Constants (mirror unexported), enabling explicit name guard.
func TestBlockedIndexerSpec_NameAndPriority(t *testing.T) {
	view := &stubBlockedView{}
	spec, _ := decisionengine.NewBlockedIndexerSpecification(view, nil)
	if spec.Name() != decisionengine.BlockedIndexerSpecificationName {
		t.Errorf("Name() = %q, want %q", spec.Name(), decisionengine.BlockedIndexerSpecificationName)
	}
	if spec.Kind() != decisionengine.RejectionTemporary {
		t.Errorf("Kind() = %v, want Temporary", spec.Kind())
	}
	if spec.Priority() != decisionengine.SpecPriorityDatabase {
		t.Errorf("Priority() = %v, want Database", spec.Priority())
	}
}
