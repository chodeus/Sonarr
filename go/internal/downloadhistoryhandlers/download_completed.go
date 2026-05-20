package downloadhistoryhandlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"sonarr.tv/sonarr/internal/downloadhistory"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

// HandleDownloadCompleted ports
// `src/NzbDrone.Core/Download/History/DownloadHistoryService.cs:165-184`
// — `Handle(DownloadCompletedEvent)`. Single DownloadImported row
// per event; closes the DownloadHistoryService event-bus arc at
// 6/6.
//
// # TrackedDownload required
//
// The C# tree at `:167` dereferences `message.TrackedDownload.DownloadItem`
// unconditionally and would NPE on a nil TrackedDownload. The Go
// port returns early when the event has no TrackedDownload — the
// row can't be attributed without Protocol + DownloadClientID, and
// the C# publishers at CompletedDownloadService.cs:226+271 always
// populate the field.
//
// # Entry shape (C# `:169-182`)
//
//   - EventType = [downloadhistory.EventTypeImported]
//     (the C# `DownloadImported` ordinal — distinct from
//     [downloadhistory.EventTypeFileImported] which the J137
//     EpisodeImported handler emits)
//   - SeriesID = ev.SeriesID
//   - DownloadID = TrackedDownload.DownloadItem.DownloadID
//   - SourceTitle = TrackedDownload.DownloadItem.Title
//   - Date = time.Now().UTC()
//   - Protocol = TrackedDownload.Protocol
//   - DownloadClientID = TrackedDownload.DownloadClient (int row id)
//   - Release = "" (no Release payload on completed events — the
//     event's GrabbedReleaseInfo is for downstream subscribers,
//     not the audit-log row)
//   - Data = JSON {DownloadClient, DownloadClientName} sourced from
//     TrackedDownload.DownloadItem.DownloadClientInfo
//
// # Two DownloadImported event ordinals
//
// The DownloadHistory schema's EventType column is the C#
// `DownloadHistoryEventType` enum which has BOTH `DownloadImported`
// (this handler) AND `FileImported` (J137's EpisodeImported
// handler) at distinct ordinals. The naming is confusingly close
// in the C# tree — DownloadImported is the lifecycle-complete
// marker (the download finished; the importer has been triggered),
// FileImported is the per-file-imported audit row (one per
// successfully-imported episode file). Both ports preserve the
// distinction verbatim.
//
// # Nil-DownloadItem / DownloadClientInfo defence
//
// Consistent with the J137-J139 defensive divergence: nil links
// in TrackedDownload → DownloadItem → DownloadClientInfo fall back
// to zero / empty strings rather than NPEing. The C# tree would
// crash CompletedDownloadService on a payload defect; the Go port
// treats it as recoverable.
//
// # Defensive arms
//
// nil-writer → typed error. nil-event / nil-TrackedDownload →
// no-op. nil-DownloadItem → no-op (no DownloadID to write — the
// audit-log row has nothing to link). Add error propagates wrapped.
func HandleDownloadCompleted(ctx context.Context, w EntryWriter, ev *trackeddownloads.DownloadCompletedEvent) error {
	if w == nil {
		return errors.New("downloadhistoryhandlers: HandleDownloadCompleted: nil writer")
	}
	if ev == nil || ev.TrackedDownload == nil {
		return nil
	}
	td := ev.TrackedDownload
	if td.DownloadItem == nil {
		return nil
	}

	clientType, clientName := completedClientFields(td)
	dataJSON, err := encodeCompletedData(clientType, clientName)
	if err != nil {
		return fmt.Errorf("downloadhistoryhandlers: HandleDownloadCompleted: encode data: %w", err)
	}

	entry := &downloadhistory.Entry{
		EventType:        int(downloadhistory.EventTypeImported),
		SeriesID:         ev.SeriesID,
		DownloadID:       td.DownloadItem.DownloadID,
		SourceTitle:      td.DownloadItem.Title,
		Date:             time.Now().UTC(),
		Protocol:         td.Protocol,
		DownloadClientID: td.DownloadClient,
		Data:             dataJSON,
	}
	if _, err := w.Add(ctx, entry); err != nil {
		return fmt.Errorf("downloadhistoryhandlers: HandleDownloadCompleted: %w", err)
	}
	return nil
}

// completedClientFields walks TrackedDownload.DownloadItem.DownloadClientInfo
// with empty-string fallback on a nil ClientInfo. Mirrors the
// failedClientFields chain from download_failed.go but the upstream
// guards differ — completed handler gates earlier on nil-DownloadItem.
func completedClientFields(td *trackeddownloads.TrackedDownload) (clientType, clientName string) {
	if td.DownloadItem.DownloadClientInfo == nil {
		return "", ""
	}
	return td.DownloadItem.DownloadClientInfo.Type, td.DownloadItem.DownloadClientInfo.Name
}

// encodeCompletedData builds the 2-key Data dictionary the C# handler
// writes at `:180-181` and JSON-marshals it.
func encodeCompletedData(clientType, clientName string) (string, error) {
	b, err := json.Marshal(map[string]string{
		"DownloadClient":     clientType,
		"DownloadClientName": clientName,
	})
	if err != nil {
		return "", err
	}
	return string(b), nil
}
