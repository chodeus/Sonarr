package healthcheck

import (
	"context"
	"fmt"
)

// IndexerRssCheckName is the canonical Source identifier.
const IndexerRssCheckName = "IndexerRssCheck"

// RssEnabledLister narrows the indexer-factory surface the
// IndexerRssCheck needs. Mirrors the C# `_indexerFactory.RssEnabled
// (bool filterBlockedIndexers)` accessor at
// `src/NzbDrone.Core/Indexers/IndexerFactory.cs:108-118` —
//
//   - filterBlockedIndexers=false → every enabled indexer with
//     EnableRss=true (ignores backoff/block state).
//   - filterBlockedIndexers=true  → the above, additionally filtered
//     down to currently-active indexers (none in backoff).
//
// Both calls return the count, not the full row — the check only
// uses `.Empty()`.
type RssEnabledLister interface {
	RssEnabledCount(ctx context.Context, requireActive bool) (int, error)
}

// IndexerRssCheck warns when no enabled RSS indexer exists, OR every
// enabled RSS indexer is currently blocked by its status backoff.
// Mirrors `src/NzbDrone.Core/HealthCheck/Checks/IndexerRssCheck.cs`.
//
// Three outcomes:
//
//  1. Zero enabled RSS indexers → Error /
//     `ReasonIndexerRssNoIndexersEnabled`.
//  2. ≥1 enabled but zero active → Warning /
//     `ReasonIndexerRssNoIndexersAvailable`.
//  3. ≥1 active → OK.
type IndexerRssCheck struct {
	rss RssEnabledLister
}

// NewIndexerRssCheck builds the check. rss MUST be non-nil.
func NewIndexerRssCheck(rss RssEnabledLister) (*IndexerRssCheck, error) {
	if rss == nil {
		return nil, fmt.Errorf("healthcheck: NewIndexerRssCheck: nil RssEnabledLister")
	}
	return &IndexerRssCheck{rss: rss}, nil
}

// Name reports the canonical Source identifier.
func (*IndexerRssCheck) Name() string { return IndexerRssCheckName }

// CheckOnStartup mirrors C# `HealthCheckBase.CheckOnStartup` default
// `true`.
func (*IndexerRssCheck) CheckOnStartup() bool { return true }

// CheckOnSchedule mirrors C# `HealthCheckBase.CheckOnSchedule`
// default `true`.
func (*IndexerRssCheck) CheckOnSchedule() bool { return true }

// EventTriggers — the C# IndexerRssCheck registers
// `[CheckOn(typeof(ProviderAddedEvent<IIndexer>))]` and the other
// three indexer-provider events. Wiring those up requires the indexer
// event types to land in the Go port; today they aren't ported yet,
// so the check only runs on startup + scheduled batch (matching the
// default behaviour without event-driven re-runs).
func (*IndexerRssCheck) EventTriggers() []EventTrigger { return nil }

// Run executes the check.
func (c *IndexerRssCheck) Run(ctx context.Context) HealthCheck {
	enabled, err := c.rss.RssEnabledCount(ctx, false)
	if err != nil {
		return New(
			IndexerRssCheckName,
			ResultWarning,
			ReasonIndexerRssNoIndexersEnabled,
			"Unable to enumerate RSS-enabled indexers: "+err.Error(),
			"#no-indexers-available-with-rss-sync-enabled-sonarr-will-not-grab-new-releases-automatically",
		)
	}
	if enabled == 0 {
		return New(
			IndexerRssCheckName,
			ResultError,
			ReasonIndexerRssNoIndexersEnabled,
			"No indexers available with RSS sync enabled, Sonarr will not grab new releases automatically",
			"#no-indexers-available-with-rss-sync-enabled-sonarr-will-not-grab-new-releases-automatically",
		)
	}
	active, err := c.rss.RssEnabledCount(ctx, true)
	if err != nil {
		return New(
			IndexerRssCheckName,
			ResultWarning,
			ReasonIndexerRssNoIndexersAvailable,
			"Unable to enumerate active RSS indexers: "+err.Error(),
			"#indexers-are-unavailable-due-to-failures",
		)
	}
	if active == 0 {
		return New(
			IndexerRssCheckName,
			ResultWarning,
			ReasonIndexerRssNoIndexersAvailable,
			"All RSS-capable indexers are temporarily unavailable due to recent indexer errors",
			"#indexers-are-unavailable-due-to-failures",
		)
	}
	return OK(IndexerRssCheckName)
}
