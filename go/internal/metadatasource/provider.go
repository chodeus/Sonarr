// Package metadatasource ports `NzbDrone.Core.MetadataSource` —
// `src/NzbDrone.Core/MetadataSource/`. This is the **upstream**
// metadata boundary: it answers "what is TVDB id N?" and "what
// TVDB ids match query Q?" by hitting the Servarr-hosted SkyHook
// proxy at `https://skyhook.sonarr.tv/v1/tvdb/...`.
//
// Family-level shape (see `go/analysis/providers/metadata-sources.md`
// and C# IL-MD card):
//
//   - This is NOT a ThingiProvider family. There is no Definition
//     row, no Factory, no Settings UI, no schema endpoint. Sonarr
//     ships exactly one concrete metadata source: SkyHookProxy,
//     registered as a singleton in C# `Bootstrap`. The Go-port
//     follows the same shape — a singleton service constructed
//     once in main.go and injected into the call-sites that need
//     it.
//
//   - Two interfaces partition the surface area:
//     [SeriesInfoProvider] resolves a single TVDB id into a
//     fully-populated Series + episode list (used by the refresh-
//     series chain); [SeriesSearcher] runs free-text + typed-id
//     lookups (used by the add-series UI + import-list ID
//     translation).
//
//   - The C# Tuple<Series, List<Episode>> return type collapses
//     into a multi-value Go return: `(*series.Series,
//     []episodes.Episode, error)`. The error channel carries
//     SkyHook outage signals as [ErrServiceUnavailable]-wrapping
//     [Error] values, the explicit "not found" sentinel
//     [ErrSeriesNotFound], and the path-injection guard
//     [ErrInvalidSearchTerm] (so the Phase-5 API surface can map
//     each to the correct HTTP status per C# parity — 503 / 404 /
//     400).
//
//   - Idiomatic-Go divergence — every method takes a
//     context.Context as the first argument. C# `IProvideSeriesInfo`
//     and `ISearchForNewSeries` are sync and ctx-less; the Go port
//     adds ctx so the HTTP layer can plumb cancellation and the
//     scheduler can honour shutdown timeouts. Documented per-
//     interface below.
//
// Slice scope:
//
//   - 4.AR (queue [M1]) — THIS slice — foundation only: the two
//     Go interfaces + the wire-format Resource DTOs + the three
//     typed error sentinels. No concrete provider yet, no mapping
//     functions, no HTTP wiring. The compile-time interface guard
//     in the SkyHook sub-arc (queue [M3]) will pin the contract.
//
//   - 4.AS (queue [M2]) — SearchSeriesComparer port: the
//     IComparer<Series> from `SearchSeriesComparer.cs` translated
//     to a sort.Interface (or stable slices.SortFunc) over the
//     []*series.Series search-result list.
//
//   - 4.AT-4.AU (queue [M3], sub-arcs M3a / M3b) — SkyHookProxy
//     concrete impl. M3a covers `GetSeriesInfo` + the MapSeries /
//     MapEpisode / MapSeason / MapImage / MapActor / MapRatings /
//     MapSeriesStatus / MapCoverType pipeline. M3b covers
//     `SearchForNewSeries` (with `tvdb:`/`tvdbid:` prefix short-
//     circuit) + the four typed-id wrappers + the comparator
//     wiring.
//
//   - 4.AV (queue [M4]) — SkyHookCheck healthcheck (PROV-Q36):
//     periodic one-shot probe against a known-stable TVDB id
//     (e.g. Breaking Bad / 81189) so operators detect SkyHook
//     outages without waiting for a user-add to fail.
//
//   - 4.AW (queue [M5]) — SystemTimeCheck: re-port the C#
//     SystemTimeCheck that compares local clock against
//     SkyHook's `TimeOfDayResource` round-trip; gates on a
//     SkyHookProxy able to surface the upstream time field.
//
//   - 4.AX (queue [M6]) — UpdateSceneMapping: the C# command
//     handler that pulls scene mappings from the same SkyHook
//     host; gates on the proxy/request-builder being wired.
//
// All Phase-4-deferred surfaces (URL-base configurability per
// PROV-Q30, client-side LRU cache per PROV-Q31, language plumb-
// through per PROV-Q32, "is library hit vs fresh result" flag per
// PROV-Q34, OriginalLanguage zero-vs-English handling per
// PROV-Q35) are documented at the call-site in the SkyHookProxy
// slices, not here at the foundation layer.
package metadatasource

import (
	"context"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/series"
)

// SeriesInfoProvider ports the C# IProvideSeriesInfo interface at
// `src/NzbDrone.Core/MetadataSource/IProvideSeriesInfo.cs:7-10`.
//
// One method: resolve a TVDB series id into a Series record plus
// its episode list. The C# signature is
//
//	Tuple<Series, List<Episode>> GetSeriesInfo(int tvdbSeriesId);
//
// which the Go port flattens into a multi-value return. The first
// returned value is the mapped Series (heap-allocated to mirror
// the C# reference type); the second is the per-episode slice in
// upstream order.
//
// Errors:
//
//   - [ErrSeriesNotFound] wraps an upstream 404 — the caller
//     (SeriesController.Lookup / RefreshSeriesService) should
//     surface this as the C# `SeriesNotFoundException` does (HTTP
//     404 on the Phase-5 API).
//   - [ErrServiceUnavailable] wraps every other transport-layer
//     failure (DNS / connection refused / 5xx / decode error).
//     The Phase-5 API maps this to HTTP 503 per C#
//     `SkyHookException` parity.
//   - Plain Go error values (non-sentinel) propagate from
//     ctx.Err() and similar wrap-and-bail paths.
//
// ctx divergence note: the C# call is sync and ctx-less; the Go
// port adds ctx for cancellation. nil ctx is a programmer error
// — implementations should panic on nil rather than substitute
// context.Background(), matching the idiomatic "ctx isn't
// optional" stance the rest of the Go port follows.
type SeriesInfoProvider interface {
	GetSeriesInfo(ctx context.Context, tvdbSeriesID int) (*series.Series, []episodes.Episode, error)
}

// SeriesSearcher ports the C# ISearchForNewSeries interface at
// `src/NzbDrone.Core/MetadataSource/ISearchForNewSeries.cs:6-13`.
//
// Five methods covering:
//
//   - free-text [SearchForNewSeries] — the user typed a query
//     string into the add-series UI. May include `tvdb:N`,
//     `tvdbid:N`, `imdb:X`, `tmdb:N`, `mal:N`, `anilist:N`
//     prefixes (case-insensitive in the SkyHookProxy impl) which
//     short-circuit to the typed-id paths.
//
//   - four typed-id helpers ([SearchForNewSeriesByImdbID],
//     [SearchForNewSeriesByTmdbID], [SearchForNewSeriesByAniListID],
//     [SearchForNewSeriesByMyAnimeListID]) which the import-list
//     ID-translation chain uses to walk an external id into a
//     TVDB id. C# `ImportListSyncService` calls these from the
//     `Provider/IDTranslator` step (`ImportListSyncService.cs:212-262`).
//
// Each method returns a slice of mapped Series in upstream order.
// The optional [SearchSeriesComparer] (slice 4.AS / queue [M2])
// re-sorts the slice by match-quality before the UI consumes it;
// the typed-id paths return at most one row so they don't need
// the comparator.
//
// IMDb-id normalisation note: C# `SkyHookProxy.SearchForNewSeriesByImdbId`
// calls `Parser.Parser.NormalizeImdbId(imdbId)` before composing
// the prefix and returns an empty list when normalisation rejects
// the input. The Go port mirrors this in the SkyHook impl (slice
// 4.AU / queue [M3b]) — empty return rather than error sentinel,
// because callers iterating multiple sources want to skip-and-
// continue not stop-and-bail.
//
// Errors mirror [SeriesInfoProvider]'s contract: [ErrInvalidSearchTerm]
// for the `IsPathValid(AnyOs)` pre-filter on `SearchForNewSeries`
// (C# `SkyHookProxy.cs:110-113`); [ErrServiceUnavailable] for
// transport-layer failures; ctx.Err() for cancellation.
type SeriesSearcher interface {
	SearchForNewSeries(ctx context.Context, title string) ([]*series.Series, error)
	SearchForNewSeriesByImdbID(ctx context.Context, imdbID string) ([]*series.Series, error)
	SearchForNewSeriesByAniListID(ctx context.Context, aniListID int) ([]*series.Series, error)
	SearchForNewSeriesByTmdbID(ctx context.Context, tmdbID int) ([]*series.Series, error)
	SearchForNewSeriesByMyAnimeListID(ctx context.Context, malID int) ([]*series.Series, error)
}
