package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
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
// optional extensions (the cmd/sonarr composite does).
//
// Tag filtering + AirDateUtc ordering are now ported (mirrors C#
// `parsedTags.Any() && parsedTags.None(series.Tags.Contains)` keep-on-
// any-match, then `OrderBy(e => e.AirDateUtc)`). The tag-set is read
// from the `tags` query param as a comma-separated list of tag IDs;
// label-form tags (C# `TagService.GetTag` resolves non-digit names via
// the repo) are not resolved here — the frontend sends IDs.
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
		// C#: when tags are supplied, keep an episode only if its series
		// shares at least one tag. Resolved via the optional series
		// lookup (production's composite implements it).
		if tagIDs := parseTagIDs(q.Get("tags")); len(tagIDs) > 0 {
			rows = filterEpisodesBySeriesTags(r.Context(), rows, lister, tagIDs)
		}
		// C#: OrderBy(e => e.AirDateUtc) — ascending, nil sorts first.
		sortEpisodesByAirDateUtc(rows)
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

// parseTagIDs parses the comma-separated `tags` query param into a
// tag-id slice. Mirrors the digit branch of C# `TagService.GetTag`
// (non-digit label resolution is not ported — the frontend sends ids).
func parseTagIDs(s string) []int {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	out := make([]int, 0, 4)
	for _, part := range strings.Split(s, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(part)); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// anyTagMatches reports whether have and want share any id. Mirrors
// C# `!parsedTags.None(series.Tags.Contains)` (keep when ANY match).
func anyTagMatches(have, want []int) bool {
	for _, w := range want {
		for _, h := range have {
			if h == w {
				return true
			}
		}
	}
	return false
}

// filterEpisodesBySeriesTags drops episodes whose owning series shares
// none of the requested tag ids. Mirrors the C# calendar tag loop,
// including its `series == null → skip` behaviour (an episode whose
// series can't be resolved is dropped). Series tags are fetched via the
// optional EpisodeSeriesLookup; when the lister doesn't implement it the
// rows pass through unfiltered (production always implements it).
func filterEpisodesBySeriesTags(ctx context.Context, rows []episodes.Episode, lister any, tagIDs []int) []episodes.Episode {
	lookup, ok := lister.(EpisodeSeriesLookup)
	if !ok {
		return rows
	}
	tagsBySeries := make(map[int][]int)
	seen := make(map[int]bool)
	out := make([]episodes.Episode, 0, len(rows))
	for _, ep := range rows {
		if !seen[ep.SeriesID] {
			seen[ep.SeriesID] = true
			if s, err := lookup.GetSeries(ctx, ep.SeriesID); err == nil && s != nil {
				tagsBySeries[ep.SeriesID] = s.Tags
			}
		}
		// Missing series → nil tags → no match → skipped (matches C#).
		if anyTagMatches(tagsBySeries[ep.SeriesID], tagIDs) {
			out = append(out, ep)
		}
	}
	return out
}

// sortEpisodesByAirDateUtc orders rows ascending by AirDateUtc; a nil
// AirDateUtc collapses to the zero time (sorts first), matching the
// feed handler's defensive posture for C# `OrderBy(e => e.AirDateUtc)`.
func sortEpisodesByAirDateUtc(rows []episodes.Episode) {
	sort.SliceStable(rows, func(i, j int) bool {
		var ai, aj time.Time
		if rows[i].AirDateUtc != nil {
			ai = *rows[i].AirDateUtc
		}
		if rows[j].AirDateUtc != nil {
			aj = *rows[j].AirDateUtc
		}
		return ai.Before(aj)
	})
}
