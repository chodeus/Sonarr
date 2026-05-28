// CutoffUnmetEpisodeSearch handler. Ports the
// IExecute<CutoffUnmetEpisodeSearchCommand> implementation at
// `src/NzbDrone.Core/IndexerSearch/EpisodeSearchService.cs:163-194`.
//
// Slice 5.J362 (Post-J352 residual-arc, Arc 3). The cutoff-unmet sibling
// of [MissingEpisodeSearch]: iterates episodes whose on-disk file ranks
// below the series' quality-profile cutoff and dispatches each through
// the per-episode search pipeline. The enumerator
// ([EpisodeCutoffUnmetLister]) is backed by the J360/J361
// qualityprofiles.QualitiesBelowCutoff + episodes.Repo.WantedCutoffUnmet
// surface.
//
// The C# command body mirrors MissingEpisodeSearchCommand exactly:
//
//   - `SeriesId int?` — when set, scope to that series's episodes only
//     (in-memory filter, matching the C# `FilterExpressions.Add(v =>
//     v.SeriesId == seriesId)` at `:174-178`).
//   - `Monitored bool` — default true. When false the lister inverts to
//     the unmonitored set (the lister applies the flag; here we just
//     forward it).
//
// Scope deferrals (identical to MissingEpisodeSearch — see that handler):
//
//   - The C# version subtracts queued episodes via `GetQueuedEpisodeIds()`
//     before dispatching. The Go port doesn't thread the queue.Service
//     into this handler yet — queued items dedup at the download-client
//     adapter. Documented divergence.
//   - The C# version uses `SearchForBulkEpisodes` (season grouping +
//     small-partial-season fallback); the Go port issues per-episode
//     searches directly. Same release set, more indexer queries.
//
// The SeriesId filter is applied at the handler tier (the lister returns
// the full cutoff-unmet set), matching the MissingEpisodeSearch posture.

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"sonarr.tv/sonarr/internal/episodes"
)

// CutoffUnmetEpisodeSearchName is the registry key + Commands.Name +
// ScheduledTasks.TypeName. Mirrors the C# short form:
// `CutoffUnmetEpisodeSearchCommand` → `CutoffUnmetEpisodeSearch`.
const CutoffUnmetEpisodeSearchName = "CutoffUnmetEpisodeSearch"

// EpisodeCutoffUnmetLister surfaces the read accessor
// CutoffUnmetEpisodeSearch consumes. The production binding wraps
// `*episodes.Repo` + `*qualityprofiles.Service` (computes the
// per-profile below-cutoff quality-id sets then runs
// `episodes.Repo.WantedCutoffUnmet`).
type EpisodeCutoffUnmetLister interface {
	CutoffUnmetEpisodes(ctx context.Context, monitored bool) ([]episodes.Episode, error)
}

// CutoffUnmetEpisodeSearchBody is the JSON shape of the command body.
// Mirrors `IndexerSearch/CutoffUnmetEpisodeSearchCommand.cs:5-23`. Both
// fields optional in JSON — missing `monitored` defaults to true (the
// C# default in the parameterless constructor).
type CutoffUnmetEpisodeSearchBody struct {
	SeriesID  *int  `json:"seriesId,omitempty"`
	Monitored *bool `json:"monitored,omitempty"`
}

// CutoffUnmetEpisodeSearch is the IExecute<CutoffUnmetEpisodeSearchCommand>
// port. Logs a stub line when no Lister/Searcher are wired so the
// registry slot still satisfies "command exists" probes.
type CutoffUnmetEpisodeSearch struct {
	lister   EpisodeCutoffUnmetLister
	searcher EpisodeSearcher
	log      *slog.Logger
}

// NewCutoffUnmetEpisodeSearch builds the handler. Both deps optional;
// when either is nil the handler logs the stub line + returns nil. log
// defaults to slog.Default.
func NewCutoffUnmetEpisodeSearch(lister EpisodeCutoffUnmetLister, searcher EpisodeSearcher, log *slog.Logger) *CutoffUnmetEpisodeSearch {
	if log == nil {
		log = slog.Default()
	}
	return &CutoffUnmetEpisodeSearch{lister: lister, searcher: searcher, log: log}
}

// Execute parses the body, loads the cutoff-unmet candidate set, applies
// the optional SeriesId scope filter, and dispatches per-episode
// searches. Per-episode failures are absorbed (warn-logged + counted);
// Execute returns nil so the dispatcher doesn't classify the run as
// failed.
func (h *CutoffUnmetEpisodeSearch) Execute(ctx context.Context, body string) error {
	if h.lister == nil || h.searcher == nil {
		h.log.LogAttrs(ctx, slog.LevelInfo,
			"cutoffunmetepisodesearch: stub Execute (Lister/Searcher not wired)",
			slog.String("command", CutoffUnmetEpisodeSearchName))
		return nil
	}

	monitored := true
	var seriesID *int
	if body != "" {
		var parsed CutoffUnmetEpisodeSearchBody
		if err := json.Unmarshal([]byte(body), &parsed); err != nil {
			h.log.LogAttrs(ctx, slog.LevelWarn,
				"cutoffunmetepisodesearch: malformed body — using defaults",
				slog.String("command", CutoffUnmetEpisodeSearchName),
				slog.Any("err", err))
		} else {
			if parsed.Monitored != nil {
				monitored = *parsed.Monitored
			}
			seriesID = parsed.SeriesID
		}
	}

	eps, err := h.lister.CutoffUnmetEpisodes(ctx, monitored)
	if err != nil {
		return fmt.Errorf("cutoffunmetepisodesearch: enumerate cutoff-unmet: %w", err)
	}

	if seriesID != nil {
		filtered := eps[:0]
		for _, e := range eps {
			if e.SeriesID == *seriesID {
				filtered = append(filtered, e)
			}
		}
		eps = filtered
	}

	if len(eps) == 0 {
		h.log.LogAttrs(ctx, slog.LevelInfo,
			"cutoffunmetepisodesearch: no candidates",
			slog.String("command", CutoffUnmetEpisodeSearchName),
			slog.Bool("monitored", monitored))
		return nil
	}

	var searched, decisions, failures int
	for _, ep := range eps {
		n, err := h.searcher.EpisodeSearch(ctx, ep.ID)
		searched++
		if err != nil {
			failures++
			h.log.LogAttrs(ctx, slog.LevelWarn,
				"cutoffunmetepisodesearch: per-episode search failed",
				slog.Int("episodeId", ep.ID),
				slog.Any("err", err))
			continue
		}
		decisions += n
	}

	h.log.LogAttrs(ctx, slog.LevelInfo,
		"cutoffunmetepisodesearch: complete",
		slog.String("command", CutoffUnmetEpisodeSearchName),
		slog.Bool("monitored", monitored),
		slog.Int("searched", searched),
		slog.Int("decisions", decisions),
		slog.Int("failures", failures))
	return nil
}
