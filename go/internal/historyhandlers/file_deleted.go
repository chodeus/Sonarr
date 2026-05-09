package historyhandlers

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"time"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/history"
)

// HandleEpisodeFileDeleted ports
// `src/NzbDrone.Core/History/HistoryService.cs:267-301` —
// `Handle(EpisodeFileDeletedEvent)`. Walks every linked episode on
// the deleted file and writes one [history.Entry] per id through
// [EntryWriter.Add], with the deletion metadata captured into the
// Entry's Data map.
//
// # Reason early-return (mirrors C# `:269-278`)
//
// Returns nil immediately for two `DeleteMediaFileReason` values
// that the C# tree explicitly skips:
//
//   - [episodefiles.ReasonNoLinkedEpisodes] — housekeeping cleanup
//     of an orphaned file row; not a user-visible deletion event.
//   - [episodefiles.ReasonManualOverride] — user-driven force-import
//     of a different file replacing the existing row; the new file's
//     import event already records the swap.
//
// # Per-episode Entry shape (mirrors C# `:282-291`)
//
//   - EventType = [history.EventTypeEpisodeFileDeleted]
//   - Date = time.Now().UTC() (call-time clock; one snapshot per
//     outer Handle invocation)
//   - Quality = ev.EpisodeFile.Quality (already the persisted
//     JSON-shape string from the EpisodeFiles row — pass through
//     verbatim)
//   - SourceTitle = the on-disk path of the deleted file:
//     filepath.Join(ev.Series.Path, ev.EpisodeFile.RelativePath).
//     Mirrors the C# `message.EpisodeFile.Path` getter which is
//     itself a `Path.Combine(Series.Path, RelativePath)` computed
//     property. When ev.Series is nil the SourceTitle falls back
//     to ev.EpisodeFile.RelativePath alone — preserves the audit
//     row even on the orphan-file path.
//   - SeriesID = ev.EpisodeFile.SeriesID
//   - EpisodeID = the iterated episode's row id
//   - DownloadID = "" (deletion isn't tied to a download)
//   - Languages = ev.EpisodeFile.Languages — the schema column is
//     already []int, so direct copy with the per-Entry slice clone.
//
// # Data map (mirrors C# `:293-297`)
//
// 5 fixed keys — Reason / ReleaseGroup / Size / IndexerFlags /
// ReleaseType. Reason emits the enum's String() form per
// [episodefiles.DeleteMediaFileReason.String]. IndexerFlags +
// ReleaseType emit the int-string form via strconv.Itoa(int(...))
// matching the slice-3.AT/3.AU/3.AV posture.
//
// # Defensive arms
//
// nil-writer → typed error. nil-event / nil-EpisodeFile /
// empty-LinkedEpisodes → no-op. Per-episode Add error aborts the
// loop and propagates wrapped with the offending episode id.
func HandleEpisodeFileDeleted(ctx context.Context, w EntryWriter, ev *episodefiles.EpisodeFileDeletedEvent) error {
	if w == nil {
		return errors.New("historyhandlers: HandleEpisodeFileDeleted: nil writer")
	}
	if ev == nil || ev.EpisodeFile == nil {
		return nil
	}
	if ev.Reason == episodefiles.ReasonNoLinkedEpisodes || ev.Reason == episodefiles.ReasonManualOverride {
		return nil
	}
	if len(ev.LinkedEpisodes) == 0 {
		return nil
	}

	file := ev.EpisodeFile
	sourceTitle := deletedSourceTitle(ev)

	now := time.Now().UTC()
	for _, ep := range ev.LinkedEpisodes {
		data := map[string]string{
			"Reason":       ev.Reason.String(),
			"ReleaseGroup": file.ReleaseGroup,
			"Size":         strconv.FormatInt(file.Size, 10),
			"IndexerFlags": strconv.Itoa(file.IndexerFlags),
			"ReleaseType":  strconv.Itoa(file.ReleaseType),
		}
		entry := &history.Entry{
			EventType:   eventTypePtr(history.EventTypeEpisodeFileDeleted),
			Date:        now,
			Quality:     defaultQualityJSON(file.Quality),
			SourceTitle: sourceTitle,
			SeriesID:    file.SeriesID,
			EpisodeID:   ep.ID,
			Languages:   cloneLanguageIDs(file.Languages),
			Data:        data,
		}
		if _, err := w.Add(ctx, entry); err != nil {
			return fmt.Errorf("historyhandlers: HandleEpisodeFileDeleted(episode=%d): %w", ep.ID, err)
		}
	}
	return nil
}

// deletedSourceTitle composes the on-disk path the deleted file
// lived at. Mirrors the C# `EpisodeFile.Path` computed property
// which is `Path.Combine(Series.Path, RelativePath)`. When the
// event lacks a Series reference (orphan path), the fallback is
// the RelativePath alone — keeps the audit-log non-empty.
func deletedSourceTitle(ev *episodefiles.EpisodeFileDeletedEvent) string {
	if ev == nil || ev.EpisodeFile == nil {
		return ""
	}
	if ev.Series != nil && ev.Series.Path != "" {
		return filepath.Join(ev.Series.Path, ev.EpisodeFile.RelativePath)
	}
	return ev.EpisodeFile.RelativePath
}

// defaultQualityJSON returns the Quality column verbatim, falling
// back to the empty-object literal when the source string is empty.
// EpisodeFile.Quality is already JSON-shape as persisted; the C#
// tree's `EpisodeFile.Quality` is a typed QualityModel which
// auto-serialises through the JsonConverter, but the Go-port's
// schema column carries the raw JSON string already, so no
// re-marshal is needed.
func defaultQualityJSON(q string) string {
	if q == "" {
		return "{}"
	}
	return q
}

// cloneLanguageIDs returns a NON-nil per-Entry copy of the source
// language-id slice. nil-source maps to `[]int{}` so the persisted
// History.Languages JSON column emits `[]` rather than `null` (the
// schema column is NOT NULL DEFAULT '[]'). Mutating the returned
// slice does not bleed into the source or peer-Entry copies.
func cloneLanguageIDs(in []int) []int {
	if len(in) == 0 {
		return []int{}
	}
	out := make([]int, len(in))
	copy(out, in)
	return out
}
