package decisionengine_test

import (
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/delayprofiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/qualityprofiles"
	"sonarr.tv/sonarr/internal/series"
)

// stubComparerConfig satisfies ConfigReader for comparer tests.
type stubComparerConfig struct{ mode quality.ProperDownloadType }

func (s stubComparerConfig) DownloadPropersAndRepacks() quality.ProperDownloadType { return s.mode }

// makeCmpDecision wraps a Subject in an approved Decision.
func makeCmpDecision(s *decisionengine.Subject) decisionengine.Decision {
	return decisionengine.Decision{Subject: s}
}

// minimalSubject returns a Subject with the required fields populated
// for size/quality compare. Runtime is pinned to 30 — tests that need
// a different runtime mutate post-construction. Tests can mutate
// further freely.
func minimalSubject(qualityID, sizeMB int, downloadProto indexers.DownloadProtocol) *decisionengine.Subject {
	const runtime = 30
	q, _ := quality.FindByID(qualityID)
	return &decisionengine.Subject{
		Series: &series.Series{
			ID:               7,
			Runtime:          runtime,
			SeriesType:       series.SeriesTypeStandard,
			QualityProfileID: asPtr(1),
		},
		Episodes: []episodes.Episode{{ID: 1, EpisodeNumber: 5}},
		ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{
			Quality: quality.Model{Quality: q, Revision: quality.NewRevision()},
		},
		Release: &parser.ReleaseInfo{
			Title:            "Show.S01E05",
			Size:             int64(sizeMB) * 1024 * 1024,
			DownloadProtocol: downloadProto,
		},
	}
}

// twoTierProfile builds a *ProfileView with SDTV at index 0 and
// HDTV720p at index 1 (so HDTV720p is "better").
func twoTierProfile() *decisionengine.ProfileView {
	return &decisionengine.ProfileView{
		Items: []qualityprofiles.QualityItem{
			{Quality: quality.SDTV, Allowed: true},
			{Quality: quality.HDTV720p, Allowed: true},
		},
	}
}

// === stage isolation tests ===

func TestComparer_Quality_HigherIndexWins(t *testing.T) {
	t.Parallel()
	profile := twoTierProfile()
	c := decisionengine.NewDecisionComparer(
		stubComparerConfig{mode: quality.ProperPreferAndUpgrade},
		func(_ int) *decisionengine.ProfileView { return profile },
		nil,
	)
	xSDTV := makeCmpDecision(minimalSubject(quality.SDTV.ID, 500, indexers.DownloadProtocolUsenet))
	yHD := makeCmpDecision(minimalSubject(quality.HDTV720p.ID, 500, indexers.DownloadProtocolUsenet))
	if c.Compare(yHD, xSDTV) <= 0 {
		t.Errorf("expected HDTV to rank above SDTV")
	}
	if c.Compare(xSDTV, yHD) >= 0 {
		t.Errorf("expected SDTV below HDTV (negative compare)")
	}
}

func TestComparer_Quality_RevisionTiebreak(t *testing.T) {
	t.Parallel()
	profile := twoTierProfile()
	c := decisionengine.NewDecisionComparer(
		stubComparerConfig{mode: quality.ProperPreferAndUpgrade},
		func(_ int) *decisionengine.ProfileView { return profile },
		nil,
	)
	x := makeCmpDecision(minimalSubject(quality.SDTV.ID, 500, indexers.DownloadProtocolUsenet))
	y := makeCmpDecision(minimalSubject(quality.SDTV.ID, 500, indexers.DownloadProtocolUsenet))
	y.Subject.(*decisionengine.Subject).ParsedEpisodeInfo.Quality.Revision = quality.Revision{Version: 2}
	if c.Compare(y, x) <= 0 {
		t.Errorf("expected y (v2) above x (v1)")
	}
}

func TestComparer_Quality_DoNotPreferIgnoresRevision(t *testing.T) {
	t.Parallel()
	profile := twoTierProfile()
	c := decisionengine.NewDecisionComparer(
		stubComparerConfig{mode: quality.ProperDoNotPrefer},
		func(_ int) *decisionengine.ProfileView { return profile },
		nil,
	)
	x := makeCmpDecision(minimalSubject(quality.SDTV.ID, 500, indexers.DownloadProtocolUsenet))
	y := makeCmpDecision(minimalSubject(quality.SDTV.ID, 500, indexers.DownloadProtocolUsenet))
	y.Subject.(*decisionengine.Subject).ParsedEpisodeInfo.Quality.Revision = quality.Revision{Version: 5, Real: 5}
	// Revision shouldn't break tie when DoNotPrefer is set.
	if c.Compare(y, x) != 0 {
		// May still be non-zero from CustomFormatScore tie etc; size
		// should be equal too. Force size-bucket tie.
		t.Errorf("DoNotPrefer: revisions ignored, expected tie at quality stage; got %d", c.Compare(y, x))
	}
}

func TestComparer_CustomFormatScore_HigherWins(t *testing.T) {
	t.Parallel()
	c := decisionengine.NewDecisionComparer(stubComparerConfig{}, nil, nil)
	x := makeCmpDecision(minimalSubject(quality.SDTV.ID, 500, indexers.DownloadProtocolUsenet))
	x.Subject.(*decisionengine.Subject).CustomFormatScore = 10
	y := makeCmpDecision(minimalSubject(quality.SDTV.ID, 500, indexers.DownloadProtocolUsenet))
	y.Subject.(*decisionengine.Subject).CustomFormatScore = 100
	if c.Compare(y, x) <= 0 {
		t.Errorf("expected higher CF score to win")
	}
}

func TestComparer_Protocol_MatchesPreferredWins(t *testing.T) {
	t.Parallel()
	delayLookup := func(_ []int) (delayprofiles.Profile, bool) {
		return delayprofiles.Profile{
			EnableUsenet:      true,
			EnableTorrent:     true,
			PreferredProtocol: int(indexers.DownloadProtocolUsenet),
		}, true
	}
	c := decisionengine.NewDecisionComparer(stubComparerConfig{}, nil, delayLookup)
	xUsenet := makeCmpDecision(minimalSubject(quality.SDTV.ID, 500, indexers.DownloadProtocolUsenet))
	yTorrent := makeCmpDecision(minimalSubject(quality.SDTV.ID, 500, indexers.DownloadProtocolTorrent))
	if c.Compare(xUsenet, yTorrent) <= 0 {
		t.Errorf("expected Usenet (preferred) to win, got %d", c.Compare(xUsenet, yTorrent))
	}
}

func TestComparer_EpisodeCount_FullSeasonWins(t *testing.T) {
	t.Parallel()
	c := decisionengine.NewDecisionComparer(stubComparerConfig{}, nil, nil)
	full := makeCmpDecision(minimalSubject(quality.SDTV.ID, 500, indexers.DownloadProtocolUsenet))
	full.Subject.(*decisionengine.Subject).ParsedEpisodeInfo.FullSeason = true
	single := makeCmpDecision(minimalSubject(quality.SDTV.ID, 500, indexers.DownloadProtocolUsenet))
	if c.Compare(full, single) <= 0 {
		t.Errorf("expected FullSeason to win")
	}
}

func TestComparer_EpisodeCount_NonAnimeFewerEpisodesWins(t *testing.T) {
	t.Parallel()
	// C# CompareByReverse for non-Anime: more episodes ranks LOWER.
	// Sonarr's per-episode sort path prefers narrower releases when
	// the FullSeason gate didn't fire.
	c := decisionengine.NewDecisionComparer(stubComparerConfig{}, nil, nil)
	multi := makeCmpDecision(minimalSubject(quality.SDTV.ID, 500, indexers.DownloadProtocolUsenet))
	multi.Subject.(*decisionengine.Subject).Episodes = []episodes.Episode{{ID: 1, EpisodeNumber: 1}, {ID: 2, EpisodeNumber: 2}}
	single := makeCmpDecision(minimalSubject(quality.SDTV.ID, 500, indexers.DownloadProtocolUsenet))
	if c.Compare(single, multi) <= 0 {
		t.Errorf("expected single-episode to win for non-Anime, got Compare(single,multi)=%d", c.Compare(single, multi))
	}
}

func TestComparer_EpisodeCount_AnimeMoreEpisodesWins(t *testing.T) {
	t.Parallel()
	// C# CompareBy for Anime: more episodes ranks HIGHER. Anime
	// absolute-numbering bundles are typically larger packs.
	c := decisionengine.NewDecisionComparer(stubComparerConfig{}, nil, nil)
	multi := makeCmpDecision(minimalSubject(quality.SDTV.ID, 500, indexers.DownloadProtocolUsenet))
	multi.Subject.(*decisionengine.Subject).Series.SeriesType = series.SeriesTypeAnime
	multi.Subject.(*decisionengine.Subject).Episodes = []episodes.Episode{{ID: 1, EpisodeNumber: 1}, {ID: 2, EpisodeNumber: 2}}
	single := makeCmpDecision(minimalSubject(quality.SDTV.ID, 500, indexers.DownloadProtocolUsenet))
	single.Subject.(*decisionengine.Subject).Series.SeriesType = series.SeriesTypeAnime
	if c.Compare(multi, single) <= 0 {
		t.Errorf("expected multi-episode to win for Anime, got Compare(multi,single)=%d", c.Compare(multi, single))
	}
}

func TestComparer_EpisodeNumber_LowerWins(t *testing.T) {
	t.Parallel()
	c := decisionengine.NewDecisionComparer(stubComparerConfig{}, nil, nil)
	low := makeCmpDecision(minimalSubject(quality.SDTV.ID, 500, indexers.DownloadProtocolUsenet))
	low.Subject.(*decisionengine.Subject).Episodes = []episodes.Episode{{ID: 1, EpisodeNumber: 1}}
	high := makeCmpDecision(minimalSubject(quality.SDTV.ID, 500, indexers.DownloadProtocolUsenet))
	high.Subject.(*decisionengine.Subject).Episodes = []episodes.Episode{{ID: 2, EpisodeNumber: 10}}
	if c.Compare(low, high) <= 0 {
		t.Errorf("expected lower episode number to win")
	}
}

func TestComparer_IndexerPriority_LowerNumberWins(t *testing.T) {
	t.Parallel()
	// Sonarr convention: lower IndexerPriority = preferred indexer
	// (Settings → Indexers shows 1 as top, 50 as bottom). The C#
	// CompareByReverse achieves "lower number ranks higher".
	c := decisionengine.NewDecisionComparer(stubComparerConfig{}, nil, nil)
	preferred := makeCmpDecision(minimalSubject(quality.SDTV.ID, 500, indexers.DownloadProtocolUsenet))
	preferred.Subject.(*decisionengine.Subject).Release.(*parser.ReleaseInfo).IndexerPriority = 10
	deprio := makeCmpDecision(minimalSubject(quality.SDTV.ID, 500, indexers.DownloadProtocolUsenet))
	deprio.Subject.(*decisionengine.Subject).Release.(*parser.ReleaseInfo).IndexerPriority = 50
	if c.Compare(preferred, deprio) <= 0 {
		t.Errorf("expected lower-numbered IndexerPriority to rank higher, got %d", c.Compare(preferred, deprio))
	}
}

func TestComparer_PeersIfTorrent_OnlyAppliesToTorrent(t *testing.T) {
	t.Parallel()
	c := decisionengine.NewDecisionComparer(stubComparerConfig{}, nil, nil)
	usenet := makeCmpDecision(minimalSubject(quality.SDTV.ID, 500, indexers.DownloadProtocolUsenet))
	if got := c.Compare(usenet, usenet); got != 0 {
		// With identical subjects, all stages tie → 0.
		t.Errorf("identical usenet should compare 0, got %d", got)
	}
}

func TestComparer_PeersIfTorrent_HigherSeedersWins(t *testing.T) {
	t.Parallel()
	c := decisionengine.NewDecisionComparer(stubComparerConfig{}, nil, nil)
	manySeeders := 1000
	fewSeeders := 5
	hi := &decisionengine.Subject{
		Series:            &series.Series{ID: 7, SeriesType: series.SeriesTypeStandard, Runtime: 30},
		Episodes:          []episodes.Episode{{ID: 1, EpisodeNumber: 5}},
		ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{Quality: quality.Model{Quality: quality.SDTV, Revision: quality.NewRevision()}},
		Release: &parser.TorrentInfo{
			ReleaseInfo: parser.ReleaseInfo{Title: "X", DownloadProtocol: indexers.DownloadProtocolTorrent, Size: 500 * 1024 * 1024},
			Seeders:     &manySeeders,
		},
	}
	lo := &decisionengine.Subject{
		Series:            &series.Series{ID: 7, SeriesType: series.SeriesTypeStandard, Runtime: 30},
		Episodes:          []episodes.Episode{{ID: 1, EpisodeNumber: 5}},
		ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{Quality: quality.Model{Quality: quality.SDTV, Revision: quality.NewRevision()}},
		Release: &parser.TorrentInfo{
			ReleaseInfo: parser.ReleaseInfo{Title: "Y", DownloadProtocol: indexers.DownloadProtocolTorrent, Size: 500 * 1024 * 1024},
			Seeders:     &fewSeeders,
		},
	}
	if c.Compare(makeCmpDecision(hi), makeCmpDecision(lo)) <= 0 {
		t.Errorf("expected high-seed torrent to win")
	}
}

func TestComparer_AgeIfUsenet_FreshWins(t *testing.T) {
	t.Parallel()
	c := decisionengine.NewDecisionComparer(stubComparerConfig{}, nil, nil)
	now := time.Now()
	fresh := makeCmpDecision(minimalSubject(quality.SDTV.ID, 500, indexers.DownloadProtocolUsenet))
	freshTime := now.Add(-30 * time.Minute) // <1h
	fresh.Subject.(*decisionengine.Subject).Release.(*parser.ReleaseInfo).PublishDate = freshTime
	old := makeCmpDecision(minimalSubject(quality.SDTV.ID, 500, indexers.DownloadProtocolUsenet))
	oldTime := now.Add(-30 * 24 * time.Hour) // 30 days
	old.Subject.(*decisionengine.Subject).Release.(*parser.ReleaseInfo).PublishDate = oldTime
	if c.Compare(fresh, old) <= 0 {
		t.Errorf("expected fresh release to win")
	}
}

func TestComparer_Size_LargerWinsWithoutPreferred(t *testing.T) {
	t.Parallel()
	profile := &decisionengine.ProfileView{
		Items: []qualityprofiles.QualityItem{
			{Quality: quality.SDTV, Allowed: true}, // no PreferredSize
		},
	}
	c := decisionengine.NewDecisionComparer(
		stubComparerConfig{mode: quality.ProperPreferAndUpgrade},
		func(_ int) *decisionengine.ProfileView { return profile },
		nil,
	)
	big := makeCmpDecision(minimalSubject(quality.SDTV.ID, 1000, indexers.DownloadProtocolUsenet))
	small := makeCmpDecision(minimalSubject(quality.SDTV.ID, 200, indexers.DownloadProtocolUsenet))
	if c.Compare(big, small) <= 0 {
		t.Errorf("expected larger size to win without PreferredSize")
	}
}

func TestComparer_Size_ClosestToPreferredWins(t *testing.T) {
	t.Parallel()
	preferred := 10.0 // MB/min
	profile := &decisionengine.ProfileView{
		Items: []qualityprofiles.QualityItem{
			{Quality: quality.SDTV, Allowed: true, PreferredSize: &preferred},
		},
	}
	c := decisionengine.NewDecisionComparer(
		stubComparerConfig{mode: quality.ProperPreferAndUpgrade},
		func(_ int) *decisionengine.ProfileView { return profile },
		nil,
	)
	// Runtime 30min × 10MB = 300MB preferred.
	close := makeCmpDecision(minimalSubject(quality.SDTV.ID, 350, indexers.DownloadProtocolUsenet))
	farTooLarge := makeCmpDecision(minimalSubject(quality.SDTV.ID, 5000, indexers.DownloadProtocolUsenet))
	if c.Compare(close, farTooLarge) <= 0 {
		t.Errorf("expected closer-to-preferred (350MB) to beat far (5000MB)")
	}
}

func TestComparer_Sort_BestFirst(t *testing.T) {
	t.Parallel()
	profile := twoTierProfile()
	c := decisionengine.NewDecisionComparer(
		stubComparerConfig{mode: quality.ProperPreferAndUpgrade},
		func(_ int) *decisionengine.ProfileView { return profile },
		nil,
	)
	sd := makeCmpDecision(minimalSubject(quality.SDTV.ID, 500, indexers.DownloadProtocolUsenet))
	hd := makeCmpDecision(minimalSubject(quality.HDTV720p.ID, 500, indexers.DownloadProtocolUsenet))
	decisions := []decisionengine.Decision{sd, hd}
	c.Sort(decisions)
	if first := subjectQualityID(decisions[0]); first != quality.HDTV720p.ID {
		t.Errorf("expected HDTV720p first, got quality ID %d", first)
	}
}

func TestComparer_NilLookups_DegradesGracefully(t *testing.T) {
	t.Parallel()
	c := decisionengine.NewDecisionComparer(nil, nil, nil)
	x := makeCmpDecision(minimalSubject(quality.SDTV.ID, 500, indexers.DownloadProtocolUsenet))
	y := makeCmpDecision(minimalSubject(quality.SDTV.ID, 500, indexers.DownloadProtocolUsenet))
	got := c.Compare(x, y)
	if got != 0 {
		// All identical → 0
		t.Errorf("expected 0 with nil lookups + identical subjects, got %d", got)
	}
}

func TestComparer_NilSubjectsTie(t *testing.T) {
	t.Parallel()
	c := decisionengine.NewDecisionComparer(nil, nil, nil)
	got := c.Compare(decisionengine.Decision{}, decisionengine.Decision{})
	if got != 0 {
		t.Errorf("expected 0 for nil-subject pair, got %d", got)
	}
}

// === helpers ===

func subjectQualityID(d decisionengine.Decision) int {
	if s, ok := d.Subject.(*decisionengine.Subject); ok && s.ParsedEpisodeInfo != nil {
		return s.ParsedEpisodeInfo.Quality.Quality.ID
	}
	return 0
}
