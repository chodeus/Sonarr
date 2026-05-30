package decisionengine_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/delayprofiles"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/qualityprofiles"
	"sonarr.tv/sonarr/internal/series"
)

// stubDelayProfileView is the DelayProfileViewReader test double.
type stubDelayProfileView struct {
	view  *decisionengine.ProfileView
	found bool
	err   error
}

func (s stubDelayProfileView) LookupProfile(_ context.Context, _ int) (*decisionengine.ProfileView, bool, error) {
	return s.view, s.found, s.err
}

// stubDelayPending is the PendingReleaseAgeReader test double.
type stubDelayPending struct {
	age   float64
	found bool
	err   error
}

func (s stubDelayPending) OldestPendingAgeMinutes(_ context.Context, _, _ int, _ []int) (float64, bool, error) {
	return s.age, s.found, s.err
}

// delayDeps bundles the swappable DelaySpecification dependencies for a
// test so each case overrides only what it cares about.
type delayDeps struct {
	resolver *stubDelayProfileResolver
	view     stubDelayProfileView
	upgrade  bool // stubUpgradable.isUpgrade (proper bypass)
	propers  quality.ProperDownloadType
	files    map[int]*episodefiles.File
	pending  stubDelayPending
}

func newDelaySpec(t *testing.T, d delayDeps) *decisionengine.DelaySpecification {
	t.Helper()
	spec, err := decisionengine.NewDelaySpecification(
		d.resolver,
		d.view,
		stubUpgradable{isUpgrade: d.upgrade},
		stubPropersCfg{kind: d.propers},
		stubFileResolver{files: d.files},
		d.pending,
		slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatalf("NewDelaySpecification: %v", err)
	}
	return spec
}

// usenetProfile returns a delay profile preferring usenet with the
// given usenet delay (minutes) and bypass flags off.
func usenetProfile(usenetDelay int) delayprofiles.Profile {
	return delayprofiles.Profile{
		PreferredProtocol: int(indexers.DownloadProtocolUsenet),
		UsenetDelay:       usenetDelay,
		TorrentDelay:      usenetDelay,
	}
}

func delaySubject(publishedMinutesAgo int, q quality.Model, episodeFileIDs ...int) *decisionengine.Subject {
	eps := make([]episodes.Episode, len(episodeFileIDs))
	for i, fid := range episodeFileIDs {
		var efp *int
		if fid != 0 {
			efp = ptrInt(fid)
		}
		eps[i] = episodes.Episode{ID: i + 1, EpisodeNumber: i + 1, EpisodeFileID: efp}
	}
	return &decisionengine.Subject{
		Series:             &series.Series{ID: 7, Tags: []int{1}, QualityProfileID: 3},
		MappedSeasonNumber: 1,
		Episodes:           eps,
		ParsedEpisodeInfo:  &parser.ParsedEpisodeInfo{Quality: q},
		Release: &parser.ReleaseInfo{
			Title:            "Show.S01E01",
			DownloadProtocol: indexers.DownloadProtocolUsenet,
			PublishDate:      time.Now().Add(-time.Duration(publishedMinutesAgo) * time.Minute),
		},
	}
}

func q720() quality.Model { return quality.Model{Quality: quality.HDTV720p} }

// baseDelayDeps returns deps where the delay applies and NO bypass
// fires: a usenet profile with a 60-min delay, no profile view, no
// pending release, propers-not-preferred.
func baseDelayDeps(usenetDelay int) delayDeps {
	return delayDeps{
		resolver: &stubDelayProfileResolver{profile: usenetProfile(usenetDelay), ok: true},
		view:     stubDelayProfileView{found: false},
		propers:  quality.ProperDoNotPrefer,
		pending:  stubDelayPending{found: false},
	}
}

func TestDelay_NewRejectsNilDeps(t *testing.T) {
	t.Parallel()
	good := func() (decisionengine.DelayProfileResolver, decisionengine.DelayProfileViewReader, decisionengine.UpgradableRevisionChecker, decisionengine.PropersAndRepacksConfigReader, decisionengine.EpisodeFileResolver) {
		return &stubDelayProfileResolver{}, stubDelayProfileView{}, stubUpgradable{}, stubPropersCfg{}, stubFileResolver{}
	}
	r, v, u, c, f := good()
	if _, err := decisionengine.NewDelaySpecification(nil, v, u, c, f, nil, nil); err == nil {
		t.Error("expected error on nil resolver")
	}
	if _, err := decisionengine.NewDelaySpecification(r, nil, u, c, f, nil, nil); err == nil {
		t.Error("expected error on nil view reader")
	}
	if _, err := decisionengine.NewDelaySpecification(r, v, nil, c, f, nil, nil); err == nil {
		t.Error("expected error on nil upgradable")
	}
	if _, err := decisionengine.NewDelaySpecification(r, v, u, nil, f, nil, nil); err == nil {
		t.Error("expected error on nil cfg")
	}
	if _, err := decisionengine.NewDelaySpecification(r, v, u, c, nil, nil, nil); err == nil {
		t.Error("expected error on nil files")
	}
}

func TestDelay_KindIsTemporary(t *testing.T) {
	t.Parallel()
	spec := newDelaySpec(t, baseDelayDeps(60))
	if spec.Kind() != decisionengine.RejectionTemporary {
		t.Errorf("Kind = %v, want Temporary", spec.Kind())
	}
}

func TestDelay_TooYoung_RejectsTemporary(t *testing.T) {
	t.Parallel()
	spec := newDelaySpec(t, baseDelayDeps(60))
	got := spec.IsSatisfiedBy(t.Context(), delaySubject(10, q720()), decisionengine.NewReleaseDecisionInformation())
	if got.Accepted || got.Reason != decisionengine.ReasonMinimumAgeDelay {
		t.Fatalf("young release should reject MinimumAgeDelay; got accepted=%v reason=%v", got.Accepted, got.Reason)
	}
}

func TestDelay_OldEnough_Accept(t *testing.T) {
	t.Parallel()
	spec := newDelaySpec(t, baseDelayDeps(60))
	got := spec.IsSatisfiedBy(t.Context(), delaySubject(120, q720()), decisionengine.NewReleaseDecisionInformation())
	if !got.Accepted {
		t.Errorf("release older than delay should Accept; reason=%v", got.Reason)
	}
}

func TestDelay_UserInvokedSearch_Accept(t *testing.T) {
	t.Parallel()
	spec := newDelaySpec(t, baseDelayDeps(60))
	info := decisionengine.ReleaseDecisionInformation{SearchCriteria: &decisionengine.SearchCriteria{UserInvokedSearch: true}}
	if !spec.IsSatisfiedBy(t.Context(), delaySubject(10, q720()), info).Accepted {
		t.Error("user-invoked search should bypass the delay")
	}
}

func TestDelay_ZeroDelay_Accept(t *testing.T) {
	t.Parallel()
	spec := newDelaySpec(t, baseDelayDeps(0))
	if !spec.IsSatisfiedBy(t.Context(), delaySubject(0, q720()), decisionengine.NewReleaseDecisionInformation()).Accepted {
		t.Error("zero delay should Accept")
	}
}

func TestDelay_ProperUpgradeBypass_Accept(t *testing.T) {
	t.Parallel()
	// Preferred protocol + propers preferred + a revision upgrade over an
	// existing file → skip the delay even though the release is young.
	deps := baseDelayDeps(60)
	deps.propers = quality.ProperPreferAndUpgrade
	deps.upgrade = true
	deps.files = map[int]*episodefiles.File{10: {ID: 10}}
	spec := newDelaySpec(t, deps)
	if !spec.IsSatisfiedBy(t.Context(), delaySubject(5, q720(), 10), decisionengine.NewReleaseDecisionInformation()).Accepted {
		t.Error("preferred-protocol proper upgrade should bypass the delay")
	}
}

func TestDelay_HighestQualityBypass_Accept(t *testing.T) {
	t.Parallel()
	// BypassIfHighestQuality + candidate quality == profile's last
	// allowed quality + preferred protocol → skip delay.
	deps := baseDelayDeps(60)
	deps.resolver.profile.BypassIfHighestQuality = true
	deps.view = stubDelayProfileView{
		found: true,
		view: &decisionengine.ProfileView{
			Profile: &qualityprofiles.Profile{},
			Items: []qualityprofiles.QualityItem{
				{ID: 1, Quality: quality.SDTV, Allowed: true},
				{ID: 2, Quality: quality.HDTV720p, Allowed: true},
			},
		},
	}
	spec := newDelaySpec(t, deps)
	if !spec.IsSatisfiedBy(t.Context(), delaySubject(5, q720()), decisionengine.NewReleaseDecisionInformation()).Accepted {
		t.Error("highest-quality-in-profile should bypass the delay")
	}
}

func TestDelay_CustomFormatScoreBypass_Accept(t *testing.T) {
	t.Parallel()
	deps := baseDelayDeps(60)
	deps.resolver.profile.BypassIfAboveCustomFormatScore = true
	min := 100
	deps.resolver.profile.MinimumCustomFormatScore = &min
	spec := newDelaySpec(t, deps)
	sub := delaySubject(5, q720())
	sub.CustomFormatScore = 150 // >= minimum
	if !spec.IsSatisfiedBy(t.Context(), sub, decisionengine.NewReleaseDecisionInformation()).Accepted {
		t.Error("custom-format score above minimum should bypass the delay")
	}
}

func TestDelay_OldestPendingBypass_Accept(t *testing.T) {
	t.Parallel()
	// An already-pending release for these episodes has waited longer
	// than the delay → grab the new one now.
	deps := baseDelayDeps(60)
	deps.pending = stubDelayPending{age: 120, found: true} // > 60-min delay
	spec := newDelaySpec(t, deps)
	// Pass one episode (no file) so the OldestPending lookup has an
	// episode-number set to key on.
	if !spec.IsSatisfiedBy(t.Context(), delaySubject(5, q720(), 0), decisionengine.NewReleaseDecisionInformation()).Accepted {
		t.Error("oldest pending older than delay should bypass")
	}
}

func TestDelay_NoDelayProfile_Accept(t *testing.T) {
	t.Parallel()
	deps := baseDelayDeps(60)
	deps.resolver = &stubDelayProfileResolver{ok: false} // no profile resolved
	spec := newDelaySpec(t, deps)
	if !spec.IsSatisfiedBy(t.Context(), delaySubject(5, q720()), decisionengine.NewReleaseDecisionInformation()).Accepted {
		t.Error("no delay profile → Accept")
	}
}
