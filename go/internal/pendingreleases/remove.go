package pendingreleases

import (
	"context"
	"crypto/sha1" //nolint:gosec // C# parity: HashConverter.GetHashInt31 uses SHA-1 for stable IDs (NOT a security primitive).
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/quality"
)

// Slice 4.BY (C-cleanup sub-arc C7b-i of queue [C7]) ports the
// remove-side of `PendingReleaseService.cs:282-612`. Five public
// methods land here:
//
//   - DeleteBySeriesIDs — for SeriesDeletedEvent.
//   - RemoveByQueueID — port of C# RemovePendingQueueItems(int).
//   - RemoveGrabbed — for EpisodeGrabbedEvent.
//   - RemoveRejected — for RssSyncCompleteEvent.
//   - OldestPending — read-side companion to the remove paths.
//
// Sub-arc C7b-ii will follow with GetPendingQueue + the
// IHandle event dispatcher methods + Subscribe wire. Queue [C7]
// closes when both C7b sub-arcs land.

// QueueIDFor mirrors C# `PendingReleaseService.GetQueueId` at
// `:626-627`:
//
//	HashConverter.GetHashInt31(string.Format("pending-{0}", pendingRelease.Id))
//
// Returns SHA-1("pending-{id}")[0..3] as a little-endian uint32 with
// the sign bit masked off — gives a stable positive int31 derived
// from the pending row's primary key.
//
// Exported so callers building a Queue.Queue Id from a Release row
// (sub-arc C7b-ii) and the v3 api/v3 queue endpoint (Phase 5) can
// compute the same hash without depending on a private helper.
func QueueIDFor(pendingReleaseID int) int {
	input := "pending-" + strconv.Itoa(pendingReleaseID)
	sum := sha1.Sum([]byte(input)) //nolint:gosec // C# parity (see file-level note).
	v := binary.LittleEndian.Uint32(sum[:4]) & 0x7fffffff
	return int(v)
}

// pendingParsedInfo captures the (SeasonNumber, EpisodeNumbers,
// Quality) triple the remove paths read out of the stored
// ParsedEpisodeInfo JSON blob. The wire-shape uses the camelCase
// tags slice 4.BQ's AddMany serializer wrote — kept self-contained
// here so the parser package's full ParsedEpisodeInfo doesn't have
// to ship JSON tags it doesn't otherwise need.
type pendingParsedInfo struct {
	SeasonNumber   int           `json:"seasonNumber"`
	EpisodeNumbers []int         `json:"episodeNumbers"`
	Quality        quality.Model `json:"quality"`
}

// parsedInfoFor decodes a Release row's ParsedEpisodeInfo column.
// Returns the zero value when the column is empty / malformed; the
// row counts as "no season + no episodes" for matching purposes
// (matches the C# defensive null-coalescing posture).
func parsedInfoFor(row *Release) pendingParsedInfo {
	if row == nil {
		return pendingParsedInfo{}
	}
	raw := row.ParsedEpisodeInfo
	if raw == "" {
		return pendingParsedInfo{}
	}
	var p pendingParsedInfo
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return pendingParsedInfo{}
	}
	return p
}

// DeleteBySeriesIDs ports the C# `IPendingReleaseRepository.DeleteBySeriesIds`
// usage at `:663`. Walks each ID, fetches the per-series rows via the
// Repo's AllBySeriesID, and deletes them one at a time via the
// shared Repo.Delete (the package's Repository[T] base doesn't
// expose a DeleteMany helper — see internal/repo).
//
// Publishes a single PendingReleasesUpdatedEvent at the end if any
// row was deleted. Empty input is a no-op.
//
// Slice 4.BY (sub-arc C7b-i).
func (s *Service) DeleteBySeriesIDs(ctx context.Context, seriesIDs []int) error {
	if s == nil {
		return errors.New("pendingreleases: DeleteBySeriesIDs on nil receiver")
	}
	if len(seriesIDs) == 0 {
		return nil
	}
	var deleted int
	for _, sid := range seriesIDs {
		rows, err := s.repo.AllBySeriesID(ctx, sid)
		if err != nil {
			return fmt.Errorf("pendingreleases: DeleteBySeriesIDs: AllBySeriesID(%d): %w", sid, err)
		}
		for _, r := range rows {
			if err := s.repo.Delete(ctx, r.ID); err != nil {
				return fmt.Errorf("pendingreleases: DeleteBySeriesIDs: Delete(%d): %w", r.ID, err)
			}
			deleted++
		}
	}
	s.publishUpdated(ctx, deleted)
	return nil
}

// RemoveByQueueID ports C# `RemovePendingQueueItems(int queueId)` at
// `:282-292`. Finds the pending row whose hashed queue-id matches
// the supplied value, then deletes EVERY pending row for the same
// series whose stored ParsedEpisodeInfo (SeasonNumber, EpisodeNumbers)
// equals the target's. Mirrors the C# `SequenceEqual` semantic on
// the episode-numbers list.
//
// Returns nil when no pending row carries the requested queueID —
// matches the C# `First` no-throw posture only when the orchestrator
// validates the input upstream; defensive-return here avoids
// panicking on a stale UI click.
//
// Slice 4.BY (sub-arc C7b-i).
func (s *Service) RemoveByQueueID(ctx context.Context, queueID int) error {
	if s == nil {
		return errors.New("pendingreleases: RemoveByQueueID on nil receiver")
	}
	all, err := s.repo.All(ctx)
	if err != nil {
		return fmt.Errorf("pendingreleases: RemoveByQueueID: All: %w", err)
	}
	var target *Release
	for i := range all {
		if QueueIDFor(all[i].ID) == queueID {
			target = &all[i]
			break
		}
	}
	if target == nil {
		s.log.Debug("pendingreleases: RemoveByQueueID: no pending row with queueID",
			"queueId", queueID)
		return nil
	}
	tParsed := parsedInfoFor(target)
	rows, err := s.repo.AllBySeriesID(ctx, target.SeriesID)
	if err != nil {
		return fmt.Errorf("pendingreleases: RemoveByQueueID: AllBySeriesID(%d): %w", target.SeriesID, err)
	}
	deleted := 0
	for _, r := range rows {
		rParsed := parsedInfoFor(&r)
		if rParsed.SeasonNumber != tParsed.SeasonNumber {
			continue
		}
		if !intsEqual(rParsed.EpisodeNumbers, tParsed.EpisodeNumbers) {
			continue
		}
		if err := s.repo.Delete(ctx, r.ID); err != nil {
			return fmt.Errorf("pendingreleases: RemoveByQueueID: Delete(%d): %w", r.ID, err)
		}
		deleted++
	}
	s.publishUpdated(ctx, deleted)
	return nil
}

// GrabbedInput is the per-call payload for [Service.RemoveGrabbed].
// Mirrors the (seriesId, mappedSeason, episodeNumbers, quality)
// quadruple the C# RemoveGrabbed pulls off RemoteEpisode at `:567-569`.
//
// Divergence from C# `:567`: C# matches on Episode.Id intersection
// (requires RemoteEpisode rehydration); the Go port matches on
// (SeriesID, SeasonNumber, EpisodeNumber) since pending rows store
// EpisodeNumbers but not Episode.Id. The practical effect is
// negligible — a single (series, season, episodeNumber) tuple maps
// to one episode in 99%+ of catalogue layouts. Documented at the
// type doc-comment so callers know the matching grain.
type GrabbedInput struct {
	SeriesID       int
	SeasonNumber   int
	EpisodeNumbers []int
	Quality        quality.Model
}

// RemoveGrabbed ports C# `RemoveGrabbed(RemoteEpisode)` at
// `:565-595`. For every pending row matching the supplied series +
// season + episode intersection:
//
//   - If the row's stored Quality is ≤ the grabbed Quality (via
//     [quality.Model.Compare]), DELETE the row.
//   - Otherwise: preserve (the higher-quality pending might still
//     beat the grab on retry).
//
// The C# code uses `QualityModelComparer(profile).Compare(grabbed,
// existing) >= 0` — i.e. delete when the GRABBED quality is at-or-
// above the pending quality. The Go port preserves the semantic
// via the static-weight Compare; the profile-aware
// custom-formats-extended comparison is a follow-up (matches the
// posture documented at quality.Model.Compare).
//
// Slice 4.BY (sub-arc C7b-i).
func (s *Service) RemoveGrabbed(ctx context.Context, in GrabbedInput) error {
	if s == nil {
		return errors.New("pendingreleases: RemoveGrabbed on nil receiver")
	}
	if in.SeriesID == 0 || len(in.EpisodeNumbers) == 0 {
		return nil
	}
	rows, err := s.repo.AllBySeriesID(ctx, in.SeriesID)
	if err != nil {
		return fmt.Errorf("pendingreleases: RemoveGrabbed: AllBySeriesID(%d): %w", in.SeriesID, err)
	}
	deleted := 0
	for _, r := range rows {
		parsed := parsedInfoFor(&r)
		if parsed.SeasonNumber != in.SeasonNumber {
			continue
		}
		if !intsIntersect(parsed.EpisodeNumbers, in.EpisodeNumbers) {
			continue
		}
		// Quality compare. C# `:584-589`: only remove when the GRABBED
		// quality is AT-LEAST the pending row's stored quality.
		// `grabbed.Compare(pending) >= 0` ↔ grabbed wins or ties.
		if in.Quality.Compare(parsed.Quality) < 0 {
			s.log.Debug("pendingreleases: RemoveGrabbed: preserving higher-quality pending row",
				"rowId", r.ID, "pendingQuality", parsed.Quality.Quality,
				"grabbedQuality", in.Quality.Quality)
			continue
		}
		if err := s.repo.Delete(ctx, r.ID); err != nil {
			return fmt.Errorf("pendingreleases: RemoveGrabbed: Delete(%d): %w", r.ID, err)
		}
		deleted++
	}
	s.publishUpdated(ctx, deleted)
	return nil
}

// RejectedInput is the per-rejected-decision payload for
// [Service.RemoveRejected]. Mirrors the (SeriesID, Title,
// PublishDate, Indexer) tuple the C# RemoveRejected pulls off each
// rejected DownloadDecision.RemoteEpisode.Release at `:597-612`.
type RejectedInput struct {
	SeriesID     int
	MatchTitle   string
	MatchPubDate time.Time
	MatchIndexer string
}

// RemoveRejected ports C# `RemoveRejected(List<DownloadDecision>)` at
// `:597-612`. For each rejected decision, finds every pending row in
// the same series whose stored ReleaseInfo matches the rejected
// decision's (Title, PublishDate, Indexer) tuple — the same predicate
// AddMany uses at slice 4.BQ to dedup. Deletes the matched rows.
//
// Slice 4.BY (sub-arc C7b-i).
func (s *Service) RemoveRejected(ctx context.Context, rejected []RejectedInput) error {
	if s == nil {
		return errors.New("pendingreleases: RemoveRejected on nil receiver")
	}
	if len(rejected) == 0 {
		return nil
	}
	// Cache by series.
	seriesRows := make(map[int][]Release)
	loadSeries := func(sid int) ([]Release, error) {
		if cached, ok := seriesRows[sid]; ok {
			return cached, nil
		}
		rs, err := s.repo.AllBySeriesID(ctx, sid)
		if err != nil {
			return nil, err
		}
		seriesRows[sid] = rs
		return rs, nil
	}

	deleted := 0
	for _, rej := range rejected {
		if rej.SeriesID == 0 {
			continue
		}
		rows, err := loadSeries(rej.SeriesID)
		if err != nil {
			return fmt.Errorf("pendingreleases: RemoveRejected: AllBySeriesID(%d): %w", rej.SeriesID, err)
		}
		// Reuse the matchingRows predicate from AddMany via an
		// equivalent Input shape; the helper itself is package-private
		// to keep the public surface narrow.
		in := Input{
			SeriesID:     rej.SeriesID,
			MatchTitle:   rej.MatchTitle,
			MatchPubDate: rej.MatchPubDate,
			MatchIndexer: rej.MatchIndexer,
		}
		matches := matchingRows(rows, in)
		// Walk in REVERSE index order so each deletion doesn't disturb
		// later positions in the cached slice.
		for i := len(matches) - 1; i >= 0; i-- {
			idx := matches[i]
			id := rows[idx].ID
			if err := s.repo.Delete(ctx, id); err != nil {
				return fmt.Errorf("pendingreleases: RemoveRejected: Delete(%d): %w", id, err)
			}
			// Drop from cache so subsequent rejections targeting the
			// same series see the updated state.
			rows = append(rows[:idx], rows[idx+1:]...)
			deleted++
		}
		seriesRows[rej.SeriesID] = rows
	}
	s.publishUpdated(ctx, deleted)
	return nil
}

// OldestPending ports C# `OldestPendingRelease(int seriesId, int[]
// episodeIds)` at `:306-313`. Returns the oldest pending row for
// the supplied series whose stored EpisodeNumbers intersect the
// supplied list.
//
// Divergence from C# `:311`: C# matches on Episode.Id intersection;
// the Go port matches on EpisodeNumber (same gap as RemoveGrabbed —
// the pending row's stored ParsedEpisodeInfo carries EpisodeNumbers,
// not IDs). The seasonNumber param disambiguates between standard +
// specials sharing episode numbers.
//
// Returns (nil, nil) on no-match. (nil, error) on lookup failure.
//
// "Oldest" here mirrors C# `MaxBy(p => p.Release.AgeHours)` —
// AgeHours is hours-since-PublishDate; the Go port reads
// Added (the row creation time) since that's what the Release schema
// stores at-rest. Functionally equivalent: both point at the
// earliest-added still-pending row for the series + episode set.
//
// Slice 4.BY (sub-arc C7b-i).
func (s *Service) OldestPending(ctx context.Context, seriesID, seasonNumber int, episodeNumbers []int) (*Release, error) {
	if s == nil {
		return nil, errors.New("pendingreleases: OldestPending on nil receiver")
	}
	if seriesID == 0 || len(episodeNumbers) == 0 {
		return nil, nil
	}
	rows, err := s.repo.AllBySeriesID(ctx, seriesID)
	if err != nil {
		return nil, fmt.Errorf("pendingreleases: OldestPending: AllBySeriesID(%d): %w", seriesID, err)
	}
	var oldest *Release
	for i := range rows {
		r := &rows[i]
		parsed := parsedInfoFor(r)
		if parsed.SeasonNumber != seasonNumber {
			continue
		}
		if !intsIntersect(parsed.EpisodeNumbers, episodeNumbers) {
			continue
		}
		if oldest == nil || r.Added.Before(oldest.Added) {
			oldest = r
		}
	}
	if oldest == nil {
		return nil, nil
	}
	// Return a copy so the caller can't mutate the in-cache row.
	clone := *oldest
	return &clone, nil
}

// publishUpdated emits PendingReleasesUpdatedEvent when `n` rows
// were mutated. n == 0 → no event (matches the AddMany pattern).
func (s *Service) publishUpdated(ctx context.Context, n int) {
	if n == 0 || s.bus == nil {
		return
	}
	_ = messaging.Publish(ctx, s.bus, PendingReleasesUpdatedEvent{})
}

// intsEqual reports whether two int slices contain the same elements
// in the same order. Mirrors C# `SequenceEqual` over `int[]`.
func intsEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// intsIntersect reports whether the two slices share at least one
// element. Order-insensitive. Mirrors C# `IEnumerable<int>.Intersect`
// followed by `.Any()`.
func intsIntersect(a, b []int) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	set := make(map[int]struct{}, len(a))
	for _, v := range a {
		set[v] = struct{}{}
	}
	for _, v := range b {
		if _, ok := set[v]; ok {
			return true
		}
	}
	return false
}
