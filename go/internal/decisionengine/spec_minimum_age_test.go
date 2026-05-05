package decisionengine_test

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/parser"
)

// stubMinimumAge is the minimal MinimumAgeReader implementation used
// by the spec tests.
type stubMinimumAge struct{ minutes int }

func (s stubMinimumAge) MinimumAge() int { return s.minutes }

// newMinimumAgeSpec builds a MinimumAgeSpecification with a stub
// reader and a discarded logger.
func newMinimumAgeSpec(t *testing.T, minMinutes int) *decisionengine.MinimumAgeSpecification {
	t.Helper()
	spec, err := decisionengine.NewMinimumAgeSpecification(stubMinimumAge{minutes: minMinutes}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewMinimumAgeSpecification: %v", err)
	}
	return spec
}

// usenetRelease builds a *parser.ReleaseInfo with PublishDate set to
// `now - ageMinutes minutes` and DownloadProtocol pinned to Usenet.
// Mirrors the C# fixture's WithAge helper.
func usenetRelease(ageMinutes int) *parser.ReleaseInfo {
	return &parser.ReleaseInfo{
		Title:            "Show.S01E01",
		PublishDate:      time.Now().Add(-time.Duration(ageMinutes) * time.Minute),
		DownloadProtocol: indexers.DownloadProtocolUsenet,
	}
}

func TestMinimumAge_NewRejectsNilConfig(t *testing.T) {
	t.Parallel()
	if _, err := decisionengine.NewMinimumAgeSpecification(nil, nil); err == nil {
		t.Fatalf("expected error on nil config, got nil")
	}
}

func TestMinimumAge_AcceptsWhenMinIsZero(t *testing.T) {
	t.Parallel()
	spec := newMinimumAgeSpec(t, 0)
	subject := &decisionengine.Subject{Release: usenetRelease(100)}
	got := spec.IsSatisfiedBy(t.Context(), subject, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept when MinimumAge=0, got %+v", got)
	}
}

func TestMinimumAge_AcceptsWhenAgeAboveMin(t *testing.T) {
	t.Parallel()
	spec := newMinimumAgeSpec(t, 30)
	subject := &decisionengine.Subject{Release: usenetRelease(100)}
	got := spec.IsSatisfiedBy(t.Context(), subject, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept when age > min, got %+v", got)
	}
}

func TestMinimumAge_RejectsWhenAgeBelowMin(t *testing.T) {
	t.Parallel()
	spec := newMinimumAgeSpec(t, 30)
	subject := &decisionengine.Subject{Release: usenetRelease(10)}
	got := spec.IsSatisfiedBy(t.Context(), subject, decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject when age < min, got Accept")
	}
	if got.Reason != decisionengine.ReasonMinimumAge {
		t.Errorf("Reason = %v, want ReasonMinimumAge", got.Reason)
	}
	if !strings.Contains(got.Message, "minimum age is 30 minutes") {
		t.Errorf("Message = %q, want to contain min-age phrase", got.Message)
	}
	if !strings.Contains(got.Message, "Only ") {
		t.Errorf("Message = %q, want to start with 'Only'", got.Message)
	}
}

func TestMinimumAge_AcceptsNonUsenetReleases(t *testing.T) {
	t.Parallel()
	// Torrent releases skip the check entirely — gated by
	// seeders/peers instead. Even an "age = 0" usenet release at
	// MinimumAge=30 would be rejected; flipping the protocol to
	// Torrent should accept it.
	spec := newMinimumAgeSpec(t, 30)
	rel := usenetRelease(0)
	rel.DownloadProtocol = indexers.DownloadProtocolTorrent
	subject := &decisionengine.Subject{Release: rel}
	got := spec.IsSatisfiedBy(t.Context(), subject, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on Torrent release, got %+v", got)
	}
}

func TestMinimumAge_AcceptsUnknownProtocol(t *testing.T) {
	t.Parallel()
	// Belt-and-braces: the C# `!= Usenet` predicate is false for
	// the Unknown zero-value too. A release missing protocol metadata
	// passes — the C# tree treats unknown as "not usenet, leave alone."
	spec := newMinimumAgeSpec(t, 30)
	rel := usenetRelease(0)
	rel.DownloadProtocol = indexers.DownloadProtocolUnknown
	subject := &decisionengine.Subject{Release: rel}
	got := spec.IsSatisfiedBy(t.Context(), subject, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on Unknown protocol, got %+v", got)
	}
}

func TestMinimumAge_AcceptsNilRelease(t *testing.T) {
	t.Parallel()
	spec := newMinimumAgeSpec(t, 30)
	got := spec.IsSatisfiedBy(t.Context(), &decisionengine.Subject{}, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on nil-release, got %+v", got)
	}
}

func TestMinimumAge_TorrentInfoFlowsThrough(t *testing.T) {
	t.Parallel()
	// *parser.TorrentInfo carries DownloadProtocol via the embedded
	// ReleaseInfo. A Usenet-protocol TorrentInfo (atypical but legal)
	// should still be checked.
	spec := newMinimumAgeSpec(t, 30)
	rel := usenetRelease(5)
	subject := &decisionengine.Subject{
		Release: &parser.TorrentInfo{ReleaseInfo: *rel},
	}
	got := spec.IsSatisfiedBy(t.Context(), subject, decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject on Usenet TorrentInfo below min, got Accept")
	}
}

func TestMinimumAge_AcceptsWhenMinIsNegative(t *testing.T) {
	t.Parallel()
	// Defensive: negative MinimumAge from a misconfigured config-service
	// is treated like 0 (disabled). Same rationale as MaximumSize.
	spec := newMinimumAgeSpec(t, -5)
	subject := &decisionengine.Subject{Release: usenetRelease(0)}
	got := spec.IsSatisfiedBy(t.Context(), subject, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on negative MinimumAge, got %+v", got)
	}
}

func TestMinimumAge_InterfaceShape(t *testing.T) {
	t.Parallel()
	spec, err := decisionengine.NewMinimumAgeSpecification(stubMinimumAge{minutes: 30}, nil)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	var iface decisionengine.Specification = spec
	if iface.Name() != decisionengine.MinimumAgeSpecificationName {
		t.Errorf("Name = %q", iface.Name())
	}
	if iface.Kind() != decisionengine.RejectionTemporary {
		t.Errorf("Kind = %v, want Temporary", iface.Kind())
	}
	if iface.Priority() != decisionengine.SpecPriorityDefault {
		t.Errorf("Priority = %v", iface.Priority())
	}
}
