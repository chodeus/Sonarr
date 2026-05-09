package blocklisthandlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/blocklist"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

// EntryWriter narrows [*blocklist.Service] to the Add method this
// handler needs. Pulled out so tests can inject a stub without
// constructing the full service.
type EntryWriter interface {
	Add(ctx context.Context, e *blocklist.Entry) (*blocklist.Entry, error)
}

// SeriesDeleter narrows [*blocklist.Service] to the bulk-delete
// method the SeriesDeleted handler needs. Distinct from EntryWriter
// since the two handlers have orthogonal write surfaces (one inserts,
// one deletes).
type SeriesDeleter interface {
	DeleteForSeries(ctx context.Context, seriesIDs []int) (int64, error)
}

// HandleDownloadFailed ports
// `src/NzbDrone.Core/Blocklisting/BlocklistService.cs:126-158` —
// `Handle(DownloadFailedEvent)`. Builds one blocklist.Entry from the
// failed event's metadata + ev.Data dict, then writes it through
// [EntryWriter.Add].
//
// # Field mapping (mirrors C# `:128-145`)
//
// Direct event fields:
//
//   - SeriesId / EpisodeIds / SourceTitle / Quality / Languages /
//     Message / Source — pass-through.
//   - Date — time.Now().UTC() (C# `DateTime.UtcNow`).
//
// ev.Data dict reads (lowercase-first keys per `EpisodeHistory.cs`
// constants — same scheme as the slice-3.AV history handler):
//
//   - "publishedDate" → time.Parse via parsePublishedDate; bad input
//     OR missing key collapses to zero value (C# DateTime.Parse on
//     an empty string would throw — the Go port mirrors functional
//     intent by recording a zero PublishedDate rather than crashing
//     the audit-log writer).
//   - "size" → strconv.ParseInt; missing key OR bad parse → 0
//     (matches C# `long.Parse(message.Data.GetValueOrDefault("size", "0"))`).
//   - "indexer" → string pass-through.
//   - "protocol" → strconv.Atoi → DownloadProtocol; bad parse → 0
//     (matches C# `(DownloadProtocol)Convert.ToInt32(...)` which
//     returns 0 for null/empty input).
//   - "torrentInfoHash" → string fallback; only consulted when
//     TrackedDownload.Protocol != Torrent.
//   - "indexerFlags" → parser.ParseIndexerFlags (slice-3.71 helper
//     accepting both numeric AND named-list forms; matches C#
//     `Enum.TryParse<IndexerFlags>(... ignoreCase: true)`).
//   - "releaseType" → parser.ParseReleaseType (slice-3.71 helper
//     accepting numeric AND named forms).
//
// TrackedDownload-driven TorrentInfoHash arm (mirrors C# `:142-144`):
//
//	TorrentInfoHash = TrackedDownload?.Protocol == Torrent
//	    ? TrackedDownload.DownloadItem.DownloadId
//	    : ev.Data["torrentInfoHash"]
//
// The C# tree dereferences `TrackedDownload.DownloadItem` without
// a `?.` after the Protocol check — the Go-port stays defensive on
// the DownloadItem deref since a Phase-4 producer might leave it
// nil even when Protocol is Torrent.
//
// # Defensive arms
//
// nil-writer → typed error. nil-event → no-op. EpisodeIDs may be
// empty (C# `Insert` accepts an empty list). Insert error
// propagates wrapped.
//
// Unlike the slice-3.AV history HandleDownloadFailed which writes
// one row per episode id, this handler writes ONE row total — the
// blocklist row carries the full EpisodeIDs list as a single
// embedded `[]int` JSON column.
func HandleDownloadFailed(ctx context.Context, w EntryWriter, ev *trackeddownloads.DownloadFailedEvent) error {
	if w == nil {
		return errors.New("blocklisthandlers: HandleDownloadFailed: nil writer")
	}
	if ev == nil {
		return nil
	}

	qualityJSON, err := encodeQuality(ev.Quality)
	if err != nil {
		return fmt.Errorf("blocklisthandlers: HandleDownloadFailed: encode quality: %w", err)
	}

	publishedDate := parsePublishedDate(ev.Data["publishedDate"])
	size := parseInt64(ev.Data["size"])
	protocol := parseProtocol(ev.Data["protocol"])
	torrentInfoHash := resolveTorrentInfoHash(ev)

	entry := &blocklist.Entry{
		SeriesID:        ev.SeriesID,
		EpisodeIDs:      append([]int(nil), ev.EpisodeIDs...),
		SourceTitle:     ev.SourceTitle,
		Quality:         qualityJSON,
		Date:            time.Now().UTC(),
		Indexer:         ev.Data["indexer"],
		Message:         ev.Message,
		Source:          ev.Source,
		Languages:       failedLanguageIDs(ev),
		TorrentInfoHash: torrentInfoHash,
	}
	if !publishedDate.IsZero() {
		pd := publishedDate
		entry.PublishedDate = &pd
	}
	if size > 0 || ev.Data["size"] == "0" {
		v := size
		entry.Size = &v
	}
	if protocol > 0 || ev.Data["protocol"] != "" {
		v := int(protocol)
		entry.Protocol = &v
	}
	if flags := parser.ParseIndexerFlags(ev.Data["indexerFlags"]); flags != 0 {
		entry.IndexerFlags = int(flags)
	}
	if rt := parser.ParseReleaseType(ev.Data["releaseType"]); rt != parser.ReleaseTypeUnknown {
		entry.ReleaseType = int(rt)
	}

	if _, err := w.Add(ctx, entry); err != nil {
		return fmt.Errorf("blocklisthandlers: HandleDownloadFailed: %w", err)
	}
	return nil
}

// resolveTorrentInfoHash mirrors C# `:142-144`:
//
//	TrackedDownload?.Protocol == Torrent
//	    ? TrackedDownload.DownloadItem.DownloadId
//	    : ev.Data["torrentInfoHash"]
//
// nil-TrackedDownload OR Protocol != Torrent → use the Data fallback.
// nil-DownloadItem (with Torrent protocol) → empty string (defensive
// Go-port, the C# tree would NPE).
func resolveTorrentInfoHash(ev *trackeddownloads.DownloadFailedEvent) string {
	if ev.TrackedDownload != nil && indexers.DownloadProtocol(ev.TrackedDownload.Protocol) == indexers.DownloadProtocolTorrent {
		if ev.TrackedDownload.DownloadItem != nil {
			return ev.TrackedDownload.DownloadItem.DownloadID
		}
		return ""
	}
	return ev.Data["torrentInfoHash"]
}

// parsePublishedDate mirrors C# `DateTime.Parse(message.Data.GetValueOrDefault("publishedDate"))`.
// Empty / unparseable input → zero time. The handler treats zero as
// "absent" and leaves PublishedDate as nil on the entry, sidestepping
// the C# arm that would throw on empty input.
//
// Tries the canonical RFC3339 form first (the format the slice-3.AT
// EpisodeGrabbedEvent handler emits when populating the Data dict),
// then a few fallback shapes the C# DateTime.Parse accepts loosely.
func parsePublishedDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// parseInt64 mirrors C# `long.Parse(message.Data.GetValueOrDefault("size", "0"))`.
// Empty / unparseable input → 0.
func parseInt64(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// parseProtocol mirrors C# `(DownloadProtocol)Convert.ToInt32(...)`.
// Empty input → DownloadProtocolUnknown=0. Unparseable input → 0
// (Go-port mirrors C# `Convert.ToInt32` returning 0 for malformed
// strings — actually .NET `Convert.ToInt32(string)` THROWS on
// malformed input, but the Go-port relaxes to 0 to avoid crashing
// the audit-log writer when a Phase-4 producer ships unexpected
// payload).
func parseProtocol(s string) indexers.DownloadProtocol {
	s = strings.TrimSpace(s)
	if s == "" {
		return indexers.DownloadProtocolUnknown
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return indexers.DownloadProtocolUnknown
	}
	return indexers.DownloadProtocol(v)
}

// encodeQuality JSON-marshals the quality.Model into the persisted
// shape. Empty-object form on marshal error.
func encodeQuality(q any) (string, error) {
	b, err := json.Marshal(q)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// failedLanguageIDs flattens the failed-event's language list to the
// persisted []int form. Returns a non-nil empty slice for the empty
// case so the persisted JSON column emits "[]" not "null".
func failedLanguageIDs(ev *trackeddownloads.DownloadFailedEvent) []int {
	if len(ev.Languages) == 0 {
		return []int{}
	}
	out := make([]int, 0, len(ev.Languages))
	for _, l := range ev.Languages {
		out = append(out, l.ID)
	}
	return out
}
