package healthcheck

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"sonarr.tv/sonarr/internal/series"
)

// RemovedSeriesCheckName is the canonical Source identifier.
const RemovedSeriesCheckName = "RemovedSeriesCheck"

// RemovedSeriesLister is the narrow series-side dependency the check
// needs — every persisted series row. Wraps the existing
// `series.Service.GetAllSeries(ctx)`.
type RemovedSeriesLister interface {
	GetAllSeries(ctx context.Context) ([]series.Series, error)
}

// RemovedSeriesCheck warns when one or more tracked series have been
// flagged `SeriesStatusDeleted` by the metadata refresh pipeline (the
// upstream metadata source — typically TheTVDB — removed the show).
//
// Mirrors `src/NzbDrone.Core/HealthCheck/Checks/RemovedSeriesCheck.cs:1-66`.
//
// Two outcomes:
//
//  1. No deleted series → OK.
//  2. One-or-more deleted → Error with reason
//     `ReasonRemovedSeriesSingle` (count == 1) or
//     `ReasonRemovedSeriesMultiple` (count > 1). Message lists each
//     deleted series as `<Title> (tvdbid <ID>)`, comma-separated and
//     sorted alphabetically by title for stable rendering.
type RemovedSeriesCheck struct {
	lister RemovedSeriesLister
}

// NewRemovedSeriesCheck builds the check. lister MUST be non-nil.
func NewRemovedSeriesCheck(lister RemovedSeriesLister) (*RemovedSeriesCheck, error) {
	if lister == nil {
		return nil, fmt.Errorf("healthcheck: NewRemovedSeriesCheck: nil RemovedSeriesLister")
	}
	return &RemovedSeriesCheck{lister: lister}, nil
}

// Name returns the canonical check identifier.
func (*RemovedSeriesCheck) Name() string { return RemovedSeriesCheckName }

// CheckOnStartup defaults true.
func (*RemovedSeriesCheck) CheckOnStartup() bool { return true }

// CheckOnSchedule defaults true.
func (*RemovedSeriesCheck) CheckOnSchedule() bool { return true }

// EventTriggers wires the C# `[CheckOn(...)]` attributes at
// `RemovedSeriesCheck.cs:10-12`. The C# class declares three
// triggers:
//
//   - SeriesUpdatedEvent (with `ICheckOnCondition<SeriesUpdatedEvent>`
//     filter — only fires when `Series.Status == Deleted`).
//   - SeriesDeletedEvent (with `ICheckOnCondition<SeriesDeletedEvent>`
//     filter — only fires when at least one removed series had
//     `Status == Deleted`).
//   - SeriesRefreshCompleteEvent (no filter).
//
// The Go port currently has only `SeriesDeletedEvent` available;
// `SeriesUpdatedEvent` and `SeriesRefreshCompleteEvent` haven't been
// ported yet (Phase 3 follow-ups in series subsystem). The
// SeriesDeletedEvent binding here applies the C# filter via the
// `Filter` callback — the check fires only when at least one of the
// removed series had Status == Deleted at delete time.
//
// SeriesUpdatedEvent + SeriesRefreshCompleteEvent bindings land
// trivially when those events arrive in the series subsystem.
func (*RemovedSeriesCheck) EventTriggers() []EventTrigger {
	return []EventTrigger{
		{
			EventType: reflect.TypeOf(series.SeriesDeletedEvent{}),
			Condition: CheckOnAlways,
			Filter:    seriesDeletedHadDeletedStatus,
		},
	}
}

// seriesDeletedHadDeletedStatus mirrors the C#
// `ICheckOnCondition<SeriesDeletedEvent>.ShouldCheckOnEvent` at
// `:56-59`: returns true only when at least one of the deleted
// series was `Status == Deleted`. The plain "user removed it
// manually from Sonarr" flow doesn't trigger a redundant health-
// check refresh.
func seriesDeletedHadDeletedStatus(ev any) bool {
	deleted, ok := ev.(series.SeriesDeletedEvent)
	if !ok {
		return false
	}
	for _, s := range deleted.Series {
		if s.Status == series.SeriesStatusDeleted {
			return true
		}
	}
	return false
}

// Run mirrors the C# `Check()` method at `:23-54`.
func (c *RemovedSeriesCheck) Run(ctx context.Context) HealthCheck {
	all, err := c.lister.GetAllSeries(ctx)
	if err != nil {
		// Surface the lookup failure as a warning rather than crashing.
		// Same pattern as RootFolderCheck on AllSeriesPaths failure.
		return New(
			RemovedSeriesCheckName,
			ResultWarning,
			ReasonRemovedSeriesSingle,
			"Unable to enumerate series: "+err.Error(),
			"#series-removed-from-thetvdb",
		)
	}

	deleted := make([]series.Series, 0)
	for _, s := range all {
		if s.Status == series.SeriesStatusDeleted {
			deleted = append(deleted, s)
		}
	}
	if len(deleted) == 0 {
		return OK(RemovedSeriesCheckName)
	}

	// Sort by Title (case-insensitive) for stable rendering across
	// runs. The C# tree relies on the SeriesService ordering; the Go
	// port pins the order explicitly.
	sort.Slice(deleted, func(i, j int) bool {
		return strings.ToLower(deleted[i].Title) < strings.ToLower(deleted[j].Title)
	})

	parts := make([]string, 0, len(deleted))
	for _, s := range deleted {
		parts = append(parts, fmt.Sprintf("%s (tvdbid %d)", s.Title, s.TvdbID))
	}
	listed := strings.Join(parts, ", ")

	if len(deleted) == 1 {
		return New(
			RemovedSeriesCheckName,
			ResultError,
			ReasonRemovedSeriesSingle,
			"Series removed from TheTVDB: "+listed,
			"#series-removed-from-thetvdb",
		)
	}
	return New(
		RemovedSeriesCheckName,
		ResultError,
		ReasonRemovedSeriesMultiple,
		"Series removed from TheTVDB: "+listed,
		"#series-removed-from-thetvdb",
	)
}
