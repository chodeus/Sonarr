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
// Deferred vs C#: the post-import UntrackedDownloadCompletedEvent /
// DownloadCompletedEvent publication + tracked-download output-folder
// cleanup (C# `:572-626`) are not wired — the Go port's event bus
// arms for these consumers aren't ported. The custom-format
// recalculation (C# `:545`) is also deferred; the augmented values
// stand.

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"sonarr.tv/sonarr/internal/downloadclients"
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

// ManualImport is the IExecute<ManualImportCommand> port. Holds four
// required collaborators + one optional tracked-download finder.
type ManualImport struct {
	seriesGetter  ManualImportSeriesGetter
	episodeGetter ManualImportEpisodeGetter
	aggregator    ManualImportAggregator
	importer      ManualImportImporter
	finder        ManualImportTrackedDownloadFinder
	log           *slog.Logger
}

// NewManualImport builds the handler. seriesGetter / episodeGetter /
// aggregator / importer MUST be non-nil. finder is optional; nil
// disables the tracked-download arm. log defaults to [slog.Default].
func NewManualImport(
	seriesGetter ManualImportSeriesGetter,
	episodeGetter ManualImportEpisodeGetter,
	aggregator ManualImportAggregator,
	importer ManualImportImporter,
	finder ManualImportTrackedDownloadFinder,
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

	for i := range cmd.Files {
		if err := h.importOne(ctx, &cmd.Files[i], mode); err != nil {
			return fmt.Errorf("handlers: ManualImport: file %q: %w", cmd.Files[i].Path, err)
		}
	}
	return nil
}

// importOne builds + dispatches the import for a single file. Mirrors
// the C# loop body at `:493-569`.
func (h *ManualImport) importOne(ctx context.Context, file *manualImportFile, mode episodeimport.ImportMode) error {
	ser, err := h.seriesGetter.GetSeries(ctx, file.SeriesID)
	if err != nil {
		return fmt.Errorf("resolve series %d: %w", file.SeriesID, err)
	}
	if ser == nil {
		return fmt.Errorf("series %d not found", file.SeriesID)
	}
	eps, err := h.episodeGetter.GetEpisodes(ctx, file.EpisodeIDs)
	if err != nil {
		return fmt.Errorf("resolve episodes %v: %w", file.EpisodeIDs, err)
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
	if td == nil {
		_, err = h.importer.Import(ctx, []*episodeimport.ImportDecision{decision}, !existingFile, nil, mode)
	} else {
		_, err = h.importer.Import(ctx, []*episodeimport.ImportDecision{decision}, true, td.DownloadItem, mode)
	}
	if err != nil {
		return fmt.Errorf("import: %w", err)
	}
	return nil
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
