package decisionengine_test

import (
	"context"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/parser"
)

// TestNewSubject_ZeroLengthNotNilSlices pins the C# constructor's
// invariant: Episodes / CustomFormats / Languages are zero-length-not-nil.
// Downstream specs iterate them without nil checks; nil would panic.
func TestNewSubject_ZeroLengthNotNilSlices(t *testing.T) {
	t.Parallel()
	s := decisionengine.NewSubject()
	if s.Episodes == nil {
		t.Errorf("Episodes is nil; want zero-length non-nil")
	}
	if s.CustomFormats == nil {
		t.Errorf("CustomFormats is nil; want zero-length non-nil")
	}
	if s.Languages == nil {
		t.Errorf("Languages is nil; want zero-length non-nil")
	}
}

// TestSubject_IsRecentEpisode_TrueWhenAirDateInLast14Days mirrors the
// C# helper at `RemoteEpisode.cs:36-39`.
func TestSubject_IsRecentEpisode_TrueWhenAirDateInLast14Days(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	recent := now.AddDate(0, 0, -10)
	s := decisionengine.NewSubject()
	s.Episodes = []episodes.Episode{
		{AirDateUtc: &recent},
	}
	if !s.IsRecentEpisode(now) {
		t.Errorf("expected IsRecentEpisode(10 days ago) = true")
	}
}

// TestSubject_IsRecentEpisode_FalseWhenAirDateOlderThan14Days — the
// gate of the helper. 14 days exactly should still return true (cutoff
// is "before" not "before-or-equal").
func TestSubject_IsRecentEpisode_FalseWhenAirDateOlderThan14Days(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -30)
	s := decisionengine.NewSubject()
	s.Episodes = []episodes.Episode{{AirDateUtc: &old}}
	if s.IsRecentEpisode(now) {
		t.Errorf("expected IsRecentEpisode(30 days ago) = false")
	}
}

// TestSubject_IsRecentEpisode_AnyEpisodeRecent — multi-episode releases
// trigger the "recent" classification when ANY constituent episode is
// recent. Mirrors the C# `Episodes.Any(...)` shape.
func TestSubject_IsRecentEpisode_AnyEpisodeRecent(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	old := now.AddDate(0, -1, 0)
	recent := now.AddDate(0, 0, -3)
	s := decisionengine.NewSubject()
	s.Episodes = []episodes.Episode{
		{AirDateUtc: &old},
		{AirDateUtc: &recent},
	}
	if !s.IsRecentEpisode(now) {
		t.Errorf("expected IsRecentEpisode = true (one episode is recent)")
	}
}

// TestSubject_IsRecentEpisode_HandlesNilAirDates — episodes with nil
// AirDateUtc must NOT panic; they're simply skipped (the C# tree
// also skips them — `e.AirDateUtc >= cutoff` is false for null).
func TestSubject_IsRecentEpisode_HandlesNilAirDates(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	s := decisionengine.NewSubject()
	s.Episodes = []episodes.Episode{
		{AirDateUtc: nil},
		{AirDateUtc: nil},
	}
	if s.IsRecentEpisode(now) {
		t.Errorf("expected IsRecentEpisode = false for all-nil air dates")
	}
}

// TestSubject_String_ReadsReleaseTitle pins the C# default ToString
// shape — returns the underlying release's Title. Both ReleaseInfo
// and TorrentInfo paths are covered.
func TestSubject_String_ReadsReleaseTitle(t *testing.T) {
	t.Parallel()
	s := decisionengine.NewSubject()
	s.Release = &parser.ReleaseInfo{Title: "Plain.Show.S01E01"}
	if got := s.String(); got != "Plain.Show.S01E01" {
		t.Errorf("ReleaseInfo path: %q, want %q", got, "Plain.Show.S01E01")
	}
	s.Release = &parser.TorrentInfo{ReleaseInfo: parser.ReleaseInfo{Title: "Torrent.S01E01"}}
	if got := s.String(); got != "Torrent.S01E01" {
		t.Errorf("TorrentInfo path: %q, want %q", got, "Torrent.S01E01")
	}
	s.Release = nil
	if got := s.String(); got != "<no release>" {
		t.Errorf("nil release: %q, want %q", got, "<no release>")
	}
}

// fakeSpec is a Specification implementation used to verify the
// interface shape compiles + has the contract we expect.
type fakeSpec struct {
	name      string
	kind      decisionengine.RejectionKind
	priority  decisionengine.SpecPriority
	decideFn  func(*decisionengine.Subject) decisionengine.SpecDecision
	callCount int
}

func (s *fakeSpec) Name() string                          { return s.name }
func (s *fakeSpec) Kind() decisionengine.RejectionKind    { return s.kind }
func (s *fakeSpec) Priority() decisionengine.SpecPriority { return s.priority }
func (s *fakeSpec) IsSatisfiedBy(_ context.Context, subject *decisionengine.Subject, _ decisionengine.ReleaseDecisionInformation) decisionengine.SpecDecision {
	s.callCount++
	if s.decideFn == nil {
		return decisionengine.Accept()
	}
	return s.decideFn(subject)
}

// TestSpecification_InterfaceShapeCompiles confirms a struct
// satisfies the Specification interface — catches signature drift
// on the four-method contract.
func TestSpecification_InterfaceShapeCompiles(t *testing.T) {
	t.Parallel()
	var _ decisionengine.Specification = &fakeSpec{}
}

// TestSpecification_AcceptingSpec — sanity check that a fake spec
// reaches IsSatisfiedBy and returns Accept.
func TestSpecification_AcceptingSpec(t *testing.T) {
	t.Parallel()
	spec := &fakeSpec{
		name:     "Test",
		kind:     decisionengine.RejectionPermanent,
		priority: decisionengine.SpecPriorityDefault,
	}
	got := spec.IsSatisfiedBy(t.Context(), decisionengine.NewSubject(),
		decisionengine.NewReleaseDecisionInformation())
	if !got.Accepted {
		t.Errorf("default fakeSpec: Accepted = false, want true")
	}
	if spec.callCount != 1 {
		t.Errorf("callCount = %d, want 1", spec.callCount)
	}
}

func TestNewReleaseDecisionInformation_DefaultsZero(t *testing.T) {
	t.Parallel()
	info := decisionengine.NewReleaseDecisionInformation()
	if info.PushedRelease {
		t.Errorf("default PushedRelease = true, want false")
	}
	if info.SearchCriteria != nil {
		t.Errorf("default SearchCriteria = %v, want nil", info.SearchCriteria)
	}
}

func TestNewPushedRelease_FlagsPushed(t *testing.T) {
	t.Parallel()
	info := decisionengine.NewPushedRelease()
	if !info.PushedRelease {
		t.Errorf("PushedRelease = false, want true")
	}
}

func TestNewSearchInformation_StoresCriteria(t *testing.T) {
	t.Parallel()
	criteria := struct{ Foo string }{Foo: "bar"}
	info := decisionengine.NewSearchInformation(criteria)
	if info.SearchCriteria == nil {
		t.Errorf("SearchCriteria = nil, want non-nil")
	}
	if got, ok := info.SearchCriteria.(struct{ Foo string }); !ok || got.Foo != "bar" {
		t.Errorf("SearchCriteria type-assert failed: %v", info.SearchCriteria)
	}
}
