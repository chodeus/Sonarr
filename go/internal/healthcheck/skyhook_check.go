package healthcheck

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"sonarr.tv/sonarr/internal/metadatasource"
)

// SkyHookCheckName is the canonical Source identifier emitted on the
// v3/v5 healthcheck API. Used as the cache key in
// healthcheck.Service's result map.
const SkyHookCheckName = "SkyHookCheck"

// SkyHookKnownTvdbID is the upstream TVDB id this check probes. C#
// `SkyHookProxy.cs:43-71` has no Test() method — the Go port
// introduces this healthcheck per PROV-Q36 in
// `go/analysis/providers/metadata-sources.md`. The probe id is
// "Breaking Bad" (81189) — a long-running ended series with no
// likelihood of removal from TVDB, so a 404 here genuinely signals
// SkyHook-side trouble (not "this id is gone").
//
// Exposed as an exported constant so tests + the production
// adapter share a single source of truth.
const SkyHookKnownTvdbID = 81189

// SkyHookProber is the narrow nil-tolerant interface SkyHookCheck
// uses to do its periodic upstream probe. Production wires this
// against a metadatasource.Proxy via a one-method adapter (the
// proxy's surface is wider than the check needs — only "does a
// GetSeriesInfo succeed?" matters here).
//
// The adapter shape lives in cmd/sonarr/main.go so this package
// stays leaf-y in the import graph (the healthcheck package can
// import metadatasource for the sentinel error map, but the
// adapter implementation belongs with the wiring code).
type SkyHookProber interface {
	// ProbeKnownSeries fires a GetSeriesInfo against the known-stable
	// TVDB id. Returns nil on success; the adapter is responsible
	// for translating successful response bodies into nil error.
	//
	// Errors returned should preserve the metadatasource sentinel
	// chain (errors.Is(err, metadatasource.ErrServiceUnavailable))
	// so [SkyHookCheck.Run] can classify the failure correctly.
	ProbeKnownSeries(ctx context.Context) error
}

// SkyHookCheck periodically probes the SkyHook metadata-source
// upstream so operators are alerted to outages WITHOUT having to
// trigger a user-driven add-series flow that fails. C# tree has no
// equivalent (the C# SkyHookProxy is a singleton with no Test() —
// see go/analysis/providers/metadata-sources.md PROV-Q36).
//
// Three outcomes:
//
//  1. Probe succeeds → OK (no result entry; orchestrator publishes
//     RestoredEvent if previously failing).
//  2. Probe fails with errors.Is(err, metadatasource.ErrServiceUnavailable)
//     → Error / ReasonSkyHookUnavailable. SkyHook is reachable-
//     but-degraded or unreachable; user-add will fail.
//  3. Probe fails with anything else (decode failure / unexpected
//     shape / ctx error) → Warning / ReasonSkyHookProbeFailed.
//     Less-severe — could be transient.
//
// Slice 4.AV (queue [M4] of Phase 4 focused-subset 5 —
// METADATA-SOURCES family). Pattern-mirrors slice 4.AI's
// NotificationStatusCheck single-probe shape.
type SkyHookCheck struct {
	prober SkyHookProber
	log    *slog.Logger
}

// NewSkyHookCheck builds the check. prober MUST be non-nil — the
// healthcheck framework treats an absent prober as a bug, not a
// graceful skip, because main.go is responsible for wiring exactly
// the checks it can drive.
func NewSkyHookCheck(prober SkyHookProber, log *slog.Logger) (*SkyHookCheck, error) {
	if prober == nil {
		return nil, fmt.Errorf("healthcheck: NewSkyHookCheck: nil SkyHookProber")
	}
	if log == nil {
		log = slog.Default()
	}
	return &SkyHookCheck{prober: prober, log: log}, nil
}

// Name returns the canonical check identifier.
func (*SkyHookCheck) Name() string { return SkyHookCheckName }

// CheckOnStartup defaults true — operators should learn about
// SkyHook unreachability at boot, not on the first user action.
func (*SkyHookCheck) CheckOnStartup() bool { return true }

// CheckOnSchedule defaults true — the periodic healthcheck tick
// re-runs this probe, surfacing recovery + new failures.
func (*SkyHookCheck) CheckOnSchedule() bool { return true }

// EventTriggers returns nil — no event-driven retriggers. The
// SkyHook probe is purely cadence-driven; no user action causes a
// state change worth re-probing immediately.
func (*SkyHookCheck) EventTriggers() []EventTrigger { return nil }

// Run fires one probe against the upstream and classifies the
// outcome per the docstring on [SkyHookCheck]. ctx cancellation
// propagates through the prober and surfaces as the "other error"
// branch (ResultWarning) — cancelled probes shouldn't surface as
// "SkyHook unavailable" since the cancellation came from us.
func (c *SkyHookCheck) Run(ctx context.Context) HealthCheck {
	err := c.prober.ProbeKnownSeries(ctx)
	if err == nil {
		return OK(SkyHookCheckName)
	}
	if errors.Is(err, metadatasource.ErrServiceUnavailable) {
		c.log.Warn("healthcheck: SkyHook unreachable", "err", err)
		return New(
			SkyHookCheckName,
			ResultError,
			ReasonSkyHookUnavailable,
			"SkyHook metadata source is unavailable; new series cannot be added until it recovers",
			"#skyhook-is-unavailable",
		)
	}
	c.log.Warn("healthcheck: SkyHook probe failed", "err", err)
	return New(
		SkyHookCheckName,
		ResultWarning,
		ReasonSkyHookProbeFailed,
		"SkyHook probe returned an unexpected error: "+err.Error(),
		"#skyhook-probe-failed",
	)
}

// Compile-time guard. The optional EventDrivenCheck guard would
// also be valid since EventTriggers is defined; we satisfy Check
// here and leave the EventDrivenCheck-aware orchestrator to do its
// own type-assert (matching the [NotificationStatusCheck]
// precedent).
var _ Check = (*SkyHookCheck)(nil)
