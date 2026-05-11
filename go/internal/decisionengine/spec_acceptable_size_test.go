package decisionengine_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/qualityprofiles"
	"sonarr.tv/sonarr/internal/series"
)

// stubProfileReader is the minimal AcceptableSizeProfileReader test
// double — maps profileID → ProfileView.
type stubProfileReader struct {
	views map[int]*decisionengine.ProfileView
	err   error
}

func (s stubProfileReader) LookupProfile(_ context.Context, profileID int) (*decisionengine.ProfileView, bool, error) {
	if s.err != nil {
		return nil, false, s.err
	}
	v, ok := s.views[profileID]
	return v, ok, nil
}

// stubEpisodeFinder is the minimal AcceptableSizeEpisodeFinder test
// double — maps (seriesID, seasonNumber) → first episode.
type stubEpisodeFinder struct {
	pilots map[[2]int]episodes.Episode
}

func (s stubEpisodeFinder) FirstEpisodeOfSeason(_ context.Context, seriesID, seasonNumber int) (episodes.Episode, bool, error) {
	e, ok := s.pilots[[2]int{seriesID, seasonNumber}]
	return e, ok, nil
}

// asPtr is a generic value-to-pointer helper.
func asPtr[T any](v T) *T { return &v }

// makeProfile builds a *ProfileView with a single SDTV item carrying
// the supplied MinSize/MaxSize (in megabytes-per-minute), wrapped in
// the qualityprofiles.QualityItem shape.
func makeProfile(minSize, maxSize *float64) *decisionengine.ProfileView {
	return &decisionengine.ProfileView{
		Items: []qualityprofiles.QualityItem{
			{Quality: quality.SDTV, MinSize: minSize, MaxSize: maxSize, Allowed: true},
		},
	}
}

// makeProfileWithGroup builds a *ProfileView whose first item is a
// group containing WEBDL720p with the supplied size limits — exercises
// the group-member resolution path.
func makeProfileWithGroup(minSize, maxSize *float64) *decisionengine.ProfileView {
	return &decisionengine.ProfileView{
		Items: []qualityprofiles.QualityItem{
			{
				ID: 1001, Name: "WEB 720p",
				Items: []qualityprofiles.QualityItem{
					{Quality: quality.WEBDL720p, MinSize: minSize, MaxSize: maxSize, Allowed: true},
				},
			},
		},
	}
}

// newAcceptableSizeSpec builds a spec wired with the supplied stubs.
func newAcceptableSizeSpec(t *testing.T, profiles map[int]*decisionengine.ProfileView, finder decisionengine.AcceptableSizeEpisodeFinder) *decisionengine.AcceptableSizeSpecification {
	t.Helper()
	spec, err := decisionengine.NewAcceptableSizeSpecification(
		stubProfileReader{views: profiles},
		finder,
		slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatalf("NewAcceptableSizeSpecification: %v", err)
	}
	return spec
}

const twoSeasonsJSON = `[{"seasonNumber":1,"monitored":true},{"seasonNumber":2,"monitored":true}]`

// makeSubject builds a Subject with a Series + Episodes + parsed quality.
// runtime is the per-episode runtime; epCount is the number of episodes
// in the release; sizeMB is the release size in megabytes. Series.Seasons
// is pinned to twoSeasonsJSON — every existing test only needs the
// "first non-special season is 1" answer.
func makeSubject(runtime, epCount, sizeMB int, q quality.Quality) *decisionengine.Subject {
	eps := make([]episodes.Episode, epCount)
	for i := range eps {
		eps[i] = episodes.Episode{ID: i + 1, SeasonNumber: 1, Runtime: runtime}
	}
	return &decisionengine.Subject{
		Series: &series.Series{
			ID:               42,
			Runtime:          runtime,
			Seasons:          twoSeasonsJSON,
			QualityProfileID: 7,
		},
		Episodes: eps,
		ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{
			Quality: quality.Model{Quality: q},
		},
		Release: &parser.ReleaseInfo{
			Title: "Show.S01E01",
			Size:  int64(sizeMB) * 1024 * 1024,
		},
	}
}

func TestAcceptableSize_NewRejectsNilProfileReader(t *testing.T) {
	t.Parallel()
	_, err := decisionengine.NewAcceptableSizeSpecification(nil, nil, nil)
	if err == nil {
		t.Fatalf("expected error on nil profile reader, got nil")
	}
}

func TestAcceptableSize_AcceptsSpecial(t *testing.T) {
	t.Parallel()
	spec := newAcceptableSizeSpec(t, map[int]*decisionengine.ProfileView{7: makeProfile(asPtr(2.0), asPtr(10.0))}, nil)
	subj := makeSubject(30, 1, 100, quality.SDTV)
	subj.ParsedEpisodeInfo.Special = true
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on Special, got %+v", got)
	}
}

func TestAcceptableSize_AcceptsSizeZero(t *testing.T) {
	t.Parallel()
	spec := newAcceptableSizeSpec(t, map[int]*decisionengine.ProfileView{7: makeProfile(asPtr(2.0), asPtr(10.0))}, nil)
	subj := makeSubject(30, 1, 0, quality.SDTV)
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on size==0, got %+v", got)
	}
}

func TestAcceptableSize_SingleEpisode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		runtime  int
		sizeMB   int
		expected bool
	}{
		{"30min/50MB rejected (below min)", 30, 50, false},
		{"30min/250MB accepted", 30, 250, true},
		{"30min/500MB rejected (above max)", 30, 500, false},
		{"60min/100MB rejected (below min)", 60, 100, false},
		{"60min/500MB accepted", 60, 500, true},
		{"60min/1000MB rejected (above max)", 60, 1000, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := newAcceptableSizeSpec(t, map[int]*decisionengine.ProfileView{7: makeProfile(asPtr(2.0), asPtr(10.0))}, nil)
			subj := makeSubject(tc.runtime, 1, tc.sizeMB, quality.SDTV)
			got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
			if got.Accepted != tc.expected {
				t.Errorf("expected accepted=%v, got %+v", tc.expected, got)
			}
		})
	}
}

func TestAcceptableSize_MultiEpisode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		runtime  int
		sizeMB   int
		expected bool
	}{
		{"30min*2/100MB rejected", 30, 50 * 2, false},
		{"30min*2/500MB accepted", 30, 250 * 2, true},
		{"30min*2/1000MB rejected", 30, 500 * 2, false},
		{"60min*2/200MB rejected", 60, 100 * 2, false},
		{"60min*2/1000MB accepted", 60, 500 * 2, true},
		{"60min*2/2000MB rejected", 60, 1000 * 2, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := newAcceptableSizeSpec(t, map[int]*decisionengine.ProfileView{7: makeProfile(asPtr(2.0), asPtr(10.0))}, nil)
			subj := makeSubject(tc.runtime, 2, tc.sizeMB, quality.SDTV)
			got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
			if got.Accepted != tc.expected {
				t.Errorf("expected accepted=%v, got %+v", tc.expected, got)
			}
		})
	}
}

func TestAcceptableSize_RejectionMessageMentionsRuntime(t *testing.T) {
	t.Parallel()
	spec := newAcceptableSizeSpec(t, map[int]*decisionengine.ProfileView{7: makeProfile(asPtr(2.0), asPtr(10.0))}, nil)
	subj := makeSubject(30, 1, 500, quality.SDTV)
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Fatalf("expected reject")
	}
	if got.Reason != decisionengine.ReasonAboveMaximumSize {
		t.Errorf("Reason = %v, want ReasonAboveMaximumSize", got.Reason)
	}
	if !strings.Contains(got.Message, "30min") {
		t.Errorf("Message %q should mention runtime", got.Message)
	}
	if !strings.Contains(got.Message, "larger than maximum allowed") {
		t.Errorf("Message %q should mention maximum allowed", got.Message)
	}
}

func TestAcceptableSize_MultiEpisodeRejectionMessageCountsEpisodes(t *testing.T) {
	t.Parallel()
	spec := newAcceptableSizeSpec(t, map[int]*decisionengine.ProfileView{7: makeProfile(asPtr(2.0), asPtr(10.0))}, nil)
	subj := makeSubject(30, 2, 500*2, quality.SDTV)
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Fatalf("expected reject")
	}
	if !strings.Contains(got.Message, "2 episodes totalling 60min") {
		t.Errorf("Message %q should mention multi-episode totals", got.Message)
	}
}

func TestAcceptableSize_BelowMinSizeRejected(t *testing.T) {
	t.Parallel()
	spec := newAcceptableSizeSpec(t, map[int]*decisionengine.ProfileView{7: makeProfile(asPtr(2.0), asPtr(10.0))}, nil)
	subj := makeSubject(30, 1, 10, quality.SDTV) // 10MB << 60MB min
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Fatalf("expected reject")
	}
	if got.Reason != decisionengine.ReasonBelowMinimumSize {
		t.Errorf("Reason = %v, want ReasonBelowMinimumSize", got.Reason)
	}
}

func TestAcceptableSize_UnlimitedMaxSize(t *testing.T) {
	t.Parallel()
	// nil MaxSize means unlimited — any size above MinSize accepts.
	spec := newAcceptableSizeSpec(t, map[int]*decisionengine.ProfileView{7: makeProfile(asPtr(2.0), nil)}, nil)
	subj := makeSubject(30, 1, 18000, quality.SDTV)
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on unlimited MaxSize, got %+v", got)
	}
}

func TestAcceptableSize_ZeroMaxSizeIsUnlimited(t *testing.T) {
	t.Parallel()
	// MaxSize == 0 is treated as unlimited (mirrors C# `MaxSize.Value == 0`).
	spec := newAcceptableSizeSpec(t, map[int]*decisionengine.ProfileView{7: makeProfile(asPtr(2.0), asPtr(0.0))}, nil)
	subj := makeSubject(30, 1, 18000, quality.SDTV)
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on MaxSize==0, got %+v", got)
	}
}

func TestAcceptableSize_GroupedQuality(t *testing.T) {
	t.Parallel()
	spec := newAcceptableSizeSpec(t, map[int]*decisionengine.ProfileView{7: makeProfileWithGroup(asPtr(2.0), asPtr(20.0))}, nil)
	subj := makeSubject(30, 1, 500, quality.WEBDL720p)
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on grouped-quality 30*20=600MB cap, got %+v", got)
	}
}

func TestAcceptableSize_QualityNotInProfileAccepts(t *testing.T) {
	t.Parallel()
	spec := newAcceptableSizeSpec(t, map[int]*decisionengine.ProfileView{7: makeProfile(asPtr(2.0), asPtr(10.0))}, nil)
	subj := makeSubject(30, 1, 9999, quality.HDTV1080p) // not in profile
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on missing-quality, got %+v", got)
	}
}

func TestAcceptableSize_NilSeriesProfileIDAccepts(t *testing.T) {
	t.Parallel()
	spec := newAcceptableSizeSpec(t, map[int]*decisionengine.ProfileView{7: makeProfile(asPtr(2.0), asPtr(10.0))}, nil)
	subj := makeSubject(30, 1, 500, quality.SDTV)
	subj.Series.QualityProfileID = 0
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on nil profileID, got %+v", got)
	}
}

func TestAcceptableSize_MissingProfileAccepts(t *testing.T) {
	t.Parallel()
	spec := newAcceptableSizeSpec(t, map[int]*decisionengine.ProfileView{}, nil) // empty
	subj := makeSubject(30, 1, 500, quality.SDTV)
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on profile-not-found, got %+v", got)
	}
}

func TestAcceptableSize_ProfileLookupErrorAccepts(t *testing.T) {
	t.Parallel()
	spec, err := decisionengine.NewAcceptableSizeSpecification(
		stubProfileReader{err: errors.New("boom")},
		nil,
		slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	subj := makeSubject(30, 1, 500, quality.SDTV)
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on profile-lookup-error (defensive), got %+v", got)
	}
}

func TestAcceptableSize_RuntimeZeroNoFinderRejects(t *testing.T) {
	t.Parallel()
	spec := newAcceptableSizeSpec(t, map[int]*decisionengine.ProfileView{7: makeProfile(asPtr(2.0), asPtr(10.0))}, nil)
	subj := makeSubject(0, 1, 200, quality.SDTV)
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject on runtime==0 without finder, got Accept")
	}
	if got.Reason != decisionengine.ReasonUnknownRuntime {
		t.Errorf("Reason = %v, want ReasonUnknownRuntime", got.Reason)
	}
}

func TestAcceptableSize_RuntimeZeroSecondSeasonRejects(t *testing.T) {
	t.Parallel()
	// Episode is in Season 2, but the pilot lives in Season 1 — the
	// fallback only fires on first-aired-season releases.
	pilotAir := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	finder := stubEpisodeFinder{pilots: map[[2]int]episodes.Episode{
		{42, 1}: {ID: 100, SeasonNumber: 1, AirDateUtc: &pilotAir},
	}}
	spec := newAcceptableSizeSpec(t, map[int]*decisionengine.ProfileView{7: makeProfile(asPtr(2.0), asPtr(10.0))}, finder)
	subj := makeSubject(0, 1, 200, quality.SDTV)
	subj.Episodes[0].SeasonNumber = 2
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject on second-season + runtime==0, got Accept")
	}
}

func TestAcceptableSize_RuntimeZeroOutsideGracePeriodRejects(t *testing.T) {
	t.Parallel()
	pilotAir := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	episodeAir := pilotAir.Add(7 * 24 * time.Hour) // 7 days after pilot
	finder := stubEpisodeFinder{pilots: map[[2]int]episodes.Episode{
		{42, 1}: {ID: 100, SeasonNumber: 1, AirDateUtc: &pilotAir},
	}}
	spec := newAcceptableSizeSpec(t, map[int]*decisionengine.ProfileView{7: makeProfile(asPtr(2.0), asPtr(10.0))}, finder)
	subj := makeSubject(0, 1, 200, quality.SDTV)
	subj.Episodes[0].AirDateUtc = &episodeAir
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if got.Accepted {
		t.Errorf("expected Reject on episode-outside-24h, got Accept")
	}
}

func TestAcceptableSize_RuntimeZeroInsideGracePeriodAccepts(t *testing.T) {
	t.Parallel()
	pilotAir := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	episodeAir := pilotAir.Add(1 * time.Hour) // 1 hour after pilot
	finder := stubEpisodeFinder{pilots: map[[2]int]episodes.Episode{
		{42, 1}: {ID: 100, SeasonNumber: 1, AirDateUtc: &pilotAir},
	}}
	spec := newAcceptableSizeSpec(t, map[int]*decisionengine.ProfileView{7: makeProfile(asPtr(2.0), asPtr(10.0))}, finder)
	// 200MB / 45min = 4.44 MB/min — between 2 and 10 → accept.
	subj := makeSubject(0, 1, 200, quality.SDTV)
	subj.Episodes[0].AirDateUtc = &episodeAir
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on episode-inside-24h with 45min fallback, got %+v", got)
	}
}

func TestAcceptableSize_FallbackToSeriesRuntime(t *testing.T) {
	t.Parallel()
	// Episode runtime 0 but Series runtime 30 — series runtime wins.
	spec := newAcceptableSizeSpec(t, map[int]*decisionengine.ProfileView{7: makeProfile(asPtr(2.0), asPtr(10.0))}, nil)
	subj := makeSubject(30, 1, 200, quality.SDTV)
	subj.Episodes[0].Runtime = 0
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept when episode runtime falls back to series 30min, got %+v", got)
	}
}

func TestAcceptableSize_NilParsedInfoAccepts(t *testing.T) {
	t.Parallel()
	spec := newAcceptableSizeSpec(t, map[int]*decisionengine.ProfileView{7: makeProfile(asPtr(2.0), asPtr(10.0))}, nil)
	subj := makeSubject(30, 1, 500, quality.SDTV)
	subj.ParsedEpisodeInfo = nil
	got := spec.IsSatisfiedBy(t.Context(), subj, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on nil parsed info, got %+v", got)
	}
}

func TestAcceptableSize_NilReleaseAccepts(t *testing.T) {
	t.Parallel()
	spec := newAcceptableSizeSpec(t, map[int]*decisionengine.ProfileView{7: makeProfile(asPtr(2.0), asPtr(10.0))}, nil)
	got := spec.IsSatisfiedBy(t.Context(), &decisionengine.Subject{}, decisionengine.ReleaseDecisionInformation{})
	if !got.Accepted {
		t.Errorf("expected Accept on nil release, got %+v", got)
	}
}

func TestAcceptableSize_InterfaceShape(t *testing.T) {
	t.Parallel()
	spec, err := decisionengine.NewAcceptableSizeSpecification(stubProfileReader{}, nil, nil)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	var iface decisionengine.Specification = spec
	if iface.Name() != decisionengine.AcceptableSizeSpecificationName {
		t.Errorf("Name = %q", iface.Name())
	}
	if iface.Kind() != decisionengine.RejectionPermanent {
		t.Errorf("Kind = %v, want Permanent", iface.Kind())
	}
	if iface.Priority() != decisionengine.SpecPriorityDefault {
		t.Errorf("Priority = %v", iface.Priority())
	}
}
