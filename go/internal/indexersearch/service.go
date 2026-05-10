// Package indexersearch ... (doc continued from criteria.go).
//
// Service body — the fan-out search orchestrator. C# cite:
// src/NzbDrone.Core/IndexerSearch/ReleaseSearchService.cs:28-573.
//
// What this slice (queue [7], sub-arc 7a) ships:
//
//   - The Indexer narrow interface + Adapter (indexer.go).
//   - The Service struct + NewService.
//   - Eligible — filter the enabled-indexer list by the criteria's
//     interactive-vs-automatic flag and tag-overlap with the series.
//     C# cite: ReleaseSearchService.Dispatch :522-527.
//   - Dispatch — concurrent fan-out of a typed SearchAction across the
//     eligible indexers, aggregate the released slices, swallow
//     per-indexer errors after logging + recording. C# cite:
//     ReleaseSearchService.Dispatch :520-549 + DispatchIndexer :552-564.
//   - FetchRSS — the RssSync feeder: dispatch FetchRecent across every
//     EnableRSS indexer. C# cite: the IndexerService.RssEnabled() loop
//     consumed by RssSyncService at queue [9].
//   - indexerstatus.Recorder integration: RecordSuccess on a
//     per-indexer success, RecordFailure on a per-indexer error.
//
// What's deferred to follow-on sub-arcs (queue [7b]/[7c]/[7d]):
//
//   - 7b: ISearchForReleases entry points (EpisodeSearch /
//     SeasonSearch / scene-mapping flow).
//   - 7c: decisionengine wire-through — convert raw releases into
//     Decisions, dedup-by-Guid via the C# DeDupeDecisions semantics.
//   - 7d: cmd/sonarr wiring threading the three concrete indexers
//     (newznab / torznab / torrentrss) through the IndexerSource.
package indexersearch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"sonarr.tv/sonarr/internal/providerstatus"
)

// IndexerSource is the per-call snapshot of currently-enabled indexers
// the Service consults at dispatch time. The wiring-layer implementation
// (queue [7d]) hydrates rows from indexers.Repo, matches each row's
// Implementation against the per-family runtime constructor, and packs
// each into an Adapter.
//
// `Enabled` returns the union of all enabled indexers — RSS-only,
// search-only, and search+RSS. The Service filters further per-call
// via Eligible/FetchRSS/Dispatch since the criteria-vs-indexer
// eligibility check is criteria-specific.
//
// Why an interface, not a snapshot list passed to NewService:
//
// Indexer membership changes at runtime (user adds/removes/disables
// rows via the API/v3); the Service must re-query each dispatch so a
// freshly-disabled indexer doesn't get a stale entry-in-the-fleet.
// The C# tree achieves this via DI of IIndexerFactory (a per-request
// scope); the Go port models it as a small interface the wiring layer
// implements once.
type IndexerSource interface {
	Enabled(ctx context.Context) ([]Indexer, error)
}

// SearchAction is the per-indexer action Dispatch invokes. Mirrors
// the C# `Func<IIndexer, Task<IList<ReleaseInfo>>>` shape at
// ReleaseSearchService.cs:520. Concrete usage at this layer:
//
//	action := func(ctx context.Context, idx indexersearch.Indexer) ([]any, error) {
//	    return idx.FetchSearch(ctx, searchCriteria)
//	}
//
// queue [7b] will hide this behind the typed EpisodeSearch /
// SeasonSearch entry points so callers don't construct SearchAction
// values directly.
type SearchAction func(ctx context.Context, idx Indexer) ([]any, error)

// Service is the fan-out search orchestrator. Goroutine-safe — the
// per-call source.Enabled snapshot makes membership changes visible
// without internal mutation, and the Recorder is itself safe for
// concurrent calls.
//
// `recorder` is optional; nil disables status-recording (useful in
// tests and during early integration before queue [3]'s recorder
// wires through). C# faithfulness: the C# tree always has a
// non-null IIndexerStatusService since DI registers it singleton, but
// the Go port allows nil to keep the orchestrator decoupled.
type Service struct {
	source   IndexerSource
	recorder providerstatus.Recorder
	log      *slog.Logger
}

// NewService binds the source + recorder + logger. log defaults to
// slog.Default when nil. recorder may be nil to skip status-recording
// entirely.
func NewService(source IndexerSource, recorder providerstatus.Recorder, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{source: source, recorder: recorder, log: log}
}

// Eligible filters the enabled-indexer list by the criteria. Two
// gates apply:
//
//   - Interactive-vs-automatic: when criteria.InteractiveSearch is
//     set, only EnableInteractiveSearch indexers are eligible;
//     otherwise EnableAutomaticSearch wins. Mirrors the C# selector at
//     ReleaseSearchService.cs:522-524.
//   - Tag intersection: an indexer with no tags is eligible for any
//     series; otherwise its tag list must intersect the series'.
//     Mirrors :527.
//
// Returns the filtered slice in the same order source.Enabled returned
// — preserving order matters for log-line readability and dedup ties.
func (s *Service) Eligible(ctx context.Context, criteria CriteriaBase) ([]Indexer, error) {
	all, err := s.source.Enabled(ctx)
	if err != nil {
		return nil, fmt.Errorf("indexersearch: list enabled indexers: %w", err)
	}
	out := make([]Indexer, 0, len(all))
	seriesTags := seriesTagSet(criteria)
	for _, idx := range all {
		if idx == nil {
			continue
		}
		if criteria.InteractiveSearch {
			if !idx.EnableInteractiveSearch() {
				continue
			}
		} else {
			if !idx.EnableAutomaticSearch() {
				continue
			}
		}
		if !tagsMatch(idx.Tags(), seriesTags) {
			continue
		}
		out = append(out, idx)
	}
	return out, nil
}

// Dispatch fans `action` across the eligible indexers concurrently
// and aggregates the released slices. Mirrors
// ReleaseSearchService.Dispatch :520-549 minus the
// MakeDownloadDecision call (queue [7c]) and the
// last-search-time update (queue [7b]).
//
// Per-indexer error policy mirrors DispatchIndexer :552-564:
//   - The error is logged with the criteria for context.
//   - That indexer contributes the empty slice to the aggregate.
//   - Other indexers continue.
//
// Per-indexer status recording (the C# tree's IndexerStatusService
// integration that lives one layer below in HttpIndexerBase, hoisted
// here for the Go port's narrower indexer interface):
//   - On success → recorder.RecordSuccess(idx.ID).
//   - On error   → recorder.RecordFailure(idx.ID, 0).
//
// recorder errors themselves are logged but do not fail Dispatch —
// status-recording is best-effort and must never block the search
// pipeline. Matches C# IIndexerStatusService usage where the
// `RecordFailure` path catches its own DB exceptions.
func (s *Service) Dispatch(ctx context.Context, action SearchAction, criteria CriteriaBase) ([]any, error) {
	if action == nil {
		return nil, errors.New("indexersearch: nil SearchAction")
	}
	eligible, err := s.Eligible(ctx, criteria)
	if err != nil {
		return nil, err
	}
	s.log.Info("indexersearch: dispatching",
		slog.Int("indexerCount", len(eligible)),
		slog.String("criteria", criteriaLabel(criteria)),
	)
	if len(eligible) == 0 {
		return nil, nil
	}
	return s.fanOut(ctx, eligible, func(ctx context.Context, idx Indexer) ([]any, error) {
		return action(ctx, idx)
	}), nil
}

// FetchRSS fans FetchRecent across every EnableRSS indexer the source
// returns. Used by the RssSync job at queue [9]. Returns the
// aggregated release slice; per-indexer errors are logged + recorded
// the same as Dispatch.
//
// C# cite: this is the "for each enabled-rss indexer, FetchRecent"
// shape RssSyncService.cs:Execute consumes — moved here so the
// orchestrator owns the fan-out and RssSync just feeds the result
// through the decisionengine pipeline (queue [9]).
func (s *Service) FetchRSS(ctx context.Context) ([]any, error) {
	all, err := s.source.Enabled(ctx)
	if err != nil {
		return nil, fmt.Errorf("indexersearch: list enabled indexers: %w", err)
	}
	rssIndexers := make([]Indexer, 0, len(all))
	for _, idx := range all {
		if idx == nil || !idx.EnableRSS() {
			continue
		}
		rssIndexers = append(rssIndexers, idx)
	}
	s.log.Info("indexersearch: RSS sync dispatching", slog.Int("indexerCount", len(rssIndexers)))
	if len(rssIndexers) == 0 {
		return nil, nil
	}
	return s.fanOut(ctx, rssIndexers, func(ctx context.Context, idx Indexer) ([]any, error) {
		return idx.FetchRecent(ctx)
	}), nil
}

// fanOut runs `op` against each indexer concurrently, recording
// success / failure via the optional recorder, logging errors, and
// returning the aggregated release slice in indexer-iteration order.
//
// Concurrent shape: one goroutine per indexer, results collected via
// pre-allocated indexed slices so the aggregation order is
// deterministic (mirrors C# Task.WhenAll + SelectMany at
// ReleaseSearchService.cs:531-535 — Task.WhenAll preserves input
// order in the result array).
//
// Cancellation: passes ctx through; goroutine errors caused by ctx
// cancellation do trigger a RecordFailure call (the indexer
// genuinely failed to deliver, even if the cause was upstream). The
// log line tags ctx.Err so the operator can disambiguate.
func (s *Service) fanOut(ctx context.Context, indexers []Indexer, op func(ctx context.Context, idx Indexer) ([]any, error)) []any {
	results := make([][]any, len(indexers))
	var wg sync.WaitGroup
	wg.Add(len(indexers))
	for i, idx := range indexers {
		i, idx := i, idx
		go func() {
			defer wg.Done()
			rs, err := op(ctx, idx)
			if err != nil {
				s.log.Error("indexersearch: per-indexer fetch failed",
					slog.String("indexer", idx.Name()),
					slog.Int("indexerId", idx.ID()),
					slog.Any("err", err),
				)
				s.recordFailure(ctx, idx.ID(), idx.Name())
				return
			}
			results[i] = rs
			s.recordSuccess(ctx, idx.ID(), idx.Name())
		}()
	}
	wg.Wait()
	total := 0
	for _, r := range results {
		total += len(r)
	}
	agg := make([]any, 0, total)
	for _, r := range results {
		agg = append(agg, r...)
	}
	return agg
}

func (s *Service) recordSuccess(ctx context.Context, id int, name string) {
	if s.recorder == nil {
		return
	}
	if err := s.recorder.RecordSuccess(ctx, id); err != nil {
		s.log.Warn("indexersearch: RecordSuccess failed",
			slog.String("indexer", name),
			slog.Int("indexerId", id),
			slog.Any("err", err),
		)
	}
}

func (s *Service) recordFailure(ctx context.Context, id int, name string) {
	if s.recorder == nil {
		return
	}
	if err := s.recorder.RecordFailure(ctx, id, 0); err != nil {
		s.log.Warn("indexersearch: RecordFailure failed",
			slog.String("indexer", name),
			slog.Int("indexerId", id),
			slog.Any("err", err),
		)
	}
}

// seriesTagSet returns the criteria's series-tag set (or nil when
// the series is unbound). Returning a map keyed by tag-id makes the
// per-indexer intersection check O(tagCount) instead of O(N*M).
func seriesTagSet(criteria CriteriaBase) map[int]struct{} {
	if criteria.Series == nil || len(criteria.Series.Tags) == 0 {
		return nil
	}
	out := make(map[int]struct{}, len(criteria.Series.Tags))
	for _, t := range criteria.Series.Tags {
		out[t] = struct{}{}
	}
	return out
}

// tagsMatch implements the C# `i.Definition.Tags.Empty() ||
// i.Definition.Tags.Intersect(criteriaBase.Series.Tags).Any()` test
// at ReleaseSearchService.cs:527 — an untagged indexer matches every
// series; a tagged indexer must share at least one tag.
func tagsMatch(indexerTags []int, seriesTags map[int]struct{}) bool {
	if len(indexerTags) == 0 {
		return true
	}
	if len(seriesTags) == 0 {
		// Untagged series + tagged indexer → no match. This mirrors
		// the C# Intersect semantics: an empty seriesTags ∩ non-empty
		// indexerTags is empty, so .Any() is false.
		return false
	}
	for _, t := range indexerTags {
		if _, ok := seriesTags[t]; ok {
			return true
		}
	}
	return false
}

// criteriaLabel returns the criteria's String() output, or "<rss>"
// when it's the zero value (FetchRSS path doesn't construct a typed
// criteria). Used for log-line context only.
func criteriaLabel(c CriteriaBase) string {
	if c.Series == nil {
		return "<rss>"
	}
	return c.Series.Title
}
