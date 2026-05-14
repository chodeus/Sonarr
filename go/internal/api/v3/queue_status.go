package v3

import (
	"context"
	"encoding/json"
	"net/http"

	"sonarr.tv/sonarr/internal/queue"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

// QueueStatusResource is the JSON shape produced by
// GET /api/v3/queue/status. Mirrors
// `src/Sonarr.Api.V3/Queue/QueueStatusResource.cs`.
//
// Id is intentionally absent on the wire — the C# RestResource base
// class declares `[JsonIgnore(WhenWritingDefault)] virtual int Id`,
// and QueueStatusResource never sets it (no row id concept for a
// status summary). The Go port simply omits the field from the
// struct.
//
// Field-source mapping (C# → Go):
//
//	totalCount      → queue.Count + pending.Count
//	count           → queue.Count(Series != nil) + pending.Count
//	unknownCount    → queue.Count(Series == nil)
//	errors          → queue.Any(Series != nil && Status == Error)
//	warnings        → queue.Any(Series != nil && Status == Warning)
//	unknownErrors   → queue.Any(Series == nil && Status == Error)
//	unknownWarnings → queue.Any(Series == nil && Status == Warning)
type QueueStatusResource struct {
	TotalCount      int  `json:"totalCount"`
	Count           int  `json:"count"`
	UnknownCount    int  `json:"unknownCount"`
	Errors          bool `json:"errors"`
	Warnings        bool `json:"warnings"`
	UnknownErrors   bool `json:"unknownErrors"`
	UnknownWarnings bool `json:"unknownWarnings"`
}

// QueueLister narrows the in-process queue surface to the snapshot
// the status handler needs. *queue.Service.GetQueue() satisfies this
// once a thin adapter lifts its returned slice through the interface.
type QueueLister interface {
	GetQueue() []queue.Queue
}

// PendingLister returns the count of pending-release rows currently
// queued. Mirrors the C# `_pendingReleaseService.GetPendingQueue().Count`
// usage — only the cardinality is needed, not the row shape, so the
// interface is intentionally a single int. When the Go
// pendingreleases.Service grows a queue-projection method, the
// adapter in host_wiring.go satisfies this directly.
type PendingLister interface {
	PendingCount(ctx context.Context) (int, error)
}

// QueueStatusHandler returns the http.Handler for
// GET /api/v3/queue/status. Mirrors
// `QueueStatusController.GetQueueStatus` at
// `src/Sonarr.Api.V3/Queue/QueueStatusController.cs:45-68`.
//
// On an empty DB every count is zero and every bool is false — the
// recorded golden's exact shape.
func QueueStatusHandler(queueLister QueueLister, pending PendingLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := BuildQueueStatus(r.Context(), queueLister, pending)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(body)
		_, _ = w.Write(buf)
	})
}

// BuildQueueStatus projects the current queue + pending-release
// state into a QueueStatusResource. Extracted from QueueStatusHandler
// so the SignalR queue/status subscriber can re-use the same
// projection logic without re-rolling the field-by-field summing.
//
// Errors from the pending lister fall back to "0 pending" rather
// than propagating — matches the C# defensive read posture (the
// pending service caches in-process and never fails over a
// network).
func BuildQueueStatus(ctx context.Context, queueLister QueueLister, pending PendingLister) QueueStatusResource {
	rows := queueLister.GetQueue()
	pendingCount := 0
	if pending != nil {
		if n, err := pending.PendingCount(ctx); err == nil {
			pendingCount = n
		}
	}
	body := QueueStatusResource{TotalCount: len(rows) + pendingCount}
	for _, q := range rows {
		hasSeries := q.Series != nil
		status := trackeddownloads.StatusOK
		if q.TrackedDownloadStatus != nil {
			status = *q.TrackedDownloadStatus
		}
		if hasSeries {
			body.Count++
			if status == trackeddownloads.StatusError {
				body.Errors = true
			}
			if status == trackeddownloads.StatusWarning {
				body.Warnings = true
			}
		} else {
			body.UnknownCount++
			if status == trackeddownloads.StatusError {
				body.UnknownErrors = true
			}
			if status == trackeddownloads.StatusWarning {
				body.UnknownWarnings = true
			}
		}
	}
	body.Count += pendingCount // pending releases always count as Series-known per C# `pending.Count`
	return body
}
