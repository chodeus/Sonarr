package importlists

import "time"

// ItemInfo is the per-poll output the import-list Fetch hooks
// return — a single series identifier with the minimum metadata
// needed for downstream ID translation + series-add. Mirrors C#
// `Parser/Model/ImportListItemInfo.cs:8-31`.
//
// Slice 4.AK (sub-arc I2a of queue [I2]) — foundation slice for the
// importlists package base. The Provider interface (queue [I2b])
// and the per-list Fetch implementations (queue [I3] — SonarrList,
// PlexList) consume this type. The sync orchestrator (queue [I4] —
// ImportListSyncService) applies the importlistexclusions filter
// against the (Title, TvdbId, ImdbId) tuple before dispatching each
// item into the series-add pipeline.
//
// Field rationale (C# call-site grep):
//
//   - ImportListId / ImportList are populated by [CleanupListItems]
//     post-fetch — they record which configured list produced the
//     row. The per-list Fetch impl leaves them zero/empty; the
//     base-class fan-out in [ImportListBase.CleanupListItems] at
//     `ImportListBase.cs:85-96` stamps them in.
//   - Title + Year + ReleaseDate are the "free-form" series
//     identifiers the parser falls back to when no external ID is
//     supplied (common for the CustomImport / Sonarr-peer paths).
//   - TvdbId / TmdbId / ImdbId / MalId / AniListId are the per-
//     source external IDs. Each concrete provider populates the
//     subset it knows; the parser translates whichever ID is set
//     into a series during the sync (see ImportListSyncService).
//   - Seasons carries the per-season monitor-state hints some
//     providers return (Sonarr-peer notably). Typed as []Season —
//     a thin local record rather than dragging in
//     internal/series.Season — to keep the package import graph
//     narrow. The orchestrator (queue [I4]) translates between the
//     two shapes.
//
// C# DistinctBy at `ImportListBase.cs:87` deduplicates on
// (Title, TvdbId, ImdbId) — NOT on ImportListId/ImportList. The
// Go port matches this in [DedupeKey].
type ItemInfo struct {
	// ID mirrors the C# `ModelBase.Id` field — runtime-only here
	// (no persistence layer for the item info itself; rows live in
	// ImportListItems via internal/importlistitems which has its
	// own type).
	ID int `json:"id"`

	// ImportListID is the producing import-list's Id column.
	// Stamped in by the fan-out wrapper.
	ImportListID int `json:"importListId"`

	// ImportList is the producing import-list's Name. Stamped in by
	// the fan-out wrapper. UI surfaces this when reporting "added X
	// via list Y".
	ImportList string `json:"importList"`

	// Title is the series title as the source list returned it.
	Title string `json:"title"`

	// Year is the series first-aired year (0 = unknown).
	Year int `json:"year"`

	// TvdbId is the TheTVDB series id (0 = unknown).
	TvdbID int `json:"tvdbId"`

	// TmdbId is the TheMovieDB series id (0 = unknown). Plex
	// returns this; most other providers do not.
	TmdbID int `json:"tmdbId"`

	// ImdbId is the IMDB series id ("" = unknown). Typically
	// "tt"-prefixed.
	ImdbID string `json:"imdbId"`

	// MalId is the MyAnimeList series id (0 = unknown).
	MalID int `json:"malId"`

	// AniListId is the AniList series id (0 = unknown).
	AniListID int `json:"aniListId"`

	// ReleaseDate is the series release date. Zero time = unknown.
	ReleaseDate time.Time `json:"releaseDate"`

	// Seasons carries the per-season monitor-state hints some
	// providers return.
	Seasons []Season `json:"seasons"`
}

// Season is the per-season monitor-state hint an import-list item
// may carry. Mirrors the subset of C# `Tv.Season` the
// ImportListItemInfo's Seasons collection populates — every
// concrete C# import list reads only SeasonNumber + Monitored from
// the embedded Tv.Season type.
//
// Kept as a local record rather than reusing internal/series.Season
// to keep this package's import graph narrow. Translation to the
// series.Season the add-pipeline consumes lives in the orchestrator
// (queue [I4] — ImportListSyncService).
type Season struct {
	// SeasonNumber is the 0-indexed season ordinal (0 = specials).
	SeasonNumber int `json:"seasonNumber"`

	// Monitored is the source list's monitor hint. The orchestrator
	// applies this against the user's per-list MonitorNewItems
	// preference.
	Monitored bool `json:"monitored"`
}

// DedupeKey returns the (Title, TvdbId, ImdbId) tuple
// [ImportListBase.CleanupListItems] deduplicates on. Stored as a
// struct rather than a string concatenation to avoid the ambiguity
// of "x|y|" — a title containing "|" would collide.
//
// Mirrors C# `releases.DistinctBy(r => new { r.Title, r.TvdbId, r.ImdbId })`
// at `ImportListBase.cs:87`.
type DedupeKey struct {
	Title  string
	TvdbID int
	ImdbID string
}

// Dedup returns the input slice with duplicate entries removed by
// (Title, TvdbId, ImdbId). The first occurrence of each key wins.
// Mirrors C# `DistinctBy` ordering semantics — preserves input
// order, keeps the first occurrence.
//
// Used by the per-list base wrapper post-fetch and by tests; the
// concrete providers themselves typically do not call this.
func Dedup(in []ItemInfo) []ItemInfo {
	seen := make(map[DedupeKey]struct{}, len(in))
	out := make([]ItemInfo, 0, len(in))
	for _, item := range in {
		k := DedupeKey{Title: item.Title, TvdbID: item.TvdbID, ImdbID: item.ImdbID}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, item)
	}
	return out
}

// FetchResult is the return type of a Provider Fetch call. Mirrors
// C# `ImportListFetchResult` at `ImportListBase.cs:14-29`. Carries
// both the per-poll items and a partial-failure flag the
// orchestrator (queue [I4]) consults to decide whether to apply
// the configured ListSyncLevel.
//
// AnyFailure is the C# `bool AnyFailure` — true when ANY paged
// request inside the fetch failed (the others may still have
// returned data). The orchestrator at C#
// `ImportListSyncService.SyncList` (~`ImportListSyncService.cs:88`)
// guards the "clean stale items" pass on AnyFailure=false to avoid
// purging series the user still wants when the upstream paged-API
// dropped a single page.
type FetchResult struct {
	// Series is the per-poll output. The C# field is also called
	// Series; the Go port keeps the name for direct parity even
	// though the contained values are not full Series rows.
	Series []ItemInfo

	// AnyFailure reports a partial-failure inside the fetch — at
	// least one paged sub-request returned an error. Mirrors C#
	// `AnyFailure { get; set; }`.
	AnyFailure bool
}
