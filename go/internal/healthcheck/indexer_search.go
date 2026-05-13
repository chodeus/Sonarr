package healthcheck

import (
	"context"
	"fmt"
)

// IndexerSearchCheckName is the canonical Source identifier.
const IndexerSearchCheckName = "IndexerSearchCheck"

// SearchEnabledLister narrows the indexer-factory surface the
// IndexerSearchCheck needs. Three counts feed the three failure
// branches:
//
//   - AutomaticSearchEnabledCount(false): any enabled indexer with
//     EnableAutomaticSearch=true, regardless of backoff state.
//   - InteractiveSearchEnabledCount(): any enabled indexer with
//     EnableInteractiveSearch=true (no backoff filter — C# uses
//     filterBlockedIndexers=false on this branch).
//   - AutomaticSearchEnabledCount(true): same as first but filtered
//     down to currently-active.
//
// Mirrors C# `_indexerFactory.AutomaticSearchEnabled(bool)` +
// `InteractiveSearchEnabled(bool)` at
// `src/NzbDrone.Core/Indexers/IndexerFactory.cs:120-140`.
type SearchEnabledLister interface {
	AutomaticSearchEnabledCount(ctx context.Context, requireActive bool) (int, error)
	InteractiveSearchEnabledCount(ctx context.Context) (int, error)
}

// IndexerSearchCheck warns when no search-capable indexer exists, or
// when all enabled ones are currently blocked by backoff. Mirrors
// `src/NzbDrone.Core/HealthCheck/Checks/IndexerSearchCheck.cs`.
//
// Four outcomes (in C# order):
//
//  1. No enabled auto-search indexer → Warning /
//     `ReasonIndexerSearchNoAutomatic`.
//  2. No enabled interactive-search indexer → Warning /
//     `ReasonIndexerSearchNoInteractive`.
//  3. No active auto-search indexer → Warning /
//     `ReasonIndexerSearchNoAvailableIndexers`.
//  4. Otherwise → OK.
type IndexerSearchCheck struct {
	search SearchEnabledLister
}

// NewIndexerSearchCheck builds the check. search MUST be non-nil.
func NewIndexerSearchCheck(search SearchEnabledLister) (*IndexerSearchCheck, error) {
	if search == nil {
		return nil, fmt.Errorf("healthcheck: NewIndexerSearchCheck: nil SearchEnabledLister")
	}
	return &IndexerSearchCheck{search: search}, nil
}

// Name reports the canonical Source identifier.
func (*IndexerSearchCheck) Name() string { return IndexerSearchCheckName }

// CheckOnStartup mirrors the C# default `true`.
func (*IndexerSearchCheck) CheckOnStartup() bool { return true }

// CheckOnSchedule mirrors the C# default `true`.
func (*IndexerSearchCheck) CheckOnSchedule() bool { return true }

// EventTriggers — same pattern as IndexerRssCheck. The C# tree
// registers the four indexer-provider events; event-driven re-runs
// require the Go indexer event types which haven't ported yet.
func (*IndexerSearchCheck) EventTriggers() []EventTrigger { return nil }

// Run executes the check.
func (c *IndexerSearchCheck) Run(ctx context.Context) HealthCheck {
	auto, err := c.search.AutomaticSearchEnabledCount(ctx, false)
	if err != nil {
		return New(
			IndexerSearchCheckName,
			ResultWarning,
			ReasonIndexerSearchNoAutomatic,
			"Unable to enumerate auto-search indexers: "+err.Error(),
			"#no-indexers-available-with-automatic-search-enabled-sonarr-will-not-provide-any-automatic-search-results",
		)
	}
	if auto == 0 {
		return New(
			IndexerSearchCheckName,
			ResultWarning,
			ReasonIndexerSearchNoAutomatic,
			"No indexers available with Automatic Search enabled, Sonarr will not provide any automatic search results",
			"#no-indexers-available-with-automatic-search-enabled-sonarr-will-not-provide-any-automatic-search-results",
		)
	}

	interactive, err := c.search.InteractiveSearchEnabledCount(ctx)
	if err != nil {
		return New(
			IndexerSearchCheckName,
			ResultWarning,
			ReasonIndexerSearchNoInteractive,
			"Unable to enumerate interactive-search indexers: "+err.Error(),
			"#no-indexers-available-with-interactive-search-enabled",
		)
	}
	if interactive == 0 {
		return New(
			IndexerSearchCheckName,
			ResultWarning,
			ReasonIndexerSearchNoInteractive,
			"No indexers available with Interactive Search enabled, Sonarr will not provide any interactive search results",
			"#no-indexers-available-with-interactive-search-enabled",
		)
	}

	active, err := c.search.AutomaticSearchEnabledCount(ctx, true)
	if err != nil {
		return New(
			IndexerSearchCheckName,
			ResultWarning,
			ReasonIndexerSearchNoAvailableIndexers,
			"Unable to enumerate active auto-search indexers: "+err.Error(),
			"#indexers-are-unavailable-due-to-failures",
		)
	}
	if active == 0 {
		return New(
			IndexerSearchCheckName,
			ResultWarning,
			ReasonIndexerSearchNoAvailableIndexers,
			"All search-capable indexers are temporarily unavailable due to recent indexer errors",
			"#indexers-are-unavailable-due-to-failures",
		)
	}

	return OK(IndexerSearchCheckName)
}
