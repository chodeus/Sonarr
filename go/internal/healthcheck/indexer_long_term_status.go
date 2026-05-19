package healthcheck

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// IndexerLongTermStatusCheckName is the canonical Source identifier.
const IndexerLongTermStatusCheckName = "IndexerLongTermStatusCheck"

// IndexerLongTermStatusCheck warns when one or more enabled indexers
// have been blocked LONGER than `IndexerRecentFailureWindow` (6 hours
// today). Mirrors
// `src/NzbDrone.Core/HealthCheck/Checks/IndexerLongTermStatusCheck.cs:1-61`.
//
// The shape is identical to [IndexerStatusCheck] except for the
// time-comparison sign and the reason codes:
//
//   - IndexerStatusCheck flags InitialFailure AFTER (now - 6h) —
//     "recently failed".
//   - IndexerLongTermStatusCheck flags InitialFailure BEFORE
//     (now - 6h) — "failed for a long time".
//
// Outcomes:
//
//  1. Every enabled indexer either has no failure OR a recent-only
//     failure → OK.
//  2. SOME enabled indexers are long-blocked → Warning /
//     `ReasonIndexerLongTermStatusUnavailable` with names.
//  3. ALL enabled indexers are long-blocked → Error /
//     `ReasonIndexerLongTermStatusAllUnavailable`.
//
// Re-uses the same `EnabledIndexerLister` + `IndexerStatusBlockedLister`
// interfaces from IndexerStatusCheck so wiring stays single-source.
type IndexerLongTermStatusCheck struct {
	indexers EnabledIndexerLister
	statuses IndexerStatusBlockedLister
	now      func() time.Time
}

// NewIndexerLongTermStatusCheck builds the check. indexers + statuses
// MUST be non-nil. now defaults to `time.Now().UTC()` when nil.
func NewIndexerLongTermStatusCheck(indexers EnabledIndexerLister, statuses IndexerStatusBlockedLister, now func() time.Time) (*IndexerLongTermStatusCheck, error) {
	if indexers == nil {
		return nil, fmt.Errorf("healthcheck: NewIndexerLongTermStatusCheck: nil EnabledIndexerLister")
	}
	if statuses == nil {
		return nil, fmt.Errorf("healthcheck: NewIndexerLongTermStatusCheck: nil IndexerStatusBlockedLister")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &IndexerLongTermStatusCheck{indexers: indexers, statuses: statuses, now: now}, nil
}

// Name returns the canonical check identifier.
func (*IndexerLongTermStatusCheck) Name() string { return IndexerLongTermStatusCheckName }

// CheckOnStartup defaults true.
func (*IndexerLongTermStatusCheck) CheckOnStartup() bool { return true }

// CheckOnSchedule defaults true.
func (*IndexerLongTermStatusCheck) CheckOnSchedule() bool { return true }

// EventTriggers mirrors the three C# `[CheckOn]` attributes at
// `IndexerLongTermStatusCheck.cs:11-13`. As with IndexerStatusCheck
// the C# provider-event types aren't ported yet; the bindings will
// slot in alongside their event types. Empty trigger slice today.
func (*IndexerLongTermStatusCheck) EventTriggers() []EventTrigger { return nil }

// Run mirrors the C# `Check()` method at `:26-59`.
func (c *IndexerLongTermStatusCheck) Run(ctx context.Context) HealthCheck {
	enabled, err := c.indexers.EnabledIndexers(ctx)
	if err != nil {
		return New(
			IndexerLongTermStatusCheckName,
			ResultWarning,
			ReasonIndexerLongTermStatusUnavailable,
			"Unable to enumerate indexers: "+err.Error(),
			"#indexers-are-unavailable-due-to-failures",
		)
	}
	if len(enabled) == 0 {
		return OK(IndexerLongTermStatusCheckName)
	}

	blocked, err := c.statuses.GetBlockedProviders(ctx)
	if err != nil {
		return New(
			IndexerLongTermStatusCheckName,
			ResultWarning,
			ReasonIndexerLongTermStatusUnavailable,
			"Unable to enumerate indexer statuses: "+err.Error(),
			"#indexers-are-unavailable-due-to-failures",
		)
	}

	cutoff := c.now().Add(-IndexerRecentFailureWindow)
	longByID := make(map[int]bool, len(blocked))
	for _, s := range blocked {
		if s.InitialFailure == nil {
			continue
		}
		// Long-term: failure started BEFORE the cutoff (i.e. older than 6h).
		if s.InitialFailure.Before(cutoff) {
			longByID[s.ProviderID] = true
		}
	}

	var down []EnabledIndexer
	for _, ix := range enabled {
		if longByID[ix.ID] {
			down = append(down, ix)
		}
	}
	if len(down) == 0 {
		return OK(IndexerLongTermStatusCheckName)
	}
	if len(down) == len(enabled) {
		return New(
			IndexerLongTermStatusCheckName,
			ResultError,
			ReasonIndexerLongTermStatusAllUnavailable,
			"All indexers are unavailable due to long-term failures",
			"#indexers-are-unavailable-due-to-failures",
		)
	}

	sort.SliceStable(down, func(i, j int) bool {
		return strings.ToLower(down[i].Name) < strings.ToLower(down[j].Name)
	})
	names := make([]string, 0, len(down))
	for _, ix := range down {
		names = append(names, ix.Name)
	}
	return New(
		IndexerLongTermStatusCheckName,
		ResultWarning,
		ReasonIndexerLongTermStatusUnavailable,
		"Indexers unavailable due to long-term failures: "+strings.Join(names, ", "),
		"#indexers-are-unavailable-due-to-failures",
	)
}
