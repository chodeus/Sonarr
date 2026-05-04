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
