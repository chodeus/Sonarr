package decisionengine_test

import (
	"errors"
	"io/fs"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

// === FreeSpaceSpecification ===

type stubFreeSpaceCfg struct {
	skip  bool
	minMB int
}

func (s stubFreeSpaceCfg) SkipFreeSpaceCheckWhenImporting() bool { return s.skip }
func (s stubFreeSpaceCfg) MinimumFreeSpaceWhenImporting() int    { return s.minMB }

type stubFreeSpaceDisk struct {
	free int64
	err  error
}

func (s stubFreeSpaceDisk) GetAvailableSpace(_ string) (int64, error) {
	return s.free, s.err
}

func newFreeSpaceSpec(t *testing.T, skip bool, minMB int, free int64, diskErr error) *decisionengine.FreeSpaceSpecification {
	t.Helper()
	spec, err := decisionengine.NewFreeSpaceSpecification(
		stubFreeSpaceCfg{skip: skip, minMB: minMB},
		stubFreeSpaceDisk{free: free, err: diskErr},
		slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatalf("NewFreeSpaceSpecification: %v", err)
	}
	return spec
}

func freeSpaceSubject(seriesPath string, releaseSize int64) *decisionengine.Subject {
	return &decisionengine.Subject{
		Series: &series.Series{ID: 7, Path: seriesPath},
		Release: &parser.ReleaseInfo{
			Title: "Show.S01E01",
			Size:  releaseSize,
		},
	}
}

func TestFreeSpace_NewRejectsNilDeps(t *testing.T) {
	t.Parallel()
	if _, err := decisionengine.NewFreeSpaceSpecification(nil, stubFreeSpaceDisk{}, nil); err == nil {
		t.Errorf("expected error on nil cfg")
	}
	if _, err := decisionengine.NewFreeSpaceSpecification(stubFreeSpaceCfg{}, nil, nil); err == nil {
		t.Errorf("expected error on nil disk")
	}
}

func TestFreeSpace_AcceptsWhenSkipEnabled(t *testing.T) {
	t.Parallel()
	spec := newFreeSpaceSpec(t, true, 100, 0, nil)
	got := spec.IsSatisfiedBy(t.Context(), freeSpaceSubject("/tv", 999999999),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept when skip enabled, got %+v", got)
	}
}

func TestFreeSpace_AcceptsWhenDiskLookupFails(t *testing.T) {
	t.Parallel()
	spec := newFreeSpaceSpec(t, false, 100, 0, errors.New("permission denied"))
	got := spec.IsSatisfiedBy(t.Context(), freeSpaceSubject("/tv", 1000),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected defensive Accept on disk error, got %+v", got)
	}
}

func TestFreeSpace_AcceptsWhenSeriesPathMissing(t *testing.T) {
	t.Parallel()
	spec := newFreeSpaceSpec(t, false, 100, 0, fs.ErrNotExist)
	got := spec.IsSatisfiedBy(t.Context(), freeSpaceSubject("/missing", 1000),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on ErrNotExist, got %+v", got)
	}
}

func TestFreeSpace_RejectsWhenWouldExhaust(t *testing.T) {
	t.Parallel()
	// 1 MB free, 2 MB release → remainingSpace negative.
	spec := newFreeSpaceSpec(t, false, 0, 1*1024*1024, nil)
	got := spec.IsSatisfiedBy(t.Context(),
		freeSpaceSubject("/tv", 2*1024*1024),
		decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject when import would exhaust disk, got Accept")
	}
	if got.Reason != decisionengine.ReasonMinimumFreeSpace {
		t.Errorf("Reason = %v, want ReasonMinimumFreeSpace", got.Reason)
	}
}

func TestFreeSpace_RejectsBelowMinimum(t *testing.T) {
	t.Parallel()
	// 200 MB free, 50 MB release → 150 MB remaining; min 200 MB → reject.
	spec := newFreeSpaceSpec(t, false, 200, 200*1024*1024, nil)
	got := spec.IsSatisfiedBy(t.Context(),
		freeSpaceSubject("/tv", 50*1024*1024),
		decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject below minimum, got Accept")
	}
	if got.Reason != decisionengine.ReasonMinimumFreeSpace {
		t.Errorf("Reason = %v, want ReasonMinimumFreeSpace", got.Reason)
	}
}

func TestFreeSpace_AcceptsAboveMinimum(t *testing.T) {
	t.Parallel()
	// 500 MB free, 50 MB release → 450 MB remaining; min 100 MB → accept.
	spec := newFreeSpaceSpec(t, false, 100, 500*1024*1024, nil)
	got := spec.IsSatisfiedBy(t.Context(),
		freeSpaceSubject("/tv", 50*1024*1024),
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept above minimum, got %+v", got)
	}
}

func TestFreeSpace_NilSubjectAccepts(t *testing.T) {
	t.Parallel()
	spec := newFreeSpaceSpec(t, false, 100, 0, nil)
	got := spec.IsSatisfiedBy(t.Context(), nil, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on nil subject, got %+v", got)
	}
}

func TestFreeSpace_NilSeriesAccepts(t *testing.T) {
	t.Parallel()
	spec := newFreeSpaceSpec(t, false, 100, 0, nil)
	got := spec.IsSatisfiedBy(t.Context(),
		&decisionengine.Subject{Release: &parser.ReleaseInfo{Title: "x"}},
		decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on nil Series, got %+v", got)
	}
}

func TestFreeSpace_Metadata(t *testing.T) {
	t.Parallel()
	spec := newFreeSpaceSpec(t, false, 0, 0, nil)
	if spec.Name() != "FreeSpace" {
		t.Errorf("Name = %q, want FreeSpace", spec.Name())
	}
	if spec.Kind() != decisionengine.RejectionPermanent {
		t.Errorf("Kind = %v, want Permanent", spec.Kind())
	}
	if spec.Priority() != decisionengine.SpecPriorityDisk {
		t.Errorf("Priority = %v, want Disk", spec.Priority())
	}
}
