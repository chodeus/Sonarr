package queue

import (
	"sort"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/quality"
)

// SortQueue orders a queue page by the supplied sort key + direction,
// mirroring the C# `QueueController.GetQueue` ordering at
// `src/Sonarr.Api.V3/Queue/QueueController.cs:219-285` (its
// MapToPagingSpec surface). The full queue is sorted BEFORE pagination
// (the caller skips/takes the page afterward), matching C#.
//
// Every primary ordering is followed by the same secondary tiebreak the
// C# applies unconditionally — `ThenByDescending(q => progress)` where
// progress = `Size == 0 ? 0 : 100 - (SizeLeft / Size * 100)` — so rows
// that tie on the primary key fall back to most-complete-first. The
// secondary is ALWAYS descending regardless of the primary direction
// (C# `:276`).
//
// qualityWeight maps a [quality.Quality] to its profile/definition
// weight for the "quality" sort key (higher weight = higher quality),
// mirroring the C# `QualityModelComparer` built from the default
// profile. nil is tolerated — the quality key then falls back to the
// raw Quality.ID ordering. Revision (proper/repack) breaks weight ties.
//
// rows is not mutated; a sorted copy is returned.
func SortQueue(rows []Queue, sortKey string, ascending bool, qualityWeight func(quality.Quality) int) []Queue {
	out := make([]Queue, len(rows))
	copy(out, rows)
	primary := queuePrimaryCompare(sortKey, qualityWeight)
	sort.SliceStable(out, func(i, j int) bool {
		c := primary(out[i], out[j])
		if !ascending {
			c = -c
		}
		if c != 0 {
			return c < 0
		}
		// Secondary tiebreak — most-complete-first (always descending).
		return queueProgress(out[i]) > queueProgress(out[j])
	})
	return out
}

// queuePrimaryCompare returns the primary comparison function for a sort
// key. Each returns negative / zero / positive in ascending terms; the
// caller negates for descending. Mirrors the C# SortKey dispatch
// (`QueueController.cs:221-274` explicit branches + `:290-321`
// GetOrderByFunc default cases).
func queuePrimaryCompare(sortKey string, qualityWeight func(quality.Quality) int) func(a, b Queue) int {
	switch sortKey {
	case "timeleft", "":
		return func(a, b Queue) int { return compareTimeLeft(a.TimeLeft, b.TimeLeft) }
	case "estimatedCompletionTime":
		return func(a, b Queue) int { return compareNullableTime(a.EstimatedCompletionTime, b.EstimatedCompletionTime) }
	case "added":
		return func(a, b Queue) int { return compareNullableTime(a.Added, b.Added) }
	case "protocol":
		return func(a, b Queue) int { return strings.Compare(a.Protocol.String(), b.Protocol.String()) }
	case "indexer":
		return func(a, b Queue) int { return compareFoldedString(a.Indexer, b.Indexer) }
	case "downloadClient":
		return func(a, b Queue) int { return compareFoldedString(a.DownloadClient, b.DownloadClient) }
	case "quality":
		return func(a, b Queue) int { return compareQuality(a.Quality, b.Quality, qualityWeight) }
	case "language", "languages":
		return func(a, b Queue) int { return compareLanguages(a.Languages, b.Languages) }
	case "status":
		return func(a, b Queue) int { return strings.Compare(a.Status.String(), b.Status.String()) }
	case "series.sortTitle":
		return func(a, b Queue) int { return strings.Compare(seriesSortTitle(a), seriesSortTitle(b)) }
	case "title":
		return func(a, b Queue) int { return strings.Compare(a.Title, b.Title) }
	case "episode.airDateUtc", "episodes.airDateUtc":
		return func(a, b Queue) int { return compareNullableTime(firstEpisodeAirDate(a), firstEpisodeAirDate(b)) }
	case "episode.title", "episodes.title":
		return func(a, b Queue) int { return strings.Compare(firstEpisodeTitle(a), firstEpisodeTitle(b)) }
	case "size":
		return func(a, b Queue) int { return compareInt64(a.Size, b.Size) }
	case "progress":
		return func(a, b Queue) int { return compareFloat(queueProgress(a), queueProgress(b)) }
	default:
		// C# GetOrderByFunc default + the bare "episode" object case both
		// have no meaningful comparable; fall back to timeleft (the C#
		// `default: return q => q.TimeLeft`).
		return func(a, b Queue) int { return compareTimeLeft(a.TimeLeft, b.TimeLeft) }
	}
}

// queueProgress mirrors C# `QueueController.cs:276` —
// `Size == 0 ? 0 : 100 - (SizeLeft / Size * 100)`. Computed in float so
// the percentage doesn't collapse to integer-division zero.
func queueProgress(q Queue) float64 {
	if q.Size == 0 {
		return 0
	}
	return 100 - (float64(q.SizeLeft)/float64(q.Size))*100
}

// compareTimeLeft ports `TimeleftComparer` at
// `src/NzbDrone.Core/Queue/TimeleftComparer.cs` — nil (no ETA) sorts
// LAST in ascending order; otherwise by duration.
func compareTimeLeft(x, y *time.Duration) int {
	switch {
	case x == nil && y == nil:
		return 0
	case x == nil:
		return 1
	case y == nil:
		return -1
	case *x > *y:
		return 1
	case *x < *y:
		return -1
	default:
		return 0
	}
}

// compareNullableTime ports `DatetimeComparer` at
// `src/NzbDrone.Core/Queue/DatetimeComparer.cs` — nil sorts LAST in
// ascending order.
func compareNullableTime(x, y *time.Time) int {
	switch {
	case x == nil && y == nil:
		return 0
	case x == nil:
		return 1
	case y == nil:
		return -1
	case x.After(*y):
		return 1
	case x.Before(*y):
		return -1
	default:
		return 0
	}
}

// compareFoldedString mirrors the C#
// `StringComparer.InvariantCultureIgnoreCase` used for the indexer +
// downloadClient sort keys.
func compareFoldedString(a, b string) int {
	return strings.Compare(strings.ToLower(a), strings.ToLower(b))
}

// compareInt64 is a small int64 ordering helper.
func compareInt64(a, b int64) int {
	switch {
	case a > b:
		return 1
	case a < b:
		return -1
	default:
		return 0
	}
}

// compareFloat is a small float ordering helper.
func compareFloat(a, b float64) int {
	switch {
	case a > b:
		return 1
	case a < b:
		return -1
	default:
		return 0
	}
}

// compareQuality orders by the injected profile/definition weight
// (higher weight = higher quality), falling back to Quality.ID when no
// weight function is supplied. Revision (proper/repack version) breaks
// weight ties — mirrors the C# `QualityModelComparer` which compares the
// quality then the revision.
func compareQuality(a, b quality.Model, weight func(quality.Quality) int) int {
	var wa, wb int
	if weight != nil {
		wa, wb = weight(a.Quality), weight(b.Quality)
	} else {
		wa, wb = a.Quality.ID, b.Quality.ID
	}
	if c := compareInt(wa, wb); c != 0 {
		return c
	}
	return a.Revision.Compare(b.Revision)
}

func compareInt(a, b int) int {
	switch {
	case a > b:
		return 1
	case a < b:
		return -1
	default:
		return 0
	}
}

// compareLanguages ports `LanguagesComparer` at
// `src/NzbDrone.Core/Languages/LanguagesComparer.cs` — empty sorts last;
// multi-language piles sort after single-language ones (more languages
// = "larger"); single-vs-single breaks by the first language's name.
func compareLanguages(x, y []language.Language) int {
	switch {
	case len(x) == 0 && len(y) == 0:
		return 0
	case len(x) == 0:
		return 1
	case len(y) == 0:
		return -1
	}
	if len(x) > 1 && len(y) > 1 {
		if len(x) > len(y) {
			return 1
		}
		if len(x) < len(y) {
			return -1
		}
		return 0
	}
	if len(x) > 1 && len(y) == 1 {
		return 1
	}
	if len(x) == 1 && len(y) > 1 {
		return -1
	}
	// both single.
	return strings.Compare(x[0].Name, y[0].Name)
}

// seriesSortTitle mirrors C# `:297` — `q.Series?.SortTitle ?? q.Title`.
func seriesSortTitle(q Queue) string {
	if q.Series != nil && q.Series.SortTitle != "" {
		return q.Series.SortTitle
	}
	return q.Title
}

// firstEpisode returns the first matched episode (the C# row carries a
// singular Episode; the Go row carries Episodes[] — the first entry is
// the singular-equivalent).
func firstEpisode(q Queue) *episodes.Episode {
	if len(q.Episodes) > 0 {
		return &q.Episodes[0]
	}
	return nil
}

// firstEpisodeAirDate mirrors C# `:304` — `q.Episode?.AirDateUtc ??
// DateTime.MinValue`. Returns nil for no-episode rows; compareNullableTime
// sorts nil last (DateTime.MinValue sorts first in C# ascending, but the
// nil-last convention matches the other Go queue comparers and the
// frontend treats both as "no date").
func firstEpisodeAirDate(q Queue) *time.Time {
	if e := firstEpisode(q); e != nil {
		return e.AirDateUtc
	}
	return nil
}

// firstEpisodeTitle mirrors C# `:307` — `q.Episode?.Title ??
// string.Empty`.
func firstEpisodeTitle(q Queue) string {
	if e := firstEpisode(q); e != nil {
		return e.Title
	}
	return ""
}
