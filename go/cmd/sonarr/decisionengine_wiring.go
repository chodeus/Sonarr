// Boot-time adapters that bridge the production services into the
// per-spec narrow interfaces declared in `internal/decisionengine`.
// Same posture as `downloadedimport_wiring.go`: an adapter where a
// real type exists but its signature differs (presence-as-bool vs
// sentinel-error, package-level free fns, multi-row → first-row
// reductions); a tiny noop reader where the C# IConfigService getter
// hasn't ported.
//
// Each adapter is drop-in replaceable as the upstream subsystem
// extends. The `decisionengine.DecisionMaker` constructor takes a
// `[]Specification`; this file's `buildDecisionEngineSpecs` builds
// that slice from the boot-site service handles.

package main

import (
	"context"
	"errors"
	"log/slog"

	"sonarr.tv/sonarr/internal/blocklist"
	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/delayprofiles"
	"sonarr.tv/sonarr/internal/disk"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/qualityprofiles"
	"sonarr.tv/sonarr/internal/releaseprofiles"
)

// profileViewLookupAdapter wraps `*qualityprofiles.Service` to satisfy
// both [decisionengine.AcceptableSizeProfileReader] and
// [decisionengine.QualityAllowedByProfileProfileReader] (same shape —
// `LookupProfile(ctx, id) (*ProfileView, bool, error)`). Resolves the
// row via Service.Get and parses the embedded JSON Items / FormatItems
// columns once via [decisionengine.NewProfileView]. ErrNotFound
// classification surfaces as `(nil, false, nil)`; any other error is
// returned verbatim.
type profileViewLookupAdapter struct {
	svc *qualityprofiles.Service
}

func (a profileViewLookupAdapter) LookupProfile(ctx context.Context, qualityProfileID int) (*decisionengine.ProfileView, bool, error) {
	p, err := a.svc.Get(ctx, qualityProfileID)
	if err != nil {
		if errors.Is(err, qualityprofiles.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	view, err := decisionengine.NewProfileView(p)
	if err != nil {
		return nil, false, err
	}
	return view, true, nil
}

// firstEpisodeOfSeasonAdapter wraps `*episodes.Service.GetEpisodesBySeason`
// and reduces the multi-row result to the first row to satisfy
// [decisionengine.AcceptableSizeEpisodeFinder]. The multi-row → first
// reduction matches the C# call chain at
// `AcceptableSizeSpecification.cs:48-52` —
// `_episodeService.GetEpisodesBySeasonNumber(seriesId, seasonNumber).First()`.
// Empty result → `(zero, false, nil)`. Any DB error surfaces verbatim.
type firstEpisodeOfSeasonAdapter struct {
	svc *episodes.Service
}

func (a firstEpisodeOfSeasonAdapter) FirstEpisodeOfSeason(ctx context.Context, seriesID, seasonNumber int) (episodes.Episode, bool, error) {
	rows, err := a.svc.GetEpisodesBySeason(ctx, seriesID, seasonNumber)
	if err != nil {
		return episodes.Episode{}, false, err
	}
	if len(rows) == 0 {
		return episodes.Episode{}, false, nil
	}
	return rows[0], true, nil
}

// delayProfileResolverAdapter wraps `*delayprofiles.Service.BestForTags`
// and translates its `(*Profile, error)` result to the
// [decisionengine.DelayProfileResolver] presence-as-bool shape
// `(Profile, bool, error)`. ErrNotFound → `(zero, false, nil)`.
type delayProfileResolverAdapter struct {
	svc *delayprofiles.Service
}

func (a delayProfileResolverAdapter) BestForTags(ctx context.Context, tagIDs []int) (delayprofiles.Profile, bool, error) {
	p, err := a.svc.BestForTags(ctx, tagIDs)
	if err != nil {
		if errors.Is(err, delayprofiles.ErrNotFound) {
			return delayprofiles.Profile{}, false, nil
		}
		return delayprofiles.Profile{}, false, err
	}
	if p == nil {
		return delayprofiles.Profile{}, false, nil
	}
	return *p, true, nil
}

// Compile-time guards: every adapter satisfies the corresponding
// per-spec narrow interface in `internal/decisionengine`. A signature
// drift on either side breaks the build at this site.
var (
	_ decisionengine.AcceptableSizeProfileReader          = profileViewLookupAdapter{}
	_ decisionengine.QualityAllowedByProfileProfileReader = profileViewLookupAdapter{}
	_ decisionengine.AcceptableSizeEpisodeFinder          = firstEpisodeOfSeasonAdapter{}
	_ decisionengine.DelayProfileResolver                 = delayProfileResolverAdapter{}
)

// decisionEngineDeps groups the production handles
// [buildDecisionEngineSpecs] needs. Bundled into a struct so the
// caller (main.go serve) doesn't grow a 12-arg signature.
type decisionEngineDeps struct {
	upgradable     *decisionengine.UpgradableSpecification
	qualityProfile *qualityprofiles.Service
	episodes       *episodes.Service
	episodeFiles   *episodefiles.Service
	disk           *disk.Provider
	configCfg      decisionengine.PropersAndRepacksConfigReader // *config.Provider
	freeSpaceCfg   decisionengine.FreeSpaceConfigReader         // *config.Provider
	retentionCfg   decisionengine.RetentionConfigReader         // *config.Provider
	maxSizeCfg     decisionengine.MaximumSizeReader             // *config.Provider
	minAgeCfg      decisionengine.MinimumAgeReader              // *config.Provider
	cdhCfg         decisionengine.AlreadyImportedCDHReader      // *config.Provider
	historyByEp    decisionengine.HistoryByEpisodeFinder        // *history.Service
	delayProfiles  *delayprofiles.Service
	releaseProfile *releaseprofiles.Service
	matcher        *releaseprofiles.MatcherService
	blocklistRepo  *blocklist.Repo
	log            *slog.Logger
}

// buildDecisionEngineSpecs constructs every ported decisionengine
// specification against the production deps and returns them in a
// single slice ready for [decisionengine.NewDecisionMaker]. Each
// constructor's nil-required-dep guard surfaces as a typed error here;
// the caller wraps the failure with the boot-site's `fmt.Errorf("decisionengine specs: %w", err)`.
//
// Spec coverage as of slice 3.AL: 20 of ~25 specs (the 5 unported
// gate on subsystems that don't exist yet — UpgradeSpec /
// UpgradeDiskSpec / CustomFormatAllowedByProfile on the
// CustomFormatCalculator-by-EpisodeFile port; QueueSpec on
// queue.Service; SceneMappingSpec on scenemappings.Service signature;
// Phase 4 specs — BlockedIndexer / TorrentSeeding — on provider
// integrations).
func buildDecisionEngineSpecs(d decisionEngineDeps) ([]decisionengine.Specification, error) {
	profileLookup := profileViewLookupAdapter{svc: d.qualityProfile}
	episodeFinder := firstEpisodeOfSeasonAdapter{svc: d.episodes}
	delayResolver := delayProfileResolverAdapter{svc: d.delayProfiles}
	blocklistChecker := decisionengine.NewRepoBlocklistChecker(d.blocklistRepo)

	specs := make([]decisionengine.Specification, 0, 20)

	acceptableSize, err := decisionengine.NewAcceptableSizeSpecification(profileLookup, episodeFinder, d.log)
	if err != nil {
		return nil, err
	}
	specs = append(specs, acceptableSize)

	alreadyImported, err := decisionengine.NewAlreadyImportedSpecification(d.cdhCfg, d.historyByEp, d.log)
	if err != nil {
		return nil, err
	}
	specs = append(specs, alreadyImported)

	airDate, err := decisionengine.NewAirDateSpecification(d.releaseProfile, d.log)
	if err != nil {
		return nil, err
	}
	specs = append(specs, airDate)

	animeVersion, err := decisionengine.NewAnimeVersionUpgradeSpecification(d.upgradable, d.configCfg, d.episodeFiles, d.log)
	if err != nil {
		return nil, err
	}
	specs = append(specs, animeVersion)

	blocklistSpec, err := decisionengine.NewBlocklistSpecification(blocklistChecker, d.log)
	if err != nil {
		return nil, err
	}
	specs = append(specs, blocklistSpec)

	freeSpace, err := decisionengine.NewFreeSpaceSpecification(d.freeSpaceCfg, d.disk, d.log)
	if err != nil {
		return nil, err
	}
	specs = append(specs, freeSpace)

	specs = append(specs, decisionengine.NewFullSeasonSpecification(nil, d.log))

	maxSize, err := decisionengine.NewMaximumSizeSpecification(d.maxSizeCfg, d.log)
	if err != nil {
		return nil, err
	}
	specs = append(specs, maxSize)

	minAge, err := decisionengine.NewMinimumAgeSpecification(d.minAgeCfg, d.log)
	if err != nil {
		return nil, err
	}
	specs = append(specs, minAge)

	specs = append(specs, decisionengine.NewMultiSeasonSpecification(d.log))
	specs = append(specs, decisionengine.NewNotSampleSpecification(d.log))

	protocol, err := decisionengine.NewProtocolSpecification(delayResolver, d.log)
	if err != nil {
		return nil, err
	}
	specs = append(specs, protocol)

	qualityAllowed, err := decisionengine.NewQualityAllowedByProfileSpecification(profileLookup, d.log)
	if err != nil {
		return nil, err
	}
	specs = append(specs, qualityAllowed)

	specs = append(specs, decisionengine.NewRawDiskSpecification(d.log))

	releaseRestrictions, err := decisionengine.NewReleaseRestrictionsSpecification(d.releaseProfile, d.matcher, d.log)
	if err != nil {
		return nil, err
	}
	specs = append(specs, releaseRestrictions)

	repack, err := decisionengine.NewRepackSpecification(d.upgradable, d.configCfg, d.episodeFiles, d.log)
	if err != nil {
		return nil, err
	}
	specs = append(specs, repack)

	retention, err := decisionengine.NewRetentionSpecification(d.retentionCfg, d.log)
	if err != nil {
		return nil, err
	}
	specs = append(specs, retention)

	sameEpisodes, err := decisionengine.NewSameEpisodesGrabSpecification(d.episodes, d.log)
	if err != nil {
		return nil, err
	}
	specs = append(specs, sameEpisodes)

	specs = append(specs, decisionengine.NewSeasonPackOnlySpecification(nil, d.log))
	specs = append(specs, decisionengine.NewSplitEpisodeSpecification(d.log))

	return specs, nil
}
