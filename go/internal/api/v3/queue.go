package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

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
	ID                                  int           `json:"id,omitempty"`
	SeriesID                            *int          `json:"seriesId,omitempty"`
	EpisodeID                           *int          `json:"episodeId,omitempty"`
	Title                               string        `json:"title,omitempty"`
	Status                              string        `json:"status,omitempty"`
	DownloadID                          string        `json:"downloadId,omitempty"`
	Protocol                            string        `json:"protocol,omitempty"`
	Languages                           []LanguageRef `json:"languages,omitempty"`
	Size                                int64         `json:"size,omitempty"`
	Indexer                             string        `json:"indexer,omitempty"`
	DownloadClient                      string        `json:"downloadClient,omitempty"`
	DownloadClientHasPostImportCategory bool          `json:"downloadClientHasPostImportCategory,omitempty"`
	OutputPath                          string        `json:"outputPath,omitempty"`
	ErrorMessage                        string        `json:"errorMessage,omitempty"`
	CustomFormatScore                   int           `json:"customFormatScore,omitempty"`
	EpisodesWithFilesCount              int           `json:"episodesWithFilesCount,omitempty"`
	IsFullSeason                        bool          `json:"isFullSeason,omitempty"`
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
	PagedQueue(ctx context.Context, req PagingRequest) ([]QueueResource, int, error)
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
// status query params; only the default-paging branch is exercised
// by the recorded corpus today, so the other branches are DEFERRED.
func QueueHandler(lister QueuePagedLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := parsePagingRequest(r, "timeleft")
		records, total, err := lister.PagedQueue(r.Context(), req)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("queue: %w", err), nil)
			return
		}
		if records == nil {
			records = []QueueResource{}
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
