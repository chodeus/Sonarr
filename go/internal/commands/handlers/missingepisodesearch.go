// MissingEpisodeSearch handler. Ports the
// IExecute<MissingEpisodeSearchCommand> implementation at
// `src/NzbDrone.Core/IndexerSearch/EpisodeSearchService.cs:121-160`.
//
// Slice 5.J39 (audit B.3 close-out). Iterates monitored episodes that
// have aired without files and dispatches each through the
// per-episode search pipeline (indexersearch.Service.EpisodeSearch).
//
// The C# command body has two optional fields:
//
//   - `SeriesId int?` — when set, scope to that series's episodes
//     only (in-memory filter on `GetEpisodeBySeries(id)`).
//   - `Monitored bool` — default true. When false, the filter
//     inverts to `Monitored == false || Series.Monitored == false`.
//
// Scope deferrals (matches the existing audit row's "missing
// entirely" baseline — the implementation can grow these without
// breaking the wire shape):
//
//   - The C# version subtracts queued episodes via
//     `GetQueuedEpisodeIds()` before dispatching the bulk search.
//     The Go port doesn't yet thread the queue.Service into this
//     handler — items already in the queue would just dedup at the
//     download-client adapter via the indexer's name+title cache
//     (slice 6.A6c). Documented divergence.
//   - The C# version uses `SearchForBulkEpisodes` which groups by
//     season + falls back to single-episode search on small partial
//     seasons. The Go port issues per-episode searches directly —
//     same release set, more indexer queries on aired-out seasons.
//     Tightening this is a follow-up indexersearch.Service method.
//
// The `SeriesId` filter is implemented at the handler tier (NOT in
// the query) — the SQL query enumerates all missing episodes; the
// handler then filters by SeriesId when the body sets it. This
// matches the C# `GetEpisodeBySeries(id).Where(...)` approach at
// `:127-133`.

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"sonarr.tv/sonarr/internal/episodes"
)

// MissingEpisodeSearchName is the registry key + Commands.Name +
// ScheduledTasks.TypeName. Mirrors the C# short form:
// `MissingEpisodeSearchCommand` → `MissingEpisodeSearch`.
const MissingEpisodeSearchName = "MissingEpisodeSearch"

// EpisodeMissingLister surfaces the read accessor MissingEpisodeSearch
// consumes. `*episodes.Service` satisfies this via Go's structural
// typing (the method added in slice 5.J39).
type EpisodeMissingLister interface {
	EpisodesWithoutFiles(ctx context.Context, monitored bool, now time.Time) ([]episodes.Episode, error)
}

// EpisodeSearcher dispatches the per-episode search. The narrow
// interface keeps the handlers package decoupled from
// indexersearch — `*indexersearch.Service.EpisodeSearch` satisfies
// this; the returned decision count is logged but not surfaced
// further (the search service handles its own grab dispatch).
type EpisodeSearcher interface {
	EpisodeSearch(ctx context.Context, episodeID int) (int, error)
}

// MissingEpisodeSearchBody is the JSON shape of the command body.
// Mirrors the C# command class at
// `IndexerSearch/MissingEpisodeSearchCommand.cs:5-23`. Both fields
// are optional in JSON — missing `monitored` defaults to true (the
// C# default in `MissingEpisodeSearchCommand()` constructor).
type MissingEpisodeSearchBody struct {
	SeriesID  *int  `json:"seriesId,omitempty"`
	Monitored *bool `json:"monitored,omitempty"`
}

// MissingEpisodeSearch is the IExecute<MissingEpisodeSearchCommand>
// port. Logs a stub line when no Lister/Searcher are wired so the
// registry slot still satisfies "command exists" probes.
type MissingEpisodeSearch struct {
	lister   EpisodeMissingLister
	searcher EpisodeSearcher
	clock    func() time.Time
	log      *slog.Logger
}

// NewMissingEpisodeSearch builds the handler. Both deps optional;
// when either is nil the handler logs the stub line + returns nil.
// log defaults to slog.Default.
func NewMissingEpisodeSearch(lister EpisodeMissingLister, searcher EpisodeSearcher, log *slog.Logger) *MissingEpisodeSearch {
	if log == nil {
		log = slog.Default()
	}
	return &MissingEpisodeSearch{
		lister:   lister,
		searcher: searcher,
		clock:    func() time.Time { return time.Now().UTC() },
		log:      log,
	}
}

// SetClock overrides the now() source for tests so the AirDateUtc <
// now filter is deterministic.
func (h *MissingEpisodeSearch) SetClock(c func() time.Time) {
	if c != nil {
		h.clock = c
	}
}

// Execute parses the body, loads the candidate set, applies the
// optional SeriesId scope filter, and dispatches per-episode
// searches. Per-episode search failures are absorbed (warn-logged
// and counted); Execute itself returns nil so the scheduler /
// command dispatcher doesn't classify the run as failed.
func (h *MissingEpisodeSearch) Execute(ctx context.Context, body string) error {
	if h.lister == nil || h.searcher == nil {
		h.log.LogAttrs(ctx, slog.LevelInfo,
			"missingepisodesearch: stub Execute (Lister/Searcher not wired)",
			slog.String("command", MissingEpisodeSearchName))
		return nil
	}

	monitored := true
	var seriesID *int
	if body != "" {
		var parsed MissingEpisodeSearchBody
		if err := json.Unmarshal([]byte(body), &parsed); err != nil {
			h.log.LogAttrs(ctx, slog.LevelWarn,
				"missingepisodesearch: malformed body — using defaults",
				slog.String("command", MissingEpisodeSearchName),
				slog.Any("err", err))
		} else {
			if parsed.Monitored != nil {
				monitored = *parsed.Monitored
			}
			seriesID = parsed.SeriesID
		}
	}

	eps, err := h.lister.EpisodesWithoutFiles(ctx, monitored, h.clock())
	if err != nil {
		return fmt.Errorf("missingepisodesearch: enumerate missing: %w", err)
	}

	// Optional SeriesId scope filter — applied post-query because the
	// SQL enumerator returns ALL missing episodes for the monitored
	// flag (matches the C# `GetEpisodeBySeries(id).Where(...)`
	// posture at `:127-133`).
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
			"missingepisodesearch: no candidates",
			slog.String("command", MissingEpisodeSearchName),
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
				"missingepisodesearch: per-episode search failed",
				slog.Int("episodeId", ep.ID),
				slog.Any("err", err))
			continue
		}
		decisions += n
	}

	h.log.LogAttrs(ctx, slog.LevelInfo,
		"missingepisodesearch: complete",
		slog.String("command", MissingEpisodeSearchName),
		slog.Bool("monitored", monitored),
		slog.Int("searched", searched),
		slog.Int("decisions", decisions),
		slog.Int("failures", failures))
	return nil
}
