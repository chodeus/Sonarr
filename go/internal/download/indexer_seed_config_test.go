package download_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/download"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/parser"
)

// stubIndexerSettings is a hand-rolled [indexers.IndexerSettingsLookup].
// Keyed map lets each test seed per-indexer settings; counter
// confirms the impl actually consults the lookup (not the cache —
// caching is exercised in seed_config_provider_test.go).
type stubIndexerSettings struct {
	byID  map[int]*indexers.CachedIndexerSettings
	err   error
	calls int
}

func (s *stubIndexerSettings) GetSettings(_ context.Context, indexerID int) (*indexers.CachedIndexerSettings, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.byID[indexerID], nil
}

func ptrf(f float64) *float64 { return &f }
func ptri(i int) *int         { return &i }

func TestIndexerSeedConfigProvider_ZeroIndexerIDReturnsNil(t *testing.T) {
	t.Parallel()
	lookup := &stubIndexerSettings{}
	p := download.NewIndexerSeedConfigProvider(lookup)

	cfg, err := p.GetSeedConfiguration(context.Background(), 0, false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cfg != nil {
		t.Errorf("cfg=%v, want nil for indexerID=0", cfg)
	}
	if lookup.calls != 0 {
		t.Errorf("lookup called %d times for indexerID=0; want 0", lookup.calls)
	}
}

func TestIndexerSeedConfigProvider_UnknownIndexerReturnsNil(t *testing.T) {
	t.Parallel()
	lookup := &stubIndexerSettings{byID: map[int]*indexers.CachedIndexerSettings{}}
	p := download.NewIndexerSeedConfigProvider(lookup)

	cfg, err := p.GetSeedConfiguration(context.Background(), 5, false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cfg != nil {
		t.Errorf("cfg=%v, want nil for unknown indexer", cfg)
	}
}

func TestIndexerSeedConfigProvider_NilSeedCriteriaReturnsNil(t *testing.T) {
	t.Parallel()
	lookup := &stubIndexerSettings{byID: map[int]*indexers.CachedIndexerSettings{
		7: {FailDownloads: []indexers.FailDownloads{indexers.FailDownloadsExecutables}, SeedCriteriaSettings: nil},
	}}
	p := download.NewIndexerSeedConfigProvider(lookup)

	cfg, err := p.GetSeedConfiguration(context.Background(), 7, true)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cfg != nil {
		t.Errorf("cfg=%v, want nil when SeedCriteriaSettings is nil (usenet indexer)", cfg)
	}
}

func TestIndexerSeedConfigProvider_StandardGoalUsesSeedRatioForSingleEp(t *testing.T) {
	t.Parallel()
	lookup := &stubIndexerSettings{byID: map[int]*indexers.CachedIndexerSettings{
		1: {SeedCriteriaSettings: &indexers.SeedCriteriaSettings{
			SeedRatio:           ptrf(1.5),
			SeedTime:            ptri(60),
			SeasonPackSeedGoal:  indexers.SeasonPackSeedGoalUseStandard,
			SeasonPackSeedRatio: ptrf(3.0),
			SeasonPackSeedTime:  ptri(180),
		}},
	}}
	p := download.NewIndexerSeedConfigProvider(lookup)

	cfg, err := p.GetSeedConfiguration(context.Background(), 1, false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cfg == nil {
		t.Fatal("cfg=nil, want non-nil")
	}
	if cfg.Ratio == nil || *cfg.Ratio != 1.5 {
		t.Errorf("Ratio=%v, want 1.5", cfg.Ratio)
	}
	if cfg.SeedTime == nil || *cfg.SeedTime != 60*time.Minute {
		t.Errorf("SeedTime=%v, want 60m", cfg.SeedTime)
	}
}

func TestIndexerSeedConfigProvider_StandardGoalIgnoresFullSeasonOverrides(t *testing.T) {
	t.Parallel()
	// SeasonPackSeedGoal=UseStandard means even FullSeason releases
	// use SeedRatio/SeedTime, not the SeasonPack overrides.
	lookup := &stubIndexerSettings{byID: map[int]*indexers.CachedIndexerSettings{
		1: {SeedCriteriaSettings: &indexers.SeedCriteriaSettings{
			SeedRatio:           ptrf(1.5),
			SeedTime:            ptri(60),
			SeasonPackSeedGoal:  indexers.SeasonPackSeedGoalUseStandard,
			SeasonPackSeedRatio: ptrf(3.0),
			SeasonPackSeedTime:  ptri(180),
		}},
	}}
	p := download.NewIndexerSeedConfigProvider(lookup)

	cfg, err := p.GetSeedConfiguration(context.Background(), 1, true)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cfg.Ratio == nil || *cfg.Ratio != 1.5 {
		t.Errorf("Ratio=%v, want 1.5 (standard goal ignores fullSeason)", cfg.Ratio)
	}
	if cfg.SeedTime == nil || *cfg.SeedTime != 60*time.Minute {
		t.Errorf("SeedTime=%v, want 60m", cfg.SeedTime)
	}
}

func TestIndexerSeedConfigProvider_SeasonPackGoalSwitchesForFullSeason(t *testing.T) {
	t.Parallel()
	lookup := &stubIndexerSettings{byID: map[int]*indexers.CachedIndexerSettings{
		1: {SeedCriteriaSettings: &indexers.SeedCriteriaSettings{
			SeedRatio:           ptrf(1.5),
			SeedTime:            ptri(60),
			SeasonPackSeedGoal:  indexers.SeasonPackSeedGoalUseSeasonPack,
			SeasonPackSeedRatio: ptrf(3.0),
			SeasonPackSeedTime:  ptri(180),
		}},
	}}
	p := download.NewIndexerSeedConfigProvider(lookup)

	cfg, err := p.GetSeedConfiguration(context.Background(), 1, true)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cfg.Ratio == nil || *cfg.Ratio != 3.0 {
		t.Errorf("Ratio=%v, want 3.0 (season-pack goal + fullSeason)", cfg.Ratio)
	}
	if cfg.SeedTime == nil || *cfg.SeedTime != 180*time.Minute {
		t.Errorf("SeedTime=%v, want 180m", cfg.SeedTime)
	}
}

func TestIndexerSeedConfigProvider_SeasonPackGoalReverts_ForSingleEp(t *testing.T) {
	t.Parallel()
	// Even with UseSeasonPackSeedGoal, a single-episode release
	// (fullSeason=false) still uses SeedRatio/SeedTime — the toggle
	// only swings when BOTH conditions hold.
	lookup := &stubIndexerSettings{byID: map[int]*indexers.CachedIndexerSettings{
		1: {SeedCriteriaSettings: &indexers.SeedCriteriaSettings{
			SeedRatio:           ptrf(1.5),
			SeedTime:            ptri(60),
			SeasonPackSeedGoal:  indexers.SeasonPackSeedGoalUseSeasonPack,
			SeasonPackSeedRatio: ptrf(3.0),
			SeasonPackSeedTime:  ptri(180),
		}},
	}}
	p := download.NewIndexerSeedConfigProvider(lookup)

	cfg, err := p.GetSeedConfiguration(context.Background(), 1, false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cfg.Ratio == nil || *cfg.Ratio != 1.5 {
		t.Errorf("Ratio=%v, want 1.5", cfg.Ratio)
	}
	if cfg.SeedTime == nil || *cfg.SeedTime != 60*time.Minute {
		t.Errorf("SeedTime=%v, want 60m", cfg.SeedTime)
	}
}

func TestIndexerSeedConfigProvider_NilSeedTimeStaysNil(t *testing.T) {
	t.Parallel()
	// C# `:62-65` — if seedTime.HasValue==false, no SeedTime is
	// materialised on the TorrentSeedConfiguration. The Go port
	// leaves the field nil rather than zero-valuing it.
	lookup := &stubIndexerSettings{byID: map[int]*indexers.CachedIndexerSettings{
		1: {SeedCriteriaSettings: &indexers.SeedCriteriaSettings{
			SeedRatio: ptrf(2.0),
			// SeedTime: nil
		}},
	}}
	p := download.NewIndexerSeedConfigProvider(lookup)

	cfg, err := p.GetSeedConfiguration(context.Background(), 1, false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cfg == nil {
		t.Fatal("cfg=nil")
	}
	if cfg.Ratio == nil || *cfg.Ratio != 2.0 {
		t.Errorf("Ratio=%v, want 2.0", cfg.Ratio)
	}
	if cfg.SeedTime != nil {
		t.Errorf("SeedTime=%v, want nil", cfg.SeedTime)
	}
}

func TestIndexerSeedConfigProvider_NilRatioStaysNil(t *testing.T) {
	t.Parallel()
	lookup := &stubIndexerSettings{byID: map[int]*indexers.CachedIndexerSettings{
		1: {SeedCriteriaSettings: &indexers.SeedCriteriaSettings{
			// SeedRatio: nil
			SeedTime: ptri(45),
		}},
	}}
	p := download.NewIndexerSeedConfigProvider(lookup)

	cfg, err := p.GetSeedConfiguration(context.Background(), 1, false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cfg == nil {
		t.Fatal("cfg=nil")
	}
	if cfg.Ratio != nil {
		t.Errorf("Ratio=%v, want nil", cfg.Ratio)
	}
	if cfg.SeedTime == nil || *cfg.SeedTime != 45*time.Minute {
		t.Errorf("SeedTime=%v, want 45m", cfg.SeedTime)
	}
}

func TestIndexerSeedConfigProvider_LookupErrorPropagates(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("settings down")
	lookup := &stubIndexerSettings{err: wantErr}
	p := download.NewIndexerSeedConfigProvider(lookup)

	cfg, err := p.GetSeedConfiguration(context.Background(), 1, false)
	if cfg != nil {
		t.Errorf("cfg=%v, want nil on lookup error", cfg)
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("err=%v, want wrap of %v", err, wantErr)
	}
}

func TestIndexerSeedConfigProvider_NilLookupPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic on nil IndexerSettingsLookup")
		}
	}()
	download.NewIndexerSeedConfigProvider(nil)
}

// TestIndexerSeedConfigProvider_SatisfiesIndexerSeedConfig confirms
// the J91 IndexerSeedConfig interface — i.e. an
// *IndexerSeedConfigProvider can be passed directly to
// NewSeedConfigProvider once the wiring slice lands. The compile-time
// guard at the bottom of indexer_seed_config.go enforces this, but
// the test makes the relationship explicit in the test surface so
// a regression doesn't sneak through a refactor.
func TestIndexerSeedConfigProvider_SatisfiesIndexerSeedConfig(t *testing.T) {
	t.Parallel()
	lookup := &stubIndexerSettings{byID: map[int]*indexers.CachedIndexerSettings{}}
	var _ download.IndexerSeedConfig = download.NewIndexerSeedConfigProvider(lookup)
}

// --- J97 GetSeedConfigurationForSubject (RemoteEpisode overload) ---

func newTorrentSubject(indexerID int, fullSeason bool) *decisionengine.Subject {
	return &decisionengine.Subject{
		Release: &parser.ReleaseInfo{
			IndexerID:        indexerID,
			DownloadProtocol: indexers.DownloadProtocolTorrent,
			Title:            "T",
		},
		ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{
			FullSeason: fullSeason,
		},
	}
}

func TestGetSeedConfigurationForSubject_NilSubjectReturnsNil(t *testing.T) {
	t.Parallel()
	lookup := &stubIndexerSettings{}
	p := download.NewIndexerSeedConfigProvider(lookup)
	cfg, err := p.GetSeedConfigurationForSubject(context.Background(), nil)
	if cfg != nil || err != nil {
		t.Errorf("nil subject: cfg=%v err=%v, want both nil", cfg, err)
	}
}

func TestGetSeedConfigurationForSubject_NilReleaseReturnsNil(t *testing.T) {
	t.Parallel()
	lookup := &stubIndexerSettings{}
	p := download.NewIndexerSeedConfigProvider(lookup)
	cfg, err := p.GetSeedConfigurationForSubject(context.Background(), &decisionengine.Subject{Release: nil})
	if cfg != nil || err != nil {
		t.Errorf("nil release: cfg=%v err=%v", cfg, err)
	}
}

func TestGetSeedConfigurationForSubject_UsenetProtocolReturnsNil(t *testing.T) {
	t.Parallel()
	lookup := &stubIndexerSettings{}
	p := download.NewIndexerSeedConfigProvider(lookup)
	subject := &decisionengine.Subject{
		Release: &parser.ReleaseInfo{
			IndexerID:        7,
			DownloadProtocol: indexers.DownloadProtocolUsenet,
		},
		ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{},
	}
	cfg, err := p.GetSeedConfigurationForSubject(context.Background(), subject)
	if cfg != nil || err != nil {
		t.Errorf("usenet protocol: cfg=%v err=%v", cfg, err)
	}
}

func TestGetSeedConfigurationForSubject_ZeroIndexerIDReturnsNil(t *testing.T) {
	t.Parallel()
	lookup := &stubIndexerSettings{}
	p := download.NewIndexerSeedConfigProvider(lookup)
	cfg, err := p.GetSeedConfigurationForSubject(context.Background(), newTorrentSubject(0, false))
	if cfg != nil || err != nil {
		t.Errorf("indexerID=0: cfg=%v err=%v", cfg, err)
	}
}

func TestGetSeedConfigurationForSubject_DelegatesWithFullSeason(t *testing.T) {
	t.Parallel()
	lookup := &stubIndexerSettings{byID: map[int]*indexers.CachedIndexerSettings{
		11: {SeedCriteriaSettings: &indexers.SeedCriteriaSettings{
			SeedRatio:           ptrf(1.5),
			SeasonPackSeedGoal:  indexers.SeasonPackSeedGoalUseSeasonPack,
			SeasonPackSeedRatio: ptrf(3.0),
		}},
	}}
	p := download.NewIndexerSeedConfigProvider(lookup)

	cfg, err := p.GetSeedConfigurationForSubject(context.Background(), newTorrentSubject(11, true))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cfg == nil || cfg.Ratio == nil || *cfg.Ratio != 3.0 {
		t.Errorf("fullSeason+seasonPackGoal: Ratio=%v, want 3.0 (delegated to int-overload)", cfg)
	}
}

func TestGetSeedConfigurationForSubject_DelegatesWithoutFullSeason(t *testing.T) {
	t.Parallel()
	lookup := &stubIndexerSettings{byID: map[int]*indexers.CachedIndexerSettings{
		11: {SeedCriteriaSettings: &indexers.SeedCriteriaSettings{
			SeedRatio: ptrf(1.5),
		}},
	}}
	p := download.NewIndexerSeedConfigProvider(lookup)

	cfg, err := p.GetSeedConfigurationForSubject(context.Background(), newTorrentSubject(11, false))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cfg == nil || cfg.Ratio == nil || *cfg.Ratio != 1.5 {
		t.Errorf("single-episode: Ratio=%v, want 1.5", cfg)
	}
}

func TestGetSeedConfigurationForSubject_TorrentInfoReleaseShape(t *testing.T) {
	t.Parallel()
	// Subject.Release can be either *parser.ReleaseInfo or
	// *parser.TorrentInfo (which embeds ReleaseInfo). The Go port's
	// subjectReleaseInfo narrows both shapes — exercise the
	// torrent-info branch.
	lookup := &stubIndexerSettings{byID: map[int]*indexers.CachedIndexerSettings{
		22: {SeedCriteriaSettings: &indexers.SeedCriteriaSettings{
			SeedRatio: ptrf(2.0),
		}},
	}}
	p := download.NewIndexerSeedConfigProvider(lookup)
	subject := &decisionengine.Subject{
		Release: &parser.TorrentInfo{
			ReleaseInfo: parser.ReleaseInfo{
				IndexerID:        22,
				DownloadProtocol: indexers.DownloadProtocolTorrent,
				Title:            "Show.S01E02",
			},
		},
		ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{},
	}

	cfg, err := p.GetSeedConfigurationForSubject(context.Background(), subject)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cfg == nil || cfg.Ratio == nil || *cfg.Ratio != 2.0 {
		t.Errorf("TorrentInfo release shape: Ratio=%v, want 2.0", cfg)
	}
}

func TestGetSeedConfigurationForSubject_NilPEITreatsAsSingleEpisode(t *testing.T) {
	t.Parallel()
	// Go-port divergence: C# would NRE on null ParsedEpisodeInfo;
	// Go treats missing PEI as fullSeason=false (single episode
	// fallback - more conservative gate).
	lookup := &stubIndexerSettings{byID: map[int]*indexers.CachedIndexerSettings{
		33: {SeedCriteriaSettings: &indexers.SeedCriteriaSettings{
			SeedRatio:           ptrf(1.0),
			SeasonPackSeedGoal:  indexers.SeasonPackSeedGoalUseSeasonPack,
			SeasonPackSeedRatio: ptrf(5.0),
		}},
	}}
	p := download.NewIndexerSeedConfigProvider(lookup)
	subject := &decisionengine.Subject{
		Release: &parser.ReleaseInfo{
			IndexerID:        33,
			DownloadProtocol: indexers.DownloadProtocolTorrent,
		},
		ParsedEpisodeInfo: nil,
	}
	cfg, err := p.GetSeedConfigurationForSubject(context.Background(), subject)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cfg == nil || cfg.Ratio == nil || *cfg.Ratio != 1.0 {
		t.Errorf("nil PEI: Ratio=%v, want 1.0 (single-episode fallback)", cfg)
	}
}
