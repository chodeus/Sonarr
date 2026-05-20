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

// HandleDownloadFailed ports
// `src/NzbDrone.Core/Download/History/DownloadHistoryService.cs:186-209`
// — `Handle(DownloadFailedEvent)`. Single DownloadFailed row per
// event. Gated on non-nil TrackedDownload (the C# tree at `:189-192`
// returns early — failed downloads without a tracked record have no
// Protocol / DownloadClientID to attribute, so the audit row is
// useless).
//
// # Entry shape (C# `:194-208`)
//
//   - EventType = [downloadhistory.EventTypeFailed]
//   - SeriesID = ev.SeriesID
//   - DownloadID = ev.DownloadID
//   - SourceTitle = ev.SourceTitle
//   - Date = time.Now().UTC()
//   - Protocol = TrackedDownload.Protocol
//   - DownloadClientID = TrackedDownload.DownloadClient (the int row
//     id; not to be confused with TrackedDownload.DownloadItem.
//     DownloadClientInfo.Type which is the class-identifier string)
//   - Release = "" (no Release payload on failed events)
//   - Data = JSON {DownloadClient, DownloadClientName} sourced from
//     TrackedDownload.DownloadItem.DownloadClientInfo
//
// # Nil-DownloadItem / DownloadClientInfo defence
//
// The C# tree dereferences `TrackedDownload.DownloadItem.DownloadClientInfo.Type`
// unconditionally and would NPE if either link is nil. The Go port
// nil-guards both links with empty-string fallback — consistent with
// the J137 imported handler's defence-in-depth posture against
// payload defects.
//
// # Defensive arms
//
// nil-writer → typed error. nil-event / nil-TrackedDownload → no-op.
// Add error propagates wrapped.
func HandleDownloadFailed(ctx context.Context, w EntryWriter, ev *trackeddownloads.DownloadFailedEvent) error {
	if w == nil {
		return errors.New("downloadhistoryhandlers: HandleDownloadFailed: nil writer")
	}
	if ev == nil || ev.TrackedDownload == nil {
		return nil
	}

	td := ev.TrackedDownload
	clientType, clientName := failedClientFields(td)
	dataJSON, err := encodeFailedData(clientType, clientName)
	if err != nil {
		return fmt.Errorf("downloadhistoryhandlers: HandleDownloadFailed: encode data: %w", err)
	}

	entry := &downloadhistory.Entry{
		EventType:        int(downloadhistory.EventTypeFailed),
		SeriesID:         ev.SeriesID,
		DownloadID:       ev.DownloadID,
		SourceTitle:      ev.SourceTitle,
		Date:             time.Now().UTC(),
		Protocol:         td.Protocol,
		DownloadClientID: td.DownloadClient,
		Data:             dataJSON,
	}
	if _, err := w.Add(ctx, entry); err != nil {
		return fmt.Errorf("downloadhistoryhandlers: HandleDownloadFailed: %w", err)
	}
	return nil
}

// failedClientFields walks TrackedDownload → DownloadItem →
// DownloadClientInfo, surfacing (Type, Name) with empty-string
// fallback on any nil link.
func failedClientFields(td *trackeddownloads.TrackedDownload) (clientType, clientName string) {
	if td == nil || td.DownloadItem == nil || td.DownloadItem.DownloadClientInfo == nil {
		return "", ""
	}
	return td.DownloadItem.DownloadClientInfo.Type, td.DownloadItem.DownloadClientInfo.Name
}

// encodeFailedData builds the 2-key Data dictionary the C# handler
// writes at `:205-206` and JSON-marshals it.
func encodeFailedData(clientType, clientName string) (string, error) {
	b, err := json.Marshal(map[string]string{
		"DownloadClient":     clientType,
		"DownloadClientName": clientName,
	})
	if err != nil {
		return "", err
	}
	return string(b), nil
}
