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
	"sonarr.tv/sonarr/internal/mediafiles"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/metadatasource"
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
// uses. Get + UpdateSeries cover the J191 single-id surface; J193
// grew GetAllSeries for the all-series sweep; J196 grew UpdateTags
// for the per-row auto-tagging refresh. Future slices that wire
// RefreshEpisodeService can grow the interface further.
type SeriesStore interface {
	GetSeries(ctx context.Context, id int) (*series.Series, error)
	GetAllSeries(ctx context.Context) ([]series.Series, error)
	UpdateSeries(ctx context.Context, ns *series.Series, opts series.UpdateOptions) (*series.Series, error)
	UpdateTags(ctx context.Context, s *series.Series) (changed bool, err error)
}

// Rescanner is the narrow slice of [mediafiles.Service] the
// disk-rescan-after-refresh arm consumes (C# `DiskScanService.Scan` at
// `:138`). Satisfied by *mediafiles.Service. Defined as an interface
// so the unit tests can stub the scan without spinning up the full
// disk-scan stack.
type Rescanner interface {
	Scan(ctx context.Context, ser *series.Series) error
}

// RescanConfig is the narrow slice of [config.Provider] the rescan
// gate consumes (C# `IConfigService.RescanAfterRefresh` at
// `ConfigService.cs`). Returns the string token "Always" / "AfterManual"
// / "Never". Defined as an interface so the unit tests can pin the
// config branch without standing up a sqlite-backed provider.
type RescanConfig interface {
	RescanAfterRefresh() string
}

// EpisodeRefresher is the narrow slice of [*refreshepisodes.Service]
// the refresh path reaches for. Satisfied by *refreshepisodes.Service.
// Defined here so the unit tests can stub without spinning up the
// episodes stack. J200 wires this — pre-J200 the SkyHook episodes
// payload was discarded after GetSeriesInfo.
type EpisodeRefresher interface {
	RefreshEpisodeInfo(ctx context.Context, ser *series.Series, remote []episodes.Episode) error
}

// Service is the RefreshSeriesService port. Per-id Refresh is the
// only entry point; callers (the RefreshSeries command handler, the
// scheduled-task tick) marshal `[]int` series IDs from their command
// shape.
type Service struct {
	info        SeriesInfoProvider
	store       SeriesStore
	bus         *messaging.Bus
	episodes    EpisodeLookup    // optional; gates the all-series sweep on ShouldRefresh
	rescanner   Rescanner        // optional; gates the disk-rescan-after-refresh arm (J197)
	rescanCfg   RescanConfig     // optional; sibling of rescanner — both must be set to scan
	epRefresher EpisodeRefresher // optional; J200 — reconciles SkyHook episodes payload after persist
	now         func() time.Time
	log         *slog.Logger
}

// SetEpisodeLookup wires the [EpisodeLookup] dep used by the
// J195 ShouldRefresh predicate in [Service.RefreshAll]. Optional —
// nil leaves the sweep refreshing every row (J193 baseline). Done
// as a post-construction setter so the J191 NewService signature
// stays stable across the J195 layering; main.go calls this after
// NewService.
func (s *Service) SetEpisodeLookup(ep EpisodeLookup) {
	s.episodes = ep
}

// SetEpisodeRefresher wires the [EpisodeRefresher] dep used by the
// J200 episode-reconcile arm in [Service.RefreshSeriesInfo]. Optional —
// nil leaves the SkyHook episodes payload discarded (the J191
// baseline). Done as a post-construction setter so the J191
// NewService signature stays stable across the J200 layering; main.go
// calls this after NewService.
func (s *Service) SetEpisodeRefresher(er EpisodeRefresher) {
	s.epRefresher = er
}

// SetRescanner wires the [Rescanner] + [RescanConfig] deps used by the
// J197 disk-rescan-after-refresh arm (C# `RefreshSeriesService.RescanSeries`
// at `:168-199`). Both must be non-nil for the rescan to fire; if either
// is nil the per-row scan is skipped (matches the J191 baseline where
// no rescan happened). Done as a post-construction setter so the J191
// NewService signature stays stable across the J197 layering; main.go
// calls this after NewService once *mediafiles.Service + *config.Provider
// are both available.
func (s *Service) SetRescanner(scanner Rescanner, cfg RescanConfig) {
	s.rescanner = scanner
	s.rescanCfg = cfg
}

// NewService binds the dependencies into a Service. `now` defaults to
// time.Now().UTC() when nil — pinning is for deterministic tests. `bus`
// is optional — nil silently skips the SeriesUpdatedEvent publish on
// the soft-delete arm.
func NewService(info SeriesInfoProvider, store SeriesStore, bus *messaging.Bus, now func() time.Time, log *slog.Logger) (*Service, error) {
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
	return &Service{info: info, store: store, bus: bus, now: now, log: log}, nil
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
// [Service.RefreshSeriesInfo] for callers that need the updated row.
//
// J198 — isManual mirrors C# `RefreshSeriesService.Execute :213`
// (`trigger == CommandTrigger.Manual`). True when the command was
// user-triggered (UI button, REST call); false for the scheduled-task
// tick. Threaded through to the rescan tail so the "AfterManual"
// RescanAfterRefresh config branch fires correctly.
func (s *Service) Refresh(ctx context.Context, seriesID int, isManual bool) error {
	_, err := s.RefreshSeriesInfo(ctx, seriesID, isManual)
	return err
}

// RefreshAll ports the all-series sweep arm of C#
// `RefreshSeriesService.Execute` at `:249-282`. Iterates every series
// in SortTitle order and calls [Service.Refresh] per row. Per-row
// failures are absorbed with a warn log — matches the C# `catch
// (SeriesNotFoundException)` + `catch (Exception)` arms at
// `:262-270`. The C# `ICheckIfSeriesShouldBeRefreshed.ShouldRefresh`
// predicate (`:256`) that gates per-row refresh on scheduled triggers
// is deferred: J193 treats every row as needs-refresh, equivalent to
// `trigger == CommandTrigger.Manual` always-fire. The predicate port
// is a follow-up slice.
//
// Returns the first ctx.Err() encountered (cancellation propagates)
// but per-row errors are logged-and-swallowed so a partially-refreshed
// catalogue is acceptable. The C# version's `CommandResult.Indeterminate`
// reporter integration is deferred.
//
// The 12-hour scheduled-task tick (registered at
// `internal/commands/handlers/handlers.go` with `RefreshSeriesName`)
// now does useful work: an empty-body RefreshSeriesCommand fires this
// path instead of a stub-log no-op.
func (s *Service) RefreshAll(ctx context.Context, isManual bool) error {
	if s == nil {
		return errors.New("refreshseries: nil Service")
	}
	all, err := s.store.GetAllSeries(ctx)
	if err != nil {
		return fmt.Errorf("refreshseries: GetAllSeries: %w", err)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].SortTitle < all[j].SortTitle })

	skipped := 0
	for i := range all {
		if err := ctx.Err(); err != nil {
			return err
		}
		row := all[i]
		// J195 — gate refresh on ShouldRefresh predicate when an
		// EpisodeLookup is wired. When unset (or when the predicate
		// returns true) the row is refreshed; the per-row Refresh
		// path is otherwise unchanged.
		//
		// J198 — isManual bypasses the ShouldRefresh gate per C#
		// `RefreshSeriesService.cs:256`
		// (`trigger == CommandTrigger.Manual || ShouldRefresh(...)`).
		// A user-triggered "Refresh All" intentionally refreshes every
		// row regardless of staleness — the scheduled-task tick is the
		// only path that should honour the predicate.
		if !isManual && s.episodes != nil && !ShouldRefresh(ctx, &row, s.episodes, s.now(), s.log) {
			s.log.LogAttrs(ctx, slog.LevelInfo,
				"refreshseries: skipping row (ShouldRefresh=false)",
				slog.Int("seriesId", row.ID),
				slog.String("title", row.Title))
			skipped++
			// J196 — even skipped rows get UpdateTags per C# sweep
			// `:277-279`: auto-tagging rules can change independently
			// of TheTVDB metadata, so the tick re-evaluates them
			// regardless of refresh decision.
			s.updateTags(ctx, &row)
			// J197 — even skipped rows get rescanSeries per C# sweep
			// `:279`: a rescan on the periodic tick catches disk-side
			// changes (manual file edits, new episodes appearing on
			// disk) even when the TheTVDB metadata is fresh enough to
			// skip the upstream refresh.
			s.rescanSeries(ctx, &row, false, isManual)
			continue
		}
		if _, err := s.RefreshSeriesInfo(ctx, row.ID, isManual); err != nil {
			s.log.LogAttrs(ctx, slog.LevelWarn,
				"refreshseries: per-row refresh failed (sweep continues)",
				slog.Int("seriesId", row.ID),
				slog.String("title", row.Title),
				slog.Int("tvdbId", row.TvdbID),
				slog.Any("err", err))
		}
	}
	s.log.LogAttrs(ctx, slog.LevelInfo,
		"refreshseries: all-series sweep complete",
		slog.Int("count", len(all)),
		slog.Int("skipped", skipped))
	return nil
}

// RefreshSeriesInfo is the full per-series refresh; see the package
// doc-comment for the field-list semantics + deferred surface.
//
// J198 — isManual mirrors C# `RefreshSeriesService.Execute :213`
// (`trigger == CommandTrigger.Manual`). Threaded through to the rescan
// tail; callers reading via [Refresh] forward the bool from their own
// trigger context.
func (s *Service) RefreshSeriesInfo(ctx context.Context, seriesID int, isManual bool) (*series.Series, error) {
	if s == nil {
		return nil, errors.New("refreshseries: nil Service")
	}

	row, err := s.store.GetSeries(ctx, seriesID)
	if err != nil {
		return nil, fmt.Errorf("refreshseries: GetSeries(%d): %w", seriesID, err)
	}

	merged, remoteEpisodes, err := s.info.GetSeriesInfo(ctx, row.TvdbID)
	if err != nil {
		// J192 — SeriesNotFound soft-delete arm. Ports C#
		// `RefreshSeriesService.cs:70-81`: when TheTVDB returns 404,
		// flip Series.Status to Deleted (if not already), persist
		// silently, publish a SeriesUpdatedEvent so MediaCover /
		// PendingReleaseService / SignalR consumers can react.
		// Original error still propagates so the handler logs +
		// the command result reflects the failure.
		if errors.Is(err, metadatasource.ErrSeriesNotFound) {
			s.handleSeriesNotFound(ctx, row)
		}
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

	// J208 — path canonicalisation per C# `RefreshSeriesService.cs:118-124`.
	// Two steps: DirectoryInfo.FullName canonicalisation (always-on) and
	// GetActualCasing (Windows-only in C#; no-op on the Go-port until the
	// dedicated Windows slice lands — see pathcasing.go). Errors absorbed
	// via warn-log + continue mirroring C# try/catch at `:121-124` so a
	// transient path issue doesn't bubble past the per-series-refresh
	// boundary; the row's other refreshed fields still persist.
	if normalized, err := normalizePath(row.Path); err != nil {
		s.log.LogAttrs(ctx, slog.LevelWarn,
			"refreshseries: couldn't normalise series path",
			slog.Int("seriesId", row.ID),
			slog.String("path", row.Path),
			slog.Any("err", err))
	} else {
		row.Path = getActualCasing(normalized)
	}

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

	// J196 — auto-tagging refresh per C# `RefreshSeriesService.Execute`
	// at `:226` and `:272`: UpdateTags runs after every per-id
	// RefreshSeriesInfo success.
	s.updateTags(ctx, updated)

	// J200 — episode reconcile per C# `RefreshSeriesService.cs:129`
	// (`_refreshEpisodeService.RefreshEpisodeInfo(series, episodes)`).
	// Optional: nil refresher leaves the J191 baseline behaviour
	// (SkyHook episodes discarded). Errors are absorbed with a warn
	// log so an episode-reconcile failure doesn't bubble past the
	// per-series-refresh boundary; the series row itself is already
	// persisted by this point.
	if s.epRefresher != nil {
		if err := s.epRefresher.RefreshEpisodeInfo(ctx, updated, remoteEpisodes); err != nil {
			s.log.LogAttrs(ctx, slog.LevelWarn,
				"refreshseries: episode reconcile failed (series row already persisted)",
				slog.Int("seriesId", updated.ID),
				slog.String("title", updated.Title),
				slog.Any("err", err))
		}
	}

	// J197 — post-refresh disk rescan per C# Execute `:227`. isNew
	// stays false until the IsNewSeries field lands on the
	// RefreshSeriesCommand wire shape; isManual is forwarded from the
	// caller (J198 — populated from the dispatcher's ctx-attached
	// Trigger).
	s.rescanSeries(ctx, updated, false, isManual)

	s.log.LogAttrs(ctx, slog.LevelInfo,
		"refreshseries: refresh complete",
		slog.Int("seriesId", seriesID),
		slog.String("title", updated.Title),
		slog.Int("tvdbId", updated.TvdbID))
	return updated, nil
}

// rescanSeries ports C# `RefreshSeriesService.RescanSeries` at
// `RefreshSeriesService.cs:168-199`. Gates the post-refresh disk-rescan
// on the `RescanAfterRefresh` config enum:
//
//   - "Never" — publish [mediafiles.SeriesScanSkippedEvent] with
//     reason [mediafiles.SeriesScanSkippedReasonNeverRescanAfterRefresh]
//     and return (`:176-181`).
//   - "AfterManual" + !isManual — publish the same event with reason
//     [mediafiles.SeriesScanSkippedReasonRescanAfterManualRefreshOnly]
//     and return (`:183-188`).
//   - else (or `isNew`) — call [Rescanner.Scan] (`:191-198`). Errors
//     are absorbed with an error log so a scan failure doesn't abort
//     the refresh batch (matches C# `:195-198`).
//
// isNew forces the scan regardless of config (C# `:172-174`). Always
// false from the Go-port refresh path today — the cold-start
// SeriesAddedEvent handler that would set it is not yet ported.
//
// isManual ports the C# `trigger == CommandTrigger.Manual` check.
// Always false from the Go-port refresh path today because the J197
// wire shape doesn't yet carry a Trigger field — deferred to the
// ManualTrigger slice (PROGRESS.md's top "Next candidates" item).
// Behavioural divergence from C#: a manually-triggered refresh with
// the "AfterManual" config will currently skip the scan; once
// ManualTrigger lands the gate becomes accurate.
//
// No-op when either [Rescanner] or [RescanConfig] is nil (the J191
// baseline behaviour) — main.go's SetRescanner call is the wiring
// boundary.
func (s *Service) rescanSeries(ctx context.Context, row *series.Series, isNew, isManual bool) {
	if row == nil {
		return
	}
	if s.rescanner == nil || s.rescanCfg == nil {
		return
	}

	mode := s.rescanCfg.RescanAfterRefresh()
	if !isNew {
		switch mode {
		case "Never":
			s.log.LogAttrs(ctx, slog.LevelDebug,
				"refreshseries: skipping rescan (RescanAfterRefresh=Never)",
				slog.Int("seriesId", row.ID),
				slog.String("title", row.Title))
			if s.bus != nil {
				_ = messaging.Publish(ctx, s.bus, mediafiles.SeriesScanSkippedEvent{
					Series: row,
					Reason: mediafiles.SeriesScanSkippedReasonNeverRescanAfterRefresh,
				})
			}
			return
		case "AfterManual":
			if !isManual {
				s.log.LogAttrs(ctx, slog.LevelDebug,
					"refreshseries: skipping rescan (RescanAfterRefresh=AfterManual, trigger=scheduled)",
					slog.Int("seriesId", row.ID),
					slog.String("title", row.Title))
				if s.bus != nil {
					_ = messaging.Publish(ctx, s.bus, mediafiles.SeriesScanSkippedEvent{
						Series: row,
						Reason: mediafiles.SeriesScanSkippedReasonRescanAfterManualRefreshOnly,
					})
				}
				return
			}
		}
	} else {
		s.log.LogAttrs(ctx, slog.LevelDebug,
			"refreshseries: forcing rescan (new series)",
			slog.Int("seriesId", row.ID),
			slog.String("title", row.Title))
	}

	if err := s.rescanner.Scan(ctx, row); err != nil {
		s.log.LogAttrs(ctx, slog.LevelError,
			"refreshseries: rescan failed (refresh batch continues)",
			slog.Int("seriesId", row.ID),
			slog.String("title", row.Title),
			slog.Any("err", err))
	}
}

// updateTags ports C# `RefreshSeriesService.UpdateTags(series)` at
// `RefreshSeriesService.cs:201-209` — calls series.Service.UpdateTags
// (which computes the auto-tagging delta + persists if changed) and
// swallows errors with a warn log so a tag-system hiccup doesn't
// abort the refresh batch. C# `:201-209` doesn't catch; the Go-port
// adds the absorb posture to match the broader "refresh failures
// don't abort the batch" stance the rest of the service takes.
//
// Returns whether tags changed (informational only — caller doesn't
// branch on it). The C# version checks `tagsUpdated` to decide
// whether to call UpdateSeries again, but the Go-port
// series.Service.UpdateTags persists internally when changed (see
// `internal/series/mutations.go:206-262`), so the caller's
// post-call UpdateSeries pass isn't needed.
func (s *Service) updateTags(ctx context.Context, row *series.Series) {
	if row == nil {
		return
	}
	if _, err := s.store.UpdateTags(ctx, row); err != nil {
		s.log.LogAttrs(ctx, slog.LevelWarn,
			"refreshseries: UpdateTags failed",
			slog.Int("seriesId", row.ID),
			slog.String("title", row.Title),
			slog.Any("err", err))
	}
}

// handleSeriesNotFound ports C# `RefreshSeriesService.cs:70-81` —
// the SeriesNotFoundException catch arm. Idempotent: if `row.Status`
// is already [series.SeriesStatusDeleted] the function is a no-op
// (matches the C# `if (series.Status != SeriesStatusType.Deleted)`
// guard at `:72`). On the first transition, the row is persisted
// silently (UpdateSeries with SkipPublishUpdatedEvent=true) and a
// [series.SeriesUpdatedEvent] is published so refresh-aware
// subscribers (MediaCover image re-fetch, PendingReleaseService
// invalidation, V3 SeriesController SignalR broadcast) react.
//
// Errors from UpdateSeries or messaging publish degrade to warn logs
// — the caller is about to propagate the original SkyHook error so
// the command result reflects the failure regardless.
func (s *Service) handleSeriesNotFound(ctx context.Context, row *series.Series) {
	if row.Status == series.SeriesStatusDeleted {
		return
	}
	row.Status = series.SeriesStatusDeleted
	if _, err := s.store.UpdateSeries(ctx, row, series.UpdateOptions{SkipPublishUpdatedEvent: true}); err != nil {
		s.log.LogAttrs(ctx, slog.LevelWarn,
			"refreshseries: soft-delete UpdateSeries failed",
			slog.Int("seriesId", row.ID),
			slog.String("title", row.Title),
			slog.Any("err", err))
		return
	}
	s.log.LogAttrs(ctx, slog.LevelDebug,
		"refreshseries: series marked deleted on TheTVDB",
		slog.Int("seriesId", row.ID),
		slog.String("title", row.Title),
		slog.Int("tvdbId", row.TvdbID))
	if s.bus != nil {
		_ = messaging.Publish(ctx, s.bus, series.SeriesUpdatedEvent{Series: row})
	}
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
