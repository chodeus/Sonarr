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
	"fmt"
	"log/slog"

	"sonarr.tv/sonarr/internal/blocklist"
	"sonarr.tv/sonarr/internal/config"
	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/delayprofiles"
	"sonarr.tv/sonarr/internal/disk"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/providerstatus"
	"sonarr.tv/sonarr/internal/qualityprofiles"
	"sonarr.tv/sonarr/internal/queue"
	"sonarr.tv/sonarr/internal/releaseprofiles"
	"sonarr.tv/sonarr/internal/series"
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

// customFormatForFileAdapter wraps `*customformats.Service.All` +
// the package-level `customformats.ParseCustomFormatForFile` free fn
// (slice 3.AP) to satisfy [decisionengine.UpgradeAllowedFormatCalculator]
// — the by-EpisodeFile CustomFormat dispatch the
// [UpgradeAllowedSpecification] (and future UpgradeDiskSpecification)
// needs. ParseCustomFormatForFile takes the full `[]*Format` catalogue
// as its third arg; the adapter fetches it on every call so a profile
// edit lands without a restart. The wrapped fn returns no error itself
// — the only failure mode is the catalogue fetch.
type customFormatForFileAdapter struct {
	svc *customformats.Service
}

func (a customFormatForFileAdapter) ParseCustomFormat(ctx context.Context, file *episodefiles.File, ser *series.Series) ([]customformats.CustomFormat, error) {
	all, err := a.svc.All(ctx)
	if err != nil {
		return nil, err
	}
	return customformats.ParseCustomFormatForFile(file, ser, all), nil
}

// seasonPackConfigAdapter wraps `*config.Provider` to satisfy
// [decisionengine.UpgradeDiskSeasonReader]. Translates the raw `string`
// SeasonPackUpgrade getter via [decisionengine.ParseSeasonPackUpgradeType]
// so the config package stays free of the decisionengine import. The
// double-tier threshold getter passes through unchanged.
type seasonPackConfigAdapter struct {
	cfg *config.Provider
}

func (a seasonPackConfigAdapter) SeasonPackUpgrade() decisionengine.SeasonPackUpgradeType {
	return decisionengine.ParseSeasonPackUpgradeType(a.cfg.SeasonPackUpgrade())
}

func (a seasonPackConfigAdapter) SeasonPackUpgradeThreshold() float64 {
	return a.cfg.SeasonPackUpgradeThreshold()
}

// queueReaderAdapter wraps `*queue.Service` to satisfy
// [decisionengine.QueueReader] — projects each queue.Queue row onto
// the narrow [decisionengine.QueueItem] shape on every spec
// evaluation. Stays out of the queue→decisionengine import edge
// (queue already imports decisionengine for the Subject type) by
// keeping QueueItem inside decisionengine; the projection happens
// here at the boot-site wire layer.
type queueReaderAdapter struct {
	svc *queue.Service
}

func (a queueReaderAdapter) GetQueue() []decisionengine.QueueItem {
	rows := a.svc.GetQueue()
	out := make([]decisionengine.QueueItem, 0, len(rows))
	for i := range rows {
		var statePtr *int
		if rows[i].TrackedDownloadState != nil {
			s := int(*rows[i].TrackedDownloadState)
			statePtr = &s
		}
		out = append(out, decisionengine.QueueItem{
			RemoteEpisode:        rows[i].RemoteEpisode,
			Size:                 rows[i].Size,
			TrackedDownloadState: statePtr,
		})
	}
	return out
}

// Compile-time guards: every adapter satisfies the corresponding
// per-spec narrow interface in `internal/decisionengine`. A signature
// drift on either side breaks the build at this site.
var (
	_ decisionengine.AcceptableSizeProfileReader          = profileViewLookupAdapter{}
	_ decisionengine.QualityAllowedByProfileProfileReader = profileViewLookupAdapter{}
	_ decisionengine.UpgradeAllowedProfileReader          = profileViewLookupAdapter{}
	_ decisionengine.UpgradeDiskProfileReader             = profileViewLookupAdapter{}
	_ decisionengine.QueueProfileReader                   = profileViewLookupAdapter{}
	_ decisionengine.AcceptableSizeEpisodeFinder          = firstEpisodeOfSeasonAdapter{}
	_ decisionengine.DelayProfileResolver                 = delayProfileResolverAdapter{}
	_ decisionengine.UpgradeAllowedFormatCalculator       = customFormatForFileAdapter{}
	_ decisionengine.UpgradeDiskFormatCalculator          = customFormatForFileAdapter{}
	_ decisionengine.UpgradeDiskSeasonReader              = seasonPackConfigAdapter{}
	_ decisionengine.QueueReader                          = queueReaderAdapter{}
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
	customFormats  *customformats.Service
	configProvider *config.Provider
	queueSvc       *queue.Service
	blockedView    providerstatus.BlockedView          // *indexerstatus.Service (slice 4.P queue [8])
	minSeeders     decisionengine.MinimumSeedersReader // minimumSeedersAdapter (slice 4.P queue [8])
	indexerTags    decisionengine.IndexerTagFinder     // indexerTagFinderAdapter (J468)
	log            *slog.Logger
}

// indexerTagFinderAdapter implements decisionengine.IndexerTagFinder —
// resolves an indexer's tag set by id for the IndexerTagSpecification
// (J468). Mirrors C# `IIndexerFactory.Get(id).Tags`
// (IndexerTagSpecification.cs:35). A missing indexer maps to found=false
// (the C# ModelNotFoundException → skip path).
type indexerTagFinderAdapter struct {
	repo *indexers.Repo
}

func (a indexerTagFinderAdapter) IndexerTags(ctx context.Context, indexerID int) ([]int, bool, error) {
	row, err := a.repo.Get(ctx, indexerID)
	if err != nil {
		if errors.Is(err, indexers.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("indexers.Get(%d): %w", indexerID, err)
	}
	return row.Tags, true, nil
}

// buildDecisionEngineSpecs constructs every ported decisionengine
// specification against the production deps and returns them in a
// single slice ready for [decisionengine.NewDecisionMaker]. Each
// constructor's nil-required-dep guard surfaces as a typed error here;
// the caller wraps the failure with the boot-site's `fmt.Errorf("decisionengine specs: %w", err)`.
//
// Spec coverage as of slice 4.P: 25 of ~25 specs. Slice 3.BF added
// QueueSpec; slice 4.P (queue [8]) adds BlockedIndexer +
// TorrentSeeding. The remaining gap is SceneMappingSpec, which
// gates on a scenemappings.Service signature shape that's
// orthogonal to this loop.
func buildDecisionEngineSpecs(d decisionEngineDeps) ([]decisionengine.Specification, error) {
	profileLookup := profileViewLookupAdapter{svc: d.qualityProfile}
	episodeFinder := firstEpisodeOfSeasonAdapter{svc: d.episodes}
	delayResolver := delayProfileResolverAdapter{svc: d.delayProfiles}
	blocklistChecker := decisionengine.NewRepoBlocklistChecker(d.blocklistRepo)
	cfForFile := customFormatForFileAdapter{svc: d.customFormats}
	seasonPack := seasonPackConfigAdapter{cfg: d.configProvider}

	specs := make([]decisionengine.Specification, 0, 25)

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

	customFormatAllowed, err := decisionengine.NewCustomFormatAllowedByProfileSpecification(profileLookup, d.log)
	if err != nil {
		return nil, err
	}
	specs = append(specs, customFormatAllowed)

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

	// J466: MonitoredEpisodeSpecification — rejects releases for an
	// unmonitored series, or where one+ matched episode is unmonitored
	// (the core "only download what I monitor" RSS-sync gate). Skips the
	// check only on a search with MonitoredEpisodesOnly=false. Ports
	// DecisionEngine/Specifications/RssSync/MonitoredEpisodeSpecification.cs.
	// J468: IndexerTagSpecification — rejects releases from an indexer
	// whose tags don't intersect the series' tags (a tag-scoped indexer
	// shouldn't feed every series). Ports
	// DecisionEngine/Specifications/RssSync/IndexerTagSpecification.cs.
	indexerTag, err := decisionengine.NewIndexerTagSpecification(d.indexerTags, d.log)
	if err != nil {
		return nil, err
	}
	specs = append(specs, indexerTag)
	specs = append(specs, decisionengine.NewMonitoredEpisodeSpecification(d.log))
	// J467: EpisodeRequestedSpecification — search-only gate rejecting
	// releases whose matched episodes don't intersect the searched
	// episode set (indexers return junk for a query). No-op on RSS (nil
	// SearchCriteria). Ports
	// DecisionEngine/Specifications/Search/EpisodeRequestedSpecification.cs.
	specs = append(specs, decisionengine.NewEpisodeRequestedSpecification(d.log))
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

	upgradeAllowed, err := decisionengine.NewUpgradeAllowedSpecification(d.upgradable, profileLookup, d.episodeFiles, cfForFile, d.log)
	if err != nil {
		return nil, err
	}
	specs = append(specs, upgradeAllowed)

	upgradeDisk, err := decisionengine.NewUpgradeDiskSpecification(d.upgradable, profileLookup, d.episodeFiles, cfForFile, seasonPack, d.log)
	if err != nil {
		return nil, err
	}
	specs = append(specs, upgradeDisk)

	// Slice 3.BF: QueueSpec wired via queueReaderAdapter projection
	// over *queue.Service. UpgradableSpecification satisfies the
	// QueueUpgradableChecker shape via Go's structural typing.
	queueSpec, err := decisionengine.NewQueueSpecification(
		queueReaderAdapter{svc: d.queueSvc},
		profileLookup,
		d.upgradable,
		d.configCfg,
		d.log,
	)
	if err != nil {
		return nil, err
	}
	specs = append(specs, queueSpec)

	// Slice 4.P (queue [8]): BlockedIndexerSpecification +
	// TorrentSeedingSpecification.
	blockedIndexer, err := decisionengine.NewBlockedIndexerSpecification(d.blockedView, d.log)
	if err != nil {
		return nil, err
	}
	specs = append(specs, blockedIndexer)

	torrentSeeding, err := decisionengine.NewTorrentSeedingSpecification(d.minSeeders, d.log)
	if err != nil {
		return nil, err
	}
	specs = append(specs, torrentSeeding)

	return specs, nil
}
