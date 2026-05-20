package historyhandlers

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"time"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/history"
	"sonarr.tv/sonarr/internal/parser"
)

// EpisodeFileReader narrows the episodes.Service surface to the one
// method this handler needs — a per-file lookup of linked episodes.
// Pulled out so tests can inject a stub without standing up the full
// service.
//
// Mirrors the C# `LazyLoaded<List<Episode>>` access at
// `EpisodeFile.Episodes.Value` — the Go port doesn't have a lazy
// loader, so the handler does an explicit lookup at event time.
type EpisodeFileReader interface {
	GetEpisodesByFileID(ctx context.Context, fileID int) ([]episodes.Episode, error)
}

// HandleEpisodeFileRenamed ports
// `src/NzbDrone.Core/History/HistoryService.cs:303-334` —
// `Handle(EpisodeFileRenamedEvent)`. Looks up the linked episodes for
// the renamed file via [EpisodeFileReader.GetEpisodesByFileID] (the
// Go-port substitute for the C# `EpisodeFile.Episodes.Value` lazy
// load) and writes one [history.Entry] per linked episode through
// [EntryWriter.Add].
//
// # Per-episode Entry shape (mirrors C# `:312-321`)
//
//   - EventType = [history.EventTypeEpisodeFileRenamed]
//   - Date = time.Now().UTC() (call-time clock; one snapshot per
//     outer Handle invocation)
//   - Quality = ev.EpisodeFile.Quality verbatim (already JSON-shape,
//     empty falls back to "{}")
//   - SourceTitle = ev.OriginalPath — the C# tree uses the absolute
//     pre-rename path, NOT the new path; this preserves the v3
//     history-search behaviour where users find a renamed file by
//     either its old name OR its new name (the new name lives in
//     the Data["Path"] key)
//   - SeriesID = ev.EpisodeFile.SeriesID
//   - EpisodeID = the iterated episode's row id
//   - DownloadID = "" (rename isn't tied to a download)
//   - Languages = clone of ev.EpisodeFile.Languages
//
// # Data map (mirrors C# `:323-330`)
//
// 8 fixed keys — SourcePath / SourceRelativePath / Path /
// RelativePath / ReleaseGroup / Size / IndexerFlags / ReleaseType.
// SourcePath is the absolute pre-rename path. SourceRelativePath is
// the pre-rename path made relative to ev.Series.Path via
// filepath.Rel — when ev.Series is nil or filepath.Rel errors (e.g.
// the original path lives on a different volume) the value falls
// back to ev.OriginalPath itself. Path is the new absolute path
// (filepath.Join(Series.Path, EpisodeFile.RelativePath)). RelativePath
// is the new relative path verbatim.
//
// IndexerFlags emits the comma-separated `[Flags]` name form via
// [parser.IndexerFlags.String] — J160 sweep — matching C#
// `HistoryService.cs:329` (`message.EpisodeFile.IndexerFlags.ToString()`).
// ReleaseType emits the PascalCase name via [parser.ReleaseType.String]
// — J159 sweep — matching C# `:330`.
//
// # Defensive arms
//
// nil-writer / nil-reader → typed error. nil-event / nil-EpisodeFile →
// no-op. Reader-error or empty linked-episodes → no-op (nothing to
// audit). Per-episode Add error aborts the loop and propagates wrapped.
func HandleEpisodeFileRenamed(
	ctx context.Context,
	w EntryWriter,
	r EpisodeFileReader,
	ev *episodefiles.EpisodeFileRenamedEvent,
) error {
	if w == nil {
		return errors.New("historyhandlers: HandleEpisodeFileRenamed: nil writer")
	}
	if r == nil {
		return errors.New("historyhandlers: HandleEpisodeFileRenamed: nil reader")
	}
	if ev == nil || ev.EpisodeFile == nil {
		return nil
	}

	linked, err := r.GetEpisodesByFileID(ctx, ev.EpisodeFile.ID)
	if err != nil {
		return fmt.Errorf("historyhandlers: HandleEpisodeFileRenamed: lookup linked episodes: %w", err)
	}
	if len(linked) == 0 {
		return nil
	}

	file := ev.EpisodeFile
	sourcePath := ev.OriginalPath
	sourceRelative := renamedSourceRelativePath(ev)
	newPath := renamedPath(ev)
	newRelative := file.RelativePath

	now := time.Now().UTC()
	for _, ep := range linked {
		data := map[string]string{
			"SourcePath":         sourcePath,
			"SourceRelativePath": sourceRelative,
			"Path":               newPath,
			"RelativePath":       newRelative,
			"ReleaseGroup":       file.ReleaseGroup,
			"Size":               strconv.FormatInt(file.Size, 10),
			"IndexerFlags":       parser.IndexerFlags(file.IndexerFlags).String(),
			"ReleaseType":        parser.ReleaseType(file.ReleaseType).String(),
		}
		entry := &history.Entry{
			EventType:   eventTypePtr(history.EventTypeEpisodeFileRenamed),
			Date:        now,
			Quality:     defaultQualityJSON(file.Quality),
			SourceTitle: sourcePath,
			SeriesID:    file.SeriesID,
			EpisodeID:   ep.ID,
			Languages:   cloneLanguageIDs(file.Languages),
			Data:        data,
		}
		if _, err := w.Add(ctx, entry); err != nil {
			return fmt.Errorf("historyhandlers: HandleEpisodeFileRenamed(episode=%d): %w", ep.ID, err)
		}
	}
	return nil
}

// renamedSourceRelativePath mirrors the C# string extension
// `Series.Path.GetRelativePath(OriginalPath)` at
// `HistoryService.cs:306`. Returns the OriginalPath made relative to
// the series root.
//
// On Go-port:
//   - When Series is nil OR Series.Path is empty: return OriginalPath
//     verbatim (no series root to relativise against).
//   - When filepath.Rel returns an error (e.g. different volumes):
//     return OriginalPath verbatim. The C# `GetRelativePath` would
//     return the OriginalPath itself in that case too (the helper at
//     PathExtensions falls through to the unmodified path when
//     relativisation fails).
func renamedSourceRelativePath(ev *episodefiles.EpisodeFileRenamedEvent) string {
	if ev == nil || ev.Series == nil || ev.Series.Path == "" {
		return ev.OriginalPath
	}
	rel, err := filepath.Rel(ev.Series.Path, ev.OriginalPath)
	if err != nil {
		return ev.OriginalPath
	}
	return rel
}

// renamedPath composes the new absolute path for the file post-rename.
// Mirrors the C# `Path.Combine(Series.Path, EpisodeFile.RelativePath)`
// computed value at `HistoryService.cs:307`. Falls back to the
// RelativePath alone when Series is nil — same fallback shape as
// the slice-3.AW deletion handler.
func renamedPath(ev *episodefiles.EpisodeFileRenamedEvent) string {
	if ev == nil || ev.EpisodeFile == nil {
		return ""
	}
	if ev.Series != nil && ev.Series.Path != "" {
		return filepath.Join(ev.Series.Path, ev.EpisodeFile.RelativePath)
	}
	return ev.EpisodeFile.RelativePath
}
