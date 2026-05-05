package decisionengine_test

import (
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/parser"
)

// === RawDiskSpecification ===

func TestRawDisk_AcceptsNilRelease(t *testing.T) {
	t.Parallel()
	spec := decisionengine.NewRawDiskSpecification(slog.New(slog.DiscardHandler))
	got := spec.IsSatisfiedBy(t.Context(), &decisionengine.Subject{}, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on nil-release, got %+v", got)
	}
}

func TestRawDisk_RejectsDiscNumberedBlurayTitle(t *testing.T) {
	t.Parallel()
	spec := decisionengine.NewRawDiskSpecification(slog.New(slog.DiscardHandler))
	subject := &decisionengine.Subject{
		Release: &parser.ReleaseInfo{Title: "Show.Name.S01.Disc.1.1080p.BluRay"},
	}
	got := spec.IsSatisfiedBy(t.Context(), subject, decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject on disc-numbered title, got Accept")
	}
	if got.Reason != decisionengine.ReasonRaw {
		t.Errorf("Reason = %v, want ReasonRaw", got.Reason)
	}
}

func TestRawDisk_RejectsFullBlurayTitle(t *testing.T) {
	t.Parallel()
	spec := decisionengine.NewRawDiskSpecification(slog.New(slog.DiscardHandler))
	subject := &decisionengine.Subject{
		Release: &parser.ReleaseInfo{Title: "Movie.Name.1080p.Full.Bluray"},
	}
	got := spec.IsSatisfiedBy(t.Context(), subject, decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject on Full Bluray title, got Accept")
	}
}

func TestRawDisk_RejectsDvdTitle(t *testing.T) {
	t.Parallel()
	spec := decisionengine.NewRawDiskSpecification(slog.New(slog.DiscardHandler))
	subject := &decisionengine.Subject{
		Release: &parser.ReleaseInfo{Title: "Movie.Name.DVD-R "},
	}
	got := spec.IsSatisfiedBy(t.Context(), subject, decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject on DVD-R title, got Accept")
	}
}

func TestRawDisk_RejectsVobContainer(t *testing.T) {
	t.Parallel()
	spec := decisionengine.NewRawDiskSpecification(slog.New(slog.DiscardHandler))
	subject := &decisionengine.Subject{
		Release: &parser.ReleaseInfo{Title: "Show.Name", Container: "VOB"},
	}
	got := spec.IsSatisfiedBy(t.Context(), subject, decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject on VOB container, got Accept")
	}
}

func TestRawDisk_RejectsM2tsContainer(t *testing.T) {
	t.Parallel()
	spec := decisionengine.NewRawDiskSpecification(slog.New(slog.DiscardHandler))
	subject := &decisionengine.Subject{
		Release: &parser.ReleaseInfo{Title: "Show.Name", Container: "m2ts"},
	}
	got := spec.IsSatisfiedBy(t.Context(), subject, decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject on m2ts container, got Accept")
	}
}

func TestRawDisk_AcceptsNormalTitleAndContainer(t *testing.T) {
	t.Parallel()
	spec := decisionengine.NewRawDiskSpecification(slog.New(slog.DiscardHandler))
	subject := &decisionengine.Subject{
		Release: &parser.ReleaseInfo{Title: "Show.Name.S01E01.1080p.BluRay.x264", Container: "MKV"},
	}
	got := spec.IsSatisfiedBy(t.Context(), subject, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on normal release, got %+v", got)
	}
}

func TestRawDisk_AcceptsEmptyContainer(t *testing.T) {
	t.Parallel()
	spec := decisionengine.NewRawDiskSpecification(slog.New(slog.DiscardHandler))
	subject := &decisionengine.Subject{
		Release: &parser.ReleaseInfo{Title: "Show.Name.S01E01.1080p.WEB-DL.x264", Container: ""},
	}
	got := spec.IsSatisfiedBy(t.Context(), subject, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on empty container, got %+v", got)
	}
}

func TestRawDisk_TorrentInfoFlowsThrough(t *testing.T) {
	t.Parallel()
	spec := decisionengine.NewRawDiskSpecification(slog.New(slog.DiscardHandler))
	subject := &decisionengine.Subject{
		Release: &parser.TorrentInfo{ReleaseInfo: parser.ReleaseInfo{Title: "Movie.Disc.01.1080p.BluRay"}},
	}
	got := spec.IsSatisfiedBy(t.Context(), subject, decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject on TorrentInfo with raw-disc title")
	}
}

func TestRawDisk_InterfaceShape(t *testing.T) {
	t.Parallel()
	var spec decisionengine.Specification = decisionengine.NewRawDiskSpecification(nil)
	if spec.Name() != decisionengine.RawDiskSpecificationName {
		t.Errorf("Name = %q", spec.Name())
	}
	if spec.Kind() != decisionengine.RejectionPermanent {
		t.Errorf("Kind = %v", spec.Kind())
	}
	if spec.Priority() != decisionengine.SpecPriorityDefault {
		t.Errorf("Priority = %v", spec.Priority())
	}
}

// === NotSampleSpecification ===

func TestNotSample_RejectsSmallSample(t *testing.T) {
	t.Parallel()
	spec := decisionengine.NewNotSampleSpecification(slog.New(slog.DiscardHandler))
	subject := &decisionengine.Subject{
		Release: &parser.ReleaseInfo{
			Title: "Show.Name.S01E01.SAMPLE.x264",
			Size:  50 * 1024 * 1024, // 50MB, below threshold
		},
	}
	got := spec.IsSatisfiedBy(t.Context(), subject, decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject on small sample, got Accept")
	}
	if got.Reason != decisionengine.ReasonSample {
		t.Errorf("Reason = %v, want ReasonSample", got.Reason)
	}
}

func TestNotSample_AcceptsLargeSample(t *testing.T) {
	t.Parallel()
	spec := decisionengine.NewNotSampleSpecification(slog.New(slog.DiscardHandler))
	subject := &decisionengine.Subject{
		Release: &parser.ReleaseInfo{
			Title: "Show.Sample.With.Real.Content",
			Size:  5_000_000_000, // 5GB
		},
	}
	got := spec.IsSatisfiedBy(t.Context(), subject, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on large 'sample'-named release, got %+v", got)
	}
}

func TestNotSample_AcceptsSmallNonSample(t *testing.T) {
	t.Parallel()
	spec := decisionengine.NewNotSampleSpecification(slog.New(slog.DiscardHandler))
	subject := &decisionengine.Subject{
		Release: &parser.ReleaseInfo{
			Title: "Show.Name.S01E01.x264",
			Size:  10 * 1024 * 1024, // 10MB but no sample token
		},
	}
	got := spec.IsSatisfiedBy(t.Context(), subject, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on small non-sample, got %+v", got)
	}
}

// TestNotSample_BoundaryAtThreshold pins the strict-less-than check.
// At exactly 70MB the release passes; one byte below it fails.
func TestNotSample_BoundaryAtThreshold(t *testing.T) {
	t.Parallel()
	spec := decisionengine.NewNotSampleSpecification(slog.New(slog.DiscardHandler))
	atThreshold := &decisionengine.Subject{
		Release: &parser.ReleaseInfo{Title: "sample", Size: decisionengine.NotSampleMaxBytes},
	}
	if got := spec.IsSatisfiedBy(t.Context(), atThreshold, decisionengine.ReleaseDecisionInformation{}); !got.Accepted {
		t.Errorf("at threshold: expected Accept (boundary is < not <=), got %+v", got)
	}
	below := &decisionengine.Subject{
		Release: &parser.ReleaseInfo{Title: "sample", Size: decisionengine.NotSampleMaxBytes - 1},
	}
	if got := spec.IsSatisfiedBy(t.Context(), below, decisionengine.ReleaseDecisionInformation{}); got.Accepted {
		t.Errorf("one byte below threshold: expected Reject, got Accept")
	}
}

func TestNotSample_AcceptsNilRelease(t *testing.T) {
	t.Parallel()
	spec := decisionengine.NewNotSampleSpecification(slog.New(slog.DiscardHandler))
	got := spec.IsSatisfiedBy(t.Context(), &decisionengine.Subject{}, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on nil-release, got %+v", got)
	}
}

func TestNotSample_InterfaceShape(t *testing.T) {
	t.Parallel()
	var spec decisionengine.Specification = decisionengine.NewNotSampleSpecification(nil)
	if spec.Name() != decisionengine.NotSampleSpecificationName {
		t.Errorf("Name = %q", spec.Name())
	}
	if spec.Kind() != decisionengine.RejectionPermanent {
		t.Errorf("Kind = %v", spec.Kind())
	}
}
