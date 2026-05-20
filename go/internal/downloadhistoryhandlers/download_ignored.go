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

// HandleDownloadIgnored ports
// `src/NzbDrone.Core/Download/History/DownloadHistoryService.cs:211-228`
// — `Handle(DownloadIgnoredEvent)`. Single DownloadIgnored row per
// event. The C# tree writes unconditionally (no gates beyond
// dereferencing DownloadClientInfo); the Go port nil-guards the
// dereference rather than NPEing, consistent with the J137-J138
// defensive-divergence posture.
//
// # Entry shape (C# `:213-226`)
//
//   - EventType = [downloadhistory.EventTypeIgnored]
//   - SeriesID = ev.SeriesID
//   - DownloadID = ev.DownloadID
//   - SourceTitle = ev.SourceTitle
//   - Date = time.Now().UTC()
//   - Protocol = ev.DownloadClientInfo.Protocol
//   - DownloadClientID = ev.DownloadClientInfo.ID
//   - Release = "" (no Release payload on ignored events)
//   - Data = JSON {DownloadClient, DownloadClientName} sourced from
//     ev.DownloadClientInfo (Type / Name fields)
//
// Distinct from the historyhandlers twin: that one walks EpisodeIDs
// and writes one row per id, plus a 7-key Data dictionary sourced
// from TrackedDownload. The downloadhistory variant is per-event and
// has no Message / ReleaseGroup / Size / Indexer / ReleaseType keys
// — the C# DownloadHistory Data shape is intentionally narrower.
//
// # Defensive arms
//
// nil-writer → typed error. nil-event → no-op. nil-DownloadClientInfo
// collapses Protocol / DownloadClientID to 0 and Data values to "".
// Add error propagates wrapped.
func HandleDownloadIgnored(ctx context.Context, w EntryWriter, ev *trackeddownloads.DownloadIgnoredEvent) error {
	if w == nil {
		return errors.New("downloadhistoryhandlers: HandleDownloadIgnored: nil writer")
	}
	if ev == nil {
		return nil
	}

	protocol := 0
	downloadClientID := 0
	clientType := ""
	clientName := ""
	if ev.DownloadClientInfo != nil {
		protocol = int(ev.DownloadClientInfo.Protocol)
		downloadClientID = ev.DownloadClientInfo.ID
		clientType = ev.DownloadClientInfo.Type
		clientName = ev.DownloadClientInfo.Name
	}

	dataJSON, err := encodeIgnoredData(clientType, clientName)
	if err != nil {
		return fmt.Errorf("downloadhistoryhandlers: HandleDownloadIgnored: encode data: %w", err)
	}

	entry := &downloadhistory.Entry{
		EventType:        int(downloadhistory.EventTypeIgnored),
		SeriesID:         ev.SeriesID,
		DownloadID:       ev.DownloadID,
		SourceTitle:      ev.SourceTitle,
		Date:             time.Now().UTC(),
		Protocol:         protocol,
		DownloadClientID: downloadClientID,
		Data:             dataJSON,
	}
	if _, err := w.Add(ctx, entry); err != nil {
		return fmt.Errorf("downloadhistoryhandlers: HandleDownloadIgnored: %w", err)
	}
	return nil
}

// encodeIgnoredData builds the 2-key Data dictionary the C# handler
// writes at `:224-225` and JSON-marshals it.
func encodeIgnoredData(clientType, clientName string) (string, error) {
	b, err := json.Marshal(map[string]string{
		"DownloadClient":     clientType,
		"DownloadClientName": clientName,
	})
	if err != nil {
		return "", err
	}
	return string(b), nil
}
