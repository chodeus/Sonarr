// Package episoderefreshed ports the C# `EpisodeRefreshedService` at
// `src/NzbDrone.Core/Tv/EpisodeRefreshedService.cs`. Slice 5.J209 ships
// the full service:
//
//   - [Service.Handle] subscribes to [episodes.EpisodeInfoRefreshedEvent]
//     (published by [refreshepisodes.Service] since J200). When a per-
//     series episode reconcile finishes, filter the Added + Updated
//     slices for episodes that recently aired (or are about to) AND
//     are monitored — those are the candidates for a post-disk-scan
//     search dispatch. The filtered IDs are buffered in an in-memory
//     cache keyed by SeriesID.
//
//   - [Service.Search] drains the cache for a given series, loads each
//     buffered episode, filters to those that still have no file on
//     disk (HasFile==false), and dispatches the search via an
//     [EpisodeSearcher]. Whatever's in the cache for that series is
//     then cleared.
//
// The cache decouples "we know which episodes need searching" from
// "the disk scan is done so it's safe to search" — search dispatched
// too early would treat just-downloaded files as missing. C# invokes
// Search from `Tv/SeriesScannedHandler.cs:42` (the IHandle for
// `SeriesScannedEvent` + `SeriesScanSkippedEvent`); the Go-port doesn't
// yet have a SeriesScanned event-wiring slice in flight, so Search is
// exposed as a public method for the future scan-event slice to invoke.
//
// Deferred relative to C# (each its own follow-up):
//
//   - SeriesScannedEvent / SeriesScanSkippedEvent subscriber that
//     calls Search — needs the corresponding event types + publishers
//     to land first. The Go-port's mediafiles package emits
//     SeriesScanSkippedEvent (J197) but SeriesScannedEvent isn't yet
//     ported.
//   - TTL bound on the cache entries. C# uses `ICached<List<int>>`
//     which has implicit cache-manager TTL; the Go-port uses a plain
//     map gated by Search() draining. Without the SeriesScanned hookup
//     entries persist until process exit (acceptable for J209 since
//     no consumer calls Search yet — the cache is essentially a
//     write-only buffer).
package episoderefreshed

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/series"
)

// SeriesLookup is the narrow accessor [Service.Handle] needs to read
// the series row that triggered the EpisodeInfoRefreshedEvent. The
// event itself carries only SeriesID (see episodes/events.go), so the
// Handle path looks up the series-side gates (Monitored flag,
// AddOptions blob) via this interface. `*series.Service` satisfies it.
type SeriesLookup interface {
	GetSeries(ctx context.Context, id int) (*series.Series, error)
}

// EpisodeLookup is the narrow accessor [Service.Search] needs to load
// each cached episode and check whether it has a file on disk. The
// HasFile semantics live entirely in the (*episodes.Episode).HasFile
// derived field — there's no separate has-file column. `*episodes.Service`
// satisfies this via its existing GetEpisode method.
type EpisodeLookup interface {
	GetEpisode(ctx context.Context, id int) (*episodes.Episode, error)
}

// EpisodeSearcher is the narrow dispatch hook [Service.Search] calls
// with the filtered missing-episode IDs. Mirrors the C#
// `_commandQueueManager.Push(new EpisodeSearchCommand(...))` shape but
// without committing the Go-port to a specific command-vs-direct-call
// architecture — the wiring slice can satisfy this with either a
// command-queue pusher OR a direct call into `*indexersearch.Service`,
// whichever is more idiomatic when the SeriesScanned hookup lands.
type EpisodeSearcher interface {
	SearchEpisodes(ctx context.Context, episodeIDs []int) error
}

// Service buffers the per-series "to-search" set between
// [Service.Handle] (called when episodes refresh) and [Service.Search]
// (called when the post-refresh disk scan completes).
type Service struct {
	seriesLookup  SeriesLookup
	episodeLookup EpisodeLookup
	searcher      EpisodeSearcher // optional; nil short-circuits Search dispatch
	now           func() time.Time
	log           *slog.Logger

	mu    sync.Mutex
	cache map[int][]int // seriesID → ordered, deduped episode IDs
}

// NewService binds the dependencies and (if `bus` is non-nil) registers
// [Service.Handle] as a subscriber to [episodes.EpisodeInfoRefreshedEvent].
// `searcher` is optional — when nil, [Service.Search] still drains the
// cache but logs+skips the search dispatch (useful while the Go-port
// is still wiring the SeriesScanned consumer end).
//
// `now` defaults to time.Now().UTC() when nil; pinning is for
// deterministic tests of the 14d/1d AirDateUtc window.
func NewService(
	bus *messaging.Bus,
	seriesLookup SeriesLookup,
	episodeLookup EpisodeLookup,
	searcher EpisodeSearcher,
	now func() time.Time,
	log *slog.Logger,
) (*Service, error) {
	if seriesLookup == nil {
		return nil, errors.New("episoderefreshed: series lookup is nil")
	}
	if episodeLookup == nil {
		return nil, errors.New("episoderefreshed: episode lookup is nil")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if log == nil {
		log = slog.Default()
	}
	s := &Service{
		seriesLookup:  seriesLookup,
		episodeLookup: episodeLookup,
		searcher:      searcher,
		now:           now,
		log:           log,
		cache:         make(map[int][]int),
	}
	if bus != nil {
		messaging.Subscribe(bus, "episoderefreshed.Handle",
			func(ctx context.Context, ev episodes.EpisodeInfoRefreshedEvent) error {
				return s.Handle(ctx, ev)
			})
	}
	return s, nil
}

// Handle ports C# `EpisodeRefreshedService.Handle(EpisodeInfoRefreshedEvent)`
// at `EpisodeRefreshedService.cs:54-100`. Filters Added + Updated for
// episodes worth searching after the next disk scan, stuffs the
// resulting deduped ID list into the per-series cache.
//
// Decision tree (line-aligned with C#):
//
//   - `:56` skip if series.AddOptions != null — active add flow,
//     SeriesAddCompletedEvent + the add-monitor override (J189) handle
//     it. Reads AddOptions via [series.DecodeAddOptions] which mirrors
//     C# AddOptions != null on the empty/null-blob case.
//   - `:60-64` skip if !series.Monitored — no point queuing searches
//     for a paused show.
//   - `:66-71` from Added: keep episodes where AirDateUtc is in
//     [now-14d, now+1d] AND Monitored.
//   - `:80-86` from Updated: keep episodes where
//     AbsoluteEpisodeNumberAdded AND AirDateUtc in [now-14d, now+1d]
//     AND Monitored. (The J207 flag drives this branch — anime shows
//     whose abs number arrived on a refresh tick get a re-search so
//     the indexer query can use the new abs number to match upstream
//     releases.)
//   - `:97` if any IDs survive both filters, set cache[seriesID] to
//     the deduped union of the two slices. Order preserved: Added IDs
//     come first, then Updated IDs, matching C# `toSearch.AddRange`
//     order.
//
// Returns nil errors on the happy path. A series-lookup failure returns
// the error (defensive: a typical bus subscriber would log+discard,
// but Handle's caller is the bus dispatcher which already does that).
//
// J209.
func (s *Service) Handle(ctx context.Context, ev episodes.EpisodeInfoRefreshedEvent) error {
	if s == nil {
		return errors.New("episoderefreshed: nil Service")
	}

	ser, err := s.seriesLookup.GetSeries(ctx, ev.SeriesID)
	if err != nil {
		return fmt.Errorf("episoderefreshed: GetSeries(%d): %w", ev.SeriesID, err)
	}
	if ser == nil {
		s.log.LogAttrs(ctx, slog.LevelDebug,
			"episoderefreshed: series not found — skipping",
			slog.Int("seriesId", ev.SeriesID))
		return nil
	}

	// AddOptions present → active add flow handled elsewhere.
	if opts, _ := series.DecodeAddOptions(ser.AddOptions); opts != nil {
		return nil
	}

	if !ser.Monitored {
		s.log.LogAttrs(ctx, slog.LevelDebug,
			"episoderefreshed: series is not monitored — skipping",
			slog.Int("seriesId", ev.SeriesID))
		return nil
	}

	now := s.now().UTC()
	lower := now.AddDate(0, 0, -14)
	upper := now.AddDate(0, 0, 1)

	previouslyAired := pickRecentMonitored(ev.Added, lower, upper, false /* requireFlag */)
	if len(previouslyAired) == 0 {
		s.log.LogAttrs(ctx, slog.LevelDebug,
			"episoderefreshed: newly added episodes all air in the future (or out of window)",
			slog.Int("seriesId", ev.SeriesID))
	}

	absoluteAdded := pickRecentMonitored(ev.Updated, lower, upper, true /* requireFlag */)
	if len(absoluteAdded) == 0 {
		s.log.LogAttrs(ctx, slog.LevelDebug,
			"episoderefreshed: no updated episodes recently aired with AbsoluteEpisodeNumberAdded",
			slog.Int("seriesId", ev.SeriesID))
	}

	if len(previouslyAired) == 0 && len(absoluteAdded) == 0 {
		return nil
	}

	combined := append(append([]int(nil), previouslyAired...), absoluteAdded...)
	deduped := distinctPreservingOrder(combined)

	s.mu.Lock()
	s.cache[ev.SeriesID] = deduped
	s.mu.Unlock()

	s.log.LogAttrs(ctx, slog.LevelDebug,
		"episoderefreshed: buffered episode search candidates",
		slog.Int("seriesId", ev.SeriesID),
		slog.Int("count", len(deduped)))
	return nil
}

// Search ports C# `EpisodeRefreshedService.Search(int seriesId)` at
// `EpisodeRefreshedService.cs:37-52`. Drains the per-series cache,
// loads each episode, filters to those still missing a file on disk
// (HasFile==false), and dispatches a search via [EpisodeSearcher].
// The cache entry is removed regardless of dispatch outcome — matches
// C# `:51 _searchCache.Remove(...)` which fires unconditionally.
//
// No-op if the cache is empty for `seriesID`. A nil searcher
// short-circuits the dispatch (logs at debug) but still drains the
// cache entry so the buffer doesn't leak across the (eventual)
// SeriesScanned hookup.
//
// Returns the first episode-lookup error or searcher error. Per-
// episode lookup failures are absorbed (warn-log + continue) so one
// missing row doesn't abort the dispatch — matches C# behaviour where
// `_episodeService.GetEpisode(e)` would throw and propagate, but the
// Go-port adopts the warn-and-continue posture established by J191's
// per-id refresh loop.
//
// J209.
func (s *Service) Search(ctx context.Context, seriesID int) error {
	if s == nil {
		return errors.New("episoderefreshed: nil Service")
	}

	s.mu.Lock()
	candidates := s.cache[seriesID]
	delete(s.cache, seriesID)
	s.mu.Unlock()

	if len(candidates) == 0 {
		return nil
	}

	missing := make([]int, 0, len(candidates))
	for _, id := range candidates {
		ep, err := s.episodeLookup.GetEpisode(ctx, id)
		if err != nil {
			s.log.LogAttrs(ctx, slog.LevelWarn,
				"episoderefreshed: episode lookup failed — skipping",
				slog.Int("episodeId", id),
				slog.Any("err", err))
			continue
		}
		if ep == nil {
			continue
		}
		if !hasFile(ep) {
			missing = append(missing, ep.ID)
		}
	}

	if len(missing) == 0 {
		s.log.LogAttrs(ctx, slog.LevelDebug,
			"episoderefreshed: cached episodes all have files — no search dispatched",
			slog.Int("seriesId", seriesID),
			slog.Int("checked", len(candidates)))
		return nil
	}

	if s.searcher == nil {
		s.log.LogAttrs(ctx, slog.LevelDebug,
			"episoderefreshed: no searcher wired — dispatch skipped",
			slog.Int("seriesId", seriesID),
			slog.Int("missingCount", len(missing)))
		return nil
	}

	if err := s.searcher.SearchEpisodes(ctx, missing); err != nil {
		return fmt.Errorf("episoderefreshed: SearchEpisodes(%v): %w", missing, err)
	}
	s.log.LogAttrs(ctx, slog.LevelDebug,
		"episoderefreshed: search dispatched",
		slog.Int("seriesId", seriesID),
		slog.Int("missingCount", len(missing)))
	return nil
}

// pickRecentMonitored returns the IDs of episodes whose AirDateUtc is
// inside [lower, upper] AND whose Monitored flag is true. When
// `requireFlag` is true the AbsoluteEpisodeNumberAdded transient flag
// must also be set (the J207 flag drives the Updated-side filter).
//
// Window endpoints match C# `Between(DateTime.UtcNow.AddDays(-14),
// DateTime.UtcNow.AddDays(1))` — the extension's inclusive
// half-open-on-the-upper semantics aren't formally documented in
// Sonarr's C# helpers, so we use simple !Before(lower) && !After(upper)
// inclusive-on-both-ends which is the most-permissive interpretation
// matching the "recently aired or imminent" intent.
func pickRecentMonitored(eps []episodes.Episode, lower, upper time.Time, requireFlag bool) []int {
	out := make([]int, 0, len(eps))
	for i := range eps {
		ep := &eps[i]
		if !ep.Monitored {
			continue
		}
		if requireFlag && !ep.AbsoluteEpisodeNumberAdded {
			continue
		}
		if ep.AirDateUtc == nil {
			continue
		}
		t := *ep.AirDateUtc
		if t.Before(lower) || t.After(upper) {
			continue
		}
		out = append(out, ep.ID)
	}
	return out
}

// distinctPreservingOrder keeps the first occurrence of each ID
// matching C# `toSearch.Distinct().ToList()` (LINQ Distinct preserves
// source order).
func distinctPreservingOrder(in []int) []int {
	seen := make(map[int]bool, len(in))
	out := make([]int, 0, len(in))
	for _, id := range in {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// hasFile mirrors C# `Episode.HasFile` derived property at
// `Tv/Episode.cs` (`EpisodeFileId > 0`). The Go-port stores
// EpisodeFileID as `*int` — nil OR zero-valued pointee both mean
// "no file" (the zero-row foreign-key sentinel matches the C# 0-default
// for unset).
func hasFile(ep *episodes.Episode) bool {
	if ep == nil || ep.EpisodeFileID == nil {
		return false
	}
	return *ep.EpisodeFileID > 0
}
