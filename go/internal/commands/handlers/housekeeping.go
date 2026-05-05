// Housekeeping handler. Ports
// `NzbDrone.Core.Housekeeping.HousekeepingService.Execute(HousekeepingCommand)`
// at `src/NzbDrone.Core/Housekeeping/HousekeepingService.cs:45-49` —
// the IExecute<HousekeepingCommand> implementation that calls the
// service's private `Clean()` and exits.
//
// Body is ignored — the C# `HousekeepingCommand` is a bare `Command`
// subclass with no fields.

package handlers

import (
	"context"

	"sonarr.tv/sonarr/internal/housekeeping"
)

// HousekeepingName is the registry key + Commands.Name +
// ScheduledTasks.TypeName the scheduler pushes against.
const HousekeepingName = "Housekeeping"

// Housekeeping is the IExecute<HousekeepingCommand> port.
type Housekeeping struct {
	svc *housekeeping.Service
}

// NewHousekeeping builds the handler. The Service must be non-nil;
// validation happens at registration time so a nil here surfaces
// immediately as "wired with no tasks".
func NewHousekeeping(svc *housekeeping.Service) *Housekeeping {
	return &Housekeeping{svc: svc}
}

// Execute calls Service.Clean. The C# version returns void; the Go
// port surfaces the first per-task error so the dispatcher can record
// it on Commands.Exception. This makes "housekeeping had problems"
// observable to operators.
func (h *Housekeeping) Execute(ctx context.Context, _ string) error {
	return h.svc.Clean(ctx)
}
