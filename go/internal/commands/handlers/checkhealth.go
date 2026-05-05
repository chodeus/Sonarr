// CheckHealth handler. Ports the IExecute<CheckHealthCommand> port at
// `src/NzbDrone.Core/HealthCheck/HealthCheckService.cs:137-150`.
//
// The Go-port shape collapses the three C# IHandle interfaces
// (IExecute<CheckHealthCommand>, IHandleAsync<ApplicationStartedEvent>,
// IHandleAsync<IEvent>) into one healthcheck.Service. This handler
// is the dispatcher-side surface — it delegates to Service.Run
// after parsing the command body's optional trigger field.
//
// Body shape — the Go port accepts `{}` (default → TriggerScheduled,
// matching the scheduler tick's body) and the explicit
// `{"trigger":"manual"}` form (matching a future API "Force Health
// Check" button). Anything else parses to Scheduled, mirroring the
// C# behaviour where `CommandTrigger.Unspecified == 0` falls onto
// the scheduled-only branch at `:139`.
//
// Lineage: this slot was a stub in 3.24 logging "real
// HealthCheckService not yet ported". Phase 3.29 closes the stub.

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"sonarr.tv/sonarr/internal/healthcheck"
)

// CheckHealthName is the registry key + Commands.Name +
// ScheduledTasks.TypeName the scheduler pushes against. Mirrors the
// C# `Command.Name` short form — `CheckHealthCommand` → `CheckHealth`.
const CheckHealthName = "CheckHealth"

// CheckHealth is the IExecute<CheckHealthCommand> port. svc must be
// the same instance that's also subscribed to the event bus — the
// dispatcher handler shares the in-memory result cache + grace-period
// state with the event-driven re-checks.
type CheckHealth struct {
	svc *healthcheck.Service
}

// NewCheckHealth builds the handler. The Service must be non-nil —
// running the command without a service has no meaningful behaviour.
func NewCheckHealth(svc *healthcheck.Service) (*CheckHealth, error) {
	if svc == nil {
		return nil, errors.New("handlers: NewCheckHealth: nil healthcheck service")
	}
	return &CheckHealth{svc: svc}, nil
}

// Execute parses the command body's trigger field and delegates to
// Service.Run. Unparseable / empty body → Scheduled. Mirrors the C#
// Execute branch at `:137-150`.
func (h *CheckHealth) Execute(ctx context.Context, body string) error {
	trigger := parseTrigger(body)
	return h.svc.Run(ctx, trigger)
}

// parseTrigger returns the Trigger encoded in body. Recognised forms:
//
//   - empty / "{}" → TriggerScheduled (the scheduler-tick default).
//   - {"trigger":"manual"} → TriggerManual.
//   - anything else → TriggerScheduled (defensive default; mirrors
//     C# CommandTrigger.Unspecified falling through to the
//     scheduled-only branch).
func parseTrigger(body string) healthcheck.Trigger {
	body = strings.TrimSpace(body)
	if body == "" || body == "{}" {
		return healthcheck.TriggerScheduled
	}
	var payload struct {
		Trigger string `json:"trigger"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return healthcheck.TriggerScheduled
	}
	if strings.EqualFold(payload.Trigger, "manual") {
		return healthcheck.TriggerManual
	}
	return healthcheck.TriggerScheduled
}
