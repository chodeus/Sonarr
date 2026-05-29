package v3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/queue"
	"sonarr.tv/sonarr/internal/signalr"
)

// QueueRemoveOptions carries the DELETE /api/v3/queue query flags into
// the [QueueItemRemover]. Mirrors the C# `QueueController.Remove*`
// action params (`QueueController.cs:75`): removeFromClient defaults
// TRUE, the rest default FALSE.
type QueueRemoveOptions struct {
	RemoveFromClient bool
	Blocklist        bool
	SkipRedownload   bool
	ChangeCategory   bool
}

// QueueItemRemover is the optional-extension surface (J395) that applies
// the real removal side-effects — download-client RemoveItem /
// MarkItemAsImported, blocklist, redownload — then removes the queue row
// from the cache. The delete handlers discover it on their writer dep;
// when absent they fall back to the cache-only [QueueWriter.Remove]
// (pre-J395 behaviour). `removed` is false when no queue row matched the
// id (→ 404).
type QueueItemRemover interface {
	RemoveQueueItem(ctx context.Context, id int, opts QueueRemoveOptions) (removed bool, err error)
}

// ErrQueueDownloadClientUnavailable is returned by a [QueueItemRemover]
// when removeFromClient/changeCategory was requested but the queue
// item's download client can't be resolved/constructed — the handlers
// map it to 400, mirroring C# `BadRequestException` at
// `QueueController.cs:338-342`.
var ErrQueueDownloadClientUnavailable = errors.New("queue: download client unavailable")

// queueRemoveOptionsFromQuery parses the four removal flags. C# defaults:
// removeFromClient=true, blocklist/skipRedownload/changeCategory=false.
func queueRemoveOptionsFromQuery(q url.Values) QueueRemoveOptions {
	return QueueRemoveOptions{
		RemoveFromClient: q.Get("removeFromClient") != "false",
		Blocklist:        q.Get("blocklist") == "true",
		SkipRedownload:   q.Get("skipRedownload") == "true",
		ChangeCategory:   q.Get("changeCategory") == "true",
	}
}

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

		// J395: when the writer applies real removal side-effects
		// (download-client RemoveItem / changeCategory / blocklist),
		// delegate the whole removal to it. Otherwise fall back to the
		// cache-only Find+Remove path.
		if remover, ok := svc.(QueueItemRemover); ok && remover != nil {
			removed, err := remover.RemoveQueueItem(r.Context(), id, queueRemoveOptionsFromQuery(r.URL.Query()))
			if err != nil {
				if errors.Is(err, ErrQueueDownloadClientUnavailable) {
					httperr.BadRequest(w, err.Error())
					return
				}
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("queue: remove %d: %w", id, err), nil)
				return
			}
			if !removed {
				httperr.NotFound(w, fmt.Sprintf("queue %d not found", id))
				return
			}
			fireQueueDeleted(r, broadcastDeleted, id)
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
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
		fireQueueDeleted(r, broadcastDeleted, id)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
}

// fireQueueDeleted broadcasts the SignalR queue/deleted event for id when
// a broadcaster is wired (no-op otherwise).
func fireQueueDeleted(r *http.Request, broadcastDeleted func(id int), id int) {
	if broadcastDeleted == nil {
		return
	}
	signalr.FireAndLog(httperr.LoggerFromRequest(r), "queue", "deleted", func() error {
		broadcastDeleted(id)
		return nil
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
// The 4 query params apply per-id via [QueueItemRemover] (J395) when the
// writer implements it; otherwise the loop falls back to the cache-only
// Remove. Returns 200 + `{}`. Empty Ids → 400 with "ids must be provided".
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
		remover, hasRemover := svc.(QueueItemRemover)
		opts := queueRemoveOptionsFromQuery(r.URL.Query())
		for _, id := range body.IDs {
			if hasRemover && remover != nil {
				// Per-id side-effects; a client-unavailable error on one
				// id is logged and the loop continues (C# RemoveMany
				// per-item try/catch posture).
				removed, err := remover.RemoveQueueItem(r.Context(), id, opts)
				if err != nil {
					httperr.LoggerFromRequest(r).Warn("queue/bulk: remove failed", "id", id, "err", err)
					continue
				}
				if removed {
					fireQueueDeleted(r, broadcastDeleted, id)
				}
				continue
			}
			if svc.Remove(id) {
				fireQueueDeleted(r, broadcastDeleted, id)
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
