package healthcheck

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/indexerstatus"
)

// IndexerStatusCheckName is the canonical Source identifier.
const IndexerStatusCheckName = "IndexerStatusCheck"

// IndexerRecentFailureWindow is the look-back used to filter blocked
// indexers down to "recently failed" ones — anything whose
// `InitialFailure` is within this window from now contributes to the
// short-term Indexer-status check. Mirrors the C#
// `InitialFailure.Value.After(DateTime.UtcNow.AddHours(-6))` filter
// at `IndexerStatusCheck.cs:34`.
//
// IndexerLongTermStatusCheck (a Phase 3 follow-up) will re-use the
// same constant on the OPPOSITE side — `InitialFailure < now - 6h`
// for the long-term variant.
const IndexerRecentFailureWindow = 6 * time.Hour

// EnabledIndexer is the narrow projection of `IndexerDefinition` the
// check needs — id + display name. The wider definition (Settings,
// ConfigContract, EnableRss/Search/AutomaticSearch, Priority, etc.)
// is irrelevant to the healthcheck; the narrow projection lets the
// host compose the "Enable predicate" however it pleases without
// dragging the full row through.
type EnabledIndexer struct {
	// ID matches the IndexerDefinition.Id used as the join key against
	// `indexerstatus.Status.ProviderID`.
	ID int
	// Name is the user-facing indexer name surfaced in the
	// some-down warning message (`indexerNames`).
	Name string
}

// EnabledIndexerLister returns every indexer the user has enabled
// (via `EnableRss || EnableAutomaticSearch || EnableInteractiveSearch`
// per the C# `IndexerDefinition.Enable` predicate at
// `src/NzbDrone.Core/Indexers/IndexerDefinition.cs:32`).
//
// Production wires this against `internal/indexers` once the indexer
// service surface lands; the Go port currently exposes only repo
// reads so the host wrapper handles the Enable filter at the
// boundary.
type EnabledIndexerLister interface {
	EnabledIndexers(ctx context.Context) ([]EnabledIndexer, error)
}

// IndexerStatusBlockedLister wraps the existing
// `indexerstatus.Service.GetBlockedProviders(ctx)` — the subset of
// status rows that are still inside their backoff window. The check
// further filters this down to recently-failed (per
// IndexerRecentFailureWindow) before deciding pass/fail.
type IndexerStatusBlockedLister interface {
	GetBlockedProviders(ctx context.Context) ([]indexerstatus.Status, error)
}

// IndexerStatusCheck warns when one or more enabled indexers have
// failed RECENTLY (within the IndexerRecentFailureWindow). Mirrors
// `src/NzbDrone.Core/HealthCheck/Checks/IndexerStatusCheck.cs:1-62`.
//
// Three outcomes:
//
//  1. Every enabled indexer is healthy (or no enabled indexers
//     exist) → OK.
//  2. SOME enabled indexers are blocked-and-recent → Warning /
//     `ReasonIndexerStatusUnavailable` with comma-separated names.
//  3. ALL enabled indexers are blocked-and-recent → Error /
//     `ReasonIndexerStatusAllUnavailable`.
type IndexerStatusCheck struct {
	indexers EnabledIndexerLister
	statuses IndexerStatusBlockedLister
	now      func() time.Time
}

// NewIndexerStatusCheck builds the check. indexers + statuses MUST
// be non-nil. now defaults to `time.Now().UTC()` when nil.
func NewIndexerStatusCheck(indexers EnabledIndexerLister, statuses IndexerStatusBlockedLister, now func() time.Time) (*IndexerStatusCheck, error) {
	if indexers == nil {
		return nil, fmt.Errorf("healthcheck: NewIndexerStatusCheck: nil EnabledIndexerLister")
	}
	if statuses == nil {
		return nil, fmt.Errorf("healthcheck: NewIndexerStatusCheck: nil IndexerStatusBlockedLister")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &IndexerStatusCheck{indexers: indexers, statuses: statuses, now: now}, nil
}

// Name returns the canonical check identifier.
func (*IndexerStatusCheck) Name() string { return IndexerStatusCheckName }

// CheckOnStartup defaults true.
func (*IndexerStatusCheck) CheckOnStartup() bool { return true }

// CheckOnSchedule defaults true.
func (*IndexerStatusCheck) CheckOnSchedule() bool { return true }

// EventTriggers wires the C# `[CheckOn(...)]` attributes at
// `IndexerStatusCheck.cs:11-13`. The C# class declares three
// `ProviderXxxEvent<IIndexer>` triggers; none of those event types
// have been ported to the Go tree yet (Phase 4 territory — they ride
// on the indexer-provider-factory wiring). The check still
// implements `EventDrivenCheck` (with an empty trigger slice) for a
// uniform registration shape; the bindings land alongside their
// event types.
func (*IndexerStatusCheck) EventTriggers() []EventTrigger { return nil }

// Run mirrors the C# `Check()` method at `:26-59`.
//
// Errors from either underlying lookup surface as Warning rather
// than crashing — the C# tree would let the exception escape and the
// orchestrator's panic guard would record it as a DecisionError; the
// Go port records the failure on the check itself.
func (c *IndexerStatusCheck) Run(ctx context.Context) HealthCheck {
	enabled, err := c.indexers.EnabledIndexers(ctx)
	if err != nil {
		return New(
			IndexerStatusCheckName,
			ResultWarning,
			ReasonIndexerStatusUnavailable,
			"Unable to enumerate indexers: "+err.Error(),
			"#indexers-are-unavailable-due-to-failures",
		)
	}
	if len(enabled) == 0 {
		// No enabled indexers at all → vacuously OK; mirrors the C#
		// `enabledProviders` empty case which would produce an empty
		// join + Empty() short-circuit at `:37`.
		return OK(IndexerStatusCheckName)
	}

	blocked, err := c.statuses.GetBlockedProviders(ctx)
	if err != nil {
		return New(
			IndexerStatusCheckName,
			ResultWarning,
			ReasonIndexerStatusUnavailable,
			"Unable to enumerate indexer statuses: "+err.Error(),
			"#indexers-are-unavailable-due-to-failures",
		)
	}

	now := c.now()
	cutoff := now.Add(-IndexerRecentFailureWindow)

	// Build a providerID → recent-failure-status lookup.
	recentByID := make(map[int]bool, len(blocked))
	for _, s := range blocked {
		if s.InitialFailure == nil {
			continue
		}
		if s.InitialFailure.After(cutoff) {
			recentByID[s.ProviderID] = true
		}
	}

	// Inner-join enabled indexers with recently-blocked statuses.
	var down []EnabledIndexer
	for _, ix := range enabled {
		if recentByID[ix.ID] {
			down = append(down, ix)
		}
	}
	if len(down) == 0 {
		return OK(IndexerStatusCheckName)
	}
	if len(down) == len(enabled) {
		return New(
			IndexerStatusCheckName,
			ResultError,
			ReasonIndexerStatusAllUnavailable,
			"All indexers are unavailable due to failures",
			"#indexers-are-unavailable-due-to-failures",
		)
	}

	// Sort by Name (case-insensitive) for stable rendering across
	// runs. The C# tree relies on the LINQ Join order; the Go port
	// pins it explicitly, matching the precedent set by
	// RemovedSeriesCheck (3.44).
	sort.Slice(down, func(i, j int) bool {
		return strings.ToLower(down[i].Name) < strings.ToLower(down[j].Name)
	})
	names := make([]string, 0, len(down))
	for _, ix := range down {
		names = append(names, ix.Name)
	}
	return New(
		IndexerStatusCheckName,
		ResultWarning,
		ReasonIndexerStatusUnavailable,
		"Indexers unavailable due to failures: "+strings.Join(names, ", "),
		"#indexers-are-unavailable-due-to-failures",
	)
}
