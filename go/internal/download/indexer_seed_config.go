package download

import (
	"context"
	"fmt"
	"time"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
)

// IndexerSeedConfigProvider ports the (int, bool) overload of the C#
// `ISeedConfigProvider` at `Indexers/SeedConfigProvider.cs:10` — the
// indexer-side half of the seed-config lookup chain.
//
// Composes an [indexers.IndexerSettingsLookup] (returns cached
// per-indexer SeedCriteriaSettings) into the
// `*downloadclients.TorrentSeedConfiguration` that the cleanup-gate
// callers (RQBit / Flood / Tribler / etc.) consume.
//
// Lives in `internal/download` rather than `internal/indexers`
// because Go forbids the indexers→downloadclients import cycle that
// C# tolerates (C# is namespace-bidirectional). The composition
// stays here next to its consumer ([SeedConfigProvider] /
// [downloadSeedConfigProvider]); future indexer-factory wiring
// supplies the IndexerSettingsLookup.
//
// Satisfies the [IndexerSeedConfig] interface that
// [NewSeedConfigProvider] consumes at J91 — the missing piece that
// closes the seed-config audit gate flagged by RQBit/Flood/Tribler.
type IndexerSeedConfigProvider struct {
	lookup indexers.IndexerSettingsLookup
}

// NewIndexerSeedConfigProvider constructs the indexer-side provider.
// nil lookup panics at construction time — matching the C# DI
// posture at `Indexers/SeedConfigProvider.cs:17-20` (the
// constructor takes the dependency by reference and stores it
// verbatim; a null-resolved dependency would surface as an NRE on
// first call, but in practice the IoC container refuses to compose
// the class).
func NewIndexerSeedConfigProvider(lookup indexers.IndexerSettingsLookup) *IndexerSeedConfigProvider {
	if lookup == nil {
		panic("download: NewIndexerSeedConfigProvider: nil IndexerSettingsLookup")
	}
	return &IndexerSeedConfigProvider{lookup: lookup}
}

// GetSeedConfiguration ports the (int indexerId, bool fullSeason)
// overload at `Indexers/SeedConfigProvider.cs:37-68`.
//
// Algorithm tracks C# verbatim:
//  1. indexerId == 0 → (nil, nil). C# `:39-42`. The 0-sentinel is
//     "no indexer" — release came from a manual-add or pre-indexer-
//     attribution path where the IndexerID column wasn't populated.
//  2. Lookup the cached settings via [indexers.IndexerSettingsLookup].
//     C# `:44`. nil settings or nil SeedCriteriaSettings → (nil, nil)
//     per C# `:45-50` (the C# uses null-conditional `settings?.SeedCriteriaSettings`
//     so a nil settings record short-circuits the criteria fetch).
//  3. Compute the season-pack-goal toggle: C# `:52`.
//     `useSeasonPackSeedGoal = (SeasonPackSeedGoal == UseSeasonPackSeedGoal)`.
//     The Go port uses the named-constant comparison rather than the
//     raw int to stay readable.
//  4. Pick Ratio: C# `:55-58`. When the release is a full-season
//     pack AND the indexer opted into the season-pack-goal,
//     use SeasonPackSeedRatio; otherwise SeedRatio. nil propagates
//     through unchanged (the field is *float64 in the output).
//  5. Pick SeedTime in minutes: C# `:61`. Same full-season+goal
//     selector logic.
//  6. If the picked seedTime is non-nil, materialise into a
//     time.Duration. C# `:62-65` uses `TimeSpan.FromMinutes`; Go
//     uses `time.Duration(*seedTime) * time.Minute`. The duration is
//     stored as *time.Duration on the TorrentSeedConfiguration so
//     downstream consumers can detect "unset" without a sentinel
//     value.
//
// The C# tree never errors here — every failure mode short-circuits
// to a nil return. The Go signature carries an error slot because
// the IndexerSettingsLookup (production binding will hit a DB-backed
// cache) can fail; transient lookup errors propagate so callers can
// distinguish "no override" (nil cfg, nil err) from "couldn't
// determine" (nil cfg, non-nil err).
func (p *IndexerSeedConfigProvider) GetSeedConfiguration(ctx context.Context, indexerID int, fullSeason bool) (*downloadclients.TorrentSeedConfiguration, error) {
	if indexerID == 0 {
		return nil, nil
	}

	settings, err := p.lookup.GetSettings(ctx, indexerID)
	if err != nil {
		return nil, fmt.Errorf("download: IndexerSeedConfigProvider.GetSettings(indexerID=%d): %w", indexerID, err)
	}
	if settings == nil || settings.SeedCriteriaSettings == nil {
		return nil, nil
	}

	criteria := settings.SeedCriteriaSettings
	useSeasonPackGoal := criteria.SeasonPackSeedGoal == indexers.SeasonPackSeedGoalUseSeasonPack

	out := &downloadclients.TorrentSeedConfiguration{}

	out.Ratio = pickRatio(criteria, fullSeason, useSeasonPackGoal)

	seedTimeMinutes := pickSeedTimeMinutes(criteria, fullSeason, useSeasonPackGoal)
	if seedTimeMinutes != nil {
		d := time.Duration(*seedTimeMinutes) * time.Minute
		out.SeedTime = &d
	}

	return out, nil
}

// pickRatio selects between SeasonPackSeedRatio and SeedRatio per
// the C# ternary at `Indexers/SeedConfigProvider.cs:56-58`. Pulled
// out so the (int, bool) overload reads as a flat composition and
// the future (RemoteEpisode) overload can reuse it verbatim.
func pickRatio(criteria *indexers.SeedCriteriaSettings, fullSeason, useSeasonPackGoal bool) *float64 {
	if fullSeason && useSeasonPackGoal {
		return criteria.SeasonPackSeedRatio
	}
	return criteria.SeedRatio
}

// pickSeedTimeMinutes selects between SeasonPackSeedTime and
// SeedTime per the C# ternary at `Indexers/SeedConfigProvider.cs:61`.
// Returns *int (minutes) rather than *time.Duration so the caller
// owns the unit conversion at one place — keeps the C# wire
// shape (minutes) authoritative and the time.Duration
// materialisation purely a Go-port idiom.
func pickSeedTimeMinutes(criteria *indexers.SeedCriteriaSettings, fullSeason, useSeasonPackGoal bool) *int {
	if fullSeason && useSeasonPackGoal {
		return criteria.SeasonPackSeedTime
	}
	return criteria.SeedTime
}

// Compile-time guard: the [IndexerSeedConfigProvider] satisfies the
// [IndexerSeedConfig] interface that [NewSeedConfigProvider]
// consumes — the wiring slice can pass an
// `*IndexerSeedConfigProvider` directly without an adapter.
var _ IndexerSeedConfig = (*IndexerSeedConfigProvider)(nil)
