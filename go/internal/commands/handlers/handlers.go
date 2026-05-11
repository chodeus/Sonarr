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

	Log *slog.Logger
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
		Handler: NewRefreshSeries(deps.Log),
	})
	registry.Register(RefreshMonitoredDownloadsName, commands.CommandSpec{
		Handler: NewRefreshMonitoredDownloads(deps.Log),
	})
	registry.Register(CheckForFinishedDownloadsName, commands.CommandSpec{
		Handler: NewCheckForFinishedDownloads(deps.Log),
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
		rssSync, err := NewRssSync(deps.RssSync, deps.RssSyncDecider, deps.RssSyncBus, deps.Log)
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
		// Slice 4.Z (queue [15]): CheckForFinishedDownloads default
		// schedule. Mirrors `TaskManager.cs:74` — `Interval = 1`
		// (every minute). The C# entry also sets Priority =
		// CommandPriority.High; per-task priority override is
		// deferred (same divergence as RefreshMonitoredDownloads).
		checkForFinishedDownloadsDefaultTask(),
	}
}
