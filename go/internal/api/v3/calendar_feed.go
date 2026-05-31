package v3

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/series"
)

// CalendarFeedSeriesLister narrows *series.Service to GetAllSeries —
// the only series read the iCal feed needs.
type CalendarFeedSeriesLister interface {
	GetAllSeries(ctx context.Context) ([]series.Series, error)
}

// CalendarFeedHandler returns GET /api/v3/calendar/Sonarr.ics. Mirrors
// `CalendarFeedController.GetCalendarFeed` at
// `src/Sonarr.Api.V3/Calendar/CalendarFeedController.cs:30-103`.
//
// Query params:
//
//   - pastDays   (default 7)   — start = today - pastDays
//   - futureDays (default 28)  — end   = today + futureDays
//   - unmonitored (default false) — include monitored=false episodes
//   - premieresOnly (default false) — keep only seasonNumber>0 + episodeNumber==1
//   - asAllDay (default false) — emit DATE (no time) rather than DATETIME
//   - tags (default "")        — comma-separated tag-id list; an event
//     is kept only when its series shares at least one tag (mirrors C#
//     `parsedTags.Any() && parsedTags.None(series.Tags.Contains)`).
//     Label-form tags are not resolved (the frontend sends ids).
//
// Output is RFC 5545 text/calendar with CRLF line endings. The Go
// port does NOT fold lines >75 chars (RFC 5545 §3.1 recommends; most
// clients tolerate the long-line shape, and the C# Ical.Net library
// the original code uses does fold — line-folding compatibility is
// DEFERRED until a contract gap surfaces).
func CalendarFeedHandler(epLister CalendarEpisodeLister, srLister CalendarFeedSeriesLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		pastDays := atoiOrDefault(q.Get("pastDays"), 7)
		futureDays := atoiOrDefault(q.Get("futureDays"), 28)
		unmonitored := strings.EqualFold(q.Get("unmonitored"), "true")
		premieresOnly := strings.EqualFold(q.Get("premieresOnly"), "true")
		asAllDay := strings.EqualFold(q.Get("asAllDay"), "true")
		parsedTags := parseTagIDs(q.Get("tags"))

		today := startOfDay(time.Now().UTC())
		start := today.AddDate(0, 0, -pastDays)
		end := today.AddDate(0, 0, futureDays)

		eps, err := epLister.EpisodesBetweenDates(r.Context(), start, end, unmonitored, true)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("calendar/feed: %w", err), nil)
			return
		}
		allSeries, err := srLister.GetAllSeries(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("calendar/feed: series: %w", err), nil)
			return
		}
		seriesByID := make(map[int]series.Series, len(allSeries))
		for _, s := range allSeries {
			seriesByID[s.ID] = s
		}
		sort.SliceStable(eps, func(i, j int) bool {
			// Episodes without AirDateUtc collapse to "earliest" so they
			// emit first — matches C# `OrderBy(v => v.AirDateUtc.Value)`
			// which throws on nil; the Go port stays defensive.
			ai := time.Time{}
			aj := time.Time{}
			if eps[i].AirDateUtc != nil {
				ai = *eps[i].AirDateUtc
			}
			if eps[j].AirDateUtc != nil {
				aj = *eps[j].AirDateUtc
			}
			return ai.Before(aj)
		})

		var sb strings.Builder
		writeICalLine(&sb, "BEGIN:VCALENDAR")
		writeICalLine(&sb, "PRODID:-//sonarr.tv//Sonarr//EN")
		writeICalLine(&sb, "VERSION:2.0")
		writeICalLine(&sb, "NAME:Sonarr TV Schedule")
		writeICalLine(&sb, "X-WR-CALNAME:Sonarr TV Schedule")
		for _, ep := range eps {
			s, ok := seriesByID[ep.SeriesID]
			if !ok {
				continue
			}
			if premieresOnly && (ep.SeasonNumber == 0 || ep.EpisodeNumber != 1) {
				continue
			}
			// Tag filter: keep only when the series shares a requested tag.
			if len(parsedTags) > 0 && !anyTagMatches(s.Tags, parsedTags) {
				continue
			}
			if ep.AirDateUtc == nil {
				continue
			}
			writeICalLine(&sb, "BEGIN:VEVENT")
			writeICalLine(&sb, fmt.Sprintf("UID:NzbDrone_episode_%d", ep.ID))
			status := "TENTATIVE"
			if ep.EpisodeFileID != nil && *ep.EpisodeFileID != 0 {
				status = "CONFIRMED"
			}
			writeICalLine(&sb, "STATUS:"+status)
			if ep.Overview != "" {
				writeICalLine(&sb, "DESCRIPTION:"+escapeICalText(ep.Overview))
			}
			if s.Network != "" {
				writeICalLine(&sb, "CATEGORIES:"+escapeICalText(s.Network))
			}
			if asAllDay {
				writeICalLine(&sb, "DTSTART;VALUE=DATE:"+ep.AirDateUtc.Format("20060102"))
			} else {
				writeICalLine(&sb, "DTSTART:"+ep.AirDateUtc.UTC().Format("20060102T150405Z"))
				end := ep.AirDateUtc.Add(time.Duration(s.Runtime) * time.Minute)
				writeICalLine(&sb, "DTEND:"+end.UTC().Format("20060102T150405Z"))
			}
			summary := summaryFor(s, ep)
			writeICalLine(&sb, "SUMMARY:"+escapeICalText(summary))
			writeICalLine(&sb, "END:VEVENT")
		}
		writeICalLine(&sb, "END:VCALENDAR")

		w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sb.String()))
	})
}

// seriesTypeDaily mirrors C# `SeriesTypes.Daily` integer value (1).
// Persisted on `Series.SeriesType`. The other values (Standard=0,
// Anime=2) use the same Summary format; only Daily skips the
// SxxEyy block.
const seriesTypeDaily = 1

func summaryFor(s series.Series, ep episodes.Episode) string {
	if s.SeriesType == seriesTypeDaily {
		return fmt.Sprintf("%s - %s", s.Title, ep.Title)
	}
	return fmt.Sprintf("%s - %dx%02d - %s", s.Title, ep.SeasonNumber, ep.EpisodeNumber, ep.Title)
}

// writeICalLine appends one CRLF-terminated iCalendar line.
func writeICalLine(sb *strings.Builder, line string) {
	sb.WriteString(line)
	sb.WriteString("\r\n")
}

// escapeICalText escapes the characters RFC 5545 §3.3.11 requires:
// backslash, comma, semicolon, and newline.
func escapeICalText(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`,`, `\,`,
		`;`, `\;`,
		"\r\n", `\n`,
		"\n", `\n`,
	)
	return r.Replace(s)
}

func atoiOrDefault(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}
