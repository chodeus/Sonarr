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

	Log *slog.Logger
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
	registry.Register(BackupName, commands.CommandSpec{
		Handler: NewBackup(deps.Log),
	})
	registry.Register(CleanUpRecycleBinName, commands.CommandSpec{
		Handler: NewCleanUpRecycleBin(deps.CleanUpRecycleBin, deps.Log),
	})
	registry.Register(RescanSeriesName, commands.CommandSpec{
		Handler: rescanSeries,
	})
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
	}
}
