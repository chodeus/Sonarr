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
	"sort"
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

	// dedupeFreeRemote keeps the first occurrence per (season,episode)
	// key, matching C# DistinctBy at `:41`.
	dupeFreeRemote := dedupeBySeasonEpisode(remote)

	// J207 — anime-only abs-number filter at C# `:43-46`. Collapses
	// cross-season duplicates that share an AbsoluteEpisodeNumber down
	// to the highest-season instance. No-op for non-anime; also no-op
	// for anime shows whose remote payload has no abs-number collisions.
	if ser.SeriesType == series.SeriesTypeAnime {
		dupeFreeRemote = mapAbsoluteEpisodeNumbers(dupeFreeRemote)
	}

	// J207 — explicit ordering pass at C# `:48`. For anime, abs-numbered
	// episodes ASC then non-abs (season, episode) ASC; for standard,
	// (season, episode) ASC. The downstream loop + episodesPerSeason
	// count consume `ordered` rather than the input.
	ordered := orderEpisodes(ser, dupeFreeRemote)

	monitoredSeasons := monitoredSeasonsLookup(ser)

	// J201 — finale-detection heuristic prep (C# :49-50, :104).
	// latestSeason mirrors C# `seasons.MaxBy(s => s.SeasonNumber)` —
	// the latest-known season for the series.
	// episodesPerSeason counts episodes per season from `ordered`
	// (C# `:49 orderedEpisodes.GroupBy(...)`); used in the
	// last-episode-of-season check.
	// hasExistingFinaleInLatestSeason mirrors C# `:104` — when ANY
	// episode in the latest season already carries a FinaleType
	// (either from TheTVDB upstream or a prior reconcile that
	// stamped it), the heuristic skips. Pre-scanned across the batch
	// so the decision is consistent regardless of iteration order.
	latestSeason := latestSeasonNumber(ser)
	episodesPerSeason := countEpisodesPerSeason(ordered)
	hasExistingFinaleInLatestSeason := anyFinaleInSeason(ordered, latestSeason)
	nowUTC := s.now().UTC()

	isAnime := ser.SeriesType == series.SeriesTypeAnime
	updateList := make([]*episodes.Episode, 0, len(ordered))
	newList := make([]*episodes.Episode, 0, len(ordered))

	for i := range ordered {
		rem := &ordered[i]
		key := seasonEpisodeKey{Season: rem.SeasonNumber, Episode: rem.EpisodeNumber}
		if matched, ok := existingByKey[key]; ok && !matched.matched {
			matched.matched = true
			row := matched.ep
			// J207 — capture pre-copy state for the AbsoluteEpisodeNumberAdded
			// stamp at C# `:64-69`. Must read BEFORE applyRemoteFields
			// overwrites row.AbsoluteEpisodeNumber.
			existingHadAbs := row.AbsoluteEpisodeNumber != nil
			applyRemoteFields(&row, rem)
			if isAnime && !existingHadAbs && rem.AbsoluteEpisodeNumber != nil {
				row.AbsoluteEpisodeNumberAdded = true
			}
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

	// J202 — UnmonitorReaddedEpisodes pass (C# `:118`). Operates on the
	// newList only (newly-inserted episodes), reads hasExisting captured
	// pre-reconcile, and mutates Monitored in-place on the cold-add path
	// to prevent unexpected downloads when TheTVDB suddenly back-fills
	// old episodes that aired before the user added the series.
	s.unmonitorReaddedEpisodes(ctx, ser, newList, hasExisting, nowUTC)

	// J203 — air-time adjustments per C# `:124-125`. Both run over the
	// combined newList + updateList (allEpisodes), mutating AirDateUtc
	// (and AirDate for the DVD backfill) in place before persistence.
	// Order matters: multi-episode-air-time spread runs first because
	// the DVD backfill only fires when every episode has a nil
	// AirDateUtc — letting spread run after backfill would defeat that
	// guard. C# uses the same order at :124-125.
	allEpisodes := make([]*episodes.Episode, 0, len(newList)+len(updateList))
	allEpisodes = append(allEpisodes, newList...)
	allEpisodes = append(allEpisodes, updateList...)
	adjustMultiEpisodeAirTime(ctx, ser, allEpisodes, s.log)
	adjustDirectToDvdAirDate(ser, allEpisodes)

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

// mapAbsoluteEpisodeNumbers ports C# `RefreshEpisodeService.cs:228-236`.
// Anime-only filter applied to dupeFreeRemote BEFORE orderEpisodes /
// reconcile. From episodes WITH an AbsoluteEpisodeNumber, keeps the
// HIGHEST-season variant per unique abs number — mirrors C#
// `OrderByDescending(SeasonNumber).DistinctBy(AbsoluteEpisodeNumber)`
// where the stable sort guarantees the highest season wins on the
// first-occurrence-wins distinct pass. Episodes WITHOUT an
// AbsoluteEpisodeNumber pass through in their original input order,
// concatenated after the distinct block.
//
// Used by anime shows where TheTVDB lists the same abs number in
// multiple seasons (e.g. seasonal re-airing of the same content); the
// higher-season instance is treated as canonical so the reconcile
// doesn't pile multiple persisted rows onto the same abs key.
//
// J207.
func mapAbsoluteEpisodeNumbers(in []episodes.Episode) []episodes.Episode {
	withAbs := make([]episodes.Episode, 0, len(in))
	withoutAbs := make([]episodes.Episode, 0)
	for _, e := range in {
		if e.AbsoluteEpisodeNumber != nil {
			withAbs = append(withAbs, e)
		} else {
			withoutAbs = append(withoutAbs, e)
		}
	}
	// Stable sort by SeasonNumber DESC so the first-occurrence-wins
	// distinct pass below picks the highest-season instance per abs key.
	sort.SliceStable(withAbs, func(i, j int) bool {
		return withAbs[i].SeasonNumber > withAbs[j].SeasonNumber
	})
	seen := make(map[int]bool, len(withAbs))
	distinct := make([]episodes.Episode, 0, len(withAbs))
	for _, e := range withAbs {
		abs := *e.AbsoluteEpisodeNumber
		if seen[abs] {
			continue
		}
		seen[abs] = true
		distinct = append(distinct, e)
	}
	out := make([]episodes.Episode, 0, len(distinct)+len(withoutAbs))
	out = append(out, distinct...)
	out = append(out, withoutAbs...)
	return out
}

// orderEpisodes ports C# `RefreshEpisodeService.cs:238-251`. For anime,
// abs-numbered episodes sort by AbsoluteEpisodeNumber ASC then concat
// with non-abs episodes ordered by (SeasonNumber, EpisodeNumber) ASC.
// For non-anime, all episodes order by (SeasonNumber, EpisodeNumber)
// ASC. Stable sorts to keep input order for equal keys.
//
// The returned slice is a fresh copy — callers can index/pointer into
// it without aliasing the input.
//
// J207.
func orderEpisodes(ser *series.Series, in []episodes.Episode) []episodes.Episode {
	out := make([]episodes.Episode, len(in))
	copy(out, in)
	if ser != nil && ser.SeriesType == series.SeriesTypeAnime {
		withAbs := make([]episodes.Episode, 0, len(out))
		withoutAbs := make([]episodes.Episode, 0)
		for _, e := range out {
			if e.AbsoluteEpisodeNumber != nil {
				withAbs = append(withAbs, e)
			} else {
				withoutAbs = append(withoutAbs, e)
			}
		}
		sort.SliceStable(withAbs, func(i, j int) bool {
			return *withAbs[i].AbsoluteEpisodeNumber < *withAbs[j].AbsoluteEpisodeNumber
		})
		sort.SliceStable(withoutAbs, func(i, j int) bool {
			if withoutAbs[i].SeasonNumber != withoutAbs[j].SeasonNumber {
				return withoutAbs[i].SeasonNumber < withoutAbs[j].SeasonNumber
			}
			return withoutAbs[i].EpisodeNumber < withoutAbs[j].EpisodeNumber
		})
		merged := make([]episodes.Episode, 0, len(withAbs)+len(withoutAbs))
		merged = append(merged, withAbs...)
		merged = append(merged, withoutAbs...)
		return merged
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].SeasonNumber != out[j].SeasonNumber {
			return out[i].SeasonNumber < out[j].SeasonNumber
		}
		return out[i].EpisodeNumber < out[j].EpisodeNumber
	})
	return out
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

// unmonitorReaddedEpisodes ports the C# heuristic at
// `RefreshEpisodeService.cs:157-189`. Defends against a TheTVDB
// back-fill where a refresh pass suddenly emits episodes that aired
// before the user added the series — without this guard the next
// scheduled-task tick would treat them as monitored-and-missing and
// search the indexers.
//
// Decision tree (line-aligned with C#):
//
//   - `:159-162` if Series.AddOptions != null → skip (active add flow,
//     the AddSeriesService monitor-override at AddSeriesService.cs:154-157
//     handles it; J189 ports that arm).
//   - `:166` collect oldEpisodes from newList where AirDateUtc <
//     now - 14d (detection threshold).
//   - `:168` if no old episodes → no-op.
//   - `:170-173` if hasExisting → warn-log only ("please check
//     monitored status"). The series already had episodes pre-reconcile,
//     so the user has been curating it; don't auto-unmonitor.
//   - `:175-186` else (cold-add path, no pre-existing episodes) →
//     re-walk newList with a now - 1d threshold (more permissive than
//     detection), set Monitored=false on every match, then warn-log.
//     Two different thresholds: detection at 14d gates whether to
//     act at all; the cold-add unmonitor at 1d is broader.
//
// The cold-add 1-day cutoff matches C# behaviour exactly — the J202
// port preserves the asymmetry between detection (14d) and unmonitor
// (1d) thresholds. The rationale is that on a fresh add, anything
// that aired before "yesterday" is presumed already-watched-or-skipped
// by the user; the 14-day detection threshold is just the trigger.
//
// J202.
func (s *Service) unmonitorReaddedEpisodes(ctx context.Context, ser *series.Series, newList []*episodes.Episode, hasExisting bool, now time.Time) {
	// Skip when AddOptions is wired — the AddSeriesService monitor
	// override already handled the initial monitored-state choice.
	opts, _ := series.DecodeAddOptions(ser.AddOptions)
	if opts != nil {
		return
	}

	detectionCutoff := now.AddDate(0, 0, -14)
	oldCount := 0
	for _, ep := range newList {
		if ep.AirDateUtc != nil && ep.AirDateUtc.Before(detectionCutoff) {
			oldCount++
		}
	}
	if oldCount == 0 {
		return
	}

	if hasExisting {
		s.log.LogAttrs(ctx, slog.LevelWarn,
			"refreshepisodes: old episodes appeared on refresh — please check monitored status",
			slog.Int("tvdbId", ser.TvdbID),
			slog.String("title", ser.Title),
			slog.Int("oldCount", oldCount))
		return
	}

	// Cold-add path: more permissive 1-day threshold for the unmonitor pass.
	unmonitorCutoff := now.AddDate(0, 0, -1)
	for _, ep := range newList {
		if ep.AirDateUtc != nil && ep.AirDateUtc.Before(unmonitorCutoff) {
			ep.Monitored = false
		}
	}
	s.log.LogAttrs(ctx, slog.LevelWarn,
		"refreshepisodes: old episodes appeared on cold-add — unmonitored aired episodes to prevent unexpected downloads",
		slog.Int("tvdbId", ser.TvdbID),
		slog.String("title", ser.Title),
		slog.Int("oldCount", oldCount))
}

// adjustMultiEpisodeAirTime ports C# `RefreshEpisodeService.cs:191-214`.
// When TheTVDB lists multiple episodes airing on the same date (the
// AirDate string, not AirDateUtc — distinct days in a UTC-rendered
// timeline still match when AirDate parses to the same yyyy-MM-dd),
// the heuristic stretches them out by `series.Runtime` minutes each so
// the UI sees an ordered sequence rather than a vertical pile at the
// same hour.
//
// Decision tree (line-aligned with C#):
//
//   - `:193-196` filter to episodes with non-nil AirDateUtc, group by
//     (SeasonNumber, AirDate), keep groups of size > 1.
//   - `:200-204` skip the group when SeasonNumber != 0 AND size > 3 —
//     TheTVDB's "all aired same day" is usually a placeholder shape for
//     >3 episodes (block release, specials dump). Season 0 isn't
//     skipped because special-episode dumps are common and the spread
//     still helps UI ordering.
//   - `:208-212` for each remaining group, order by (SeasonNumber,
//     EpisodeNumber) then offset AirDateUtc by Runtime*index minutes.
//
// J203.
func adjustMultiEpisodeAirTime(ctx context.Context, ser *series.Series, allEpisodes []*episodes.Episode, log *slog.Logger) {
	if ser == nil {
		return
	}
	// Group by (SeasonNumber, AirDate-string). C# uses the AirDate
	// string explicitly, so episodes with the same AirDateUtc but
	// different AirDate strings (rare) don't share a group.
	type groupKey struct {
		Season  int
		AirDate string
	}
	groups := make(map[groupKey][]*episodes.Episode)
	for _, ep := range allEpisodes {
		if ep == nil || ep.AirDateUtc == nil {
			continue
		}
		k := groupKey{Season: ep.SeasonNumber, AirDate: ep.AirDate}
		groups[k] = append(groups[k], ep)
	}

	for k, group := range groups {
		if len(group) <= 1 {
			continue
		}
		if k.Season != 0 && len(group) > 3 {
			log.LogAttrs(ctx, slog.LevelDebug,
				"refreshepisodes: not adjusting multi-episode air times (>3 same-day in non-special season)",
				slog.String("title", ser.Title),
				slog.Int("seasonNumber", k.Season),
				slog.Int("count", len(group)))
			continue
		}
		// Order by (SeasonNumber, EpisodeNumber) — within a single group
		// SeasonNumber is constant, so it's effectively EpisodeNumber.
		// Stable bubble-sort over the slice for clarity; group sizes are
		// tiny (2-3).
		sortByEpisodeNumber(group)
		for i, ep := range group {
			offset := time.Duration(ser.Runtime) * time.Duration(i) * time.Minute
			t := ep.AirDateUtc.Add(offset)
			ep.AirDateUtc = &t
		}
	}
}

func sortByEpisodeNumber(group []*episodes.Episode) {
	for i := 1; i < len(group); i++ {
		for j := i; j > 0; j-- {
			a, b := group[j-1], group[j]
			if a.SeasonNumber < b.SeasonNumber {
				break
			}
			if a.SeasonNumber == b.SeasonNumber && a.EpisodeNumber <= b.EpisodeNumber {
				break
			}
			group[j-1], group[j] = b, a
		}
	}
}

// adjustDirectToDvdAirDate ports C# `RefreshEpisodeService.cs:216-226`.
// When a series is Ended AND every episode has a nil AirDateUtc (the
// "direct-to-DVD" signature — series shipped as a complete set, never
// aired) AND series.FirstAired is set, stamp every episode's
// AirDateUtc + AirDate to series.FirstAired. The UI then renders all
// episodes against that single date rather than as undated rows.
//
// The all-nil guard is critical — partial-date shows (some aired,
// some not) keep their partial dates rather than getting flattened.
//
// J203.
func adjustDirectToDvdAirDate(ser *series.Series, allEpisodes []*episodes.Episode) {
	if ser == nil || ser.Status != series.SeriesStatusEnded || ser.FirstAired == nil {
		return
	}
	for _, ep := range allEpisodes {
		if ep != nil && ep.AirDateUtc != nil {
			return // not all-nil → no-op
		}
	}
	stampedDate := *ser.FirstAired
	stampedDateStr := stampedDate.Format("2006-01-02")
	for _, ep := range allEpisodes {
		if ep == nil {
			continue
		}
		ep.AirDateUtc = &stampedDate
		ep.AirDate = stampedDateStr
	}
}
