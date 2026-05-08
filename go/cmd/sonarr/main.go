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
	"sonarr.tv/sonarr/internal/blocklist"
	"sonarr.tv/sonarr/internal/buildinfo"
	"sonarr.tv/sonarr/internal/commands"
	"sonarr.tv/sonarr/internal/commands/handlers"
	"sonarr.tv/sonarr/internal/config"
	"sonarr.tv/sonarr/internal/configfile"
	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/delayprofiles"
	"sonarr.tv/sonarr/internal/disk"
	"sonarr.tv/sonarr/internal/downloadedimport"
	"sonarr.tv/sonarr/internal/episodefilemoving"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/history"
	"sonarr.tv/sonarr/internal/housekeeping"
	"sonarr.tv/sonarr/internal/instrumentation"
	"sonarr.tv/sonarr/internal/lifecycle"
	"sonarr.tv/sonarr/internal/mediafileattributes"
	"sonarr.tv/sonarr/internal/mediafiles"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/namingconfig"
	"sonarr.tv/sonarr/internal/parsingservice"
	"sonarr.tv/sonarr/internal/qualityprofiles"
	"sonarr.tv/sonarr/internal/recyclebin"
	"sonarr.tv/sonarr/internal/releaseprofiles"
	"sonarr.tv/sonarr/internal/rootfolders"
	"sonarr.tv/sonarr/internal/scheduledtasks"
	"sonarr.tv/sonarr/internal/series"
	"sonarr.tv/sonarr/internal/upgrademediafiles"
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

	// Health-check service constructed AFTER seriesSvc / diskProv /
	// rootFoldersRepo so checks that need them (RemovedSeries,
	// RootFolder — slice 3.A8) can be registered at construction.
	// healthcheck.Service has no Add method; the check list is fixed
	// at NewService time. Slice 3.A8 relocated this block from above
	// the mediafiles wiring (where it was originally placed in slice
	// 3.29 with the two zero-dep checks) to alongside the
	// healthcheck deps. healthSvc.Subscribe must run before the
	// ApplicationStartedEvent publish below; the order is preserved.

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
	// Slice 3.AB: wire series.Service deferred dependencies — episode
	// monitor cascade ([series.EpisodeMonitorCascade]) bound to
	// *episodes.Service.SetEpisodeMonitoredBySeason via Go's
	// structural typing. Mirrors the C# DI graph at
	// `SeriesService.cs:217` where `_episodeService.SetEpisodeMonitoredBySeason`
	// is invoked from inside UpdateSeries; the Go port requires the
	// host to wire the cascade via the optional setter so series can
	// avoid an episodes-package import. Without this, UpdateSeries'
	// season-monitored toggle propagation to episodes is silently
	// skipped.
	seriesSvc.SetEpisodeMonitorCascade(episodesSvc)
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
	// Slice 3.AA: cross-package event-handler subscribers.
	//
	// Wires four IHandle<TEvent> ports already shipped on episodes /
	// episodefiles into the bus. C# fires these via IoC-discovered
	// IHandle<T> attributes; the Go port surfaces them as plain
	// methods + leaves the host to thread the event payload into the
	// method args.
	//
	//   - episodefiles.Service.HandleSeriesDeleted ←
	//     series.SeriesDeletedEvent. Bulk-deletes EpisodeFiles rows
	//     for every Series in the event payload (mirrors C#
	//     `MediaFileService.HandleAsync(SeriesDeletedEvent)` at
	//     `MediaFileService.cs:131-134`).
	//
	//   - episodes.Service.HandleEpisodeFileAdded ←
	//     episodefiles.EpisodeFileAddedEvent. Sets EpisodeFileId on
	//     every linked Episode + drops the missing-from-disk cache
	//     entry. The handler signature splits the event into
	//     (fileID, linkedEpisodes) — the lambda extracts both from
	//     the event payload.
	//
	//   - episodes.Service.HandleEpisodeFileDeleted ←
	//     episodefiles.EpisodeFileDeletedEvent. Auto-unmonitor
	//     decision on the deleted episodes per the C# decision
	//     matrix. The episodes package can't import episodefiles
	//     (cycle), so episodes.FileDeleteReason is a parallel enum
	//     with matching ordinals — the lambda casts via int(...).
	//     Pinned by tests on both sides; reordering either side
	//     bites the test and forces a host-adapter audit.
	//
	//   - episodes.Service.HandleSeriesScanned ←
	//     mediafiles.SeriesScannedEvent. Drains the
	//     missing-from-disk cache for the scanned series and
	//     unmonitors any episode IDs that didn't pick up a file in
	//     the interim. The handler takes a bare seriesID; the
	//     lambda extracts ev.Series.ID (defensive: nil-Series → no-op).
	//
	// Until this slice the episodes/episodefiles services published
	// these events but no in-process subscriber consumed them — the
	// C# side has subscribers that the Go port shipped in code but
	// hadn't bus-subscribed at boot.
	messaging.Subscribe(bus, "episodefiles.HandleSeriesDeleted",
		func(ctx context.Context, ev series.SeriesDeletedEvent) error {
			return episodeFilesSvc.HandleSeriesDeleted(ctx, ev)
		})
	messaging.Subscribe(bus, "episodes.HandleEpisodeFileAdded",
		func(ctx context.Context, ev episodefiles.EpisodeFileAddedEvent) error {
			if ev.EpisodeFile == nil {
				return nil
			}
			return episodesSvc.HandleEpisodeFileAdded(ctx, ev.EpisodeFile.ID, ev.LinkedEpisodes)
		})
	messaging.Subscribe(bus, "episodes.HandleEpisodeFileDeleted",
		func(ctx context.Context, ev episodefiles.EpisodeFileDeletedEvent) error {
			if ev.EpisodeFile == nil {
				return nil
			}
			return episodesSvc.HandleEpisodeFileDeleted(
				ctx, ev.EpisodeFile.ID, ev.LinkedEpisodes,
				episodes.FileDeleteReason(int(ev.Reason)),
			)
		})
	messaging.Subscribe(bus, "episodes.HandleSeriesScanned",
		func(ctx context.Context, ev mediafiles.SeriesScannedEvent) error {
			if ev.Series == nil {
				return nil
			}
			return episodesSvc.HandleSeriesScanned(ctx, ev.Series.ID)
		})
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
	// Naming-config wiring (Phase 3.94). Singleton-row Service that
	// the slice-3.94 fileNamerAdapter reaches for to compute
	// RequiresAbsoluteEpisodeNumber / RequiresEpisodeTitle for the
	// downstream import-decision specs. namingconfig.NewRepo stores
	// + reads from the dynamix NamingConfig table.
	namingConfigRepo, err := namingconfig.NewRepo(db)
	if err != nil {
		return fmt.Errorf("namingconfig repo: %w", err)
	}
	// Slice 3.A2: bus injection + Subscribe wires the namingconfig
	// cache-invalidation handler. With Save publishing
	// NamingConfigSavedEvent, every save (Service.Save in-process or
	// any future out-of-band writer that publishes the event)
	// invalidates the cached singleton row. Eliminates the per-call
	// DB hit in the slice-3.94 fileNamerAdapter + slice-3.96
	// nameBuilderAdapter — both call GetConfig once per
	// import-decision spec / writer-path invocation.
	namingConfigSvc := namingconfig.NewService(namingConfigRepo, bus)
	namingConfigSvc.Subscribe(bus)
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
	// Sample-detector wiring (Phase 3.84 + 3.A3 ffprobe reader).
	// Constructs the real *episodeimport.SampleDetector. The
	// VideoRuntimeReader binding tries the new ffprobeRuntimeReader
	// (Phase 3.A3) — ffprobe-on-PATH backed reader that resolves the
	// slice-3.87 NotSampleSpecification's runtime-bucket Indeterminate
	// cascade for short-runtime samples — and falls back to the
	// noopVideoRuntimeReader if ffprobe isn't installed (the binary
	// isn't a hard install requirement; degrading to Indeterminate
	// keeps the orchestrator functional without it).
	var videoReader episodeimport.VideoRuntimeReader
	if r, err := newFfprobeRuntimeReader(logger); err == nil {
		videoReader = r
		logger.Info("ffprobe: video-runtime reader bound", "binary", r.binary)
	} else {
		videoReader = noopVideoRuntimeReader{}
		logger.Warn("ffprobe: binary not on PATH; falling back to noopVideoRuntimeReader (slice-3.87 Indeterminate cascade resumes)",
			"err", err)
	}
	sampleDetector := episodeimport.NewSampleDetector(videoReader, logger)

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
		// Slice 3.90: AggregateEpisodes wired via the
		// [episodeParsingServiceAdapter] ctx-bridge over the slice-3.81
		// *parsingservice.Service. Aggregator mirrors C#
		// `AggregateEpisodes.cs:11-113`: three-source pick from
		// FileEpisodeInfo / DownloadClientEpisodeInfo / FolderEpisodeInfo
		// followed by a ValidateForSeriesType gate and a special-episode
		// fallback. The adapter swallows context.Background errors as
		// log+nil to match the narrow interface's ctx-less + errorless
		// surface.
		episodeimport.NewAggregateEpisodes(episodeParsingServiceAdapter{svc: parsingSvc, log: logger}),
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
	// Slice 3.91: SameEpisodesImportSpecification wired via Go's
	// structural typing — *episodes.Service.GetEpisodesByFileID(ctx, id)
	// matches the slice-3.65 episodeimport.SameEpisodesEpisodeFinder
	// narrow interface exactly. Spec mirrors C# `SameEpisodesImportSpecification.cs:11-32`:
	// rejects an import candidate when an existing on-disk file linked
	// to one of its episodes covers OTHER episodes the candidate
	// doesn't (importing would orphan the wider file's metadata).
	// Lookup errors translate to Accept + Warn-log so a transient DB
	// outage doesn't bounce every candidate file in a batch.
	//
	// Constructor returns (*Spec, error); errors out only on nil finder
	// — *episodes.Service is non-nil here so the error arm is
	// belt-and-braces. Appended after the spec slice literal so the
	// (*Spec, error) shape stays clean.
	sameEpisodesSpec, err := episodeimport.NewSameEpisodesImportSpecification(episodesSvc, logger)
	if err != nil {
		return fmt.Errorf("same-episodes spec: %w", err)
	}
	importDecisionSpecs = append(importDecisionSpecs, sameEpisodesSpec)
	// Slice 3.92: FreeSpaceSpecification wired via Go's structural
	// typing — *config.Provider satisfies the slice-3.65
	// FreeSpaceConfig narrow interface (SkipFreeSpaceCheckWhenImporting
	// + MinimumFreeSpaceWhenImporting getters were added in slice 3.53),
	// and *disk.Provider satisfies the AvailableSpaceProvider narrow
	// interface (GetAvailableSpace shipped in the original disk
	// package). Spec mirrors C# `FreeSpaceSpecification.cs:11-67`:
	// rejects when the destination filesystem doesn't have room for
	// the candidate file plus the user's MinimumFreeSpaceWhenImporting
	// MB headroom; ExistingFile / SkipFreeSpaceCheckWhenImporting /
	// disk-error paths Accept defensively. Errors out only on nil
	// deps — both deps are non-nil here so the error arm is
	// belt-and-braces.
	freeSpaceSpec, err := episodeimport.NewFreeSpaceSpecification(configProv, diskProv, logger)
	if err != nil {
		return fmt.Errorf("free-space spec: %w", err)
	}
	importDecisionSpecs = append(importDecisionSpecs, freeSpaceSpec)
	// Slice 3.93: NotUnpackingSpecification wired via Go's structural
	// typing — *config.Provider satisfies the slice-3.65
	// UnpackingConfig narrow interface (DownloadClientWorkingFolders()
	// shipped in slice 3.53) and *disk.Provider satisfies the
	// FileLastWriteProvider narrow interface (FileGetLastWrite shipped
	// in the original disk package). nil clock → defaults to time.Now;
	// isWindows captured from runtime.GOOS at construction.
	// Spec mirrors C# `NotUnpackingSpecification.cs:12-65`: walks the
	// candidate's parent-dir chain for any name starting with a
	// configured working-folder prefix (default `_UNPACK_|_FAILED_`).
	// On non-Windows: reject Unpacking immediately. On Windows: also
	// check the file's mtime — within 5 minutes of now → reject;
	// older accept (the unpacker may have died leaving the dir
	// behind). ExistingFile path Accepts defensively.
	notUnpackingSpec, err := episodeimport.NewNotUnpackingSpecification(configProv, diskProv, nil, logger)
	if err != nil {
		return fmt.Errorf("not-unpacking spec: %w", err)
	}
	importDecisionSpecs = append(importDecisionSpecs, notUnpackingSpec)
	// Slice 3.94: AbsoluteEpisodeNumberSpecification wired via the
	// new [fileNamerAdapter] over *namingconfig.Service +
	// [organizer.RequiresAbsoluteEpisodeNumber] / RequiresEpisodeTitle
	// free fns. nil clock → defaults to time.Now.
	// Spec mirrors C# `AbsoluteEpisodeNumberSpecification.cs:11-58`:
	// rejects an anime import when the active naming pattern requires
	// the {absolute} token AND any matched episode is recently aired
	// (within 1 day) without an absolute episode number. Skip arms:
	// non-Anime SeriesType / pattern doesn't require {absolute} /
	// episode aired > 1 day ago → Accept.
	absEpsSpec, err := episodeimport.NewAbsoluteEpisodeNumberSpecification(
		fileNamerAdapter{svc: namingConfigSvc, log: logger}, nil, logger)
	if err != nil {
		return fmt.Errorf("absolute-episode-number spec: %w", err)
	}
	importDecisionSpecs = append(importDecisionSpecs, absEpsSpec)
	// Slice 3.95: EpisodeTitleSpecification wired via the slice-3.94
	// fileNamerAdapter + new episodeTitleConfigAdapter (parses the
	// config.Provider EpisodeTitleRequired() string into the typed
	// enum) + *episodes.Service for EpisodeFinderBySeason (the
	// GetEpisodesBySeason(ctx, seriesID, seasonNumber) signature
	// matches the narrow interface exactly via Go's structural typing).
	// Spec mirrors C# `EpisodeTitleSpecification.cs:13-97`: rejects
	// when the active naming pattern needs the episode title AND a
	// matched episode is recently aired (within 48h) without a real
	// title (or with a literal "TBA"). Skip arms — ExistingFile /
	// EpisodeTitleRequired=Never / pattern doesn't require {Episode
	// Title} / BulkSeasonReleases-with-non-bulk-shape → Accept.
	// nil clock → defaults to time.Now.
	episodeTitleSpec, err := episodeimport.NewEpisodeTitleSpecification(
		episodeTitleConfigAdapter{cfg: configProv},
		fileNamerAdapter{svc: namingConfigSvc, log: logger},
		episodesSvc,
		nil,
		logger,
	)
	if err != nil {
		return fmt.Errorf("episode-title spec: %w", err)
	}
	importDecisionSpecs = append(importDecisionSpecs, episodeTitleSpec)
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
	// Slice 3.96: episodefilemoving.Service wiring — first slice in
	// the ImportApprovedEpisodes (Option A) sub-arc. Constructs the
	// writer with all 9 deps populated against real production
	// services (or noop defaults where the downstream subsystem
	// hasn't ported). The Service value is held in
	// `episodeFileMovingSvc` for use by the slice-3.97
	// upgrademediafiles.Service constructor (the next slice in the
	// arc); it isn't yet plugged into the
	// `downloadedimport.Service` ImportRunner slot — that swap
	// lands in slice 3.98 once upgrademediafiles ports.
	//
	// Dep wiring:
	//
	//   - fs:      *episodefilemoving.DiskFilesystem (slice 3.74)
	//              composes *disk.Provider with the move/copy/hardlink
	//              dispatch the C# DiskTransferService layer adds.
	//
	//   - names:   nameBuilderAdapter loads the active NamingConfig row
	//              per call and translates ALL writer-relevant fields
	//              (formats + folder formats + cleaning) into a
	//              [organizer.BuildOptions] before forwarding into
	//              [organizer.BuildFilePath] / [organizer.BuildSeasonPath].
	//              Distinct from the slice-3.94 fileNamerAdapter which
	//              only populates the four format-string fields the
	//              two import-decision specs read.
	//
	//   - roots:   rootFolderFinderAdapter talks directly to
	//              *rootfolders.Repo (NOT the full Service) because the
	//              Service requires DiskProvider methods
	//              (FolderWritable / GetTotalSize) that don't exist on
	//              *disk.Provider yet. The narrow GetBestRootFolderPath
	//              path only needs the Repo + the package-level
	//              [rootfolders.BestPath] — the missing disk surfaces
	//              are only consulted by Service.fillDetails / Add /
	//              GetUnmappedFolders, none of which the writer reaches.
	//              Future slice will swap to a Service-wrapping adapter
	//              once the disk methods port.
	//
	//   - cfg:     *config.Provider via Go's structural typing — the
	//              CopyUsingHardlinks() bool getter added in this slice
	//              satisfies the writer's narrow [Config] interface.
	//
	//   - finder:  *episodes.Service via Go's structural typing — the
	//              GetEpisodesByFileID(ctx, int) signature already
	//              shipped in slice 3.3 matches the
	//              [EpisodeFinder] interface exactly.
	//
	//   - dates:   nil → noopFileDateUpdater. The C#
	//              IUpdateEpisodeFileService.ChangeFileDateForFile (used
	//              for "useScene/useUtc" file-date updates after a move)
	//              hasn't ported; nil-tolerant per the slice-3.70
	//              contract.
	//
	//   - attrs:   mediafileattributes.NewService(configProv, diskProv,
	//              logger). Slice 3.75 closed the noopAttributeWriter
	//              stub the writer used since slice 3.70; the real
	//              Service applies the configured Linux chmod/chown
	//              after each move.
	//
	//   - scripts: nil → alwaysDeferImportScriptRunner. The C#
	//              IImportScript surface (custom-script post-import
	//              hook) hasn't ported; nil-tolerant per the slice-3.70
	//              contract — the writer always falls through to its
	//              regular TransferFile path.
	//
	//   - events:  nil → noopEventPublisher. The C#
	//              EpisodeFolderCreatedEvent has no ported subscribers;
	//              the writer's Publish call after a folder-create is a
	//              no-op until a downstream consumer appears.
	//
	//   - log:     logger.
	rootFoldersRepo, err := rootfolders.NewRepo(db)
	if err != nil {
		return fmt.Errorf("rootfolders repo: %w", err)
	}

	// Slice 3.AB: wire series.Service tag-change provider (the second
	// of series.Service's two deferred-dependency setters; the
	// EpisodeMonitorCascade was wired alongside episodesSvc above).
	// First production binding of [autotagging.Service] —
	// [seriesTagChangeProviderAdapter] composes Service.All +
	// rootFoldersRepo.All + the package-level [autotagging.ChangesFor]
	// to satisfy [series.TagChangeProvider]. Mirrors the C# DI graph
	// at `SeriesService.cs:281` where IAutoTaggingService.GetTagChanges
	// is invoked from UpdateSeries / UpdateTags. Without this,
	// UpdateTags / UpdateSeries silently skip the auto-tagging delta
	// (existing tolerance at `internal/series/mutations.go:131-134`).
	autoTagsSvc := autotagging.NewService(autoTagRepo, bus)
	if autoTagsSvc == nil {
		return fmt.Errorf("autotagging service: nil — required dep regressed")
	}
	seriesSvc.SetTagChangeProvider(seriesTagChangeProviderAdapter{
		autoTags:    autoTagsSvc,
		rootFolders: rootFoldersRepo,
	})

	// Slice 3.A8: healthcheck service construction relocated here so
	// the RemovedSeriesCheck (deps: *series.Service.GetAllSeries) and
	// RootFolderCheck (deps: *series.Service.AllSeriesPaths +
	// rootFolderHealthResolverAdapter over *rootfolders.Repo +
	// *disk.Provider.FolderExists/FolderEmpty) can be registered at
	// construction. Two new checks join AppDataLocation +
	// ApiKeyValidation. Other available checks (SystemTime,
	// IndexerStatus, RecyclingBin, Proxy) gate on subsystems that
	// don't exist yet — cloud-time fetcher / EnabledIndexers /
	// disk.Provider.FolderWritable / proxy ping reach.
	apiKeyCheck, err := healthcheck.NewApiKeyValidationCheck(cfg)
	if err != nil {
		return fmt.Errorf("healthcheck apiKey: %w", err)
	}
	removedSeriesCheck, err := healthcheck.NewRemovedSeriesCheck(seriesSvc)
	if err != nil {
		return fmt.Errorf("healthcheck removedSeries: %w", err)
	}
	rootFolderCheck := healthcheck.NewRootFolderCheck(
		seriesSvc,
		rootFolderHealthResolverAdapter{repo: rootFoldersRepo},
		diskProv,
	)
	healthSvc, err := healthcheck.NewService([]healthcheck.Check{
		healthcheck.NewAppDataLocationCheck(folder),
		apiKeyCheck,
		removedSeriesCheck,
		rootFolderCheck,
	}, bus, logger)
	if err != nil {
		return fmt.Errorf("healthcheck service: %w", err)
	}
	healthSvc.Subscribe(bus)

	mediaFileAttrsSvc := mediafileattributes.NewService(configProv, diskProv, logger)
	if mediaFileAttrsSvc == nil {
		return fmt.Errorf("mediafileattributes service: nil — required dep regressed")
	}
	episodeFileMovingFs := episodefilemoving.NewDiskFilesystem(diskProv, logger)
	if episodeFileMovingFs == nil {
		return fmt.Errorf("episodefilemoving DiskFilesystem: nil — required dep regressed")
	}
	episodeFileMovingSvc, err := episodefilemoving.NewService(
		episodeFileMovingFs,
		nameBuilderAdapter{svc: namingConfigSvc, log: logger},
		rootFolderFinderAdapter{repo: rootFoldersRepo, log: logger},
		configProv,
		episodesSvc,
		nil, // FileDateUpdater — noop; C# IUpdateEpisodeFileService unported.
		mediaFileAttrsSvc,
		nil, // ImportScriptRunner — DeferMove; C# IImportScript unported.
		nil, // EventPublisher — noop; C# EpisodeFolderCreatedEvent has no ported subscribers.
		logger,
	)
	if err != nil {
		return fmt.Errorf("episodefilemoving service: %w", err)
	}
	// Slice 3.97: upgrademediafiles.Service wiring — second slice in
	// the ImportApprovedEpisodes (Option A) sub-arc. Constructs the
	// upgrader on top of the slice-3.96 *episodefilemoving.Service +
	// a freshly-constructed *recyclebin.Service. The upgrader handles
	// the recycle-bin / replace-existing flow that the ImportApprovedEpisodes
	// orchestrator (slice 3.71) reaches for through its
	// MediaFileUpgrader seam — every existing EpisodeFile referenced
	// by an incoming LocalEpisode is recycled (or hard-deleted when the
	// recycle bin is disabled) BEFORE the new file is moved into place.
	//
	// Until this slice the orchestrator's noopMediaFileUpgrader returned
	// "no existing files" for every upgrade, which would silently
	// overwrite duplicates on disk once the writer was production-bound.
	// Holding the constructed *upgrademediafiles.Service in
	// upgradeMediaFilesSvc keeps the construction error paths live;
	// slice 3.98 plugs it into the ImportApprovedEpisodes constructor.
	//
	// Dep wiring:
	//
	//   - recycle: *recyclebin.Service constructed here over
	//              *config.Provider (RecycleBin + RecycleBinCleanupDays
	//              getters shipped 3.76 / 3.77) + *disk.Provider (the
	//              7-method DiskOps surface — every method already
	//              shipped on the Provider after slice 3.74's transfer
	//              primitives). First production binding of the
	//              slice-3.76 recyclebin.Service (CleanUpRecycleBin
	//              handler in slice 3.77 binds it separately for the
	//              scheduled-task path).
	//
	//   - deleter: *episodefiles.Service via Go's structural typing —
	//              the slice-3.2 Delete(ctx, *File, DeleteMediaFileReason)
	//              method matches the upgrader's MediaFileDeleter
	//              interface exactly.
	//
	//   - mover:   *episodefilemoving.Service from slice 3.96 (above) —
	//              MoveEpisodeFileWithLocal + CopyEpisodeFile both
	//              shipped at slice 3.70; satisfies the upgrader's
	//              EpisodeFileMover interface directly.
	//
	//   - files:   *episodefiles.Service via Go's structural typing —
	//              the slice-3.2 GetFile(ctx, int) method matches the
	//              upgrader's EpisodeFileFinder interface exactly.
	//
	//   - fs:      *disk.Provider via Go's structural typing —
	//              FolderExists + FileExists both already shipped;
	//              the upgrader's narrow Filesystem interface needs
	//              nothing else.
	//
	//   - log:     logger.
	recycleBinSvc := recyclebin.NewService(configProv, diskProv, logger)
	if recycleBinSvc == nil {
		return fmt.Errorf("recyclebin service: nil — required dep regressed")
	}
	upgradeMediaFilesSvc, err := upgrademediafiles.NewService(
		recycleBinSvc,
		episodeFilesSvc,
		episodeFileMovingSvc,
		episodeFilesSvc,
		diskProv,
		logger,
	)
	if err != nil {
		return fmt.Errorf("upgrademediafiles service: %w", err)
	}
	// Slice 3.98: ImportApprovedEpisodes wiring — closes the
	// ImportApprovedEpisodes (Option A) sub-arc. Constructs
	// *episodeimport.ImportApprovedEpisodes against the slice-3.97
	// *upgrademediafiles.Service (above) + *episodefiles.Service +
	// *disk.Provider. The four optional deps that gate ancillary
	// behaviours (extras / history enrichment / quality-tiebreak /
	// event-emission / rescan) stay nil-defaulted to their package
	// noops — none are required for the arc's endpoint of "files
	// actually move":
	//
	//   - extras (ExtrasImporter): noopExtrasImporter — the C#
	//     IExtraService + IExistingExtraFiles surfaces are
	//     unported. Companion-extras detection / movement is a
	//     separate sub-arc.
	//   - historyFinder (HistoryGrabFinderForImport):
	//     noopHistoryGrabFinder — the slice-3.61 HistoryGrabsFinder
	//     port exists but its MostRecentGrabbed-shape adapter is
	//     deferred. The orchestrator's IndexerFlags / ReleaseType
	//     enrichment branches at C# `:90-108` skip cleanly.
	//   - qcmp (QualityComparer): noopQualityComparer — the
	//     decisionengine.CompareQualityModels surface needs a
	//     QualityProfile lookup that isn't wired to this site yet.
	//     Defaulting to "all qualities equal" collapses
	//     ImportApprovedEpisodes.sortApproved to Size-only ordering,
	//     which matches the C# behaviour when the comparer's
	//     QualityProfile lookup yields equal qualities.
	//   - events (ImportEventPublisher): noopImportEventPublisher —
	//     EpisodeImportedEvent / EpisodeImportFailedEvent have no
	//     ported subscribers yet, AND the messaging.Bus generic
	//     Publish[E any] doesn't satisfy the bare Publish(any)
	//     narrow interface without an adapter (a future slice can
	//     bridge by type-switching the event into the appropriate
	//     concrete type before delegating to messaging.Publish).
	//   - rescan (RescanSeriesCommander): noopRescanCommander — the
	//     RescanSeriesCommand command bus path exists, but the
	//     PushRescanSeries(seriesID) → command-queue adapter is its
	//     own slice.
	//
	// The constructed *ImportApprovedEpisodes is then plugged into
	// the slice-3.79a downloadedimport.Service via the new
	// downloadedImportRunner adapter (downloadedimport_wiring.go)
	// that bridges the ctx-bearing+error orchestrator signature to
	// the ctx-less+errorless ImportRunner narrow interface — same
	// posture as the slice-3.85 downloadedImportDecider /
	// slice-3.90 parsingservice ctx-bridge adapters.
	//
	// With this slice in place the POST /api/v3/command
	// {"name":"DownloadedEpisodesScan"} endpoint actually moves a
	// downloaded file into the series library: the last and biggest
	// stub in the import pipeline (stubImportRunner since slice 3.79a)
	// is replaced by the real production binding. End-to-end pipeline
	// is now production-bound: download-folder-scan → import-decision
	// (filter + 11 specs + 4 augmenters) → upgrade-and-recycle (slice
	// 3.97) → writer move (slice 3.96) → episodefiles row Add. ✓
	// Slice 3.A1: history.Service constructed for the import path.
	// First production binding of the slice-3.62 history.Service —
	// the historyGrabFinderForImportAdapter (downloadedimport_wiring.go)
	// reaches for it via the typed Find(ctx, downloadID, EventTypeGrabbed)
	// surface. The slice-3.62 Service is read-only here; the seven
	// IHandle<...> event-bus subscribers it has in C# are deferred
	// until each event-shape ports.
	historyRepo, err := history.NewRepo(db)
	if err != nil {
		return fmt.Errorf("history repo: %w", err)
	}
	historySvc := history.NewService(historyRepo)
	if historySvc == nil {
		return fmt.Errorf("history service: nil — required dep regressed")
	}
	// Slice 3.A2: qualityprofiles.Service constructed for the import
	// path. First production binding of the slice-2.45-ish
	// qualityprofiles.Service for non-housekeeping use — the
	// qualityComparerAdapter (downloadedimport_wiring.go) reaches for
	// it via Get(ctx, profileID) to resolve the per-series QualityProfile
	// during ImportApprovedEpisodes.sortApproved tiebreaks. The slice
	// reuses the qpRepo already constructed for housekeeping above.
	qualityProfilesSvc, err := qualityprofiles.NewService(qpRepo, bus)
	if err != nil {
		return fmt.Errorf("qualityprofiles service: %w", err)
	}
	// Slice 3.A9: customformats.Service constructed + wired to its
	// two qualityprofiles.Service event handlers. The C# class
	// `QualityProfileService` is `IHandle<CustomFormatAddedEvent>` +
	// `IHandle<CustomFormatDeletedEvent>` at
	// `Profiles/Qualities/QualityProfileService.cs:159-191`; the Go
	// port already exposes [Service.OnCustomFormatAdded] /
	// [Service.OnCustomFormatDeleted] (slice 3.X — see
	// internal/qualityprofiles/handlers.go:95-173) but the host hadn't
	// wired them yet. Subscribing here closes the cycle so any future
	// customformats.Service.Insert / .Delete API caller fans the
	// per-profile FormatItems update through every QualityProfile.
	//
	// customFormatsSvc reuses the cfRepo already constructed for the
	// CleanupQualityProfileFormatItems housekeeping task (no second
	// repo). Bus injection enables the publishAdded/publishDeleted
	// emitters; until an API caller appears, no events fire.
	customFormatsSvc := customformats.NewService(cfRepo, bus)
	if customFormatsSvc == nil {
		return fmt.Errorf("customformats service: nil — required dep regressed")
	}
	messaging.Subscribe(bus, "qualityprofiles.OnCustomFormatAdded",
		func(ctx context.Context, ev customformats.CustomFormatAddedEvent) error {
			return qualityProfilesSvc.OnCustomFormatAdded(ctx, ev.Format)
		})
	messaging.Subscribe(bus, "qualityprofiles.OnCustomFormatDeleted",
		func(ctx context.Context, ev customformats.CustomFormatDeletedEvent) error {
			return qualityProfilesSvc.OnCustomFormatDeleted(ctx, ev.Format)
		})
	_ = customFormatsSvc // held until the customformats API surface ports.
	importApprovedSvc, err := episodeimport.NewImportApprovedEpisodes(
		episodeFilesSvc,      // files (MediaFileWriter — Add + GetFilesWithRelativePath + Delete)
		diskProv,             // disk (FileSizeReader — GetFileSize)
		upgradeMediaFilesSvc, // upgrader — slice-3.97; recycles + replaces existing files
		nil,                  // extras → noopExtrasImporter (C# IExtraService unported)
		// Slice 3.A1: HistoryGrabFinderForImport wired via the new
		// historyGrabFinderForImportAdapter (downloadedimport_wiring.go) —
		// delegates to *history.Service.Find with the typed
		// EventTypeGrabbed filter (slice 3.62), returning the most-recent
		// matching entry. Mirrors the C# call chain at
		// `ImportApprovedEpisodes.cs:92-94`. With this lit, the
		// orchestrator's IndexerFlags / ReleaseType enrichment
		// branches at C# `:90-108` start populating against real
		// history rows for any download that's been previously grabbed.
		historyGrabFinderForImportAdapter{svc: historySvc},
		// Slice 3.A2: QualityComparer wired via the new
		// qualityComparerAdapter (downloadedimport_wiring.go) —
		// resolves the per-series QualityProfile via *series.Service.GetSeries
		// + *qualityprofiles.Service.Get, parses Items, and delegates to
		// [decisionengine.CompareQualityModels] with respectGroupOrder=false
		// (matches C# QualityModelComparer default ctor). Defensive: any
		// lookup/parse failure returns 0 (treats as equal — degrades
		// to Size-only ordering in sortApproved, matching C# try/catch
		// behaviour at the call site). Mirrors the C# call chain
		// `ImportApprovedEpisodes.cs:63` invoking _qualityComparer.Compare.
		qualityComparerAdapter{series: seriesSvc, qp: qualityProfilesSvc, log: logger},
		// Slice 3.99: ImportEventPublisher wired via the new
		// importEventPublisherAdapter (downloadedimport_wiring.go) —
		// type-switches on the two concrete event types the orchestrator
		// emits (EpisodeImportedEvent / EpisodeImportFailedEvent) and
		// republishes through *messaging.Bus's typed Publish[E]. With no
		// ported subscribers yet, this is "lit but no listeners" — but
		// the bus dispatch path is now exercised so the next subsystem
		// that ports a subscriber sees events from production without
		// further wiring at this site.
		importEventPublisherAdapter{bus: bus, log: logger},
		// Slice 3.A0: RescanSeriesCommander wired via the new
		// rescanSeriesCommanderAdapter (downloadedimport_wiring.go) —
		// JSON-encodes a [mediafiles.RescanSeriesCommand]{SeriesID: &id}
		// then pushes it through *commands.Service.Push using the
		// canonical RescanSeriesName + Priority=Normal + Trigger=Unspecified
		// (mirroring the default C# Push overload invoked from
		// `ImportApprovedEpisodes.cs:186`). The slice-3.49
		// IExecute<RescanSeriesCommand> handler picks the row up via
		// the dispatcher and delegates to *mediafiles.Service.RescanSeries.
		rescanSeriesCommanderAdapter{cmds: cmdSvc, log: logger},
		logger,
	)
	if err != nil {
		return fmt.Errorf("import approved episodes: %w", err)
	}

	// Slice 3.A7: decisionengine register-into-DecisionMaker wiring.
	// First production binding of [decisionengine.NewDecisionMaker] —
	// constructs every ported Specification (19 of ~25) and feeds the
	// resulting slice to the orchestrator. The DecisionMaker is held
	// via `_ = decisionMaker` until a downstream consumer (the
	// indexer-search / RSS flow that ports as part of Phase 4) wires
	// up. Holding the constructed value keeps the boot-time
	// nil-required-dep guards live: any signature drift on a per-spec
	// constructor or new required-dep addition surfaces as a startup
	// error here, not at first use.
	//
	// Per-spec adapter rationale lives in
	// `cmd/sonarr/decisionengine_wiring.go`; the noop MaximumSize /
	// MinimumAge readers there are stand-ins until
	// [config.Provider.MaximumSize] / .MinimumAge port (both default
	// to 0 = disabled in C#, so the wired specs accept-all on those
	// caps until the getters land).
	upgradableSpec, err := decisionengine.NewUpgradableSpecification(configProv, logger)
	if err != nil {
		return fmt.Errorf("upgradable specification: %w", err)
	}
	blocklistRepo, err := blocklist.NewRepo(db)
	if err != nil {
		return fmt.Errorf("blocklist repo: %w", err)
	}
	delayProfilesRepo, err := delayprofiles.NewRepo(db)
	if err != nil {
		return fmt.Errorf("delayprofiles repo: %w", err)
	}
	delayProfilesSvc := delayprofiles.NewService(delayProfilesRepo)
	if delayProfilesSvc == nil {
		return fmt.Errorf("delayprofiles service: nil — required dep regressed")
	}
	releaseProfilesRepo, err := releaseprofiles.NewRepo(db)
	if err != nil {
		return fmt.Errorf("releaseprofiles repo: %w", err)
	}
	releaseProfilesSvc := releaseprofiles.NewService(releaseProfilesRepo)
	if releaseProfilesSvc == nil {
		return fmt.Errorf("releaseprofiles service: nil — required dep regressed")
	}
	matcherSvc := releaseprofiles.NewMatcherService()
	if matcherSvc == nil {
		return fmt.Errorf("releaseprofiles matcher service: nil — required dep regressed")
	}
	decisionEngineSpecs, err := buildDecisionEngineSpecs(decisionEngineDeps{
		upgradable:     upgradableSpec,
		qualityProfile: qualityProfilesSvc,
		episodes:       episodesSvc,
		episodeFiles:   episodeFilesSvc,
		disk:           diskProv,
		configCfg:      configProv,
		freeSpaceCfg:   configProv,
		retentionCfg:   configProv,
		delayProfiles:  delayProfilesSvc,
		releaseProfile: releaseProfilesSvc,
		matcher:        matcherSvc,
		blocklistRepo:  blocklistRepo,
		log:            logger,
	})
	if err != nil {
		return fmt.Errorf("decisionengine specs: %w", err)
	}
	decisionMaker := decisionengine.NewDecisionMaker(decisionEngineSpecs, logger)
	_ = decisionMaker // held until the indexer-search / RSS flow ports.

	dimpDisk := downloadedImportDiskOps{Provider: diskProv}
	dimpSvc := downloadedimport.NewService(
		dimpDisk,
		downloadedImportDiskScanner{disk: diskProv, log: logger},
		downloadedImportSeriesPaths{svc: seriesSvc, log: logger},
		downloadedImportSeriesParser{svc: parsingSvc, log: logger},
		downloadedImportDecider{svc: importDecisionMaker, log: logger},
		downloadedImportRunner{svc: importApprovedSvc, log: logger},
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
