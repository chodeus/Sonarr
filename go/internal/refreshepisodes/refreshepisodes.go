// Package refreshepisodes ports the C# `RefreshEpisodeService` at
// `src/NzbDrone.Core/Tv/RefreshEpisodeService.cs`. Slice 5.J200 ships
// the baseline reconcile loop:
//
//   - Dedupe remote episodes by (SeasonNumber, EpisodeNumber).
//   - Match each remote episode against the persisted row by the same
//     key — when matched, copy SkyHook-authoritative fields onto the
//     existing row; when not matched, build a new row and stamp
//     Monitored via [getMonitoredStatus].
//   - Episodes that exist in the persistence but not in the remote
//     payload are deleted.
//   - Persist via Service.UpdateMany / InsertMany / DeleteMany.
//   - Publish [episodes.EpisodeInfoRefreshedEvent] with the three slices
//     so cache-invalidation + SignalR broadcaster subscribers can react.
//
// Deferred to follow-up slices (each its own sub-arc):
//
//   - UnmonitorReaddedEpisodes (C# `:157-189`) — gates the unmonitor
//     of old-aired-episodes-on-cold-add via series.AddOptions.
//   - AdjustMultiEpisodeAirTime (C# `:191-214`) — spreads same-day
//     multi-episode air times by series.Runtime.
//   - AdjustDirectToDvdAirDate (C# `:216-226`) — backfills AirDateUtc
//     from series.FirstAired for ended-shows with no episode dates.
//   - Anime absolute-episode-number renumbering (C# `:43-46, :228-236`)
//     + AbsoluteEpisodeNumberAdded flag tracking.
//   - Finale heuristic (C# `:95-107`) — stamps FinaleType=series for
//     the last episode of the last season of recently-ended shows.
//
// All deferred arms are independent of the baseline reconcile — they
// can layer in via additional helpers without touching the core loop.

package refreshepisodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/series"
)

// EpisodeStore is the narrow slice of [*episodes.Service] the
// reconcile path reaches for. Defined here so the unit tests can stub
// without spinning up the full episodes stack.
type EpisodeStore interface {
	GetEpisodesBySeries(ctx context.Context, seriesID int) ([]episodes.Episode, error)
	InsertMany(ctx context.Context, list []*episodes.Episode) error
	UpdateMany(ctx context.Context, list []*episodes.Episode) error
	DeleteMany(ctx context.Context, list []episodes.Episode) error
}

// Service ports the C# `RefreshEpisodeService`. Single entry point:
// [Service.RefreshEpisodeInfo].
type Service struct {
	store EpisodeStore
	bus   *messaging.Bus // optional; nil silently skips the publish
	now   func() time.Time
	log   *slog.Logger
}

// NewService binds the dependencies. `bus` is optional — nil silently
// skips the [episodes.EpisodeInfoRefreshedEvent] publish (the
// reconcile itself still runs and persists). `now` is optional —
// nil defaults to time.Now().UTC(); pinning is for deterministic
// tests of the J201 finale-detection 14-day cutoff.
func NewService(store EpisodeStore, bus *messaging.Bus, now func() time.Time, log *slog.Logger) (*Service, error) {
	if store == nil {
		return nil, errors.New("refreshepisodes: episode store is nil")
	}
	if log == nil {
		log = slog.Default()
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{store: store, bus: bus, now: now, log: log}, nil
}

// RefreshEpisodeInfo reconciles the persisted episode rows for
// `ser` against `remote` (the SkyHook-emitted list). Ports C#
// `RefreshEpisodeService.RefreshEpisodeInfo` at `:29-144`.
//
// Returns the first store-side error encountered (Get / Update /
// Insert / Delete); per-episode field-copy panics are recovered into
// log+continue so one bad row doesn't abort the batch (matches C#
// `:111-115` catch).
func (s *Service) RefreshEpisodeInfo(ctx context.Context, ser *series.Series, remote []episodes.Episode) error {
	if s == nil {
		return errors.New("refreshepisodes: nil Service")
	}
	if ser == nil {
		return errors.New("refreshepisodes: nil series")
	}

	s.log.LogAttrs(ctx, slog.LevelInfo,
		"refreshepisodes: starting reconcile",
		slog.Int("seriesId", ser.ID),
		slog.String("title", ser.Title),
		slog.Int("remoteCount", len(remote)))

	existing, err := s.store.GetEpisodesBySeries(ctx, ser.ID)
	if err != nil {
		return fmt.Errorf("refreshepisodes: GetEpisodesBySeries(%d): %w", ser.ID, err)
	}

	// existingByKey indexes the persisted rows by (season, episode)
	// for O(1) match-lookup. The map's values are pointers into a
	// local slice so the matcher can drop matched entries efficiently
	// — what remains after the loop is the to-delete set.
	type slot struct {
		ep      episodes.Episode
		matched bool
	}
	slots := make([]slot, len(existing))
	existingByKey := make(map[seasonEpisodeKey]*slot, len(existing))
	for i := range existing {
		slots[i] = slot{ep: existing[i]}
		key := seasonEpisodeKey{Season: existing[i].SeasonNumber, Episode: existing[i].EpisodeNumber}
		existingByKey[key] = &slots[i]
	}

	hasExisting := len(existing) > 0
	_ = hasExisting // J200 keeps this for future use by UnmonitorReaddedEpisodes (deferred).

	// dedupeFreeRemote keeps the first occurrence per (season,episode)
	// key, matching C# DistinctBy at `:41`.
	dupeFreeRemote := dedupeBySeasonEpisode(remote)

	monitoredSeasons := monitoredSeasonsLookup(ser)

	// J201 — finale-detection heuristic prep (C# :49-50, :104).
	// latestSeason mirrors C# `seasons.MaxBy(s => s.SeasonNumber)` —
	// the latest-known season for the series.
	// episodesPerSeason counts episodes per season from the
	// dupeFreeRemote list (C# `:49 episodesPerSeason`); used in the
	// last-episode-of-season check.
	// hasExistingFinaleInLatestSeason mirrors C# `:104` — when ANY
	// episode in the latest season already carries a FinaleType
	// (either from TheTVDB upstream or a prior reconcile that
	// stamped it), the heuristic skips. Pre-scanned across the batch
	// so the decision is consistent regardless of iteration order.
	latestSeason := latestSeasonNumber(ser)
	episodesPerSeason := countEpisodesPerSeason(dupeFreeRemote)
	hasExistingFinaleInLatestSeason := anyFinaleInSeason(dupeFreeRemote, latestSeason)
	nowUTC := s.now().UTC()

	updateList := make([]*episodes.Episode, 0, len(dupeFreeRemote))
	newList := make([]*episodes.Episode, 0, len(dupeFreeRemote))

	for i := range dupeFreeRemote {
		rem := &dupeFreeRemote[i]
		key := seasonEpisodeKey{Season: rem.SeasonNumber, Episode: rem.EpisodeNumber}
		if matched, ok := existingByKey[key]; ok && !matched.matched {
			matched.matched = true
			row := matched.ep
			applyRemoteFields(&row, rem)
			maybeStampSeriesFinale(&row, ser, latestSeason, episodesPerSeason, hasExistingFinaleInLatestSeason, nowUTC)
			updateList = append(updateList, &row)
			continue
		}
		// New episode — stamp Monitored from the series + season rules.
		fresh := episodes.Episode{
			SeriesID:  ser.ID,
			Monitored: getMonitoredStatus(rem, monitoredSeasons),
		}
		applyRemoteFields(&fresh, rem)
		maybeStampSeriesFinale(&fresh, ser, latestSeason, episodesPerSeason, hasExistingFinaleInLatestSeason, nowUTC)
		newList = append(newList, &fresh)
	}

	// What remains after the matcher is the to-delete set.
	toDelete := make([]episodes.Episode, 0)
	for i := range slots {
		if !slots[i].matched {
			toDelete = append(toDelete, slots[i].ep)
		}
	}

	if len(toDelete) > 0 {
		if err := s.store.DeleteMany(ctx, toDelete); err != nil {
			return fmt.Errorf("refreshepisodes: DeleteMany(%d rows): %w", len(toDelete), err)
		}
	}
	if len(updateList) > 0 {
		if err := s.store.UpdateMany(ctx, updateList); err != nil {
			return fmt.Errorf("refreshepisodes: UpdateMany(%d rows): %w", len(updateList), err)
		}
	}
	if len(newList) > 0 {
		if err := s.store.InsertMany(ctx, newList); err != nil {
			return fmt.Errorf("refreshepisodes: InsertMany(%d rows): %w", len(newList), err)
		}
	}

	s.log.LogAttrs(ctx, slog.LevelInfo,
		"refreshepisodes: reconcile complete",
		slog.Int("seriesId", ser.ID),
		slog.Int("inserted", len(newList)),
		slog.Int("updated", len(updateList)),
		slog.Int("deleted", len(toDelete)))

	if s.bus != nil {
		addedSnap := make([]episodes.Episode, 0, len(newList))
		for _, e := range newList {
			addedSnap = append(addedSnap, *e)
		}
		updatedSnap := make([]episodes.Episode, 0, len(updateList))
		for _, e := range updateList {
			updatedSnap = append(updatedSnap, *e)
		}
		_ = messaging.Publish(ctx, s.bus, episodes.EpisodeInfoRefreshedEvent{
			SeriesID: ser.ID,
			Added:    addedSnap,
			Updated:  updatedSnap,
			Removed:  toDelete,
		})
	}
	return nil
}

// seasonEpisodeKey is the (season, episode) tuple used as map key for
// existing-vs-remote matching.
type seasonEpisodeKey struct {
	Season, Episode int
}

// dedupeBySeasonEpisode keeps the first occurrence per (season,
// episode) key, preserving input order. Mirrors C# `DistinctBy` at
// `:41` (which similarly preserves source order).
func dedupeBySeasonEpisode(in []episodes.Episode) []episodes.Episode {
	seen := make(map[seasonEpisodeKey]bool, len(in))
	out := make([]episodes.Episode, 0, len(in))
	for _, e := range in {
		key := seasonEpisodeKey{Season: e.SeasonNumber, Episode: e.EpisodeNumber}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, e)
	}
	return out
}

// applyRemoteFields copies SkyHook-authoritative fields from `rem`
// onto `dst`. Mirrors C# `:78-93` field-by-field. Order preserved for
// line-comparison against the C# source.
//
// Title defaults to "TBA" when remote is empty (C# `:86`
// `episode.Title ?? "TBA"`). The C# version's `episodeToUpdate.SeriesId
// = series.Id` (`:78`) is collapsed into the caller (new episodes
// already set SeriesID at construction; updates carry it from the
// existing row).
func applyRemoteFields(dst, rem *episodes.Episode) {
	dst.TvdbID = rem.TvdbID
	dst.EpisodeNumber = rem.EpisodeNumber
	dst.SeasonNumber = rem.SeasonNumber
	dst.AbsoluteEpisodeNumber = rem.AbsoluteEpisodeNumber
	dst.AiredAfterSeasonNumber = rem.AiredAfterSeasonNumber
	dst.AiredBeforeSeasonNumber = rem.AiredBeforeSeasonNumber
	dst.AiredBeforeEpisodeNumber = rem.AiredBeforeEpisodeNumber
	if rem.Title == "" {
		dst.Title = "TBA"
	} else {
		dst.Title = rem.Title
	}
	dst.Overview = rem.Overview
	dst.AirDate = rem.AirDate
	dst.AirDateUtc = rem.AirDateUtc
	dst.Runtime = rem.Runtime
	dst.FinaleType = rem.FinaleType
	dst.Ratings = rem.Ratings
	dst.Images = rem.Images
}

// seasonMonitor mirrors the minimal field-shape used to decode the
// series Seasons JSON blob — just SeasonNumber + Monitored, the two
// fields the [getMonitoredStatus] check consults. The series package
// has its own unexported equivalent (mutations.go:120) for the
// monitor-comparison path; duplicating the shape here keeps
// refreshepisodes loosely coupled to series internals.
type seasonMonitor struct {
	SeasonNumber int  `json:"seasonNumber"`
	Monitored    bool `json:"monitored"`
}

// monitoredSeasonsLookup decodes the series Seasons JSON blob into a
// map[seasonNumber]monitored for the [getMonitoredStatus] check.
// Decode failures return a nil map — callers fall through to the
// "no matching season → monitored=true" branch, matching C#
// `:153-154` (`season == null || season.Monitored`).
func monitoredSeasonsLookup(ser *series.Series) map[int]bool {
	if ser == nil || ser.Seasons == "" {
		return nil
	}
	var parsed []seasonMonitor
	if err := json.Unmarshal([]byte(ser.Seasons), &parsed); err != nil {
		return nil
	}
	out := make(map[int]bool, len(parsed))
	for _, s := range parsed {
		out[s.SeasonNumber] = s.Monitored
	}
	return out
}

// getMonitoredStatus ports C# `:146-155`. Returns false for the
// special-case (EpisodeNumber == 0 && SeasonNumber != 1) — Sonarr
// treats the season-N "episode 0" as a non-episode-content placeholder
// (specials/recap) for everything except S1E0 (the pilot/series
// premiere). Otherwise inherits the season's Monitored flag; default
// to true when no matching season row exists in the series blob.
func getMonitoredStatus(ep *episodes.Episode, monitoredSeasons map[int]bool) bool {
	if ep.EpisodeNumber == 0 && ep.SeasonNumber != 1 {
		return false
	}
	if monitored, ok := monitoredSeasons[ep.SeasonNumber]; ok {
		return monitored
	}
	return true
}

// latestSeasonNumber returns the highest SeasonNumber from the series
// Seasons JSON blob, or -1 if the blob is missing/unparseable/empty.
// Mirrors C# `:50` `seasons.MaxBy(s => s.SeasonNumber)` with a safe
// sentinel for the empty case (the C# `latestSeason` is then
// dereferenced at :99 — but only inside the gated finale block, so an
// empty series can't reach that path; we still need a sentinel).
//
// J201 helper. Skipped season 0 is NOT excluded from the max — C#
// `latestSeason.SeasonNumber == 0` will also fail the `SeasonNumber > 0`
// gate at `:98`, so the early-return paths are equivalent.
func latestSeasonNumber(ser *series.Series) int {
	if ser == nil || ser.Seasons == "" {
		return -1
	}
	var parsed []seasonMonitor
	if err := json.Unmarshal([]byte(ser.Seasons), &parsed); err != nil {
		return -1
	}
	if len(parsed) == 0 {
		return -1
	}
	max := parsed[0].SeasonNumber
	for _, s := range parsed[1:] {
		if s.SeasonNumber > max {
			max = s.SeasonNumber
		}
	}
	return max
}

// countEpisodesPerSeason returns a map of seasonNumber → episode count
// from the supplied (already deduped) list. Mirrors C# `:49`
// `episodesPerSeason = orderedEpisodes.GroupBy(s => s.SeasonNumber)
//
//	.ToDictionary(g => g.Key, g => g.Count())`.
//
// J201 helper. Used in the finale heuristic to identify the last
// episode of the latest season.
func countEpisodesPerSeason(eps []episodes.Episode) map[int]int {
	out := make(map[int]int)
	for _, e := range eps {
		out[e.SeasonNumber]++
	}
	return out
}

// anyFinaleInSeason returns true when any episode in the supplied list
// matches the given season AND already carries a non-empty FinaleType.
// Mirrors C# `:104` `orderedEpisodes.None(e => e.SeasonNumber ==
// latestSeason.SeasonNumber && e.FinaleType != null)` (we return the
// inverse — "any" — for the maybeStampSeriesFinale guard).
//
// J201 helper. Pre-scanned once across the batch so the stamp decision
// is consistent regardless of iteration order.
func anyFinaleInSeason(eps []episodes.Episode, season int) bool {
	for _, e := range eps {
		if e.SeasonNumber == season && e.FinaleType != "" {
			return true
		}
	}
	return false
}

// maybeStampSeriesFinale ports the C# finale heuristic at
// `RefreshEpisodeService.cs:95-107`. TheTVDB has historically been
// inconsistent about flagging series finales; this back-stop stamps
// FinaleType="series" on the last episode of the last season of a
// recently-ended show when:
//
//   - Series.Status == SeriesStatusEnded (:96)
//   - episode.FinaleType is empty (:97)
//   - SeasonNumber > 0 — skip season 0/specials (:98)
//   - SeasonNumber == latest season (:99)
//   - EpisodeNumber > 1 — skip pilot-only-episode cases (:100)
//   - EpisodeNumber == episodesPerSeason[season] — last episode (:101)
//   - AirDateUtc within the last 14 days (:102-103)
//   - No other episode in the latest season already carries a FinaleType (:104)
//
// When all conditions hold, stamps FinaleType="series" on `ep`.
// Operates in-place — caller already has the episode pointer in
// updateList/newList.
//
// J201.
func maybeStampSeriesFinale(
	ep *episodes.Episode,
	ser *series.Series,
	latestSeason int,
	episodesPerSeason map[int]int,
	hasExistingFinaleInLatestSeason bool,
	now time.Time,
) {
	if ser == nil || ep == nil {
		return
	}
	if ser.Status != series.SeriesStatusEnded {
		return
	}
	if ep.FinaleType != "" {
		return
	}
	if ep.SeasonNumber <= 0 {
		return
	}
	if ep.SeasonNumber != latestSeason {
		return
	}
	if ep.EpisodeNumber <= 1 {
		return
	}
	if ep.EpisodeNumber != episodesPerSeason[ep.SeasonNumber] {
		return
	}
	if ep.AirDateUtc == nil {
		return
	}
	if !ep.AirDateUtc.After(now.AddDate(0, 0, -14)) {
		return
	}
	if hasExistingFinaleInLatestSeason {
		return
	}
	ep.FinaleType = "series"
}
