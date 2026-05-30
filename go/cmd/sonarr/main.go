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
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"

	"sonarr.tv/sonarr/internal/aggregation"
	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/appenv"
	"sonarr.tv/sonarr/internal/auth"
	"sonarr.tv/sonarr/internal/autotagging"
	"sonarr.tv/sonarr/internal/backup"
	"sonarr.tv/sonarr/internal/blocklist"
	"sonarr.tv/sonarr/internal/blocklisthandlers"
	"sonarr.tv/sonarr/internal/buildinfo"
	"sonarr.tv/sonarr/internal/commands"
	"sonarr.tv/sonarr/internal/commands/handlers"
	"sonarr.tv/sonarr/internal/config"
	"sonarr.tv/sonarr/internal/configfile"
	"sonarr.tv/sonarr/internal/customfilters"
	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/delayprofiles"
	"sonarr.tv/sonarr/internal/deploymentinfo"
	"sonarr.tv/sonarr/internal/disk"
	"sonarr.tv/sonarr/internal/download"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclientstatus"
	"sonarr.tv/sonarr/internal/downloadedimport"
	"sonarr.tv/sonarr/internal/downloadeventhub"
	"sonarr.tv/sonarr/internal/downloadhistory"
	"sonarr.tv/sonarr/internal/downloadhistoryhandlers"
	"sonarr.tv/sonarr/internal/downloadmonitoring"
	"sonarr.tv/sonarr/internal/downloadprocessing"
	"sonarr.tv/sonarr/internal/episodefilemoving"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/episodemonitored"
	"sonarr.tv/sonarr/internal/episoderefreshed"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/extrafiles"
	"sonarr.tv/sonarr/internal/faileddownloads"
	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/history"
	"sonarr.tv/sonarr/internal/historyhandlers"
	"sonarr.tv/sonarr/internal/housekeeping"
	"sonarr.tv/sonarr/internal/httpclient"
	"sonarr.tv/sonarr/internal/importlistexclusions"
	"sonarr.tv/sonarr/internal/importlistitems"
	"sonarr.tv/sonarr/internal/importlists"
	"sonarr.tv/sonarr/internal/importliststatus"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/indexersearch"
	"sonarr.tv/sonarr/internal/indexerstatus"
	"sonarr.tv/sonarr/internal/instrumentation"
	"sonarr.tv/sonarr/internal/lifecycle"
	"sonarr.tv/sonarr/internal/logs"
	"sonarr.tv/sonarr/internal/mediacover"
	"sonarr.tv/sonarr/internal/mediafileattributes"
	"sonarr.tv/sonarr/internal/mediafiles"
	"sonarr.tv/sonarr/internal/mediainfoupdater"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/metadata"
	"sonarr.tv/sonarr/internal/metadatafiles"
	"sonarr.tv/sonarr/internal/metadataservice"
	"sonarr.tv/sonarr/internal/metadatasource"
	"sonarr.tv/sonarr/internal/namingconfig"
	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/notificationstatus"
	"sonarr.tv/sonarr/internal/parsingservice"
	"sonarr.tv/sonarr/internal/pendingreleases"
	"sonarr.tv/sonarr/internal/pendingreleaseshandlers"
	"sonarr.tv/sonarr/internal/qualitydefinitions"
	"sonarr.tv/sonarr/internal/qualityprofiles"
	"sonarr.tv/sonarr/internal/queue"
	"sonarr.tv/sonarr/internal/recyclebin"
	"sonarr.tv/sonarr/internal/redownload"
	"sonarr.tv/sonarr/internal/refreshepisodes"
	"sonarr.tv/sonarr/internal/refreshseries"
	"sonarr.tv/sonarr/internal/releasecache"
	"sonarr.tv/sonarr/internal/releaseprofiles"
	"sonarr.tv/sonarr/internal/remotepathmappings"
	"sonarr.tv/sonarr/internal/rootfolders"
	"sonarr.tv/sonarr/internal/scenemappings"
	"sonarr.tv/sonarr/internal/scheduledtasks"
	"sonarr.tv/sonarr/internal/series"
	"sonarr.tv/sonarr/internal/seriesadd"
	"sonarr.tv/sonarr/internal/signalr"
	"sonarr.tv/sonarr/internal/subtitlefiles"
	"sonarr.tv/sonarr/internal/tags"
	"sonarr.tv/sonarr/internal/trackeddownloads"
	"sonarr.tv/sonarr/internal/update"
	"sonarr.tv/sonarr/internal/upgrademediafiles"
	"sonarr.tv/sonarr/internal/users"
	"sonarr.tv/sonarr/internal/videofileinforeader"

	// Slice 4.BK (sub-arc W6c of queue [W6]): blank-import the 4
	// concrete metadata-writer packages so their init() blocks
	// register the (Implementation, ConfigContract) constructors
	// with metadata.Default at startup. Pattern-mirror of the
	// notifications + importlists blank-import pattern (when those
	// land — currently still TODO per slice 4.AY/4.AZ).
	_ "sonarr.tv/sonarr/internal/metadata/plex"
	_ "sonarr.tv/sonarr/internal/metadata/roksbox"
	_ "sonarr.tv/sonarr/internal/metadata/wdtv"
	_ "sonarr.tv/sonarr/internal/metadata/xbmc"

	// Slice 6.A6c (queue [15] orchestrator production wiring):
	// blank-import the 5 concrete download-client packages so their
	// init() blocks register (Implementation, ConfigContract) →
	// Constructor mappings against downloadclients.Default(). The
	// downloadClientLister adapter constructed below consults the
	// global registry at grab time; without these imports the
	// registry is empty and every grab buckets to Pending. Pattern-
	// mirror of the metadata-writer blank imports above.
	_ "sonarr.tv/sonarr/internal/downloadclients/aria2"
	_ "sonarr.tv/sonarr/internal/downloadclients/blackhole"
	_ "sonarr.tv/sonarr/internal/downloadclients/deluge"
	_ "sonarr.tv/sonarr/internal/downloadclients/downloadstation"
	_ "sonarr.tv/sonarr/internal/downloadclients/flood"
	_ "sonarr.tv/sonarr/internal/downloadclients/freebox"
	_ "sonarr.tv/sonarr/internal/downloadclients/hadouken"
	_ "sonarr.tv/sonarr/internal/downloadclients/nzbget"
	_ "sonarr.tv/sonarr/internal/downloadclients/nzbvortex"
	_ "sonarr.tv/sonarr/internal/downloadclients/pneumatic"
	_ "sonarr.tv/sonarr/internal/downloadclients/qbittorrent"
	_ "sonarr.tv/sonarr/internal/downloadclients/rqbit"
	_ "sonarr.tv/sonarr/internal/downloadclients/rtorrent"
	_ "sonarr.tv/sonarr/internal/downloadclients/sabnzbd"
	_ "sonarr.tv/sonarr/internal/downloadclients/transmission"
	_ "sonarr.tv/sonarr/internal/downloadclients/tribler"
	_ "sonarr.tv/sonarr/internal/downloadclients/utorrent"
	_ "sonarr.tv/sonarr/internal/downloadclients/vuze"

	// G7 (J424): import-list providers self-register via init() into
	// importlists.Default(); blank-import them so ImportListSync actually
	// has providers (the registry was empty → sync added nothing).
	_ "sonarr.tv/sonarr/internal/importlists/anilist"
	_ "sonarr.tv/sonarr/internal/importlists/custom"
	_ "sonarr.tv/sonarr/internal/importlists/myanimelist"
	_ "sonarr.tv/sonarr/internal/importlists/plex"
	_ "sonarr.tv/sonarr/internal/importlists/plexrss"
	_ "sonarr.tv/sonarr/internal/importlists/simkl"
	_ "sonarr.tv/sonarr/internal/importlists/sonarr"
	_ "sonarr.tv/sonarr/internal/importlists/trakt"
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

	logger, logFanout := instrumentation.NewLogger(os.Stderr, slog.LevelInfo)
	slog.SetDefault(logger)
	logger.Info("sonarr starting", "version", buildinfo.Version, "commit", buildinfo.Commit)
	defer logger.Info("sonarr stopped")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := serve(ctx, logger, logFanout); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// serve is the top-level wiring point. As phases land, additional
// subsystems (HTTP server, SignalR hub, job scheduler, providers)
// are constructed here.
func serve(ctx context.Context, logger *slog.Logger, logFanout *instrumentation.Fanout) error {
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
	// Audit fix B.12: pick up `Sonarr__Section__Key`-style env-var
	// overrides on boot, matching the C# host's
	// `AddEnvironmentVariables()` posture at
	// `src/NzbDrone.Host/Bootstrap.cs:266`.
	envOverrides := configfile.ParseOverridesFromEnv(os.Environ())
	cfg := configfile.NewProvider(loader, envOverrides, bus, auth.GenerateAPIKey)

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

	// Log database (RoleLog) + the async slog→DB sink (J406-J408 — the
	// C# NLog DatabaseTarget equivalent, registered after the log DB is
	// available, mirroring DatabaseTarget.Register). Opened separately
	// from the main DB so backups can skip the noisy log table. When the
	// log DB is disabled (Postgres without PostgresLogDb — the C#
	// LogDbEnabled=false posture) the sink is simply not attached and
	// the GET /log read returns empty, matching upstream. logRepo is
	// kept in scope for the GET /log read + ClearLog wiring.
	var logRepo *logs.Repo
	if logCfg, logEnabled, lerr := buildLogDatastoreConfig(ctx, cfg, folder, instanceName); lerr != nil {
		return fmt.Errorf("log datastore config: %w", lerr)
	} else if logEnabled {
		logDB, err := datastore.Open(ctx, logCfg)
		if err != nil {
			return fmt.Errorf("log datastore open: %w", err)
		}
		defer func() { _ = logDB.Close() }()
		if err := datastore.Migrate(ctx, logDB); err != nil {
			return fmt.Errorf("log datastore migrate: %w", err)
		}
		logRepo, err = logs.NewRepo(logDB)
		if err != nil {
			return fmt.Errorf("log repo: %w", err)
		}
		logSink := logs.NewSink(logRepo, slog.LevelInfo, 0)
		defer func() { _ = logSink.Close() }()
		logFanout.Attach(logSink)
		logger.Info("log database ready", "role", string(logDB.Role))
	}
	// logRepo (nil when the log DB is disabled) feeds the GET /log read
	// via logLister{} at the buildHostServer call below; ClearLog→Purge
	// wiring follows in the next slice.

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
	absPathMetadata, err := housekeeping.NewCleanupAbsolutePathMetadataFiles(db, logger)
	if err != nil {
		return fmt.Errorf("housekeeping absPathMetadata: %w", err)
	}
	dcuPending, err := housekeeping.NewCleanupDownloadClientUnavailablePendingReleases(db, logger)
	if err != nil {
		return fmt.Errorf("housekeeping dcuPending: %w", err)
	}
	duplicateMetadata, err := housekeeping.NewCleanupDuplicateMetadataFiles(db, logger)
	if err != nil {
		return fmt.Errorf("housekeeping duplicateMetadata: %w", err)
	}
	tempUpdate, err := housekeeping.NewCleanupTemporaryUpdateFiles(folder, nil, logger)
	if err != nil {
		return fmt.Errorf("housekeeping tempUpdate: %w", err)
	}
	commandQueueCleanup, err := housekeeping.NewCleanupCommandQueue(cmdSvc, logger)
	if err != nil {
		return fmt.Errorf("housekeeping commandQueueCleanup: %w", err)
	}
	extraFilesRepo, err := extrafiles.NewRepo(db)
	if err != nil {
		return fmt.Errorf("housekeeping extrafiles repo: %w", err)
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
	extraFilesExcluded, err := housekeeping.NewCleanupExtraFilesInExcludedFolders(
		extraFilesRepo,
		seriesRepo,
		func(base string, paths []string) ([]string, error) {
			return mediafiles.FilterPaths(base, paths, true)
		},
		logger,
	)
	if err != nil {
		return fmt.Errorf("housekeeping extraFilesExcluded: %w", err)
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
	// houseSvc construction is deferred to after configProv +
	// metadataFilesRepo are built (slice 5.J134) — the new
	// DeleteBadMediaCovers task needs both, and housekeeping.Service
	// takes an immutable task slice at construction. The individual
	// Task variables declared above remain in scope through the rest
	// of serve() and get folded into the final NewService([]) call
	// below.
	var houseSvc *housekeeping.Service

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
	// Slice 3.AC: wire the two episodefiles.Service deferred-dep
	// setters that populate per-event payload fields. Without these,
	// EpisodeFileAddedEvent.LinkedEpisodes / EpisodeFileDeletedEvent
	// .{LinkedEpisodes,Series} are left nil — the slice-3.AA
	// subscribers (episodes.HandleEpisodeFileAdded /
	// HandleEpisodeFileDeleted) iterate the nil slice and silently
	// no-op. Wiring closes the loop so the subscribers actually
	// process linked episodes.
	//
	//   - SetEpisodeProvider: *episodes.Service satisfies
	//     EpisodeProvider via GetEpisodesByFileID(ctx, fileID).
	//   - SetSeriesProvider: *series.Service satisfies
	//     SeriesProvider via GetSeries(ctx, id).
	episodeFilesSvc.SetEpisodeProvider(episodesSvc)
	episodeFilesSvc.SetSeriesProvider(seriesSvc)
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
	// Wire the AutoUnmonitorPreviouslyDownloadedEpisodes config flag
	// into episodes.Service. Without this setter, the slice-3.AA
	// HandleEpisodeFileDeleted path defaults autoUnmonitor=false and
	// the user's config toggle is silently ignored. *config.Provider
	// satisfies episodes.AutoUnmonitorMissingProvider via Go's
	// structural typing on AutoUnmonitorPreviouslyDownloadedEpisodes()
	// bool.
	episodesSvc.SetAutoUnmonitorMissingProvider(configProv)

	// Audit fix B.9: auth.Service for Forms-mode login. Backs the
	// Users table (read by login + cookie-validate paths). Wired
	// here so the buildHostServer call below can route /login + the
	// session-cookie middleware against it.
	usersRepo, err := users.NewRepo(db)
	if err != nil {
		return fmt.Errorf("users repo: %w", err)
	}
	authSvc := auth.NewService(usersRepo)

	// CustomFilters service (slice 4.DO — first wiring; the package
	// existed but nothing consumed it until the /api/v3/customfilter
	// route landed). No seed pass; the table starts empty and the
	// frontend populates it as users save filters.
	customFiltersRepo, err := customfilters.NewRepo(db)
	if err != nil {
		return fmt.Errorf("customfilters repo: %w", err)
	}
	customFiltersSvc := customfilters.NewService(customFiltersRepo, bus)

	// QualityDefinitions service (slice 4.DJ — first read-side
	// surface needed by GET /api/v3/qualitydefinition). C# wires
	// this on app start via the IExecute<...Command> handler
	// (`QualityDefinitionService.Handle(ApplicationStartedEvent)`);
	// the Go port calls EnsureDefaults inline here so the table is
	// seeded before any route serves it. Idempotent — re-running on
	// each boot is the same code path C# uses.
	qualityDefRepo, err := qualitydefinitions.NewRepo(db)
	if err != nil {
		return fmt.Errorf("qualitydefinitions repo: %w", err)
	}
	qualityDefSvc := qualitydefinitions.NewService(qualityDefRepo)
	if err := qualityDefSvc.EnsureDefaults(ctx); err != nil {
		return fmt.Errorf("qualitydefinitions ensureDefaults: %w", err)
	}

	// PendingReleases repo (slice 4.EB — first wiring; existed but
	// nothing consumed it until the /api/v3/queue/status pending
	// count needed a real source). The full pendingreleases.Service
	// has many dependencies (parsingservice, indexers, etc.) — this
	// slice only needs Repo.Count for the queue summary, so the
	// fuller Service wiring stays DEFERRED.
	pendingReleasesRepo, err := pendingreleases.NewRepo(db)
	if err != nil {
		return fmt.Errorf("pendingreleases repo: %w", err)
	}
	// J484/J486 (Delay/Pending substrate): construct the full
	// pendingreleases.Service here (early) so its three consumers can
	// all reference it: the grab pipeline's pending-bucket persistence
	// (SetPendingWriter, J484), the EpisodeGrabbed/SeriesDeleted
	// maintenance handlers (J485), and the DelaySpecification
	// OldestPending age-bypass (J486, via decisionEngineDeps which is
	// built before the grabber). Previously only the Repo was wired
	// (queue/status count); the Service AddMany/remove paths were
	// DEFERRED.
	pendingReleasesSvc, err := pendingreleases.NewService(pendingReleasesRepo, bus, logger)
	if err != nil {
		return fmt.Errorf("pendingreleases service: %w", err)
	}
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

	// Slice 5.J185 wires the SeriesAddedEvent folder-creation
	// subscriber against the in-process bus. Mirrors the C# chain
	// `SeriesAddedEvent` → `SeriesAddedHandler` → `RefreshSeriesCommand`
	// → `DiskScanService.Scan` → `if (!seriesFolderExists &&
	// CreateEmptySeriesFolders && !DeleteEmptyFolders) CreateFolder`
	// at `DiskScanService.cs:102-117`. The Go-port short-circuits the
	// chain (no RefreshSeries port — that's BLOCKED on SkyHook M3)
	// so the folder-creation arm fires directly off the
	// SeriesAddedEvent now published by `*series.Service.AddSeries`
	// (the J185-upgraded seriesAdderAdapter calls it per-row from
	// the ImportListSync flow). Gated by the same two config
	// settings the C# arm uses; default-off, so existing user DBs
	// see no behaviour change until they explicitly enable
	// CreateEmptySeriesFolders.
	subscribeSeriesAddedFolderCreate(bus, configProv, diskProv, logger)
	// GAP2 (J496): prune now-empty season/series subfolders after an
	// episode file is deleted (config DeleteEmptyFolders, reason ≠
	// MissingFromDisk). Ports MediaFileDeletionService.Handle(
	// EpisodeFileDeletedEvent) — the file-delete sibling of the
	// rename-path RemoveEmptySubfolders. Default-off; OrderLast.
	subscribeMediaFileDeletedFolderCleanup(bus, configProv, diskProv, logger)
	// Slice 3.AD: rootFoldersRepo construction relocated up here so
	// the mediafiles.NewService call below can take a real
	// [rootFolderHealthResolverAdapter] (slice 3.A8 — same shape as
	// mediafiles.RootFolderResolver via Go's structural typing) in
	// place of the slice-3.52 stubRootFolderResolver. The real
	// adapter resolves against the rootFolders table via Repo.All +
	// the package-level rootfolders.BestPath free fn — same dep-gap
	// dodge as the slice-3.96 rootFolderFinderAdapter (full
	// *rootfolders.Service still requires disk.Provider.FolderWritable
	// + GetTotalSize that haven't ported — FAIL-CLOSED #8 territory).
	// Without this swap, mediafiles.Service.RescanSeries took every
	// series.Path as its own root and short-circuited to
	// RootFolderDoesNotExist on the first FolderExists check; with
	// the real resolver, RescanSeries finds the configured root and
	// proceeds to the FolderExists / FolderEmpty checks against the
	// real *disk.Provider. The slice-3.A8 adapter (defined in
	// healthcheck_wiring.go) is reused verbatim.
	rootFoldersRepo, err := rootfolders.NewRepo(db)
	if err != nil {
		return fmt.Errorf("rootfolders repo: %w", err)
	}
	// Slice 3.AM: construct the real *rootfolders.Service. With
	// disk.Provider.FolderWritable + GetTotalSize ported (this slice),
	// every rootfolders.NewService dep is now real:
	//
	//   - Repo            → rootFoldersRepo (above)
	//   - DiskProvider    → *disk.Provider via Go structural typing on
	//                       FolderExists / FolderWritable / FolderEmpty /
	//                       GetAvailableSpace / GetTotalSize / GetDirectories
	//   - SeriesPaths     → *series.Service.AllSeriesPaths
	//   - NamingConfig    → seriesFolderFormatAdapter (thin wrapper
	//                       around *namingconfig.Service.GetConfig that
	//                       returns just the SeriesFolderFormat field)
	//
	// Replaces the two slice-3.96 / 3.A8 adapter dodges
	// (rootFolderHealthResolverAdapter / rootFolderFinderAdapter) that
	// talked directly to *rootfolders.Repo + the package-level
	// rootfolders.BestPath free fn — both adapters now pass through
	// the real Service so the per-path GetBestRootFolderPath cache +
	// future write-side validation reach production code paths.
	rootFoldersSvc, err := rootfolders.NewService(
		rootFoldersRepo,
		diskProv,
		seriesSvc,
		seriesFolderFormatAdapter{svc: namingConfigSvc},
	)
	if err != nil {
		return fmt.Errorf("rootfolders service: %w", err)
	}

	// RemotePathMappings service (slice 4.DP — first wiring; the
	// package existed but nothing consumed it until the
	// /api/v3/remotepathmapping route landed). DiskProvider here is
	// the same disk.Provider used by rootfolders; the service's
	// LocalPath-FolderExists validation runs only on write paths,
	// so the read-only handler shipped this slice doesn't exercise
	// it.
	remotePathMappingsRepo, err := remotepathmappings.NewRepo(db)
	if err != nil {
		return fmt.Errorf("remotepathmappings repo: %w", err)
	}
	remotePathMappingsSvc, err := remotepathmappings.NewService(remotePathMappingsRepo, diskProv, bus)
	if err != nil {
		return fmt.Errorf("remotepathmappings service: %w", err)
	}
	// Slice 3.AE relocated mediafiles.NewService construction down to
	// after importApprovedSvc + importDecisionMaker so the real
	// mediafilesImportDecisionAdapter / mediafilesImportApprovedAdapter
	// (mediafiles_wiring.go) can be wired in place of
	// stubImportDecisionMaker / stubImportApprovedEpisodes. The deps
	// constructed in this block (diskProv + rootFoldersRepo) are
	// captured by the relocated NewService call.

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
	// Slice 3.AH: scenemappings.Service constructed and wired into
	// parsingservice via [sceneMappingFinderAdapter] (parsingservice_wiring.go).
	// The adapter bridges *scenemappings.Service.FindSceneMapping +
	// GetSceneSeasonNumber (ctx + error returns) to the narrow
	// [parsingservice.SceneMappingFinder] (ctx-less, errorless,
	// nil-on-miss) — same dep-gap dodge pattern as
	// historyGrabsFinderAdapter (slice 3.AG) and latestGrabFinderAdapter
	// (slice 3.AF). Without this swap parsingservice.Service used
	// the noopSceneMappingFinder default which always returned nil
	// from both methods, defeating any scene-numbering / scene-name
	// resolution path the parser walks.
	scenemappingsRepo, err := scenemappings.NewRepo(db)
	if err != nil {
		return fmt.Errorf("scenemappings repo: %w", err)
	}
	// Slice 4.AY (sub-arc M6b-i): the Service.UpdateMappings refresh
	// path needs a writer + (optional) bus + (optional) providers
	// chained on top of the read-only base. Writer + bus are
	// available here; the SceneMappingProxy comes later (after
	// metadataSourceHTTP is constructed around the metadatasource
	// proxy block). The slice 4.AZ wiring below calls
	// `.WithProviders(scenemappingsProxy)` once that exists, leaving
	// UpdateMappings a no-op + warn until the chain completes.
	scenemappingsSvc := scenemappings.NewService(scenemappingsRepo, logger).
		WithWriter(scenemappingsRepo).
		WithBus(bus)
	if scenemappingsSvc == nil {
		return fmt.Errorf("scenemappings service: nil — required dep regressed")
	}
	parsingSvc, err := parsingservice.NewService(episodesSvc, nil,
		sceneMappingFinderAdapter{svc: scenemappingsSvc, log: logger},
		seriesSvc, logger)
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
	// Slice 3.AF: downloadhistory.Service constructed for the
	// AugmentQualityFromReleaseName augmenter's [LatestGrabFinder]
	// dep. First production binding of *downloadhistory.Service for
	// non-import-orchestrator use — the slice-3.62 history.Service
	// covers EpisodeHistory; this covers DownloadHistory. The
	// [latestGrabFinderAdapter] (cmd/sonarr/episodeimport_wiring.go)
	// bridges the (ctx, error) → ctx-less narrow interface.
	dlHistoryRepo, err := downloadhistory.NewRepo(db)
	if err != nil {
		return fmt.Errorf("downloadhistory repo: %w", err)
	}
	dlHistorySvc := downloadhistory.NewService(dlHistoryRepo)
	if dlHistorySvc == nil {
		return fmt.Errorf("downloadhistory service: nil — required dep regressed")
	}
	// Slice 3.AG: history.Service relocated up here (was originally
	// constructed in slice 3.A1 alongside the import path) so the
	// new [historyGrabsFinderAdapter] feeding AggregateReleaseInfo
	// reaches it at aggregator construction. historySvc is reused
	// further down by the slice-3.A1 historyGrabFinderForImportAdapter.
	// Slice-3.62 history.Service is read-only; the seven IHandle<...>
	// event-bus subscribers it has in C# are deferred until each
	// event-shape ports.
	historyRepo, err := history.NewRepo(db)
	if err != nil {
		return fmt.Errorf("history repo: %w", err)
	}
	importListExclusionsRepo, err := importlistexclusions.NewRepo(db)
	if err != nil {
		return fmt.Errorf("importlistexclusions repo: %w", err)
	}
	historySvc := history.NewService(historyRepo)
	if historySvc == nil {
		return fmt.Errorf("history service: nil — required dep regressed")
	}

	// Slice 5.E4: failed-downloads service wraps history.Get + bus
	// publish so the POST /api/v3/history/failed/{id} handler can
	// route through the same DownloadFailedEvent the auto-fail path
	// uses. Stateless; the v3 handler holds the only consumer.
	failedDownloadsSvc, err := faileddownloads.NewService(historyRepo, bus)
	if err != nil {
		return fmt.Errorf("faileddownloads service: %w", err)
	}

	// Slice 3.AT: history IHandle<EpisodeGrabbedEvent> subscriber.
	// Builds a per-episode history.Entry from the grabbed Subject's
	// release metadata + per-episode IDs and routes through
	// history.Service.Add. The publisher (Phase 4 download
	// orchestrator) hasn't ported yet, so this is "lit but no
	// listeners on the publisher side" — the bus dispatch path is
	// exercised at boot so the next subsystem that publishes an
	// EpisodeGrabbedEvent gets the audit-log row written without
	// further wiring at this site.
	messaging.Subscribe(bus, "history.HandleEpisodeGrabbed",
		func(ctx context.Context, ev decisionengine.EpisodeGrabbedEvent) error {
			return historyhandlers.HandleEpisodeGrabbed(ctx, historySvc, &ev)
		})

	// Slice 5.J136: downloadhistory IHandle<EpisodeGrabbedEvent>
	// subscriber. Mirrors the historyhandlers fan-out exactly — both
	// subscribers consume the same event and write to their own
	// audit-log tables (History vs DownloadHistory). The
	// downloadhistory variant gates on non-empty DownloadID and writes
	// one row per event (not per-episode — DownloadHistory is keyed
	// at the lifecycle granularity).
	messaging.Subscribe(bus, "downloadhistory.HandleEpisodeGrabbed",
		func(ctx context.Context, ev decisionengine.EpisodeGrabbedEvent) error {
			return downloadhistoryhandlers.HandleEpisodeGrabbed(ctx, dlHistorySvc, &ev)
		})

	// Slice 3.AU: history IHandle<EpisodeImportedEvent> subscriber.
	// Builds a per-episode history.Entry from the imported file +
	// LocalEpisode and routes through history.Service.Add. Unlike
	// EpisodeGrabbedEvent (still listener-only), the producer here
	// IS in-tree — episodeimport.ImportApprovedEpisodes publishes
	// EpisodeImportedEvent at the end of every successful import —
	// so this subscriber actively writes audit rows for new-download
	// imports. The handler gates on NewDownload to skip rescans /
	// manual imports (matches C# `HistoryService.cs:198-201`).
	//
	// Slice 5.J128: the J127 FindDownloadId backfill is wired here via
	// WithDownloadIDLookup(historySvc). When an EpisodeImportedEvent
	// carries a whitespace-empty DownloadID — manual / library-rescan
	// imports of files Sonarr originally grabbed — the handler
	// consults historySvc.FindDownloadID to recover a still-downloading
	// grab from prior Grabbed history rows, preserving the grab→import
	// audit-log link that the C# tree maintains at
	// `HistoryService.cs:203-208`.
	messaging.Subscribe(bus, "history.HandleEpisodeImported",
		func(ctx context.Context, ev episodeimport.EpisodeImportedEvent) error {
			return historyhandlers.HandleEpisodeImported(ctx, historySvc, &ev,
				historyhandlers.WithDownloadIDLookup(historySvc))
		})

	// Slice 5.J137: downloadhistory IHandle<EpisodeImportedEvent>
	// subscriber. Mirrors the historyhandlers twin (same NewDownload
	// gate, same FindDownloadID fallback wired through *history.Service
	// from J127, same downstream-recoverable-error posture). Writes a
	// single FileImported row per event — DownloadHistory is keyed at
	// lifecycle granularity, not per-episode.
	messaging.Subscribe(bus, "downloadhistory.HandleEpisodeImported",
		func(ctx context.Context, ev episodeimport.EpisodeImportedEvent) error {
			return downloadhistoryhandlers.HandleEpisodeImported(ctx, dlHistorySvc, &ev,
				downloadhistoryhandlers.WithDownloadIDLookup(historySvc))
		})

	// Slice 3.AV: history IHandle<DownloadFailedEvent> subscriber.
	// Walks ev.EpisodeIDs and writes one history.Entry per id with
	// the failure metadata. Producer (FailedDownloadService) is
	// Phase 4 — this is "lit but no listeners on the publisher
	// side" pending Phase-4. The Data-fallback chain (ReleaseGroup /
	// Size / Indexer pulled from TrackedDownload first, then ev.Data
	// lowercase keys) preserves audit-log shape across both
	// orchestrator-driven and manual-failure paths.
	messaging.Subscribe(bus, "history.HandleDownloadFailed",
		func(ctx context.Context, ev trackeddownloads.DownloadFailedEvent) error {
			return historyhandlers.HandleDownloadFailed(ctx, historySvc, &ev)
		})

	// Slice 5.J138: downloadhistory IHandle<DownloadFailedEvent>
	// subscriber. Single DownloadFailed row per event, gated on
	// non-nil TrackedDownload (the C# tree at :189-192 returns early —
	// failed downloads without a tracked record have no Protocol /
	// DownloadClientID to attribute).
	messaging.Subscribe(bus, "downloadhistory.HandleDownloadFailed",
		func(ctx context.Context, ev trackeddownloads.DownloadFailedEvent) error {
			return downloadhistoryhandlers.HandleDownloadFailed(ctx, dlHistorySvc, &ev)
		})

	// Slice 3.AW: history IHandle<EpisodeFileDeletedEvent> subscriber.
	// Walks ev.LinkedEpisodes and writes one history.Entry per id
	// with the deletion metadata. Skips the NoLinkedEpisodes /
	// ManualOverride reasons (housekeeping cleanup + force-import
	// swaps don't earn audit rows per C# `:269-278`). Independent
	// of the existing episodes.HandleEpisodeFileDeleted subscriber
	// at line 437 — both fire on the same event, no ordering
	// dependency.
	messaging.Subscribe(bus, "history.HandleEpisodeFileDeleted",
		func(ctx context.Context, ev episodefiles.EpisodeFileDeletedEvent) error {
			return historyhandlers.HandleEpisodeFileDeleted(ctx, historySvc, &ev)
		})

	// Slice 3.AX: history IHandle<EpisodeFileRenamedEvent> subscriber.
	// Looks up linked episodes via episodesSvc.GetEpisodesByFileID
	// (the Go-port substitute for C# `EpisodeFile.Episodes.Value`
	// lazy-load) and writes one history.Entry per linked episode.
	// SourceTitle = ev.OriginalPath; the new path lives in
	// Data["Path"] so the v3 history search hits both pre- and
	// post-rename names.
	messaging.Subscribe(bus, "history.HandleEpisodeFileRenamed",
		func(ctx context.Context, ev episodefiles.EpisodeFileRenamedEvent) error {
			return historyhandlers.HandleEpisodeFileRenamed(ctx, historySvc, episodesSvc, &ev)
		})

	// Slice 3.AY: history IHandle<SeriesDeletedEvent> subscriber.
	// DELETES audit-log rows for the removed series rather than
	// inserting (per C# `:368-371`). The History.SeriesId column is
	// not declared as a foreign key, so the cascade is application-
	// side. Independent of the trackeddownloads / series-mutation
	// SeriesDeleted subscribers — fan-out, no ordering dependency.
	messaging.Subscribe(bus, "history.HandleSeriesDeleted",
		func(ctx context.Context, ev series.SeriesDeletedEvent) error {
			return historyhandlers.HandleSeriesDeleted(ctx, historySvc, &ev)
		})

	// Slice 5.J135: downloadhistory IHandle<SeriesDeletedEvent>
	// subscriber. Mirrors the historyhandlers fan-out exactly — the
	// DownloadHistory.SeriesId column is also not a FK in the schema,
	// so the cleanup is application-side. Runs side-by-side with the
	// historyhandlers subscriber above; no ordering dependency.
	messaging.Subscribe(bus, "downloadhistory.HandleSeriesDeleted",
		func(ctx context.Context, ev series.SeriesDeletedEvent) error {
			return downloadhistoryhandlers.HandleSeriesDeleted(ctx, dlHistorySvc, &ev)
		})

	// Slice 3.AZ: history IHandle<DownloadIgnoredEvent> subscriber.
	// Walks ev.EpisodeIDs and writes one history.Entry per id with
	// the ignore metadata. Producer is Phase 4
	// (FailedDownloadService / manual-ignore UI action), so this is
	// listener-only on the publisher side until Phase 4 ports.
	// Closes queue [9] entirely (the seven IHandle subscribers from
	// HistoryService are now all ported).
	messaging.Subscribe(bus, "history.HandleDownloadIgnored",
		func(ctx context.Context, ev trackeddownloads.DownloadIgnoredEvent) error {
			return historyhandlers.HandleDownloadIgnored(ctx, historySvc, &ev)
		})

	// Slice 5.J139: downloadhistory IHandle<DownloadIgnoredEvent>
	// subscriber. Single DownloadIgnored row per event. Protocol +
	// DownloadClientID sourced from ev.DownloadClientInfo (not from
	// TrackedDownload, unlike the Failed handler).
	messaging.Subscribe(bus, "downloadhistory.HandleDownloadIgnored",
		func(ctx context.Context, ev trackeddownloads.DownloadIgnoredEvent) error {
			return downloadhistoryhandlers.HandleDownloadIgnored(ctx, dlHistorySvc, &ev)
		})

	// Slice 5.J140: downloadhistory IHandle<DownloadCompletedEvent>
	// subscriber. Closes the DownloadHistoryService event-bus arc at
	// 6/6. Single DownloadImported row per event (distinct from the
	// FileImported ordinal that J137 emits). Producer
	// (CompletedDownloadService) is Phase-4 territory — lit but no
	// listeners on the publisher side until that ports.
	messaging.Subscribe(bus, "downloadhistory.HandleDownloadCompleted",
		func(ctx context.Context, ev trackeddownloads.DownloadCompletedEvent) error {
			return downloadhistoryhandlers.HandleDownloadCompleted(ctx, dlHistorySvc, &ev)
		})

	// Per-augmenter wiring (Phase 3.88 + 3.AF). Slice 3.AF lights up
	// the previously-empty AggregateLanguage + AggregateQuality
	// augmenter lists with every available augmenter. Both
	// aggregators sort augmenters by Order() at construction; the
	// orchestrator picks the highest-confidence verdict per
	// component (source / resolution / revision for quality;
	// languages list for language).
	//
	// Language augmenters (4):
	//   - AugmentLanguageFromFileName  (Order=1) — parses the
	//     local file path's stem.
	//   - AugmentLanguageFromFolder    (Order=2) — parses the
	//     containing folder name.
	//   - AugmentLanguageFromDownloadClientItem (Order=3) — parses
	//     the download-client-reported title (NZB/torrent name).
	//   - AugmentLanguageFromMediaInfo (Order=4) — walks the
	//     MediaInfo AudioStream language tags via [language.FindIso]
	//     (default closure).
	//
	// Quality augmenters (5):
	//   - AugmentQualityFromFileName            (Order=1)
	//   - AugmentQualityFromFolder              (Order=2)
	//   - AugmentQualityFromDownloadClientItem  (Order=3)
	//   - AugmentQualityFromMediaInfo           (Order=4) —
	//     resolution + source from MediaInfo; nil parser →
	//     [quality.ParseQualityName].
	//   - AugmentQualityFromReleaseName         (Order=5) —
	//     looks up the latest Grabbed history entry for the
	//     download via [latestGrabFinderAdapter] + parses its
	//     SourceTitle.
	//
	// Skipped (still no port):
	//   - AggregateReleaseInfo — needs a HistoryGrabsFinder; no
	//     Go port of the history grabs surface yet (different from
	//     downloadhistory).
	episodeAggregators := []episodeimport.LocalEpisodeAggregator{
		episodeimport.NewAggregateLanguage([]episodeimport.LanguageAugmenter{
			episodeimport.AugmentLanguageFromFileName{},
			episodeimport.AugmentLanguageFromFolder{},
			episodeimport.AugmentLanguageFromDownloadClientItem{},
			episodeimport.NewAugmentLanguageFromMediaInfo(nil),
		}, nil, logger),
		episodeimport.NewAggregateQuality([]episodeimport.QualityAugmenter{
			episodeimport.AugmentQualityFromFileName{},
			episodeimport.AugmentQualityFromFolder{},
			episodeimport.AugmentQualityFromDownloadClientItem{},
			episodeimport.NewAugmentQualityFromMediaInfo(logger, nil),
			episodeimport.NewAugmentQualityFromReleaseName(
				latestGrabFinderAdapter{svc: dlHistorySvc, log: logger}, nil),
		}, logger),
		// Slice 3.AG: AggregateReleaseInfo wired via the new
		// historyGrabsFinderAdapter (episodeimport_wiring.go) over
		// *history.Service.Find with EventTypeGrabbed. Mirrors C#
		// `AggregateReleaseInfo.cs:9-40`. With nil DownloadItem,
		// empty DownloadID, or zero Grabbed-history rows the
		// aggregator skips populating LocalEpisode.Release. The
		// finder constructor returns nil if the dep is nil; we
		// pass historySvc directly which is non-nil at this site.
		episodeimport.NewAggregateReleaseInfo(historyGrabsFinderAdapter{svc: historySvc}, logger),
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
		// J471: AggregateReleaseGroup + AggregateReleaseHash were ported
		// (aggregation.go) but never added to this chain, so imported files
		// got an EMPTY ReleaseGroup + ReleaseHash (LocalEpisode.ReleaseGroup
		// is set nowhere else; local_episode_to_file.go:73 copies it onto the
		// EpisodeFile). That broke the `{Release Group}` naming token, the
		// release-group UI column/filter, and release-group custom formats.
		// Both are zero-dependency aggregators reading the already-parsed
		// File/Folder/DownloadClient episode infos. Ports
		// Aggregators/AggregateReleaseGroup.cs + AggregateReleaseHash.cs.
		episodeimport.AggregateReleaseGroup{},
		episodeimport.AggregateReleaseHash{},
	}
	// Slice 3.AG: AggregationService.cfg arg now wires *config.Provider
	// (EnableMediaInfo getter) so user toggles via the Config table
	// take effect on the rescan-MediaInfo branch. The video arg
	// stays nil — VideoMediaInfoReader has no production impl yet
	// (gated on a full MediaInfo reader port).
	aggregationSvc, err := episodeimport.NewAggregationService(episodeAggregators, diskProv, nil, configProv, logger)
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
	// J353 (Post-J352 residual-arc, Arc 1 slice 1): AlreadyImportedSpecification
	// wired via Go's structural typing — *history.Service satisfies the
	// episodeimport.ImportHistoryFinder narrow interface
	// (FindByEpisodeId(ctx, id) []history.Entry — the lowercase-'d'
	// spelling preserved for C# grep parity). Spec mirrors C#
	// `AlreadyImportedSpecification.cs:11-79`: rejects a
	// download-client-driven candidate whose episodes already have an
	// on-disk file imported from the SAME download id after the last
	// grab of that download id (re-processing would double-import).
	// Skip arms — nil download item / episode-without-file /
	// no-prior-import / re-grabbed-after-import → Accept. Lookup errors
	// translate to Accept + Warn-log (defensive idiom). Errors out only
	// on nil finder — historySvc is non-nil here so the error arm is
	// belt-and-braces.
	alreadyImportedSpec, err := episodeimport.NewAlreadyImportedSpecification(historySvc, logger)
	if err != nil {
		return fmt.Errorf("already-imported spec: %w", err)
	}
	importDecisionSpecs = append(importDecisionSpecs, alreadyImportedSpec)
	// J353 (Post-J352 residual-arc, Arc 1 slice 1): MatchesFolderSpecification
	// wired via Go's structural typing — *parsingservice.Service
	// satisfies the episodeimport.FolderMatchParsingService narrow
	// interface (ParseSpecialEpisodeTitle + GetEpisodes). Spec mirrors
	// C# `MatchesFolderSpecification.cs:12-86`: rejects a candidate whose
	// file-name parse maps to episodes the containing folder name did
	// NOT promise (a sign of a misfiled file). Skip arms — ExistingFile /
	// nil file-or-folder info (after the scene-season-special re-parse) /
	// folder maps to zero episodes → Accept. Lookup/parse errors
	// translate to Accept + Warn-log. Errors out only on nil parsing
	// service — parsingSvc is non-nil here so the error arm is
	// belt-and-braces. This completes the 14-spec ImportDecisionMaker
	// Specification set (only UpgradeSpecification remains
	// ported-but-unwired pending its ProfileView-per-batch caching layer).
	matchesFolderSpec, err := episodeimport.NewMatchesFolderSpecification(parsingSvc, logger)
	if err != nil {
		return fmt.Errorf("matches-folder spec: %w", err)
	}
	importDecisionSpecs = append(importDecisionSpecs, matchesFolderSpec)
	// Slice 3.AI: trackeddownloads.Service constructed here for two
	// downstream consumers — the ImportDecisionMaker `tracked` arg
	// (FindIndexerFlags for IndexerFlags enrichment per
	// ImportDecisionMaker.cs:153) and the slice-3.80
	// DownloadedEpisodesScan handler `Finder` dep (Find for the
	// known-tracked-download fast path at downloadedepisodesscan.go:50).
	// The cache starts empty — Track is invoked by future Phase 4
	// download-client integration; until then both consumers see
	// the same zero-result behaviour they had with nil deps, but
	// the wiring is now ready for the cache to fill.
	trackedDownloadsSvc := trackeddownloads.NewService(logger)
	if trackedDownloadsSvc == nil {
		return fmt.Errorf("trackeddownloads service: nil — required dep regressed")
	}
	// Slice 3.AN: relocate qualityProfilesSvc + customFormatsSvc
	// construction up here (was at the post-IDM block) so the new
	// LocalEpisodeFormatCalculator can be threaded into IDM's
	// `formats` arg. qpRepo + cfRepo were already constructed early
	// for housekeeping; this block reuses both repos and bus and
	// wires the slice-3.A9 cross-package event-handler subscriptions
	// (CustomFormatAddedEvent / CustomFormatDeletedEvent → qualityprofiles.Service).
	qualityProfilesSvc, err := qualityprofiles.NewService(qpRepo, bus)
	if err != nil {
		return fmt.Errorf("qualityprofiles service: %w", err)
	}
	customFormatsSvc := customformats.NewService(cfRepo, bus)
	if customFormatsSvc == nil {
		return fmt.Errorf("customformats service: nil — required dep regressed")
	}
	// Slice 4.EE: bind the FormatProvider hook so SeedDefaults can
	// populate every default profile's FormatItems list against the
	// known CustomFormat set. Mirrors C# `QualityProfileService`'s
	// constructor-injected `ICustomFormatService`.
	qualityProfilesSvc.SetFormatProvider(customFormatsSvc)
	// Seed the six default profiles (Any / SD / HD-720p / HD-1080p /
	// Ultra-HD / HD - 720p/1080p) on a fresh DB. No-op when rows
	// already exist (the C# `if (All().Any()) return;` guard ports
	// verbatim). Mirrors C# `QualityProfileService.Handle(
	// ApplicationStartedEvent)` at `QualityProfileService.cs:91-157`.
	if err := qualityProfilesSvc.SeedDefaults(ctx); err != nil {
		return fmt.Errorf("qualityprofiles seed defaults: %w", err)
	}
	messaging.Subscribe(bus, "qualityprofiles.OnCustomFormatAdded",
		func(ctx context.Context, ev customformats.CustomFormatAddedEvent) error {
			return qualityProfilesSvc.OnCustomFormatAdded(ctx, ev.Format)
		})
	messaging.Subscribe(bus, "qualityprofiles.OnCustomFormatDeleted",
		func(ctx context.Context, ev customformats.CustomFormatDeletedEvent) error {
			return qualityProfilesSvc.OnCustomFormatDeleted(ctx, ev.Format)
		})
	// Slice 3.AN: construct *episodeimport.LocalEpisodeFormatCalculator
	// (already ported as the EpisodeCustomFormatCalculator interface
	// satisfier) and thread into the IDM `formats` arg below. Closes
	// queue [7] sub-arc 7a — the per-LocalEpisode CustomFormat
	// resolution flow now runs during ImportDecisionMaker.GetImportDecisions
	// instead of being silently skipped.
	localFormatCalculator, err := episodeimport.NewLocalEpisodeFormatCalculator(
		localEpisodeFileNameBuilderAdapter{svc: namingConfigSvc, log: logger},
		localEpisodeFormatParserAdapter{svc: customFormatsSvc, log: logger},
		localEpisodeFormatScorerAdapter{series: seriesSvc, qp: qualityProfilesSvc, log: logger},
		logger,
	)
	if err != nil {
		return fmt.Errorf("local-episode format calculator: %w", err)
	}
	// J354 (Post-J352 residual-arc, Arc 1 slice 2): UpgradeSpecification
	// production wiring — the spec was ported + unit-tested but its four
	// narrow-interface deps had no production bindings, so it was absent
	// from importDecisionSpecs. Wired here (rather than at the spec-list
	// literal above) because two of its deps — qualityProfilesSvc +
	// customFormatsSvc — aren't constructed until this block. Three J354
	// adapters (see import_upgrade_spec_wiring.go) fill the gap:
	// configProv satisfies UpgradeConfig (DownloadPropersAndRepacks)
	// directly; upgradeProfileViewBySeriesAdapter resolves series →
	// QualityProfileID → decisionengine.ProfileView;
	// upgradeEpisodeFileResolverAdapter wraps episodeFilesSvc.GetFile
	// (ErrNotFound → nil-row contract); upgradeFormatCalculatorAdapter
	// resolves the file's series and runs
	// customformats.ParseCustomFormatForFile. Spec mirrors C#
	// `Specifications/UpgradeSpecification.cs:13-104`: per existing
	// EpisodeFile on disk, reject NotQualityUpgrade / NotRevisionUpgrade /
	// NotCustomFormatUpgrade(AfterRename) when the candidate doesn't
	// improve on the file already present. Fresh imports (every episode
	// EpisodeFileID nil/0) skip every arm → Accept, so this only affects
	// upgrade scenarios. Lookup errors translate to Accept + Warn-log.
	// Errors out only on nil deps — all four are non-nil here so the
	// error arm is belt-and-braces. This brings the production spec list
	// to 13 of 14 wired (only the J346-deferred LocalEpisode-driven
	// file-size/mediainfo CF specs remain — a separate sub-arc).
	upgradeSpec, err := episodeimport.NewUpgradeSpecification(
		configProv,
		upgradeProfileViewBySeriesAdapter{series: seriesSvc, qp: qualityProfilesSvc},
		upgradeEpisodeFileResolverAdapter{svc: episodeFilesSvc},
		upgradeFormatCalculatorAdapter{series: seriesSvc, cf: customFormatsSvc},
		logger,
	)
	if err != nil {
		return fmt.Errorf("upgrade spec: %w", err)
	}
	importDecisionSpecs = append(importDecisionSpecs, upgradeSpec)
	importDecisionMaker, err := episodeimport.NewImportDecisionMaker(
		importDecisionSpecs,   // specs — slice-3.86: 5 zero-dep specs wired
		episodeFilesSvc,       // filter — FilterExistingFiles
		aggregationSvc,        // aggregator — slice-3.60 AggregationService (empty aggregators above)
		diskProv,              // disk — GetFileSize
		sampleDetector,        // sampler — slice-3.55 + 3.84 SampleDetector
		nil,                   // pathParser — nil → defaults to parser.ParsePath
		trackedDownloadsSvc,   // tracked — slice-3.AI: trackeddownloads.Service.FindIndexerFlags
		localFormatCalculator, // formats — slice-3.AN: LocalEpisodeFormatCalculator
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
	// Slice 3.AD relocated rootFoldersRepo construction up to the
	// mediafiles.NewService site so the real resolver could be wired
	// there. rootFoldersRepo is reused below.

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

	// Slice 3.BG (queue [12]): seriesSvc.SetSeriesPathBuilder lights
	// up the bulk-update path-rebuild arm at series.Service.UpdateMany.
	// Mirrors the C# `SeriesPathBuilder` injection at
	// `Tv/SeriesPathBuilder.cs:21-26`. The adapter composes
	// organizer.GetSeriesFolder + rootFoldersSvc.GetBestRootFolderPath
	// + namingConfigSvc.GetConfig under the
	// [series.SeriesPathBuilder] interface; series.Service stops
	// short-circuiting the per-row path rebuild now that the dep is
	// wired (matches C# behaviour at SeriesService.cs:244-252).
	seriesSvc.SetSeriesPathBuilder(seriesPathBuilderAdapter{
		naming:      namingConfigSvc,
		rootFolders: rootFoldersSvc,
		log:         logger,
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
		rootFoldersSvc,
		diskProv,
	)
	// Slice 3.AM: append RecyclingBinCheck. Both deps now real:
	//   - RecyclingBinCheckConfig → *config.Provider.RecycleBin (already
	//     shipped, structural typing).
	//   - RecyclingBinDiskProvider → *disk.Provider.FolderWritable
	//     (ported this slice).
	// Mirrors the C# class at HealthCheck/Checks/RecyclingBinCheck.cs.
	recyclingBinCheck, err := healthcheck.NewRecyclingBinCheck(configProv, diskProv)
	if err != nil {
		return fmt.Errorf("healthcheck recyclingBin: %w", err)
	}

	// Slice 4.P/4.R (queue [8] + [10]): wire indexers.Repo +
	// indexerstatus.Service EARLY so that:
	//   - the BlockedIndexerSpecification + TorrentSeedingSpec at
	//     decisionEngineSpecs (below, at the original site) can reach
	//     the IndexerStatus surface, AND
	//   - the IndexerStatusCheck registered as part of healthSvc
	//     (immediately below this block) can reach the same surface.
	// The runtime indexer factory (newIndexerSource +
	// indexersearch.Service from slice 4.O) wires later, after
	// httpClient is available — that path needs the HTTP transport,
	// the repo/status pair does not.
	indexersRepo, err := indexers.NewRepo(db)
	if err != nil {
		return fmt.Errorf("indexers repo: %w", err)
	}
	indexerStatusRepo, err := indexerstatus.NewRepo(db)
	if err != nil {
		return fmt.Errorf("indexerstatus repo: %w", err)
	}
	indexerStatusSvc, err := indexerstatus.NewService(indexerStatusRepo, bus, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("indexerstatus service: %w", err)
	}

	// Slice 4.V (queue [12]): wire downloadclientstatus.Service. Pattern-
	// mirror of the indexerstatus pair above. The constructor applies
	// the C# `DownloadClientStatusService.cs:18-19` overrides
	// (MinTimeSinceInitialFailure=5min, MaxEscalationLevel=5) so the
	// back-off envelope matches the C# behaviour the moment a download-
	// client provider lands. The Service has no live consumer yet — the
	// download-client family ports in queue [13]/[14]; this slice just
	// brings the persistence + Recorder surface online so the wiring is
	// ready when the provider entries appear. The compile-time guards
	// in internal/downloadclientstatus/recorder.go pin the
	// providerstatus.Recorder + BlockedView interfaces.
	// DownloadClients repo (slice 4.DT — first wiring; the
	// package existed but nothing consumed it until the
	// /api/v3/downloadclient route landed).
	downloadClientsRepo, err := downloadclients.NewRepo(db)
	if err != nil {
		return fmt.Errorf("downloadclients repo: %w", err)
	}

	downloadClientStatusRepo, err := downloadclientstatus.NewRepo(db)
	if err != nil {
		return fmt.Errorf("downloadclientstatus repo: %w", err)
	}
	downloadClientStatusSvc, err := downloadclientstatus.NewService(downloadClientStatusRepo, bus, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("downloadclientstatus service: %w", err)
	}
	// downloadClientStatusSvc is consumed by the queue [15] grab
	// orchestrator (download.NewProcessDownloadDecisions →
	// SetRecorder, wired below). The blank-ident retention from
	// slice 4.V dropped here at 6.A6c — the per-grab outcome routing
	// is now a real consumer of the status service's Recorder
	// surface.

	// Slice 4.AB (queue [N1]): wire notificationstatus.Service. Pattern-
	// mirror of the indexerstatus / downloadclientstatus pairs above.
	// The constructor applies the C# `NotificationStatusService.cs:18-19`
	// overrides (MinTimeSinceInitialFailure=5min, MaxEscalationLevel=5)
	// so the back-off envelope matches the C# behaviour the moment a
	// notification provider lands. The Service has no live consumer yet
	// — the notifications package (queue [N2]) and NotificationService
	// fan-out (queue [N4]) port in later slices of this focused-subset;
	// this slice brings the persistence + Recorder surface online so the
	// wiring is ready when the provider entries appear. The compile-time
	// guards in internal/notificationstatus/recorder.go pin the
	// providerstatus.Recorder + BlockedView interfaces.
	notificationStatusRepo, err := notificationstatus.NewRepo(db)
	if err != nil {
		return fmt.Errorf("notificationstatus repo: %w", err)
	}
	notificationStatusSvc, err := notificationstatus.NewService(notificationStatusRepo, bus, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("notificationstatus service: %w", err)
	}
	// notificationStatusSvc is consumed by the NotificationStatusCheck
	// wired below; the fan-out service (queue [N4]) consumes the
	// Recorder surface via main.go Subscribe wiring once the blank-
	// import aggregator for the Webhook+Discord providers + Service
	// instantiation land in a follow-up slice.

	// Slice 4.AJ (queue [I1]): wire importliststatus.Service. Pattern-
	// mirror of the indexerstatus / downloadclientstatus / notification-
	// status pairs above — but the C# `ImportListStatusService` at
	// `ImportLists/ImportListStatusService.cs:18-58` does NOT override
	// the back-off Tunables (no MinimumTimeSinceInitialFailure /
	// MaximumEscalationLevel reassignment in the constructor body), so
	// importliststatus.NewService uses providerstatus.DefaultTunables
	// verbatim — see the doc-comment on importliststatus.NewService for
	// the C# parity note. The Service has no live consumer yet — the
	// import-list provider family + ImportListSyncService (queue [I2]/
	// [I4]) and ImportListStatusCheck (queue [I5]) port in later slices
	// of this focused-subset; this slice brings the persistence +
	// Recorder surface online so the wiring is ready when the provider
	// entries appear. The compile-time guards in
	// internal/importliststatus/recorder.go pin the
	// providerstatus.Recorder + BlockedView interfaces.
	importListStatusRepo, err := importliststatus.NewRepo(db)
	if err != nil {
		return fmt.Errorf("importliststatus repo: %w", err)
	}
	importListStatusSvc, err := importliststatus.NewService(importListStatusRepo, bus, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("importliststatus service: %w", err)
	}
	// importListStatusSvc is consumed by the ImportListStatusCheck
	// wired below (slice 4.AQ). The fan-out orchestrator
	// (importlists.SyncService — slice 4.AP) consumes it via the
	// I4c wiring (slice 5.J183) that constructs the Factory +
	// SyncService below — see the importListSyncSvc block past the
	// healthcheck setup.

	// Slice 4.R (queue [10]): IndexerStatusCheck wires
	// EnabledIndexerLister (over indexers.Repo via the
	// enabledIndexerListerAdapter in indexersearch_wiring.go) +
	// IndexerStatusBlockedLister (over *indexerstatus.Service via
	// structural typing on GetBlockedProviders).
	indexerStatusCheck, err := healthcheck.NewIndexerStatusCheck(
		newEnabledIndexerListerAdapter(indexersRepo),
		indexerStatusSvc,
		nil, // default clock = time.Now().UTC()
	)
	if err != nil {
		return fmt.Errorf("healthcheck indexerStatus: %w", err)
	}
	// Audit fix B.5c: IndexerLongTermStatusCheck is the same shape as
	// IndexerStatusCheck above but flips the InitialFailure time
	// predicate — it flags indexers that have been blocked for LONGER
	// than IndexerRecentFailureWindow (6h). Same lister deps; pattern-
	// mirror of the IndexerStatusCheck wire just above.
	indexerLongTermStatusCheck, err := healthcheck.NewIndexerLongTermStatusCheck(
		newEnabledIndexerListerAdapter(indexersRepo),
		indexerStatusSvc,
		nil,
	)
	if err != nil {
		return fmt.Errorf("healthcheck indexerLongTermStatus: %w", err)
	}
	// Audit fix B.5d: DownloadClientSortingCheck warns when any
	// enabled download client reports a non-empty SortingMode (which
	// would conflict with Sonarr's import pipeline). The DC enumerator
	// adapter is constructed here with nil deps; its registry +
	// Dependencies bundle gets bound later (line ~2330) once
	// httpClient + downloadClientStatusSvc + remotePathMappingsSvc are
	// all in scope. Until that bind fires the check reports OK (every
	// row's Err is "registry not yet wired", which the check logs +
	// skips). After bind the check runs the GetStatus RPC per enabled
	// client. The same enumerator will be reused for the
	// DownloadClient{RemovesCompleted,RootFolder} checks in follow-up
	// slices.
	dcStatusEnumerator := newDownloadClientStatusEnumeratorAdapter(downloadClientsRepo, logger)
	downloadClientSortingCheck, err := healthcheck.NewDownloadClientSortingCheck(dcStatusEnumerator, logger)
	if err != nil {
		return fmt.Errorf("healthcheck downloadClientSorting: %w", err)
	}
	// Audit fix B.5e: warn when an enabled DC has the "remove
	// completed" feature on (Sonarr can't import what the client has
	// deleted). Same enumerator as B.5d.
	downloadClientRemovesCompletedCheck, err := healthcheck.NewDownloadClientRemovesCompletedDownloadsCheck(dcStatusEnumerator, logger)
	if err != nil {
		return fmt.Errorf("healthcheck downloadClientRemovesCompleted: %w", err)
	}
	// Audit fix B.5f: warn when an enabled DC's output folder
	// overlaps any configured root folder. Reuses the same
	// enumerator + the existing root-folder-path adapter from the
	// import-list root-folder check.
	downloadClientRootFolderCheck, err := healthcheck.NewDownloadClientRootFolderCheck(
		dcStatusEnumerator,
		newRootFolderPathListerAdapter(rootFoldersRepo),
		logger,
	)
	if err != nil {
		return fmt.Errorf("healthcheck downloadClientRootFolder: %w", err)
	}
	// Audit fix B.5g: warn when EnableCompletedDownloadHandling
	// hasn't been explicitly set (migration helper) OR has been
	// turned off. Mirrors C# ImportMechanismCheck. Reuses the
	// dcStatusEnumerator from B.5d.
	importMechanismCheck, err := healthcheck.NewImportMechanismCheck(configProv, dcStatusEnumerator, logger)
	if err != nil {
		return fmt.Errorf("healthcheck importMechanism: %w", err)
	}

	// Slice 4.AI (queue [N5]): NotificationStatusCheck wires
	// EnabledNotificationLister (over notifications.Repo via the
	// enabledNotificationListerAdapter in healthcheck_wiring.go) +
	// NotificationStatusBlockedLister (over *notificationstatus.Service
	// via structural typing on GetBlockedProviders). Pattern-mirror
	// of the IndexerStatusCheck wire just above.
	notificationsRepo, err := notifications.NewRepo(db)
	if err != nil {
		return fmt.Errorf("notifications repo: %w", err)
	}
	notificationStatusCheck, err := healthcheck.NewNotificationStatusCheck(
		newEnabledNotificationListerAdapter(notificationsRepo),
		notificationStatusSvc,
	)
	if err != nil {
		return fmt.Errorf("healthcheck notificationStatus: %w", err)
	}

	// Slice 4.AQ (queue [I5] — closes Phase 4 focused-subset 4):
	// ImportListStatusCheck + ImportListRootFolderCheck. Pattern-
	// mirror of slice 4.AI (notificationStatusCheck wire just above).
	// The importListListerAdapter in healthcheck_wiring.go
	// satisfies BOTH healthcheck.EnabledImportListLister (filters to
	// EnableAutomaticAdd=true rows for the status check) AND
	// healthcheck.AllImportListLister (every row for the root-folder
	// check). The rootFolderPathListerAdapter projects
	// rootfolders.Repo.All to the Path-only slice the check needs;
	// disk.Provider satisfies healthcheck.ImportListFolderProbe via
	// Go structural typing on FolderExists.
	importListsRepo, err := importlists.NewRepo(db)
	if err != nil {
		return fmt.Errorf("importlists repo: %w", err)
	}
	importListLister := newImportListListerAdapter(importListsRepo)
	importListStatusCheck, err := healthcheck.NewImportListStatusCheck(
		importListLister,
		importListStatusSvc,
	)
	if err != nil {
		return fmt.Errorf("healthcheck importListStatus: %w", err)
	}
	importListRootFolderCheck, err := healthcheck.NewImportListRootFolderCheck(
		importListLister,
		newRootFolderPathListerAdapter(rootFoldersRepo),
		diskProv,
	)
	if err != nil {
		return fmt.Errorf("healthcheck importListRootFolder: %w", err)
	}

	// Slice 4.AV (queue [M4] of Phase 4 focused-subset 5): construct
	// the metadata-source proxy + SkyHookCheck. The proxy itself is
	// only consumed by this healthcheck today; Phase-5 surfaces
	// (api/v3 SeriesLookup, RefreshSeriesService) will hold the same
	// reference once wired. baseURL is hardcoded per PROV-Q30 — a
	// future slice surfaces it as a config key. dailySeries +
	// seriesLookup pass nil-tolerantly (the Daily-flag override and
	// library-hit short-circuit don't fire until those deps wire).
	//
	// Dedicated *http.Client because the shared internal/httpclient
	// Client interface doesn't satisfy metadatasource.Proxy's
	// *http.Client expectation; reusing the shared retry/proxy/UA
	// machinery for SkyHook calls is a follow-up.
	metadataSourceHTTP := &http.Client{Timeout: 30 * time.Second}
	metadataSourceProxy, err := metadatasource.NewProxy(
		metadataSourceHTTP,
		"https://skyhook.sonarr.tv/v1/tvdb",
		nil, // DailySeriesService — wires when DailySeries persistence ports.
		nil, // SeriesLookup — wires alongside the api/v3 lookup endpoint.
		logger,
	)
	if err != nil {
		return fmt.Errorf("metadatasource proxy: %w", err)
	}
	skyHookCheck, err := healthcheck.NewSkyHookCheck(
		newSkyHookProberAdapter(metadataSourceProxy),
		logger,
	)
	if err != nil {
		return fmt.Errorf("healthcheck skyHook: %w", err)
	}

	// Slice 4.AW (queue [M5] of Phase 4 focused-subset 5): wire the
	// existing internal/healthcheck/system_time.go check (ported in an
	// earlier slice but never reached the healthSvc list). The fetcher
	// hits services.sonarr.tv/v1/time per C#
	// `SystemTimeCheck.cs:28-32`; the client reuses the dedicated
	// metadata-source HTTP client (30s timeout, no retry plumbing —
	// good enough for a once-per-cadence probe).
	systemTimeCheck, err := healthcheck.NewSystemTimeCheck(
		newServiceTimeFetcher(metadataSourceHTTP),
		nil, // now: defaults to time.Now().UTC()
		logger,
	)
	if err != nil {
		return fmt.Errorf("healthcheck systemTime: %w", err)
	}

	// Slice 4.BN (C-cleanup [C10]): wire the existing ProxyCheck. The
	// stub adapter reports ProxyEnabled=false until the HostConfig
	// keyvalues persistence layer ports, so the check short-circuits to
	// OK every cadence — mirrors C# behavior for a freshly-installed
	// instance with no proxy configured.
	proxyCheck, err := healthcheck.NewProxyCheck(
		proxyHostConfigAdapter{cfg: configProv},
		nil, // resolver: defaults to net.LookupIP
		newProxyPinger(configProv),
	)
	if err != nil {
		return fmt.Errorf("healthcheck proxy: %w", err)
	}

	// Slice 4.AZ (sub-arc M6b-ii of queue [M6]): construct the
	// scene-mapping cloud-fetch Provider + chain it onto the existing
	// scenemappingsSvc via WithProviders. The service was built earlier
	// with Writer + Bus already wired (slice 4.AY); attaching providers
	// now activates the UpdateMappings refresh path.
	//
	// Reuses metadataSourceHTTP (the dedicated 30s-timeout *http.Client
	// the skyhook proxy + services-time fetcher both share). A future
	// slice may swap this for a shared retry/proxy/UA-aware client
	// from internal/httpclient once that interface conversion lands.
	scenemappingsProxy, err := scenemappings.NewProxy(
		metadataSourceHTTP,
		scenemappings.ServicesSceneMappingURL,
		logger,
	)
	if err != nil {
		return fmt.Errorf("scenemappings proxy: %w", err)
	}
	scenemappingsSvc.WithProviders(scenemappingsProxy)

	// J491: wire the SceneMappingService on-demand UpdateMappings
	// handlers (C# IHandle<SeriesRefreshStartingEvent/SeriesAddedEvent/
	// SeriesImportedEvent> at SceneMappingService.cs:311-329). These
	// were UNWIRED — the service's RefreshCache doc-comment claimed
	// production wired them, but no subscription existed, so on a fresh
	// / restarted install the TheXEM scene mappings stayed empty until
	// the daily UpdateSceneMapping task ran. Now adding/importing/
	// manually-refreshing a series fetches the mappings on demand
	// (gated once-after-startup; manual refresh re-fetches if >1min stale).
	messaging.Subscribe(bus, "scenemappings.HandleSeriesRefreshStarting",
		func(ctx context.Context, ev series.SeriesRefreshStartingEvent) error {
			return scenemappingsSvc.HandleSeriesRefreshStarting(ctx, ev.ManualTrigger)
		})
	messaging.Subscribe(bus, "scenemappings.HandleSeriesAdded",
		func(ctx context.Context, _ series.SeriesAddedEvent) error {
			return scenemappingsSvc.HandleSeriesAddedOrImported(ctx)
		})
	messaging.Subscribe(bus, "scenemappings.HandleSeriesImported",
		func(ctx context.Context, _ series.SeriesImportedEvent) error {
			return scenemappingsSvc.HandleSeriesAddedOrImported(ctx)
		})

	// Slice 4.EJ wires three additional checks the C# tree fires on a
	// fresh DB: IndexerRssCheck (no indexers with RSS enabled →
	// Error), DownloadClientCheck (no clients → Warning),
	// IndexerSearchCheck (no auto-search indexers → Warning). All
	// three feed /api/v3/health and reach byte-eq with the recording.
	indexerRssCheck, err := healthcheck.NewIndexerRssCheck(newRssIndexerCountAdapter(indexersRepo))
	if err != nil {
		return fmt.Errorf("healthcheck indexerRss: %w", err)
	}
	indexerSearchCheck, err := healthcheck.NewIndexerSearchCheck(newSearchIndexerCountAdapter(indexersRepo))
	if err != nil {
		return fmt.Errorf("healthcheck indexerSearch: %w", err)
	}
	downloadClientCheck, err := healthcheck.NewDownloadClientCheck(newEnabledDownloadClientCountAdapter(downloadClientsRepo))
	if err != nil {
		return fmt.Errorf("healthcheck downloadClient: %w", err)
	}
	// Audit fix B.5a: warn when an enabled indexer's Torznab settings
	// point at Jackett's all-indexers endpoint (Sonarr can't classify
	// per-indexer responses from that endpoint). Mirrors C#
	// IndexerJackettAllCheck. *indexers.Repo satisfies the lister
	// interface directly via Repository[T].All.
	indexerJackettAllCheck, err := healthcheck.NewIndexerJackettAllCheck(indexersRepo)
	if err != nil {
		return fmt.Errorf("healthcheck indexerJackettAll: %w", err)
	}
	// Audit fix B.5b: warn when an enabled indexer pins a
	// DownloadClientId > 0 that is missing or disabled. Mirrors C#
	// IndexerDownloadClientCheck. Both repos satisfy the lister
	// interfaces directly.
	indexerDCCheck, err := healthcheck.NewIndexerDownloadClientCheck(indexersRepo, downloadClientsRepo)
	if err != nil {
		return fmt.Errorf("healthcheck indexerDownloadClient: %w", err)
	}

	// J464: DownloadClientStatusCheck — warns when enabled download
	// clients are inside their back-off window (some → Warning, all →
	// Error). Sibling of the indexer/importlist/notification status
	// checks (the reason codes already existed but no check used them).
	// Ports DownloadClientStatusCheck.cs (joins enabled clients with
	// downloadClientStatusSvc.GetBlockedProviders, no recency window).
	downloadClientStatusCheck, err := healthcheck.NewDownloadClientStatusCheck(
		newEnabledDownloadClientStatusListerAdapter(downloadClientsRepo),
		downloadClientStatusSvc,
	)
	if err != nil {
		return fmt.Errorf("healthcheck downloadClientStatus: %w", err)
	}

	// Slice 5.J142: PackageGlobalMessageCheck wired against the
	// deploymentinfo.Provider built from the binary's startup
	// folder. The Provider tolerates missing package_info /
	// release_info files (returns zero-valued fields) so the check
	// always passes for tarball / dev installs and surfaces a
	// notice / warning / error only when an OS packager populated
	// the PackageGlobalMessage field. Closes audit B.5's
	// PackageGlobalMessageCheck deferral marker.
	deployInfo := deploymentinfo.New(folder.Startup, deploymentinfo.OSFileSystem{})
	packageGlobalMessageCheck := healthcheck.NewPackageGlobalMessageCheck(deployInfo)

	// Slice 5.J143 (closes audit B.5 UpdateCheck deferral): wire
	// the previously-unwired UpdateCheck (J35-era port) against the
	// J40 update.Service. Construction relocated here from its
	// previous post-healthSvc spot so the same *update.Service can
	// satisfy both the UpdateCheck.UpdateAvailableLookup AND the
	// later ApplicationCheckUpdate handler.
	updateSvc, err := update.NewService(
		&http.Client{Timeout: 30 * time.Second},
		updateBranchAdapter{cfg: cfg},
		buildinfo.Version,
		update.DefaultServicesBaseURL,
		10*time.Second,
	)
	if err != nil {
		return fmt.Errorf("update service: %w", err)
	}
	updateCheck, err := healthcheck.NewUpdateCheck(
		diskProv,
		folder.Startup,
		cfg,
		updateCheckOsInfoAdapter{info: appenv.DetectOs()},
		updateAvailableLookupAdapter{svc: updateSvc},
		parseBuildDate(buildinfo.BuildDate),
	)
	if err != nil {
		return fmt.Errorf("healthcheck updateCheck: %w", err)
	}

	// Slice 5.J144: MountCheck wire-up. Walks every persisted series
	// path and surfaces an Error when any backing mount reports
	// IsReadOnly. Uses *series.Repo for AllSeriesPaths + *disk.Provider
	// for GetMount (the cross-platform port shipped at J36).
	mountCheck, err := healthcheck.NewMountCheck(seriesRepo, diskProv)
	if err != nil {
		return fmt.Errorf("healthcheck mountCheck: %w", err)
	}

	// Slice 5.J144: RemotePathMappingCheck wire-up. The 387-LOC C#
	// check ported at J55 (scheduled-Check() path) finally reaches
	// the healthSvc registry. The OsInfoAdapter from J143 satisfies
	// the IsDocker-only subset interface via structural typing.
	remotePathMappingCheck, err := healthcheck.NewRemotePathMappingCheck(
		configProv,
		dcStatusEnumerator,
		diskProv,
		updateCheckOsInfoAdapter{info: appenv.DetectOs()},
		runtime.GOOS,
		logger,
	)
	if err != nil {
		return fmt.Errorf("healthcheck remotePathMappingCheck: %w", err)
	}

	healthSvc, err := healthcheck.NewService([]healthcheck.Check{
		healthcheck.NewAppDataLocationCheck(folder),
		apiKeyCheck,
		removedSeriesCheck,
		rootFolderCheck,
		recyclingBinCheck,
		indexerStatusCheck,
		notificationStatusCheck,
		importListStatusCheck,
		importListRootFolderCheck,
		skyHookCheck,
		systemTimeCheck,
		proxyCheck,
		indexerRssCheck,
		indexerSearchCheck,
		downloadClientCheck,
		indexerJackettAllCheck,
		indexerDCCheck,
		indexerLongTermStatusCheck,
		downloadClientStatusCheck,
		downloadClientSortingCheck,
		downloadClientRemovesCompletedCheck,
		downloadClientRootFolderCheck,
		importMechanismCheck,
		packageGlobalMessageCheck,
		updateCheck,
		mountCheck,
		remotePathMappingCheck,
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
	// Slice 3.BK (queue [14]): real *episodefilemoving.UpdateService
	// satisfies [episodefilemoving.FileDateUpdater] — replaces the
	// slice-3.70 nil-passes-noop placeholder. Ports
	// `UpdateEpisodeFileService.ChangeFileDateForFile`. Reads the
	// FileDate config flag (None/LocalAirDate/UtcAirDate); the writer
	// pulls this once per move via the FileDateUpdater seam.
	// *disk.Provider satisfies FileTimeManager via Go structural typing
	// (FileGetLastWrite + new FileSetLastWriteTime shipped this slice).
	fileDateUpdater, err := episodefilemoving.NewUpdateService(diskProv, configProv, logger)
	if err != nil {
		return fmt.Errorf("episodefilemoving fileDate: %w", err)
	}
	// J212 — wire fileDateUpdater as a SeriesScannedEvent subscriber.
	// Ports C# UpdateEpisodeFileService.cs:103-136 (the
	// IHandle<SeriesScannedEvent> arm). episodesSvc satisfies
	// EpisodesWithFilesLister via EpisodesWithFiles; episodeFilesSvc
	// satisfies EpisodeFileLookup via GetFile. SetSeriesScannedDeps
	// MUST run before the Subscribe so the handler has its deps when
	// the first event fires. Per-series scan-complete now re-stamps
	// every episode file's mtime to the air date — was previously
	// only invoked via the move-on-import path (slice 3.BK FileDateUpdater
	// seam), so post-J212 the user-configured FileDate setting also
	// applies to pre-existing files discovered on disk during scan.
	fileDateUpdater.SetSeriesScannedDeps(episodesSvc, episodeFilesSvc)
	messaging.Subscribe(bus, "episodefilemoving.HandleSeriesScanned",
		func(ctx context.Context, ev mediafiles.SeriesScannedEvent) error {
			return fileDateUpdater.HandleSeriesScanned(ctx, ev)
		})
	// Slice 3.BL (queue [15]): the *videofileinforeader.Reader the
	// MediaInfo updater wires below is also the MediaInfoReader the
	// new ImportScriptService needs (the RenameRequested arm at C#
	// `ScriptImportDecider.cs:230` re-probes after the script renames).
	// Construct the reader once here so both consumers can hold a
	// reference; the slice-3.BJ wire site downstream just re-uses
	// videoInfoReader instead of re-constructing.
	mediaInfoReader := videofileinforeader.NewExecRunner("")
	videoInfoReader, err := videofileinforeader.NewReader(diskProv, mediaInfoReader, logger)
	if err != nil {
		return fmt.Errorf("videofileinforeader: %w", err)
	}
	// Slice 3.BL: real *episodefilemoving.ImportScriptService
	// satisfies [episodefilemoving.ImportScriptRunner] — replaces the
	// slice-3.70 nil-passes-alwaysDeferImportScriptRunner placeholder.
	// Ports `MediaFiles/ScriptImportDecider.cs:23-237`. Production now
	// invokes the user-supplied import script (when UseScriptImport=true
	// AND ScriptImportPath is set) and honours its move-decision
	// verdict; the writer's TransferFile path observes
	// DeferMove/MoveComplete/RenameRequested as if it were the C# tree.
	tagsRepo, err := tags.NewRepo(db)
	if err != nil {
		return fmt.Errorf("tags repo: %w", err)
	}
	tagsSvc := tags.NewService(tagsRepo, bus)
	importScriptRunner, err := episodefilemoving.NewImportScriptService(
		configProv,
		instanceNameReaderAdapter{p: cfg},
		tagLabelReaderAdapter{svc: tagsSvc},
		diskProv,
		videoInfoReader,
		episodefilemoving.NewExecProcessRunner(),
		logger,
	)
	if err != nil {
		return fmt.Errorf("episodefilemoving importScript: %w", err)
	}
	episodeFileMovingSvc, err := episodefilemoving.NewService(
		episodeFileMovingFs,
		nameBuilderAdapter{svc: namingConfigSvc, log: logger},
		rootFolderFinderAdapter{svc: rootFoldersSvc, log: logger},
		configProv,
		episodesSvc,
		fileDateUpdater,
		mediaFileAttrsSvc,
		importScriptRunner,
		// Slice 3.AJ: EventPublisher wired via the new
		// episodeFileMovingEventPublisherAdapter (downloadedimport_wiring.go).
		// Type-switches EpisodeFolderCreatedEvent and republishes
		// through *messaging.Bus's typed Publish[E] — same posture as
		// the slice-3.99 importEventPublisherAdapter. With no ported
		// subscribers yet this is "lit but no listeners" but the bus
		// dispatch path is now exercised so the next subsystem that
		// ports a subscriber sees events from production without
		// further wiring at this site.
		episodeFileMovingEventPublisherAdapter{bus: bus, log: logger},
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
	// Slice 3.AG relocated history.NewRepo + history.NewService up to
	// alongside dlHistoryRepo / dlHistorySvc so the new
	// historyGrabsFinderAdapter (episodeimport_wiring.go) feeding
	// AggregateReleaseInfo can reach historySvc at the aggregator
	// construction time. historySvc is reused below by the slice-3.A1
	// historyGrabFinderForImportAdapter.

	// J479: wire the extra-file import subsystem (`internal/extras`) —
	// imports subtitles / .nfo / artwork companions next to the imported
	// episode file (gated by config.ImportExtraFiles + ExtraFileExtensions).
	// Replaces the prior noopExtrasImporter.
	subtitleFilesRepo, err := subtitlefiles.NewRepo(db)
	if err != nil {
		return fmt.Errorf("subtitlefiles repo: %w", err)
	}
	extrasImporter, extrasSvc, err := buildExtrasImporter(episodeFileMovingFs, extraFilesRepo, subtitleFilesRepo, diskProv, sampleDetector, parsingSvc, mediaFileAttrsSvc, configProv, logger)
	if err != nil {
		return fmt.Errorf("extras importer: %w", err)
	}
	// J481: when a series/its files are renamed, move the imported
	// subtitle/.nfo companions to follow the new names. Ports C#
	// ExtraService IHandle<SeriesRenamedEvent>. The Go SeriesRenamedEvent
	// carries the post-move EpisodeFile rows (new RelativePath).
	messaging.Subscribe(bus, "extras.MoveFilesAfterRename",
		func(ctx context.Context, ev episodefiles.SeriesRenamedEvent) error {
			if ev.Series == nil || len(ev.RenamedFiles) == 0 {
				return nil
			}
			files := make([]*episodefiles.File, 0, len(ev.RenamedFiles))
			for i := range ev.RenamedFiles {
				if ev.RenamedFiles[i].EpisodeFile != nil {
					files = append(files, ev.RenamedFiles[i].EpisodeFile)
				}
			}
			if len(files) == 0 {
				return nil
			}
			if _, err := extrasSvc.MoveFilesAfterRename(ctx, ev.Series, files); err != nil {
				logger.Warn("extras: MoveFilesAfterRename on SeriesRenamed failed", "seriesId", ev.Series.ID, "err", err)
			}
			return nil
		})

	importApprovedSvc, err := episodeimport.NewImportApprovedEpisodes(
		episodeFilesSvc,      // files (MediaFileWriter — Add + GetFilesWithRelativePath + Delete)
		diskProv,             // disk (FileSizeReader — GetFileSize)
		upgradeMediaFilesSvc, // upgrader — slice-3.97; recycles + replaces existing files
		extrasImporter,       // J479: extra-file import (subtitles/.nfo/artwork)
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

	// Slice 3.AE: mediafiles.Service construction relocated here so
	// the real mediafilesImportDecisionAdapter +
	// mediafilesImportApprovedAdapter (cmd/sonarr/mediafiles_wiring.go)
	// replace the slice-3.52 stubImportDecisionMaker /
	// stubImportApprovedEpisodes. The adapters wrap the slice-3.85
	// *episodeimport.ImportDecisionMaker (constructed above with 11
	// specs + the slice-3.91/3.92/3.93/3.94/3.95 augmenters) and the
	// slice-3.98 *episodeimport.ImportApprovedEpisodes (constructed
	// just above). With this swap, mediafiles.Service.Scan
	// (RescanSeries → DiskScanService.Scan) walks the real
	// per-file decision pipeline against unmapped files and commits
	// the approved subset via the real ImportApprovedEpisodes; until
	// this slice it short-circuited to "no decisions" + "no-op
	// import" against the stubs. The remaining stub
	// (stubUpdateMediaInfo) gates on the MediaInfo updater port —
	// REPLACED in slice 3.BI by the real *mediainfoupdater.UpdateService
	// constructed below. As of slice 3.BJ the MediaInfoReader dep
	// is the real *videofileinforeader.Reader (sub-arc 13c) — closes
	// queue [13]. The reader shells out to ffprobe via os/exec; if
	// the binary is missing on PATH the per-file Update path's
	// reader-error fallthrough fires (debug-log "ffprobe error",
	// returns nil-result) and the row is skipped without surfacing
	// to higher layers. Production deployments are expected to ship
	// ffprobe alongside the binary (matches the C# tree's bundled
	// FFMpegCore expectation).
	//
	// Slice 3.BL relocated the videoInfoReader construction up to
	// the ImportScriptService boot site so both consumers share one
	// reader instance. This site re-uses the existing variable.
	mediaInfoUpdater, err := mediainfoupdater.NewUpdateService(
		diskProv,
		episodeFilesSvc,
		episodeFilesSvc,
		videoInfoReader,
		configProv,
		logger,
	)
	if err != nil {
		return fmt.Errorf("mediainfoupdater service: %w", err)
	}
	mediaInfoUpdater.SubscribeOn(bus)
	mediafilesSvc, err := mediafiles.NewService(
		diskProv,
		configProv,
		rootFoldersSvc,
		seriesSvc,
		episodeFilesSvc,
		mediaFileCleanupSvc,
		mediafilesImportDecisionAdapter{real: importDecisionMaker},
		mediafilesImportApprovedAdapter{real: importApprovedSvc},
		mediaInfoUpdater,
		bus,
		logger,
	)
	if err != nil {
		return fmt.Errorf("mediafiles service: %w", err)
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

	// Slice 3.BB (sub-arc 10b): blocklist IHandle subscribers — both
	// the DownloadFailedEvent insert path AND the SeriesDeletedEvent
	// bulk-delete path. Mirrors C# `BlocklistService.cs:126-163`.
	// blocklistSvc binds the repo via the slice-3.BA write surface;
	// the handlers route through it (Add for the failed insert,
	// DeleteForSeries for the series-cascade cleanup).
	blocklistSvc := blocklist.NewService(blocklistRepo, bus)
	if blocklistSvc == nil {
		return fmt.Errorf("blocklist service: nil — required dep regressed")
	}
	messaging.Subscribe(bus, "blocklist.HandleDownloadFailed",
		func(ctx context.Context, ev trackeddownloads.DownloadFailedEvent) error {
			return blocklisthandlers.HandleDownloadFailed(ctx, blocklistSvc, &ev)
		})
	messaging.Subscribe(bus, "blocklist.HandleSeriesDeleted",
		func(ctx context.Context, ev series.SeriesDeletedEvent) error {
			return blocklisthandlers.HandleSeriesDeleted(ctx, blocklistSvc, &ev)
		})

	// Slice 3.BF (sub-arc 11d): queue.Service host wire + QueueSpec
	// hookup. queueSvc is the in-memory cache; SubscribeOn registers
	// the IHandle<TrackedDownloadRefreshedEvent> dispatch + captures
	// the bus for QueueUpdatedEvent publish per slice 3.BE. The
	// QueueSpec adapter projects queue.Queue rows onto the narrow
	// decisionengine.QueueItem shape so the spec stays out of the
	// queue→decisionengine import edge.
	queueSvc := queue.NewService()
	queueSvc.SubscribeOn(bus)

	delayProfilesRepo, err := delayprofiles.NewRepo(db)
	if err != nil {
		return fmt.Errorf("delayprofiles repo: %w", err)
	}
	delayProfilesSvc := delayprofiles.NewService(delayProfilesRepo, bus)
	if delayProfilesSvc == nil {
		return fmt.Errorf("delayprofiles service: nil — required dep regressed")
	}
	releaseProfilesRepo, err := releaseprofiles.NewRepo(db)
	if err != nil {
		return fmt.Errorf("releaseprofiles repo: %w", err)
	}
	releaseProfilesSvc := releaseprofiles.NewService(releaseProfilesRepo, bus)
	if releaseProfilesSvc == nil {
		return fmt.Errorf("releaseprofiles service: nil — required dep regressed")
	}
	matcherSvc := releaseprofiles.NewMatcherService()
	if matcherSvc == nil {
		return fmt.Errorf("releaseprofiles matcher service: nil — required dep regressed")
	}

	// indexersRepo + indexerStatusSvc are constructed above (slice 4.P
	// moved their wiring above healthcheck so both the
	// BlockedIndexerSpecification at decisionEngineSpecs and the
	// IndexerStatusCheck at healthSvc reach them).

	decisionEngineSpecs, err := buildDecisionEngineSpecs(decisionEngineDeps{
		upgradable:     upgradableSpec,
		qualityProfile: qualityProfilesSvc,
		episodes:       episodesSvc,
		episodeFiles:   episodeFilesSvc,
		disk:           diskProv,
		configCfg:      configProv,
		freeSpaceCfg:   configProv,
		retentionCfg:   configProv,
		maxSizeCfg:     configProv,
		minAgeCfg:      configProv,
		cdhCfg:         configProv,
		historyByEp:    historySvc,
		delayProfiles:  delayProfilesSvc,
		pending:        pendingReleasesSvc,
		releaseProfile: releaseProfilesSvc,
		matcher:        matcherSvc,
		blocklistRepo:  blocklistRepo,
		customFormats:  customFormatsSvc,
		configProvider: configProv,
		queueSvc:       queueSvc,
		blockedView:    indexerStatusSvc,
		minSeeders:     newMinimumSeedersAdapter(indexersRepo),
		indexerTags:    indexerTagFinderAdapter{repo: indexersRepo},
		log:            logger,
	})
	if err != nil {
		return fmt.Errorf("decisionengine specs: %w", err)
	}
	decisionMaker := decisionengine.NewDecisionMaker(decisionEngineSpecs, logger)

	// Slice 4.U (queue [11] sub-arc 11c, CLOSES queue [11]): construct
	// the aggregation.Service + DownloadDecisionMaker that the RssSync
	// handler dispatches to. aggregation.Service wraps the
	// AggregateLanguages augmenter (the only augmenter at HEAD);
	// IndexerMultiLanguagesLookup is nil (the per-indexer MultiLanguages
	// reach into provider settings is a future follow-up — without it
	// the augmenter skips the indexer-side language union but still
	// runs the parsed-language + series.OriginalLanguage fallbacks).
	remoteEpisodeAggregator, err := aggregation.NewService(
		[]aggregation.Augmenter{
			aggregation.NewAggregateLanguages(nil, logger),
		},
		logger,
	)
	if err != nil {
		return fmt.Errorf("aggregation service: %w", err)
	}
	downloadDecider, err := decisionengine.NewDownloadDecisionMaker(
		decisionMaker,
		parsingSvc,
		remoteEpisodeAggregator,
		scenemappingsSvc,
		logger,
	)
	if err != nil {
		return fmt.Errorf("download decision maker: %w", err)
	}

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

	// Slice 3.BN (queue [16] sub-arc 16b): wire the *backup.Service
	// the BackupCommand handler delegates to. Picks the engine-
	// specific DatabaseBackup impl from dbCfg.Engine — SQLite uses
	// `VACUUM INTO`, Postgres shells out to pg_dump. Adapters bind
	// the Filesystem narrow over *disk.Provider+stdlib, AppFolders
	// over appenv.AppFolderInfo, VersionInfo over buildinfo.String().
	var dbBackup backup.DatabaseBackup
	switch dbCfg.Engine {
	case datastore.EngineSQLite:
		sqliteBackup, sqliteErr := backup.NewSQLiteBackup(db.SQL)
		if sqliteErr != nil {
			return fmt.Errorf("backup sqlite: %w", sqliteErr)
		}
		dbBackup = sqliteBackup
	case datastore.EnginePostgres:
		pgBackup, pgErr := backup.NewPostgresBackup(dbCfg.Host, dbCfg.Port, dbCfg.User, dbCfg.Password, dbCfg.Database)
		if pgErr != nil {
			return fmt.Errorf("backup postgres: %w", pgErr)
		}
		dbBackup = pgBackup
	default:
		return fmt.Errorf("backup: unsupported engine %q", dbCfg.Engine)
	}
	backupSvc, err := backup.NewService(
		newBackupFilesystemAdapter(diskProv, logger),
		backup.ZipArchiver{},
		dbBackup,
		configProv,
		backupAppFoldersAdapter{folder: folder},
		backupVersionInfoAdapter{},
		logger,
	)
	if err != nil {
		return fmt.Errorf("backup service: %w", err)
	}

	// Slice 3.BP (queue [18]): construct *lifecycle.Service so the
	// IExecute<ShutdownCommand> + IExecute<RestartCommand> handlers
	// register against the real publish-and-flag semantics
	// (ApplicationShutdownRequested{Restarting:false/true} + the
	// RestartPending atomic the host reads at shutdown to decide
	// whether to chain an updater process). Replaces the direct
	// messaging.Publish call at the shutdown drain below.
	lifecycleSvc, err := lifecycle.NewService(bus)
	if err != nil {
		return fmt.Errorf("lifecycle service: %w", err)
	}

	// Slice 4.D (queue [1] sub-arc 1d): construct the production HTTP
	// client. Wires the rate limiter + per-host status tracker + the
	// User-Agent built from buildinfo. ProxyProvider is intentionally
	// NoopProxyProvider for now — the typed proxy config will land with
	// the download-client family in a follow-up Phase-4 loop. Sub-arc 1c
	// already structured the dispatcher to accept a ProxyProvider, so
	// that swap is a one-line change at this site.
	//
	// httpClient is held in this scope until queue [4]+ wires real
	// consumers (Newznab/Torznab/RSS indexers, RssSync job). Until then
	// we keep a reference via the blank ident so the linter accepts the
	// unused local — the construction itself is the contract under test.
	httpClient, err := httpclient.NewClientWithOptions(httpclient.ClientOptions{
		UserAgent:         "Sonarr/" + buildinfo.Version + " (" + buildinfo.Commit + ")",
		RateLimiter:       httpclient.NewRateLimiter(),
		HostStatusTracker: httpclient.NewHostStatusTracker(),
	})
	if err != nil {
		return fmt.Errorf("http client: %w", err)
	}
	// G10 (J441): populate indexers.Default() with the Newznab/Torznab
	// Registry constructors so the indexer Test/TestAll/Action endpoints
	// build real providers (the concrete impls otherwise only register
	// into the provider.Default() catalog). Idempotent across boots.
	registerIndexerTestProviders(httpClient)

	// J459 — wire the notification DISPATCH service. Until now only the
	// notification CRUD/API + status + provider-Test paths were wired;
	// `notifications.Service` (the event-driven OnGrab/OnDownload/
	// OnImportComplete/health/etc. fan-out) was never constructed, so the
	// running app fired ZERO event-driven notifications. Build the Factory
	// over the Default() provider registry + a dedicated net/http client
	// (the providers' httpDoer is the std `Do(*http.Request)` shape, NOT
	// the internal httpclient.Client `Execute` surface — confirmed by the
	// provider tests passing `httptest.Server.Client()`), then subscribe
	// the dispatchers to the bus. notificationStatusSvc backs both the
	// Factory blocked-filter and the per-dispatch success/failure recorder.
	// ConfigProvider feeds the Discord author name + Telegram title
	// suffix from the configured InstanceName (Discord.cs:43,
	// Telegram.cs:23). The providers assert `InstanceName() string`;
	// the app-config config.Provider has no such accessor (the value
	// lives on the configfile provider), so it is adapted here.
	// Passing configProv (the prior wiring) made both providers fall
	// back to the literal "Sonarr".
	notificationDeps := notifications.Dependencies{
		HTTPClient:        &http.Client{Timeout: 30 * time.Second},
		ConfigProvider:    notificationConfigAdapter{src: cfg},
		Logger:            logger,
		StatusService:     notificationStatusSvc,
		NotificationsRepo: notificationsRepo,
	}
	notificationFactory, err := notifications.NewFactory(notificationsRepo, notifications.Default(), notificationStatusSvc, notificationDeps, logger)
	if err != nil {
		return fmt.Errorf("notifications factory: %w", err)
	}
	notificationDispatchSvc, err := notifications.NewService(notificationFactory, notificationStatusSvc, logger)
	if err != nil {
		return fmt.Errorf("notifications service: %w", err)
	}
	notificationDispatchSvc.Subscribe(bus)

	// J364 — MediaCover cover-download subscriber. Construct the
	// mediacover.Service (URL-rewrite surface ported in 3.BO; download
	// arm in J363) against the real disk + http + config adapters, then
	// subscribe its IHandleAsync<SeriesUpdatedEvent> + <SeriesDeletedEvent>
	// arms to the bus (C# MediaCoverService.HandleAsync). SeriesUpdated
	// fires after a RefreshSeries metadata sync — the moment C# fetches
	// covers; SeriesDeleted wipes the per-series cover folder. The
	// CoversUpdatedEvent is republished with the download `updated` flag
	// (mirrors C# :237) for the SignalR / UI-refresh consumers.
	mediaCoverSvc, err := mediacover.NewService(
		mediaCoverFS{disk: diskProv},
		mediaCoverFolders{appData: folder.AppData},
		mediaCoverURLBase{cfg: cfg},
		logger,
	)
	if err != nil {
		return fmt.Errorf("mediacover service: %w", err)
	}
	mediaCoverSvc.SetDownloader(mediaCoverDownloader{client: httpClient})
	// J365 — bind the resize tier (golang.org/x/image CatmullRom +
	// jpeg q92 / png), so EnsureCovers writes the per-cover-type height
	// variants the React UI requests (poster-250.jpg etc.).
	mediaCoverSvc.SetResizer(mediacover.NewImageResizer())
	messaging.Subscribe(bus, "mediacover.HandleSeriesUpdated",
		func(ctx context.Context, ev series.SeriesUpdatedEvent) error {
			updated, err := mediaCoverSvc.HandleSeriesUpdated(ctx, ev.Series)
			if err != nil {
				return err
			}
			return messaging.Publish(ctx, bus, mediacover.CoversUpdatedEvent{Series: ev.Series, Updated: updated})
		})
	messaging.Subscribe(bus, "mediacover.HandleSeriesDeleted",
		func(ctx context.Context, ev series.SeriesDeletedEvent) error {
			ids := make([]int, 0, len(ev.Series))
			for i := range ev.Series {
				ids = append(ids, ev.Series[i].ID)
			}
			return mediaCoverSvc.HandleSeriesDeleted(ctx, ids)
		})
	// Slice 4.O (queue [7] sub-arc 7c, FINAL) + 4.P (queue [8]):
	// wire indexersearch.Service. The dispatcher's typed deps
	// (series.Service / episodes.Service / scenemappings.Service)
	// satisfy the SeriesGetter/EpisodeRepo/SceneMapper narrow
	// interfaces directly. indexersRepo + indexerStatusSvc are
	// already constructed above (slice 4.P moved their wiring earlier
	// so the BlockedIndexerSpecification + TorrentSeedingSpecification
	// at decisionEngineSpecs build can reach them).
	indexerSource := newIndexerSource(indexersRepo, httpClient, indexerStatusSvc, logger)
	indexerSearchSvc := indexersearch.NewService(
		indexerSource,
		indexerStatusSvc,
		seriesSvc,
		episodesSvc,
		scenemappingsSvc,
		downloadDecider,
		logger,
	)
	// Slice 4.Q (queue [9]): wire the RssSync handler. Below in
	// handlers.RegisterAll the indexerSearchSvc satisfies the narrow
	// handlers.RssFetcher interface via Go's structural typing.

	// Slice 6.A6c (queue [15] orchestrator production wiring):
	// construct *download.ProcessDownloadDecisions — the per-batch
	// grab orchestrator ported across slices 4.Z / 4.BP / 4.BR /
	// 4.BQ and activated end-to-end across the 6.A arc. Bound here
	// AFTER httpClient (HTTPBodyFetcher needs an HTTP transport) +
	// AFTER downloadClientStatusSvc (Recorder dep) so every required
	// dep is in scope.
	//
	// Dependencies wired:
	//
	//   - ClientLister ← downloadClientLister adapter (slice 6.A6b),
	//     walking downloadClientsRepo + downloadclients.Default() at
	//     grab time. The 5 concrete client packages (Sabnzbd /
	//     Transmission / qBittorrent / Deluge / NZBGet) register their
	//     Constructors against the default registry via init() — the
	//     blank imports near the top of this file activate that.
	//   - DecisionPrioritizer ← nil. The C# `IPrioritizeDownloadDecision`
	//     sort lands as a follow-up slice (the NewDecisionComparer
	//     wiring needs ConfigReader + ComparerProfileLookup +
	//     ComparerDelayProfileLookup adapters; deferring keeps this
	//     slice surgical). With nil, the orchestrator processes
	//     decisions in input order — matches the pre-slice-4.BP
	//     behaviour and is correct (though sub-optimal) until the
	//     prioritizer slice lands.
	//   - Bus ← the shared *messaging.Bus the rest of main.go uses.
	//     Activates the 5 EpisodeGrabbedEvent listeners (historyhandlers
	//     at :822, downloadhistory.Service, pendingreleaseshandlers,
	//     notifications.Service, blocklisthandlers — all subscribed
	//     elsewhere in main.go).
	//   - Recorder ← *downloadclientstatus.Service. Per-grab outcome
	//     escalation routes through RecordSuccess /RecordFailure /
	//     RecordConnectionFailure on def.ID — drops the slice-4.V
	//     blank-ident retention.
	//   - BodyFetcher ← &download.HTTPBodyFetcher{}. The nil-Client
	//     default falls back to http.DefaultClient — adequate for
	//     unauthenticated public-feed indexers + as a baseline.
	//     A per-indexer authenticated HTTPBodyFetcher (Cookie /
	//     Authorization header attachment via indexers.Provider.
	//     GetDownloadRequest) is a documented follow-up.
	//
	// No caller wired this slice — the orchestrator is held via
	// the blank ident pending slice 6.A6d, which extends the
	// RssSync handler with a GrabProcessor seam that feeds the
	// approved decisions into ProcessDecisions.
	// Compile-time guard: *remotepathmappings.Service satisfies the
	// downloadclients.RemotePathMapper narrow interface. Pins the
	// structural-typing contract at the wiring boundary so a future
	// signature drift in either package surfaces at boot rather than
	// at the first per-client cast failure.
	var _ downloadclients.RemotePathMapper = (*remotepathmappings.Service)(nil)
	// Slice 6.C2 (Phase 6.C wire-up): the same guard for the
	// downloadclients.DiskProvider narrow interface — *disk.Provider's
	// DeleteFolder(path, recursive bool) error matches via Go
	// structural typing. The 6.C3 NZBGet RemoveItem deleteData arm
	// consumes this dep first; 6.C4 may add SABnzbd as a follow-up.
	var _ downloadclients.DiskProvider = (*disk.Provider)(nil)
	// Slice 5.J98 (Phase 5.J seed-config wire-up arc): construct the
	// seed-config chain end-to-end so the J94/J95 RQBit+Flood
	// cleanup-gate predicates flip from "never satisfy (nil binding)"
	// to seed-config-driven in production.
	//
	//   - indexers.CachedSettingsProvider (J93) wraps an
	//     indexers.RepoIndexerFinder around the existing indexersRepo —
	//     maps ErrNotFound → nil so the cached provider's null-handling
	//     matches the C# `?.` chaining. 1-hour rolling cache window.
	//   - download.IndexerSeedConfigProvider (J92) consumes the cached
	//     settings provider and produces TorrentSeedConfiguration with
	//     the SeasonPackSeedGoal / SeedRatio / SeedTime resolution.
	//   - download.SeedConfigProvider (J91) wraps the indexer-side
	//     provider with the dlHistorySvc.GetLatestGrab → ReleaseInfo
	//     parse → IndexerID + FullSeason composition + per-infohash
	//     rolling cache.
	//
	// Compile-time guards confirm the J93→J92→J91 chain assembles
	// cleanly without per-cast surprises at boot.
	var _ indexers.IndexerSettingsLookup = (*indexers.CachedSettingsProvider)(nil)
	var _ download.IndexerSeedConfig = (*download.IndexerSeedConfigProvider)(nil)
	var _ downloadclients.SeedConfigProvider = download.SeedConfigProvider(nil)
	cachedIndexerSettingsProv := indexers.NewCachedSettingsProvider(
		indexers.RepoIndexerFinder{Repo: indexersRepo},
		logger,
	)
	indexerSeedConfigProv := download.NewIndexerSeedConfigProvider(cachedIndexerSettingsProv)
	seedConfigProv := download.NewSeedConfigProvider(dlHistorySvc, indexerSeedConfigProv, logger)
	// Composition-root for download-client Dependencies. Every field
	// is consumed by at least one concrete client:
	//
	//   - HTTPClient: every client's proxy.
	//   - ConfigProvider: read-only access at construction time.
	//   - DiskProvider: NZBGet (6.C3), rTorrent (J111), Aria2 (J112),
	//     SAB (J116), DownloadStation Torrent+Usenet (J124) — clients
	//     whose own API doesn't support delete-with-data fall back to
	//     Sonarr-side DiskProvider.DeleteFolder. DS additionally uses
	//     the J121 SharedFolderResolver to translate the DSM-side
	//     OutputPath to a Sonarr-visible physical path before the
	//     DeleteFolder call.
	//   - Logger: every client.
	//   - StatusService: downloadclientstatus back-off escalation.
	//   - RemotePathMappingService: every RPM-aware client (Phase 6.B
	//     arc — sabnzbd / nzbget / qbittorrent / transmission /
	//     deluge / rqbit / hadouken / nzbvortex / freebox / flood /
	//     tribler).
	//   - SeedConfigProvider: J94 RQBit + J95 Flood cleanup-gate
	//     predicates (slice 5.J98 wired the J91/J92/J93 chain via
	//     the indexer+history composition).
	dcDeps := downloadclients.Dependencies{
		HTTPClient:               httpClient,
		ConfigProvider:           configProv,
		DiskProvider:             diskProv,
		Logger:                   logger,
		StatusService:            downloadClientStatusSvc,
		RemotePathMappingService: remotePathMappingsSvc,
		SeedConfigProvider:       seedConfigProv,
	}
	downloadClientLister := newDownloadClientLister(
		downloadClientsRepo,
		downloadclients.Default(),
		dcDeps,
		logger,
	)
	// J395: queue-delete side-effect adapter — resolves a queue row's
	// tracked download + applies removeFromClient/changeCategory via the
	// live client, then drops the cache row. Embeds queueSvc so it still
	// satisfies apiv3.QueueWriter for the delete handlers.
	queueRemover := &queueRemoveAdapter{
		Service: queueSvc,
		tracked: trackedDownloadsSvc,
		clients: downloadClientResolver{
			rows:     downloadClientsRepo,
			registry: downloadclients.Default(),
			deps:     dcDeps,
		},
		bus: bus,
		log: logger,
	}
	// Audit fix B.5d: now that the deps bundle exists, late-bind the
	// healthcheck DC-status enumerator. Pre-bind it returned an
	// "registry not yet wired" Err per row; post-bind it constructs
	// each enabled client + calls GetStatus on every check run.
	dcStatusEnumerator.SetDeps(downloadclients.Default(), dcDeps)
	// Slice 6.D (Phase 6.D — DecisionPrioritizer wiring): construct
	// *decisionengine.DecisionComparer with three deps.
	//
	//   - ConfigReader   ← configProv (satisfies via DownloadPropersAndRepacks()).
	//   - ComparerProfileLookup
	//                    ← per-call qualityProfilesSvc.Get → NewProfileView.
	//                      Defensive: any lookup/parse failure logs warn
	//                      + returns nil (the comparer degrades to "no
	//                      profile signal" — matches the C# behaviour at
	//                      DecisionComparer.cs:71-95 when the profile
	//                      can't be resolved). context.Background is
	//                      acceptable per the existing precedent at
	//                      qualityComparerAdapter (downloadedimport_wiring.go:854).
	//   - ComparerDelayProfileLookup
	//                    ← per-call delayProfilesSvc.BestForTags. Bool
	//                      false when no profile matches (the comparer
	//                      then skips the delay-tier stage).
	//
	// Closes the 6.A6c deferral that left prioritizer=nil; the
	// orchestrator's ProcessDecisions now sorts by the C# priority
	// chain (quality → custom-format-score → protocol → episode-count
	// → episode-number → indexer-priority → peers → age → size)
	// instead of input order.
	decisionPrioritizer := decisionengine.NewDecisionComparer(
		configProv,
		func(profileID int) *decisionengine.ProfileView {
			profile, err := qualityProfilesSvc.Get(context.Background(), profileID)
			if err != nil || profile == nil {
				logger.Warn("decision-prioritizer: quality profile lookup failed; degrading to no-signal",
					slog.Int("profileId", profileID),
					slog.Any("err", err))
				return nil
			}
			view, err := decisionengine.NewProfileView(profile)
			if err != nil {
				logger.Warn("decision-prioritizer: profile view parse failed; degrading to no-signal",
					slog.Int("profileId", profileID),
					slog.Any("err", err))
				return nil
			}
			return view
		},
		func(tagIDs []int) (delayprofiles.Profile, bool) {
			profile, err := delayProfilesSvc.BestForTags(context.Background(), tagIDs)
			if err != nil || profile == nil {
				return delayprofiles.Profile{}, false
			}
			return *profile, true
		},
	)
	processDownloadDecisions, err := download.NewProcessDownloadDecisions(downloadClientLister, decisionPrioritizer, logger)
	if err != nil {
		return fmt.Errorf("download.ProcessDownloadDecisions: %w", err)
	}
	// J484: ProcessDecisions persists its Pending bucket via the
	// pendingReleasesSvc constructed earlier (SetPendingWriter below) —
	// temporarily-rejected + download-client-unavailable + failed
	// releases reach the PendingReleases table. Ports the C#
	// IPendingReleaseService.AddMany call at ProcessDownloadDecisions.cs:119-121.
	processDownloadDecisions.
		SetBus(bus).
		SetRecorder(downloadClientStatusSvc).
		SetBodyFetcher(&download.HTTPBodyFetcher{}).
		SetPendingWriter(pendingReleasesSvc)

	// J485 (Delay/Pending substrate slice 2): wire the pending-release
	// maintenance handlers — previously the handler functions existed
	// (internal/pendingreleaseshandlers) but were never subscribed, so
	// once J484 began populating the PendingReleases table the rows
	// would never be cleaned. Ports the IHandle declarations on C#
	// PendingReleaseService at PendingReleaseService.cs:660-671:
	//   - EpisodeGrabbedEvent → RemoveGrabbed: clears a pending row once
	//     a matching (or higher-quality) release for the same episodes
	//     actually grabs (the J484 lingering-Delay-row fix).
	//   - SeriesDeletedEvent → DeleteBySeriesIDs: purges a deleted
	//     series' pending rows.
	// RemoveRejected (Handle(RssSyncCompleteEvent), :655-658) stays
	// DEFERRED — the Go RssSyncCompleteEvent carries only counts, not
	// the rejected decisions' (Title,PublishDate,Indexer) tuples
	// RemoveRejected needs; enriching that event is a separate slice.
	messaging.Subscribe(bus, "pendingreleases.HandleEpisodeGrabbed",
		func(ctx context.Context, ev decisionengine.EpisodeGrabbedEvent) error {
			return pendingreleaseshandlers.HandleEpisodeGrabbed(ctx, pendingReleasesSvc, &ev)
		})
	messaging.Subscribe(bus, "pendingreleases.HandleSeriesDeleted",
		func(ctx context.Context, ev series.SeriesDeletedEvent) error {
			return pendingreleaseshandlers.HandleSeriesDeleted(ctx, pendingReleasesSvc, &ev)
		})
	// J490: RemoveRejected on RssSyncCompleteEvent — purge any pending
	// row whose release is now permanently rejected. Completes the
	// pending-maintenance trio (RemoveGrabbed + DeleteBySeriesIDs above).
	// Ports C# PendingReleaseService.Handle(RssSyncCompleteEvent) →
	// RemoveRejected (PendingReleaseService.cs:673-676). The match-tuples
	// ride on the event (populated by the RssSync handler from
	// processed.Rejected).
	messaging.Subscribe(bus, "pendingreleases.HandleRssSyncComplete",
		func(ctx context.Context, ev handlers.RssSyncCompleteEvent) error {
			if len(ev.RejectedReleases) == 0 {
				return nil
			}
			return pendingReleasesSvc.RemoveRejected(ctx, ev.RejectedReleases)
		})
	// Slice 6.A6d (queue [15]): processDownloadDecisions is consumed
	// by the RssSync handler wired below via handlers.Deps.RssSyncGrabber.
	// The 4 setters above establish the dispatch + event-publish +
	// status-record + body-prefetch chain against the 5 production
	// download clients; the RssSync caller drives ProcessDecisions on
	// every scheduled-task tick.

	// Slice 4.BK (sub-arc W6c of queue [W6] — CLOSES [W6]): construct
	// the metadata-writer orchestrator. The 4 concrete writers
	// (xbmc/plex/roksbox/wdtv) register themselves with
	// metadata.Default at init() time via the blank imports near
	// the top of this file. The metadataWriterFactoryAdapter walks
	// the persisted Metadata rows + filters by Enable to fan out
	// across the registered writers.
	//
	// Required deps are wired:
	//   - DiskWriter        ← metadataDiskAdapter over *disk.Provider
	//                         (WriteAllText is a thin os.WriteFile
	//                         shim because *disk.Provider doesn't
	//                         expose a public text-writer today).
	//   - HTTPDownloader    ← metadataHTTPAdapter over httpclient.Client.
	//   - MetadataFileStore ← metadataFileStoreAdapter over
	//                         *metadatafiles.Repo with the new
	//                         AllBySeriesID query + Upsert
	//                         insert-or-update fan-out.
	//   - Attrs             ← metadataAttrsNoop (TODO: real per-file
	//                         chmod helper).
	//   - Bin               ← metadataRecycleBinAdapter over
	//                         *recyclebin.Service.
	//   - OtherRenamer      ← metadataOtherRenamerNoop (no
	//                         other-extras subsystem ports yet).
	//   - Transfer          ← metadataTransferAdapter over
	//                         *disk.Provider.MoveFile.
	//   - Seasons           ← metadataSeasonsAdapter (decodes
	//                         series.Seasons JSON-blob).
	//   - Writers           ← metadataWriterFactoryAdapter over
	//                         *metadata.Repo + metadata.Default.
	//   - Cleaner           ← nil (the C# CleanMetadataService
	//                         hasn't ported; nil-tolerant per W6a).
	metadataRepo, err := metadata.NewRepo(db)
	if err != nil {
		return fmt.Errorf("metadata repo: %w", err)
	}
	// Slice 4.EO: seed the 4 default metadata providers (Xbmc, Wdtv,
	// Roksbox, Plex inserted in that order so the recording's IDs
	// 1/2/3/4 match) on a fresh DB. No-op when rows already exist
	// per the SeedDefaults Implementation-presence guard. Mirrors C#
	// MetadataFactory.InitializeProviders at
	// src/NzbDrone.Core/Extras/Metadata/MetadataFactory.cs:25-48.
	if err := metadataRepo.SeedDefaults(ctx); err != nil {
		return fmt.Errorf("metadata seed defaults: %w", err)
	}
	metadataFilesRepo, err := metadatafiles.NewRepo(db)
	if err != nil {
		return fmt.Errorf("metadatafiles repo: %w", err)
	}
	// Slice 5.J134: build DeleteBadMediaCovers + the now-fully-known
	// houseSvc Task slice here. configProv + metadataFilesRepo were
	// not yet available at the earlier housekeeping-construction
	// block, so the NewService call deferred to this point.
	badMediaCovers, err := housekeeping.NewDeleteBadMediaCovers(
		configProv, metadataFilesRepo, seriesRepo, nil, logger)
	if err != nil {
		return fmt.Errorf("housekeeping badMediaCovers: %w", err)
	}
	houseTasks := []housekeeping.Task{
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
		absPathMetadata,
		dcuPending,
		duplicateMetadata,
		tempUpdate,
		extraFilesExcluded,
		badMediaCovers,
		commandQueueCleanup,
		addlNaming,
		addlUsers,
		updateClean,
		fixIndexerStatus,
		fixDownloadClientStatus,
		fixImportListStatus,
		fixNotificationStatus,
		cleanupTags,
		cleanupQPFormat,
	}
	// J465: TrimLogDatabase — deletes Logs rows older than 7 days
	// (C# TrimLogDatabase.cs → LogRepository.Trim). Registered ONLY when
	// the log DB is enabled (logRepo != nil); that nil-check is the Go
	// port's equivalent of the C# `_configFileProvider.LogDbEnabled` gate.
	if logRepo != nil {
		trimLogDB, err := housekeeping.NewTrimLogDatabase(logRepo, logger)
		if err != nil {
			return fmt.Errorf("housekeeping trimLogDatabase: %w", err)
		}
		houseTasks = append(houseTasks, trimLogDB)
	}
	houseSvc, err = housekeeping.NewService(houseTasks, logger)
	if err != nil {
		return fmt.Errorf("housekeeping service: %w", err)
	}
	// J472: MapCoversToLocal feeds the metadata writers' series-cover
	// image path (Xbmc/Roksbox/Wdtv SeriesImages → folder.jpg). The
	// providers cast deps.MapCoversToLocal to a CoverPathLookup
	// (GetCoverPath); a nil dep silently yields no series images. The
	// field was left unset here ("bound when the cover-cache service
	// lands"); mediaCoverSvc (a *mediacover.Service with GetCoverPath,
	// built above) is that service. Mirrors C# IMapCoversToLocal injected
	// into the metadata consumers.
	metadataWriterDeps := metadata.Dependencies{
		HTTPClient:       httpClient,
		DiskProvider:     diskProv,
		MapCoversToLocal: mediaCoverSvc,
		// J473: EpisodeLookup feeds the writers' EpisodeImages (episode
		// thumbnails) — *episodes.Repo.AllByFileID. FolderLister feeds
		// Roksbox/Wdtv SeasonImages (season posters) — *disk.Provider.
		// GetDirectories. Both were left nil ("queue [W6]"); the providers
		// cast them non-panickingly, so a nil silently dropped those
		// images. Mirror C# episodeFile.Episodes.Value + IDiskProvider.
		EpisodeLookup: episodesRepo,
		FolderLister:  diskProv,
		Logger:        logger,
	}
	metadataSvc, err := metadataservice.NewService(metadataservice.Deps{
		Disk:         newMetadataDiskAdapter(diskProv),
		HTTP:         newMetadataHTTPAdapter(httpClient),
		Files:        newMetadataFileStoreAdapter(metadataFilesRepo),
		Attrs:        metadataAttrsNoop{},
		Bin:          newMetadataRecycleBinAdapter(recycleBinSvc),
		OtherRenamer: metadataOtherRenamerNoop{},
		Transfer:     newMetadataTransferAdapter(diskProv),
		Seasons:      metadataSeasonsAdapter{},
		Writers:      newMetadataWriterFactoryAdapter(metadataRepo, metadataWriterDeps),
		Log:          logger,
	})
	if err != nil {
		return fmt.Errorf("metadataservice: %w", err)
	}
	// Q1 (J435): drive the metadata writer from the ported subset of
	// the events C# ExtraService subscribes to. See
	// subscribeMetadataWriters for the event→method map + the three
	// not-yet-ported events still pending.
	subscribeMetadataWriters(bus, metadataSvc, episodeFilesSvc, logger)

	registry := commands.NewRegistry()
	queue := commands.NewQueue(registry.Properties)
	cmdSvc.SetQueue(queue)
	// Slice 5.J37 (audit B.3): construct the downloadmonitoring.Service
	// + thread into handlers.Deps so RefreshMonitoredDownloads no
	// longer no-ops on the 1-minute scheduled tick. Service polls
	// every enabled download client's GetItems() across both
	// protocols and publishes QueueUpdatedEvent +
	// TrackedDownloadRefreshedEvent on each refresh — the
	// signalr_wiring already subscribes the queue/queue-details Sync
	// broadcaster off those events, so the React UI's queue list
	// refreshes on cadence without manual reload.
	downloadMonitoringSvc, err := downloadmonitoring.NewService(downloadClientLister, bus, logger)
	if err != nil {
		return fmt.Errorf("downloadmonitoring service: %w", err)
	}
	// J394: populate the tracked-download cache (and thus the queue) from
	// each Refresh poll by mapping polled items → TrackedDownloads.
	downloadTracker := downloadItemTrackerAdapter{
		parsing: parsingSvc,
		tracked: trackedDownloadsSvc,
		log:     logger,
	}
	downloadMonitoringSvc.SetTracker(downloadTracker)

	// J474: refresh tracked downloads' cached RemoteEpisode when a series
	// is edited or deleted, so the queue/activity view reflects the new
	// title instead of the stale one captured at track time. Ports the C#
	// TrackedDownloadService IHandle<SeriesEditedEvent>/<SeriesDeletedEvent>
	// fan-out (TrackedDownloadService.cs:310-360). Publishes
	// TrackedDownloadRefreshedEvent so the SignalR/queue layer re-renders.
	messaging.Subscribe(bus, "trackeddownloads.RefreshOnSeriesEdited",
		func(ctx context.Context, ev series.SeriesEditedEvent) error {
			if ev.Series == nil {
				return nil
			}
			edited := ev.Series
			if downloadTracker.RefreshTrackedForSeries(ctx, func(id, tvdbID int) bool {
				return id == edited.ID || (edited.TvdbID != 0 && tvdbID == edited.TvdbID)
			}) {
				_ = messaging.Publish(ctx, bus, trackeddownloads.TrackedDownloadRefreshedEvent{
					TrackedDownloads: trackedDownloadsSvc.GetTrackedDownloads(),
				})
			}
			return nil
		})
	messaging.Subscribe(bus, "trackeddownloads.RefreshOnSeriesDeleted",
		func(ctx context.Context, ev series.SeriesDeletedEvent) error {
			if len(ev.Series) == 0 {
				return nil
			}
			ids := make(map[int]bool, len(ev.Series))
			tvdbs := make(map[int]bool, len(ev.Series))
			for i := range ev.Series {
				ids[ev.Series[i].ID] = true
				if ev.Series[i].TvdbID != 0 {
					tvdbs[ev.Series[i].TvdbID] = true
				}
			}
			if downloadTracker.RefreshTrackedForSeries(ctx, func(id, tvdbID int) bool {
				return ids[id] || (tvdbID != 0 && tvdbs[tvdbID])
			}) {
				_ = messaging.Publish(ctx, bus, trackeddownloads.TrackedDownloadRefreshedEvent{
					TrackedDownloads: trackedDownloadsSvc.GetTrackedDownloads(),
				})
			}
			return nil
		})
	// J475: complete the family — SeriesBulkEdited (same multi-series
	// match as delete) + SeriesAdded (re-resolve nil-series or tvdb-match,
	// C# TrackedDownloadService.cs:294-336).
	messaging.Subscribe(bus, "trackeddownloads.RefreshOnSeriesBulkEdited",
		func(ctx context.Context, ev series.SeriesBulkEditedEvent) error {
			if len(ev.Series) == 0 {
				return nil
			}
			ids := make(map[int]bool, len(ev.Series))
			tvdbs := make(map[int]bool, len(ev.Series))
			for i := range ev.Series {
				ids[ev.Series[i].ID] = true
				if ev.Series[i].TvdbID != 0 {
					tvdbs[ev.Series[i].TvdbID] = true
				}
			}
			if downloadTracker.RefreshTrackedForSeries(ctx, func(id, tvdbID int) bool {
				return ids[id] || (tvdbID != 0 && tvdbs[tvdbID])
			}) {
				_ = messaging.Publish(ctx, bus, trackeddownloads.TrackedDownloadRefreshedEvent{
					TrackedDownloads: trackedDownloadsSvc.GetTrackedDownloads(),
				})
			}
			return nil
		})
	messaging.Subscribe(bus, "trackeddownloads.RefreshOnSeriesAdded",
		func(ctx context.Context, ev series.SeriesAddedEvent) error {
			if ev.Series == nil {
				return nil
			}
			if downloadTracker.RefreshTrackedForAddedSeries(ctx, ev.Series.TvdbID) {
				_ = messaging.Publish(ctx, bus, trackeddownloads.TrackedDownloadRefreshedEvent{
					TrackedDownloads: trackedDownloadsSvc.GetTrackedDownloads(),
				})
			}
			return nil
		})
	// J399: auto-import completed downloads. The processor reads the
	// tracked-download cache the tracker populated above + imports
	// completed ones via the existing downloadedimport pipeline. Run on
	// every Refresh cycle (SetProcessor) + registered as the
	// ProcessMonitoredDownloads command for manual/API trigger.
	downloadProcessingSvc, err := downloadprocessing.NewService(trackedDownloadsSvc, dimpSvc, logger)
	if err != nil {
		return fmt.Errorf("downloadprocessing service: %w", err)
	}
	// J400: the failure arm — a tracked download the client reports
	// failed (or that came back encrypted) and matched a series is
	// marked Failed and a DownloadFailedEvent is published (feeding the
	// already-subscribed blocklist + history handlers, same event as
	// the queue-delete blocklist arm). No publisher → arm no-ops.
	downloadProcessingSvc.SetFailurePublisher(downloadFailedPublisher{bus: bus})
	// J449: the completion arm — when every episode of a completed
	// download imports, the tracked download is marked Imported and a
	// DownloadCompletedEvent is published (feeding the already-subscribed
	// downloadhistory handler → DownloadImported audit row, + future
	// notification/metadata subscribers). No publisher → import is silent
	// (prior J399 behavior). Ports CompletedDownloadService.cs:229-234.
	downloadProcessingSvc.SetCompletionPublisher(downloadCompletedPublisher{bus: bus})
	// J454: publish a DownloadsProcessedEvent at the end of every process
	// pass so the metadata writer's CreateAfterEpisodesImported batch
	// fires once per pass per imported series (ports DownloadProcessingService.cs:80).
	downloadProcessingSvc.SetProcessedPublisher(downloadsProcessedPublisher{bus: bus})
	// J458: RemoveCompletedDownloads sweep — at the end of each pass,
	// publish DownloadCanBeRemovedEvent for every Imported + now-removable
	// tracked download (the deferred-removal path for items that were
	// still seeding at completion). Ports DownloadProcessingService.cs:35-45.
	downloadProcessingSvc.SetCanBeRemovedPublisher(downloadCanBeRemovedPublisher{bus: bus})
	downloadMonitoringSvc.SetProcessor(downloadProcessingSvc)

	// J455: DownloadEventHub completion arm. On DownloadCompletedEvent
	// (now produced by J449/J450), tell the originating client the item
	// was imported (e.g. qBittorrent post-import category move) and, when
	// the client's RemoveCompletedDownloads flag is set + the item is in a
	// removable state, remove it + StopTracking. Ports
	// DownloadEventHub.Handle(DownloadCompletedEvent).
	downloadEventHub, err := downloadeventhub.NewService(
		downloadClientResolver{rows: downloadClientsRepo, registry: downloadclients.Default(), deps: dcDeps},
		downloadClientsRepo,
		trackedDownloadsSvc,
		logger,
	)
	if err != nil {
		return fmt.Errorf("downloadeventhub service: %w", err)
	}
	messaging.Subscribe(bus, "downloadeventhub.HandleDownloadCompleted",
		func(ctx context.Context, ev trackeddownloads.DownloadCompletedEvent) error {
			return downloadEventHub.HandleDownloadCompleted(ctx, &ev)
		})
	// J457: DownloadEventHub failed arm — when the client's
	// RemoveFailedDownloads flag is set, remove an auto-failed download
	// from the client + StopTracking. Closes the gap where the Go failure
	// arm left failed downloads in the client. Ports
	// DownloadEventHub.Handle(DownloadFailedEvent).
	messaging.Subscribe(bus, "downloadeventhub.HandleDownloadFailed",
		func(ctx context.Context, ev trackeddownloads.DownloadFailedEvent) error {
			return downloadEventHub.HandleDownloadFailed(ctx, &ev)
		})
	// J458: deferred-removal arm — the RemoveCompletedDownloads sweep's
	// DownloadCanBeRemovedEvent removes a now-removable Imported download.
	messaging.Subscribe(bus, "downloadeventhub.HandleDownloadCanBeRemoved",
		func(ctx context.Context, ev trackeddownloads.DownloadCanBeRemovedEvent) error {
			return downloadEventHub.HandleDownloadCanBeRemoved(ctx, &ev)
		})

	// J401: auto-redownload-on-failure handler (ports
	// RedownloadFailedDownloadService). Subscribes to the same
	// DownloadFailedEvent the failure arm + queue-delete blocklist arm
	// publish and, when AutoRedownloadFailed is enabled and the user
	// didn't opt out (SkipRedownload), re-searches for a replacement —
	// per-episode for a single/double, season-search when the whole
	// season failed. Subscribed AFTER the history + blocklist
	// DownloadFailed handlers to mirror C# [EventHandleOrder.Last].
	redownloadSvc, err := redownload.NewService(
		configProv,
		episodesSvc,
		episodeRefreshedSearcherAdapter{svc: indexerSearchSvc, grabber: processDownloadDecisions, log: logger},
		redownloadSeasonSearchAdapter{svc: indexerSearchSvc, grabber: processDownloadDecisions, log: logger},
		logger,
	)
	if err != nil {
		return fmt.Errorf("redownload service: %w", err)
	}
	messaging.Subscribe(bus, "redownload.HandleDownloadFailed",
		func(ctx context.Context, ev trackeddownloads.DownloadFailedEvent) error {
			return redownloadSvc.Handle(ctx, ev)
		})

	// Slice 5.J39 (audit B.3 close-out): adapter around
	// indexerSearchSvc.EpisodeSearch that returns the decision count
	// only — narrows the surface the MissingEpisodeSearch handler
	// consumes. SearchSettings zero value (no Manual override; same
	// as the C# bulk missing-search default).
	episodeSearchAdapter := handlerEpisodeSearchAdapter{svc: indexerSearchSvc, grabber: processDownloadDecisions}

	// Slice 5.J40's update.Service + handlers.UpdateChecker adapter
	// is constructed earlier (around the healthSvc-build site,
	// relocated in slice 5.J143 so the same updateSvc satisfies both
	// the ApplicationCheckUpdate handler AND the UpdateCheck
	// healthcheck's UpdateAvailableLookup).
	updateChecker := updateCheckerAdapter{svc: updateSvc}

	// Slice 5.J183 closes the I4c sub-arc that slice 4.AQ flagged
	// as deferred — wires the importlists.SyncService against the
	// existing importLists* dependencies + small inline adapters
	// for seriesLookup (over seriesSvc.AllSeriesTvdbIDs) and
	// seriesAdder (a closure that loops series.Service.Insert
	// per-row + absorbs per-row failures, matching the C# `:261`
	// ignoreErrors=true contract). The Factory + SyncService now
	// satisfy the ImportListSync command handler's runner dep so the
	// J181 stub flips to a real Sync.
	//
	// Registry caveat: importlists.Default() returns the package-
	// global Registry; concrete provider packages
	// (anilist/myanimelist/plex/plexrss/sonarr/trakt/custom/simkl)
	// self-register via their init() blocks BUT only when their
	// packages are imported into the binary's link graph. The
	// cmd/sonarr binary does NOT currently blank-import any of them
	// (same pattern as the notifications family — also unimported at
	// HEAD), so the registry is effectively empty at runtime.
	// SyncService.Sync therefore falls through to the
	// `no automatic-add lists enabled` debug log + clean exit on
	// every scheduler tick. Once a follow-up slice adds the blank
	// imports (separate concern that also activates the notifications
	// registry), the scheduler-fired Sync starts processing real
	// persisted ImportList rows.
	//
	// IDTranslator (J444): items whose TvdbID is zero but which carry an
	// IMDb/TMDb/AniList/MyAnimeList id are resolved against the
	// metadata-source proxy (importListIDTranslator) before adding —
	// ports the C# ImportListSyncService translation chain.
	importListSyncFactory, err := importlists.NewFactory(
		importListsRepo,
		importlists.Default(),
		importListStatusSvc,
		importlists.Dependencies{
			HTTPClient:      httpClient,
			ConfigProvider:  configProv,
			Logger:          logger,
			StatusService:   importListStatusSvc,
			ParsingService:  parsingSvc,
			ImportListsRepo: importListsRepo,
		},
		logger,
	)
	if err != nil {
		return fmt.Errorf("importlists factory: %w", err)
	}
	importListSyncSvc, err := importlists.NewSyncService(
		importListSyncFactory,
		importListExclusionsRepo,
		seriesLookupAdapter{svc: seriesSvc},
		seriesAdderAdapter{
			svc:        seriesSvc,
			namingCfg:  namingConfigSvc,
			validator:  series.NewAddValidator(seriesRepo, seriesRepo),
			seriesInfo: metadataSourceProxy,
			episodes:   episodesRepo,
			log:        logger,
		},
		importListIDTranslator{search: metadataSourceProxy, log: logger}, // G7 sub-gap (J444): non-TVDB items resolve via the metadata source.
		logger,
	)
	if err != nil {
		return fmt.Errorf("importlists sync service: %w", err)
	}

	// J500 (queue [I4c]): wire per-list ImportListItems persistence +
	// sync-status bookkeeping into the orchestrator. Ports the C#
	// FetchAndParseImportListService per-list SyncSeriesForList +
	// UpdateListSyncStatus calls. Without these the fetched candidates
	// were aggregated in memory only and never persisted, so the
	// clean-library trigger (J501) would have read an empty table and
	// mass-unmonitored every series. importListStatusSvc already
	// satisfies the listStatusUpdater interface (UpdateListSyncStatus).
	importListItemsRepo, err := importlistitems.NewRepo(db)
	if err != nil {
		return fmt.Errorf("importlistitems repo: %w", err)
	}
	importListItemsSvc, err := importlistitems.NewService(importListItemsRepo)
	if err != nil {
		return fmt.Errorf("importlistitems service: %w", err)
	}
	importListSyncSvc.SetItemPersister(importListItemPersister{svc: importListItemsSvc})
	importListSyncSvc.SetListStatusUpdater(importListStatusSvc)

	// J501 (queue [I4c]): wire the clean-library deps. Ports C#
	// ImportListSyncService.TryCleanLibrary/CleanLibrary — after a clean
	// all-lists sync with a pending removal, series no longer on any
	// list are unmonitored / tagged / logged per config.ListSyncLevel
	// (default Disabled → full no-op). NEVER deletes series rows.
	// configProv satisfies cleanLibraryConfig (ListSyncLevel/ListSyncTag);
	// importListStatusSvc satisfies cleanStatusGate
	// (AllListsSuccessfulWithAPendingClean/MarkListsAsCleaned).
	importListSyncSvc.SetCleanLibrary(
		configProv,
		importListItemLister{svc: importListItemsSvc},
		importListSeriesCleaner{svc: seriesSvc},
		importListStatusSvc,
	)

	// Slice 5.J191 wires the RefreshSeries handler with a real
	// refreshseries.Service. The service satisfies
	// handlers.RefreshSeriesRunner structurally via Refresh(ctx, id)
	// error.
	refreshSeriesSvc, err := refreshseries.NewService(metadataSourceProxy, seriesSvc, bus, nil, logger)
	if err != nil {
		return fmt.Errorf("refreshseries service: %w", err)
	}
	// J195 — wire the ShouldRefresh predicate's episode-lookup dep
	// so the all-series sweep gates per-row refresh on staleness.
	refreshSeriesSvc.SetEpisodeLookup(episodesRepo)
	// J197 — wire the disk-rescan-after-refresh arm. mediafilesSvc
	// satisfies refreshseries.Rescanner via Scan(ctx, *series.Series);
	// configProv exposes RescanAfterRefresh() returning the C# enum
	// string ("Always" / "AfterManual" / "Never").
	refreshSeriesSvc.SetRescanner(mediafilesSvc, configProv)
	// J200 — wire the episode-reconcile arm. The refresher reads
	// existing rows via episodesSvc and persists the diff via the
	// same service's UpdateMany/InsertMany/DeleteMany surface; the
	// bus carries the EpisodeInfoRefreshedEvent.
	refreshEpisodesSvc, err := refreshepisodes.NewService(episodesSvc, bus, nil, logger)
	if err != nil {
		return fmt.Errorf("refreshepisodes service: %w", err)
	}
	refreshSeriesSvc.SetEpisodeRefresher(refreshEpisodesSvc)

	// J210 — wire episoderefreshed.Service at the composition root.
	// Subscribes Handle to the bus's EpisodeInfoRefreshedEvent (which
	// refreshEpisodesSvc just started publishing at J200). seriesSvc
	// satisfies SeriesLookup via GetSeries; episodesSvc satisfies
	// EpisodeLookup via GetEpisode. The EpisodeSearcher is a slice-
	// taking wrapper around *indexersearch.Service.EpisodeSearch (the
	// per-episode loop matches what J39's MissingEpisodeSearch handler
	// already does internally — see episodeRefreshedSearcherAdapter
	// below).
	episodeRefreshedSvc, err := episoderefreshed.NewService(
		bus,
		seriesSvc,
		episodesSvc,
		episodeRefreshedSearcherAdapter{svc: indexerSearchSvc, grabber: processDownloadDecisions, log: logger},
		nil,
		logger,
	)
	if err != nil {
		return fmt.Errorf("episoderefreshed service: %w", err)
	}

	// J211 — invoke episoderefreshed.Service.Search on every
	// SeriesScanned[Skipped]Event the mediafiles package emits. Mirrors
	// C# Tv/SeriesScannedHandler.cs:36-44 (the AddOptions==null branch
	// of HandleScanEvents). Both events route to the same Search call
	// in C# via the dual `IHandle<SeriesScannedEvent>` +
	// `IHandle<SeriesScanSkippedEvent>` interfaces on
	// SeriesScannedHandler. The Go-port's mediafiles.Service already
	// publishes both — completedScanning at service.go:367 emits
	// SeriesScannedEvent on the happy path, publishSkipped at
	// service.go:380 emits SeriesScanSkippedEvent on the
	// root-folder-missing/empty early-out paths.
	//
	// The AddOptions gate is upstream — episoderefreshed.Service.Handle
	// short-circuits the cache write for AddOptions-set series (J209),
	// so Search finds an empty cache and no-ops for those rows without
	// any subscriber-side gate needed.
	//
	// The C# SeriesScannedHandler also runs post-add actions when
	// AddOptions IS set (:47-73 — set monitored status, publish
	// SeriesAddCompletedEvent, push per-add search commands, clear
	// AddOptions). That branch is DEFERRED to its own slice — it
	// touches an entirely different code path (episode-monitored
	// service + per-add search dispatcher + SeriesAddCompletedEvent),
	// none of which are pre-requisites for the J207-J210
	// rescan-to-search chain that J211 closes.
	messaging.Subscribe(bus, "episoderefreshed.SearchOnSeriesScanned",
		func(ctx context.Context, ev mediafiles.SeriesScannedEvent) error {
			if ev.Series == nil {
				return nil
			}
			return episodeRefreshedSvc.Search(ctx, ev.Series.ID)
		})
	messaging.Subscribe(bus, "episoderefreshed.SearchOnSeriesScanSkipped",
		func(ctx context.Context, ev mediafiles.SeriesScanSkippedEvent) error {
			if ev.Series == nil {
				return nil
			}
			return episodeRefreshedSvc.Search(ctx, ev.Series.ID)
		})

	// J216 — SeriesScannedHandler post-add-actions arm (C# AddOptions!=null
	// branch of HandleScanEvents at SeriesScannedHandler.cs:47-73).
	// seriesadd.Service runs the 4-step sequence when a freshly-added
	// series completes its first scan: (1) episode monitored cascade
	// via episodemonitored.Service (J213); (2) publish
	// series.SeriesAddCompletedEvent; (3) push the right search
	// command based on AddOptions search flags (SeriesSearch when both
	// flags set per J215, MissingEpisodeSearch when only-Missing per
	// J39, CutoffUnmet-only branch DEFERRED — handler not yet ported);
	// (4) clear AddOptions via series.Service.RemoveAddOptions.
	// AddOptions == nil branch is independently handled by the J211
	// episoderefreshed subscribers above — each subscriber gates the
	// opposite way so the two paths never double-handle.
	//
	// Construct a separate episodemonitored.Service instance here for
	// the seriesadd wiring — the J214 instance lives in host_wiring.go
	// v3Routes scope. episodemonitored has no internal state so a
	// per-consumer instance is fine; per the J214 doc-comment.
	postAddCascader, err := episodemonitored.NewService(episodesSvc, seriesSvc, nil, logger)
	if err != nil {
		return fmt.Errorf("seriesadd cascader: %w", err)
	}
	seriesAddSvc, err := seriesadd.NewService(postAddCascader, cmdSvc, seriesSvc, bus, logger)
	if err != nil {
		return fmt.Errorf("seriesadd service: %w", err)
	}
	messaging.Subscribe(bus, "seriesadd.HandleSeriesScanned",
		seriesAddSvc.HandleSeriesScanned)
	messaging.Subscribe(bus, "seriesadd.HandleSeriesScanSkipped",
		seriesAddSvc.HandleSeriesScanSkipped)

	// J390/J391: the EpisodeFile delete adapter — recycle-bin + disk
	// delete before the DB row removal (ports MediaFileDeletionService).
	// Embeds episodeFilesSvc so it still satisfies apiv3.EpisodeFileWriter;
	// the API delete handlers discover the disk arm via type-assertion,
	// and the DeleteSeriesFiles command handler calls DeleteSeriesFiles.
	episodeFileDeleter := &episodeFileDeleteAdapter{
		Service:     episodeFilesSvc,
		series:      seriesSvc,
		rootFolders: rootFoldersSvc,
		recycleBin:  recycleBinSvc,
		disk:        diskProv,
		log:         logger,
	}

	// J393 (punch-list #6): MoveSeries + BulkMoveSeries command mover —
	// disk folder move + revert-on-failure (ports MoveSeriesService).
	seriesMover := &seriesMoveAdapter{
		series: seriesSvc,
		naming: namingConfigSvc,
		disk:   diskProv,
		log:    logger,
	}

	// ClearLog is wired only when the log DB is enabled (logRepo non-nil);
	// a typed-nil would register a handler that panics on Purge. Mirrors
	// the C# ClearLog command which requires the log database.
	var logClearer handlers.LogClearer
	if logRepo != nil {
		logClearer = logRepo
	}

	if err := handlers.RegisterAll(registry, handlers.Deps{
		Commands:               cmdSvc,
		Housekeeping:           houseSvc,
		HealthCheck:            healthSvc,
		RescanSeries:           mediafilesSvc,
		Backup:                 backupSvc,
		Lifecycle:              lifecycleSvc,
		ApplicationCheckUpdate: updateChecker,
		MissingEpisodeLister:   episodesSvc,
		MissingEpisodeSearcher: episodeSearchAdapter,
		// J362 — CutoffUnmetEpisodeSearch handler. The lister computes
		// the per-profile below-cutoff sets + enumerates via
		// episodes.Repo.WantedCutoffUnmet (the J360/J361 surface); the
		// per-episode dispatcher reuses episodeSearchAdapter
		// (MissingEpisodeSearcher).
		CutoffUnmetEpisodeLister: cutoffUnmetSearchLister{episodes: episodesRepo, profiles: qualityProfilesSvc},
		// J215 — SeriesSearchCommand handler deps. seriesSvc satisfies
		// SeriesSearchLookup via GetSeries; episodesSvc satisfies
		// SeriesSearchEpisodesLister via GetEpisodesBySeries;
		// MissingEpisodeSearcher above (the per-episode adapter)
		// doubles as the per-episode dispatcher for the no-seasons-
		// monitored branch. seasonSearchAdapter wraps
		// *indexersearch.Service.SeasonSearch into the decisions-count
		// SeasonSearcher shape — see the type at the bottom of this file.
		SeriesSearchLookup:         seriesSvc,
		SeriesSearchEpisodes:       episodesSvc,
		SeriesSeasonSearcher:       seasonSearchAdapter{svc: indexerSearchSvc, grabber: processDownloadDecisions},
		MonitoredDownloadRefresher: downloadMonitoringSvc,
		RssSync:                    indexerSearchSvc,
		RssSyncBus:                 bus,
		RssSyncDecider:             downloadDecider,
		// Slice 6.A6d (queue [15]): the grab orchestrator (constructed
		// above) consumes the decider's output. *download.ProcessDownloadDecisions
		// satisfies handlers.GrabProcessor via Go's structural typing.
		RssSyncGrabber: processDownloadDecisions,
		// Slice 4.AZ (sub-arc M6b-ii of queue [M6]): wire the
		// UpdateSceneMapping command handler against
		// *scenemappings.Service. The Service satisfies
		// handlers.SceneMappingUpdater via the UpdateMappings(ctx)
		// method. Scheduled-task entry fires every 3h matching
		// C# TaskManager.cs:90.
		UpdateSceneMapping: scenemappingsSvc,
		// Slice 5.J183 closes the I4c stub (J181) by binding the
		// scheduled ImportListSync ticks to the real
		// importlists.SyncService. *importlists.SyncService satisfies
		// handlers.ImportListSyncRunner structurally via Sync(ctx).
		// See the importListSyncSvc construction above for the
		// registry-empty-at-runtime caveat — Sync currently no-ops
		// until concrete provider packages are blank-imported.
		ImportListSync: importListSyncSvc,
		// Slice 5.J191 — promotes the J166 RefreshSeries stub to the
		// real per-id refresh path. Closes audit row B.3.
		// *refreshseries.Service satisfies handlers.RefreshSeriesRunner
		// structurally via Refresh(ctx, seriesID) error.
		RefreshSeries: refreshSeriesSvc,
		DownloadedEpisodesScan: handlers.DownloadedEpisodesScanDeps{
			// Processor REQUIRED — slice 3.80 contract: nil Processor
			// → handler not registered. We always wire the real
			// downloadedimport.Service here so the API surface is
			// always live; the upstream stubs are inside the service
			// itself, not at the handler boundary.
			Processor: dimpSvc,
			// Slice 3.AI: Finder wired to trackeddownloads.Service.
			// The handler reaches for it via the slice-3.80 narrow
			// TrackedDownloadFinder.Find(downloadID) signature; the
			// real Service.Find returns nil for any unknown
			// downloadID (cache starts empty, populates only when
			// future Phase 4 download-client wiring calls Track).
			Finder: trackedDownloadsSvc,
			// Verifier / Reporter remain nil — those slots gate on
			// the completed-download lifecycle / ICommandResultReporter
			// ports respectively. Each is documented optional at
			// slice-3.80's handler definition; the handler tolerates
			// nil for both.
		},
		// Slice 5.J386 (punch-list #2) — ManualImport execute command.
		// All four core deps satisfy the handler's narrow interfaces
		// structurally: seriesSvc.GetSeries, episodesSvc.GetEpisodes,
		// aggregationSvc.Augment, importApprovedSvc.Import. Finder reuses
		// the same trackeddownloads.Service the DownloadedEpisodesScan
		// slot wires (nil for any unknown downloadId until Phase 4
		// download-client wiring populates the cache).
		ManualImport: handlers.ManualImportDeps{
			SeriesGetter:  seriesSvc,
			EpisodeGetter: episodesSvc,
			Aggregator:    aggregationSvc,
			Importer:      importApprovedSvc,
			Finder:        trackedDownloadsSvc,
			// J450: publish post-import completion events
			// (Download/UntrackedDownloadCompletedEvent) so a manual
			// import drives the same history + notification consumers a
			// download-client completion does.
			Publisher: manualImportCompletionPublisher{bus: bus},
			// J451: delete the tracked download's output folder after a
			// manual import when it's safe + the client allows moves.
			Cleaner: manualImportFolderCleaner{disk: diskProv, dimp: dimpSvc},
		},
		// J391 (punch-list #6): DeleteSeriesFiles command → the shared
		// recycle-bin delete adapter.
		DeleteSeriesFiles: episodeFileDeleter,
		// J392 (punch-list #6): Rename{Files,Series} commands. series +
		// episodefiles services + the episodefilemoving mover all satisfy
		// the narrow RenameDeps interfaces structurally.
		Rename: handlers.RenameDeps{
			Series:  seriesSvc,
			Files:   episodeFilesSvc,
			Mover:   episodeFileMovingSvc,
			Updater: episodeFilesSvc,
			// J453: publish per-file EpisodeFileRenamedEvent (→ history
			// "renamed" row) + per-series SeriesRenamedEvent (→ metadata
			// MoveFilesAfterRename). Without this the rename moved files
			// silently and both consumers stayed dormant.
			Publisher: renameEventPublisher{bus: bus},
			// J463: prune empty subfolders left after renames
			// (C# RenameEpisodeFileService.cs:173). *disk.Provider.
			Disk: diskProv,
		},
		// J393 (punch-list #6): MoveSeries/BulkMoveSeries commands.
		SeriesMover: seriesMover,
		// J398 (punch-list #6): ClearBlocklist command → blocklist.Service.Clear.
		ClearBlocklist: blocklistSvc,
		// J402 (punch-list #6): ResetQualityDefinitions command →
		// qualitydefinitions.Service.ResetTitles.
		ResetQualityDefinitions: qualityDefSvc,
		// J410 (punch-list #6): ClearLog command → logs.Repo.Purge.
		// nil when the log DB is disabled (skips registration).
		ClearLog: logClearer,
		// G2 (J421): CleanUpRecycleBin scheduled task → recyclebin.Service.
		// Cleanup. Was unset → the 24h task ran a nil no-op stub.
		CleanUpRecycleBin: recycleBinSvc,
		// J399 (Phase-4 auto-import): ProcessMonitoredDownloads command.
		ProcessMonitoredDownloads: downloadProcessingSvc,
		// J446: DeleteLogFiles / DeleteUpdateLogFiles commands (Settings→
		// General log-deletion buttons) — empty the app log folders.
		LogFolderCleaner: diskProv,
		LogFolder:        filepath.Join(folder.AppData, "logs"),
		UpdateLogFolder:  filepath.Join(folder.AppData, "UpdateLogs"),
		Log:              logger,
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
	// Slice 4.DF adds the HTTP host as a third goroutine — same
	// lifecycle (cancel ctx → graceful drain → workers.Wait).
	workerCtx, workerCancel := context.WithCancel(ctx)
	defer workerCancel()
	var workers sync.WaitGroup
	workers.Add(3)
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

	// SystemStatusContext (slice 4.EA) — boot-static snapshot the
	// /api/v3/system/status handler bakes into every response.
	// Built here after the database is migrated so MigrationVersion
	// + DatabaseVersion are queryable.
	systemStatus, err := buildSystemStatusContext(ctx, db, folder, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("system status context: %w", err)
	}

	// SignalR hub (slice 4.EU). The hub is the in-process fan-out
	// pipeline that owns the live WebSocket connection registry.
	// hostVersion is sourced from buildinfo so the connection-
	// handshake version event matches the /api/v3/system/status
	// version field.
	signalRHub := signalr.NewHub(signalr.WithHostVersion(buildinfo.Version))
	// wireSignalRSubscribers returns the queue/status debouncer
	// (slice 4.FH): the GET /api/v3/queue/status handler brackets
	// its projection in Pause/Resume on this shared instance so
	// the read doesn't re-arm the broadcaster timer with the
	// snapshot it already serves.
	// J341: SignalR EpisodeFile broadcasters route the projected
	// resource through the J339 populator wrapper so subscribers
	// (the React UI's real-time refresh path) see the same
	// populated customFormats[] + customFormatScore as the
	// /api/v3/episodefile HTTP path. Uses the same wrapper as
	// v3Routes — constructed once here at the main.go call site
	// since wireSignalRSubscribers is outside buildHostServer's
	// scope.
	signalRepFilePop := &episodeFileServiceWithPopulator{
		Service:            episodeFilesSvc,
		seriesSvc:          seriesSvc,
		qualityProfilesSvc: qualityProfilesSvc,
		customFormatsSvc:   customFormatsSvc,
	}
	queueStatusDebouncer := wireSignalRSubscribers(bus, signalRHub, logger, &QueueStatusBroadcastDeps{
		Queue:   queueSvc,
		Pending: pendingCountLister{repo: pendingReleasesRepo},
	}, seriesSvc, signalRepFilePop)
	// Lift to apiv3.Pauser only when non-nil — a typed-nil
	// *signalr.Debouncer wrapped in an interface would survive the
	// `if pauser != nil` guard inside QueueStatusHandler and panic on
	// the first method call.
	var queueStatusPauser apiv3.Pauser
	if queueStatusDebouncer != nil {
		queueStatusPauser = queueStatusDebouncer
	}

	// Slice 4.FD: optional frontend-bundle mount. `--frontenddir=
	// /path/to/frontend/build` makes the host serve the React shell
	// under `/` with SPA-fallback routing (per slice 4.FC). When
	// the flag is unset, the host falls back to the embedded
	// bundle in internal/frontend (slice 4.FG) — the shipped
	// placeholder explains itself; production builds copy the
	// real frontend/build/* into go/internal/frontend/bundle/
	// before `go build`.
	frontendDir, _ := startup.Arg(appenv.StartupArgFrontendDir)

	backupTempPath := folder.Temp
	if backupTempPath == "" {
		backupTempPath = os.TempDir()
	}
	releaseCache := releasecache.New(0) // 30-min default TTL per J237 / C# `ReleaseController.cs:240`.
	// J356: the ManualImport reprocess GetDecision rejector — assembles a
	// LocalEpisode from a reprocessed item, runs the production
	// AggregationService + ImportDecisionMaker, and projects the
	// rejections. Threaded into buildHostServer where it's attached to the
	// parsingServiceWithPopulator (the reprocess handler's calc dep)
	// and discovered via optional-extension type-assertion.
	manualImportDecisionRejector := manualImportDecisionAdapter{
		idm:          importDecisionMaker,
		agg:          aggregationSvc,
		series:       seriesSvc,
		episodes:     episodesSvc,
		episodeFiles: episodeFilesSvc,
		tracked:      trackedDownloadsSvc,
		disk:         diskProv,
		parsing:      parsingSvc,
		log:          logger,
	}
	hostSrv, err := buildHostServer(ctx, cfg, authSvc, indexerSearchSvc, logger, configRepo, configProv, qualityDefSvc, qualityProfilesSvc, tagsSvc, autoTagsSvc, releaseProfilesSvc, customFormatsSvc, delayProfilesSvc, customFiltersSvc, queueSvc, rootFoldersSvc, remotePathMappingsSvc, namingConfigSvc, notificationsRepo, indexersRepo, importListsRepo, importListExclusionsRepo, downloadClientsRepo, episodesSvc, episodesRepo, seriesSvc, mediaCoverSvc, healthSvc, metadataRepo, signalRHub, queueStatusPauser, systemStatus, pendingReleasesRepo, blocklistRepo, historyRepo, lifecycleSvc, cmdSvc, registry, failedDownloadsSvc, taskSvc, backupSvc, episodeFilesSvc, diskProv, bus, processDownloadDecisions, downloadDecider, releaseCache, parsingSvc, manualImportDecisionRejector, episodeFileDeleter, queueRemover, recentUpdateLister{svc: updateSvc}, logLister{repo: logRepo}, metadataSourceProxy, backupTempPath, filepath.Join(folder.AppData, "logs"), frontendDir)
	if err != nil {
		return err
	}
	go func() {
		defer workers.Done()
		if err := hostSrv.Run(workerCtx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Warn("host server exited with error", "err", err)
		}
	}()

	// Bus is hot — fan out the start signal so subscribers (auth
	// service seed, future health checks, etc.) can do their lazy
	// init.
	if err := messaging.Publish(ctx, bus, lifecycle.ApplicationStartedEvent{}); err != nil {
		return fmt.Errorf("publish ApplicationStartedEvent: %w", err)
	}

	logger.Info("daemon ready", "version", buildinfo.Version,
		"hint", "Phase 5 OPENED — host shell + /ping live; REST surface + SignalR + frontend wiring incoming")
	<-ctx.Done()

	// Shutdown sequence — publish the signal BEFORE BeginShutdown
	// gates further lifecycle events; then drain the async pool.
	// Slice 3.BP: route through *lifecycle.Service.Shutdown so the
	// publish goes through the same code path API-triggered
	// ShutdownCommand uses, and the RestartPending flag check at
	// the end of this function reflects whether a Restart command
	// fired before the SIGTERM/SIGINT signal landed (mirrors C#
	// `_runtimeInfo.RestartPending` read at startup-script tier).
	logger.Info("sonarr shutdown requested")
	if err := lifecycleSvc.Shutdown(context.Background()); err != nil &&
		!errors.Is(err, messaging.ErrShutdown) {
		logger.Warn("lifecycle shutdown publish failed", "err", err)
	}
	// Stop the dispatcher + scheduler before draining the bus so any
	// in-flight CommandExecutedEvent publishes flow through to the
	// (still-live) bus subscribers. Close() is defensive — Run already
	// exits on ctx cancel.
	workerCancel()
	queue.Close()
	workers.Wait()
	// Slice 4.FJ: release the queue/status broadcaster debouncer
	// timer (if wired) before the bus + hub tear down. Stop is
	// idempotent + does not wait for any in-flight fire — a
	// broadcast already-scheduled within ~5s before Stop will
	// still execute, but the hub/bus are still live at this point
	// so the broadcast lands cleanly. Subsequent Execute()s no-op.
	if queueStatusDebouncer != nil {
		queueStatusDebouncer.Stop()
	}
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

// buildLogDatastoreConfig builds the RoleLog datastore config, mirroring
// the C# log-DB selection. SQLite always has a log DB (the logs.db file
// next to the main DB). For Postgres the log DB is the separate
// `PostgresLogDb` database; when that isn't configured the log DB is
// disabled (the C# LogDbEnabled=false posture) and the bool is false so
// the caller skips opening it + attaching the sink.
func buildLogDatastoreConfig(ctx context.Context, cfg *configfile.Provider, folder appenv.AppFolderInfo, instance string) (datastore.Config, bool, error) {
	pgMain, err := cfg.String(ctx, "PostgresMainDb", "", false)
	if err != nil {
		return datastore.Config{}, false, err
	}
	if pgMain == "" {
		return datastore.Config{
			Engine: datastore.EngineSQLite,
			Role:   datastore.RoleLog,
			Path:   folder.LogDBPath(instance),
		}, true, nil
	}
	pgLog, err := cfg.String(ctx, "PostgresLogDb", "", false)
	if err != nil {
		return datastore.Config{}, false, err
	}
	if pgLog == "" {
		return datastore.Config{}, false, nil // log DB disabled
	}
	host, err := cfg.String(ctx, "PostgresHost", "localhost", false)
	if err != nil {
		return datastore.Config{}, false, err
	}
	port, err := cfg.Int(ctx, "PostgresPort", 5432, false)
	if err != nil {
		return datastore.Config{}, false, err
	}
	user, err := cfg.String(ctx, "PostgresUser", "", false)
	if err != nil {
		return datastore.Config{}, false, err
	}
	password, err := cfg.String(ctx, "PostgresPassword", "", false)
	if err != nil {
		return datastore.Config{}, false, err
	}
	return datastore.Config{
		Engine:   datastore.EnginePostgres,
		Role:     datastore.RoleLog,
		Host:     host,
		Port:     port,
		User:     user,
		Password: password,
		Database: pgLog,
	}, true, nil
}

// handlerEpisodeSearchAdapter narrows the indexersearch.Service
// EpisodeSearch signature for the MissingEpisodeSearch command
// handler (slice 5.J39). The handler only wants the decision count;
// the full Decision slice is consumed by the upstream grab pipeline
// inside indexersearch — not by the missing-search dispatcher.
// decisionGrabber is the narrow grab surface the search adapters use to
// dispatch approved search decisions to the download clients (G1/J423).
// *download.ProcessDownloadDecisions satisfies it.
type decisionGrabber interface {
	ProcessDecisions(ctx context.Context, decisions []decisionengine.Decision) *download.ProcessedDecisions
}

// grabSearchDecisions feeds search decisions into the grabber so approved
// candidates actually download. No-op when grabber is nil or there are no
// decisions. Shared by the search-command / episoderefreshed / redownload
// adapters — the bridge that was missing (search found releases but never
// grabbed them), mirroring the RssSync handler's decider→grabber flow.
func grabSearchDecisions(ctx context.Context, grabber decisionGrabber, decisions []decisionengine.Decision) {
	if grabber != nil && len(decisions) > 0 {
		grabber.ProcessDecisions(ctx, decisions)
	}
}

type handlerEpisodeSearchAdapter struct {
	svc     *indexersearch.Service
	grabber decisionGrabber
}

// EpisodeSearch satisfies the handlers.EpisodeSearcher interface.
// SearchSettings zero value matches the C# `MissingEpisodeSearch`
// path which uses bulk defaults (no UserInvokedSearch override).
//
// G1 (J423): feed the search decisions into the grabber so approved
// candidates actually download — mirrors the RssSync handler's
// decider→grabber bridge. Without this the search found releases but
// never grabbed them. Returns the decision count (the handler's log
// metric); the grab is a side effect.
func (a handlerEpisodeSearchAdapter) EpisodeSearch(ctx context.Context, episodeID int) (int, error) {
	decisions, err := a.svc.EpisodeSearch(ctx, episodeID, indexersearch.SearchSettings{})
	if err != nil {
		return 0, err
	}
	grabSearchDecisions(ctx, a.grabber, decisions)
	return len(decisions), nil
}

// seasonSearchAdapter narrows *indexersearch.Service.SeasonSearch
// (returns []decisionengine.Decision) into the decisions-count shape
// the SeriesSearch handler (slice 5.J215) wants. The third positional
// argument is `missingOnly`; SearchSettings zero value matches the
// C# `SeriesSearchService` bulk-default posture (no UserInvokedSearch
// override; matches the J215 documented quality-profile divergence).
type seasonSearchAdapter struct {
	svc     *indexersearch.Service
	grabber decisionGrabber
}

func (a seasonSearchAdapter) SeasonSearch(ctx context.Context, seriesID, seasonNumber int, missingOnly bool) (int, error) {
	decisions, err := a.svc.SeasonSearch(ctx, seriesID, seasonNumber, missingOnly, indexersearch.SearchSettings{})
	if err != nil {
		return 0, err
	}
	grabSearchDecisions(ctx, a.grabber, decisions) // G1 (J423)
	return len(decisions), nil
}

// episodeRefreshedSearcherAdapter satisfies
// episoderefreshed.EpisodeSearcher (J209) — a slice-taking dispatch
// shape that C# `EpisodeRefreshedService.cs:47` provides via
// `_commandQueueManager.Push(new EpisodeSearchCommand(...))`. The Go
// port currently lacks a multi-episode EpisodeSearchCommand handler,
// so we loop over IDs and dispatch each through the existing
// per-episode `*indexersearch.Service.EpisodeSearch` (same pattern as
// J39's MissingEpisodeSearch handler at handlers/missingepisodesearch.go:172).
// Per-episode failures are warn-logged and swallowed; one bad indexer
// query in a batch shouldn't abort the rest. Slice 5.J210.
type episodeRefreshedSearcherAdapter struct {
	svc     *indexersearch.Service
	grabber decisionGrabber
	log     *slog.Logger
}

func (a episodeRefreshedSearcherAdapter) SearchEpisodes(ctx context.Context, episodeIDs []int) error {
	for _, id := range episodeIDs {
		decisions, err := a.svc.EpisodeSearch(ctx, id, indexersearch.SearchSettings{})
		if err != nil {
			a.log.LogAttrs(ctx, slog.LevelWarn,
				"episoderefreshed: per-episode search failed",
				slog.Int("episodeId", id),
				slog.Any("err", err))
			continue
		}
		grabSearchDecisions(ctx, a.grabber, decisions) // G1 (J423)
	}
	return nil
}

// updateBranchAdapter narrows *configfile.Provider for the
// update.BranchProvider interface (slice 5.J40). The configfile
// accessor returns (string, error); the adapter discards the error
// and falls back to "main" — matches the C# `_configFileProvider.
// Branch` getter shape which can't fail.
type updateBranchAdapter struct {
	cfg *configfile.Provider
}

func (a updateBranchAdapter) Branch(ctx context.Context) string {
	v, err := a.cfg.Branch(ctx)
	if err != nil || v == "" {
		return "main"
	}
	return v
}

// notificationInstanceNamer is the narrow surface
// [notificationConfigAdapter] reads its instance name from — the
// configfile accessor returns (string, error). Declared here (the
// consumer side) so the adapter can be unit-tested with a fake.
type notificationInstanceNamer interface {
	InstanceName(ctx context.Context) (string, error)
}

// notificationConfigAdapter adapts the configfile InstanceName
// accessor to the `InstanceName() string` surface the Discord and
// Telegram notification providers narrow off
// notifications.Dependencies.ConfigProvider.
//
// C# injects IConfigFileProvider into both providers and reads
// `_configFileProvider.InstanceName` — Discord author name at
// `src/NzbDrone.Core/Notifications/Discord/Discord.cs:43` (and :146,
// :260, :367) and Telegram title suffix at
// `src/NzbDrone.Core/Notifications/Telegram/Telegram.cs:23,28`.
// The Go providers assert `interface{ InstanceName() string }`
// (discord.go:85 / telegram.go instanceNamer). The app-config
// config.Provider previously wired into deps.ConfigProvider has no
// InstanceName accessor at all, so both providers silently fell back
// to the literal "Sonarr" instead of the user's configured instance
// name. This adapter sources the live value from the configfile
// provider (the same source C# reads). The configfile accessor
// already defaults to the product name, so the err/empty fallback is
// purely defensive.
type notificationConfigAdapter struct {
	src notificationInstanceNamer
}

func (a notificationConfigAdapter) InstanceName() string {
	name, err := a.src.InstanceName(context.Background())
	if err != nil || name == "" {
		return configfile.AppName
	}
	return name
}

// updateCheckerAdapter narrows *update.Service for the
// handlers.UpdateChecker interface (slice 5.J40). The handler only
// wants the version string; the adapter discards the rest of the
// UpdatePackage projection (URL / Hash / ReleaseDate — consumed by
// the future install-side wiring).
type updateCheckerAdapter struct {
	svc *update.Service
}

func (a updateCheckerAdapter) AvailableVersion(ctx context.Context) (string, error) {
	pkg, err := a.svc.AvailableUpdate(ctx)
	if err != nil {
		return "", err
	}
	if pkg == nil {
		return "", nil
	}
	return pkg.Version, nil
}

// updateCheckOsInfoAdapter narrows [appenv.OsInfo] (a struct with
// flag fields) to the [healthcheck.UpdateCheckOsInfo] interface (a
// pair of methods). Slice 5.J143.
type updateCheckOsInfoAdapter struct {
	info appenv.OsInfo
}

func (a updateCheckOsInfoAdapter) IsDocker() bool { return a.info.IsDocker }
func (a updateCheckOsInfoAdapter) IsDarwin() bool { return appenv.IsDarwin() }

// updateAvailableLookupAdapter bridges [*update.Service] to the
// [healthcheck.UpdateAvailableLookup] interface — re-projects the
// update.Package shape into the narrower AvailableUpdate the check
// consumes. Slice 5.J143.
type updateAvailableLookupAdapter struct {
	svc *update.Service
}

func (a updateAvailableLookupAdapter) AvailableUpdate(ctx context.Context) (*healthcheck.AvailableUpdate, error) {
	pkg, err := a.svc.AvailableUpdate(ctx)
	if err != nil {
		return nil, err
	}
	if pkg == nil {
		return nil, nil
	}
	return &healthcheck.AvailableUpdate{Version: pkg.Version}, nil
}

// parseBuildDate parses [buildinfo.BuildDate] (an RFC3339 UTC
// timestamp stamped at link time) into a [time.Time]. Returns zero
// when BuildDate is empty / "unknown" / unparseable — the UpdateCheck
// at slice 5.J35 treats a zero-time as "build age unknown" and skips
// step 4 (the > 14d-old gate).
func parseBuildDate(raw string) time.Time {
	if raw == "" || raw == "unknown" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}
