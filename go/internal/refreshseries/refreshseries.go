// Package refreshseries ports the C# `RefreshSeriesService` at
// `src/NzbDrone.Core/Tv/RefreshSeriesService.cs`. Slice 5.J191 ships
// the first viable port — the per-series RefreshSeriesInfo path that
// re-loads TheTVDB metadata via the SkyHook proxy and persists the
// updated field set. Deferred to follow-up slices:
//
//   - RefreshEpisodeService episode-reconcile (C# `:129`,
//     `_refreshEpisodeService.RefreshEpisodeInfo`).
//   - DiskScanService rescan (C# `RescanSeries` at `:168-199`).
//   - SeriesService.UpdateTags auto-tagging (`UpdateTags` at `:201-209`).
//   - All-series sweep mode (C# `:249-282`) — when SeriesIds is empty,
//     C# iterates every row and refreshes those that
//     `ICheckIfSeriesShouldBeRefreshed.ShouldRefresh` returns true for.
//   - SeriesRefreshStartingEvent / SeriesRefreshCompleteEvent /
//     SeriesUpdatedEvent (`:215, :132, :284`). The current slice
//     leans on UpdateSeries's existing SeriesEditedEvent emit which
//     does double duty for the UI refresh.
//   - SeriesNotFound → Status=Deleted soft-delete arm (`:70-81`).
//     For now SeriesNotFound propagates upward; the handler logs +
//     marks the command Indeterminate.
package refreshseries

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/series"
)

// SeriesInfoProvider is the slice of metadatasource.SeriesInfoProvider
// the service reaches for. Defined here as a narrow interface so the
// service can be unit-tested with a stub provider without importing
// the metadatasource package.
type SeriesInfoProvider interface {
	GetSeriesInfo(ctx context.Context, tvdbSeriesID int) (*series.Series, []episodes.Episode, error)
}

// SeriesStore is the narrow slice of *series.Service the refresh path
// uses. Get + UpdateSeries are enough for the J191 surface; future
// slices that wire RefreshEpisodeService + tags + disk-rescan can
// grow the interface.
type SeriesStore interface {
	GetSeries(ctx context.Context, id int) (*series.Series, error)
	UpdateSeries(ctx context.Context, ns *series.Series, opts series.UpdateOptions) (*series.Series, error)
}

// Service is the RefreshSeriesService port. Per-id Refresh is the
// only entry point; callers (the RefreshSeries command handler, the
// scheduled-task tick) marshal `[]int` series IDs from their command
// shape.
type Service struct {
	info  SeriesInfoProvider
	store SeriesStore
	now   func() time.Time
	log   *slog.Logger
}

// NewService binds the dependencies into a Service. `now` defaults to
// time.Now().UTC() when nil — pinning is for deterministic tests.
func NewService(info SeriesInfoProvider, store SeriesStore, now func() time.Time, log *slog.Logger) (*Service, error) {
	if info == nil {
		return nil, errors.New("refreshseries: info provider is nil")
	}
	if store == nil {
		return nil, errors.New("refreshseries: series store is nil")
	}
	if log == nil {
		log = slog.Default()
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{info: info, store: store, now: now, log: log}, nil
}

// Refresh ports C# `RefreshSeriesService.RefreshSeriesInfo` at
// `RefreshSeriesService.cs:53-135` for a single series id. Steps:
//
//  1. Load the series from the store (`:57`).
//  2. Call SkyHook GetSeriesInfo (`:66`). Errors propagate.
//  3. Copy the SkyHook-authoritative fields onto the loaded row
//     (`:89-114`). The Go-port copies the same 24-field subset C#
//     covers — see [copySkyhookFields] for the field list.
//  4. Set LastInfoSync = now (`:104`).
//  5. Merge Seasons: preserve existing monitored state when a
//     season carries over; default new seasons' monitored based on
//     series.MonitorNewItems == 0 (NewItemMonitorTypes.All); season
//     0 always defaults to unmonitored when new (`:147-152`).
//  6. Persist via UpdateSeries with SkipPublishUpdatedEvent=false so
//     the SeriesEditedEvent fires for the UI refresh. This collapses
//     C#'s separate UpdateSeries(publishUpdatedEvent:false) +
//     PublishEvent(new SeriesUpdatedEvent(series)) pair at `:128, :132`
//     into a single emit — Phase-5 UIs subscribed to the edit event
//     still see the change.
//
// Path-normalisation (C# `:118-119` DirectoryInfo + GetActualCasing)
// is deferred: it's a host-OS-specific operation that needs the disk
// package to grow a portable case-resolver. Not behaviour-critical
// for the refresh path — the path-on-disk just stays the way it was.
//
// Returns the updated row. Episode reconcile happens in a follow-up
// slice; for now the SkyHook-emitted []episodes.Episode is discarded
// (the import-list add path's inline-seeder covered the cold-start
// case; subsequent refreshes don't reconcile yet).
// Refresh adapts [Service.RefreshSeriesInfo] to the
// commands/handlers RefreshSeriesRunner interface by discarding the
// returned series. The full-return form is preserved as
// [Service.RefreshSeriesInfo] for callers that need the updated row
// (the future all-series sweep + tests).
func (s *Service) Refresh(ctx context.Context, seriesID int) error {
	_, err := s.RefreshSeriesInfo(ctx, seriesID)
	return err
}

// RefreshSeriesInfo is the full per-series refresh; see the package
// doc-comment for the field-list semantics + deferred surface.
func (s *Service) RefreshSeriesInfo(ctx context.Context, seriesID int) (*series.Series, error) {
	if s == nil {
		return nil, errors.New("refreshseries: nil Service")
	}

	row, err := s.store.GetSeries(ctx, seriesID)
	if err != nil {
		return nil, fmt.Errorf("refreshseries: GetSeries(%d): %w", seriesID, err)
	}

	merged, _, err := s.info.GetSeriesInfo(ctx, row.TvdbID)
	if err != nil {
		// SeriesNotFound deletion-flip deferred — for now propagate so
		// the handler can mark the command Indeterminate.
		return nil, fmt.Errorf("refreshseries: GetSeriesInfo(tvdbId=%d): %w", row.TvdbID, err)
	}
	if merged == nil {
		return nil, errors.New("refreshseries: SkyHook returned nil series")
	}

	// TVDB-id replacement warning (C# `:83-87`). A different TvdbId
	// from SkyHook means the upstream marked the original a duplicate;
	// take the new id but log so the operator can investigate.
	if merged.TvdbID != row.TvdbID {
		s.log.LogAttrs(ctx, slog.LevelWarn,
			"refreshseries: TVDB id replaced — original was marked duplicate upstream",
			slog.String("title", row.Title),
			slog.Int("oldTvdbId", row.TvdbID),
			slog.String("newTitle", merged.Title),
			slog.Int("newTvdbId", merged.TvdbID))
		row.TvdbID = merged.TvdbID
	}

	copySkyhookFields(row, merged)
	utcNow := s.now().UTC()
	row.LastInfoSync = &utcNow

	mergedSeasons, err := mergeSeasons(row.Seasons, merged.Seasons, row.MonitorNewItems == 0, s.log, ctx, row)
	if err != nil {
		// Decode failures on either side fall back to the SkyHook
		// shape verbatim — preserves the refresh's correctness even
		// when the persisted blob is junk. Log so the operator can
		// notice.
		s.log.LogAttrs(ctx, slog.LevelWarn,
			"refreshseries: season merge failed (using SkyHook seasons verbatim)",
			slog.Int("seriesId", row.ID),
			slog.Any("err", err))
		mergedSeasons = merged.Seasons
	}
	row.Seasons = mergedSeasons

	updated, err := s.store.UpdateSeries(ctx, row, series.UpdateOptions{})
	if err != nil {
		return nil, fmt.Errorf("refreshseries: UpdateSeries(%d): %w", seriesID, err)
	}

	s.log.LogAttrs(ctx, slog.LevelInfo,
		"refreshseries: refresh complete",
		slog.Int("seriesId", seriesID),
		slog.String("title", updated.Title),
		slog.Int("tvdbId", updated.TvdbID))
	return updated, nil
}

// copySkyhookFields copies the 24 SkyHook-authoritative fields from
// `src` onto `dst`. Mirrors C# `:89-114` field-by-field. Order
// preserved for easy line-comparison against the C# source.
func copySkyhookFields(dst, src *series.Series) {
	dst.Title = src.Title
	dst.Year = src.Year
	dst.TitleSlug = src.TitleSlug
	dst.TvRageID = src.TvRageID
	dst.TvMazeID = src.TvMazeID
	dst.TmdbID = src.TmdbID
	dst.ImdbID = src.ImdbID
	dst.MalIDs = src.MalIDs
	dst.AniListIDs = src.AniListIDs
	dst.AirTime = src.AirTime
	dst.Overview = src.Overview
	dst.OriginalLanguage = src.OriginalLanguage
	dst.Status = src.Status
	dst.CleanTitle = src.CleanTitle
	dst.SortTitle = src.SortTitle
	dst.Runtime = src.Runtime
	dst.Images = src.Images
	dst.Network = src.Network
	dst.FirstAired = src.FirstAired
	dst.LastAired = src.LastAired
	dst.Ratings = src.Ratings
	dst.Actors = src.Actors
	dst.Genres = src.Genres
	dst.Certification = src.Certification
	dst.OriginalCountry = src.OriginalCountry
}

// mergeSeasons ports C# `RefreshSeriesService.UpdateSeasons` at
// `:137-166`. Returns a JSON-encoded Seasons array where:
//
//   - Every season SkyHook returns is present (DistinctBy seasonNumber).
//   - If the existing series already had a row for that season number,
//     the persisted Monitored value carries forward.
//   - If the season is new AND seasonNumber == 0, Monitored = false
//     (C# `:147-152`).
//   - If the season is new AND seasonNumber > 0, Monitored =
//     `monitorNew` (caller passes series.MonitorNewItems == All).
//
// Implementation uses `map[string]json.RawMessage` per season so any
// upstream fields the Go-port hasn't named (Images, etc.) round-trip
// untouched. Decoding failures on either input return an error and
// the caller falls back to the SkyHook payload verbatim.
//
// ctx is threaded only for logging in the per-season "new season" trace
// — keeps the C# trace fidelity at `:149, :156` without polluting the
// hot path.
func mergeSeasons(existing, fresh string, monitorNew bool, log *slog.Logger, ctx context.Context, row *series.Series) (string, error) {
	if fresh == "" || fresh == "null" || fresh == "[]" {
		// Nothing fresh — preserve existing untouched.
		return existing, nil
	}
	var freshSeasons []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(fresh), &freshSeasons); err != nil {
		return "", fmt.Errorf("decode SkyHook seasons: %w", err)
	}
	existingByNumber := map[int]bool{} // seasonNumber → existing Monitored
	if existing != "" && existing != "null" && existing != "[]" {
		var rows []map[string]json.RawMessage
		if err := json.Unmarshal([]byte(existing), &rows); err != nil {
			return "", fmt.Errorf("decode existing seasons: %w", err)
		}
		for _, r := range rows {
			n, ok := decodeSeasonNumber(r)
			if !ok {
				continue
			}
			mon, _ := decodeMonitored(r)
			existingByNumber[n] = mon
		}
	}

	// DistinctBy seasonNumber — C# `:139`. First occurrence wins.
	seen := map[int]bool{}
	out := make([]map[string]json.RawMessage, 0, len(freshSeasons))
	for _, season := range freshSeasons {
		n, ok := decodeSeasonNumber(season)
		if !ok || seen[n] {
			continue
		}
		seen[n] = true

		var monitored bool
		if existingMonitored, hadExisting := existingByNumber[n]; hadExisting {
			monitored = existingMonitored
		} else {
			if n == 0 {
				monitored = false
				log.LogAttrs(ctx, slog.LevelDebug,
					"refreshseries: ignoring season 0 by default",
					slog.Int("tvdbId", row.TvdbID),
					slog.String("title", row.Title))
			} else {
				monitored = monitorNew
				log.LogAttrs(ctx, slog.LevelDebug,
					"refreshseries: new season",
					slog.Int("seasonNumber", n),
					slog.Int("tvdbId", row.TvdbID),
					slog.String("title", row.Title),
					slog.Bool("monitored", monitored))
			}
		}
		monBytes, _ := json.Marshal(monitored)
		season["monitored"] = monBytes
		out = append(out, season)
	}

	// Stable sort by seasonNumber so the persisted blob's order is
	// deterministic. C# preserves SkyHook upstream order which is
	// already by season number on the wire; the sort is defensive.
	sort.SliceStable(out, func(i, j int) bool {
		ni, _ := decodeSeasonNumber(out[i])
		nj, _ := decodeSeasonNumber(out[j])
		return ni < nj
	})

	encoded, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("encode merged seasons: %w", err)
	}
	return string(encoded), nil
}

func decodeSeasonNumber(season map[string]json.RawMessage) (int, bool) {
	raw, ok := season["seasonNumber"]
	if !ok {
		return 0, false
	}
	var n int
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, false
	}
	return n, true
}

func decodeMonitored(season map[string]json.RawMessage) (bool, bool) {
	raw, ok := season["monitored"]
	if !ok {
		return false, false
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		return false, false
	}
	return b, true
}
