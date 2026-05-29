// Package handlers wires concrete IExecute<T> ports into the
// commands.CommandRegistry. One file per command type; this file
// holds the cross-handler RegisterAll helper the host calls at boot.
//
// The package lives under internal/commands so handlers stay
// adjacent to the registry they target, but is a sibling sub-package
// so the core commands package never imports handler code (which
// would create a cycle once handlers grow domain dependencies like
// series/episodefiles services).
//
// Naming convention (settled in Phase 3.13):
//
//   - The string used as `Commands.Name`, `ScheduledTasks.TypeName`,
//     and `CommandRegistry` key is the C# `Command.Name` short form —
//     `Type.Name.Replace("Command", "")` — e.g. `MessagingCleanup`,
//     `RefreshSeries`, `Backup`. This matches the C# JSON API surface
//     (`POST /api/v3/command` body `{"name":"MessagingCleanup"}`) and
//     the scheduled-task display name. The C# tree persists
//     `ScheduledTasks.TypeName` as the assembly-qualified `Type.FullName`
//     ("NzbDrone.Core.Messaging.Commands.MessagingCleanupCommand"); the
//     Go port collapses both representations to the short form. Existing
//     C# Sonarr databases upgrade fine: any FullName-style row not in
//     the Go-port default set is deleted as an orphan by
//     `scheduledtasks.Service.RegisterDefaults`, then re-inserted under
//     the short name on the next boot.
package handlers

import (
	"errors"
	"log/slog"

	"sonarr.tv/sonarr/internal/commands"
	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/housekeeping"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/scheduledtasks"
)

// Deps bundles the cross-cutting services the handler set wires
// against. Adding a new dep here keeps the host's RegisterAll call
// stable; existing handlers that don't need the new service simply
// ignore it. A pointer-bag struct is more honest than a long
// positional argument list once we're past three handlers.
//
// Log is used by stub-shaped handlers (e.g. CheckHealth) that don't
// have a real service to delegate to yet — they log the fired event
// at info level so operators can see scheduler ticks land. Nil
// defaults to slog.Default at handler construction time.
type Deps struct {
	Commands     *commands.Service
	Housekeeping *housekeeping.Service
	HealthCheck  *healthcheck.Service

	// RescanSeries wires the IExecute<RescanSeriesCommand> handler
	// into the dispatcher. Defined as the narrow [RescanSeriesScanner]
	// interface so the test suite can pass a stub without constructing
	// a fully-wired *mediafiles.Service. Production binds to the
	// concrete Service at boot via Go's structural typing.
	//
	// Nil is rejected by RegisterAll — there's no sensible stub
	// fallback for "rescan disk" the way there is for some other
	// handlers (a stub would silently no-op every API rescan).
	RescanSeries RescanSeriesScanner

	// CleanUpRecycleBin wires the IExecute<CleanUpRecycleBinCommand>
	// handler into the dispatcher. Optional — nil falls back to the
	// pre-3.77 stub-shaped log line so hosts that haven't yet wired a
	// recyclebin Service still register the slot (the 24-hour
	// scheduled-task entry depends on the registry key existing).
	// Production binds `*recyclebin.Service` via structural typing.
	CleanUpRecycleBin CleanupRunner

	// Backup wires the IExecute<BackupCommand> handler into the
	// dispatcher. Optional — nil falls back to the pre-3.BN stub-
	// shaped log line so hosts that haven't yet wired a backup
	// Service still register the slot (the 7-day scheduled-task
	// entry depends on the registry key existing).
	// Production binds `*backup.Service` via structural typing.
	Backup BackupRunner

	// Lifecycle wires the IExecute<ShutdownCommand> +
	// IExecute<RestartCommand> handler pair into the dispatcher.
	// Required when callers want API-triggered shutdown/restart;
	// nil skips registration entirely (the dispatcher then rejects
	// the commands with ErrNoHandler — there's no useful stub
	// since shutdown/restart MUST go through the bus to reach the
	// host's signal-handling code). Production binds
	// `*lifecycle.Service` via structural typing.
	Lifecycle LifecycleRunner

	// DownloadedEpisodesScan wires the IExecute<DownloadedEpisodesScanCommand>
	// handler into the dispatcher. The processor field is REQUIRED
	// when this struct is non-zero — RegisterAll passes the whole
	// nested struct through. When `Processor` is nil the handler is
	// not registered at all (RegisterAll skips the slot).
	//
	// Finder / Verifier / Reporter are all optional — see the
	// per-field doc on the [DownloadedEpisodesScan] handler.
	DownloadedEpisodesScan DownloadedEpisodesScanDeps

	// RssSync wires the IExecute<RssSyncCommand> handler into the
	// dispatcher. Slice 4.Q (queue [9]) — the FetchRSS-only minimum
	// the focused-subset Phase-4 loop ships. Optional: nil skips
	// registration entirely (the API surface returns "Unknown
	// command" for callers and the scheduler entry is also skipped).
	// Production binds `*indexersearch.Service` via structural typing.
	RssSync RssFetcher

	// RssSyncBus is the optional message bus the RssSync handler
	// publishes RssSyncCompleteEvent on. Nil skips the publish; the
	// handler still completes the FetchRSS work.
	RssSyncBus *messaging.Bus

	// RssSyncDecider is the optional decisionengine.DownloadDecisionMaker
	// projection RssSync dispatches the fetched releases to after
	// FetchRSS returns. Slice 4.U (queue [11] sub-arc 11c). Nil skips
	// the decision pipeline (compat with the slice 4.Q wiring); the
	// handler still logs the fetched-release count and publishes the
	// RssSyncCompleteEvent (with zero approved/rejected counts).
	RssSyncDecider RssDecider

	// RssSyncGrabber is the optional download.ProcessDownloadDecisions
	// projection RssSync feeds the decider's output into after the
	// decision pipeline runs. Slice 6.A6d (queue [15]). Nil skips the
	// dispatch step (preserves the slice-4.U behaviour); the handler
	// still publishes RssSyncCompleteEvent with the approved/rejected
	// counts and zero Grabbed/Pending counts. Production binds
	// `*download.ProcessDownloadDecisions` via Go's structural typing.
	RssSyncGrabber GrabProcessor

	// ApplicationCheckUpdate wires the
	// IExecute<ApplicationUpdateCheckCommand> handler into the
	// dispatcher. Slice 5.J40 (audit B.3 close-out). Optional — nil
	// falls back to the stub-log behaviour so hosts without a wired
	// update.Service still register the slot (the 6-hour
	// scheduled-task entry depends on the registry key existing).
	// Production binds an adapter around `*update.Service.AvailableUpdate`.
	ApplicationCheckUpdate UpdateChecker

	// MissingEpisodeLister + MissingEpisodeSearcher wire the
	// IExecute<MissingEpisodeSearchCommand> handler into the
	// dispatcher. Slice 5.J39 (audit B.3 close-out). Both optional —
	// either nil falls back to the stub-log behaviour so hosts
	// without a wired Service still register the slot. Production
	// binds `*episodes.Service` + an adapter around
	// `*indexersearch.Service.EpisodeSearch` (the count-returning
	// shape — see handlers.EpisodeSearcher doc).
	MissingEpisodeLister   EpisodeMissingLister
	MissingEpisodeSearcher EpisodeSearcher

	// CutoffUnmetEpisodeLister wires the
	// IExecute<CutoffUnmetEpisodeSearchCommand> handler (slice 5.J362).
	// Optional — nil falls back to the stub-log behaviour. The
	// per-episode dispatcher reuses MissingEpisodeSearcher (the same
	// indexersearch.Service.EpisodeSearch adapter). Production binds an
	// adapter around `*episodes.Repo` + `*qualityprofiles.Service`.
	CutoffUnmetEpisodeLister EpisodeCutoffUnmetLister

	// SeriesSearchLookup / SeriesSearchEpisodes / SeriesSeasonSearcher
	// wire the IExecute<SeriesSearchCommand> handler (slice 5.J215).
	// All four (incl. MissingEpisodeSearcher above which doubles as
	// the per-episode dispatcher) optional — any nil falls back to the
	// stub-log behaviour so hosts without a wired indexersearch.Service
	// still register the command slot.
	SeriesSearchLookup   SeriesSearchLookup
	SeriesSearchEpisodes SeriesSearchEpisodesLister
	SeriesSeasonSearcher SeasonSearcher

	// MonitoredDownloadRefresher wires the
	// IExecute<RefreshMonitoredDownloadsCommand> handler into the
	// dispatcher with a real refresher. Slice 5.J37 (audit B.3).
	// Optional — nil falls back to the pre-5.J37 stub-log behaviour so
	// hosts without a wired downloadmonitoring.Service still register
	// the slot (the 1-minute scheduled-task entry depends on the
	// registry key existing).
	// Production binds `*downloadmonitoring.Service` via structural
	// typing.
	MonitoredDownloadRefresher Refresher

	// UpdateSceneMapping wires the IExecute<UpdateSceneMappingCommand>
	// handler into the dispatcher. Slice 4.AZ (sub-arc M6b-ii of queue
	// [M6]). Optional — nil skips registration entirely (the API
	// returns "Unknown command" + the scheduler entry is also
	// skipped, since there's no useful stub for "refresh scene
	// mappings"). Production binds `*scenemappings.Service` via
	// structural typing.
	UpdateSceneMapping SceneMappingUpdater

	// ImportListSync wires the IExecute<ImportListSyncCommand>
	// handler into the dispatcher with a real
	// [importlists.SyncService]. Slice 5.J183 (closes the I4c stub
	// from J181). Optional — nil falls back to the J181 stub-log
	// posture so hosts that haven't yet wired the SyncService boot
	// path still register the slot (the 5-minute scheduled-task
	// entry depends on the registry key existing). Production binds
	// `*importlists.SyncService` via structural typing.
	ImportListSync ImportListSyncRunner

	// RefreshSeries wires the IExecute<RefreshSeriesCommand> handler
	// into the dispatcher with a real [refreshseries.Service]. Slice
	// 5.J191 (closes audit row B.3). Optional — nil falls back to the
	// J166-era stub-log posture so hosts that haven't wired the runner
	// still register the slot. Production binds
	// `*refreshseries.Service` via structural typing.
	RefreshSeries RefreshSeriesRunner

	// ManualImport wires the IExecute<ManualImportCommand> handler into
	// the dispatcher. Slice 5.J386 (punch-list #2). RegisterAll
	// registers the slot only when Importer is non-nil — there's no
	// useful stub fallback (a stub would silently no-op a user-
	// triggered import). The four core sub-deps are all required; the
	// Finder is optional. Production binds the existing
	// episodeimport/series/episodes services via structural typing.
	ManualImport ManualImportDeps

	// DeleteSeriesFiles wires the IExecute<DeleteSeriesFilesCommand>
	// handler (slice 5.J391, punch-list #6). Optional — nil skips
	// registration entirely (no useful stub for "recycle every file";
	// a stub would silently no-op a destructive user action). Production
	// binds the cmd/sonarr episodeFileDeleteAdapter (recycle + DB delete).
	DeleteSeriesFiles SeriesFilesDeleter

	// Rename wires the IExecute<RenameFilesCommand> +
	// IExecute<RenameSeriesCommand> handlers (slice 5.J392, punch-list
	// #6). Registered only when all RenameDeps fields are non-nil (a
	// stub would silently no-op a user rename). Production binds
	// series/episodefiles/episodefilemoving services.
	Rename RenameDeps

	// SeriesMover wires the IExecute<MoveSeriesCommand> +
	// IExecute<BulkMoveSeriesCommand> handlers (slice 5.J393, punch-list
	// #6). Optional — nil skips registration (no useful stub for a
	// destructive folder move). Production binds the cmd/sonarr
	// seriesMoveAdapter (ports MoveSeriesService).
	SeriesMover SeriesMover

	// ClearBlocklist wires the IExecute<ClearBlocklistCommand> handler
	// (slice 5.J398, punch-list #6 admin commands). Optional — nil skips
	// registration. Production binds *blocklist.Service.
	ClearBlocklist BlocklistClearer

	// ResetQualityDefinitions wires the
	// IExecute<ResetQualityDefinitionsCommand> handler (slice 5.J402,
	// punch-list #6 admin commands). Optional — nil skips registration.
	// Production binds *qualitydefinitions.Service.
	ResetQualityDefinitions QualityDefinitionResetter

	// ClearLog wires the IExecute<ClearLogCommand> handler (slice 5.J410,
	// punch-list #6 admin commands). Optional — nil skips registration
	// (e.g. the log DB is disabled). Production binds *logs.Repo.
	ClearLog LogClearer

	// ProcessMonitoredDownloads wires the
	// IExecute<ProcessMonitoredDownloadsCommand> handler (slice 5.J399,
	// Phase-4 auto-import). Optional — nil skips registration. Production
	// binds *downloadprocessing.Service.
	ProcessMonitoredDownloads MonitoredDownloadProcessor

	// LogFolderCleaner + LogFolder/UpdateLogFolder wire the
	// IExecute<DeleteLogFilesCommand> + <DeleteUpdateLogFilesCommand>
	// handlers (the Settings→General log-deletion buttons). Optional —
	// the commands register only when LogFolderCleaner is non-nil.
	// Production binds *disk.Provider + the app log folders.
	LogFolderCleaner LogFolderCleaner
	LogFolder        string
	UpdateLogFolder  string

	Log *slog.Logger
}

// ManualImportDeps bundles the collaborators the ManualImport handler
// reaches for. Hoisted into its own struct so the parent Deps stays
// readable. SeriesGetter / EpisodeGetter / Aggregator / Importer are
// required when this struct is non-zero; Finder is optional. When
// Importer is nil the whole slot is skipped (RegisterAll registers no
// ManualImport command).
type ManualImportDeps struct {
	SeriesGetter  ManualImportSeriesGetter
	EpisodeGetter ManualImportEpisodeGetter
	Aggregator    ManualImportAggregator
	Importer      ManualImportImporter
	Finder        ManualImportTrackedDownloadFinder
}

// DownloadedEpisodesScanDeps bundles the four sub-deps the
// DownloadedEpisodesScan handler reaches for. Hoisted into its own
// struct so the parent Deps stays readable; only Processor is
// required.
type DownloadedEpisodesScanDeps struct {
	Processor ImportProcessor
	Finder    TrackedDownloadFinder
	Verifier  CompletedDownloadVerifier
	Reporter  CommandResultReporter
}

// RegisterAll wires every shipped handler into `registry`. The host
// calls this once at boot, after the dependency services have been
// constructed and before `commands.NewDispatcher`.
//
// New handlers append a `registry.Register(...)` line here so the
// host's wiring stays in one place; the alternative — host-side
// per-handler imports — would force every cmd/* binary to track the
// handler set explicitly.
//
// Returns an error if any required dep is nil; nil handlers panic at
// registration per the registry's contract (boot-time setup error,
// not a runtime condition).
func RegisterAll(registry *commands.CommandRegistry, deps Deps) error {
	if registry == nil {
		return errors.New("handlers: RegisterAll: nil registry")
	}
	if deps.Commands == nil {
		return errors.New("handlers: RegisterAll: nil Deps.Commands")
	}
	if deps.Housekeeping == nil {
		return errors.New("handlers: RegisterAll: nil Deps.Housekeeping")
	}
	if deps.HealthCheck == nil {
		return errors.New("handlers: RegisterAll: nil Deps.HealthCheck")
	}
	if deps.RescanSeries == nil {
		return errors.New("handlers: RegisterAll: nil Deps.RescanSeries")
	}
	checkHealth, err := NewCheckHealth(deps.HealthCheck)
	if err != nil {
		return err
	}
	rescanSeries, err := NewRescanSeries(deps.RescanSeries, deps.Log)
	if err != nil {
		return err
	}
	registry.Register(MessagingCleanupName, commands.CommandSpec{
		Handler: NewMessagingCleanup(deps.Commands),
	})
	registry.Register(HousekeepingName, commands.CommandSpec{
		Handler: NewHousekeeping(deps.Housekeeping),
	})
	registry.Register(CheckHealthName, commands.CommandSpec{
		Handler: checkHealth,
	})
	registry.Register(RefreshSeriesName, commands.CommandSpec{
		Handler: NewRefreshSeries(deps.RefreshSeries, deps.RssSyncBus, deps.Log),
	})
	registry.Register(RefreshMonitoredDownloadsName, commands.CommandSpec{
		Handler: NewRefreshMonitoredDownloads(deps.MonitoredDownloadRefresher, deps.Log),
	})
	registry.Register(CheckForFinishedDownloadsName, commands.CommandSpec{
		Handler: NewCheckForFinishedDownloads(deps.MonitoredDownloadRefresher, deps.Log),
	})
	registry.Register(MissingEpisodeSearchName, commands.CommandSpec{
		Handler: NewMissingEpisodeSearch(deps.MissingEpisodeLister, deps.MissingEpisodeSearcher, deps.Log),
	})
	registry.Register(CutoffUnmetEpisodeSearchName, commands.CommandSpec{
		Handler: NewCutoffUnmetEpisodeSearch(deps.CutoffUnmetEpisodeLister, deps.MissingEpisodeSearcher, deps.Log),
	})
	registry.Register(SeriesSearchName, commands.CommandSpec{
		Handler: NewSeriesSearch(deps.SeriesSearchLookup, deps.SeriesSearchEpisodes, deps.MissingEpisodeSearcher, deps.SeriesSeasonSearcher, deps.Log),
	})
	// G11 (J445): per-episode + per-season MANUAL search commands (the
	// UI "search" buttons). Reuse the already-wired EpisodeSearcher /
	// SeasonSearcher adapters (search + grab).
	registry.Register(EpisodeSearchName, commands.CommandSpec{
		Handler: NewEpisodeSearch(deps.MissingEpisodeSearcher, deps.Log),
	})
	registry.Register(SeasonSearchName, commands.CommandSpec{
		Handler: NewSeasonSearch(deps.SeriesSeasonSearcher, deps.Log),
	})
	registry.Register(ImportListSyncName, commands.CommandSpec{
		Handler: NewImportListSync(deps.ImportListSync, deps.Log),
	})
	registry.Register(ApplicationCheckUpdateName, commands.CommandSpec{
		Handler: NewApplicationCheckUpdate(deps.ApplicationCheckUpdate, deps.Log),
	})
	if deps.Backup != nil {
		backupHandler, err := NewBackup(deps.Backup, deps.Log)
		if err != nil {
			return err
		}
		registry.Register(BackupName, commands.CommandSpec{
			Handler: backupHandler,
		})
	} else {
		// Pre-3.BN behaviour: log-only stub when no backup Service
		// is wired (boot site can opt out by leaving Deps.Backup nil).
		registry.Register(BackupName, commands.CommandSpec{
			Handler: newBackupStub(deps.Log),
		})
	}
	registry.Register(CleanUpRecycleBinName, commands.CommandSpec{
		Handler: NewCleanUpRecycleBin(deps.CleanUpRecycleBin, deps.Log),
	})
	registry.Register(RescanSeriesName, commands.CommandSpec{
		Handler: rescanSeries,
	})
	if deps.Lifecycle != nil {
		shutdownHandler, err := NewShutdown(deps.Lifecycle, deps.Log)
		if err != nil {
			return err
		}
		registry.Register(ShutdownName, commands.CommandSpec{
			Handler: shutdownHandler,
		})
		restartHandler, err := NewRestart(deps.Lifecycle, deps.Log)
		if err != nil {
			return err
		}
		registry.Register(RestartName, commands.CommandSpec{
			Handler: restartHandler,
		})
	}

	// RssSync registers only when the fetcher is wired — there's no
	// useful stub fallback (a stub would silently no-op the RSS sweep
	// the scheduler ticks against). Hosts that haven't yet wired
	// indexersearch.Service simply omit the slot.
	if deps.RssSync != nil {
		rssSync, err := NewRssSync(deps.RssSync, deps.RssSyncDecider, deps.RssSyncGrabber, deps.RssSyncBus, deps.Log)
		if err != nil {
			return err
		}
		registry.Register(RssSyncName, commands.CommandSpec{
			Handler: rssSync,
		})
	}

	// DownloadedEpisodesScan registers only when the processor is
	// wired — there's no useful stub fallback (the C# command always
	// requires a path + an import service). Hosts that haven't yet
	// constructed `*downloadedimport.Service` simply omit the slot;
	// the API surface returns "Unknown command" for those callers.
	if deps.DownloadedEpisodesScan.Processor != nil {
		downloadedScan, err := NewDownloadedEpisodesScan(
			deps.DownloadedEpisodesScan.Processor,
			deps.DownloadedEpisodesScan.Finder,
			deps.DownloadedEpisodesScan.Verifier,
			deps.DownloadedEpisodesScan.Reporter,
			deps.Log,
		)
		if err != nil {
			return err
		}
		registry.Register(DownloadedEpisodesScanName, commands.CommandSpec{
			Handler: downloadedScan,
		})
	}

	// ManualImport registers only when the importer is wired — there's
	// no useful stub fallback (a stub would silently no-op a user-
	// triggered import). RequiresDiskAccess + IsLongRunning mirror the
	// C# ManualImportCommand flags (`ManualImportCommand.cs:11-13`).
	if deps.ManualImport.Importer != nil {
		manualImport, err := NewManualImport(
			deps.ManualImport.SeriesGetter,
			deps.ManualImport.EpisodeGetter,
			deps.ManualImport.Aggregator,
			deps.ManualImport.Importer,
			deps.ManualImport.Finder,
			deps.Log,
		)
		if err != nil {
			return err
		}
		registry.Register(ManualImportName, commands.CommandSpec{
			Handler:            manualImport,
			RequiresDiskAccess: true,
			IsLongRunning:      true,
		})
	}

	// DeleteSeriesFiles registers only when the deleter is wired — no
	// useful stub for a destructive recycle. RequiresDiskAccess mirrors
	// the C# DeleteSeriesFilesCommand flag.
	if deps.DeleteSeriesFiles != nil {
		deleteSeriesFiles, err := NewDeleteSeriesFiles(deps.DeleteSeriesFiles, deps.Log)
		if err != nil {
			return err
		}
		registry.Register(DeleteSeriesFilesName, commands.CommandSpec{
			Handler:            deleteSeriesFiles,
			RequiresDiskAccess: true,
		})
	}

	// Rename{Files,Series} register only when all deps are wired.
	if deps.Rename.valid() {
		renameFiles, err := NewRenameFiles(deps.Rename, deps.Log)
		if err != nil {
			return err
		}
		registry.Register(RenameFilesName, commands.CommandSpec{
			Handler:            renameFiles,
			RequiresDiskAccess: true,
		})
		renameSeries, err := NewRenameSeries(deps.Rename, deps.Log)
		if err != nil {
			return err
		}
		registry.Register(RenameSeriesName, commands.CommandSpec{
			Handler:            renameSeries,
			RequiresDiskAccess: true,
		})
	}

	// Move{Series,BulkSeries} register only when the mover is wired.
	if deps.SeriesMover != nil {
		moveSeries, err := NewMoveSeries(deps.SeriesMover, deps.Log)
		if err != nil {
			return err
		}
		registry.Register(MoveSeriesName, commands.CommandSpec{
			Handler:            moveSeries,
			RequiresDiskAccess: true,
		})
		bulkMoveSeries, err := NewBulkMoveSeries(deps.SeriesMover, deps.Log)
		if err != nil {
			return err
		}
		registry.Register(BulkMoveSeriesName, commands.CommandSpec{
			Handler:            bulkMoveSeries,
			RequiresDiskAccess: true,
		})
	}

	// ProcessMonitoredDownloads registers only when the processor is wired.
	if deps.ProcessMonitoredDownloads != nil {
		pmd, err := NewProcessMonitoredDownloads(deps.ProcessMonitoredDownloads, deps.Log)
		if err != nil {
			return err
		}
		registry.Register(ProcessMonitoredDownloadsName, commands.CommandSpec{
			Handler:            pmd,
			RequiresDiskAccess: true,
		})
	}

	// DeleteLogFiles + DeleteUpdateLogFiles register only when a folder
	// cleaner is wired (J446). RequiresDiskAccess — they empty folders.
	if deps.LogFolderCleaner != nil {
		registry.Register(DeleteLogFilesName, commands.CommandSpec{
			Handler:            NewDeleteLogFiles(deps.LogFolderCleaner, deps.LogFolder, deps.Log),
			RequiresDiskAccess: true,
		})
		registry.Register(DeleteUpdateLogFilesName, commands.CommandSpec{
			Handler:            NewDeleteUpdateLogFiles(deps.LogFolderCleaner, deps.UpdateLogFolder, deps.Log),
			RequiresDiskAccess: true,
		})
	}

	// ClearBlocklist registers only when the clearer is wired.
	if deps.ClearBlocklist != nil {
		clearBlocklist, err := NewClearBlocklist(deps.ClearBlocklist, deps.Log)
		if err != nil {
			return err
		}
		registry.Register(ClearBlocklistName, commands.CommandSpec{Handler: clearBlocklist})
	}

	// ResetQualityDefinitions registers only when the resetter is wired.
	if deps.ResetQualityDefinitions != nil {
		resetQualityDefinitions, err := NewResetQualityDefinitions(deps.ResetQualityDefinitions, deps.Log)
		if err != nil {
			return err
		}
		registry.Register(ResetQualityDefinitionsName, commands.CommandSpec{Handler: resetQualityDefinitions})
	}

	// ClearLog registers only when the clearer is wired (log DB enabled).
	if deps.ClearLog != nil {
		clearLog, err := NewClearLog(deps.ClearLog, deps.Log)
		if err != nil {
			return err
		}
		registry.Register(ClearLogName, commands.CommandSpec{Handler: clearLog})
	}

	// UpdateSceneMapping registers only when the updater is wired.
	// Slice 4.AZ — sub-arc M6b-ii of queue [M6]. The scheduled-task
	// entry in DefaultScheduledTasks fires on a 3-hour cadence
	// matching C# `TaskManager.cs:90`.
	if deps.UpdateSceneMapping != nil {
		updateSceneMapping, err := NewUpdateSceneMapping(deps.UpdateSceneMapping, deps.Log)
		if err != nil {
			return err
		}
		registry.Register(UpdateSceneMappingName, commands.CommandSpec{
			Handler: updateSceneMapping,
		})
	}
	return nil
}

// DefaultScheduledTasks returns the seed `[]*scheduledtasks.Task` for
// every shipped handler that runs on a periodic interval. The host
// passes the result to `scheduledtasks.Service.RegisterDefaults` at
// boot — handlers that don't appear here are still registered in the
// command dispatcher and remain reachable via manual API push, but
// the scheduler never fires them.
//
// Intervals are in MINUTES, matching the C# `ScheduledTask.Interval`
// column. The values mirror the C# `TaskManager.cs:67-135` defaults
// for handlers that have ports.
func DefaultScheduledTasks() []*scheduledtasks.Task {
	return []*scheduledtasks.Task{
		{
			TypeName: MessagingCleanupName,
			// Matches `TaskManager.cs:79` — `Interval = 5` (minutes).
			Interval: 5,
		},
		{
			TypeName: HousekeepingName,
			// Matches `TaskManager.cs:108` — `Interval = 24 * 60`
			// (24h in minutes).
			Interval: 24 * 60,
		},
		{
			TypeName: CheckHealthName,
			// Matches `TaskManager.cs:96-99` — `Interval = 6 * 60`
			// (6h in minutes).
			Interval: 6 * 60,
		},
		{
			TypeName: RefreshSeriesName,
			// Matches `TaskManager.cs:101-104` — `Interval = 12 * 60`
			// (12h in minutes).
			Interval: 12 * 60,
		},
		{
			TypeName: RefreshMonitoredDownloadsName,
			// Matches `TaskManager.cs:71` — `Interval = 1` (every minute).
			// The C# entry also sets Priority = CommandPriority.High; the
			// Go Scheduler currently pushes all tasks at PriorityLow —
			// per-task priority override is deferred.
			Interval: 1,
		},
		{
			TypeName: BackupName,
			// Seed with the 7-day default (BackupIntervalDefault).
			// When the real ConfigService lands, HandleAsync(ConfigSavedEvent)
			// will update this row live, matching TaskManager.cs:126-128.
			Interval: BackupIntervalDefault,
		},
		{
			TypeName: CleanUpRecycleBinName,
			// Matches `TaskManager.cs:108-113` — `Interval = 24 * 60`
			// (24h in minutes).
			Interval: 24 * 60,
		},
		// Slice 4.Q (queue [9]): RssSync default schedule. Mirrors the
		// C# `TaskManager.cs:132` entry — Interval set from
		// `_configService.RssSyncInterval` with the default 15-minute
		// clamp. The Go port hardcodes the default until the
		// RssSyncInterval config getter ports.
		rssSyncDefaultTask(),
		// Slice 5.J181 (audit B.1 contract-replay drift —
		// system.tasks task-set) drops the `CheckForFinishedDownload`
		// scheduled-task entry that slice 4.Z added in good faith but
		// against a misread C# cite. The 4.Z comment claimed it
		// mirrored `TaskManager.cs:74 — Interval = 1`, but `:74` is
		// the `Priority = CommandPriority.High` field of the
		// `RefreshMonitoredDownloads` ScheduledTask (lines 69-75 of
		// the C# defaults block), NOT a separate task entry. The C#
		// tree explicitly does NOT schedule
		// `CheckForFinishedDownloadCommand` — it stays registered as
		// a deprecated alias only third-party API callers reach. The
		// Go-port command handler stays registered (so the API
		// surface accepts it), but the scheduler no longer auto-
		// fires it every minute (RefreshMonitoredDownloads, which
		// IS scheduled, does the same work via the same Refresher).
		// Slice 5.J181 (audit B.1, system.tasks task-set) adds the
		// ImportListSync entry that C# `TaskManager.cs:118-122`
		// registers at a 5-minute cadence; the Go-port previously
		// lacked this slot (the I4c sub-arc the slice 4.AQ closure
		// flagged as deferred — the handler ships as a stub here,
		// the real port lands when importlists.SyncService boot
		// wiring plumbs).
		{
			TypeName: ImportListSyncName,
			Interval: ImportListSyncIntervalDefault,
		},
		// Slice 4.AZ (sub-arc M6b-ii of queue [M6]):
		// UpdateSceneMapping default schedule. Mirrors
		// `TaskManager.cs:90` — `Interval = 3 * 60` (3 hours in
		// minutes). The seed task fires the cloud refresh against
		// services.sonarr.tv/v1/scenemapping at boot + every 3h.
		{
			TypeName: UpdateSceneMappingName,
			Interval: UpdateSceneMappingIntervalMinutes,
		},
		// Slice 5.J40 (audit B.3 close-out): ApplicationCheckUpdate
		// default schedule. Matches C# `TaskManager.cs:127` — `Interval
		// = 6 * 60` (6 hours in minutes). The seed task fires against
		// services.sonarr.tv/v1/update/{branch} at boot + every 6h.
		{
			TypeName: ApplicationCheckUpdateName,
			Interval: ApplicationCheckUpdateIntervalDefault,
		},
	}
}
