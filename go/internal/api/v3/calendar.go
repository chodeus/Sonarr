package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/httperr"
)

// CalendarEpisodeLister narrows the read surface for the calendar
// endpoint. *episodes.Service.EpisodesBetweenDates satisfies it.
type CalendarEpisodeLister interface {
	EpisodesBetweenDates(ctx context.Context, start, end time.Time, includeUnmonitored, includeSpecials bool) ([]episodes.Episode, error)
}

// CalendarHandler returns the http.Handler for GET /api/v3/calendar.
// Mirrors `CalendarController.GetCalendar` at
// `src/Sonarr.Api.V3/Calendar/CalendarController.cs:35-70`.
//
// Date defaults match C# `start ?? DateTime.Today` and `end ?? Today+2`.
// includeSpecials is hard-coded to true to mirror the C# call site
// (`EpisodesBetweenDates(..., unmonitored, true)`).
//
// J376: the includeSeries / includeEpisodeFile / includeEpisodeImages
// query branches are wired — C# `CalendarController.GetCalendar` passes
// them straight into `MapToResource(result, includeSeries,
// includeEpisodeFile, includeEpisodeImages)`, the same MapToResource the
// Episode handlers use (ported via applyEpisodeSeries /
// applyEpisodeFileSubresource / applyEpisodeImages). Note C# names the
// images param `includeEpisodeImages` here (not `includeImages` like the
// Episode controller). The Series/EpisodeFile embeds light up only when
// the lister ALSO implements the EpisodeSeriesLookup / EpisodeFileLookup
// optional extensions (the cmd/sonarr composite does). Tag filtering +
// AirDateUtc ordering remain DEFERRED (separate from the projection
// branches); the empty-DB recording has zero rows so the gap is
// unobservable in the goldens.
func CalendarHandler(lister CalendarEpisodeLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		now := time.Now().UTC()
		startUse := dateOrDefault(q.Get("start"), startOfDay(now))
		endUse := dateOrDefault(q.Get("end"), startOfDay(now).Add(48*time.Hour))
		unmonitored := strings.EqualFold(q.Get("unmonitored"), "true")

		rows, err := lister.EpisodesBetweenDates(r.Context(), startUse, endUse, unmonitored, true)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("calendar: %w", err), nil)
			return
		}
		includeSeries := q.Get("includeSeries") == "true"
		includeEpisodeFile := q.Get("includeEpisodeFile") == "true"
		includeEpisodeImages := q.Get("includeEpisodeImages") == "true"
		seriesCache := map[int]*SeriesResource{}
		out := make([]EpisodeResource, 0, len(rows))
		for _, row := range rows {
			res := ToEpisodeResource(row)
			ep := row
			applyEpisodeFileSubresource(r.Context(), &res, &ep, lister, includeEpisodeFile)
			applyEpisodeImages(&res, row, includeEpisodeImages)
			applyEpisodeSeries(r.Context(), &res, row.SeriesID, lister, includeSeries, seriesCache)
			out = append(out, res)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

// dateOrDefault parses an ISO date param, falling back to the
// default on empty / unparseable input. Matches ASP.NET model binding
// tolerance for nullable DateTime query params.
func dateOrDefault(s string, def time.Time) time.Time {
	if s == "" {
		return def
	}
	// Accept date-only (2026-01-01), date+time, or full RFC3339.
	for _, layout := range []string{"2006-01-02", "2006-01-02T15:04:05", time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return def
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}
