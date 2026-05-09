package historyhandlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"sonarr.tv/sonarr/internal/history"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

// HandleDownloadFailed ports
// `src/NzbDrone.Core/History/HistoryService.cs:239-265` —
// `Handle(DownloadFailedEvent)`. Walks every episode id in the
// failed event's EpisodeIDs and writes one [history.Entry] per id
// through [EntryWriter.Add], with the failure metadata captured into
// the Entry's Data map.
//
// # Per-episode Entry shape (mirrors C# `:243-253`)
//
//   - EventType = [history.EventTypeDownloadFailed]
//   - Date = time.Now().UTC() (call-time clock; one snapshot per
//     outer Handle invocation, shared across emitted entries)
//   - Quality = JSON-encoded ev.Quality
//   - SourceTitle = ev.SourceTitle
//   - SeriesID = ev.SeriesID (NOT pulled off TrackedDownload — the
//     event carries the matched id directly)
//   - EpisodeID = the iterated id
//   - DownloadID = ev.DownloadID
//   - Languages = ev.Languages flattened to []int IDs
//
// # Data map (mirrors C# `:255-261`)
//
// 7 fixed keys — DownloadClient / DownloadClientName / Message /
// Source / ReleaseGroup / Size / Indexer. The last three coalesce
// the TrackedDownload-derived value with an ev.Data fallback per C#
// `EpisodeHistory.cs:15-17` constants ("releaseGroup" / "size" /
// "indexer" — lowercase-first keys):
//
//   - ReleaseGroup: TrackedDownload.RemoteEpisode.ParsedEpisodeInfo.ReleaseGroup
//     ?? ev.Data["releaseGroup"]
//   - Size: TrackedDownload.DownloadItem.TotalSize.ToString()
//     ?? ev.Data["size"]
//   - Indexer: TrackedDownload.RemoteEpisode.Release.Indexer
//     ?? ev.Data["indexer"]
//
// The C# `??` operator treats the LHS as null-coalescing, but the
// Go-port treats EMPTY-STRING from the TrackedDownload chain as a
// miss too — the C# tree's underlying `string` chains can return ""
// (e.g. when ParsedEpisodeInfo is non-nil but ReleaseGroup is unset)
// and the audit-log column would then store an empty value where
// the user-visible v3 history filter expects the fallback. Treating
// "" as "missing" preserves the user-visible behaviour even where
// the C# `??` operator wouldn't fall through.
//
// DownloadClientName: read off `TrackedDownload.DownloadItem.DownloadClientInfo.Name`.
// nil-tolerant via the chain — any nil link in
// TrackedDownload / DownloadItem / DownloadClientInfo collapses to
// empty string.
//
// # Defensive arms
//
// nil-writer → typed error. nil-event / empty-EpisodeIDs → no-op.
// Per-episode Add error aborts the loop and propagates wrapped with
// the offending episode id (matches HandleEpisodeImported's posture).
func HandleDownloadFailed(ctx context.Context, w EntryWriter, ev *trackeddownloads.DownloadFailedEvent) error {
	if w == nil {
		return errors.New("historyhandlers: HandleDownloadFailed: nil writer")
	}
	if ev == nil || len(ev.EpisodeIDs) == 0 {
		return nil
	}

	qualityJSON, err := encodeFailedQuality(ev.Quality)
	if err != nil {
		return fmt.Errorf("historyhandlers: HandleDownloadFailed: encode quality: %w", err)
	}

	clientName := failedClientName(ev.TrackedDownload)
	releaseGroup := failedReleaseGroup(ev)
	size := failedSize(ev)
	indexer := failedIndexer(ev)
	languageIDsList := failedLanguageIDs(ev.Languages)

	now := time.Now().UTC()
	for _, episodeID := range ev.EpisodeIDs {
		data := map[string]string{
			"DownloadClient":     ev.DownloadClient,
			"DownloadClientName": clientName,
			"Message":            ev.Message,
			"Source":             ev.Source,
			"ReleaseGroup":       releaseGroup,
			"Size":               size,
			"Indexer":            indexer,
		}
		entry := &history.Entry{
			EventType:   eventTypePtr(history.EventTypeDownloadFailed),
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
			return fmt.Errorf("historyhandlers: HandleDownloadFailed(episode=%d): %w", episodeID, err)
		}
	}
	return nil
}

// encodeFailedQuality JSON-marshals the quality.Model into the
// persisted-row form. Empty-object form on marshal error.
func encodeFailedQuality(q quality.Model) (string, error) {
	b, err := json.Marshal(q)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// failedClientName walks TrackedDownload→DownloadItem→DownloadClientInfo
// to surface the user-friendly client name. Any nil link collapses
// the result to empty string. Mirrors the C# `?.` chain at
// `:256` — `message.TrackedDownload?.DownloadItem.DownloadClientInfo.Name`.
//
// Note the C# `?.` short-circuits on TrackedDownload only — once
// inside, DownloadItem and DownloadClientInfo are dereferenced
// unconditionally. The Go-port adds nil-safety on every link
// because the producer can leave DownloadItem.DownloadClientInfo
// nil without crashing the audit-log writer (defensive vs C#'s
// reliance on the producer-side guarantee).
func failedClientName(td *trackeddownloads.TrackedDownload) string {
	if td == nil || td.DownloadItem == nil || td.DownloadItem.DownloadClientInfo == nil {
		return ""
	}
	return td.DownloadItem.DownloadClientInfo.Name
}

// failedReleaseGroup mirrors C# `:259`:
//
//	message.TrackedDownload?.RemoteEpisode?.ParsedEpisodeInfo?.ReleaseGroup
//	?? message.Data.GetValueOrDefault(EpisodeHistory.RELEASE_GROUP)
//
// Treats empty-string as "missing" so the Data fallback fires when
// the TrackedDownload chain is present but the field itself is unset.
// See package-level note on the empty-string semantics.
func failedReleaseGroup(ev *trackeddownloads.DownloadFailedEvent) string {
	td := ev.TrackedDownload
	if td != nil && td.RemoteEpisode != nil && td.RemoteEpisode.ParsedEpisodeInfo != nil {
		if rg := td.RemoteEpisode.ParsedEpisodeInfo.ReleaseGroup; rg != "" {
			return rg
		}
	}
	return ev.Data["releaseGroup"]
}

// failedSize mirrors C# `:260`:
//
//	message.TrackedDownload?.DownloadItem.TotalSize.ToString()
//	?? message.Data.GetValueOrDefault(EpisodeHistory.SIZE)
//
// The C# tree calls `.ToString()` even on `0`, so a TrackedDownload
// with TotalSize=0 emits "0" rather than falling through to the
// Data fallback. The Go port mirrors that behaviour exactly so
// downstream filters that gate on "Size present" stay consistent.
func failedSize(ev *trackeddownloads.DownloadFailedEvent) string {
	if ev.TrackedDownload != nil && ev.TrackedDownload.DownloadItem != nil {
		return strconv.FormatInt(ev.TrackedDownload.DownloadItem.TotalSize, 10)
	}
	return ev.Data["size"]
}

// failedIndexer mirrors C# `:261`:
//
//	message.TrackedDownload?.RemoteEpisode?.Release?.Indexer
//	?? message.Data.GetValueOrDefault(EpisodeHistory.INDEXER)
//
// Treats empty-string as "missing" per the same logic as
// failedReleaseGroup. The Subject.Release field is `any` and
// resolves to either *parser.ReleaseInfo or *parser.TorrentInfo
// (which embeds ReleaseInfo); both expose Indexer via the same
// access path.
func failedIndexer(ev *trackeddownloads.DownloadFailedEvent) string {
	td := ev.TrackedDownload
	if td != nil && td.RemoteEpisode != nil && td.RemoteEpisode.Release != nil {
		if idx := indexerFromRelease(td.RemoteEpisode.Release); idx != "" {
			return idx
		}
	}
	return ev.Data["indexer"]
}

// indexerFromRelease unwraps *parser.ReleaseInfo or *parser.TorrentInfo
// to surface the Indexer string. nil-safe.
func indexerFromRelease(r any) string {
	if ri, ok := r.(*parser.ReleaseInfo); ok && ri != nil {
		return ri.Indexer
	}
	if t, ok := r.(*parser.TorrentInfo); ok && t != nil {
		return t.Indexer
	}
	return ""
}

// failedLanguageIDs flattens the failed-event's language list to the
// persisted []int form. Mirrors the slice-3.AT/3.AU pattern.
func failedLanguageIDs(in []language.Language) []int {
	if len(in) == 0 {
		return []int{}
	}
	out := make([]int, 0, len(in))
	for _, l := range in {
		out = append(out, l.ID)
	}
	return out
}
