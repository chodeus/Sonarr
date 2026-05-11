package metadatasource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

// skyhook_search.go ports the search arm of `SkyHookProxy` at C#
// `src/NzbDrone.Core/MetadataSource/SkyHook/SkyHookProxy.cs:73-180`.
//
// Slice 4.AU (sub-arc M3b of queue [M3]) — CLOSES queue [M3].
//
// Five public methods:
//
//   - [Proxy.SearchForNewSeries] — the free-text + prefix-aware search.
//     Handles the `tvdb:`/`tvdbid:` short-circuit, the path-injection
//     guard, the search HTTP call, and the per-result map-or-existing
//     decision via [mapSearchResult].
//   - [Proxy.SearchForNewSeriesByImdbID] — normalises via
//     parser.NormalizeImdbID, returns empty on normalisation failure
//     (matches C# `:75-80`), else delegates to SearchForNewSeries
//     with the `imdb:` prefix.
//   - [Proxy.SearchForNewSeriesByAniListID] / ByTmdbID / ByMyAnimeListID
//     — thin wrappers composing `<prefix>:<id>` and delegating.
//
// All five share the same error semantics as [Proxy.GetSeriesInfo]:
// [ErrInvalidSearchTerm] for path-injection, [ErrServiceUnavailable]
// for transport / decode failures, ctx.Err() for cancellation.

// SearchForNewSeries ports `SearchForNewSeries(string title)` at
// C# `SkyHookProxy.cs:108-168`. Five execution arms:
//
//  1. Path-injection guard — `if (title.IsPathValid(AnyOs))` →
//     return *Error wrapping [ErrInvalidSearchTerm].
//  2. `tvdb:` / `tvdbid:` prefix → see [resolveByTvdbPrefix].
//  3. Free-text search → GET <baseURL>/search/en/?term=... ; decode
//     `[]ShowResource`; map each via [mapSearchResult].
//  4. Decode / non-2xx / transport-error → return *Error wrapping
//     [ErrServiceUnavailable].
//  5. ctx.Err() propagates verbatim.
//
// The C# catch-Exception-then-rethrow-as-SkyHookException pattern at
// `:163-167` is mirrored by the *Error wrap with a generic message
// when JSON decode fails (the C# catch-Exception arm primarily
// handles unexpected decode failures). All three C# catches collapse
// into one Go branch via [ErrServiceUnavailable].
func (p *Proxy) SearchForNewSeries(ctx context.Context, title string) ([]*series.Series, error) {
	if p == nil {
		return nil, errors.New("metadatasource: Proxy is nil")
	}

	if looksLikePath(title) {
		return nil, NewInvalidSearchTermError(title)
	}

	lowerTitle := strings.ToLower(title)
	if strings.HasPrefix(lowerTitle, "tvdb:") || strings.HasPrefix(lowerTitle, "tvdbid:") {
		return p.resolveByTvdbPrefix(ctx, lowerTitle)
	}

	reqURL := fmt.Sprintf("%s/search/en/?term=%s", p.baseURL, urlEncodeTerm(title))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, NewServiceUnavailableError(
			fmt.Sprintf("metadatasource: build search request for %q failed", title),
			err,
		)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, NewServiceUnavailableError(
			fmt.Sprintf("metadatasource: search HTTP request for %q failed", title),
			err,
		)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, NewServiceUnavailableError(
			fmt.Sprintf("metadatasource: search HTTP %d for %q", resp.StatusCode, title),
			nil,
		)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, NewServiceUnavailableError(
			fmt.Sprintf("metadatasource: read search body for %q failed", title),
			err,
		)
	}

	var shows []ShowResource
	if err := json.Unmarshal(body, &shows); err != nil {
		return nil, NewServiceUnavailableError(
			fmt.Sprintf("metadatasource: decode search body for %q failed", title),
			err,
		)
	}

	out := make([]*series.Series, 0, len(shows))
	for i := range shows {
		mapped, err := p.mapSearchResult(ctx, &shows[i])
		if err != nil {
			return nil, NewServiceUnavailableError(
				fmt.Sprintf("metadatasource: map search result for %q failed", title),
				err,
			)
		}
		out = append(out, mapped)
	}
	return out, nil
}

// resolveByTvdbPrefix ports the `tvdb:` / `tvdbid:` short-circuit at
// C# `SkyHookProxy.cs:119-142`. Five validation gates → one of three
// outcomes:
//
//  1. Slug after colon must be non-empty, contain no whitespace,
//     parse as int, and be > 0. Any miss → return empty []*Series.
//  2. SeriesLookup (when wired) tries to find the local row first.
//     Hit → return [localSeries]. Miss (ErrNotFound) → continue.
//  3. GetSeriesInfo fetches fresh. Success → return [mappedSeries].
//     ErrSeriesNotFound → return empty []*Series. Other error →
//     propagate.
//
// The lowerTitle arrives pre-lowercased from [SearchForNewSeries].
func (p *Proxy) resolveByTvdbPrefix(ctx context.Context, lowerTitle string) ([]*series.Series, error) {
	colonIdx := strings.IndexByte(lowerTitle, ':')
	if colonIdx < 0 {
		// Should not happen since the caller checked HasPrefix, but
		// guard defensively in case the prefix list grows.
		return []*series.Series{}, nil
	}
	slug := strings.TrimSpace(lowerTitle[colonIdx+1:])
	if slug == "" || containsWhitespace(slug) {
		return []*series.Series{}, nil
	}
	tvdbID, err := strconv.Atoi(slug)
	if err != nil || tvdbID <= 0 {
		return []*series.Series{}, nil
	}

	if p.seriesLookup != nil {
		existing, err := p.seriesLookup.GetByTvdbID(ctx, tvdbID)
		if err == nil {
			return []*series.Series{existing}, nil
		}
		if !errors.Is(err, series.ErrNotFound) {
			// A real persistence-layer error — wrap as
			// service-unavailable so the caller doesn't confuse
			// "DB error" with "TVDB id not found".
			return nil, NewServiceUnavailableError(
				fmt.Sprintf("metadatasource: local series lookup for tvdbId %d failed", tvdbID),
				err,
			)
		}
		// fall through to GetSeriesInfo
	}

	mapped, _, err := p.GetSeriesInfo(ctx, tvdbID)
	if err != nil {
		if errors.Is(err, ErrSeriesNotFound) {
			return []*series.Series{}, nil
		}
		return nil, err
	}
	return []*series.Series{mapped}, nil
}

// mapSearchResult ports `MapSearchResult(ShowResource show)` at
// C# `SkyHookProxy.cs:170-180`. Three-way:
//
//   - SeriesLookup wired and hits → return the local Series row.
//   - SeriesLookup wired and misses (or not wired at all) → map the
//     ShowResource fresh via [mapSeries].
//
// PROV-Q34 documents the open question of surfacing "library hit vs
// fresh hit" to the caller — the Go port preserves the C# semantic of
// returning library rows transparently so callers cannot tell the
// difference. Future API-surface slice may add an IsExisting flag.
func (p *Proxy) mapSearchResult(ctx context.Context, show *ShowResource) (*series.Series, error) {
	if p.seriesLookup != nil {
		existing, err := p.seriesLookup.GetByTvdbID(ctx, show.TvdbID)
		if err == nil {
			return existing, nil
		}
		if !errors.Is(err, series.ErrNotFound) {
			return nil, fmt.Errorf("local series lookup for tvdbId %d: %w", show.TvdbID, err)
		}
	}
	return mapSeries(show, p.dailySeries)
}

// SearchForNewSeriesByImdbID ports
// `SearchForNewSeriesByImdbId(string imdbId)` at C# `SkyHookProxy.cs:73-85`.
//
// Normalises via parser.NormalizeImdbID (the Go-port of
// `Parser.NormalizeImdbId` at `Parser.cs:863-879`). When normalisation
// returns "" (invalid input — C# returns null), the wrapper returns
// an empty list with NO error — matching the C# `:77-80` early-return
// path. Otherwise composes `imdb:<normalised>` and delegates to
// [Proxy.SearchForNewSeries] which then routes through the free-text
// search call (since `imdb:` is NOT a recognised prefix for the
// tvdb-shortcut — see `:119` for the exhaustive prefix list).
func (p *Proxy) SearchForNewSeriesByImdbID(ctx context.Context, imdbID string) ([]*series.Series, error) {
	if p == nil {
		return nil, errors.New("metadatasource: Proxy is nil")
	}
	normalised := parser.NormalizeImdbID(imdbID)
	if normalised == "" {
		return []*series.Series{}, nil
	}
	return p.SearchForNewSeries(ctx, "imdb:"+normalised)
}

// SearchForNewSeriesByAniListID ports `:87-92`. Thin wrapper.
func (p *Proxy) SearchForNewSeriesByAniListID(ctx context.Context, aniListID int) ([]*series.Series, error) {
	if p == nil {
		return nil, errors.New("metadatasource: Proxy is nil")
	}
	return p.SearchForNewSeries(ctx, fmt.Sprintf("anilist:%d", aniListID))
}

// SearchForNewSeriesByMyAnimeListID ports `:94-99`. Thin wrapper.
// Note the prefix is `mal:`, not `myanimelist:` (matches C# `:96`).
func (p *Proxy) SearchForNewSeriesByMyAnimeListID(ctx context.Context, malID int) ([]*series.Series, error) {
	if p == nil {
		return nil, errors.New("metadatasource: Proxy is nil")
	}
	return p.SearchForNewSeries(ctx, fmt.Sprintf("mal:%d", malID))
}

// SearchForNewSeriesByTmdbID ports `:101-106`. Thin wrapper.
func (p *Proxy) SearchForNewSeriesByTmdbID(ctx context.Context, tmdbID int) ([]*series.Series, error) {
	if p == nil {
		return nil, errors.New("metadatasource: Proxy is nil")
	}
	return p.SearchForNewSeries(ctx, fmt.Sprintf("tmdb:%d", tmdbID))
}

// containsWhitespace mirrors the C# `slug.Any(char.IsWhiteSpace)`
// check at `:123`. Go's unicode.IsSpace covers the same Unicode
// whitespace categories.
func containsWhitespace(s string) bool {
	for _, r := range s {
		if unicode.IsSpace(r) {
			return true
		}
	}
	return false
}
