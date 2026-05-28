package queue

import "sonarr.tv/sonarr/internal/indexers"

// FilterCriteria is the domain-level queue filter, mirroring the C#
// `QueueController.GetQueue` filter surface at
// `src/Sonarr.Api.V3/Queue/QueueController.cs:173-217`. Every set/slice
// field is "no filter" when empty; the pointer Protocol is "no filter"
// when nil.
type FilterCriteria struct {
	// IncludeUnknownSeriesItems, when false, drops rows whose Series is
	// nil (C# `:179` `queue.Where(q => q.Series != null)`). Defaults to
	// false at the controller (`:141`) — the common case excludes
	// series-less rows.
	IncludeUnknownSeriesItems bool

	// SeriesIDs keeps only rows whose Series.ID is in the set (C#
	// `:191-194`). Empty = no filter.
	SeriesIDs map[int]bool

	// Protocol keeps only rows matching the protocol (C# `:196-199`).
	// nil = no filter.
	Protocol *indexers.DownloadProtocol

	// Languages keeps rows where ANY language id is in the set (C#
	// `:201-204`). Empty = no filter.
	Languages map[int]bool

	// Quality keeps rows whose Quality.Quality.ID is in the set (C#
	// `:206-209`). Empty = no filter.
	Quality map[int]bool

	// Status keeps rows whose Status is in the set (C# `:211-214`).
	// Empty = no filter.
	Status map[QueueStatus]bool
}

// active reports whether any filter (other than the default
// series-less exclusion) is set.
func (c FilterCriteria) hasAnyFilter() bool {
	return len(c.SeriesIDs) > 0 || c.Protocol != nil || len(c.Languages) > 0 ||
		len(c.Quality) > 0 || len(c.Status) > 0
}

// FilterQueue applies [FilterCriteria] to a queue slice, mirroring the
// C# include logic at `QueueController.cs:179-217`. Order of the
// boolean ANDs matches C# (seriesId → protocol → languages → quality →
// status); since each only narrows, order doesn't affect the result.
// rows is not mutated; a new slice is returned.
func FilterQueue(rows []Queue, c FilterCriteria) []Queue {
	// Fast path: nothing to filter (include-unknown + no criteria).
	if c.IncludeUnknownSeriesItems && !c.hasAnyFilter() {
		out := make([]Queue, len(rows))
		copy(out, rows)
		return out
	}
	out := make([]Queue, 0, len(rows))
	for _, q := range rows {
		// C# `:179` — exclude series-less rows unless explicitly included.
		if !c.IncludeUnknownSeriesItems && q.Series == nil {
			continue
		}
		if len(c.SeriesIDs) > 0 {
			if q.Series == nil || !c.SeriesIDs[q.Series.ID] {
				continue
			}
		}
		if c.Protocol != nil && q.Protocol != *c.Protocol {
			continue
		}
		if len(c.Languages) > 0 && !anyLanguageInSet(q, c.Languages) {
			continue
		}
		if len(c.Quality) > 0 && !c.Quality[q.Quality.Quality.ID] {
			continue
		}
		if len(c.Status) > 0 && !c.Status[q.Status] {
			continue
		}
		out = append(out, q)
	}
	return out
}

// anyLanguageInSet reports whether any of the row's languages is in the
// filter set (C# `:203` `q.Languages.Any(l => languages.Contains(l.Id))`).
func anyLanguageInSet(q Queue, set map[int]bool) bool {
	for _, l := range q.Languages {
		if set[l.ID] {
			return true
		}
	}
	return false
}
