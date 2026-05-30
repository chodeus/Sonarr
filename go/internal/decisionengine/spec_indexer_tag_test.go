package decisionengine_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

type stubIndexerTagFinder struct {
	tags  []int
	found bool
	err   error
}

func (s stubIndexerTagFinder) IndexerTags(_ context.Context, _ int) ([]int, bool, error) {
	return s.tags, s.found, s.err
}

func makeIndexerTagSpec(t *testing.T, f decisionengine.IndexerTagFinder) *decisionengine.IndexerTagSpecification {
	t.Helper()
	spec, err := decisionengine.NewIndexerTagSpecification(f, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewIndexerTagSpecification: %v", err)
	}
	return spec
}

func indexerTagSubject(indexerID int, seriesTags ...int) *decisionengine.Subject {
	return &decisionengine.Subject{
		Series:  &series.Series{ID: 1, Tags: seriesTags},
		Release: &parser.ReleaseInfo{IndexerID: indexerID},
	}
}

func TestIndexerTagSpec_NilFinder_Error(t *testing.T) {
	t.Parallel()
	if _, err := decisionengine.NewIndexerTagSpecification(nil, nil); err == nil {
		t.Fatal("expected error on nil finder")
	}
}

func TestIndexerTagSpec_NoReleaseOrZeroIndexer_Accept(t *testing.T) {
	t.Parallel()
	spec := makeIndexerTagSpec(t, stubIndexerTagFinder{tags: []int{9}, found: true})
	info := decisionengine.NewReleaseDecisionInformation()
	// IndexerId == 0 → Accept (no indexer to check).
	if !spec.IsSatisfiedBy(t.Context(), indexerTagSubject(0, 1), info).Accepted {
		t.Error("IndexerId==0 should Accept")
	}
	// nil series → Accept.
	if !spec.IsSatisfiedBy(t.Context(), &decisionengine.Subject{Release: &parser.ReleaseInfo{IndexerID: 3}}, info).Accepted {
		t.Error("nil series should Accept")
	}
}

func TestIndexerTagSpec_IndexerNotFound_Accept(t *testing.T) {
	t.Parallel()
	spec := makeIndexerTagSpec(t, stubIndexerTagFinder{found: false})
	got := spec.IsSatisfiedBy(t.Context(), indexerTagSubject(3, 1), decisionengine.NewReleaseDecisionInformation())
	if !got.Accepted {
		t.Error("missing indexer should Accept (skip)")
	}
}

func TestIndexerTagSpec_UntaggedIndexer_Accept(t *testing.T) {
	t.Parallel()
	spec := makeIndexerTagSpec(t, stubIndexerTagFinder{tags: nil, found: true})
	got := spec.IsSatisfiedBy(t.Context(), indexerTagSubject(3 /* no series tags */), decisionengine.NewReleaseDecisionInformation())
	if !got.Accepted {
		t.Error("untagged indexer should Accept for any series")
	}
}

func TestIndexerTagSpec_MatchingTag_Accept(t *testing.T) {
	t.Parallel()
	spec := makeIndexerTagSpec(t, stubIndexerTagFinder{tags: []int{5, 9}, found: true})
	got := spec.IsSatisfiedBy(t.Context(), indexerTagSubject(3, 2, 9), decisionengine.NewReleaseDecisionInformation())
	if !got.Accepted {
		t.Errorf("shared tag (9) should Accept, got reject: %s", got.Message)
	}
}

func TestIndexerTagSpec_NoMatchingTag_Reject(t *testing.T) {
	t.Parallel()
	spec := makeIndexerTagSpec(t, stubIndexerTagFinder{tags: []int{5, 9}, found: true})
	got := spec.IsSatisfiedBy(t.Context(), indexerTagSubject(3, 2, 7), decisionengine.NewReleaseDecisionInformation())
	if got.Accepted || got.Reason != decisionengine.ReasonNoMatchingTag {
		t.Fatalf("disjoint tags should reject NoMatchingTag, got accepted=%v reason=%v", got.Accepted, got.Reason)
	}
}

func TestIndexerTagSpec_TaglessSeries_TaggedIndexer_Reject(t *testing.T) {
	t.Parallel()
	// C#-behavioral: a tag-less series is rejected by a tagged indexer
	// (empty intersection), NOT early-accepted.
	spec := makeIndexerTagSpec(t, stubIndexerTagFinder{tags: []int{5}, found: true})
	got := spec.IsSatisfiedBy(t.Context(), indexerTagSubject(3 /* no series tags */), decisionengine.NewReleaseDecisionInformation())
	if got.Accepted {
		t.Error("tag-less series must be rejected by a tagged indexer")
	}
}

func TestIndexerTagSpec_FinderError_AcceptDefensive(t *testing.T) {
	t.Parallel()
	spec := makeIndexerTagSpec(t, stubIndexerTagFinder{err: errors.New("db down")})
	got := spec.IsSatisfiedBy(t.Context(), indexerTagSubject(3, 1), decisionengine.NewReleaseDecisionInformation())
	if !got.Accepted {
		t.Error("lookup error should Accept defensively")
	}
}
