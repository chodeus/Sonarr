package v3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/series"
	"sonarr.tv/sonarr/internal/signalr"
)

// SeasonPassResource is the wire shape for POST /api/v3/seasonpass.
// Mirrors C# `Sonarr.Api.V3.SeasonPass.SeasonPassResource` at
// `src/Sonarr.Api.V3/SeasonPass/SeasonPassResource.cs`. The React
// UI's Season Pass page bulk-toggles per-series + per-season
// monitored flags and posts the entire snapshot as one request.
type SeasonPassResource struct {
	Series            []SeasonPassSeriesResource `json:"series"`
	MonitoringOptions *MonitoringOptions         `json:"monitoringOptions,omitempty"`
}

// SeasonPassSeriesResource is one row in the body's `series[]` array.
// Monitored is nullable so the body can opt-in to changing only the
// series-level flag or only the per-season flags.
type SeasonPassSeriesResource struct {
	ID        int                       `json:"id"`
	Monitored *bool                     `json:"monitored,omitempty"`
	Seasons   []SeasonPassSeasonOnWrite `json:"seasons,omitempty"`
}

// SeasonPassSeasonOnWrite is the write-side projection of the v3
// SeasonResource. Statistics + Images aren't on the wire for write
// — the React UI only sends SeasonNumber + Monitored when toggling
// per-season flags in the Season Pass drawer.
type SeasonPassSeasonOnWrite struct {
	SeasonNumber int  `json:"seasonNumber"`
	Monitored    bool `json:"monitored"`
}

// MonitoringOptions mirrors C# `NzbDrone.Core.Tv.MonitoringOptions`
// at `src/NzbDrone.Core/Tv/MonitoringOptions.cs`. The `Monitor` enum
// is the wire-form of the C# `MonitorTypes` — camelCase strings:
// `all` / `future` / `missing` / `existing` / `firstSeason` /
// `lastSeason` / `pilot` / `recent` / `monitorSpecials` /
// `unmonitorSpecials` / `none` / `skip`.
//
// The `IgnoreEpisodesWith{File,outFiles}` booleans are consumed by
// the per-episode SetEpisodeMonitoredStatus pass — DEFERRED at the
// Go port (the episode-bulk-update service doesn't exist yet).
type MonitoringOptions struct {
	IgnoreEpisodesWithFiles    bool   `json:"ignoreEpisodesWithFiles"`
	IgnoreEpisodesWithoutFiles bool   `json:"ignoreEpisodesWithoutFiles"`
	Monitor                    string `json:"monitor"`
}

// seasonPassSeasonOnDisk is the JSON shape inside series.Series.Seasons.
// Series persists Seasons as an embedded-document JSON column; this
// struct round-trips the columns the SeasonPass write path mutates.
// Other fields the on-disk Seasons[N] entries carry are preserved
// via the map[string]any decode/re-encode in [updateSeasonsJSON].
type seasonPassSeasonOnDisk struct {
	SeasonNumber int  `json:"seasonNumber"`
	Monitored    bool `json:"monitored"`
}

// SeasonPassHandler returns POST /api/v3/seasonpass. Mirrors C#
// `SeasonPassController.UpdateAll` at
// `src/Sonarr.Api.V3/SeasonPass/SeasonPassController.cs:24-58`.
//
// Per series:
//
//  1. If body.Monitored is non-nil, override series.Monitored.
//  2. If MonitoringOptions.Monitor is "none" (case-insensitive),
//     force series.Monitored = false (matches C# `:48-51`).
//  3. If body.Seasons is non-empty, walk the existing Seasons JSON
//     and overwrite Monitored on each entry whose SeasonNumber
//     matches a body entry.
//  4. Persist via svc.Update.
//
// DEFERRED: the C# call to `_episodeMonitoredService.SetEpisodeMonitoredStatus`
// at `:55` cascades per-episode Monitored bookkeeping based on the
// MonitoringOptions enum (e.g. "Missing" sets Episode.Monitored =
// (EpisodeFileID == 0)). The Go port's episodes.Service has no
// bulk-set-monitored method yet — episode-level state will diverge
// from the series-level Seasons[].Monitored until that cascade
// ports. Series-level Monitored + Seasons[].Monitored DO update.
//
// Returns 202 with `{}`. Missing series ids 404 the whole batch
// (matches the C# `SingleOrDefault` which throws on missing).
func SeasonPassHandler(svc SeriesWriter, emitter SeriesEmitter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeSeasonPassBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("seasonpass: malformed body: %v", err))
			return
		}
		if len(body.Series) == 0 {
			httperr.BadRequest(w, "series must be provided")
			return
		}
		// Pre-load + validate every id exists before mutating.
		// Matches C# `Single(c => c.Id == s.Id)` throwing on miss.
		rows := make([]*series.Series, 0, len(body.Series))
		for _, s := range body.Series {
			row, err := svc.GetSeries(r.Context(), s.ID)
			if errors.Is(err, series.ErrNotFound) {
				httperr.NotFound(w, fmt.Sprintf("series %d not found", s.ID))
				return
			}
			if err != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("seasonpass: get %d: %w", s.ID, err), nil)
				return
			}
			rows = append(rows, row)
		}
		for i, s := range body.Series {
			row := rows[i]
			if s.Monitored != nil {
				row.Monitored = *s.Monitored
			}
			if body.MonitoringOptions != nil && strings.EqualFold(body.MonitoringOptions.Monitor, "none") {
				row.Monitored = false
			}
			if len(s.Seasons) > 0 {
				updated, err := updateSeasonsJSON(row.Seasons, s.Seasons)
				if err != nil {
					httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("seasonpass: seasons json for %d: %w", s.ID, err), nil)
					return
				}
				row.Seasons = updated
			}
			if err := svc.Update(r.Context(), row); err != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("seasonpass: update %d: %w", s.ID, err), nil)
				return
			}
			res := ToSeriesResource(*row)
			signalr.FireAndLog(httperr.LoggerFromRequest(r), "series", "updated", func() error {
				return emitter.Updated(r.Context(), res)
			})
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{}`))
	})
}

func decodeSeasonPassBody(r *http.Request) (SeasonPassResource, error) {
	defer func() { _ = r.Body.Close() }()
	var out SeasonPassResource
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&out); err != nil {
		return SeasonPassResource{}, err
	}
	return out, nil
}

// updateSeasonsJSON walks the existing Seasons JSON (a `[]obj`
// array) and overwrites the Monitored field on entries whose
// SeasonNumber appears in `bodySeasons`. Entries the body doesn't
// reference are left untouched.
//
// The on-disk JSON carries additional per-season fields (statistics
// snapshots, image arrays — see C# Season.cs / SeasonStatistics.cs)
// that the v3 write path doesn't surface. Decode through
// `[]map[string]any` so those extra fields survive the round-trip
// — only the `monitored` key is overwritten.
//
// Empty / missing on-disk JSON: if the existing row has empty
// Seasons (a fresh-imported series), synthesise a minimal array
// from the body so the user's first toggle persists.
func updateSeasonsJSON(existing string, bodySeasons []SeasonPassSeasonOnWrite) (string, error) {
	overlay := make(map[int]bool, len(bodySeasons))
	for _, b := range bodySeasons {
		overlay[b.SeasonNumber] = b.Monitored
	}
	if strings.TrimSpace(existing) == "" {
		// Synthesise: minimal-shape array preserving only SeasonNumber +
		// Monitored. The richer fields are filled in by the next series-
		// refresh pass (which the empty-existing case implies hasn't run).
		out := make([]seasonPassSeasonOnDisk, 0, len(bodySeasons))
		for _, b := range bodySeasons {
			out = append(out, seasonPassSeasonOnDisk(b))
		}
		buf, err := json.Marshal(out)
		if err != nil {
			return "", err
		}
		return string(buf), nil
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(existing), &rows); err != nil {
		return "", err
	}
	for _, row := range rows {
		nf, ok := row["seasonNumber"].(float64)
		if !ok {
			continue
		}
		if newMon, ok := overlay[int(nf)]; ok {
			row["monitored"] = newMon
		}
	}
	buf, err := json.Marshal(rows)
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

// _ ensures the embedded-document struct is wired into the
// compile-time graph even when no caller references it explicitly.
// updateSeasonsJSON works on raw maps; the typed struct is exported
// for tests asserting JSON-shape compatibility with the C# wire.
var _ = seasonPassSeasonOnDisk{}

// SeasonPassHandlerCtxKey is reserved for a future per-request
// MonitoringOptions enrichment middleware. Unused today.
type SeasonPassHandlerCtxKey struct{}

// Compile-time guard that context import isn't dead.
var _ = context.Background
