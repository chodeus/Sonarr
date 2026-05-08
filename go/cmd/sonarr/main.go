// Command sonarr is the Sonarr daemon entry point.
//
// During the C#-to-Go port this binary is a thin shell that grows
// constructor wiring as subsystems land. At Phase 1 sign-off it
// satisfies the strategic plan's bar: "a Go binary that opens an
// existing user DB and reports the same schema version" — see
// ~/.claude/plans/curried-discovering-meadow.md § Phase 1.
//
// What this binary currently does:
//
//  1. Parse startup args (`/data=PATH`, `/nobrowser`, etc.).
//  2. Detect the AppFolder (data dir, startup dir, temp dir).
//  3. Load <data>/config.xml or treat as empty when missing.
//  4. Construct a messaging Bus + lifecycle wiring.
//  5. Open the SQLite main database at <data>/<InstanceName>.db.
//  6. Run migrations to head (Sonarr always auto-migrates on
//     startup; the C# tree does this in NzbDroneStartupArguments).
//  7. Report the schema version through slog.
//  8. Publish ApplicationStartedEvent and block on ctx.Done().
//  9. On signal, publish ApplicationShutdownRequested, drain the
//     bus, close the DB.
//
// Postgres support: when config.xml carries PostgresHost +
// PostgresMainDb (matching the C# detection), the binary opens
// Postgres instead of SQLite. Phase 1 ships only the wiring; the
// configfile package already exposes the typed accessors.
//
// See ../../CONVERSION.md for the rules every change must follow.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"sonarr.tv/sonarr/internal/appenv"
	"sonarr.tv/sonarr/internal/auth"
	"sonarr.tv/sonarr/internal/autotagging"
	"sonarr.tv/sonarr/internal/buildinfo"
	"sonarr.tv/sonarr/internal/commands"
	"sonarr.tv/sonarr/internal/commands/handlers"
	"sonarr.tv/sonarr/internal/config"
	"sonarr.tv/sonarr/internal/configfile"
	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/disk"
	"sonarr.tv/sonarr/internal/downloadedimport"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/housekeeping"
	"sonarr.tv/sonarr/internal/instrumentation"
	"sonarr.tv/sonarr/internal/lifecycle"
	"sonarr.tv/sonarr/internal/mediafiles"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/parsingservice"
	"sonarr.tv/sonarr/internal/qualityprofiles"
	"sonarr.tv/sonarr/internal/scheduledtasks"
	"sonarr.tv/sonarr/internal/series"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "sonarr: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Special-case --version BEFORE structured logging — keep the
	// machine-readable line clean for build tooling.
	for _, a := range os.Args[1:] {
		if a == "--version" || a == "-version" || a == "/version" {
			fmt.Println(buildinfo.String())
			return nil
		}
	}

	logger := instrumentation.NewLogger(os.Stderr, slog.LevelInfo)
	slog.SetDefault(logger)
	logger.Info("sonarr starting", "version", buildinfo.Version, "commit", buildinfo.Commit)
	defer logger.Info("sonarr stopped")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := serve(ctx, logger); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// serve is the top-level wiring point. As phases land, additional
// subsystems (HTTP server, SignalR hub, job scheduler, providers)
// are constructed here.
func serve(ctx context.Context, logger *slog.Logger) error {
	startup := appenv.ParseStartupArgs(os.Args[1:])

	folder, err := appenv.DetectAppFolder(startup)
	if err != nil {
		return fmt.Errorf("appenv: %w", err)
	}
	logger.Info("appfolder",
		"data", folder.AppData,
		"startup", folder.Startup,
		"config", folder.ConfigPath())

	if err := os.MkdirAll(folder.AppData, 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	loader := configfile.NewLoader(folder.ConfigPath())
	if err := loader.Load(ctx); err != nil {
		return fmt.Errorf("config load: %w", err)
	}
	bus := messaging.New(logger)
	cfg := configfile.NewProvider(loader, configfile.Overrides{}, bus, auth.GenerateAPIKey)

	instanceName, err := cfg.InstanceName(ctx)
	if err != nil {
		return fmt.Errorf("config InstanceName: %w", err)
	}

	dbCfg, source, err := buildDatastoreConfig(ctx, cfg, folder, instanceName)
	if err != nil {
		return fmt.Errorf("datastore config: %w", err)
	}
	db, err := datastore.Open(ctx, dbCfg)
	if err != nil {
		return fmt.Errorf("datastore open (%s): %w", source, err)
	}
	defer func() { _ = db.Close() }()

	if err := datastore.Migrate(ctx, db); err != nil {
		return fmt.Errorf("datastore migrate: %w", err)
	}
	version, err := datastore.CurrentVersion(ctx, db)
	if err != nil {
		return fmt.Errorf("datastore version: %w", err)
	}
	logger.Info("datastore ready",
		"engine", string(db.Engine),
		"role", string(db.Role),
		"source", source,
		"schemaVersion", version,
		"instance", instanceName)

	// Commands + scheduled-tasks wiring (Phase 3.13). Build the
	// service stack before the ApplicationStartedEvent fan-out so
	// boot recovery (orphan detection + queued re-push) runs against
	// a fresh queue. The scheduler subscribes via HandleCommandExecuted
	// so a Tick → Push → Dispatch → Complete round-trip flows back into
	// the LastExecution stamp.
	cmdRepo, err := commands.NewRepo(db)
	if err != nil {
		return fmt.Errorf("commands repo: %w", err)
	}
	cmdSvc, err := commands.NewService(cmdRepo, bus)
	if err != nil {
		return fmt.Errorf("commands service: %w", err)
	}
	// Housekeeping service — pre-built so the Housekeeping handler
	// can register against it. Each Task gets its own constructor so
	// nil-deps surface at boot, not at first run. New housekeepers
	// append to the [task] slice below.
	fixFuture, err := housekeeping.NewFixFutureRunScheduledTasks(db, logger)
	if err != nil {
		return fmt.Errorf("housekeeping fixFuture: %w", err)
	}
	orphanEpisodes, err := housekeeping.NewCleanupOrphanedEpisodes(db, logger)
	if err != nil {
		return fmt.Errorf("housekeeping orphanEpisodes: %w", err)
	}
	orphanFiles, err := housekeeping.NewCleanupOrphanedEpisodeFiles(db, logger)
	if err != nil {
		return fmt.Errorf("housekeeping orphanFiles: %w", err)
	}
	orphanHistory, err := housekeeping.NewCleanupOrphanedHistoryItems(db, logger)
	if err != nil {
		return fmt.Errorf("housekeeping orphanHistory: %w", err)
	}
	orphanBlocklist, err := housekeeping.NewCleanupOrphanedBlocklist(db, logger)
	if err != nil {
		return fmt.Errorf("housekeeping orphanBlocklist: %w", err)
	}
	orphanIndexerStatus, err := housekeeping.NewCleanupOrphanedIndexerStatus(db, logger)
	if err != nil {
		return fmt.Errorf("housekeeping orphanIndexerStatus: %w", err)
	}
	orphanDownloadClientStatus, err := housekeeping.NewCleanupOrphanedDownloadClientStatus(db, logger)
	if err != nil {
		return fmt.Errorf("housekeeping orphanDownloadClientStatus: %w", err)
	}
	orphanImportListStatus, err := housekeeping.NewCleanupOrphanedImportListStatus(db, logger)
	if err != nil {
		return fmt.Errorf("housekeeping orphanImportListStatus: %w", err)
	}
	orphanNotificationStatus, err := housekeeping.NewCleanupOrphanedNotificationStatus(db, logger)
	if err != nil {
		return fmt.Errorf("housekeeping orphanNotificationStatus: %w", err)
	}
	orphanPending, err := housekeeping.NewCleanupOrphanedPendingReleases(db, logger)
	if err != nil {
		return fmt.Errorf("housekeeping orphanPending: %w", err)
	}
	orphanExtra, err := housekeeping.NewCleanupOrphanedExtraFiles(db, logger)
	if err != nil {
		return fmt.Errorf("housekeeping orphanExtra: %w", err)
	}
	orphanSubtitle, err := housekeeping.NewCleanupOrphanedSubtitleFiles(db, logger)
	if err != nil {
		return fmt.Errorf("housekeeping orphanSubtitle: %w", err)
	}
	orphanMetadata, err := housekeeping.NewCleanupOrphanedMetadataFiles(db, logger)
	if err != nil {
		return fmt.Errorf("housekeeping orphanMetadata: %w", err)
	}
	addlNaming, err := housekeeping.NewCleanupAdditionalNamingSpecs(db, logger)
	if err != nil {
		return fmt.Errorf("housekeeping addlNaming: %w", err)
	}
	addlUsers, err := housekeeping.NewCleanupAdditionalUsers(db, logger)
	if err != nil {
		return fmt.Errorf("housekeeping addlUsers: %w", err)
	}
	seriesRepo, err := series.NewRepo(db)
	if err != nil {
		return fmt.Errorf("housekeeping series repo: %w", err)
	}
	updateClean, err := housekeeping.NewUpdateCleanTitleForSeries(seriesRepo, logger)
	if err != nil {
		return fmt.Errorf("housekeeping updateClean: %w", err)
	}
	fixIndexerStatus, err := housekeeping.NewFixFutureIndexerStatusTimes(db, logger)
	if err != nil {
		return fmt.Errorf("housekeeping fixIndexerStatus: %w", err)
	}
	fixDownloadClientStatus, err := housekeeping.NewFixFutureDownloadClientStatusTimes(db, logger)
	if err != nil {
		return fmt.Errorf("housekeeping fixDownloadClientStatus: %w", err)
	}
	fixImportListStatus, err := housekeeping.NewFixFutureImportListStatusTimes(db, logger)
	if err != nil {
		return fmt.Errorf("housekeeping fixImportListStatus: %w", err)
	}
	fixNotificationStatus, err := housekeeping.NewFixFutureNotificationStatusTimes(db, logger)
	if err != nil {
		return fmt.Errorf("housekeeping fixNotificationStatus: %w", err)
	}
	autoTagRepo, err := autotagging.NewRepo(db)
	if err != nil {
		return fmt.Errorf("housekeeping autotagging repo: %w", err)
	}
	cleanupTags, err := housekeeping.NewCleanupUnusedTags(db, autoTagRepo, logger)
	if err != nil {
		return fmt.Errorf("housekeeping cleanupTags: %w", err)
	}
	qpRepo, err := qualityprofiles.NewRepo(db)
	if err != nil {
		return fmt.Errorf("housekeeping qualityprofiles repo: %w", err)
	}
	cfRepo, err := customformats.NewRepo(db)
	if err != nil {
		return fmt.Errorf("housekeeping customformats repo: %w", err)
	}
	cleanupQPFormat, err := housekeeping.NewCleanupQualityProfileFormatItems(qpRepo, cfRepo, logger)
	if err != nil {
		return fmt.Errorf("housekeeping cleanupQPFormat: %w", err)
	}
	houseSvc, err := housekeeping.NewService([]housekeeping.Task{
		fixFuture,
		orphanEpisodes,
		orphanFiles,
		orphanHistory,
		orphanBlocklist,
		orphanIndexerStatus,
		orphanDownloadClientStatus,
		orphanImportListStatus,
		orphanNotificationStatus,
		orphanPending,
		orphanExtra,
		orphanSubtitle,
		orphanMetadata,
		addlNaming,
		addlUsers,
		updateClean,
		fixIndexerStatus,
		fixDownloadClientStatus,
		fixImportListStatus,
		fixNotificationStatus,
		cleanupTags,
		cleanupQPFormat,
	}, logger)
	if err != nil {
		return fmt.Errorf("housekeeping service: %w", err)
	}

	// Health-check service. Phase 3.29 closes the 3.24 stub by wiring
	// the real orchestrator with two concrete checks (AppDataLocation
	// + ApiKeyValidation). Additional checks land alongside their
	// dependent subsystems in subsequent slices.
	apiKeyCheck, err := healthcheck.NewApiKeyValidationCheck(cfg)
	if err != nil {
		return fmt.Errorf("healthcheck apiKey: %w", err)
	}
	healthSvc, err := healthcheck.NewService([]healthcheck.Check{
		healthcheck.NewAppDataLocationCheck(folder),
		apiKeyCheck,
	}, bus, logger)
	if err != nil {
		return fmt.Errorf("healthcheck service: %w", err)
	}
	healthSvc.Subscribe(bus)

	// mediafiles.Service wiring (Phase 3.52). The Service is
	// constructed with concrete impls for series / episodefiles /
	// episodefiles cleanup; the not-yet-ported subsystems (Config
	// table, IDiskProvider, RootFolderService, EpisodeImport,
	// MediaInfo updater) ride on stub impls in mediafiles_stubs.go
	// so the dispatcher surface compiles end-to-end. Each stub is
	// drop-in replaceable as its real port lands.
	seriesSvc, err := series.NewService(seriesRepo, bus)
	if err != nil {
		return fmt.Errorf("series service: %w", err)
	}
	episodesRepo, err := episodes.NewRepo(db)
	if err != nil {
		return fmt.Errorf("episodes repo: %w", err)
	}
	episodesSvc, err := episodes.NewService(episodesRepo, bus)
	if err != nil {
		return fmt.Errorf("episodes service: %w", err)
	}
	episodeFilesRepo, err := episodefiles.NewRepo(db)
	if err != nil {
		return fmt.Errorf("episodefiles repo: %w", err)
	}
	episodeFilesSvc, err := episodefiles.NewService(episodeFilesRepo, bus)
	if err != nil {
		return fmt.Errorf("episodefiles service: %w", err)
	}
	mediaFileCleanupSvc, err := episodefiles.NewCleanupService(episodeFilesSvc, episodesSvc, logger)
	if err != nil {
		return fmt.Errorf("episodefiles cleanup service: %w", err)
	}
	// Config table provider (Phase 3.53). First mediafiles boot stub
	// to drop — the typed Provider lazily caches every Config row and
	// invalidates on ConfigSavedEvent so getters stay fresh as the
	// rest of the IConfigService surface ports.
	configRepo, err := config.NewRepo(db)
	if err != nil {
		return fmt.Errorf("config repo: %w", err)
	}
	configProv, err := config.NewProvider(configRepo, bus)
	if err != nil {
		return fmt.Errorf("config provider: %w", err)
	}
	if err := configProv.Load(ctx); err != nil {
		return fmt.Errorf("config provider load: %w", err)
	}
	configProv.Subscribe(bus)
	// Disk provider (Phase 3.54). Concrete os/filepath-backed impl of
	// the 8-method mediafiles.DiskProvider surface; SetPermissions is
	// platform-aware via build tags (chmod+chown on Linux, no-op on
	// other GOOS). Drops the second mediafiles boot stub.
	diskProv := disk.NewProvider(logger)
	mediafilesSvc, err := mediafiles.NewService(
		diskProv,
		configProv,
		stubRootFolderResolver{},
		seriesSvc,
		episodeFilesSvc,
		mediaFileCleanupSvc,
		stubImportDecisionMaker{},
		stubImportApprovedEpisodes{},
		stubUpdateMediaInfo{},
		bus,
		logger,
	)
	if err != nil {
		return fmt.Errorf("mediafiles service: %w", err)
	}

	// parsingservice.Service wiring (Phase 3.82). Slice 3.81 closed
	// the [Service.GetSeriesFromTitle] gap that the
	// downloadedimport.SeriesParser narrow interface reaches for; this
	// is the first production binding of the parsing service, wired
	// minimally — episodes finder is the real episodes.Service, scene
	// mappings + series finder + parsed-info builder are left as their
	// noop/default fallbacks since the EpisodeImport orchestrator
	// hasn't lifted yet. The series finder upgrade to the real
	// *series.Service is included so GetSeriesFromTitle resolves
	// against the live catalog.
	parsingSvc, err := parsingservice.NewService(episodesSvc, nil, nil, seriesSvc, logger)
	if err != nil {
		return fmt.Errorf("parsingservice: %w", err)
	}

	// downloadedimport.Service wiring (Phase 3.82) — the production
	// caller that satisfies the slice-3.80 [handlers.Deps.DownloadedEpisodesScan]
	// processor slot. Composes:
	//
	//   - DiskOps          ← *disk.Provider via [downloadedImportDiskOps]
	//                        (no-op IsFileLocked until disk-pkg port).
	//   - DiskScanner      ← [downloadedImportDiskScanner] over
	//                        mediafiles.GetVideoFiles + FilterPaths.
	//   - SeriesPathChecker ← [downloadedImportSeriesPaths] over
	//                        *series.Service (ctx-adapter).
	//   - SeriesParser     ← [downloadedImportSeriesParser] over
	//                        *parsingservice.Service (ctx-adapter).
	//   - ImportDecider    ← stubImportDecider (returns nil decisions
	//                        until episodeimport.ImportDecisionMaker
	//                        is wired at boot).
	//   - ImportRunner     ← stubImportRunner (no-op import until
	//                        episodeimport.ImportApprovedEpisodes is
	//                        wired at boot).
	//   - SampleDetector   ← stubSampleDetector (always NotSample
	//                        until episodeimport.SampleDetector is
	//                        wired at boot — needs a real
	//                        VideoRuntimeReader).
	//   - ConfigReader     ← *config.Provider (UserRejectedExtensions
	//                        added in slice 3.82).
	//
	// API command path becomes reachable end-to-end:
	// POST /api/v3/command {"name":"DownloadedEpisodesScan"} →
	// handlers.DownloadedEpisodesScan → downloadedimport.Service.ProcessPath.
	// Decision-making + import-running still no-op; the path/folder
	// rejection ladder (DangerousFile / ExecutableFile / ArchiveFile /
	// UserRejectedExtensions / video-extension filtering / multi-season
	// gate) IS exercised against real disk + parser state.
	// Sample-detector wiring (Phase 3.84). Constructs the real
	// *episodeimport.SampleDetector with a noopVideoRuntimeReader
	// stand-in until an ffprobe-backed reader ports — the
	// extension/special short-circuit arms still fire correctly
	// (.flv / .strm / specials → NotSample), and the runtime-bucket
	// arm conservatively returns Indeterminate (which the
	// downloadedimport ShouldDeleteFolder gate at C# `:131` treats as
	// "don't sample-reject"). The downloadedImportSampleDetector
	// adapter bridges to the slice-3.84 IsSampleForSeries 3-arg
	// overload; replacing the noopVideoRuntimeReader with a real
	// binding is the only edit needed when ffprobe lands.
	sampleDetector := episodeimport.NewSampleDetector(noopVideoRuntimeReader{}, logger)

	// AggregationService + ImportDecisionMaker wiring (Phase 3.85).
	// Replaces the slice-3.82 stubImportDecider at the cmd/sonarr
	// boot site. AggregationService runs with an empty aggregator
	// list — the per-augmenter constructor chain (Languages /
	// Quality / Episodes / ReleaseInfo / SubtitleInfo) ports in
	// later slices. Augment still fills LocalEpisode.Size + SceneName
	// even with an empty aggregator list, which is enough for the
	// orchestrator's per-file Specification pass.
	//
	// ImportDecisionMaker constructor takes specs as the first arg —
	// nil/empty is explicitly allowed (every file is approved by
	// default, matching C# IoC behaviour with zero spec bindings).
	// Per-spec wiring lands as each Specification's dep chain (config
	// readers, scene-mapping finders, etc.) is constructed at boot.
	// Per-augmenter wiring (Phase 3.88). Two of five LocalEpisodeAggregator
	// implementations populated — both are zero-dep at the boot site:
	//
	//   - AggregateLanguage with an empty []LanguageAugmenter establishes
	//     the baseline pick: [Series.OriginalLanguage] resolved via the
	//     constructor's default closure over [language.FindByID]. Real
	//     augmenters (AugmentLanguageFromMediaInfo, etc.) progressively
	//     refine in later slices once their narrow-interface deps land.
	//   - AggregateQuality with an empty []QualityAugmenter establishes
	//     the baseline source/resolution merge against zero augmenter
	//     verdicts — Quality stays at its zero-value default until
	//     real augmenters (AugmentQualityFromMediaInfo / AugmentQualityFromReleaseName)
	//     wire in.
	//
	// Skipped here:
	//   - AggregateEpisodes — needs an EpisodeParsingService adapter
	//     over *parsingservice.Service (sig mismatch on
	//     ParseSpecialEpisodeTitle / GetEpisodes — both bear ctx + error
	//     in the Go port; future slice writes the adapter).
	//   - AggregateReleaseInfo — needs a HistoryGrabsFinder; no Go
	//     port of the history grabs surface yet.
	//   - AggregateSubtitleInfo — needs an EpisodeFileResolver adapter
	//     over *episodefiles.Service (GetFile vs GetByID method-name
	//     mismatch; trivial adapter).
	episodeAggregators := []episodeimport.LocalEpisodeAggregator{
		episodeimport.NewAggregateLanguage(nil, nil, logger),
		episodeimport.NewAggregateQuality(nil, logger),
		// Slice 3.89: AggregateSubtitleInfo wired via the
		// [episodeFileResolverAdapter] thin GetByID→GetFile bridge over
		// the existing *episodefiles.Service. Aggregator mirrors C#
		// `AggregateSubtitleInfo.cs:8-50`: when LocalEpisode.ExistingFile
		// is true (rescan path), fetches the persisted EpisodeFile by ID
		// and merges its SubtitleFile paths into the in-memory
		// LocalEpisode.SubtitleInfo so the orchestrator's downstream
		// rename / extras steps don't lose subtitle metadata. New-import
		// path (ExistingFile=false) skips the lookup entirely.
		episodeimport.NewAggregateSubtitleInfo(episodeFileResolverAdapter{svc: episodeFilesSvc}, logger),
	}
	aggregationSvc, err := episodeimport.NewAggregationService(episodeAggregators, diskProv, nil, nil, logger)
	if err != nil {
		return fmt.Errorf("aggregation service: %w", err)
	}
	// Per-spec wiring (Phase 3.86). Five zero-dep specs (each takes
	// only a logger) populate the ImportDecisionMaker's specs slice.
	// Each Specification implements
	// `IsSatisfiedBy(le, dci) *ImportSpecDecision` and `Name() string`;
	// the orchestrator walks the list per file and collects rejections.
	//
	// Specs with heavier dep chains (SameEpisodes / AbsoluteEpisodeNumber
	// / EpisodeTitle / FreeSpace / NotUnpacking / Upgrade / NotSample)
	// port over subsequent slices as their narrow interfaces' real
	// production bindings come online.
	importDecisionSpecs := []episodeimport.Specification{
		episodeimport.NewFullSeasonSpecification(logger),
		episodeimport.NewSplitEpisodeSpecification(logger),
		episodeimport.NewUnverifiedSceneNumberingSpecification(logger),
		episodeimport.NewHasAudioTrackSpecification(logger),
		episodeimport.NewMatchesGrabSpecification(logger),
		// Slice 3.87: NotSampleSpecification — its SampleChecker dep
		// (`IsSample(ctx, *LocalEpisode) DetectSampleResult`) is
		// satisfied structurally by the slice-3.84 *SampleDetector.
		// Skips the check for ExistingFile=true (library-rescan flow)
		// and rejects Sample / SampleIndeterminate verdicts per C#
		// `NotSampleSpecification.cs:32-39`.
		//
		// Behaviour-with-noop-reader note: with the slice-3.84
		// noopVideoRuntimeReader still in place AND the slice-3.85
		// empty aggregator list (so LocalEpisode.MediaInfo also stays
		// nil), every non-special, non-.flv/.strm file the spec sees
		// surfaces as DetectSampleIndeterminate → ImportRejectionSampleIndeterminate.
		// Production users see "Unable to determine if file is a
		// sample" rejections per file until either an ffprobe-backed
		// VideoRuntimeReader is wired (slice TBD) OR the MediaInfo
		// aggregator is wired (also slice TBD) — whichever lands first
		// supplies the runtime data the spec needs to produce real
		// Sample/NotSample verdicts. The diagnostic surface is the
		// correct C#-faithful posture: ffprobe-not-available is a
		// loud-fail rather than a silent-pass.
		episodeimport.NewNotSampleSpecification(sampleDetector, logger),
	}
	importDecisionMaker, err := episodeimport.NewImportDecisionMaker(
		importDecisionSpecs, // specs — slice-3.86: 5 zero-dep specs wired
		episodeFilesSvc,     // filter — FilterExistingFiles
		aggregationSvc,      // aggregator — slice-3.60 AggregationService (empty aggregators above)
		diskProv,            // disk — GetFileSize
		sampleDetector,      // sampler — slice-3.55 + 3.84 SampleDetector
		nil,                 // pathParser — nil → defaults to parser.ParsePath
		nil,                 // tracked — no trackeddownloads.Service yet
		nil,                 // formats — no LocalEpisodeCustomFormatCalculator yet
		logger,
	)
	if err != nil {
		return fmt.Errorf("import decision maker: %w", err)
	}
	dimpDisk := downloadedImportDiskOps{Provider: diskProv}
	dimpSvc := downloadedimport.NewService(
		dimpDisk,
		downloadedImportDiskScanner{disk: diskProv, log: logger},
		downloadedImportSeriesPaths{svc: seriesSvc, log: logger},
		downloadedImportSeriesParser{svc: parsingSvc, log: logger},
		downloadedImportDecider{svc: importDecisionMaker, log: logger},
		stubImportRunner{},
		downloadedImportSampleDetector{svc: sampleDetector},
		configProv,
		logger,
	)
	if dimpSvc == nil {
		// NewService returns nil iff any required dep is nil. Every
		// dep here is non-nil at construction so this is a
		// belt-and-braces guard against an interface refactor that
		// adds a new required method without updating this site.
		return fmt.Errorf("downloadedimport service: nil — required dep regressed")
	}

	registry := commands.NewRegistry()
	queue := commands.NewQueue(registry.Properties)
	cmdSvc.SetQueue(queue)
	if err := handlers.RegisterAll(registry, handlers.Deps{
		Commands:     cmdSvc,
		Housekeeping: houseSvc,
		HealthCheck:  healthSvc,
		RescanSeries: mediafilesSvc,
		DownloadedEpisodesScan: handlers.DownloadedEpisodesScanDeps{
			// Processor REQUIRED — slice 3.80 contract: nil Processor
			// → handler not registered. We always wire the real
			// downloadedimport.Service here so the API surface is
			// always live; the upstream stubs are inside the service
			// itself, not at the handler boundary.
			Processor: dimpSvc,
			// Finder / Verifier / Reporter remain nil — those slots
			// gate on the trackeddownloads.Service / completed-download
			// lifecycle / ICommandResultReporter ports respectively.
			// Each is documented optional at slice-3.80's handler
			// definition; the handler tolerates nil for all three.
		},
		Log: logger,
	}); err != nil {
		return fmt.Errorf("commands handlers: %w", err)
	}
	dispatcher, err := commands.NewDispatcher(cmdSvc, queue, registry, logger)
	if err != nil {
		return fmt.Errorf("commands dispatcher: %w", err)
	}

	taskRepo, err := scheduledtasks.NewRepo(db)
	if err != nil {
		return fmt.Errorf("scheduledtasks repo: %w", err)
	}
	taskSvc, err := scheduledtasks.NewService(taskRepo)
	if err != nil {
		return fmt.Errorf("scheduledtasks service: %w", err)
	}
	if err := taskSvc.RegisterDefaults(ctx, handlers.DefaultScheduledTasks()); err != nil {
		return fmt.Errorf("scheduledtasks RegisterDefaults: %w", err)
	}
	messaging.Subscribe(bus, "scheduledtasks.HandleCommandExecuted",
		taskSvc.HandleCommandExecuted)

	scheduler, err := scheduledtasks.NewScheduler(taskSvc, cmdSvc, logger)
	if err != nil {
		return fmt.Errorf("scheduledtasks scheduler: %w", err)
	}

	// Boot recovery: orphan any leftover Started rows from the
	// previous (crashed) run and re-feed every Queued row into the
	// in-memory queue so the dispatcher can pick them up.
	orphaned, queuedAtBoot, err := cmdSvc.HandleApplicationStarted(ctx)
	if err != nil {
		return fmt.Errorf("commands boot recovery: %w", err)
	}
	if len(queuedAtBoot) > 0 {
		ptrs := make([]*commands.Command, len(queuedAtBoot))
		for i := range queuedAtBoot {
			row := queuedAtBoot[i]
			ptrs[i] = &row
		}
		queue.AddMany(ptrs)
	}
	logger.Info("commands boot recovery",
		"orphaned", orphaned,
		"requeued", len(queuedAtBoot))

	// Bring up the dispatcher + scheduler goroutines. Both exit
	// cleanly on ctx cancellation; we wait for both via wg below.
	workerCtx, workerCancel := context.WithCancel(ctx)
	defer workerCancel()
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		if err := dispatcher.Run(workerCtx); err != nil {
			logger.Warn("commands dispatcher exited with error", "err", err)
		}
	}()
	go func() {
		defer workers.Done()
		if err := scheduler.Run(workerCtx); err != nil {
			logger.Warn("scheduledtasks scheduler exited with error", "err", err)
		}
	}()

	// Bus is hot — fan out the start signal so subscribers (auth
	// service seed, future health checks, etc.) can do their lazy
	// init.
	if err := messaging.Publish(ctx, bus, lifecycle.ApplicationStartedEvent{}); err != nil {
		return fmt.Errorf("publish ApplicationStartedEvent: %w", err)
	}

	logger.Info("daemon ready", "version", buildinfo.Version,
		"hint", "HTTP server / SignalR arrive in Phase 5")
	<-ctx.Done()

	// Shutdown sequence — publish the signal BEFORE BeginShutdown
	// gates further lifecycle events; then drain the async pool.
	logger.Info("sonarr shutdown requested")
	if err := messaging.Publish(context.Background(), bus, lifecycle.ApplicationShutdownRequested{}); err != nil &&
		!errors.Is(err, messaging.ErrShutdown) {
		logger.Warn("publish ApplicationShutdownRequested failed", "err", err)
	}
	// Stop the dispatcher + scheduler before draining the bus so any
	// in-flight CommandExecutedEvent publishes flow through to the
	// (still-live) bus subscribers. Close() is defensive — Run already
	// exits on ctx cancel.
	workerCancel()
	queue.Close()
	workers.Wait()
	bus.BeginShutdown()
	bus.Wait()
	return ctx.Err()
}

// buildDatastoreConfig picks SQLite or Postgres based on the
// configfile's Postgres* keys and returns a human-readable source
// label for logging. The C# detection is "PostgresMainDb is
// non-empty"; we replicate that so existing user databases keep
// being detected the same way.
func buildDatastoreConfig(ctx context.Context, cfg *configfile.Provider, folder appenv.AppFolderInfo, instance string) (datastore.Config, string, error) {
	pgMain, err := cfg.String(ctx, "PostgresMainDb", "", false)
	if err != nil {
		return datastore.Config{}, "", err
	}
	if pgMain == "" {
		return datastore.Config{
			Engine: datastore.EngineSQLite,
			Role:   datastore.RoleMain,
			Path:   folder.MainDBPath(instance),
		}, "sqlite:" + folder.MainDBPath(instance), nil
	}
	host, err := cfg.String(ctx, "PostgresHost", "localhost", false)
	if err != nil {
		return datastore.Config{}, "", err
	}
	port, err := cfg.Int(ctx, "PostgresPort", 5432, false)
	if err != nil {
		return datastore.Config{}, "", err
	}
	user, err := cfg.String(ctx, "PostgresUser", "", false)
	if err != nil {
		return datastore.Config{}, "", err
	}
	password, err := cfg.String(ctx, "PostgresPassword", "", false)
	if err != nil {
		return datastore.Config{}, "", err
	}
	return datastore.Config{
		Engine:   datastore.EnginePostgres,
		Role:     datastore.RoleMain,
		Host:     host,
		Port:     port,
		User:     user,
		Password: password,
		Database: pgMain,
	}, fmt.Sprintf("postgres://%s@%s:%d/%s", user, host, port, pgMain), nil
}
