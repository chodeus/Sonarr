package decisionengine_test

import (
	"errors"
	"log/slog"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/qualityprofiles"
	"sonarr.tv/sonarr/internal/series"
)

// === UpgradeDiskSpecification ===

// stubUpgradeDiskChecker implements [UpgradeDiskCutoffChecker] with
// fixed responses. Captures the last invocation args.
type stubUpgradeDiskChecker struct {
	cutoffNotMet bool
	reason       decisionengine.UpgradeableRejectReason
	cutoffCalls  int
	upgradeCalls int
	gotCurrent   quality.Model
	gotNew       *quality.Model
}

func (s *stubUpgradeDiskChecker) CutoffNotMet(_ *decisionengine.ProfileView, currentQuality quality.Model, _ []customformats.CustomFormat, newQuality *quality.Model) bool {
	s.cutoffCalls++
	s.gotCurrent = currentQuality
	s.gotNew = newQuality
	return s.cutoffNotMet
}

func (s *stubUpgradeDiskChecker) IsUpgradable(_ *decisionengine.ProfileView, _ quality.Model, _ []customformats.CustomFormat, _ *quality.Model, _ []customformats.CustomFormat) decisionengine.UpgradeableRejectReason {
	s.upgradeCalls++
	return s.reason
}

// stubSeasonPack implements [UpgradeDiskSeasonReader].
type stubSeasonPack struct {
	mode      decisionengine.SeasonPackUpgradeType
	threshold float64
}

func (s stubSeasonPack) SeasonPackUpgrade() decisionengine.SeasonPackUpgradeType { return s.mode }
func (s stubSeasonPack) SeasonPackUpgradeThreshold() float64                     { return s.threshold }

func upgradeDiskView(t *testing.T) *decisionengine.ProfileView {
	t.Helper()
	view, err := decisionengine.NewProfileView(&qualityprofiles.Profile{
		ID:                    7,
		Name:                  "HD",
		UpgradeAllowed:        true,
		Cutoff:                quality.Bluray1080p.ID,
		Items:                 `[{"quality":4,"allowed":true},{"quality":7,"allowed":true}]`,
		FormatItems:           "[]",
		MinFormatScore:        0,
		CutoffFormatScore:     0,
		MinUpgradeFormatScore: 1,
	})
	if err != nil {
		t.Fatalf("NewProfileView: %v", err)
	}
	return view
}

func upgradeDiskSubject(fullSeason bool, episodeFileIDs []int) *decisionengine.Subject {
	eps := make([]episodes.Episode, len(episodeFileIDs))
	for i, fid := range episodeFileIDs {
		var efp *int
		if fid != 0 {
			efp = ptrInt(fid)
		}
		eps[i] = episodes.Episode{ID: i + 1, EpisodeFileID: efp}
	}
	pid := 7
	return &decisionengine.Subject{
		Series:   &series.Series{ID: 7, QualityProfileID: pid},
		Episodes: eps,
		ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{
			Quality:    quality.Model{Quality: quality.HDTV720p, Revision: quality.Revision{Version: 1}},
			FullSeason: fullSeason,
		},
		Release: &parser.ReleaseInfo{Title: "Show.S01"},
	}
}

func newUpgradeDiskSpec(
	t *testing.T,
	cutoffNotMet bool,
	reason decisionengine.UpgradeableRejectReason,
	view *decisionengine.ProfileView,
	files map[int]*episodefiles.File,
	mode decisionengine.SeasonPackUpgradeType,
	threshold float64,
) (*decisionengine.UpgradeDiskSpecification, *stubUpgradeDiskChecker) {
	t.Helper()
	checker := &stubUpgradeDiskChecker{cutoffNotMet: cutoffNotMet, reason: reason}
	calc := &stubFormatCalc{}
	spec, err := decisionengine.NewUpgradeDiskSpecification(
		checker,
		stubProfileLookup{view: view, ok: view != nil},
		stubFileResolver{files: files},
		calc,
		stubSeasonPack{mode: mode, threshold: threshold},
		slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatalf("NewUpgradeDiskSpecification: %v", err)
	}
	return spec, checker
}

func TestUpgradeDisk_NewRejectsNilDeps(t *testing.T) {
	t.Parallel()
	checker := &stubUpgradeDiskChecker{}
	view := upgradeDiskView(t)
	if _, err := decisionengine.NewUpgradeDiskSpecification(nil, stubProfileLookup{view: view, ok: true}, stubFileResolver{}, &stubFormatCalc{}, stubSeasonPack{}, nil); err == nil {
		t.Errorf("expected error on nil checker")
	}
	if _, err := decisionengine.NewUpgradeDiskSpecification(checker, nil, stubFileResolver{}, &stubFormatCalc{}, stubSeasonPack{}, nil); err == nil {
		t.Errorf("expected error on nil profile lookup")
	}
	if _, err := decisionengine.NewUpgradeDiskSpecification(checker, stubProfileLookup{view: view, ok: true}, nil, &stubFormatCalc{}, stubSeasonPack{}, nil); err == nil {
		t.Errorf("expected error on nil file resolver")
	}
	if _, err := decisionengine.NewUpgradeDiskSpecification(checker, stubProfileLookup{view: view, ok: true}, stubFileResolver{}, nil, stubSeasonPack{}, nil); err == nil {
		t.Errorf("expected error on nil format calculator")
	}
	if _, err := decisionengine.NewUpgradeDiskSpecification(checker, stubProfileLookup{view: view, ok: true}, stubFileResolver{}, &stubFormatCalc{}, nil, nil); err == nil {
		t.Errorf("expected error on nil season pack reader")
	}
}

func TestUpgradeDisk_AcceptsDefensiveArms(t *testing.T) {
	t.Parallel()
	view := upgradeDiskView(t)
	for _, tc := range []struct {
		name string
		subj *decisionengine.Subject
	}{
		{"nil-Subject", nil},
		{"nil-Series", &decisionengine.Subject{ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{}}},
		{"nil-ParsedEpisodeInfo", &decisionengine.Subject{Series: &series.Series{ID: 1}}},
		{"nil-QualityProfileID", func() *decisionengine.Subject {
			subj := upgradeDiskSubject(false, []int{1})
			subj.Series.QualityProfileID = 0
			return subj
		}()},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			spec, _ := newUpgradeDiskSpec(t, true, decisionengine.UpgradeableNone, view, nil, decisionengine.SeasonPackUpgradeAll, 100)
			if got := spec.IsSatisfiedBy(t.Context(), tc.subj, decisionengine.ReleaseDecisionInformation{}); !got.Accepted {
				t.Errorf("expected Accept on %s, got %+v", tc.name, got)
			}
		})
	}
}

func TestUpgradeDisk_AcceptsOnProfileLookupError(t *testing.T) {
	t.Parallel()
	checker := &stubUpgradeDiskChecker{}
	spec, err := decisionengine.NewUpgradeDiskSpecification(
		checker,
		stubProfileLookup{err: errors.New("boom")},
		stubFileResolver{},
		&stubFormatCalc{},
		stubSeasonPack{mode: decisionengine.SeasonPackUpgradeAll, threshold: 100},
		slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatalf("NewUpgradeDiskSpecification: %v", err)
	}
	if got := spec.IsSatisfiedBy(t.Context(), upgradeDiskSubject(false, []int{1}), decisionengine.ReleaseDecisionInformation{}); !got.Accepted {
		t.Errorf("expected Accept on profile-lookup error, got %+v", got)
	}
	if checker.cutoffCalls != 0 {
		t.Errorf("checker should not be called on profile-lookup error; calls=%d", checker.cutoffCalls)
	}
}

func TestUpgradeDisk_AcceptsOnProfileNotFound(t *testing.T) {
	t.Parallel()
	checker := &stubUpgradeDiskChecker{}
	spec, err := decisionengine.NewUpgradeDiskSpecification(
		checker,
		stubProfileLookup{ok: false},
		stubFileResolver{},
		&stubFormatCalc{},
		stubSeasonPack{},
		slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatalf("NewUpgradeDiskSpecification: %v", err)
	}
	if got := spec.IsSatisfiedBy(t.Context(), upgradeDiskSubject(false, []int{1}), decisionengine.ReleaseDecisionInformation{}); !got.Accepted {
		t.Errorf("expected Accept on profile-not-found, got %+v", got)
	}
}

// --- Per-episode path ---

func TestUpgradeDisk_PerEpisode_SkipsMissingFile(t *testing.T) {
	t.Parallel()
	spec, checker := newUpgradeDiskSpec(t, false, decisionengine.UpgradeableBetterQuality, upgradeDiskView(t),
		map[int]*episodefiles.File{}, decisionengine.SeasonPackUpgradeAll, 100)
	if got := spec.IsSatisfiedBy(t.Context(), upgradeDiskSubject(false, []int{42}), decisionengine.ReleaseDecisionInformation{}); !got.Accepted {
		t.Errorf("expected Accept when file lookup misses, got %+v", got)
	}
	if checker.cutoffCalls != 0 {
		t.Errorf("checker should not be called when file resolver misses; calls=%d", checker.cutoffCalls)
	}
}

func TestUpgradeDisk_PerEpisode_AcceptsWhenAllFilesUpgradable(t *testing.T) {
	t.Parallel()
	files := map[int]*episodefiles.File{
		1: {ID: 1, Quality: "{}"},
		2: {ID: 2, Quality: "{}"},
	}
	spec, _ := newUpgradeDiskSpec(t, true, decisionengine.UpgradeableNone, upgradeDiskView(t), files, decisionengine.SeasonPackUpgradeAll, 100)
	if got := spec.IsSatisfiedBy(t.Context(), upgradeDiskSubject(false, []int{1, 2}), decisionengine.ReleaseDecisionInformation{}); !got.Accepted {
		t.Errorf("expected Accept when every file is upgradable, got %+v", got)
	}
}

func TestUpgradeDisk_PerEpisode_RejectsCutoffMet(t *testing.T) {
	t.Parallel()
	files := map[int]*episodefiles.File{1: {ID: 1, Quality: "{}"}}
	spec, _ := newUpgradeDiskSpec(t, false, decisionengine.UpgradeableNone, upgradeDiskView(t),
		files, decisionengine.SeasonPackUpgradeAll, 100)
	got := spec.IsSatisfiedBy(t.Context(), upgradeDiskSubject(false, []int{1}), decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject (cutoff met)")
	}
	if got.Reason != decisionengine.ReasonDiskCutoffMet {
		t.Errorf("Reason = %v, want ReasonDiskCutoffMet", got.Reason)
	}
}

func TestUpgradeDisk_PerEpisode_RejectsByUpgradableReason(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		reason decisionengine.UpgradeableRejectReason
		want   decisionengine.RejectionReason
	}{
		{"BetterQuality", decisionengine.UpgradeableBetterQuality, decisionengine.ReasonDiskHigherPreference},
		{"BetterRevision", decisionengine.UpgradeableBetterRevision, decisionengine.ReasonDiskHigherRevision},
		{"QualityCutoff", decisionengine.UpgradeableQualityCutoff, decisionengine.ReasonDiskCutoffMet},
		{"CustomFormatCutoff", decisionengine.UpgradeableCustomFormatCutoff, decisionengine.ReasonDiskCustomFormatCutoffMet},
		{"CustomFormatScore", decisionengine.UpgradeableCustomFormatScore, decisionengine.ReasonDiskCustomFormatScore},
		{"MinCustomFormatScore", decisionengine.UpgradeableMinCustomFormatScore, decisionengine.ReasonDiskCustomFormatScoreIncrement},
		{"UpgradesNotAllowed", decisionengine.UpgradeableUpgradesNotAllowed, decisionengine.ReasonDiskUpgradesNotAllowed},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			files := map[int]*episodefiles.File{1: {ID: 1, Quality: "{}"}}
			spec, _ := newUpgradeDiskSpec(t, true, tc.reason, upgradeDiskView(t),
				files, decisionengine.SeasonPackUpgradeAll, 100)
			got := spec.IsSatisfiedBy(t.Context(), upgradeDiskSubject(false, []int{1}), decisionengine.ReleaseDecisionInformation{})
			if got.Accepted {
				t.Fatalf("expected Reject for %s, got Accept", tc.name)
			}
			if got.Reason != tc.want {
				t.Errorf("Reason = %v, want %v", got.Reason, tc.want)
			}
		})
	}
}

func TestUpgradeDisk_PerEpisode_ShortCircuitsOnFirstFailure(t *testing.T) {
	t.Parallel()
	files := map[int]*episodefiles.File{
		1: {ID: 1, Quality: "{}"},
		2: {ID: 2, Quality: "{}"},
	}
	spec, checker := newUpgradeDiskSpec(t, true, decisionengine.UpgradeableBetterQuality, upgradeDiskView(t),
		files, decisionengine.SeasonPackUpgradeAll, 100)
	got := spec.IsSatisfiedBy(t.Context(), upgradeDiskSubject(false, []int{1, 2}), decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Fatalf("expected Reject")
	}
	if checker.upgradeCalls != 1 {
		t.Errorf("expected 1 IsUpgradable call (short-circuit), got %d", checker.upgradeCalls)
	}
}

// --- Season-pack path ---

func TestUpgradeDisk_SeasonPack_AcceptsAllMissing(t *testing.T) {
	t.Parallel()
	spec, _ := newUpgradeDiskSpec(t, true, decisionengine.UpgradeableNone, upgradeDiskView(t),
		nil, decisionengine.SeasonPackUpgradeAll, 100)
	got := spec.IsSatisfiedBy(t.Context(), upgradeDiskSubject(true, []int{0, 0, 0}), decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept when all episodes missing, got %+v", got)
	}
}

func TestUpgradeDisk_SeasonPack_AcceptsEmptyEpisodes(t *testing.T) {
	t.Parallel()
	// "Should not happen, but good to guard against it." (C# `:39-43`)
	spec, _ := newUpgradeDiskSpec(t, true, decisionengine.UpgradeableNone, upgradeDiskView(t),
		nil, decisionengine.SeasonPackUpgradeAll, 100)
	got := spec.IsSatisfiedBy(t.Context(), upgradeDiskSubject(true, nil), decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on empty episodes, got %+v", got)
	}
}

func TestUpgradeDisk_SeasonPack_AnyMode(t *testing.T) {
	t.Parallel()
	files := map[int]*episodefiles.File{
		1: {ID: 1, Quality: "{}"},
		2: {ID: 2, Quality: "{}"},
		3: {ID: 3, Quality: "{}"},
	}
	// Any mode: any upgradable episode → accept.
	spec, _ := newUpgradeDiskSpec(t, true, decisionengine.UpgradeableNone, upgradeDiskView(t),
		files, decisionengine.SeasonPackUpgradeAny, 100)
	got := spec.IsSatisfiedBy(t.Context(), upgradeDiskSubject(true, []int{1, 2, 3}), decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept with Any mode + all upgradable, got %+v", got)
	}

	// Any mode: zero upgradable + all on-disk → reject.
	spec2, _ := newUpgradeDiskSpec(t, false, decisionengine.UpgradeableBetterQuality, upgradeDiskView(t),
		files, decisionengine.SeasonPackUpgradeAny, 100)
	got = spec2.IsSatisfiedBy(t.Context(), upgradeDiskSubject(true, []int{1, 2, 3}), decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject when no episode is upgradable in Any mode")
	}
	if got.Reason != decisionengine.ReasonDiskNotUpgrade {
		t.Errorf("Reason = %v, want ReasonDiskNotUpgrade", got.Reason)
	}
}

func TestUpgradeDisk_SeasonPack_AllModeRequires100Percent(t *testing.T) {
	t.Parallel()
	files := map[int]*episodefiles.File{
		1: {ID: 1, Quality: "{}"},
		2: {ID: 2, Quality: "{}"},
	}
	// 1 missing + 1 cutoff-met (not upgradable) → 50% → reject in All mode.
	spec, _ := newUpgradeDiskSpec(t, false, decisionengine.UpgradeableNone, upgradeDiskView(t),
		files, decisionengine.SeasonPackUpgradeAll, 100)
	got := spec.IsSatisfiedBy(t.Context(), upgradeDiskSubject(true, []int{1, 0}), decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject (50%% < 100%% in All mode)")
	}

	// All missing or upgradable → accept.
	spec2, _ := newUpgradeDiskSpec(t, true, decisionengine.UpgradeableNone, upgradeDiskView(t),
		files, decisionengine.SeasonPackUpgradeAll, 100)
	got = spec2.IsSatisfiedBy(t.Context(), upgradeDiskSubject(true, []int{1, 2}), decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept (100%% upgradable in All mode)")
	}
}

func TestUpgradeDisk_SeasonPack_ThresholdMode(t *testing.T) {
	t.Parallel()
	files := map[int]*episodefiles.File{
		1: {ID: 1, Quality: "{}"},
		2: {ID: 2, Quality: "{}"},
		3: {ID: 3, Quality: "{}"},
		4: {ID: 4, Quality: "{}"},
	}
	// 1 missing + 3 cutoff-met (not upgradable) → 25% → reject at 50% threshold.
	spec, _ := newUpgradeDiskSpec(t, false, decisionengine.UpgradeableBetterQuality, upgradeDiskView(t),
		files, decisionengine.SeasonPackUpgradeThresholdMode, 50)
	got := spec.IsSatisfiedBy(t.Context(), upgradeDiskSubject(true, []int{1, 2, 3, 0}), decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject (25%% < 50%%)")
	}
	if got.Reason != decisionengine.ReasonDiskNotUpgrade {
		t.Errorf("Reason = %v, want ReasonDiskNotUpgrade", got.Reason)
	}
	if !strings.Contains(got.Message, "Threshold") {
		t.Errorf("rejection message %q should mention mode", got.Message)
	}

	// 2 missing + 1 upgradable + 1 cutoff-met → 75% → accept at 50% threshold.
	spec2, _ := newUpgradeDiskSpec(t, true, decisionengine.UpgradeableNone, upgradeDiskView(t),
		files, decisionengine.SeasonPackUpgradeThresholdMode, 50)
	got = spec2.IsSatisfiedBy(t.Context(), upgradeDiskSubject(true, []int{1, 0, 0, 0}), decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept (75%% >= 50%%), got %+v", got)
	}
}

func TestUpgradeDisk_SeasonPack_CutoffMetCountsAsNotUpgradable(t *testing.T) {
	t.Parallel()
	// CutoffNotMet=false → existing file does NOT count as upgradable
	// (matches C# `:67-74` `continue`); the upgrade-percentage falls.
	files := map[int]*episodefiles.File{
		1: {ID: 1, Quality: "{}"},
		2: {ID: 2, Quality: "{}"},
	}
	spec, checker := newUpgradeDiskSpec(t, false, decisionengine.UpgradeableNone, upgradeDiskView(t),
		files, decisionengine.SeasonPackUpgradeAll, 100)
	got := spec.IsSatisfiedBy(t.Context(), upgradeDiskSubject(true, []int{1, 2}), decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject when both files are at-cutoff")
	}
	if checker.upgradeCalls != 0 {
		t.Errorf("IsUpgradable should NOT be called when CutoffNotMet=false; calls=%d", checker.upgradeCalls)
	}
}

func TestUpgradeDisk_InterfaceShape(t *testing.T) {
	t.Parallel()
	spec, _ := newUpgradeDiskSpec(t, true, decisionengine.UpgradeableNone, upgradeDiskView(t),
		nil, decisionengine.SeasonPackUpgradeAll, 100)
	var iface decisionengine.Specification = spec
	if iface.Name() != decisionengine.UpgradeDiskSpecificationName {
		t.Errorf("Name = %q", iface.Name())
	}
	if iface.Kind() != decisionengine.RejectionPermanent {
		t.Errorf("Kind = %v", iface.Kind())
	}
	if iface.Priority() != decisionengine.SpecPriorityDefault {
		t.Errorf("Priority = %v", iface.Priority())
	}
}

// === SeasonPackUpgradeType parser ===

func TestParseSeasonPackUpgradeType(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want decisionengine.SeasonPackUpgradeType
	}{
		{"All", decisionengine.SeasonPackUpgradeAll},
		{"all", decisionengine.SeasonPackUpgradeAll},
		{"Threshold", decisionengine.SeasonPackUpgradeThresholdMode},
		{"threshold", decisionengine.SeasonPackUpgradeThresholdMode},
		{"  Threshold  ", decisionengine.SeasonPackUpgradeThresholdMode},
		{"Any", decisionengine.SeasonPackUpgradeAny},
		{"ANY", decisionengine.SeasonPackUpgradeAny},
		{"", decisionengine.SeasonPackUpgradeAll},
		{"unknown", decisionengine.SeasonPackUpgradeAll},
	}
	for _, tc := range cases {
		if got := decisionengine.ParseSeasonPackUpgradeType(tc.in); got != tc.want {
			t.Errorf("ParseSeasonPackUpgradeType(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestSeasonPackUpgradeType_String(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   decisionengine.SeasonPackUpgradeType
		want string
	}{
		{decisionengine.SeasonPackUpgradeAll, "All"},
		{decisionengine.SeasonPackUpgradeThresholdMode, "Threshold"},
		{decisionengine.SeasonPackUpgradeAny, "Any"},
		// Out-of-range falls back to "All".
		{decisionengine.SeasonPackUpgradeType(99), "All"},
	}
	for _, tc := range cases {
		if got := tc.in.String(); got != tc.want {
			t.Errorf("(%v).String() = %q, want %q", tc.in, got, tc.want)
		}
	}

	// Round-trip: String → Parse → original.
	for _, v := range []decisionengine.SeasonPackUpgradeType{
		decisionengine.SeasonPackUpgradeAll,
		decisionengine.SeasonPackUpgradeThresholdMode,
		decisionengine.SeasonPackUpgradeAny,
	} {
		if got := decisionengine.ParseSeasonPackUpgradeType(v.String()); got != v {
			t.Errorf("round-trip %v: got %v", v, got)
		}
	}
}
