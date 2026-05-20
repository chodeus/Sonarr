package historyhandlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/history"
	"sonarr.tv/sonarr/internal/quality"
)

// LookupDownloadID narrows [*history.Service] to the
// FindDownloadID surface that [HandleEpisodeImported] reaches for
// when the event's DownloadClientItem has no DownloadID of its own.
// Pulled out so tests can inject a stub without constructing a full
// history.Service + repo.
//
// Returns "" + nil when the lookup declines to guess; non-empty
// downloadID + nil for the unanimous-grab match; "" + error when the
// underlying repo fails (handler logs + skips the backfill rather
// than aborting the import history write).
type LookupDownloadID interface {
	FindDownloadID(ctx context.Context, seriesID int, qualityModel quality.Model, episodeIDs []int) (string, error)
}

// HandleEpisodeImportedOption tunes [HandleEpisodeImported] via the
// variadic-options pattern so existing test callers that don't need
// the FindDownloadID fallback don't have to thread a nil through.
//
// Future imported-handler tuning slots (event-bus retry policy,
// custom clock for the per-row Date) plug in here without breaking
// the signature.
type HandleEpisodeImportedOption func(*importedConfig)

// importedConfig is the resolved options bag the handler walks. Kept
// unexported — callers tune via the public WithX option constructors.
type importedConfig struct {
	lookup LookupDownloadID
}

// WithDownloadIDLookup wires a LookupDownloadID into
// [HandleEpisodeImported]. When the event's DownloadClientItem
// carries no DownloadID, the handler consults the lookup and uses
// any returned id for the per-episode Entry.DownloadID column.
// Lookup errors are non-fatal (the handler proceeds with an empty
// DownloadID — same as the C# behaviour where FindDownloadId's
// underlying repo errors propagate and the EpisodeImportedEvent
// handler doesn't try/catch).
//
// Pass nil → no-op (the option silently drops). Useful for wiring
// sites that conditionally enable the fallback off a config flag.
func WithDownloadIDLookup(l LookupDownloadID) HandleEpisodeImportedOption {
	return func(c *importedConfig) {
		if l != nil {
			c.lookup = l
		}
	}
}

// HandleEpisodeImported ports
// `src/NzbDrone.Core/History/HistoryService.cs:196-237` —
// `Handle(EpisodeImportedEvent)`. Walks every episode in the imported
// LocalEpisode and writes one [history.Entry] per episode through
// [EntryWriter.Add], with the import-side metadata captured into the
// Entry's Data map.
//
// # NewDownload gate (mirrors C# `:198-201`)
//
// Returns nil immediately when the event's NewDownload flag is false —
// matches the C# early-return that skips history for rescans, manual
// imports without a fresh download, and library-rescan flows. The
// audit log only captures NEW imports.
//
// # Per-episode Entry shape (mirrors C# `:212-222`)
//
//   - EventType = [history.EventTypeDownloadFolderImported]
//   - Date = time.Now().UTC() (call-time clock; one snapshot per
//     outer Handle invocation, shared across emitted entries)
//   - Quality = JSON-encoded LocalEpisode.Quality
//   - SourceTitle = ImportedFile.SceneName when non-blank, else the
//     filename-without-extension of LocalEpisode.Path. Mirrors C#
//     `message.ImportedEpisode.SceneName ?? Path.GetFileNameWithoutExtension(message.EpisodeInfo.Path)`.
//   - SeriesID = ImportedFile.SeriesID (NOT LocalEpisode.Series.ID —
//     the C# tree reads the value off the episodeFile row directly).
//   - EpisodeID = the iterated episode's row id.
//   - DownloadID = ev.DownloadClientItem.DownloadID when the event
//     carries a download-client item; empty otherwise. The C# tree
//     also runs a [HistoryService.FindDownloadId] fallback that walks
//     existing Grabbed history rows to backfill the id when the event
//     payload itself has no id (`:203-208`). Pass
//     [WithDownloadIDLookup] to enable the same backfill on the Go
//     side — when the event's DownloadID is empty the handler
//     consults the [LookupDownloadID] surface to resolve a still-
//     downloading grab. The lookup is optional: without it (or with
//     a nil lookup) the handler proceeds with an empty DownloadID,
//     matching the pre-J127 behaviour where manual / scan imports
//     of Sonarr-grabbed files lost their grab→import link.
//   - Languages = LocalEpisode.Languages flattened to []int IDs.
//
// # Data map (mirrors C# `:224-233`)
//
// 10 fixed entries — FileId / DroppedPath / ImportedPath /
// DownloadClient / DownloadClientName / ReleaseGroup /
// CustomFormatScore / Size / IndexerFlags / ReleaseType. The C# code
// inserts every key unconditionally even when the source value is
// nullable / empty; the Go port mirrors so the column shape stays
// stable across grabbed→imported pairs in v3 history filtering.
//
// # IndexerFlags / ReleaseType formatting
//
// EpisodeFile.IndexerFlags is the persisted int form (the schema column
// is INT). This handler emits the integer string ("9" for
// Freeleech|Internal=1|8) — same pattern as the slice-3.AT
// HandleEpisodeGrabbed handler. The C# `[Flags] enum.ToString()` would
// emit comma-separated names; the int form round-trips through
// [parser.ParseIndexerFlags] unchanged. Same posture for ReleaseType:
// emit the integer string, not the named form, for consistency with
// the persisted column form.
//
// # Defensive arms
//
// nil-writer → typed error (matches HandleEpisodeGrabbed). nil-event /
// nil-LocalEpisode / nil-EpisodeFile / empty-Episodes → no-op (no
// Entry written, no error). Per-episode Add errors abort the loop and
// propagate (the C# tree relies on EF transaction semantics; the Go
// port surfaces the first per-row failure rather than swallowing).
func HandleEpisodeImported(ctx context.Context, w EntryWriter, ev *episodeimport.EpisodeImportedEvent, opts ...HandleEpisodeImportedOption) error {
	if w == nil {
		return errors.New("historyhandlers: HandleEpisodeImported: nil writer")
	}
	if ev == nil {
		return nil
	}
	if !ev.NewDownload {
		return nil
	}
	if ev.LocalEpisode == nil || ev.EpisodeFile == nil {
		return nil
	}
	le := ev.LocalEpisode
	if len(le.Episodes) == 0 {
		return nil
	}

	cfg := importedConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	sourceTitle := ev.EpisodeFile.SceneName
	if strings.TrimSpace(sourceTitle) == "" {
		sourceTitle = fileNameWithoutExt(le.Path)
	}

	downloadID, clientType, clientName := importedClientFields(ev)
	if strings.TrimSpace(downloadID) == "" && cfg.lookup != nil {
		downloadID = lookupDownloadIDFallback(ctx, cfg.lookup, ev.EpisodeFile.SeriesID, le)
	}

	qualityJSON, err := encodeImportedQuality(le)
	if err != nil {
		return fmt.Errorf("historyhandlers: HandleEpisodeImported: encode quality: %w", err)
	}

	importedPath := ""
	if le.Series != nil {
		importedPath = filepath.Join(le.Series.Path, ev.EpisodeFile.RelativePath)
	}

	languageIDsList := importedLanguageIDs(le)

	now := time.Now().UTC()
	for _, ep := range le.Episodes {
		data := buildImportedData(ev, le, clientType, clientName, importedPath)
		entry := &history.Entry{
			EventType:   eventTypePtr(history.EventTypeDownloadFolderImported),
			Date:        now,
			Quality:     qualityJSON,
			SourceTitle: sourceTitle,
			SeriesID:    ev.EpisodeFile.SeriesID,
			EpisodeID:   ep.ID,
			DownloadID:  downloadID,
			Languages:   append([]int(nil), languageIDsList...),
			Data:        data,
		}
		if _, err := w.Add(ctx, entry); err != nil {
			return fmt.Errorf("historyhandlers: HandleEpisodeImported(episode=%d): %w", ep.ID, err)
		}
	}
	return nil
}

// lookupDownloadIDFallback invokes the wired [LookupDownloadID] and
// returns the resolved id, or "" when the lookup declines (no match)
// or errors. Lookup errors are intentionally non-fatal: the C# tree
// at `HistoryService.cs:203-208` lets the FindDownloadId repo call
// propagate, but every callsite catches at the IHandle boundary —
// the Go-port chooses to swallow at the helper so a transient repo
// failure on the backfill doesn't abort the per-episode history Add
// loop the user actually cares about.
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

// importedClientFields pulls the DownloadID + DownloadClientInfo
// trio off ev.DownloadClientItem with full nil-tolerance. The C#
// event ctor at `EpisodeImportedEvent.cs:24-28` does the same nil
// guard before assigning DownloadClientInfo / DownloadId; the Go-port
// event leaves the resolution to subscribers.
func importedClientFields(ev *episodeimport.EpisodeImportedEvent) (downloadID, clientType, clientName string) {
	if ev == nil || ev.DownloadClientItem == nil {
		return "", "", ""
	}
	downloadID = ev.DownloadClientItem.DownloadID
	if info := ev.DownloadClientItem.DownloadClientInfo; info != nil {
		clientType = info.Type
		clientName = info.Name
	}
	return downloadID, clientType, clientName
}

// encodeImportedQuality JSON-marshals LocalEpisode.Quality into the
// persisted-row form. Empty-object form on nil or marshal error.
func encodeImportedQuality(le *episodeimport.LocalEpisode) (string, error) {
	if le == nil {
		return "{}", nil
	}
	b, err := json.Marshal(le.Quality)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// importedLanguageIDs flattens LocalEpisode.Languages to the
// persisted []int form (the History.Languages column is a JSON array
// of language IDs).
func importedLanguageIDs(le *episodeimport.LocalEpisode) []int {
	if le == nil || len(le.Languages) == 0 {
		return []int{}
	}
	out := make([]int, 0, len(le.Languages))
	for _, l := range le.Languages {
		out = append(out, l.ID)
	}
	return out
}

// buildImportedData materialises the 10-key Data dictionary the C#
// handler builds at `:224-233`. Every key is set unconditionally —
// missing source values resolve to "" / "0" rather than dropping.
func buildImportedData(ev *episodeimport.EpisodeImportedEvent, le *episodeimport.LocalEpisode, clientType, clientName, importedPath string) map[string]string {
	data := make(map[string]string, 10)
	data["FileId"] = strconv.Itoa(ev.EpisodeFile.ID)
	data["DroppedPath"] = le.Path
	data["ImportedPath"] = importedPath
	data["DownloadClient"] = clientType
	data["DownloadClientName"] = clientName
	data["ReleaseGroup"] = le.ReleaseGroup
	data["CustomFormatScore"] = strconv.Itoa(le.CustomFormatScore)
	data["Size"] = strconv.FormatInt(le.Size, 10)
	// IndexerFlags / ReleaseType — emit the int-string form to match
	// the slice-3.AT HandleEpisodeGrabbed handler's posture for the
	// shared `data["IndexerFlags"]` audit-log key. Both round-trip
	// through [parser.ParseIndexerFlags] / [parser.ParseReleaseType]
	// since those parsers accept the numeric form.
	data["IndexerFlags"] = strconv.Itoa(ev.EpisodeFile.IndexerFlags)
	data["ReleaseType"] = strconv.Itoa(ev.EpisodeFile.ReleaseType)
	return data
}

// fileNameWithoutExt mirrors C# `Path.GetFileNameWithoutExtension(p)`:
// strips any directory components and the final extension (the part
// after the last `.` in the basename, including the dot). An input
// without an extension returns the basename verbatim. An input ending
// in `.` returns the basename with the trailing dot stripped.
func fileNameWithoutExt(p string) string {
	if p == "" {
		return ""
	}
	base := filepath.Base(p)
	ext := filepath.Ext(base)
	if ext == "" {
		return base
	}
	return base[:len(base)-len(ext)]
}
