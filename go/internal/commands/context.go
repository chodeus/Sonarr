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

import "context"

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
