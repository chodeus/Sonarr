// ImportListSync handler. Ports the IExecute<ImportListSyncCommand>
// implementation. Slice 5.J183 closed the J181 stub by delegating to
// an injected [ImportListSyncRunner] when wired; when no runner is
// supplied (test wiring or hosts that haven't yet plumbed the
// SyncService boot path), Execute falls back to the J181 stub-log
// posture so the registry slot + scheduled-task entry stay valid.
//
// The C# `ImportListSyncCommand` carries an optional `DefinitionId`
// field (null = sync every AutomaticAddEnabled list, set = sync the
// single list at that Id). The Go-port handler ignores the body
// entirely for the scheduled-tick path — every scheduler fire syncs
// all AutomaticAddEnabled lists. The DefinitionId arm exists in the
// underlying [importlists.SyncService.SyncList] method; wiring it
// to the body lands when the v3 API endpoint "refresh this list
// now" needs it (separate slice, not part of J183).
//
// Failure posture: the C# scheduled-task pipeline catches every
// exception per-tick and logs but does not abort the scheduler. The
// Go port matches by logging the SyncService.Sync error at warn level
// and returning nil to the dispatcher — the scheduler keeps firing
// on the next interval. Per-list provider failures inside Sync are
// already absorbed by the SyncService's `AnyFailure` aggregation
// (the orchestrator processes the healthy lists' items and reports
// the partial-failure flag in the SyncResult); J183 logs the result
// summary so operators can observe whether the tick was clean.

package handlers

import (
	"context"
	"log/slog"

	"sonarr.tv/sonarr/internal/importlists"
)

// ImportListSyncName is the registry key + Commands.Name +
// ScheduledTasks.TypeName. Mirrors the C# short form
// `ImportListSyncCommand` → `ImportListSync`.
const ImportListSyncName = "ImportListSync"

// ImportListSyncIntervalDefault is the 5-minute scheduler cadence.
// Matches C# `TaskManager.cs:120` (`Interval = 5`).
const ImportListSyncIntervalDefault = 5

// ImportListSyncRunner is the narrow interface this handler needs
// from `*importlists.SyncService`. Defined locally so the handlers
// package doesn't take a hard dep on the SyncService struct shape
// (the test stub in importlistsync_test.go satisfies the interface
// without pulling the real Factory + adapters into the test graph).
//
// `*importlists.SyncService` satisfies this structurally via its
// existing [importlists.SyncService.Sync] method.
type ImportListSyncRunner interface {
	Sync(ctx context.Context) (importlists.SyncResult, error)
}

// ImportListSync is the IExecute<ImportListSyncCommand> port.
type ImportListSync struct {
	runner ImportListSyncRunner
	log    *slog.Logger
}

// NewImportListSync builds the handler. runner may be nil — Execute
// then falls back to the stub-log posture so hosts without a wired
// SyncService still register the slot (the 5-minute scheduled-task
// entry depends on the registry key existing). log defaults to
// slog.Default.
func NewImportListSync(runner ImportListSyncRunner, log *slog.Logger) *ImportListSync {
	if log == nil {
		log = slog.Default()
	}
	return &ImportListSync{runner: runner, log: log}
}

// Execute calls runner.Sync when wired, logging the result summary
// at info level. When runner is nil, falls back to the J181 stub-log
// posture (no-op + info line) so the scheduled-task tick is benign
// against an unwired host. The body is ignored on the scheduled-tick
// path — the per-list DefinitionId arm is reserved for the future
// "refresh this list now" v3 API endpoint.
func (h *ImportListSync) Execute(ctx context.Context, _ string) error {
	if h.runner == nil {
		h.log.LogAttrs(ctx, slog.LevelInfo,
			"importlistsync: stub Execute (SyncService boot wiring not yet plumbed)",
			slog.String("command", ImportListSyncName))
		return nil
	}
	result, err := h.runner.Sync(ctx)
	if err != nil {
		h.log.LogAttrs(ctx, slog.LevelWarn,
			"importlistsync: Sync completed with errors (scheduler tick will fire again on next interval)",
			slog.String("command", ImportListSyncName),
			slog.Any("err", err))
		return nil
	}
	h.log.LogAttrs(ctx, slog.LevelInfo,
		"importlistsync: Sync complete",
		slog.String("command", ImportListSyncName),
		slog.Int("itemsFetched", result.ItemsFetched),
		slog.Int("seriesAdded", result.SeriesAdded),
		slog.Int("skippedExcluded", result.SkippedExcluded),
		slog.Int("skippedExisting", result.SkippedExisting),
		slog.Int("skippedUnresolvedID", result.SkippedUnresolvedID),
		slog.Bool("anyFailure", result.AnyFailure))
	return nil
}
