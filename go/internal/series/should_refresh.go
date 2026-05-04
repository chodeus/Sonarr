package series

import (
	"time"

	"sonarr.tv/sonarr/internal/episodes"
)

// ShouldRefresh ports `Tv/ShouldRefreshSeries.cs:24-77` as a pure
// function. The C# class injects IEpisodeService to load episodes
// inside the method; the Go port lifts that dep out so the caller
// supplies the episode set, keeping this function dependency-free
// and trivially testable.
//
// The decision tree (mirrors C# verbatim):
//
//  1. LastInfoSync older than 30 days → refresh.
//  2. ANY aired in-season (SeasonNumber > 0) episode whose Title is
//     "TBA" (case-sensitive Ordinal compare) → refresh.
//  3. LastInfoSync within the last 6 hours → DO NOT refresh.
//  4. Status != SeriesStatusEnded → refresh.
//  5. Most recent episode by AirDateUtc aired within the last 30
//     days → refresh.
//  6. Otherwise → DO NOT refresh.
//
// `now` is injected so tests run deterministically; production
// callers pass `time.Now().UTC()`.
//
// The C# version wraps the whole tree in try/catch and returns true
// on exception ("when in doubt, refresh"). The Go port does no
// equivalent — there are no panic-paths in the pure-function form.
//
// Note: A nil-or-zero `LastInfoSync` evaluates as "before any
// timestamp" in the C# `series.LastInfoSync < DateTime.UtcNow.AddDays(-30)`
// comparison (because C# DateTime is a value type and .MinValue is
// way in the past). The Go port preserves this by treating
// `*time.Time == nil` or zero-time as "very old" — both rules 1 and
// 3 fall through to refresh if LastInfoSync is unknown.
func ShouldRefresh(series *Series, eps []episodes.Episode, now time.Time) bool {
	if series == nil {
		return true // pathological caller; "when in doubt, refresh" matches C# catch-all.
	}

	last := lastInfoSync(series)

	// Rule 1: 30+ day stale.
	if last.Before(now.AddDate(0, 0, -30)) {
		return true
	}

	// Rule 2: any aired in-season episode with Title=="TBA" (case-sensitive).
	for _, e := range eps {
		if e.SeasonNumber <= 0 {
			continue
		}
		if e.AirDateUtc == nil || !e.AirDateUtc.Before(now) {
			continue
		}
		if e.Title == "TBA" {
			return true
		}
	}

	// Rule 3: refreshed within the last 6 hours → skip.
	if !last.Before(now.Add(-6 * time.Hour)) {
		return false
	}

	// Rule 4: not ended → refresh.
	if series.Status != SeriesStatusEnded {
		return true
	}

	// Rule 5: ended but most-recent episode aired within last 30 days → refresh.
	latest := mostRecentAir(eps)
	if latest != nil && latest.After(now.AddDate(0, 0, -30)) {
		return true
	}

	// Rule 6: ended long ago → skip.
	return false
}

// lastInfoSync returns series.LastInfoSync as a non-pointer time.Time,
// substituting time.Time{} (zero value) for nil. The zero value
// compares as "before any real timestamp", matching the C# behaviour
// where `series.LastInfoSync < DateTime.UtcNow.AddDays(-30)` is true
// for unset rows because C# DateTime defaults to MinValue.
func lastInfoSync(s *Series) time.Time {
	if s.LastInfoSync == nil {
		return time.Time{}
	}
	return *s.LastInfoSync
}

// mostRecentAir returns a pointer to the latest non-nil AirDateUtc
// across the episode slice, or nil if the slice has no aired entries.
// Mirrors C# `episodes.MaxBy(e => e.AirDateUtc)` which itself returns
// null on an empty source.
func mostRecentAir(eps []episodes.Episode) *time.Time {
	var best *time.Time
	for _, e := range eps {
		if e.AirDateUtc == nil {
			continue
		}
		if best == nil || e.AirDateUtc.After(*best) {
			t := *e.AirDateUtc
			best = &t
		}
	}
	return best
}
