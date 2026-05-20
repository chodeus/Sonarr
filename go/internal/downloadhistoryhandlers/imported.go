package downloadhistoryhandlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/downloadhistory"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/quality"
)

// LookupDownloadID mirrors the historyhandlers interface of the same
// name — the J127 [history.Service.FindDownloadID] surface, narrowed
// to the one method this handler reaches for when the event's
// downloadID is empty. Tests inject a stub.
type LookupDownloadID interface {
	FindDownloadID(ctx context.Context, seriesID int, qualityModel quality.Model, episodeIDs []int) (string, error)
}

// HandleEpisodeImportedOption tunes [HandleEpisodeImported] via the
// variadic-options pattern. The current options slot only carries the
// FindDownloadID fallback; future tuning (custom clock, per-row
// metadata) plugs in here without breaking the signature.
type HandleEpisodeImportedOption func(*importedConfig)

// importedConfig is the resolved options bag.
type importedConfig struct {
	lookup LookupDownloadID
}

// WithDownloadIDLookup wires a [LookupDownloadID] for the empty-
// downloadID fallback path. nil is silently dropped.
//
// Mirrors [historyhandlers.WithDownloadIDLookup] (slice 5.J127) — the
// composition root threads the SAME *history.Service into both
// subscribers so the fallback walks one shared audit-log surface.
func WithDownloadIDLookup(l LookupDownloadID) HandleEpisodeImportedOption {
	return func(c *importedConfig) {
		if l != nil {
			c.lookup = l
		}
	}
}

// HandleEpisodeImported ports
// `src/NzbDrone.Core/Download/History/DownloadHistoryService.cs:124-163`
// — `Handle(EpisodeImportedEvent)`. Single FileImported row per event;
// gated on NewDownload AND non-empty downloadID (post-FindDownloadID-
// fallback).
//
// # Empty-DownloadID fallback (C# `:131-139`)
//
// When the event payload has no DownloadID (manual import / API
// import without a client item), consult the wired
// [LookupDownloadID]. Without a wired lookup or when the lookup
// declines, the C# tree returns early at `:141-144` — the audit-log
// row is not written. This mirrors the historyhandlers J127 fallback
// posture: lookup failures are non-fatal (logged at the lookup
// boundary; the handler proceeds with empty downloadID and bails).
//
// # Entry shape (C# `:146-162`)
//
//   - EventType = [downloadhistory.EventTypeFileImported]
//   - SeriesID  = ImportedFile.SeriesID
//   - DownloadID = resolved (event id or fallback id)
//   - SourceTitle = LocalEpisode.Path (NOT the .RelativePath — the
//     C# tree uses the dropped-folder absolute path as the audit
//     label here)
//   - Date      = time.Now().UTC()
//   - Protocol  = DownloadClientItem.DownloadClientInfo.Protocol
//   - DownloadClientID = DownloadClientItem.DownloadClientInfo.ID
//   - Release   = "" (no Release payload on import events)
//   - Data      = JSON map with 4 keys: DownloadClient,
//     DownloadClientName, SourcePath, DestinationPath
//
// # DownloadClientItem dereference
//
// When the event's DownloadClientItem is nil (manual / library-rescan
// imports), Protocol + DownloadClientID + the Data-map's
// DownloadClient* values default to zero / empty. The C# tree would
// NPE on `message.DownloadClientInfo.Protocol` in that case — the Go
// port defends against the nil with a typed-zero fallback rather than
// preserving the NPE, since the C# bug would crash the housekeeping
// pass outright and the Go port treats event-bus payload defects as
// recoverable.
//
// # Defensive arms
//
// nil-writer → typed error. nil-event / nil-LocalEpisode /
// nil-EpisodeFile / NewDownload=false / unresolved-DownloadID → no-op
// (no row, no error). Add error propagates wrapped.
func HandleEpisodeImported(ctx context.Context, w EntryWriter, ev *episodeimport.EpisodeImportedEvent, opts ...HandleEpisodeImportedOption) error {
	if w == nil {
		return errors.New("downloadhistoryhandlers: HandleEpisodeImported: nil writer")
	}
	if ev == nil || !ev.NewDownload {
		return nil
	}
	if ev.LocalEpisode == nil || ev.EpisodeFile == nil {
		return nil
	}
	le := ev.LocalEpisode

	cfg := importedConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	downloadID := ""
	if ev.DownloadClientItem != nil {
		downloadID = ev.DownloadClientItem.DownloadID
	}
	if strings.TrimSpace(downloadID) == "" && cfg.lookup != nil {
		downloadID = lookupDownloadIDFallback(ctx, cfg.lookup, ev.EpisodeFile.SeriesID, le)
	}
	if strings.TrimSpace(downloadID) == "" {
		return nil
	}

	protocol := 0
	downloadClientID := 0
	clientType := ""
	clientName := ""
	if ev.DownloadClientItem != nil && ev.DownloadClientItem.DownloadClientInfo != nil {
		protocol = int(ev.DownloadClientItem.DownloadClientInfo.Protocol)
		downloadClientID = ev.DownloadClientItem.DownloadClientInfo.ID
		clientType = ev.DownloadClientItem.DownloadClientInfo.Type
		clientName = ev.DownloadClientItem.DownloadClientInfo.Name
	}

	destinationPath := ""
	if le.Series != nil {
		destinationPath = filepath.Join(le.Series.Path, ev.EpisodeFile.RelativePath)
	}

	dataJSON, err := encodeImportedData(clientType, clientName, le.Path, destinationPath)
	if err != nil {
		return fmt.Errorf("downloadhistoryhandlers: HandleEpisodeImported: encode data: %w", err)
	}

	entry := &downloadhistory.Entry{
		EventType:        int(downloadhistory.EventTypeFileImported),
		SeriesID:         ev.EpisodeFile.SeriesID,
		DownloadID:       downloadID,
		SourceTitle:      le.Path,
		Date:             time.Now().UTC(),
		Protocol:         protocol,
		DownloadClientID: downloadClientID,
		Data:             dataJSON,
	}
	if _, err := w.Add(ctx, entry); err != nil {
		return fmt.Errorf("downloadhistoryhandlers: HandleEpisodeImported: %w", err)
	}
	return nil
}

// lookupDownloadIDFallback invokes the wired LookupDownloadID and
// returns the resolved id, or "" when the lookup declines or errors.
// Lookup errors swallowed — the C# tree at `:138` lets repo errors
// propagate, but the Go port treats per-row backfill failures as
// recoverable (same posture as historyhandlers.lookupDownloadIDFallback
// at slice 5.J127).
func lookupDownloadIDFallback(ctx context.Context, l LookupDownloadID, seriesID int, le *episodeimport.LocalEpisode) string {
	if l == nil || le == nil || seriesID <= 0 || len(le.Episodes) == 0 {
		return ""
	}
	ids := make([]int, 0, len(le.Episodes))
	for _, ep := range le.Episodes {
		ids = append(ids, ep.ID)
	}
	got, err := l.FindDownloadID(ctx, seriesID, le.Quality, ids)
	if err != nil {
		return ""
	}
	return got
}

// encodeImportedData builds the 4-key Data dictionary the C# handler
// writes at `:157-160` and JSON-marshals it. Keys are emitted
// unconditionally (the C# code does the same — Dictionary.Add accepts
// empty values).
func encodeImportedData(clientType, clientName, sourcePath, destinationPath string) (string, error) {
	data := map[string]string{
		"DownloadClient":     clientType,
		"DownloadClientName": clientName,
		"SourcePath":         sourcePath,
		"DestinationPath":    destinationPath,
	}
	b, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
