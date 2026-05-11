package decisionengine_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/qualityprofiles"
	"sonarr.tv/sonarr/internal/series"
)

// stubQueueReader implements [decisionengine.QueueReader] with a
// fixed snapshot.
type stubQueueReader struct {
	rows []decisionengine.QueueItem
}

func (s stubQueueReader) GetQueue() []decisionengine.QueueItem { return s.rows }

// stubQueueUpgradable implements [decisionengine.QueueUpgradableChecker]
// with fixed responses.
type stubQueueUpgradable struct {
	cutoffNotMet     bool
	upgradableReason decisionengine.UpgradeableRejectReason
	revisionUpgrade  bool
	cutoffCalls      int
	upgradableCalls  int
	revisionUpgCalls int
}

func (s *stubQueueUpgradable) CutoffNotMet(_ *decisionengine.ProfileView, _ quality.Model, _ []customformats.CustomFormat, _ *quality.Model) bool {
	s.cutoffCalls++
	return s.cutoffNotMet
}

func (s *stubQueueUpgradable) IsUpgradable(_ *decisionengine.ProfileView, _ quality.Model, _ []customformats.CustomFormat, _ *quality.Model, _ []customformats.CustomFormat) decisionengine.UpgradeableRejectReason {
	s.upgradableCalls++
	return s.upgradableReason
}

func (s *stubQueueUpgradable) IsRevisionUpgrade(_, _ quality.Model) bool {
	s.revisionUpgCalls++
	return s.revisionUpgrade
}

// stubQueuePropers implements [decisionengine.QueuePropersConfigReader].
type stubQueuePropers struct {
	mode quality.ProperDownloadType
}

func (s stubQueuePropers) DownloadPropersAndRepacks() quality.ProperDownloadType { return s.mode }

func queueView(t *testing.T) *decisionengine.ProfileView {
	t.Helper()
	view, err := decisionengine.NewProfileView(&qualityprofiles.Profile{
		ID:                    7,
		Name:                  "HD",
		UpgradeAllowed:        true,
		Cutoff:                quality.Bluray1080p.ID,
		Items:                 `[{"quality":4,"allowed":true},{"quality":7,"allowed":true}]`,
		FormatItems:           "[]",
		MinFormatScore:        0,
		CutoffFormatScore:     100,
		MinUpgradeFormatScore: 5,
	})
	if err != nil {
		t.Fatalf("NewProfileView: %v", err)
	}
	return view
}

func queueSubject() *decisionengine.Subject {
	pid := 7
	return &decisionengine.Subject{
		Series:   &series.Series{ID: 42, QualityProfileID: pid},
		Episodes: []episodes.Episode{{ID: 11}, {ID: 12}},
		ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{
			Quality: quality.Model{Quality: quality.WEBDL1080p, Revision: quality.Revision{Version: 1}},
		},
		Release:       &parser.ReleaseInfo{Title: "Show.S01E01.WEB-DL"},
		CustomFormats: []customformats.CustomFormat{},
	}
}

// queueRow constructs a QueueItem matching the queueSubject (same
// series + overlapping episode id).
func queueRow(matchEpisodeID int, queuedQuality quality.Quality, state *int) decisionengine.QueueItem {
	pid := 7
	return decisionengine.QueueItem{
		RemoteEpisode: &decisionengine.Subject{
			Series:   &series.Series{ID: 42, QualityProfileID: pid},
			Episodes: []episodes.Episode{{ID: matchEpisodeID}},
			ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{
				Quality: quality.Model{Quality: queuedQuality, Revision: quality.Revision{Version: 1}},
			},
		},
		Size:                 1_000_000_000,
		TrackedDownloadState: state,
	}
}

func newQueueSpec(t *testing.T,
	rows []decisionengine.QueueItem,
	cutoffNotMet bool,
	upgradableReason decisionengine.UpgradeableRejectReason,
	revisionUpgrade bool,
	propers quality.ProperDownloadType,
	view *decisionengine.ProfileView,
) (*decisionengine.QueueSpecification, *stubQueueUpgradable) {
	t.Helper()
	upg := &stubQueueUpgradable{
		cutoffNotMet:     cutoffNotMet,
		upgradableReason: upgradableReason,
		revisionUpgrade:  revisionUpgrade,
	}
	spec, err := decisionengine.NewQueueSpecification(
		stubQueueReader{rows: rows},
		stubProfileLookup{view: view, ok: view != nil},
		upg,
		stubQueuePropers{mode: propers},
		slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatalf("NewQueueSpecification: %v", err)
	}
	return spec, upg
}

func TestQueue_NewRejectsNilDeps(t *testing.T) {
	t.Parallel()
	upg := &stubQueueUpgradable{}
	view := queueView(t)
	cases := []struct {
		name string
		fn   func() error
	}{
		{"nil queue", func() error {
			_, err := decisionengine.NewQueueSpecification(nil, stubProfileLookup{view: view}, upg, stubQueuePropers{}, nil)
			return err
		}},
		{"nil profiles", func() error {
			_, err := decisionengine.NewQueueSpecification(stubQueueReader{}, nil, upg, stubQueuePropers{}, nil)
			return err
		}},
		{"nil upgradable", func() error {
			_, err := decisionengine.NewQueueSpecification(stubQueueReader{}, stubProfileLookup{view: view}, nil, stubQueuePropers{}, nil)
			return err
		}},
		{"nil propers", func() error {
			_, err := decisionengine.NewQueueSpecification(stubQueueReader{}, stubProfileLookup{view: view}, upg, nil, nil)
			return err
		}},
	}
	for _, c := range cases {
		if err := c.fn(); err == nil {
			t.Errorf("%s: want error, got nil", c.name)
		}
	}
}

func TestQueue_DefensiveAccepts(t *testing.T) {
	t.Parallel()
	view := queueView(t)
	spec, _ := newQueueSpec(t, nil, true, decisionengine.UpgradeableNone, false, quality.ProperPreferAndUpgrade, view)
	cases := []struct {
		name    string
		subject *decisionengine.Subject
	}{
		{"nil subject", nil},
		{"nil Series", &decisionengine.Subject{}},
		{"nil ParsedEpisodeInfo", &decisionengine.Subject{Series: &series.Series{ID: 42}}},
		{"empty Episodes", &decisionengine.Subject{Series: &series.Series{ID: 42}, ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{}}},
		{"nil QualityProfileID", &decisionengine.Subject{Series: &series.Series{ID: 42}, ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{}, Episodes: []episodes.Episode{{ID: 11}}}},
	}
	for _, c := range cases {
		dec := spec.IsSatisfiedBy(t.Context(), c.subject, decisionengine.ReleaseDecisionInformation{})
		if !dec.Accepted {
			t.Errorf("%s: want Accept, got Reject(%s)", c.name, dec.Message)
		}
	}
}

func TestQueue_AcceptsOnEmptyQueue(t *testing.T) {
	t.Parallel()
	spec, _ := newQueueSpec(t, nil, true, decisionengine.UpgradeableNone, false, quality.ProperPreferAndUpgrade, queueView(t))
	dec := spec.IsSatisfiedBy(t.Context(), queueSubject(), decisionengine.ReleaseDecisionInformation{})
	if !dec.Accepted {
		t.Errorf("empty queue: want Accept, got Reject(%s)", dec.Message)
	}
}

func TestQueue_AcceptsOnProfileLookupError(t *testing.T) {
	t.Parallel()
	upg := &stubQueueUpgradable{cutoffNotMet: true}
	rows := []decisionengine.QueueItem{queueRow(11, quality.HDTV1080p, nil)}
	spec, err := decisionengine.NewQueueSpecification(
		stubQueueReader{rows: rows},
		stubProfileLookup{err: errAnyLookup},
		upg,
		stubQueuePropers{},
		slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatalf("NewQueueSpecification: %v", err)
	}
	dec := spec.IsSatisfiedBy(t.Context(), queueSubject(), decisionengine.ReleaseDecisionInformation{})
	if !dec.Accepted {
		t.Errorf("profile error: want Accept, got Reject(%s)", dec.Message)
	}
}

func TestQueue_AcceptsOnNoMatchingRows(t *testing.T) {
	t.Parallel()
	rows := []decisionengine.QueueItem{queueRow(99, quality.HDTV1080p, nil)} // episode-id 99 doesn't match candidate's 11/12.
	spec, _ := newQueueSpec(t, rows, true, decisionengine.UpgradeableNone, false, quality.ProperPreferAndUpgrade, queueView(t))
	dec := spec.IsSatisfiedBy(t.Context(), queueSubject(), decisionengine.ReleaseDecisionInformation{})
	if !dec.Accepted {
		t.Errorf("no matching rows: want Accept, got Reject(%s)", dec.Message)
	}
}

func TestQueue_RejectsOnCutoffMet(t *testing.T) {
	t.Parallel()
	rows := []decisionengine.QueueItem{queueRow(11, quality.HDTV1080p, nil)}
	spec, _ := newQueueSpec(t, rows, false /*cutoff IS met*/, decisionengine.UpgradeableNone, false, quality.ProperPreferAndUpgrade, queueView(t))
	dec := spec.IsSatisfiedBy(t.Context(), queueSubject(), decisionengine.ReleaseDecisionInformation{})
	if dec.Accepted {
		t.Fatal("want Reject, got Accept")
	}
	if dec.Reason != decisionengine.ReasonQueueCutoffMet {
		t.Errorf("Reason = %v, want ReasonQueueCutoffMet", dec.Reason)
	}
	if !strings.Contains(dec.Message, "meets cutoff") {
		t.Errorf("Message missing 'meets cutoff': %q", dec.Message)
	}
}

func TestQueue_RejectsByUpgradeableReason(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		upReason   decisionengine.UpgradeableRejectReason
		wantReason decisionengine.RejectionReason
	}{
		{"BetterQuality", decisionengine.UpgradeableBetterQuality, decisionengine.ReasonQueueHigherPreference},
		{"BetterRevision", decisionengine.UpgradeableBetterRevision, decisionengine.ReasonQueueHigherRevision},
		{"QualityCutoff", decisionengine.UpgradeableQualityCutoff, decisionengine.ReasonQueueCutoffMet},
		{"CustomFormatCutoff", decisionengine.UpgradeableCustomFormatCutoff, decisionengine.ReasonQueueCustomFormatCutoffMet},
		{"CustomFormatScore", decisionengine.UpgradeableCustomFormatScore, decisionengine.ReasonQueueCustomFormatScore},
		{"MinCustomFormatScore", decisionengine.UpgradeableMinCustomFormatScore, decisionengine.ReasonQueueCustomFormatScoreIncrement},
		{"UpgradesNotAllowed", decisionengine.UpgradeableUpgradesNotAllowed, decisionengine.ReasonQueueUpgradesNotAllowed},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			rows := []decisionengine.QueueItem{queueRow(11, quality.HDTV1080p, nil)}
			spec, _ := newQueueSpec(t, rows, true /*cutoff NOT met → falls through to IsUpgradable*/, c.upReason, false, quality.ProperPreferAndUpgrade, queueView(t))
			dec := spec.IsSatisfiedBy(t.Context(), queueSubject(), decisionengine.ReleaseDecisionInformation{})
			if dec.Accepted {
				t.Fatalf("%s: want Reject, got Accept", c.name)
			}
			if dec.Reason != c.wantReason {
				t.Errorf("%s: Reason = %v, want %v", c.name, dec.Reason, c.wantReason)
			}
		})
	}
}

func TestQueue_RejectsOnPropersDisabled(t *testing.T) {
	t.Parallel()
	rows := []decisionengine.QueueItem{queueRow(11, quality.HDTV1080p, nil)}
	// CutoffNotMet=true → no cutoff reject. Upgradable=None → no upgrade
	// reject. RevisionUpgrade=true + propers DoNotUpgrade → reject.
	spec, _ := newQueueSpec(t, rows, true, decisionengine.UpgradeableNone, true, quality.ProperDoNotUpgrade, queueView(t))
	dec := spec.IsSatisfiedBy(t.Context(), queueSubject(), decisionengine.ReleaseDecisionInformation{})
	if dec.Accepted {
		t.Fatal("want Reject, got Accept")
	}
	if dec.Reason != decisionengine.ReasonQueuePropersDisabled {
		t.Errorf("Reason = %v, want ReasonQueuePropersDisabled", dec.Reason)
	}
}

func TestQueue_AcceptsOnPropersEnabledRevisionUpgrade(t *testing.T) {
	t.Parallel()
	rows := []decisionengine.QueueItem{queueRow(11, quality.HDTV1080p, nil)}
	spec, _ := newQueueSpec(t, rows, true, decisionengine.UpgradeableNone, true, quality.ProperPreferAndUpgrade, queueView(t))
	dec := spec.IsSatisfiedBy(t.Context(), queueSubject(), decisionengine.ReleaseDecisionInformation{})
	if !dec.Accepted {
		t.Errorf("propers enabled + revision upgrade: want Accept, got Reject(%s)", dec.Message)
	}
}

func TestQueue_SkipsFailedPendingRow(t *testing.T) {
	t.Parallel()
	failedPending := 5 // trackeddownloads.StateFailedPending ordinal
	rows := []decisionengine.QueueItem{queueRow(11, quality.HDTV1080p, &failedPending)}
	// CutoffNotMet=false would normally trigger reject, but skip should
	// short-circuit before that check.
	spec, upg := newQueueSpec(t, rows, false, decisionengine.UpgradeableNone, false, quality.ProperPreferAndUpgrade, queueView(t))
	dec := spec.IsSatisfiedBy(t.Context(), queueSubject(), decisionengine.ReleaseDecisionInformation{})
	if !dec.Accepted {
		t.Errorf("FailedPending row: want Accept, got Reject(%s)", dec.Message)
	}
	if upg.cutoffCalls != 0 {
		t.Errorf("CutoffNotMet was called %d times despite FailedPending skip", upg.cutoffCalls)
	}
}

func TestQueue_SkipsRowsWithNilRemoteEpisode(t *testing.T) {
	t.Parallel()
	rows := []decisionengine.QueueItem{
		{RemoteEpisode: nil, Size: 0}, // skipped
		queueRow(11, quality.HDTV1080p, nil),
	}
	spec, _ := newQueueSpec(t, rows, false, decisionengine.UpgradeableNone, false, quality.ProperPreferAndUpgrade, queueView(t))
	dec := spec.IsSatisfiedBy(t.Context(), queueSubject(), decisionengine.ReleaseDecisionInformation{})
	if dec.Accepted {
		t.Fatal("second row should still trigger CutoffMet reject")
	}
	if dec.Reason != decisionengine.ReasonQueueCutoffMet {
		t.Errorf("Reason = %v, want CutoffMet", dec.Reason)
	}
}

func TestQueue_SkipsRowsWithDifferentSeries(t *testing.T) {
	t.Parallel()
	otherSeries := decisionengine.QueueItem{
		RemoteEpisode: &decisionengine.Subject{
			Series:            &series.Series{ID: 999}, // different series
			Episodes:          []episodes.Episode{{ID: 11}},
			ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{Quality: quality.Model{Quality: quality.HDTV1080p}},
		},
	}
	rows := []decisionengine.QueueItem{otherSeries}
	spec, _ := newQueueSpec(t, rows, false, decisionengine.UpgradeableNone, false, quality.ProperPreferAndUpgrade, queueView(t))
	dec := spec.IsSatisfiedBy(t.Context(), queueSubject(), decisionengine.ReleaseDecisionInformation{})
	if !dec.Accepted {
		t.Errorf("different series: want Accept, got Reject(%s)", dec.Message)
	}
}

func TestQueue_AcceptsWhenAllChecksPass(t *testing.T) {
	t.Parallel()
	rows := []decisionengine.QueueItem{queueRow(11, quality.HDTV1080p, nil)}
	spec, _ := newQueueSpec(t, rows, true /*cutoff not met*/, decisionengine.UpgradeableNone, false, quality.ProperPreferAndUpgrade, queueView(t))
	dec := spec.IsSatisfiedBy(t.Context(), queueSubject(), decisionengine.ReleaseDecisionInformation{})
	if !dec.Accepted {
		t.Errorf("all checks pass: want Accept, got Reject(%s)", dec.Message)
	}
}

func TestQueue_InterfaceShape(t *testing.T) {
	t.Parallel()
	spec, _ := newQueueSpec(t, nil, true, decisionengine.UpgradeableNone, false, quality.ProperPreferAndUpgrade, queueView(t))
	if spec.Name() != decisionengine.QueueSpecificationName {
		t.Errorf("Name() = %q, want %q", spec.Name(), decisionengine.QueueSpecificationName)
	}
	if spec.Kind() != decisionengine.RejectionPermanent {
		t.Errorf("Kind() = %v, want Permanent", spec.Kind())
	}
	if spec.Priority() != decisionengine.SpecPriorityDefault {
		t.Errorf("Priority() = %v, want Default", spec.Priority())
	}
}

// errAnyLookup is a sentinel error used by the profile-lookup-error
// test path. Defined as package-level so tests can pass a single
// instance without per-test allocation.
var errAnyLookup = errAny("profile lookup failure")

type errAny string

func (e errAny) Error() string { return string(e) }

// Compile-time guard for the QueueReader interface contract.
var _ decisionengine.QueueReader = stubQueueReader{}

// Compile-time guard the stub still satisfies the spec's narrow
// interface set.
var _ context.Context = context.Background()
