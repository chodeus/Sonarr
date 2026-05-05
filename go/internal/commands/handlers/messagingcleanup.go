// MessagingCleanup handler. Ports
// `NzbDrone.Core.Messaging.Commands.CleanupCommandMessagingService` at
// `src/NzbDrone.Core/Messaging/Commands/CleanupCommandMessagingService.cs:1-17`,
// the IExecute<MessagingCleanupCommand> implementation that calls
// `IManageCommandQueue.CleanCommands()` and exits.
//
// `CommandQueueManager.CleanCommands()` at
// `src/NzbDrone.Core/Messaging/Commands/CommandQueueManager.cs:235-246`
// hard-codes two cutoffs: 5 minutes for the in-memory queue (drop
// stale Started/Completed entries) and 1 day for the persisted
// rows (`_repo.Trim(DateTime.UtcNow.AddDays(-1))`). The Go-port
// `Service.CleanCommands` collapses both to a single `cutoff` parameter
// — this handler passes the C# DB retention (`now - 24h`) since the
// Go port's `Queue` doesn't keep terminal entries (the dispatcher's
// `RemoveCompleted` drops them as soon as the row transitions out of
// Started). The 5-minute branch is a no-op against the Go in-memory
// queue, so we only port the 1-day DB-trim half.
//
// Body shape: the C# `MessagingCleanupCommand` is a bare `Command`
// subclass with no fields — body is `"{}"`. We accept any body
// without parsing.

package handlers

import (
	"context"
	"time"

	"sonarr.tv/sonarr/internal/commands"
)

// MessagingCleanupName is the registry key + Commands.Name + ScheduledTasks.TypeName
// the scheduler pushes against.
const MessagingCleanupName = "MessagingCleanup"

// MessagingCleanupRetention is the cutoff age for `Commands` row
// retention. Mirrors the C# `:240` `DateTime.UtcNow.AddDays(-1)`.
const MessagingCleanupRetention = 24 * time.Hour

// MessagingCleanup is the IExecute<MessagingCleanupCommand> port.
type MessagingCleanup struct {
	svc *commands.Service
	now func() time.Time
}

// NewMessagingCleanup builds the handler. The Service must be the
// same instance the dispatcher feeds, so CleanCommands runs against
// the live repo.
func NewMessagingCleanup(svc *commands.Service) *MessagingCleanup {
	return &MessagingCleanup{
		svc: svc,
		now: func() time.Time { return time.Now().UTC() },
	}
}

// SetClock overrides the clock used to derive the cutoff. Tests pin a
// fixed value to make the cutoff deterministic.
func (h *MessagingCleanup) SetClock(f func() time.Time) {
	if f == nil {
		return
	}
	h.now = f
}

// Execute calls Service.CleanCommands with `now - MessagingCleanupRetention`.
// The body is ignored — the C# command type carries no fields.
func (h *MessagingCleanup) Execute(ctx context.Context, _ string) error {
	cutoff := h.now().Add(-MessagingCleanupRetention)
	_, err := h.svc.CleanCommands(ctx, cutoff)
	return err
}
