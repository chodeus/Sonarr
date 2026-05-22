// Context-scoped command metadata. Slice 5.J198 ports the C#
// `CommandTrigger` plumbing for the command-execution scope. C# wires
// the trigger through `ProgressMessageContext`, an `AsyncLocal<T>`
// keyed off the command id — the Go-port mirrors that with a typed
// context.Value key.
//
// Why a context key and not a parameter on the Handler interface:
//
//   - The trigger is optional cross-cutting metadata that only a small
//     subset of handlers consume (today: RefreshSeries; eventually
//     anything that needs to distinguish manual from scheduled
//     execution). Threading it as a Handler.Execute parameter would
//     force all 18 registered handlers to update their signatures.
//   - The codebase already uses this pattern twice — messaging
//     recursion-depth tracking in `internal/messaging/messaging.go`
//     (recursionKey) and request-logger plumbing in
//     `internal/httperr/httperr.go` (loggerCtxKey). Both use the
//     unexported zero-size-struct key pattern this file mirrors.
//   - The default value (TriggerUnspecified) is the safe behaviour for
//     handlers that don't consume the trigger — equivalent to the
//     pre-J198 baseline where no handler had access to it.
//
// Callers attach the trigger via [WithTrigger] (the command dispatcher
// at `commands/dispatcher.go:runHandler` does this for every dispatched
// command) and read via [TriggerFromContext] (handlers that branch on
// the value call this; handlers that don't never reference it).

package commands

import (
	"context"
	"sync"
)

// triggerKey is the unexported context key for the per-command-execution
// Trigger metadata. Zero-size struct is the canonical Go pattern — it
// prevents accidental collisions with any other context.Value key that
// happens to share the same value (since the type is unexported and
// has no comparable value beyond the zero struct).
type triggerKey struct{}

// WithTrigger returns a derived context carrying the given Trigger.
// Used by the dispatcher to attach the row's Trigger metadata to the
// context before invoking the handler. Handlers consume it via
// [TriggerFromContext].
//
// Calling with [TriggerUnspecified] still attaches the key — readers
// can't distinguish "not attached" from "attached as Unspecified",
// matching C# AsyncLocal<T> semantics where the default and the
// explicitly-set-to-default cases are indistinguishable.
func WithTrigger(ctx context.Context, t Trigger) context.Context {
	return context.WithValue(ctx, triggerKey{}, t)
}

// TriggerFromContext extracts the Trigger from the context. Returns
// [TriggerUnspecified] when no trigger has been attached — this is the
// safe default that matches the pre-J198 behaviour of every handler.
//
// Use this in handlers that branch on manual vs scheduled execution
// (e.g. RefreshSeries gates its disk-rescan-after-refresh arm on
// Trigger == Manual when the user's `RescanAfterRefresh` config is
// `AfterManual`). Handlers that don't branch on the trigger should
// never call this — keep the dependency footprint minimal.
func TriggerFromContext(ctx context.Context) Trigger {
	if v, ok := ctx.Value(triggerKey{}).(Trigger); ok {
		return v
	}
	return TriggerUnspecified
}

// ---- J199 — Per-execution Result reporting via ctx.Value ----
//
// Ports C# `ICommandResultReporter` (Messaging/Commands/CommandResultReporter.cs):
// handlers that want to stamp the Result column with a non-default value
// (typically [ResultIndeterminate] — "I tried but couldn't finish every
// row") call [ReportResult] which writes to a per-execution slot the
// dispatcher then reads when computing the final Result.
//
// C# uses an AsyncLocal<T>-backed `ProgressMessageContext.CommandModel`
// + a mutable command instance. Go's context.Value is immutable, so we
// attach a pointer to a mutable [resultSlot] — same shape, idiomatic
// translation. The slot's [sync.Mutex] guards against concurrent writes
// when a handler dispatches goroutines that each report independently
// (the slot is shared per command-execution, not per goroutine).

// resultSlot is the mutable cell the dispatcher attaches to ctx and the
// reporter writes to. last-write-wins (matches C# SetResult semantics
// where the most recent Report call overrides the previous).
type resultSlot struct {
	mu     sync.Mutex
	result Result
	set    bool
}

// resultSlotKey is the unexported context key for the per-execution
// [resultSlot]. Same zero-size-struct pattern as [triggerKey].
type resultSlotKey struct{}

// withResultSlot derives a context carrying a fresh [resultSlot] and
// returns the slot pointer so the dispatcher can read its final state
// after the handler returns. Internal — only the dispatcher attaches
// the slot; handlers reach for it via [ReportResult].
func withResultSlot(ctx context.Context) (context.Context, *resultSlot) {
	slot := &resultSlot{}
	return context.WithValue(ctx, resultSlotKey{}, slot), slot
}

// ReportResult stamps the per-execution Result slot from a handler.
// No-op when the slot is missing (the context wasn't routed through
// the dispatcher's [withResultSlot] wrap) — this matches the C# guard
// at `CommandResultReporter.cs:23` (`if (command == null) return`)
// where Report on a context with no active command silently drops.
//
// Use this when a handler partially succeeds: refreshseries with one
// missing series in a 100-row batch, missing-episode-search hitting
// an indexer error mid-pass, etc. Mirrors C# `:235, :245`
// (`_commandResultReporter.Report(CommandResult.Indeterminate)`).
//
// last-write-wins: a later Report call overrides an earlier one. This
// matches C# `IManageCommandQueue.SetResult` semantics where the most
// recent stamp prevails.
func ReportResult(ctx context.Context, result Result) {
	slot, _ := ctx.Value(resultSlotKey{}).(*resultSlot)
	if slot == nil {
		return
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	slot.result = result
	slot.set = true
}

// readResultSlot is the dispatcher-side counterpart to [ReportResult].
// Returns the stamped Result + true when a handler called ReportResult
// during the execution; zero-value + false otherwise.
func readResultSlot(slot *resultSlot) (Result, bool) {
	if slot == nil {
		return ResultUnknown, false
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	return slot.result, slot.set
}
