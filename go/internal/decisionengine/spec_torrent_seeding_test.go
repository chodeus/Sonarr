package decisionengine_test

import (
	"context"
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

// stubMinSeedersReader implements MinimumSeedersReader.
type stubMinSeedersReader struct {
	min     int
	ok      bool
	err     error
	callIDs []int
}

func (s *stubMinSeedersReader) MinimumSeeders(_ context.Context, id int) (int, bool, error) {
	s.callIDs = append(s.callIDs, id)
	return s.min, s.ok, s.err
}

func newTorrentSubject(indexerID int, seeders *int) *decisionengine.Subject {
	t := &parser.TorrentInfo{
		ReleaseInfo: parser.ReleaseInfo{
			Title:     "Show.S01E01.1080p",
			IndexerID: indexerID,
			Indexer:   "torznab",
			Guid:      "guid-1",
		},
		Seeders: seeders,
	}
	return &decisionengine.Subject{
		Release: t,
		Series:  &series.Series{ID: 1, Title: "Show"},
	}
}

func intp(n int) *int { return &n }

func TestTorrentSeedingSpec_NilReaderErrors(t *testing.T) {
	if _, err := decisionengine.NewTorrentSeedingSpecification(nil, nil); err == nil {
		t.Fatalf("expected error for nil MinimumSeedersReader")
	}
}

func TestTorrentSeedingSpec_NonTorrentReleaseAccepts(t *testing.T) {
	rel := &parser.ReleaseInfo{Title: "X", IndexerID: 7, Guid: "g"}
	subject := &decisionengine.Subject{Release: rel, Series: &series.Series{ID: 1}}
	reader := &stubMinSeedersReader{min: 5, ok: true}
	spec, _ := decisionengine.NewTorrentSeedingSpecification(reader, nil)
	dec := spec.IsSatisfiedBy(context.Background(), subject, decisionengine.NewReleaseDecisionInformation())
	if !dec.Accepted {
		t.Errorf("non-torrent release should accept, got %+v", dec)
	}
	if len(reader.callIDs) != 0 {
		t.Errorf("non-torrent should NOT call MinimumSeeders, got %v", reader.callIDs)
	}
}

func TestTorrentSeedingSpec_ZeroIndexerIDAccepts(t *testing.T) {
	subject := newTorrentSubject(0, intp(2))
	reader := &stubMinSeedersReader{min: 5, ok: true}
	spec, _ := decisionengine.NewTorrentSeedingSpecification(reader, nil)
	dec := spec.IsSatisfiedBy(context.Background(), subject, decisionengine.NewReleaseDecisionInformation())
	if !dec.Accepted {
		t.Errorf("zero IndexerID should accept, got %+v", dec)
	}
	if len(reader.callIDs) != 0 {
		t.Errorf("expected no MinimumSeeders calls for zero ID")
	}
}

func TestTorrentSeedingSpec_NotFoundAccepts(t *testing.T) {
	subject := newTorrentSubject(99, intp(2))
	reader := &stubMinSeedersReader{err: decisionengine.ErrTorrentIndexerNotFound}
	spec, _ := decisionengine.NewTorrentSeedingSpecification(reader, nil)
	dec := spec.IsSatisfiedBy(context.Background(), subject, decisionengine.NewReleaseDecisionInformation())
	if !dec.Accepted {
		t.Errorf("ErrTorrentIndexerNotFound should accept (mirrors C# ModelNotFoundException catch), got %+v", dec)
	}
}

func TestTorrentSeedingSpec_ReaderErrorAcceptsDefensively(t *testing.T) {
	subject := newTorrentSubject(7, intp(2))
	reader := &stubMinSeedersReader{err: errors.New("transient db error")}
	spec, _ := decisionengine.NewTorrentSeedingSpecification(reader, nil)
	dec := spec.IsSatisfiedBy(context.Background(), subject, decisionengine.NewReleaseDecisionInformation())
	if !dec.Accepted {
		t.Errorf("transient reader error should accept defensively, got %+v", dec)
	}
}

func TestTorrentSeedingSpec_NonTorrentSettingsAccepts(t *testing.T) {
	subject := newTorrentSubject(7, intp(2))
	// ok=false → indexer is non-torrent (Newznab). Spec must accept.
	reader := &stubMinSeedersReader{min: 0, ok: false}
	spec, _ := decisionengine.NewTorrentSeedingSpecification(reader, nil)
	dec := spec.IsSatisfiedBy(context.Background(), subject, decisionengine.NewReleaseDecisionInformation())
	if !dec.Accepted {
		t.Errorf("non-torrent indexer settings should accept, got %+v", dec)
	}
}

func TestTorrentSeedingSpec_NilSeedersAccepts(t *testing.T) {
	subject := newTorrentSubject(7, nil)
	reader := &stubMinSeedersReader{min: 5, ok: true}
	spec, _ := decisionengine.NewTorrentSeedingSpecification(reader, nil)
	dec := spec.IsSatisfiedBy(context.Background(), subject, decisionengine.NewReleaseDecisionInformation())
	if !dec.Accepted {
		t.Errorf("nil seeders should accept (mirrors HasValue guard), got %+v", dec)
	}
}

func TestTorrentSeedingSpec_RejectsBelowMinimum(t *testing.T) {
	subject := newTorrentSubject(7, intp(2))
	reader := &stubMinSeedersReader{min: 5, ok: true}
	spec, _ := decisionengine.NewTorrentSeedingSpecification(reader, nil)
	dec := spec.IsSatisfiedBy(context.Background(), subject, decisionengine.NewReleaseDecisionInformation())
	if dec.Accepted {
		t.Fatalf("expected reject (seeders<min), got accepted")
	}
	if dec.Reason != decisionengine.ReasonMinimumSeeders {
		t.Errorf("reason = %v, want ReasonMinimumSeeders", dec.Reason)
	}
	if dec.Message == "" {
		t.Errorf("expected non-empty message")
	}
}

func TestTorrentSeedingSpec_AcceptsAtThreshold(t *testing.T) {
	subject := newTorrentSubject(7, intp(5))
	reader := &stubMinSeedersReader{min: 5, ok: true}
	spec, _ := decisionengine.NewTorrentSeedingSpecification(reader, nil)
	dec := spec.IsSatisfiedBy(context.Background(), subject, decisionengine.NewReleaseDecisionInformation())
	if !dec.Accepted {
		t.Errorf("seeders==min should accept (strict <), got %+v", dec)
	}
}

func TestTorrentSeedingSpec_AcceptsAboveThreshold(t *testing.T) {
	subject := newTorrentSubject(7, intp(50))
	reader := &stubMinSeedersReader{min: 5, ok: true}
	spec, _ := decisionengine.NewTorrentSeedingSpecification(reader, nil)
	dec := spec.IsSatisfiedBy(context.Background(), subject, decisionengine.NewReleaseDecisionInformation())
	if !dec.Accepted {
		t.Errorf("seeders>min should accept, got %+v", dec)
	}
}

func TestTorrentSeedingSpec_NameKindPriority(t *testing.T) {
	reader := &stubMinSeedersReader{}
	spec, _ := decisionengine.NewTorrentSeedingSpecification(reader, nil)
	if spec.Name() != decisionengine.TorrentSeedingSpecificationName {
		t.Errorf("Name() = %q", spec.Name())
	}
	if spec.Kind() != decisionengine.RejectionPermanent {
		t.Errorf("Kind() = %v, want Permanent", spec.Kind())
	}
	if spec.Priority() != decisionengine.SpecPriorityDefault {
		t.Errorf("Priority() = %v, want Default", spec.Priority())
	}
}
