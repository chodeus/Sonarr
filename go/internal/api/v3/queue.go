package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/httperr"
)

// QueueResource is the per-record shape in the
// PagingResource[QueueResource] envelope. Mirrors
// `src/Sonarr.Api.V3/Queue/QueueResource.cs`.
//
// This slice ships the wire-shape skeleton; populated-row mapping
// awaits the queue.Service paged-projection port (the full C#
// QueueResource carries ~25 fields joining QueueItem + Series +
// Episode + TrackedDownload + DownloadClient + Indexer +
// CustomFormat — none of that is needed for the empty-DB byte-eq
// gate). The empty-DB golden has zero records so the gap is
// unobservable today (DEFERRED — STATUS line 9 carries a note).
//
// Field-source mapping (C# → Go) for the minimal subset:
//
//	id            → QueueItem.ID (omitempty per RestResource base)
//	seriesId      → nullable Series link
//	episodeId     → nullable Episode link
//	title         → QueueItem.Title
//	status        → QueueItem.Status (camelCase enum string)
//	downloadId    → QueueItem.DownloadID
//	protocol      → QueueItem.Protocol (camelCase enum string)
//
// Pointers for the nullable int fields preserve the C# `int?`
// semantic — null when unset on the wire, populated when the queue
// row is tied to a Series/Episode.
//
// J324 v5-additive list-form widening: EpisodeIDs + SeasonNumbers
// (both pointer-to-`[]int` with omitempty per J309 pointer-to-slice
// 3-state).
//
//   - `EpisodeIDs *[]int json:"episodeIds,omitempty"` — v5-only.
//     v5 declares `IEnumerable<int> EpisodeIds = []` at
//     QueueResource.cs:15 (replaces v3's singular `EpisodeId int?`
//     which the Go-port already carries at `EpisodeID *int`). The
//     Go-port emits BOTH (`episodeId` singular for v3 wire +
//     `episodeIds` plural for v5 wire) on the shared apiv3 type as
//     deliberate additive divergence on each surface (same posture
//     as J322 Episode/Episodes). 3-state: nil → key absent;
//     non-nil empty → `"episodeIds":[]`; populated → `[1,2]`.
//   - `SeasonNumbers *[]int json:"seasonNumbers,omitempty"` —
//     v5-only. v5 declares `List<int> SeasonNumbers = []` at
//     QueueResource.cs:16 (no v3 equivalent — v3's singular
//     SeasonNumber sits on the model not the wire). Additive
//     divergence on v3 (v3 clients ignore unknown JSON keys). Same
//     3-state distinction.
//
// J323 StatusMessages typed-list widening: pointer-to-slice of
// [QueueStatusMessage] with omitempty (J309 SeasonResource.Images
// pattern — 3-state distinction: nil → key absent matching C#
// `List<TrackedDownloadStatusMessage>?` null under WhenWritingNull;
// non-nil empty → `"statusMessages":[]`; populated → typed list).
// The QueueStatusMessage struct (defined alongside QueueResource)
// mirrors C# TrackedDownloadStatusMessage with the 2-field shape
// `{title, messages[]}`.
//
// J322 subresource widening: Series + Episode + Episodes (J302
// pattern — pointer-to-struct + pointer-to-slice with omitempty).
//
//   - `Series *SeriesResource json:"series,omitempty"` — both v3
//     and v5 declare a single nullable Series subresource
//     (`SeriesResource? Series` at C# v3 :15 / v5 :14). Populated
//     when the `includeSeries=true` query branch is set on the C#
//     mapper (`QueueResourceMapper.cs:46-47` /
//     `QueueResourceMapper.cs:23`). Today's Go-port stub leaves nil
//     so omitempty drops the key.
//   - `Episode *EpisodeResource json:"episode,omitempty"` — v3-only.
//     v3 carries the SINGULAR field `EpisodeResource? Episode` at
//     QueueResource.cs:14, populated when `includeEpisode=true`.
//     v5 dropped this field in favor of the plural Episodes list.
//     The Go-port emits both (additive divergence on v5 — v5
//     clients see an extra `"episode"` key when populated; v5
//     ignores unknown JSON keys so harmless). Today nil drops the
//     key.
//   - `Episodes *[]EpisodeResource json:"episodes,omitempty"` —
//     v5-only. v5 carries `List<EpisodeResource>? Episodes` at
//     QueueResource.cs:18, populated when `includeEpisodes=true`.
//     Pointer-to-slice for the 3-state distinction (nil → key
//     absent; non-nil empty → `"episodes":[]`; populated →
//     `"episodes":[{...}]`). Additive divergence on v3 same as
//     above (v3 clients ignore unknown JSON keys).
//
// J321 SizeLeft + TimeLeft widening:
//   - SizeLeft int64 — non-nullable on v5 (`decimal SizeLeft`),
//     CAMEL-case key on the wire (`"sizeLeft"`). v3 uses the
//     obsolete lowercase `Sizeleft` field with key `"sizeleft"`
//     (the C# v3 declaration at QueueResource.cs:52-53 carries the
//     `[Obsolete("Will be replaced by SizeLeft")]` attribute; the
//     new `SizeLeft` field is COMMENTED OUT in C# v3 due to
//     case-insensitive deserialization collision). The Go-port
//     emits the v5 form `"sizeLeft"` on BOTH surfaces — this is a
//     deliberate forward-compatible v3 divergence (additive: today
//     the field doesn't appear on the wire at all, so no current
//     v3 client depends on the obsolete `"sizeleft"` key; future
//     populated rows will surface the v5-form key on both
//     surfaces). omitempty drops 0 to absent (acceptable: 0
//     SizeLeft on a queue row carries no semantic).
//   - TimeLeft string — wire form is the C# TimeSpan default
//     format `"[d.]hh:mm:ss"` (days suffix omitted when 0). v5 +
//     v3 wire-key divergence same as SizeLeft (v5 `"timeLeft"`,
//     v3 obsolete `"timeleft"`); Go-port emits the v5 form. The
//     populated-mapper invoker formats `*time.Duration` →
//     wire-string via [FormatTimeLeft] (this slice's helper).
//     omitempty drops empty to absent (matches C# `TimeSpan?` null
//     under WhenWritingNull).
//
// J320 enum-string widening: TrackedDownloadStatus +
// TrackedDownloadState (both v3+v5; both nullable on C# —
// `TrackedDownloadStatus?` / `TrackedDownloadState?` per
// `QueueResource.cs:39-40`). Wired as `string` with omitempty so
// nil/empty drops the key (matches C# WhenWritingNull). Values are
// the camelCase form per STJson's global
// `JsonStringEnumConverter(CamelCase)` applied to the C# enum
// names: `"downloading"` / `"importBlocked"` / `"importPending"` /
// `"importing"` / `"imported"` / `"failedPending"` / `"failed"` /
// `"ignored"` for State; `"ok"` / `"warning"` / `"error"` for
// Status. The populated-mapper invoker (cmd/sonarr — gated on
// queue.Service's paged surface) computes via
// `CamelCaseEnumName(trackeddownloads.Status(*persisted).String())`
// — both enums already have `.String()` methods returning the C#
// PascalCase form at internal/trackeddownloads/trackeddownloads.go.
// Today's empty-DB stub leaves both fields empty → omitempty drops
// the keys; no wire-shape change at the empty-DB shadow-traffic
// level.
//
// J319 *time.Time widening: Added + EstimatedCompletionTime (both
// v3+v5; both C# `DateTime?` nullable). Wired here on the shared
// apiv3.QueueResource with omitempty pointer-to-time (J302 pattern
// — nil pointer drops the key matching C# WhenWritingNull; non-nil
// pointer emits the RFC3339 timestamp the encoding/json default
// `time.Time` marshal produces, which matches C#'s ISO-8601 format
// modulo timezone offset formatting). Both fields are already on
// the persisted queue.Queue model (internal/queue/queue.go:184
// Added, :182 EstimatedCompletionTime); when populated-mapper
// lands it allocates `Added: e.Added, EstimatedCompletionTime:
// e.EstimatedCompletionTime` verbatim without re-widening the
// struct.
//
// J349 CustomFormats list widening: CustomFormats
// ([]CustomFormatResource with omitempty). C# v3+v5 declare
// `List<CustomFormatResource> CustomFormats` (QueueResource.cs:26),
// always set via `model.RemoteEpisode?.CustomFormats?.ToResource(false)`
// (QueueResource.cs:75). The Go-port projects from the queue row's
// `RemoteEpisode *decisionengine.Subject` — the Subject carries the
// decision-time-matched CustomFormats slice. omitempty drops the key
// when the queue row has no RemoteEpisode (or no matched formats),
// matching the C# null-list-absent posture. Paired with the existing
// CustomFormatScore int: the Go-port uses the Subject's precomputed
// CustomFormatScore (computed at decision time via
// qualityprofiles.CalculateCustomFormatScore against the series QP)
// rather than recomputing at resource-mapping time as C# does — the
// two are equivalent unless the QP changed between grab and display,
// and reusing the precomputed score avoids threading the
// customFormatsSvc + qualityProfilesSvc lookups into the queue paged
// adapter.
//
// J318 bool/int widening: DownloadClientHasPostImportCategory bool
// (both v3+v5) + CustomFormatScore int (both v3+v5) +
// EpisodesWithFilesCount int (v5-additive — v3 carries
// EpisodeHasFile bool instead; the Go-port surfaces the v5 form on
// the shared apiv3 type as deliberate v3 divergence per the J316
// pattern, since the v3 wire field is rarely consumed and the v5
// shape is the forward-compatible one) + IsFullSeason bool (v5-
// additive). All omitempty-tagged: bool false / int 0 drop the
// key, populated values emit. Today's empty-DB stub never
// allocates these fields → no wire-shape change at the empty-DB
// shadow-traffic level.
//
// J317 scalar widening: Size (int64) + Indexer + DownloadClient +
// OutputPath + ErrorMessage (4 strings). All persisted on the
// queue.Queue model already (queue.go:170 Size, :211 Indexer, :204
// DownloadClient, :214 OutputPath, :217 ErrorMessage); wired here
// on the wire shape with omitempty so today's empty-DB stub (no
// rows) emits no change. C# v3+v5 have all 5 fields — Size is
// non-nullable `decimal` (always emits as `size:0` on populated
// rows under no-omitempty in C#; Go-port collapses 0 to absent
// via omitempty as an acceptable trade-off for the queue carrier
// where 0-size on a queue row would mean "no size reported" which
// is semantically equivalent to "size key absent"). The 4 string
// fields are nullable in C# v5 (`string?`) — omitempty matches
// WhenWritingNull for the null case with the Go-typical empty-
// string-collapse caveat (acceptable: empty Indexer / OutputPath
// / DownloadClient / ErrorMessage carry no semantic).
//
// DEFERRED on the QueueResource scalar tier: SizeLeft + TimeLeft
// (need TimeSpan wire format), TrackedDownloadStatus + State
// (need enum-string conversion via existing trackeddownloads
// types), StatusMessages (typed list DTO), Quality + CustomFormats
// (typed DTOs), DownloadClientHasPostImportCategory + IsFullSeason
// + EpisodesWithFilesCount (bool/int — addable in a J317-pattern
// follow-up).
//
// J315 widening: Languages ([]LanguageRef with omitempty). C# v3
// declares `List<Language>` (non-nullable) and v5 marks the same
// field as `List<Language> Languages = []` with a constructor
// default. Under STJson `WhenWritingNull`, a null List drops the
// key — the omitempty Go tag mirrors that posture: nil drops the
// key (matches today's empty-DB stub where the QueueResource
// projection never allocates Languages); non-nil empty emits
// `"languages":[]`; populated emits `"languages":[{...}]`. The
// populated-row mapper (gated on queue.Service's paged surface +
// the TrackedDownloads service join the C# tree does) should
// allocate via apiv3.ToLanguageRefs(model.Languages) to match the
// C# `model.Languages` passthrough; the empty Go-port queue
// stub today leaves the field nil so the wire stays unchanged
// at the empty-DB shape.
type QueueResource struct {
	ID                                  int                    `json:"id,omitempty"`
	SeriesID                            *int                   `json:"seriesId,omitempty"`
	EpisodeID                           *int                   `json:"episodeId,omitempty"`
	Title                               string                 `json:"title,omitempty"`
	Status                              string                 `json:"status,omitempty"`
	DownloadID                          string                 `json:"downloadId,omitempty"`
	Protocol                            string                 `json:"protocol,omitempty"`
	Languages                           []LanguageRef          `json:"languages,omitempty"`
	Quality                             *QualityModelResource  `json:"quality,omitempty"`
	TrackedDownloadStatus               string                 `json:"trackedDownloadStatus,omitempty"`
	TrackedDownloadState                string                 `json:"trackedDownloadState,omitempty"`
	Size                                int64                  `json:"size,omitempty"`
	SizeLeft                            int64                  `json:"sizeLeft,omitempty"`
	TimeLeft                            string                 `json:"timeLeft,omitempty"`
	Indexer                             string                 `json:"indexer,omitempty"`
	DownloadClient                      string                 `json:"downloadClient,omitempty"`
	DownloadClientHasPostImportCategory bool                   `json:"downloadClientHasPostImportCategory,omitempty"`
	OutputPath                          string                 `json:"outputPath,omitempty"`
	ErrorMessage                        string                 `json:"errorMessage,omitempty"`
	CustomFormats                       []CustomFormatResource `json:"customFormats,omitempty"`
	CustomFormatScore                   int                    `json:"customFormatScore,omitempty"`
	EpisodesWithFilesCount              int                    `json:"episodesWithFilesCount,omitempty"`
	IsFullSeason                        bool                   `json:"isFullSeason,omitempty"`
	Added                               *time.Time             `json:"added,omitempty"`
	EstimatedCompletionTime             *time.Time             `json:"estimatedCompletionTime,omitempty"`
	Series                              *SeriesResource        `json:"series,omitempty"`
	Episode                             *EpisodeResource       `json:"episode,omitempty"`
	Episodes                            *[]EpisodeResource     `json:"episodes,omitempty"`
	EpisodeIDs                          *[]int                 `json:"episodeIds,omitempty"`
	SeasonNumbers                       *[]int                 `json:"seasonNumbers,omitempty"`
	StatusMessages                      *[]QueueStatusMessage  `json:"statusMessages,omitempty"`
}

// QueueStatusMessage is the wire-shape mirror of C#
// `NzbDrone.Core.Download.TrackedDownloads.TrackedDownloadStatusMessage`
// (`src/NzbDrone.Core/Download/TrackedDownloads/TrackedDownloadStatusMessage.cs`).
// Surfaced as a typed list on QueueResource.StatusMessages — both
// v3 and v5 C# declare `List<TrackedDownloadStatusMessage>?
// StatusMessages` (nullable list of structured warnings).
//
// Wire shape: `{"title":"<group>","messages":["<msg1>","<msg2>"]}`.
// Title carries a group/category label (e.g. "Cannot import:");
// Messages carries one or more lines under that title. Both fields
// always emit when the parent struct is non-nil (no omitempty on
// either) — matches the C# default-non-null property serialization
// once the parent List<> is populated.
//
// Persistence gap: the current Go-port `queue.Queue` model
// simplifies StatusMessages to `[]string` (per
// internal/queue/queue.go:193). When the populated mapper lands,
// the simple-string form will be wrapped as
// `[]QueueStatusMessage{{Title: "Warning", Messages: [s]}}` per
// persisted entry (single-element synthesis) OR the persistence
// layer will be refactored to carry the structured form. This
// slice pins the wire shape so the populated-mapper port can
// proceed without re-widening.
//
// J323.
type QueueStatusMessage struct {
	Title    string   `json:"title"`
	Messages []string `json:"messages"`
}

// FormatTimeLeft renders a *time.Duration in the C# TimeSpan
// default `[d.]hh:mm:ss` wire form used by QueueResource.TimeLeft.
// nil or zero duration returns "" so the omitempty json tag drops
// the key entirely (matches C# `TimeSpan?` null under STJson
// WhenWritingNull). Negative durations are clamped to "" since
// queue rows never carry negative time-left in practice.
//
// Format rules mirror C# `TimeSpan.ToString("c")` (the "constant"
// format pattern):
//   - days == 0: `hh:mm:ss` (zero-padded, e.g. `"00:05:23"`).
//   - days >= 1: `d.hh:mm:ss` (no padding on days, e.g.
//     `"3.04:12:00"`).
//
// J321.
func FormatTimeLeft(d *time.Duration) string {
	if d == nil || *d <= 0 {
		return ""
	}
	total := int64((*d).Seconds())
	days := total / 86400
	hours := (total / 3600) % 24
	minutes := (total / 60) % 60
	seconds := total % 60
	if days > 0 {
		return fmt.Sprintf("%d.%02d:%02d:%02d", days, hours, minutes, seconds)
	}
	return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds)
}

// QueueDetailsLister narrows the queue read for the /queue/details
// endpoint. Returns flat (unpaged) QueueResource rows optionally
// filtered by seriesId or episodeIds. Today the production wiring
// substitutes an empty implementation matching the recorded
// empty-DB shape — populated-row projection requires the same
// tracked-downloads / pending-release join the /queue paged surface
// needs, which is DEFERRED.
type QueueDetailsLister interface {
	QueueDetails(ctx context.Context, seriesID *int, episodeIDs []int) ([]QueueResource, error)
}

// QueueDetailsHandler returns GET /api/v3/queue/details. Mirrors
// `QueueDetailsController.GetQueue` at
// `src/Sonarr.Api.V3/Queue/QueueDetailsController.cs:43-61`. Supports
// optional `seriesId` (single int) + `episodeIds` (repeated int) query
// filters. C# also accepts includeSeries / includeEpisode flags that
// expand the resource projection; that join is DEFERRED in line with
// the rest of the Go-port queue surface.
func QueueDetailsHandler(lister QueueDetailsLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		var seriesID *int
		if s := q.Get("seriesId"); s != "" {
			n, err := strconv.Atoi(s)
			if err != nil || n <= 0 {
				httperr.BadRequest(w, "queue/details: invalid seriesId")
				return
			}
			seriesID = &n
		}
		var episodeIDs []int
		for _, raw := range q["episodeIds"] {
			n, err := strconv.Atoi(raw)
			if err != nil || n <= 0 {
				httperr.BadRequest(w, fmt.Sprintf("queue/details: invalid episodeIds entry %q", raw))
				return
			}
			episodeIDs = append(episodeIDs, n)
		}
		rows, err := lister.QueueDetails(r.Context(), seriesID, episodeIDs)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("queue/details: %w", err), nil)
			return
		}
		if rows == nil {
			rows = []QueueResource{}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(rows)
		_, _ = w.Write(buf)
	})
}

// QueuePagedLister is the read side the handler depends on. The
// production Go *queue.Service does not yet expose a paged surface,
// so cmd/sonarr substitutes an empty fixture today. When the paged
// port lands, *queue.Service grows a method that satisfies this
// interface and the production wiring takes effect — same shape
// the blocklist + history adapters use.
type QueuePagedLister interface {
	PagedQueue(ctx context.Context, req PagingRequest, filter QueueFilter) ([]QueueResource, int, error)
}

// QueueFilter carries the parsed queue filter query params (C#
// `QueueController.GetQueue` at
// `src/Sonarr.Api.V3/Queue/QueueController.cs:141,173-217`). The lister
// implementation translates these wire-level values into the domain
// filter and applies it before sort + pagination. Every slice is "no
// filter" when empty; Protocol is "no filter" when "".
type QueueFilter struct {
	// SeriesIDs — `seriesIds` repeated int query param.
	SeriesIDs []int
	// Protocol — `protocol` single enum query param ("usenet" /
	// "torrent", case-insensitive). "" = no filter.
	Protocol string
	// Languages — `languages` repeated int query param (language ids).
	Languages []int
	// Quality — `quality` repeated int query param (quality ids).
	Quality []int
	// Status — `status` repeated enum query param (QueueStatus names,
	// case-insensitive camelCase).
	Status []string
	// IncludeUnknownSeriesItems — `includeUnknownSeriesItems` bool query
	// param (default false → series-less rows are excluded).
	IncludeUnknownSeriesItems bool
}

// parseQueueFilter extracts the queue filter query params. Mirrors the
// C# `[FromQuery]` binding of `int[] seriesIds` / `DownloadProtocol?
// protocol` / `int[] languages` / `int[] quality` / `QueueStatus[]
// status` + `bool includeUnknownSeriesItems` at
// `QueueController.cs:141`. Repeated params (`?seriesIds=1&seriesIds=2`)
// and unparseable ints are tolerated (skipped) — the controller's model
// binder behaves the same.
func parseQueueFilter(r *http.Request) QueueFilter {
	q := r.URL.Query()
	parseInts := func(key string) []int {
		var out []int
		for _, v := range q[key] {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				out = append(out, n)
			}
		}
		return out
	}
	return QueueFilter{
		SeriesIDs:                 parseInts("seriesIds"),
		Protocol:                  strings.TrimSpace(q.Get("protocol")),
		Languages:                 parseInts("languages"),
		Quality:                   parseInts("quality"),
		Status:                    q["status"],
		IncludeUnknownSeriesItems: q.Get("includeUnknownSeriesItems") == "true",
	}
}

// QueueHandler returns the http.Handler for GET /api/v3/queue.
// Mirrors `QueueController.GetQueue` at
// `src/Sonarr.Api.V3/Queue/QueueController.cs:139-171`. Default
// sortKey is "timeleft" (the C# controller's hard-coded fallback
// inside MapToPagingSpec); default direction "descending" comes
// from the shared paging-request defaults.
//
// The full C# controller dispatches on includeSeries /
// includeEpisode / seriesIds / protocol / languages / quality /
// status query params. J348 wires the includeSeries + includeEpisode
// subresource gating (both default false per C#
// `QueueController.cs:141`); the remaining filter params (seriesIds /
// protocol / languages / quality / status) are still DEFERRED — only
// the default-paging branch is exercised by the recorded corpus.
//
// Subresource gating posture (J348): the QueuePagedLister always
// hydrates Series + Episode into each record (the queue row carries
// the domain objects); this handler strips them back to nil when the
// corresponding include flag is absent. Keeps the lister interface
// free of wire-shape concerns.
func QueueHandler(lister QueuePagedLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := parsePagingRequest(r, "timeleft")
		q := r.URL.Query()
		includeSeries := q.Get("includeSeries") == "true"
		includeEpisode := q.Get("includeEpisode") == "true"
		records, total, err := lister.PagedQueue(r.Context(), req, parseQueueFilter(r))
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("queue: %w", err), nil)
			return
		}
		if records == nil {
			records = []QueueResource{}
		}
		// J348 — strip the always-hydrated subresources when the client
		// didn't request them (C# gates the projection on these flags).
		// J352 — the v3 wire carries only the SINGULAR Episode; strip the
		// plural Episodes (a v5-only field the shared adapter hydrates)
		// unconditionally so the v3 surface stays faithful.
		for i := range records {
			records[i].Episodes = nil
			if !includeSeries {
				records[i].Series = nil
			}
			if !includeEpisode {
				records[i].Episode = nil
			}
		}
		resp := PagingResource[QueueResource]{
			Page: req.Page, PageSize: req.PageSize,
			Records: records, SortDirection: req.SortDirection,
			SortKey: req.SortKey, TotalRecords: total,
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(resp)
		_, _ = w.Write(buf)
	})
}

// QueueHandlerV5 returns GET /api/v5/queue. Mirrors C#
// `Sonarr.Api.V5.Queue.QueueController.GetQueue` at
// `src/Sonarr.Api.V5/Queue/QueueController.cs:141-173`. The v5
// controller replaces v3's `includeSeries` + `includeEpisode` bool
// pair with a single `includeSubresources` enum-array query param
// (`QueueSubresource{Series, Episodes}`) — `includeSeries =
// includeSubresources.Contains(Series)` (`:170`) + `includeEpisodes =
// includeSubresources.Contains(Episodes)` (`:171`).
//
// Lives in the apiv3 package (alongside the shared QueueResource +
// the unexported parsePagingRequest) and is aliased from the
// internal/api/v5/queue package. The shared queuePagedAdapter
// hydrates Series + Episode (singular) + Episodes (plural); this
// handler strips them to the v5 surface shape:
//
//   - Series — kept iff `includeSubresources` contains "series".
//   - Episodes (plural) — kept iff it contains "episodes".
//   - Episode (singular) — stripped unconditionally; the v5
//     QueueResource (QueueResource.cs:13-42) dropped the singular
//     Episode field in favour of the plural Episodes list.
//
// Enum values arrive camelCased per STJson's
// `JsonStringEnumConverter(CamelCase)` — `?includeSubresources=series`
// / `&includeSubresources=episodes` (repeated). Parsing is
// case-insensitive for client tolerance.
//
// J352.
func QueueHandlerV5(lister QueuePagedLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := parsePagingRequest(r, "timeleft")
		includeSeries, includeEpisodes := false, false
		for _, v := range r.URL.Query()["includeSubresources"] {
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "series":
				includeSeries = true
			case "episodes":
				includeEpisodes = true
			}
		}
		records, total, err := lister.PagedQueue(r.Context(), req, parseQueueFilter(r))
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("queue: %w", err), nil)
			return
		}
		if records == nil {
			records = []QueueResource{}
		}
		// Strip to the v5 surface shape: drop the singular Episode
		// always; gate Series + plural Episodes on the enum.
		for i := range records {
			records[i].Episode = nil
			if !includeSeries {
				records[i].Series = nil
			}
			if !includeEpisodes {
				records[i].Episodes = nil
			}
		}
		resp := PagingResource[QueueResource]{
			Page: req.Page, PageSize: req.PageSize,
			Records: records, SortDirection: req.SortDirection,
			SortKey: req.SortKey, TotalRecords: total,
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(resp)
		_, _ = w.Write(buf)
	})
}
