package v3

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/queue"
	"sonarr.tv/sonarr/internal/signalr"
)

// QueueWriter is the narrow write surface for the queue DELETE
// endpoints. *queue.Service satisfies it via Find + Remove.
//
// Find returns nil when no row matches the supplied id; Remove
// returns false when the silent-on-miss path fired (mirrors C#
// `_queue.SingleOrDefault` + `List.Remove(null)` returning false
// without raising — preserved verbatim per queue.Service.Remove
// doc-comment).
type QueueWriter interface {
	Find(id int) *queue.Queue
	Remove(id int) bool
}

// QueueDeleteHandler returns DELETE /api/v3/queue/{id}. Mirrors C#
// `QueueController.RemoveAction` at QueueController.cs:75-96.
//
// Query params (all PARSED-AND-IGNORED — DEFERRED until the
// download-client RemoveItem + tracked-downloads + pending-release
// service join port):
//
//   - removeFromClient=true  — call DownloadClient.RemoveItem on the
//     upstream client (qBittorrent/Sabnzbd/etc.).
//   - blocklist=false        — add the release to the blocklist.
//   - skipRedownload=false   — skip the auto-redownload after blocklisting.
//   - changeCategory=false   — change category on the download client.
//
// 5.E2a ships the queue-row removal only. The upstream-client side-
// effects are documented in doc-comments + the gap is visible in
// the audit's item #1 "queue grab+delete" follow-up.
//
// The C# action distinguishes pending-release rows from
// tracked-downloads — the Go port collapses to queue.Service.Remove
// which operates on the unified queue cache (pending + tracked are
// both rebuilt into the cache by the queue.Manager rebuild pipeline).
//
// Returns 200 + `{}` on success, 404 when no queue row matched the
// id (matches C# NotFoundException at :91).
func QueueDeleteHandler(svc QueueWriter, broadcastDeleted func(id int)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			httperr.BadRequest(w, "queue: invalid id")
			return
		}
		if svc.Find(id) == nil {
			httperr.NotFound(w, fmt.Sprintf("queue %d not found", id))
			return
		}
		if !svc.Remove(id) {
			// Race: Find returned non-nil but Remove returned false.
			// Treat as 404 (something else cleared the row between
			// the two calls).
			httperr.NotFound(w, fmt.Sprintf("queue %d not found", id))
			return
		}
		if broadcastDeleted != nil {
			signalr.FireAndLog(httperr.LoggerFromRequest(r), "queue", "deleted", func() error {
				broadcastDeleted(id)
				return nil
			})
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
}

// QueueBulkDeleteResource mirrors C# `QueueBulkResource` at
// `src/Sonarr.Api.V3/Queue/QueueBulkResource.cs`. Just an Ids array
// (no per-row optional fields — the per-row side-effects come from
// query params, not the body).
type QueueBulkDeleteResource struct {
	IDs []int `json:"ids"`
}

// QueueBulkDeleteHandler returns DELETE /api/v3/queue/bulk. Mirrors
// C# `QueueController.RemoveMany` at QueueController.cs:98-137.
// Loops queue.Service.Remove per id; ids that don't match are
// silently skipped (matches C# behaviour where the pending +
// tracked lookup helpers return null and the loop just skips).
//
// Same 4 query params as the single-row delete are PARSED-AND-IGNORED.
//
// Returns 200 + `{}`. Empty Ids → 400 with "ids must be provided".
func QueueBulkDeleteHandler(svc QueueWriter, broadcastDeleted func(id int)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeQueueBulkDeleteBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("queue/bulk: malformed body: %v", err))
			return
		}
		if len(body.IDs) == 0 {
			httperr.BadRequest(w, "ids must be provided")
			return
		}
		for _, id := range body.IDs {
			if svc.Remove(id) && broadcastDeleted != nil {
				id := id
				signalr.FireAndLog(httperr.LoggerFromRequest(r), "queue", "deleted", func() error {
					broadcastDeleted(id)
					return nil
				})
			}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
}

func decodeQueueBulkDeleteBody(r *http.Request) (QueueBulkDeleteResource, error) {
	defer func() { _ = r.Body.Close() }()
	var out QueueBulkDeleteResource
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return QueueBulkDeleteResource{}, err
	}
	return out, nil
}
