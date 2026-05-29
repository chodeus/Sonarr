// Package calendar is the v5 wire surface for /api/v5/calendar and
// /feed/v5/calendar/sonarr.ics.
//
// The v5 family (src/Sonarr.Api.V5/Calendar/) is 184 LOC of C# across
// 3 files — CalendarController (73), CalendarFeedController (103),
// CalendarSubresource enum (8).
//
// Three endpoints:
//
//	v5 endpoint                                 v5 status   vs v3 Go-port
//	GET /api/v5/calendar                          200       same envelope as /api/v3/calendar (path-alias)
//	GET /api/v5/calendar/{id}                     200/404   same as v3 — inherited GetResourceById
//	GET /feed/v5/calendar/sonarr.ics              200/204   same as /feed/v3/calendar/Sonarr.ics
//	                                                        (URL casing change is wire-only)
//
// **PATH CASING change on the feed**: v3 exposes the iCal endpoint at
// `/feed/v3/calendar/Sonarr.ics` (capital S); v5 lowercases to
// `/feed/v5/calendar/sonarr.ics`. The Go http.ServeMux pattern is
// case-sensitive, so wire the v5 route with the lowercase exactly.
//
// **Wire-contract diff vs v3**:
//
//  1. **Query-param shape differs (calendar list).** v3
//     CalendarController takes three independent bools — `includeSeries`,
//     `includeEpisodeFile`, `includeEpisodeImages`; v5 collapses to
//     one CSV enum array `includeSubresources` of `CalendarSubresource`
//     (Series, EpisodeFile, Images). v5 also adds `tags` (CSV) and
//     `includeSpecials` (default true). Invisible at the empty-corpus
//     level because the v3 Go-port handler ignores all three bools
//     and emits the base EpisodeResource projection. Populated-row
//     sub-resource hydration is part of the same Wave 1 deferred chain
//     as J258/J259/J261/J264 — when the custom-format calculator +
//     Series/EpisodeFile join port lands, v5 will need a
//     `includeSubresources` CSV→bool projection helper.
//
//  2. **EpisodeResource widens in v5.** v5 EpisodeResource adds
//     `EndTime?` + `GrabDate?` and marks more fields explicitly
//     nullable; at the empty-corpus level (records `[]`) this is
//     invisible. Same deferral chain.
//
//  3. **Feed body / ICS format**: identical between v3 and v5 (both
//     use Ical.Net with the same calendar properties + event shape).
//     Path-alias is byte-equal.
//
// Pure path-alias slice (J251/J259/J264 pattern), modulo the URL
// casing change. CalendarController inherits EpisodeControllerWithSignalR,
// which inherits RestControllerWithSignalR — `GetResourceById`
// resolves to the same EpisodeByIDHandler the v3 surface already uses.
//
// Wave 3 v5 endpoint family port — 184 LOC C# source. J268.
package calendar

import (
	"net/http"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/api/v5/internal/v5util"
)

// EpisodeLister is an alias of the v3 read interface used by both
// the list endpoint and the iCal feed. Satisfied by *episodes.Service
// via EpisodesBetweenDates.
type EpisodeLister = apiv3.CalendarEpisodeLister

// FeedSeriesLister is an alias of the v3 series-read interface used
// by the iCal feed (looks up Series for Network + Runtime).
type FeedSeriesLister = apiv3.CalendarFeedSeriesLister

// EpisodeByIDGetter is an alias of the v3 by-id read interface used
// by the inherited GetResourceById endpoint.
type EpisodeByIDGetter = apiv3.EpisodeByIDGetter

// Resource is an alias of the v3 EpisodeResource wire shape.
// Populated-row v5 widening (EndTime/GrabDate) deferred per the
// package doc above.
type Resource = apiv3.EpisodeResource

// ----- handlers (all path-aliases — same shape + status as v3) -----

// calendarSubresourceMapping maps the v5 CalendarSubresource enum tokens
// (Series/EpisodeFile/Images, camelCase) onto the v3 include-flag query
// keys the v3 calendar handler reads. Note: calendar's images flag is
// `includeEpisodeImages` (not `includeImages`).
var calendarSubresourceMapping = map[string]string{
	"series":      "includeSeries",
	"episodefile": "includeEpisodeFile",
	"images":      "includeEpisodeImages",
}

// ListHandler returns GET /api/v5/calendar. Same envelope, sort
// (AirDateUtc ASC), and status (200) as v3. Translates the v5
// `?includeSubresources=…` enum-array param onto v3's include bools
// (J418); v3-style bools still pass through.
func ListHandler(lister EpisodeLister) http.Handler {
	return v5util.TranslateIncludeSubresources(apiv3.CalendarHandler(lister), calendarSubresourceMapping)
}

// ByIDHandler returns GET /api/v5/calendar/{id}. Inherited from
// RestController via EpisodeControllerWithSignalR — both versions
// resolve to the EpisodeByIDHandler shape: 200 + EpisodeResource on
// found, 404 on miss.
var ByIDHandler = apiv3.EpisodeByIDHandler

// FeedHandler returns GET /feed/v5/calendar/sonarr.ics. Same iCal
// format and status (200 text/calendar OR 204 NoContent on empty)
// as v3's /feed/v3/calendar/Sonarr.ics — only the URL casing changes.
var FeedHandler = apiv3.CalendarFeedHandler
