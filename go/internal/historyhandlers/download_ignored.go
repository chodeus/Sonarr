package historyhandlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"sonarr.tv/sonarr/internal/history"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

// HandleDownloadIgnored ports
// `src/NzbDrone.Core/History/HistoryService.cs:336-366` —
// `Handle(DownloadIgnoredEvent)`. Walks every episode id in the
// ignored event's EpisodeIDs and writes one [history.Entry] per id
// through [EntryWriter.Add].
//
// # Per-episode Entry shape (mirrors C# `:341-352`)
//
//   - EventType = [history.EventTypeDownloadIgnored]
//   - Date = time.Now().UTC() (call-time clock; one snapshot per
//     outer Handle invocation)
//   - Quality = JSON-encoded ev.Quality
//   - SourceTitle = ev.SourceTitle
//   - SeriesID = ev.SeriesID (NOT pulled off TrackedDownload)
//   - EpisodeID = the iterated id
//   - DownloadID = ev.DownloadID
//   - Languages = ev.Languages flattened to []int IDs
//
// # Data map (mirrors C# `:354-360`)
//
// 7 fixed keys — DownloadClient / DownloadClientName / Message /
// ReleaseGroup / Size / Indexer / ReleaseType. Unlike
// HandleDownloadFailed there is NO ev.Data fallback chain on this
// event (the DownloadIgnoredEvent C# class has no Data dictionary
// field) — TrackedDownload-derived values fall through to empty
// string when the chain has nil links rather than reaching a
// secondary source.
//
// DownloadClient / DownloadClientName come off ev.DownloadClientInfo
// directly (the C# tree dereferences this unconditionally at
// `:354-355` so the producer is expected to populate it). The Go-
// port handler stays defensive: nil DownloadClientInfo collapses
// both fields to "" rather than NPEing.
//
// ReleaseType pulls off TrackedDownload→RemoteEpisode→
// ParsedEpisodeInfo→ReleaseType() — the parser-derived enum, NOT
// the EpisodeFile.ReleaseType column. Emitted in int-string form
// via strconv.Itoa(int(...)) for posture-consistency with the
// other handlers; a future slice that ports a Stringer on the
// parser enum can swap to the named form.
//
// # Defensive arms
//
// nil-writer → typed error. nil-event / empty-EpisodeIDs → no-op.
// Per-episode Add error aborts the loop and propagates wrapped.
func HandleDownloadIgnored(ctx context.Context, w EntryWriter, ev *trackeddownloads.DownloadIgnoredEvent) error {
	if w == nil {
		return errors.New("historyhandlers: HandleDownloadIgnored: nil writer")
	}
	if ev == nil || len(ev.EpisodeIDs) == 0 {
		return nil
	}

	qualityJSON, err := encodeIgnoredQuality(ev.Quality)
	if err != nil {
		return fmt.Errorf("historyhandlers: HandleDownloadIgnored: encode quality: %w", err)
	}

	clientType, clientName := ignoredClientFields(ev.DownloadClientInfo)
	releaseGroup := ignoredReleaseGroup(ev.TrackedDownload)
	size := ignoredSize(ev.TrackedDownload)
	indexer := ignoredIndexer(ev.TrackedDownload)
	releaseType := ignoredReleaseType(ev.TrackedDownload)
	languageIDsList := failedLanguageIDs(ev.Languages)

	now := time.Now().UTC()
	for _, episodeID := range ev.EpisodeIDs {
		data := map[string]string{
			"DownloadClient":     clientType,
			"DownloadClientName": clientName,
			"Message":            ev.Message,
			"ReleaseGroup":       releaseGroup,
			"Size":               size,
			"Indexer":            indexer,
			"ReleaseType":        releaseType,
		}
		entry := &history.Entry{
			EventType:   eventTypePtr(history.EventTypeDownloadIgnored),
			Date:        now,
			Quality:     qualityJSON,
			SourceTitle: ev.SourceTitle,
			SeriesID:    ev.SeriesID,
			EpisodeID:   episodeID,
			DownloadID:  ev.DownloadID,
			Languages:   append([]int(nil), languageIDsList...),
			Data:        data,
		}
		if _, err := w.Add(ctx, entry); err != nil {
			return fmt.Errorf("historyhandlers: HandleDownloadIgnored(episode=%d): %w", episodeID, err)
		}
	}
	return nil
}

// encodeIgnoredQuality JSON-marshals the quality.Model into the
// persisted-row form. Errors propagate (caller wraps with the slice
// context).
func encodeIgnoredQuality(q quality.Model) (string, error) {
	b, err := json.Marshal(q)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ignoredClientFields surfaces the (Type, Name) pair from the event's
// DownloadClientInfo. nil-info collapses to ("", "") — defensive vs
// the C# tree's unconditional deref at `:354-355`.
func ignoredClientFields(info *trackeddownloads.ItemClientInfo) (clientType, clientName string) {
	if info == nil {
		return "", ""
	}
	return info.Type, info.Name
}

// ignoredReleaseGroup walks TrackedDownload→RemoteEpisode→
// ParsedEpisodeInfo→ReleaseGroup. nil at any link → "". Distinct
// from the DownloadFailed handler's ReleaseGroup (which has an
// ev.Data fallback) — the DownloadIgnoredEvent C# class has no
// Data dictionary field so empty-string is the terminal value.
func ignoredReleaseGroup(td *trackeddownloads.TrackedDownload) string {
	if td == nil || td.RemoteEpisode == nil || td.RemoteEpisode.ParsedEpisodeInfo == nil {
		return ""
	}
	return td.RemoteEpisode.ParsedEpisodeInfo.ReleaseGroup
}

// ignoredSize mirrors C# `:358`:
//
//	message.TrackedDownload?.DownloadItem.TotalSize.ToString()
//
// nil-TrackedDownload OR nil-DownloadItem → "" (no fallback exists
// on this event). TotalSize=0 still emits "0" — mirrors C#
// `.ToString()` on long.
func ignoredSize(td *trackeddownloads.TrackedDownload) string {
	if td == nil || td.DownloadItem == nil {
		return ""
	}
	return strconv.FormatInt(td.DownloadItem.TotalSize, 10)
}

// ignoredIndexer mirrors C# `:359`:
//
//	message.TrackedDownload?.RemoteEpisode?.Release?.Indexer
//
// Reuses indexerFromRelease (defined in download_failed.go) to
// unwrap Subject.Release for both *parser.ReleaseInfo and
// *parser.TorrentInfo. nil at any chain link → "".
func ignoredIndexer(td *trackeddownloads.TrackedDownload) string {
	if td == nil || td.RemoteEpisode == nil || td.RemoteEpisode.Release == nil {
		return ""
	}
	return indexerFromRelease(td.RemoteEpisode.Release)
}

// ignoredReleaseType mirrors C# `:360`:
//
//	message.TrackedDownload?.RemoteEpisode?.ParsedEpisodeInfo?.ReleaseType.ToString()
//
// The Go-port [parser.ParsedEpisodeInfo.ReleaseType] is a derived
// METHOD (not a field) at `parser/model.go:114-125` — returns
// SingleEpisode / MultiEpisode / SeasonPack / Unknown based on
// the parsed numbers. Emitted in int-string form via
// strconv.Itoa(int(...)) matching the slice-3.AT/3.AU/3.AV/3.AW
// posture; a future Stringer on the parser enum can swap to the
// named form. nil chain link → "0" (the int value of
// ReleaseTypeUnknown), preserving the C# `.ToString()` behaviour
// where calling on the zero-valued enum emits "Unknown" (which
// our Go port renders as the int "0").
func ignoredReleaseType(td *trackeddownloads.TrackedDownload) string {
	if td == nil || td.RemoteEpisode == nil || td.RemoteEpisode.ParsedEpisodeInfo == nil {
		return ""
	}
	return strconv.Itoa(int(td.RemoteEpisode.ParsedEpisodeInfo.ReleaseType()))
}
