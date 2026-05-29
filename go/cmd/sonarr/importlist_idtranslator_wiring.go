package main

import (
	"context"
	"log/slog"

	"sonarr.tv/sonarr/internal/importlists"
	"sonarr.tv/sonarr/internal/series"
)

// G7 sub-gap (J444): import-list items whose TvdbId is zero but which
// carry an IMDb/TMDb/AniList/MyAnimeList id were dropped because the
// SyncService's IDTranslator was nil. The metadata-source proxy already
// exposes the four typed-id lookups (SearchForNewSeriesBy{Imdb,Tmdb,
// AniList,MyAnimeList}ID), so this adapter wires them into the translator
// the SyncService calls. Ports the C# `ImportListSyncService` translation
// chain (`ImportListSyncService.cs:151-208`): per item, look the external
// id up against the metadata source, take the first match, and adopt its
// TvdbId + Title.

// seriesIDSearcher is the narrow slice of *metadatasource.Proxy the
// translator needs. The Proxy satisfies it structurally.
type seriesIDSearcher interface {
	SearchForNewSeriesByImdbID(ctx context.Context, imdbID string) ([]*series.Series, error)
	SearchForNewSeriesByTmdbID(ctx context.Context, tmdbID int) ([]*series.Series, error)
	SearchForNewSeriesByAniListID(ctx context.Context, aniListID int) ([]*series.Series, error)
	SearchForNewSeriesByMyAnimeListID(ctx context.Context, malID int) ([]*series.Series, error)
}

// importListIDTranslator implements importlists.IDTranslator over the
// metadata-source proxy.
type importListIDTranslator struct {
	search seriesIDSearcher
	log    *slog.Logger
}

func (t importListIDTranslator) ByImdb(ctx context.Context, imdbID string) (int, string, bool) {
	return t.first(t.search.SearchForNewSeriesByImdbID(ctx, imdbID))
}

func (t importListIDTranslator) ByTmdb(ctx context.Context, tmdbID int) (int, string, bool) {
	return t.first(t.search.SearchForNewSeriesByTmdbID(ctx, tmdbID))
}

func (t importListIDTranslator) ByAniList(ctx context.Context, aniListID int) (int, string, bool) {
	return t.first(t.search.SearchForNewSeriesByAniListID(ctx, aniListID))
}

func (t importListIDTranslator) ByMyAnimeList(ctx context.Context, malID int) (int, string, bool) {
	return t.first(t.search.SearchForNewSeriesByMyAnimeListID(ctx, malID))
}

// first adopts the first match's TvdbId + Title. Returns ok=false (so
// the SyncService falls through to the next id kind) when the lookup
// errored, returned nothing, or the match carries no usable TvdbId —
// mirroring the C# `item.TvdbId <= 0` gate that drives the next step.
func (t importListIDTranslator) first(results []*series.Series, err error) (int, string, bool) {
	if err != nil {
		if t.log != nil {
			t.log.Debug("importlist idtranslator: lookup failed", "err", err)
		}
		return 0, "", false
	}
	if len(results) == 0 || results[0] == nil || results[0].TvdbID <= 0 {
		return 0, "", false
	}
	return results[0].TvdbID, results[0].Title, true
}

// compile-time guard.
var _ importlists.IDTranslator = importListIDTranslator{}
