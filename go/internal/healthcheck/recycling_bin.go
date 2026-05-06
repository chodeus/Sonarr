package healthcheck

import (
	"context"
	"fmt"
	"strings"
)

// RecyclingBinCheckName is the canonical Source identifier.
const RecyclingBinCheckName = "RecyclingBinCheck"

// RecyclingBinCheckConfig is the narrow config dependency the check
// needs: the user's recycling-bin path. Implementations typically wrap
// the larger config-service surface; the narrow interface keeps the
// check decoupled from a not-yet-ported full IConfigService (the
// recycling-bin field hasn't been ported to `configfile.Provider` yet,
// so a focused interface is preferable to extending it speculatively).
type RecyclingBinCheckConfig interface {
	// RecycleBin returns the configured recycling-bin folder. Empty /
	// whitespace-only means "no bin configured" — the check passes
	// silently in that case (mirroring the C# `IsNullOrWhiteSpace`
	// short-circuit at `:30`).
	RecycleBin() string
}

// RecyclingBinDiskProvider is the slice of `IDiskProvider` the check
// reaches for: a single FolderWritable predicate. Stubbed in tests;
// the real impl ports alongside the wider `IDiskProvider` surface.
type RecyclingBinDiskProvider interface {
	// FolderWritable mirrors the C# `IDiskProvider.FolderWritable(path)`
	// predicate — true when the folder exists AND the running user
	// can create files inside it.
	FolderWritable(path string) bool
}

// RecyclingBinCheck warns when the user has configured a recycling-bin
// folder that the running process cannot write to. Mirrors
// `src/NzbDrone.Core/HealthCheck/Checks/RecyclingBinCheck.cs:1-48`.
//
// Three outcomes:
//
//  1. Recycle-bin path empty / whitespace-only → OK (feature off).
//  2. Path set AND FolderWritable returns false → Error /
//     `ReasonRecycleBinUnableToWrite`.
//  3. Path set AND FolderWritable returns true → OK.
type RecyclingBinCheck struct {
	cfg  RecyclingBinCheckConfig
	disk RecyclingBinDiskProvider
}

// NewRecyclingBinCheck builds the check. Both arguments MUST be
// non-nil — there's no sensible fallback for either dependency, and
// the check would silently no-op (returning OK on every run) if one
// were missing.
func NewRecyclingBinCheck(cfg RecyclingBinCheckConfig, disk RecyclingBinDiskProvider) (*RecyclingBinCheck, error) {
	if cfg == nil {
		return nil, fmt.Errorf("healthcheck: NewRecyclingBinCheck: nil RecyclingBinCheckConfig")
	}
	if disk == nil {
		return nil, fmt.Errorf("healthcheck: NewRecyclingBinCheck: nil RecyclingBinDiskProvider")
	}
	return &RecyclingBinCheck{cfg: cfg, disk: disk}, nil
}

// Name returns the canonical check identifier.
func (*RecyclingBinCheck) Name() string { return RecyclingBinCheckName }

// CheckOnStartup defaults true.
func (*RecyclingBinCheck) CheckOnStartup() bool { return true }

// CheckOnSchedule defaults true.
func (*RecyclingBinCheck) CheckOnSchedule() bool { return true }

// EventTriggers wires the C# `[CheckOn(...)]` attributes at
// `RecyclingBinCheck.cs:10-11`. The C# class declares two triggers:
//
//   - EpisodeImportedEvent (CheckOnCondition.FailedOnly).
//   - EpisodeImportFailedEvent (CheckOnCondition.SuccessfulOnly).
//
// Neither event has been ported to the Go tree yet — both live in
// the not-yet-ported mediafiles subsystem. The bindings will land
// alongside their event types in a Phase 3 follow-up; until then the
// check still runs on startup + scheduled batches (the orchestrator
// surface is unaffected).
//
// The check still implements EventDrivenCheck (with an empty trigger
// list) so the registration shape is uniform across checks; the
// orchestrator iterates EventTriggers() before indexing and a
// zero-length slice is a no-op.
func (*RecyclingBinCheck) EventTriggers() []EventTrigger {
	return nil
}

// Run mirrors the C# `Check()` method at `:24-46`.
func (c *RecyclingBinCheck) Run(_ context.Context) HealthCheck {
	bin := c.cfg.RecycleBin()
	if strings.TrimSpace(bin) == "" {
		return OK(RecyclingBinCheckName)
	}
	if !c.disk.FolderWritable(bin) {
		return New(
			RecyclingBinCheckName,
			ResultError,
			ReasonRecycleBinUnableToWrite,
			fmt.Sprintf("Unable to write to configured recycling bin folder: %s. Ensure this path exists and is writable by the user running Sonarr", bin),
			"#cannot-write-recycle-bin",
		)
	}
	return OK(RecyclingBinCheckName)
}
