package healthcheck

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"
)

// SystemTimeCheckName is the canonical Source identifier.
const SystemTimeCheckName = "SystemTimeCheck"

// SystemTimeDriftThreshold is the absolute clock drift threshold the
// check tolerates. Mirrors the C# `Math.Abs(...).TotalDays >= 1`
// predicate at `SystemTimeCheck.cs:37`. Anything ≥ 24h triggers a
// fatal Error; smaller drifts are ignored (a more fine-grained
// tolerance lives in the C# tree only inside the time-sync command,
// not the healthcheck).
const SystemTimeDriftThreshold = 24 * time.Hour

// SystemTimeFetcher returns the upstream "true" wall-clock from the
// Sonarr cloud `/time` endpoint. The check compares this to the
// local clock and trips when |delta| ≥ SystemTimeDriftThreshold.
//
// Production wiring builds the request via the cloud-request-builder
// (C# `ISonarrCloudRequestBuilder.Services` at `:13`) and parses the
// `ServiceTimeResponse.DateTimeUtc` JSON field; that wire-shape lives
// in the host once the cloud-request-builder + HttpClient ports land.
//
// fetcher errors are swallowed (the check returns OK) — mirrors the
// C# `catch (Exception e)` block at `:47-50` which logs at Warn level
// and falls through to the passing branch. Network blips, DNS
// hiccups, and cloud-side outages should not surface as user-visible
// healthcheck failures.
type SystemTimeFetcher func(ctx context.Context) (time.Time, error)

// SystemTimeCheck warns when the local system clock drifts from the
// upstream cloud-time service by 24h or more. Mirrors
// `src/NzbDrone.Core/HealthCheck/Checks/SystemTimeCheck.cs:1-60`.
//
// Three outcomes:
//
//  1. Fetcher returns error → OK (logged at Warn; same as C# catch).
//  2. Returned time within ±24h of local clock → OK.
//  3. |delta| ≥ 24h → Error / `ReasonSystemTime`.
type SystemTimeCheck struct {
	fetcher SystemTimeFetcher
	now     func() time.Time
	log     *slog.Logger
}

// NewSystemTimeCheck builds the check. fetcher MUST be non-nil. now
// defaults to time.Now.UTC when nil; log defaults to slog.Default()
// when nil. Production wires both at the host boundary; tests inject
// stubs for deterministic clocks.
func NewSystemTimeCheck(fetcher SystemTimeFetcher, now func() time.Time, log *slog.Logger) (*SystemTimeCheck, error) {
	if fetcher == nil {
		return nil, fmt.Errorf("healthcheck: NewSystemTimeCheck: nil SystemTimeFetcher")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if log == nil {
		log = slog.Default()
	}
	return &SystemTimeCheck{fetcher: fetcher, now: now, log: log}, nil
}

// Name returns the canonical check identifier.
func (*SystemTimeCheck) Name() string { return SystemTimeCheckName }

// CheckOnStartup defaults true.
func (*SystemTimeCheck) CheckOnStartup() bool { return true }

// CheckOnSchedule defaults true.
func (*SystemTimeCheck) CheckOnSchedule() bool { return true }

// EventTriggers returns nil. The C# tree decorates the check with
// no `[CheckOn]` attributes — it only runs on startup + scheduled
// batches. The Go port still implements `EventDrivenCheck` (with an
// empty trigger slice) for uniform registration shape.
func (*SystemTimeCheck) EventTriggers() []EventTrigger { return nil }

// Run mirrors the C# `Check()` method at `:24-53`.
func (c *SystemTimeCheck) Run(ctx context.Context) HealthCheck {
	upstream, err := c.fetcher(ctx)
	if err != nil {
		// C# `:47-50` — log at Warn and fall through to OK.
		c.log.Warn("Unable to verify system time", slog.String("error", err.Error()))
		return OK(SystemTimeCheckName)
	}

	local := c.now()
	drift := upstream.Sub(local)
	if math.Abs(drift.Hours()) < SystemTimeDriftThreshold.Hours() {
		return OK(SystemTimeCheckName)
	}

	c.log.Error("System time mismatch",
		slog.Time("system_time", local),
		slog.Time("expected_time", upstream),
		slog.String("drift", drift.String()),
	)
	return New(
		SystemTimeCheckName,
		ResultError,
		ReasonSystemTime,
		"System time is off by more than 1 day. Scheduled tasks may not run correctly until the time is corrected",
		"#system-time-off",
	)
}
