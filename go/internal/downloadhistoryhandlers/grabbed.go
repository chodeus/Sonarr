package downloadhistoryhandlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/downloadhistory"
	"sonarr.tv/sonarr/internal/parser"
)

// EntryWriter narrows [*downloadhistory.Service] to the one method
// this package needs (Add).
type EntryWriter interface {
	Add(ctx context.Context, e *downloadhistory.Entry) (*downloadhistory.Entry, error)
}

// HandleEpisodeGrabbed ports
// `src/NzbDrone.Core/Download/History/DownloadHistoryService.cs:95-122`
// — `Handle(EpisodeGrabbedEvent)`. Writes one [downloadhistory.Entry]
// per event (not per-episode — the DownloadHistory table is keyed by
// downloadID at the lifecycle granularity, not by episode).
//
// # Empty-DownloadID gate (C# `:98-101`)
//
// Skips grab events from download clients that don't report a
// downloadID (e.g. push-based clients where the response carries no
// id). The audit log on this table is only useful for grabs where
// subsequent import/fail/ignore events can link back via the same
// downloadID; rows without an id are unrecoverable.
//
// # Entry shape (C# `:103-114`)
//
//   - EventType  = [downloadhistory.EventTypeGrabbed]
//   - SeriesID   = Subject.Series.ID
//   - DownloadID = ev.DownloadID
//   - SourceTitle = Subject.Release.Title (via the ReleaseInfo embedded
//     in the typed-any release; same accessor pattern as
//     historyhandlers.HandleEpisodeGrabbed)
//   - Date  = time.Now().UTC()
//   - Protocol = Subject.Release.DownloadProtocol (int form)
//   - IndexerID = Subject.Release.IndexerID
//   - DownloadClientID = ev.DownloadClientID
//   - Release = JSON-encoded Subject.Release (the full ReleaseInfo
//     payload — the C# tree assigns the whole object and persists
//     via Dapper's text serializer; Go encodes once via json.Marshal)
//   - Data = JSON map with 4 keys: Indexer, DownloadClient,
//     DownloadClientName, CustomFormatScore. Distinct from the
//     19-key historyhandlers Data — DownloadHistory's audit-log
//     scope is narrower.
//
// # Defensive arms
//
// nil-writer → typed error. nil-event / nil-event.Episode / empty
// Subject.Series → no-op (no row, no error). Empty DownloadID → no-op
// (matches C# gate). Repo Insert error propagates wrapped.
func HandleEpisodeGrabbed(ctx context.Context, w EntryWriter, ev *decisionengine.EpisodeGrabbedEvent) error {
	if w == nil {
		return errors.New("downloadhistoryhandlers: HandleEpisodeGrabbed: nil writer")
	}
	if ev == nil || ev.Episode == nil {
		return nil
	}
	if ev.DownloadID == "" {
		return nil
	}
	subject := ev.Episode
	if subject.Series == nil {
		return nil
	}

	rel := releaseInfoFromSubject(subject)
	releaseJSON, err := encodeRelease(subject.Release)
	if err != nil {
		return fmt.Errorf("downloadhistoryhandlers: HandleEpisodeGrabbed: encode release: %w", err)
	}
	dataJSON, err := encodeGrabbedData(ev, subject, rel)
	if err != nil {
		return fmt.Errorf("downloadhistoryhandlers: HandleEpisodeGrabbed: encode data: %w", err)
	}

	sourceTitle := ""
	if rel != nil {
		sourceTitle = rel.Title
	}

	protocol := 0
	indexerID := 0
	if rel != nil {
		protocol = int(rel.DownloadProtocol)
		indexerID = rel.IndexerID
	}

	entry := &downloadhistory.Entry{
		EventType:        int(downloadhistory.EventTypeGrabbed),
		SeriesID:         subject.Series.ID,
		DownloadID:       ev.DownloadID,
		SourceTitle:      sourceTitle,
		Date:             time.Now().UTC(),
		Protocol:         protocol,
		IndexerID:        indexerID,
		DownloadClientID: ev.DownloadClientID,
		Release:          releaseJSON,
		Data:             dataJSON,
	}
	if _, err := w.Add(ctx, entry); err != nil {
		return fmt.Errorf("downloadhistoryhandlers: HandleEpisodeGrabbed: %w", err)
	}
	return nil
}

// releaseInfoFromSubject narrows Subject.Release (any) to a
// [*parser.ReleaseInfo] for the typed-column reads (IndexerID,
// DownloadProtocol). Returns nil when the release is neither a
// ReleaseInfo nor a TorrentInfo — matches the
// historyhandlers.releaseInfoFromSubject behaviour.
func releaseInfoFromSubject(s *decisionengine.Subject) *parser.ReleaseInfo {
	if s == nil {
		return nil
	}
	if r, ok := s.Release.(*parser.ReleaseInfo); ok && r != nil {
		return r
	}
	if t, ok := s.Release.(*parser.TorrentInfo); ok && t != nil {
		return &t.ReleaseInfo
	}
	return nil
}

// encodeRelease serialises the Subject.Release payload into the
// persisted-row form for the Release column. The C# tree assigns the
// raw ReleaseInfo object and lets Dapper's text serializer write the
// JSON; the Go port matches by json.Marshal-ing the typed-any value.
// Empty string when the release is nil (column is nullable; empty
// string mirrors the sql.NullString round-trip).
func encodeRelease(release any) (string, error) {
	if release == nil {
		return "", nil
	}
	b, err := json.Marshal(release)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// encodeGrabbedData builds the 4-key Data dictionary the C# handler
// writes at `:116-119` and JSON-marshals it. Keys: Indexer,
// DownloadClient, DownloadClientName, CustomFormatScore.
func encodeGrabbedData(ev *decisionengine.EpisodeGrabbedEvent, subject *decisionengine.Subject, rel *parser.ReleaseInfo) (string, error) {
	data := make(map[string]string, 4)
	if rel != nil {
		data["Indexer"] = rel.Indexer
	} else {
		data["Indexer"] = ""
	}
	data["DownloadClient"] = ev.DownloadClient
	data["DownloadClientName"] = ev.DownloadClientName
	if subject != nil {
		data["CustomFormatScore"] = strconv.Itoa(subject.CustomFormatScore)
	} else {
		data["CustomFormatScore"] = "0"
	}
	b, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
