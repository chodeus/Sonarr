// ManualImport handler. Ports the IExecute<ManualImportCommand>
// implementation at
// `src/NzbDrone.Core/MediaFiles/EpisodeImport/Manual/ManualImportService.cs:485-627`.
//
// Routes a `{"name":"ManualImport","files":[...],"importMode":0|1|2}`
// API command into the existing episodeimport pipeline: per file it
// resolves the user-chosen series + episodes, augments a LocalEpisode
// so it carries everything an automatic import would, re-applies the
// user's overrides, then dispatches the single approved decision into
// [ImportApprovedEpisodes.Import] (which performs the disk move/copy +
// EpisodeFile row insert).
//
// # Optional dependencies
//
// TrackedDownloadFinder is nil-tolerant — when nil OR when a file has
// no `downloadId`, that file imports via the untracked path
// (newDownload = !existingFile, nil download item). When wired and the
// file carries a downloadId that resolves, the file imports via the
// tracked path (newDownload = true, the tracked download's item).
//
// Faithfulness note: the C# Execute unconditionally re-applies
// file.Quality / file.Languages even when the request omits them
// (overwriting the augmented values with null). The Go port re-applies
// only when the field is present in the body, preserving the augmented
// value otherwise — the real UI always sends both, so this only
// affects malformed/partial requests, where keeping the augmented
// value is strictly safer than zeroing it.
//
// Completion events (J450): after the per-file loop, the handler
// publishes the same completion events C# does (`ManualImportService.cs:572-626`)
// when a [ManualImportCompletionPublisher] is wired —
// [trackeddownloads.UntrackedDownloadCompletedEvent] per series/season
// group of imported untracked files, and [trackeddownloads.DownloadCompletedEvent]
// per tracked download whose every episode imported (state → Imported).
// Unwired the publication is skipped (prior behavior). The tracked-
// download output-folder cleanup (C# `:606-615`) is a follow-up (J451 —
// needs the ShouldDeleteFolder + DeleteFolder substrate wired in). The
// custom-format recalculation (C# `:545`) is also deferred; the
// augmented values stand.

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/ospath"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

// ManualImportName is the registry key + Commands.Name. Mirrors the C#
// short form: `ManualImportCommand` → `ManualImport`. The v3 command
// endpoint special-cases this name to PriorityHigh (command.go:137).
const ManualImportName = "ManualImport"

// ManualImportSeriesGetter narrows [series.Service.GetSeries] — the
// one method the handler reaches for per file. `*series.Service`
// satisfies it via Go's structural typing.
type ManualImportSeriesGetter interface {
	GetSeries(ctx context.Context, id int) (*series.Series, error)
}

// ManualImportEpisodeGetter narrows [episodes.Service.GetEpisodes]
// (resolve a set of episodes by id). `*episodes.Service` satisfies it.
type ManualImportEpisodeGetter interface {
	GetEpisodes(ctx context.Context, ids []int) ([]episodes.Episode, error)
}

// ManualImportAggregator narrows the episodeimport AggregationService
// [episodeimport.Aggregator] surface — the augment pass that fills in
// quality/languages/release-group/episodes the same way an automatic
// import would. `*episodeimport.AggregationService` satisfies it.
type ManualImportAggregator interface {
	Augment(le *episodeimport.LocalEpisode, item *downloadclients.Item) (*episodeimport.LocalEpisode, error)
}

// ManualImportImporter narrows [episodeimport.ImportApprovedEpisodes.Import]
// — the disk move/copy + EpisodeFile insert executor.
// `*episodeimport.ImportApprovedEpisodes` satisfies it.
type ManualImportImporter interface {
	Import(ctx context.Context, decisions []*episodeimport.ImportDecision, newDownload bool, item *downloadclients.Item, mode episodeimport.ImportMode) ([]*episodeimport.ImportResult, error)
}

// ManualImportTrackedDownloadFinder narrows [trackeddownloads.Service.Find]
// (one method). Optional — when nil the handler always imports via the
// untracked path. Mirrors the C# `_trackedDownloadService.Find` arm at
// `ManualImportService.cs:522`.
type ManualImportTrackedDownloadFinder interface {
	Find(downloadID string) *trackeddownloads.TrackedDownload
}

// ManualImportCompletionPublisher publishes the post-import completion
// events (J450). A boot-time adapter over *messaging.Bus satisfies it;
// tests fake it to capture the events. Optional — when nil the handler
// imports files but publishes nothing (prior behavior). Mirrors the
// optional-publisher pattern of downloadprocessing (J449/J400).
type ManualImportCompletionPublisher interface {
	PublishDownloadCompleted(ctx context.Context, ev trackeddownloads.DownloadCompletedEvent) error
	PublishUntrackedDownloadCompleted(ctx context.Context, ev trackeddownloads.UntrackedDownloadCompletedEvent) error
}

// ManualImport is the IExecute<ManualImportCommand> port. Holds four
// required collaborators + one optional tracked-download finder +
// one optional completion-event publisher.
type ManualImport struct {
	seriesGetter  ManualImportSeriesGetter
	episodeGetter ManualImportEpisodeGetter
	aggregator    ManualImportAggregator
	importer      ManualImportImporter
	finder        ManualImportTrackedDownloadFinder
	publisher     ManualImportCompletionPublisher
	log           *slog.Logger
}

// NewManualImport builds the handler. seriesGetter / episodeGetter /
// aggregator / importer MUST be non-nil. finder + publisher are optional;
// nil finder disables the tracked-download arm, nil publisher disables
// completion-event publication. log defaults to [slog.Default].
func NewManualImport(
	seriesGetter ManualImportSeriesGetter,
	episodeGetter ManualImportEpisodeGetter,
	aggregator ManualImportAggregator,
	importer ManualImportImporter,
	finder ManualImportTrackedDownloadFinder,
	publisher ManualImportCompletionPublisher,
	log *slog.Logger,
) (*ManualImport, error) {
	switch {
	case seriesGetter == nil:
		return nil, errors.New("handlers: NewManualImport: nil ManualImportSeriesGetter")
	case episodeGetter == nil:
		return nil, errors.New("handlers: NewManualImport: nil ManualImportEpisodeGetter")
	case aggregator == nil:
		return nil, errors.New("handlers: NewManualImport: nil ManualImportAggregator")
	case importer == nil:
		return nil, errors.New("handlers: NewManualImport: nil ManualImportImporter")
	}
	if log == nil {
		log = slog.Default()
	}
	return &ManualImport{
		seriesGetter:  seriesGetter,
		episodeGetter: episodeGetter,
		aggregator:    aggregator,
		importer:      importer,
		finder:        finder,
		publisher:     publisher,
		log:           log,
	}, nil
}

// manualImportBody is the JSON shape of the command body. Mirrors the
// C# `ManualImportCommand` at `ManualImportCommand.cs:9-15`.
type manualImportBody struct {
	Files []manualImportFile `json:"files"`
	// ImportMode: 0=Auto, 1=Move, 2=Copy. Matches
	// [episodeimport.ImportMode] ordinals.
	ImportMode int `json:"importMode"`
}

// manualImportFile mirrors C# `ManualImportFile.cs`. Quality /
// Languages stay as raw JSON so the handler can decode the UI's
// expanded wire shapes (`{"quality":{"id":N,...},"revision":{...}}`,
// `[{"id":N,"name":"X"}]`) without importing the api/v3 DTO layer
// (which would invert the package layering).
type manualImportFile struct {
	Path         string          `json:"path"`
	FolderName   string          `json:"folderName"`
	SeriesID     int             `json:"seriesId"`
	EpisodeIDs   []int           `json:"episodeIds"`
	Quality      json.RawMessage `json:"quality"`
	Languages    json.RawMessage `json:"languages"`
	ReleaseGroup string          `json:"releaseGroup"`
	IndexerFlags int             `json:"indexerFlags"`
	ReleaseType  string          `json:"releaseType"`
	DownloadID   string          `json:"downloadId"`
}

// Execute parses the JSON command body and imports each file. Mirrors
// C# `ManualImportService.Execute` at `:485-570` (the per-file build +
// dispatch loop; the post-loop event publication is deferred — see the
// package doc).
func (h *ManualImport) Execute(ctx context.Context, body string) error {
	cmd, err := parseManualImportBody(body)
	if err != nil {
		return err
	}
	if len(cmd.Files) == 0 {
		return errors.New("handlers: ManualImport: no files provided")
	}

	mode := episodeimport.ImportMode(cmd.ImportMode)
	h.log.LogAttrs(ctx, slog.LevelInfo, "manualimport: importing files",
		slog.Int("count", len(cmd.Files)), slog.String("mode", mode.String()))

	// Collect results for the post-loop completion-event publication,
	// mirroring C# `imported` / `importedUntrackedDownload` /
	// `importedTrackedDownload` (`ManualImportService.cs:497,553-568`).
	var imported []*episodeimport.ImportResult  // C# `imported` (all, in order)
	var untracked []*episodeimport.ImportResult // C# importedUntrackedDownload
	var tracked []trackedManualImport           // C# importedTrackedDownload

	for i := range cmd.Files {
		results, td, err := h.importOne(ctx, &cmd.Files[i], mode)
		if err != nil {
			return fmt.Errorf("handlers: ManualImport: file %q: %w", cmd.Files[i].Path, err)
		}
		if td == nil {
			// Untracked path: C# AddRange's every result (`:557-558`).
			imported = append(imported, results...)
			untracked = append(untracked, results...)
			continue
		}
		// Tracked path: C# takes .First() of the import result (`:561-567`).
		if len(results) > 0 {
			imported = append(imported, results[0])
			tracked = append(tracked, trackedManualImport{td: td, result: results[0]})
		}
	}

	h.publishCompletions(ctx, imported, untracked, tracked)
	return nil
}

// trackedManualImport pairs a tracked download with the (first) import
// result of one of its files. Mirrors C# `ManuallyImportedFile`
// (`ManualImportService.cs:633-637`).
type trackedManualImport struct {
	td     *trackeddownloads.TrackedDownload
	result *episodeimport.ImportResult
}

// importOne builds + dispatches the import for a single file. Mirrors
// the C# loop body at `:493-569`. Returns the import results plus the
// resolved tracked download (nil for the untracked path) so the caller
// can publish the post-loop completion events.
func (h *ManualImport) importOne(ctx context.Context, file *manualImportFile, mode episodeimport.ImportMode) ([]*episodeimport.ImportResult, *trackeddownloads.TrackedDownload, error) {
	ser, err := h.seriesGetter.GetSeries(ctx, file.SeriesID)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve series %d: %w", file.SeriesID, err)
	}
	if ser == nil {
		return nil, nil, fmt.Errorf("series %d not found", file.SeriesID)
	}
	eps, err := h.episodeGetter.GetEpisodes(ctx, file.EpisodeIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve episodes %v: %w", file.EpisodeIDs, err)
	}

	existingFile := ospath.IsParent(ser.Path, file.Path)

	// Coalesce a nil parse to an empty ParsedEpisodeInfo, mirroring C#
	// `:500` `?? new ParsedEpisodeInfo()`. This guarantees a non-nil
	// parse-info source so Augment (aggregation.go:153) runs its full
	// enrichment (size / mediainfo / scene name / aggregators) instead
	// of short-circuiting with ErrAugmentingFailed — which matters for
	// the oddly-named files manual import exists to handle.
	fileEpisodeInfo := parser.ParsePath(file.Path)
	if fileEpisodeInfo == nil {
		fileEpisodeInfo = &parser.ParsedEpisodeInfo{}
	}

	le := episodeimport.NewLocalEpisode()
	le.Path = file.Path
	le.Series = ser
	le.Episodes = eps
	le.FileEpisodeInfo = fileEpisodeInfo
	le.ExistingFile = existingFile
	le.Size = 0

	// Tracked-download enrichment (C# `:520-525`). The finder is
	// nil-tolerant; a non-resolving downloadId falls through to the
	// untracked path.
	var td *trackeddownloads.TrackedDownload
	if strings.TrimSpace(file.DownloadID) != "" && h.finder != nil {
		td = h.finder.Find(file.DownloadID)
		if td != nil {
			if td.RemoteEpisode != nil {
				le.DownloadClientEpisodeInfo = td.RemoteEpisode.ParsedEpisodeInfo
			}
			le.DownloadItem = td.DownloadItem
		}
	}

	// Folder-name parse (C# `:527-531`).
	if strings.TrimSpace(file.FolderName) != "" {
		le.FolderEpisodeInfo = parser.ParseTitle(file.FolderName)
		le.SceneSource = !existingFile
	}

	// Augment so the file carries everything an automatic import would
	// (C# `:534`). Augment mutates + returns the same pointer.
	var downloadItem *downloadclients.Item
	if td != nil {
		downloadItem = td.DownloadItem
	}
	if _, err := h.aggregator.Augment(le, downloadItem); err != nil {
		h.log.LogAttrs(ctx, slog.LevelWarn, "manualimport: augment failed",
			slog.String("path", file.Path), slog.String("err", err.Error()))
	}

	// Re-apply the user-chosen values (C# `:537-543`). Series /
	// Episodes / ReleaseGroup / IndexerFlags / ReleaseType are applied
	// verbatim; Quality / Languages only when present (see package doc).
	le.Series = ser
	le.Episodes = eps
	le.ReleaseGroup = file.ReleaseGroup
	le.IndexerFlags = parser.IndexerFlags(file.IndexerFlags)
	le.ReleaseType = parser.ParseReleaseType(file.ReleaseType)
	if q, ok := decodeQualityModel(file.Quality); ok {
		le.Quality = q
	}
	if langs, ok := decodeLanguages(file.Languages); ok {
		le.Languages = langs
	}

	decision := episodeimport.NewImportDecision(le)

	// Untracked vs tracked dispatch (C# `:551-569`). Untracked:
	// newDownload = !existingFile, nil item. Tracked: newDownload =
	// true, the tracked download's item.
	var results []*episodeimport.ImportResult
	if td == nil {
		results, err = h.importer.Import(ctx, []*episodeimport.ImportDecision{decision}, !existingFile, nil, mode)
	} else {
		results, err = h.importer.Import(ctx, []*episodeimport.ImportDecision{decision}, true, td.DownloadItem, mode)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("import: %w", err)
	}
	return results, td, nil
}

// publishCompletions fires the post-import completion events when a
// publisher is wired. Ports the tail of C# `ManualImportService.Execute`
// (`:576-625`): UntrackedDownloadCompletedEvent per untracked
// series/season group, then DownloadCompletedEvent per tracked download
// whose every episode imported. nil publisher → no-op.
func (h *ManualImport) publishCompletions(ctx context.Context, imported, untracked []*episodeimport.ImportResult, tracked []trackedManualImport) {
	if h.publisher == nil {
		return
	}
	h.publishUntracked(ctx, untracked)
	h.publishTracked(ctx, imported, tracked)
}

// publishUntracked groups the imported untracked results by their
// EpisodeFile's {SeriesId, SeasonNumber} (first-seen order) and
// publishes an UntrackedDownloadCompletedEvent per group. Ports C#
// `:578-595`.
func (h *ManualImport) publishUntracked(ctx context.Context, untracked []*episodeimport.ImportResult) {
	type groupKey struct {
		seriesID int
		season   int
	}
	var order []groupKey
	groups := map[groupKey][]*episodeimport.ImportResult{}
	for _, r := range untracked {
		if r == nil || r.ResultType() != episodeimport.ImportResultImported || r.EpisodeFile == nil {
			continue
		}
		k := groupKey{seriesID: r.EpisodeFile.SeriesID, season: r.EpisodeFile.SeasonNumber}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], r)
	}
	for _, k := range order {
		g := groups[k]
		var (
			files []episodefiles.File
			paths []string
			eps   []episodes.Episode
			le0   *episodeimport.LocalEpisode
		)
		for _, r := range g {
			le := r.Decision.LocalEpisode
			if le0 == nil {
				le0 = le
			}
			files = append(files, *r.EpisodeFile)
			paths = append(paths, le.Path)
			eps = append(eps, le.Episodes...)
		}
		// ParsedEpisodeInfo: folder parse, fallback file parse (C# `:589`).
		parsed := le0.FolderEpisodeInfo
		if parsed == nil {
			parsed = le0.FileEpisodeInfo
		}
		ev := trackeddownloads.UntrackedDownloadCompletedEvent{
			Series:            le0.Series,
			Episodes:          eps,
			EpisodeFiles:      files,
			ParsedEpisodeInfo: parsed,
			SourcePath:        longestCommonPath(paths),
		}
		if err := h.publisher.PublishUntrackedDownloadCompleted(ctx, ev); err != nil {
			h.log.LogAttrs(ctx, slog.LevelWarn, "manualimport: publish UntrackedDownloadCompletedEvent failed",
				slog.Int("seriesId", k.seriesID), slog.Int("season", k.season), slog.String("err", err.Error()))
		}
	}
}

// publishTracked groups the tracked import results by download id and,
// per group whose every episode imported, marks the tracked download
// Imported + publishes a DownloadCompletedEvent. Ports C# `:597-625`.
//
// importedSeries mirrors the C# quirk `imported.First().LocalEpisode.Series`
// (`:600`): the first imported file of the WHOLE batch supplies the
// event's series id (manual imports are virtually always single-series).
func (h *ManualImport) publishTracked(ctx context.Context, imported []*episodeimport.ImportResult, tracked []trackedManualImport) {
	if len(tracked) == 0 {
		return
	}
	importedSeries := firstLocalEpisodeSeries(imported)
	if importedSeries == nil {
		return
	}

	var order []string
	groups := map[string][]trackedManualImport{}
	for _, t := range tracked {
		if t.td == nil || t.td.DownloadItem == nil {
			continue
		}
		id := t.td.DownloadItem.DownloadID
		if _, ok := groups[id]; !ok {
			order = append(order, id)
		}
		groups[id] = append(groups[id], t)
	}

	for _, id := range order {
		g := groups[id]
		td := g[0].td

		var importedResults []*episodeimport.ImportResult
		for _, t := range g {
			if t.result != nil && t.result.ResultType() == episodeimport.ImportResultImported {
				importedResults = append(importedResults, t.result)
			}
		}

		// all-episodes-imported gate (C# `:619-621`): count imported
		// episodes (multiset) >= max(1, RemoteEpisode.Episodes.Count).
		count := 0
		for _, r := range importedResults {
			if r.Decision != nil && r.Decision.LocalEpisode != nil {
				count += len(r.Decision.LocalEpisode.Episodes)
			}
		}
		expected := 1
		if td.RemoteEpisode != nil {
			if n := len(td.RemoteEpisode.Episodes); n > expected {
				expected = n
			}
		}
		if count < expected {
			continue
		}

		var files []episodefiles.File
		for _, r := range importedResults {
			if r.EpisodeFile != nil {
				files = append(files, *r.EpisodeFile)
			}
		}
		// Release from the first imported result's LocalEpisode (C# `:624`).
		var release *episodeimport.GrabbedReleaseInfo
		if len(importedResults) > 0 && importedResults[0].Decision != nil && importedResults[0].Decision.LocalEpisode != nil {
			release = importedResults[0].Decision.LocalEpisode.Release
		}
		td.State = trackeddownloads.StateImported
		ev := trackeddownloads.DownloadCompletedEvent{
			TrackedDownload: td,
			SeriesID:        importedSeries.ID,
			EpisodeFiles:    files,
			Release:         release,
		}
		if err := h.publisher.PublishDownloadCompleted(ctx, ev); err != nil {
			h.log.LogAttrs(ctx, slog.LevelWarn, "manualimport: publish DownloadCompletedEvent failed",
				slog.String("downloadId", id), slog.String("err", err.Error()))
		}
	}
}

// firstLocalEpisodeSeries returns the Series of the first result that
// carries a LocalEpisode. Mirrors C# `imported.First().LocalEpisode.Series`
// (`:600`) with a nil-guard instead of the C# throw on an empty list.
func firstLocalEpisodeSeries(results []*episodeimport.ImportResult) *series.Series {
	for _, r := range results {
		if r != nil && r.Decision != nil && r.Decision.LocalEpisode != nil {
			return r.Decision.LocalEpisode.Series
		}
	}
	return nil
}

// longestCommonPath ports C# `PathExtensions.GetLongestCommonPath`
// (`PathExtensions.cs:271-296`): the longest common character prefix of
// the inputs, truncated at the last path separator (`/` or `\`).
// Byte-indexed (the inputs are filesystem paths). Empty input, or a
// common prefix with no separator, → "" (the C# `Substring(0, -1)` would
// throw; the Go-port returns "" — there's no common directory).
func longestCommonPath(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	first := paths[0]
	length := len(first)
	for _, p := range paths[1:] {
		if len(p) < length {
			length = len(p)
		}
		for i := 0; i < length; i++ {
			if p[i] != first[i] {
				length = i
				break
			}
		}
	}
	sub := first[:length]
	idx := strings.LastIndexAny(sub, `/\`)
	if idx < 0 {
		return ""
	}
	return sub[:idx]
}

// parseManualImportBody parses the JSON body. Empty / `{}` bodies
// decode to the zero-value struct (no files — the caller's empty-files
// check fires). A malformed body returns a wrapped error.
func parseManualImportBody(body string) (manualImportBody, error) {
	var out manualImportBody
	body = strings.TrimSpace(body)
	if body == "" || body == "{}" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return out, fmt.Errorf("handlers: ManualImport: parse body: %w", err)
	}
	return out, nil
}

// decodeQualityModel decodes the UI's quality wire shape into a
// [quality.Model]. Accepts both the expanded form
// (`{"quality":{"id":N},"revision":{...}}` — what the React UI sends)
// and the persisted bare-int form (`{"quality":N,"revision":{...}}`).
// Returns ok=false for absent/null/malformed input so the caller keeps
// the augmented value.
func decodeQualityModel(raw json.RawMessage) (quality.Model, bool) {
	if isAbsentJSON(raw) {
		return quality.Model{}, false
	}
	var wire struct {
		Quality  json.RawMessage  `json:"quality"`
		Revision quality.Revision `json:"revision"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return quality.Model{}, false
	}
	var id int
	if err := json.Unmarshal(wire.Quality, &id); err != nil {
		var obj struct {
			ID int `json:"id"`
		}
		if err2 := json.Unmarshal(wire.Quality, &obj); err2 != nil {
			return quality.Model{}, false
		}
		id = obj.ID
	}
	q, _ := quality.FindByID(id)
	return quality.Model{Quality: q, Revision: wire.Revision}, true
}

// decodeLanguages decodes the UI's `[{"id":N,"name":"X"}]` language
// wire shape into typed [language.Language] entries. Unknown ids are
// skipped. Returns ok=false for absent/null/malformed input so the
// caller keeps the augmented value.
func decodeLanguages(raw json.RawMessage) ([]language.Language, bool) {
	if isAbsentJSON(raw) {
		return nil, false
	}
	var refs []struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(raw, &refs); err != nil {
		return nil, false
	}
	out := make([]language.Language, 0, len(refs))
	for _, ref := range refs {
		if l, ok := language.FindByID(ref.ID); ok {
			out = append(out, l)
		}
	}
	return out, true
}

// isAbsentJSON reports whether raw is empty or the JSON null literal.
func isAbsentJSON(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s == "" || s == "null"
}
