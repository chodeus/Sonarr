package pendingreleases

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"sonarr.tv/sonarr/internal/messaging"
)

// Service ports the C# `PendingReleaseService` at
// `src/NzbDrone.Core/Download/Pending/PendingReleaseService.cs:42-700`
// — the orchestration layer over the [Repo] CRUD wrapper. Slice 4.BQ
// (C-cleanup sub-arc C7a) ships the WRITE-SIDE surface that C# named
// `Add` / `AddMany`. The remaining surface (RemoveByQueueRequest /
// RemoveGrabbed / RemoveRejected / GetPendingQueue / OldestPending /
// IHandle event dispatchers) lands in sub-arc C7b once the
// customformats + RemoteEpisode rehydration deps unblock it.
//
// Package layering: this package is at the persistence tier — every
// type higher in the stack (parser, decisionengine) imports the
// PendingReleaseReason enum from here. To avoid a cycle, the Service
// takes a narrow [Input] shape carrying the three matching fields +
// three pre-serialized JSON blobs. Callers (boot wiring once the
// orchestrator drives Add/AddMany) extract those fields from a
// *decisionengine.Decision before calling.
//
// Divergence from C# AddMany (`:102-170`):
//
//   - The C# version pre-filters candidate dedup matches by
//     episode-id intersection (CreateEpisodeLookup at `:113`). The Go
//     port can't replay that filter cheaply because RemoteEpisode
//     rehydration (parsingservice.Map + customformats) is C12-gated;
//     episode-ids never reach the DB, only EpisodeNumbers do. So the
//     Go port widens the dedup scope to "every pending release for
//     the same Series.Id" and then applies the C# MatchingReleasePredicate
//     (Title + PublishDate + Indexer) which is the final discriminator.
//     Practical effect: same Title+PublishDate+Indexer release added
//     for a DIFFERENT episode set against the same Series collapses
//     into one row (vs C# which would keep them separate). Real-world
//     callers almost never produce that shape — a single (Title,
//     PublishDate, Indexer) tuple maps to one decision-engine pass —
//     so the divergence is negligible in practice.
//
//   - The C# version holds an in-memory `_pendingReleases` cache and
//     UpdatePendingReleases-refreshes it at the end of AddMany. The
//     Go port reads fresh from the DB each call; the cache is a
//     perf optimization that we'll add separately when a hot path
//     justifies it.
//
// Goroutine-safety: the Service is stateless (deps are goroutine-safe).
// Concurrent AddMany calls against overlapping series may both pass
// the dedup check and both insert — the dedup is best-effort, not
// transactional. Matches C# behavior (no advisory lock there either).
type Service struct {
	repo *Repo
	bus  *messaging.Bus
	log  *slog.Logger
}

// NewService binds the dependencies. repo is REQUIRED. bus may be
// nil — when nil, AddMany silently skips PendingReleasesUpdatedEvent
// publish (matches the pattern used by other services that pre-date
// the bus wire-up).
func NewService(repo *Repo, bus *messaging.Bus, log *slog.Logger) (*Service, error) {
	if repo == nil {
		return nil, errors.New("pendingreleases: NewService: nil Repo")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{repo: repo, bus: bus, log: log}, nil
}

// Input is the per-add payload [Service.Add] / [Service.AddMany]
// consume. The three Match* fields ARE the C# `MatchingReleasePredicate`
// triple (Title, PublishDate, Indexer); the three *JSON fields are
// the stored opaque blobs from the [Release] schema (ParsedEpisodeInfo,
// ReleaseInfo, AdditionalInfo). The caller is responsible for
// marshaling its decision-engine Subject + Release into the JSON
// strings before calling — this keeps pendingreleases off the
// decisionengine/parser import path (those packages import the
// PendingReleaseReason enum from here, so the back-reference would
// cycle).
//
// SeriesID + Match* fields go onto the indexed Release columns;
// every JSON blob is opaque to the matching code.
type Input struct {
	SeriesID     int
	MatchTitle   string
	MatchPubDate time.Time
	MatchIndexer string

	ParsedEpisodeInfoJSON string
	ReleaseInfoJSON       string
	AdditionalInfoJSON    string
}

// AddItem pairs an [Input] with the [Reason] tag the caller wants
// recorded. Mirrors the C# `Tuple<DownloadDecision, PendingReleaseReason>`
// at `:30`.
type AddItem struct {
	Input  Input
	Reason Reason
}

// Add wraps [Service.AddMany] for the single-input shape. Mirrors
// C# `Add` at `:97-100`.
func (s *Service) Add(ctx context.Context, in Input, reason Reason) error {
	return s.AddMany(ctx, []AddItem{{Input: in, Reason: reason}})
}

// AddMany persists a batch of [AddItem]s. Ports the C# `AddMany` at
// `:102-170` with the divergences documented on [Service].
//
// Behavior summary per item:
//
//  1. Skip items whose Input.SeriesID is 0 (the C# code NPEs at
//     `decision.RemoteEpisode.Series.Id`; the Go port logs + drops).
//  2. Look up every pending row for the item's SeriesID.
//  3. Filter that set by MatchingReleasePredicate (Title + PublishDate
//     + Indexer). If at least one row matches:
//     - If the matched row's Reason equals the new reason → log + skip.
//     - Else if the matched row's Reason is
//     [ReasonDownloadClientUnavailable] → preserve it (the C# rule
//     at `:133-136`: client-unavailable is sticky, don't downgrade
//     to Delay/Fallback).
//     - Else → Update the row's Reason in-place.
//     If more than one row matched, delete the surplus (the C#
//     dedup at `:149-159`).
//  4. Otherwise insert a fresh row carrying Title / Reason /
//     serialized ParsedEpisodeInfo + ReleaseInfo + AdditionalInfo.
//
// Publishes a single [PendingReleasesUpdatedEvent] at the end if any
// row was written. Empty batches are a no-op (no event).
//
// Error handling: any DB error propagates immediately — the batch
// halts on first failure (matches C# behavior where the underlying
// IRepository<T> throws). Earlier writes in the same batch are NOT
// rolled back; the C# code has the same property since it doesn't
// wrap AddMany in a transaction.
func (s *Service) AddMany(ctx context.Context, items []AddItem) error {
	if s == nil {
		return errors.New("pendingreleases: AddMany on nil receiver")
	}
	if len(items) == 0 {
		return nil
	}

	// Group by SeriesID, preserving first-seen group order.
	type grouped struct {
		seriesID int
		items    []AddItem
	}
	buckets := make(map[int]*grouped)
	var order []int
	for _, it := range items {
		if it.Input.SeriesID == 0 {
			s.log.Debug("pendingreleases: AddMany skipping item with zero SeriesID")
			continue
		}
		sid := it.Input.SeriesID
		b, ok := buckets[sid]
		if !ok {
			b = &grouped{seriesID: sid}
			buckets[sid] = b
			order = append(order, sid)
		}
		b.items = append(b.items, it)
	}

	var changed bool
	for _, sid := range order {
		existing, err := s.repo.AllBySeriesID(ctx, sid)
		if err != nil {
			return fmt.Errorf("pendingreleases: AddMany: AllBySeriesID(%d): %w", sid, err)
		}
		for _, it := range buckets[sid].items {
			wrote, next, err := s.applyOne(ctx, existing, it)
			if err != nil {
				return err
			}
			if wrote {
				changed = true
			}
			existing = next
		}
	}

	if changed && s.bus != nil {
		// Publish is fire-and-forget at this layer — handler errors
		// are logged by the bus, never propagated.
		_ = messaging.Publish(ctx, s.bus, PendingReleasesUpdatedEvent{})
	}
	return nil
}

// applyOne handles a single AddItem against the supplied "existing"
// snapshot of the series's pending rows. Returns (wrote, newExisting, err).
// newExisting is the post-write snapshot the next item in the batch
// will dedup against — keeps the loop O(n × per-series-rows) without
// re-querying the DB after each insert.
func (s *Service) applyOne(ctx context.Context, existing []Release, it AddItem) (bool, []Release, error) {
	matches := matchingRows(existing, it.Input)

	if len(matches) > 0 {
		return s.handleMatch(ctx, existing, matches, it)
	}

	row := inputToRelease(it.Input, it.Reason, time.Now().UTC())
	if _, err := s.repo.Insert(ctx, row); err != nil {
		return false, existing, fmt.Errorf("pendingreleases: AddMany: Insert: %w", err)
	}
	s.log.Debug("pendingreleases: inserted",
		"seriesId", row.SeriesID, "title", row.Title, "reason", it.Reason)
	return true, append(existing, *row), nil
}

// handleMatch ports the C# `:127-162` matching-reports branch.
func (s *Service) handleMatch(ctx context.Context, existing []Release, matches []int, it AddItem) (bool, []Release, error) {
	first := &existing[matches[0]]
	wrote := false
	newReason := int(it.Reason)

	switch {
	case first.Reason == newReason:
		s.log.Debug("pendingreleases: release already pending, not re-adding",
			"title", first.Title, "reason", it.Reason)
	case first.Reason == int(ReasonDownloadClientUnavailable):
		// Don't downgrade a client-unavailable defer (the C# `:133-136`
		// rule). Leave the row alone.
		s.log.Debug("pendingreleases: release already pending with DownloadClientUnavailable; preserving",
			"title", first.Title)
	default:
		s.log.Debug("pendingreleases: release reason transitioning",
			"title", first.Title, "from", first.Reason, "to", newReason)
		first.Reason = newReason
		if err := s.repo.Update(ctx, first); err != nil {
			return false, existing, fmt.Errorf("pendingreleases: AddMany: Update(%d): %w", first.ID, err)
		}
		wrote = true
	}

	if len(matches) > 1 {
		s.log.Debug("pendingreleases: deleting duplicate pending releases",
			"title", first.Title, "duplicates", len(matches)-1)
		// Walk surplus matches in REVERSE index order so each deletion
		// doesn't disturb earlier slice positions.
		dropIdx := append([]int(nil), matches[1:]...)
		for i := len(dropIdx) - 1; i >= 0; i-- {
			idx := dropIdx[i]
			if err := s.repo.Delete(ctx, existing[idx].ID); err != nil {
				return wrote, existing, fmt.Errorf("pendingreleases: AddMany: Delete(%d): %w", existing[idx].ID, err)
			}
			existing = append(existing[:idx], existing[idx+1:]...)
			wrote = true
		}
	}

	return wrote, existing, nil
}

// matchPayload captures the (Title, PublishDate, Indexer) triple
// dedup needs. Parsed out of each row's stored ReleaseInfo JSON
// blob — the field names mirror the [parser.ReleaseInfo] JSON tags so
// any caller that marshals via parser.ReleaseInfo's
// `json.Marshal(release)` slots in cleanly.
type matchPayload struct {
	Title       string    `json:"title"`
	PublishDate time.Time `json:"publishDate"`
	Indexer     string    `json:"indexer"`
}

// matchingRows returns the indices into `existing` whose stored
// ReleaseInfo matches the input on the C# `MatchingReleasePredicate`
// triple (Title + PublishDate + Indexer). The stored Title column is
// authoritative; the stored ReleaseInfo JSON supplies PublishDate +
// Indexer. Rows whose ReleaseInfo JSON fails to decode are silently
// skipped — the C# version's typed deserialization would have thrown;
// treating a malformed row as "no match" lets the orchestrator move
// on rather than poison the whole batch.
func matchingRows(existing []Release, in Input) []int {
	var out []int
	for i := range existing {
		if existing[i].Title != in.MatchTitle {
			continue
		}
		var stored matchPayload
		if err := json.Unmarshal([]byte(existing[i].ReleaseInfo), &stored); err != nil {
			continue
		}
		if stored.PublishDate.Equal(in.MatchPubDate) && stored.Indexer == in.MatchIndexer {
			out = append(out, i)
		}
	}
	return out
}

// inputToRelease builds the Release row for [Repo.Insert]. The
// SeriesID and Title come from the matching tuple; the three JSON
// blobs are passed through verbatim (the [Repo.Insert] mapper's
// defaultJSON helper substitutes `{}` for empty strings).
func inputToRelease(in Input, reason Reason, now time.Time) *Release {
	return &Release{
		SeriesID:          in.SeriesID,
		Title:             in.MatchTitle,
		Added:             now,
		ParsedEpisodeInfo: in.ParsedEpisodeInfoJSON,
		ReleaseInfo:       in.ReleaseInfoJSON,
		Reason:            int(reason),
		AdditionalInfo:    in.AdditionalInfoJSON,
	}
}
