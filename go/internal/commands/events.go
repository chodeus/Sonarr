package commands

// CommandExecutedEvent ports
// `NzbDrone.Core.Messaging.Events.CommandExecutedEvent`. Published by
// [Service.Complete] and [Service.Fail] after the command terminates,
// with the final Command row (status / result / duration / exception
// fields all populated).
//
// Listeners (notably TaskManager — `src/NzbDrone.Core/Jobs/TaskManager.cs:202`)
// react to this to update LastExecution timestamps and clear
// ProgressMessageContext state.
//
// In the C# tree this event is published by `CommandExecutor`, not by
// the queue manager. The Go port collapses both roles into a single
// Service for now (the in-memory dispatch queue + IExecute<T>
// registration land in Phase 3.10+); when the dispatcher splits out,
// publishing moves with it.
type CommandExecutedEvent struct {
	Command *Command
}

// CommandUpdatedEvent ports
// `NzbDrone.Core.Messaging.Events.CommandUpdatedEvent`. Published by
// [Service.Push], [Service.PushMany], [Service.Start],
// [Service.Complete], [Service.Fail], and [Service.Cancel] — every
// lifecycle transition that changes a row's Status or terminal-state
// fields.
//
// Listeners: the SignalR `command` channel broadcaster
// (cmd/sonarr/signalr_wiring.go) — the React UI's progress drawer
// listens on Updated to refresh per-command progress without
// polling the REST endpoint.
//
// C# behaviour comparison: C# `CommandExecutor` fires
// CommandUpdatedEvent at two points per executed command — after
// `Start` and in the `finally` of the execution body
// (`CommandExecutor.cs:77,102`). Push doesn't directly emit (the
// event comes from the executor when it picks the row up). The Go
// port doesn't yet have a split executor — Push transitions are
// reachable to the SignalR layer only if Push itself publishes.
// Until the dispatcher splits out (Phase 3.10+ — see
// CommandExecutedEvent doc-comment), the Service publishes
// CommandUpdatedEvent at all transitions so the wire shape is
// preserved.
//
// Divergence from C# `SendUpdatesToClient` filter
// (`CommandController.cs:98-105` — only commands whose Body opts in
// get broadcast): the Go-port Command schema doesn't surface that
// flag today (the Body string is canonical JSON without typed access
// to per-command properties). Every command is broadcast for now;
// when the Body deserialisation lands the broadcaster subscription
// can re-introduce the filter. Conservative posture — the React UI
// already filters by Status in the progress drawer so the extra
// emissions are non-functional cost only.
type CommandUpdatedEvent struct {
	Command *Command
}
