// ImportListSync handler. Ports the IExecute<ImportListSyncCommand>
// implementation — AS A STUB. The real port is gated on the wider
// `internal/importlists.SyncService` boot wiring (the sub-arc I4c
// follow-up the slice 4.AQ closure paragraph flagged as deferred —
// the SyncService itself lives in internal/importlists since slice
// 4.AP, but its provider Factory + seriesLookup + seriesAdder
// dependencies are not yet constructed in cmd/sonarr/main.go).
// This slot ships so:
//
//   - The eleventh shipped IExecute<T> handler exists, matching the
//     C# `TaskManager.cs:118-122` ScheduledTask entry that registers
//     `ImportListSyncCommand` at a 5-minute cadence.
//   - The /api/v3/system/task endpoint surfaces "Import List Sync"
//     in its task list (the v3 contract-replay drift slice 5.J181
//     closes — the C# golden has it in the registered-task set and
//     the Go-port previously lacked the entry).
//   - API callers pushing `ImportListSync` get a structured log line
//     instead of an "Unknown command" rejection.
//
// The C# `ImportListSyncCommand` carries an optional `DefinitionId`
// field (null = sync every AutomaticAddEnabled list, set = sync the
// single list). The stub ignores the body entirely; the real port
// will dispatch to `importlists.SyncService.Sync` or
// `SyncService.SyncList(definitionId)` based on the body.

package handlers

import (
	"context"
	"log/slog"
)

// ImportListSyncName is the registry key + Commands.Name +
// ScheduledTasks.TypeName. Mirrors the C# short form
// `ImportListSyncCommand` → `ImportListSync`.
const ImportListSyncName = "ImportListSync"

// ImportListSyncIntervalDefault is the 5-minute scheduler cadence.
// Matches C# `TaskManager.cs:120` (`Interval = 5`).
const ImportListSyncIntervalDefault = 5

// ImportListSync is the IExecute<ImportListSyncCommand> port stub.
type ImportListSync struct {
	log *slog.Logger
}

// NewImportListSync builds the handler. log defaults to slog.Default.
func NewImportListSync(log *slog.Logger) *ImportListSync {
	if log == nil {
		log = slog.Default()
	}
	return &ImportListSync{log: log}
}

// Execute logs that the command fired and returns nil. The real port
// will replace this with a delegation into
// `importlists.SyncService.Sync` (or .SyncList for the body-supplied
// DefinitionId arm).
func (h *ImportListSync) Execute(ctx context.Context, _ string) error {
	h.log.LogAttrs(ctx, slog.LevelInfo,
		"importlistsync: stub Execute (SyncService boot wiring not yet plumbed)",
		slog.String("command", ImportListSyncName))
	return nil
}
