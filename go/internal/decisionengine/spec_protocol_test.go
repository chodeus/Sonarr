package decisionengine_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/delayprofiles"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

// stubDelayProfileResolver is the minimal DelayProfileResolver test
// double.
type stubDelayProfileResolver struct {
	profile delayprofiles.Profile
	ok      bool
	err     error
	calls   int
	gotTags []int
}

func (s *stubDelayProfileResolver) BestForTags(_ context.Context, tagIDs []int) (delayprofiles.Profile, bool, error) {
	s.calls++
	s.gotTags = tagIDs
	return s.profile, s.ok, s.err
}

// makeProtocolSubject builds a Subject with the given protocol.
func makeProtocolSubject(protocol indexers.DownloadProtocol) *decisionengine.Subject {
	return &decisionengine.Subject{
		Series: &series.Series{ID: 7, Tags: []int{1, 2}},
		Release: &parser.ReleaseInfo{
			Title:            "Show.S01E01",
			DownloadProtocol: protocol,
		},
	}
}

func TestProtocol_NewRejectsNilResolver(t *testing.T) {
	t.Parallel()
	if _, err := decisionengine.NewProtocolSpecification(nil, nil); err == nil {
		t.Fatalf("expected error on nil resolver, got nil")
	}
}

func TestProtocol_AcceptsWhenUsenetEnabled(t *testing.T) {
	t.Parallel()
	resolver := &stubDelayProfileResolver{
		profile: delayprofiles.Profile{EnableUsenet: true, EnableTorrent: true},
		ok:      true,
	}
	spec, _ := decisionengine.NewProtocolSpecification(resolver, slog.New(slog.DiscardHandler))
	got := spec.IsSatisfiedBy(t.Context(), makeProtocolSubject(indexers.DownloadProtocolUsenet), decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on enabled Usenet, got %+v", got)
	}
	if resolver.calls != 1 {
		t.Errorf("expected 1 resolver call, got %d", resolver.calls)
	}
	if len(resolver.gotTags) != 2 || resolver.gotTags[0] != 1 {
		t.Errorf("expected Series.Tags forwarded, got %v", resolver.gotTags)
	}
}

func TestProtocol_AcceptsWhenTorrentEnabled(t *testing.T) {
	t.Parallel()
	resolver := &stubDelayProfileResolver{
		profile: delayprofiles.Profile{EnableUsenet: false, EnableTorrent: true},
		ok:      true,
	}
	spec, _ := decisionengine.NewProtocolSpecification(resolver, slog.New(slog.DiscardHandler))
	got := spec.IsSatisfiedBy(t.Context(), makeProtocolSubject(indexers.DownloadProtocolTorrent), decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on enabled Torrent, got %+v", got)
	}
}

func TestProtocol_RejectsWhenUsenetDisabled(t *testing.T) {
	t.Parallel()
	resolver := &stubDelayProfileResolver{
		profile: delayprofiles.Profile{EnableUsenet: false, EnableTorrent: true},
		ok:      true,
	}
	spec, _ := decisionengine.NewProtocolSpecification(resolver, slog.New(slog.DiscardHandler))
	got := spec.IsSatisfiedBy(t.Context(), makeProtocolSubject(indexers.DownloadProtocolUsenet), decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject on disabled Usenet, got Accept")
	}
	if got.Reason != decisionengine.ReasonProtocolDisabled {
		t.Errorf("Reason = %v, want ReasonProtocolDisabled", got.Reason)
	}
	if got.Message != "Usenet is not enabled for this series" {
		t.Errorf("Message = %q, want exact C# string", got.Message)
	}
}

func TestProtocol_RejectsWhenTorrentDisabled(t *testing.T) {
	t.Parallel()
	resolver := &stubDelayProfileResolver{
		profile: delayprofiles.Profile{EnableUsenet: true, EnableTorrent: false},
		ok:      true,
	}
	spec, _ := decisionengine.NewProtocolSpecification(resolver, slog.New(slog.DiscardHandler))
	got := spec.IsSatisfiedBy(t.Context(), makeProtocolSubject(indexers.DownloadProtocolTorrent), decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject on disabled Torrent, got Accept")
	}
	if got.Reason != decisionengine.ReasonProtocolDisabled {
		t.Errorf("Reason = %v, want ReasonProtocolDisabled", got.Reason)
	}
	if got.Message != "Torrent is not enabled for this series" {
		t.Errorf("Message = %q, want exact C# string", got.Message)
	}
}

func TestProtocol_AcceptsOnResolverError(t *testing.T) {
	t.Parallel()
	resolver := &stubDelayProfileResolver{err: errors.New("boom")}
	spec, _ := decisionengine.NewProtocolSpecification(resolver, slog.New(slog.DiscardHandler))
	got := spec.IsSatisfiedBy(t.Context(), makeProtocolSubject(indexers.DownloadProtocolUsenet), decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on resolver error (defensive), got %+v", got)
	}
}

func TestProtocol_AcceptsOnNoMatchingProfile(t *testing.T) {
	t.Parallel()
	resolver := &stubDelayProfileResolver{ok: false}
	spec, _ := decisionengine.NewProtocolSpecification(resolver, slog.New(slog.DiscardHandler))
	got := spec.IsSatisfiedBy(t.Context(), makeProtocolSubject(indexers.DownloadProtocolUsenet), decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept when no profile matches, got %+v", got)
	}
}

func TestProtocol_AcceptsUnknownProtocol(t *testing.T) {
	t.Parallel()
	// Defensive: a release with DownloadProtocolUnknown shouldn't trip
	// either gate. The C# spec uses if/if (not switch), so neither
	// branch fires for Unknown — accept.
	resolver := &stubDelayProfileResolver{
		profile: delayprofiles.Profile{EnableUsenet: false, EnableTorrent: false},
		ok:      true,
	}
	spec, _ := decisionengine.NewProtocolSpecification(resolver, slog.New(slog.DiscardHandler))
	got := spec.IsSatisfiedBy(t.Context(), makeProtocolSubject(indexers.DownloadProtocolUnknown), decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on Unknown protocol (no gate fires), got %+v", got)
	}
}

func TestProtocol_AcceptsNilSeries(t *testing.T) {
	t.Parallel()
	resolver := &stubDelayProfileResolver{
		profile: delayprofiles.Profile{EnableUsenet: false, EnableTorrent: false},
		ok:      true,
	}
	spec, _ := decisionengine.NewProtocolSpecification(resolver, slog.New(slog.DiscardHandler))
	subj := &decisionengine.Subject{Release: &parser.ReleaseInfo{DownloadProtocol: indexers.DownloadProtocolUsenet}}
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on nil Series, got %+v", got)
	}
	if resolver.calls != 0 {
		t.Errorf("expected resolver not called for nil Series, got %d", resolver.calls)
	}
}

func TestProtocol_AcceptsNilRelease(t *testing.T) {
	t.Parallel()
	resolver := &stubDelayProfileResolver{
		profile: delayprofiles.Profile{EnableUsenet: false, EnableTorrent: false},
		ok:      true,
	}
	spec, _ := decisionengine.NewProtocolSpecification(resolver, slog.New(slog.DiscardHandler))
	subj := &decisionengine.Subject{Series: &series.Series{ID: 7}}
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on nil Release, got %+v", got)
	}
}

func TestProtocol_InterfaceShape(t *testing.T) {
	t.Parallel()
	spec, err := decisionengine.NewProtocolSpecification(&stubDelayProfileResolver{}, nil)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	var iface decisionengine.Specification = spec
	if iface.Name() != decisionengine.ProtocolSpecificationName {
		t.Errorf("Name = %q", iface.Name())
	}
	if iface.Kind() != decisionengine.RejectionPermanent {
		t.Errorf("Kind = %v", iface.Kind())
	}
	if iface.Priority() != decisionengine.SpecPriorityDefault {
		t.Errorf("Priority = %v", iface.Priority())
	}
}
