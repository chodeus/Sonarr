package v3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/download"
	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/pendingreleases"
	"sonarr.tv/sonarr/internal/series"
)

// PendingReleaseLookup is the narrow read interface the grab handlers
// depend on. `*pendingreleases.Repo.Get` (via the embedded
// `repo.Repository[Release]`) satisfies it.
//
// Audit row C.1.i-follow-5c (J236).
type PendingReleaseLookup interface {
	Get(ctx context.Context, id int) (*pendingreleases.Release, error)
}

// QueueGrabSeriesLookup is the narrow read interface to resolve a
// series row by ID. `*series.Service.GetSeries` satisfies it. Kept
// distinct from the other Series lookup interfaces in this package so
// callers can pass a narrower adapter.
type QueueGrabSeriesLookup interface {
	GetSeries(ctx context.Context, id int) (*series.Series, error)
}

// QueueGrabDispatcher is the narrow dispatch interface the handlers
// hand a reconstructed [decisionengine.Decision] to.
// `*download.ProcessDownloadDecisions.ProcessDecisions` satisfies it
// directly via Go's structural typing.
type QueueGrabDispatcher interface {
	ProcessDecisions(ctx context.Context, decisions []decisionengine.Decision) *download.ProcessedDecisions
}

// QueueBulkGrabResource is the JSON body shape for the bulk-grab
// endpoint. Mirrors C# `QueueBulkResource`'s `Ids` field — the only
// field the grab endpoint reads. Other QueueBulkResource consumers
// (delete) use a separate type already in this package.
type QueueBulkGrabResource struct {
	Ids []int `json:"ids"`
}

// QueueGrabHandler returns POST /api/v3/queue/grab/{id}.
//
// Mirrors `QueueActionController.Grab(int)` at C#
// `src/Sonarr.Api.V3/Queue/QueueActionController.cs:24-36`:
//
//  1. Find pending release by queue id.
//  2. 404 NotFound if missing.
//  3. Call `_downloadService.DownloadReport(remoteEpisode, null)` —
//     dispatches the grab to the matching download client.
//  4. Return empty body `{}` on success.
//
// The Go-port path differs from C# in one structural way: C# preserves
// the full `RemoteEpisode` object in-memory across the persistence
// boundary; Go-port serialises `ReleaseInfo` + `ParsedEpisodeInfo` to
// JSON in the `PendingReleases` row and rehydrates here at grab time.
// The Series row is re-loaded by ID (cheap; one query). Episodes are
// NOT rehydrated for this initial port — the dispatcher's grab path
// (see `download.ProcessDownloadDecisions.dispatch`) reads only
// `Subject.Release` + `Subject.Series` from the Subject; Episode-level
// metadata is consumed by the post-grab history stamping which is
// (a) decoupled via the bus from the grab itself and (b) re-resolves
// episodes from the persisted ParsedEpisodeInfo + Series at history-
// write time. So the minimal Subject built here is sufficient for the
// grab to land in the download client. Documented deferred — see slice
// note for the post-grab history-correlation arm.
//
// Audit row C.1.i-follow-5a / C.1.i-follow-5c (J236).
func QueueGrabHandler(pending PendingReleaseLookup, seriesLookup QueueGrabSeriesLookup, dispatcher QueueGrabDispatcher) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			httperr.BadRequest(w, "queue/grab: invalid id")
			return
		}
		if err := grabOne(r.Context(), id, pending, seriesLookup, dispatcher); err != nil {
			writeGrabErr(w, r, err, id)
			return
		}
		writeGrabOK(w)
	})
}

// QueueGrabBulkHandler returns POST /api/v3/queue/grab/bulk.
//
// Mirrors `QueueActionController.Grab(QueueBulkResource)` at C# `:38-55`:
// iterates ids; first 404 short-circuits the batch (the C# version
// throws on the first FindPendingQueueItem null, which translates to
// a 404 — Go-port preserves this). On success the matching grabs land
// and the body is `{}`.
//
// No transactional rollback if a mid-batch grab fails — same as C#
// (the C# version awaits each DownloadReport sequentially; a mid-batch
// failure leaves earlier grabs already-dispatched).
func QueueGrabBulkHandler(pending PendingReleaseLookup, seriesLookup QueueGrabSeriesLookup, dispatcher QueueGrabDispatcher) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body QueueBulkGrabResource
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			httperr.BadRequest(w, fmt.Sprintf("queue/grab/bulk: malformed body: %v", err))
			return
		}
		for _, id := range body.Ids {
			if err := grabOne(r.Context(), id, pending, seriesLookup, dispatcher); err != nil {
				writeGrabErr(w, r, err, id)
				return
			}
		}
		writeGrabOK(w)
	})
}

// grabOne implements the per-id grab pipeline shared by both
// handlers. Returns an error that callers route to the appropriate
// HTTP response via [writeGrabErr].
func grabOne(ctx context.Context, id int, pending PendingReleaseLookup, seriesLookup QueueGrabSeriesLookup, dispatcher QueueGrabDispatcher) error {
	row, err := pending.Get(ctx, id)
	if err != nil {
		// pendingreleases.ErrNotFound + repo.ErrNotFound both surface here;
		// callers translate to 404.
		return err
	}
	if row == nil {
		return pendingreleases.ErrNotFound
	}
	// Deserialise the persisted JSON columns.
	var release parser.ReleaseInfo
	if err := json.Unmarshal([]byte(row.ReleaseInfo), &release); err != nil {
		return fmt.Errorf("queue/grab: deserialise ReleaseInfo for pending %d: %w", id, err)
	}
	var parsedInfo parser.ParsedEpisodeInfo
	if row.ParsedEpisodeInfo != "" {
		if err := json.Unmarshal([]byte(row.ParsedEpisodeInfo), &parsedInfo); err != nil {
			// Empty/malformed ParsedEpisodeInfo is non-fatal for the
			// grab itself (dispatcher reads Release for protocol +
			// URL); log via context not required because the dispatcher
			// will surface its own errors downstream.
			parsedInfo = parser.ParsedEpisodeInfo{}
		}
	}
	// Resolve the series row by ID. nil-result handled below; lookup
	// errors propagate as 500.
	var seriesRow *series.Series
	if row.SeriesID > 0 && seriesLookup != nil {
		s, lerr := seriesLookup.GetSeries(ctx, row.SeriesID)
		if lerr != nil && !errors.Is(lerr, series.ErrNotFound) {
			return fmt.Errorf("queue/grab: series lookup for pending %d (series %d): %w", id, row.SeriesID, lerr)
		}
		seriesRow = s
	}
	// Build the minimal Subject. Episodes are NOT rehydrated for this
	// initial port — see handler doc-comment for rationale.
	subject := &decisionengine.Subject{
		Release:           &release,
		ParsedEpisodeInfo: &parsedInfo,
		Series:            seriesRow,
		// DownloadAllowed must be true to clear the dispatcher's
		// isQualified gate (the C# `IsQualifiedReport` predicate at
		// `:161-165`). Pending releases come from previous decision
		// passes that approved them — re-approve here for dispatch.
		DownloadAllowed: true,
	}
	decision := decisionengine.NewDecision(subject)
	// Dispatch through the existing ProcessDownloadDecisions
	// orchestrator. Returns *ProcessedDecisions which carries Grabbed
	// / Pending / Rejected splits; we don't currently surface per-id
	// outcome to the caller (matches C# which also returns `new { }`
	// regardless of dispatch outcome — the grab "command" succeeded
	// even if the underlying download client failed; surfacing the
	// failure is the job of the SignalR-broadcast `GrabFailed` /
	// `Pending` event subscribers).
	_ = dispatcher.ProcessDecisions(ctx, []decisionengine.Decision{decision})
	return nil
}

// writeGrabErr maps an internal error to the appropriate HTTP
// response. NotFound → 404 (matches C# `NotFoundException`); anything
// else → 500.
func writeGrabErr(w http.ResponseWriter, r *http.Request, err error, id int) {
	if errors.Is(err, pendingreleases.ErrNotFound) {
		httperr.NotFound(w, fmt.Sprintf("queue/grab: pending release %d not found", id))
		return
	}
	httperr.WriteErr(w, r, http.StatusInternalServerError, err, nil)
}

// writeGrabOK emits the C# `return new { }` shape — 200 + `{}` body
// with `application/json` Content-Type.
func writeGrabOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{}`))
}
