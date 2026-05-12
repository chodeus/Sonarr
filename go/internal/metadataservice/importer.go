package metadataservice

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/metadata"
	"sonarr.tv/sonarr/internal/metadatafiles"
	"sonarr.tv/sonarr/internal/series"
)

// AggregationService narrows the surface [ExistingImporter] needs
// to resolve a candidate file path into the matching [episodes.Episode]
// rows + their SeasonNumber + EpisodeFileID. Mirrors C#
// `IAggregationService.Augment(LocalEpisode, DownloadClientItem)`
// at the EpisodeImport pipeline — given a file path under a series
// folder, parse the filename + match against the database's
// episode rows.
//
// The C# tree's IAggregationService isn't ported yet — the
// EpisodeImport pipeline lands in a future subset. The Go-port
// importer accepts a nil-tolerant adapter for now: when nil, the
// importer SKIPS classification of EpisodeImage + EpisodeMetadata
// (those depend on the per-episode EpisodeFileID lookup); SeriesImage
// / SeriesMetadata / SeasonImage classifications continue to work.
//
// Production binding will wrap the future
// internal/episodeimport.AggregationService.Augment.
type AggregationService interface {
	// Augment returns the episodes matching `path` under `series`.
	// nil-or-empty result → "no related episodes" branch (skip).
	// Errors are documented per C# `AugmentingFailedException`
	// catch behaviour: the C# tree skips that file silently; the Go
	// port returns the error to give caller observability — the
	// importer logs + continues.
	Augment(ctx context.Context, sr *series.Series, path string) ([]episodes.Episode, error)
}

// subtitleExtensions is the case-insensitive allow-list of file
// extensions [ExistingImporter] skips at the start of per-file
// processing. Mirrors C# `SubtitleFileExtensions.Extensions` at
// `src/NzbDrone.Core/Extras/Subtitles/SubtitleFileExtensions.cs:12-29`.
//
// Inlined here as the smallest seam — the future
// internal/subtitlefiles package may surface a shared constant when
// the Subtitles ExtraFileManager ports.
var subtitleExtensions = map[string]struct{}{
	".aqt": {}, ".ass": {}, ".idx": {}, ".jss": {}, ".psb": {}, ".rt": {},
	".smi": {}, ".srt": {}, ".ssa": {}, ".sub": {}, ".txt": {},
	".utf": {}, ".utf8": {}, ".utf-8": {}, ".vtt": {},
}

// ImporterDeps bundles the per-event deps [ExistingImporter] needs.
// Mirrors the W6a Deps shape — same Writers + Files contracts plus
// the new Aggregator. Fields are nil-tolerant where the C# tree
// allows; required deps cause NewImporter to return a typed error.
type ImporterDeps struct {
	// Writers is the writer-factory surface — same shape as
	// [Service.writers]. REQUIRED.
	Writers WriterFactory

	// Files is the MetadataFiles-table CRUD surface. REQUIRED.
	Files MetadataFileStore

	// Aggregator is the per-file episode-lookup helper. nil →
	// EpisodeImage + EpisodeMetadata classifications are silently
	// skipped.
	Aggregator AggregationService

	// Log is the slog.Logger handle. nil → slog.Default.
	Log *slog.Logger
}

// ExistingImporter ports the C# `ExistingMetadataImporter` at
// `src/NzbDrone.Core/Extras/Metadata/ExistingMetadataImporter.cs:15-110`.
//
// Scans a list of on-disk file paths under a series root, asks each
// registered writer to classify each path, and persists the resulting
// MetadataFile rows. Used by the existing-library scan that
// repopulates the MetadataFiles table for libraries that pre-date
// Sonarr's metadata tracking.
//
// The Go port inlines the C# `ImportExistingExtraFilesBase<MetadataFile>`
// Filter/Clean helpers because Subtitles + Other Extras don't share
// the base in the Go port (yet); when those siblings land we revisit
// a shared internal/extras.ImportExistingHelpers package.
type ExistingImporter struct {
	writers    WriterFactory
	files      MetadataFileStore
	aggregator AggregationService
	log        *slog.Logger
}

// NewImporter binds the [ImporterDeps] bundle. Returns a typed error
// when Writers or Files are nil; Aggregator + Log are nil-tolerant.
func NewImporter(deps ImporterDeps) (*ExistingImporter, error) {
	if deps.Writers == nil {
		return nil, fmt.Errorf("metadataservice: NewImporter: Writers is nil")
	}
	if deps.Files == nil {
		return nil, fmt.Errorf("metadataservice: NewImporter: Files is nil")
	}
	log := deps.Log
	if log == nil {
		log = slog.Default()
	}
	return &ExistingImporter{
		writers:    deps.Writers,
		files:      deps.Files,
		aggregator: deps.Aggregator,
		log:        log,
	}, nil
}

// Order returns the priority [IImportExistingExtraFiles.Order] for
// this importer. Mirrors C# `:34` — 0 (the metadata importer is the
// baseline; subtitles + other-extras importers will be 1+).
func (i *ExistingImporter) Order() int { return 0 }

// FilterResult captures the per-call filter+clean output: the
// per-series files previously imported (so other importers don't
// re-import them) and the candidate filesOnDisk that survived the
// filter. Mirrors C# `ImportExistingExtraFileFilterResult<MetadataFile>`.
type FilterResult struct {
	// PreviouslyImported are the rows whose on-disk paths still
	// exist. These are returned to the caller along with the newly-
	// imported rows so subtitles/other importers see them as
	// "already handled" and skip.
	PreviouslyImported []metadatafiles.MetadataFile

	// FilesOnDisk are the candidate paths the per-writer classifier
	// will process: paths NOT already known to the MetadataFiles
	// table AND NOT in the importedFiles allow-list.
	FilesOnDisk []string
}

// ProcessFiles is the main entry point. Mirrors C# `:36-110`.
//
// Algorithm:
//  1. FilterAndClean — split filesOnDisk into "previously imported"
//     vs "fresh candidates" (and delete stale rows on the way);
//  2. For each candidate path:
//     - Skip subtitle-extension files;
//     - For each registered writer: FindMetadataFile; on a non-nil
//     result, classify as SeriesImage / SeasonImage / EpisodeImage
//     / EpisodeMetadata / SeriesMetadata;
//     - For EpisodeImage + EpisodeMetadata: resolve SeasonNumber +
//     EpisodeFileID via [AggregationService.Augment]. Skip when
//     no episodes match.
//     - Stamp the Extension column from filepath.Ext.
//  3. Upsert the discovered rows.
//  4. Return the discovered rows + previouslyImported (for the
//     downstream importer chain).
//
// `keepExistingEntries` corresponds to C# `fileNameBeforeRename is
// not null` at `:41` — when true (a rename just completed), drop
// only the rows whose paths still exist; preserve everything else
// untouched.
//
// nil-receiver / nil-series → empty result.
func (i *ExistingImporter) ProcessFiles(ctx context.Context, sr *series.Series, filesOnDisk, importedFiles []string, keepExistingEntries bool) ([]metadatafiles.MetadataFile, error) {
	if i == nil || sr == nil {
		return nil, nil
	}
	i.log.Debug("metadataservice: ExistingImporter: scanning series", "series", sr.ID, "path", sr.Path)

	filter, err := i.filterAndClean(ctx, sr, filesOnDisk, importedFiles, keepExistingEntries)
	if err != nil {
		return nil, err
	}

	consumers, err := i.writers.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("metadataservice: ExistingImporter: WriterFactory.All: %w", err)
	}

	var found []metadatafiles.MetadataFile
	for _, candidate := range filter.FilesOnDisk {
		ext := strings.ToLower(filepath.Ext(candidate))
		if _, isSubtitle := subtitleExtensions[ext]; isSubtitle {
			continue
		}
		for _, consumer := range consumers {
			mf, classifyErr := consumer.FindMetadataFile(sr, candidate)
			if classifyErr != nil {
				i.log.Debug("metadataservice: ExistingImporter: writer classify err (continuing)", "writer", consumer.Name(), "path", candidate, "err", classifyErr)
				continue
			}
			if mf == nil {
				continue
			}

			classified := metadata.MetadataType(mf.Type)
			if classified == metadata.MetadataTypeEpisodeImage || classified == metadata.MetadataTypeEpisodeMetadata {
				resolved, augErr := i.augmentEpisode(ctx, sr, candidate)
				if augErr != nil {
					i.log.Debug("metadataservice: ExistingImporter: augment failed (skip)", "path", candidate, "err", augErr)
					continue
				}
				if resolved == nil {
					// No episodes matched OR multi-file-spanning
					// match — both skip per C# `:81-91`.
					continue
				}
				mf.SeasonNumber = &resolved.seasonNumber
				efID := resolved.episodeFileID
				if efID != 0 {
					mf.EpisodeFileID = &efID
				}
			}

			mf.Extension = filepath.Ext(candidate)
			found = append(found, *mf)
		}
	}

	i.log.Info("metadataservice: ExistingImporter: found existing metadata files", "series", sr.ID, "count", len(found))
	if upsertErr := i.upsertIfAny(ctx, found); upsertErr != nil {
		return nil, upsertErr
	}

	// Return found + previouslyImported so the downstream importer
	// chain (subtitles, other extras) doesn't double-process.
	out := make([]metadatafiles.MetadataFile, 0, len(found)+len(filter.PreviouslyImported))
	out = append(out, found...)
	out = append(out, filter.PreviouslyImported...)
	return out, nil
}

// resolvedEpisode is the projection [augmentEpisode] returns —
// just the two fields the importer stamps onto MetadataFile rows.
type resolvedEpisode struct {
	seasonNumber  int
	episodeFileID int
}

// augmentEpisode resolves a candidate path's per-episode fields.
// Returns nil when the resolution fails per C# `:81-91` skip rules:
//   - aggregator is nil (the Go-port nil-tolerant shortcut);
//   - aggregator errors (the C# `AugmentingFailedException` catch);
//   - aggregator returns no episodes;
//   - aggregator returns episodes spanning multiple files (the
//     C# `DistinctBy(EpisodeFileId).Count() > 1` skip).
//
// The C# code uses `localEpisode.Episodes.First().EpisodeFileId`
// after the distinct check; the Go port mirrors that selection.
func (i *ExistingImporter) augmentEpisode(ctx context.Context, sr *series.Series, path string) (*resolvedEpisode, error) {
	if i.aggregator == nil {
		return nil, nil
	}
	eps, err := i.aggregator.Augment(ctx, sr, path)
	if err != nil {
		return nil, err
	}
	if len(eps) == 0 {
		return nil, nil
	}
	// Multi-file-spanning check: skip when episodes span more than
	// one EpisodeFileID. Mirrors C# `:87`.
	seen := make(map[int]struct{}, 2)
	for _, e := range eps {
		efID := 0
		if e.EpisodeFileID != nil {
			efID = *e.EpisodeFileID
		}
		seen[efID] = struct{}{}
		if len(seen) > 1 {
			return nil, nil
		}
	}
	first := eps[0]
	resolved := &resolvedEpisode{seasonNumber: first.SeasonNumber}
	if first.EpisodeFileID != nil {
		resolved.episodeFileID = *first.EpisodeFileID
	}
	return resolved, nil
}

// filterAndClean implements the C# `:41` FilterAndClean invocation.
// Mirrors `ImportExistingExtraFilesBase.FilterAndClean` at
// `:24-40`:
//
//   - Fetch every existing MetadataFile row for the series;
//   - keepExistingEntries=true (rename mid-flight) → DeleteMany on
//     the rows whose paths are STILL on disk (incomplete imports
//     from the in-flight rename) + filter from a clean slate.
//   - keepExistingEntries=false → Clean (delete imported-elsewhere +
//     gone-from-disk rows) + filter against the cleaned set.
//
// Returns the per-call FilterResult.
func (i *ExistingImporter) filterAndClean(ctx context.Context, sr *series.Series, filesOnDisk, importedFiles []string, keepExistingEntries bool) (FilterResult, error) {
	seriesFiles, err := i.files.FilesBySeries(ctx, sr.ID)
	if err != nil {
		return FilterResult{}, fmt.Errorf("metadataservice: ExistingImporter: FilesBySeries(%d): %w", sr.ID, err)
	}

	if keepExistingEntries {
		var incomplete []int
		for _, sf := range seriesFiles {
			fullPath := filepath.Join(sr.Path, sf.RelativePath)
			if pathInSet(fullPath, filesOnDisk) {
				incomplete = append(incomplete, sf.ID)
			}
		}
		if len(incomplete) > 0 {
			if delErr := i.files.DeleteMany(ctx, incomplete); delErr != nil {
				return FilterResult{}, fmt.Errorf("metadataservice: ExistingImporter: DeleteMany incomplete: %w", delErr)
			}
		}
		return i.filter(sr, filesOnDisk, importedFiles, nil), nil
	}

	// Clean: delete rows that are imported-elsewhere or gone-from-disk.
	var alreadyImportedIDs, deletedIDs []int
	for _, sf := range seriesFiles {
		fullPath := filepath.Join(sr.Path, sf.RelativePath)
		if pathInSet(fullPath, importedFiles) {
			alreadyImportedIDs = append(alreadyImportedIDs, sf.ID)
			continue
		}
		if !pathInSet(fullPath, filesOnDisk) {
			deletedIDs = append(deletedIDs, sf.ID)
		}
	}
	if len(alreadyImportedIDs) > 0 {
		if delErr := i.files.DeleteMany(ctx, alreadyImportedIDs); delErr != nil {
			return FilterResult{}, fmt.Errorf("metadataservice: ExistingImporter: DeleteMany imported-elsewhere: %w", delErr)
		}
	}
	if len(deletedIDs) > 0 {
		if delErr := i.files.DeleteMany(ctx, deletedIDs); delErr != nil {
			return FilterResult{}, fmt.Errorf("metadataservice: ExistingImporter: DeleteMany gone-from-disk: %w", delErr)
		}
	}
	// Re-fetch seriesFiles after the cleanup so the filter sees the
	// post-clean state. Cheaper than tracking which IDs survived.
	postClean, err := i.files.FilesBySeries(ctx, sr.ID)
	if err != nil {
		return FilterResult{}, fmt.Errorf("metadataservice: ExistingImporter: FilesBySeries(%d) post-clean: %w", sr.ID, err)
	}
	return i.filter(sr, filesOnDisk, importedFiles, postClean), nil
}

// filter is the C# `:42-53` private helper that splits filesOnDisk
// into "previouslyImported" vs "filteredFiles" (the candidate list
// per-writer classification iterates).
//
//   - previouslyImported = seriesFiles ∩ filesOnDisk (case-
//     insensitive path compare).
//   - filteredFiles = filesOnDisk − previouslyImported − importedFiles.
func (i *ExistingImporter) filter(sr *series.Series, filesOnDisk, importedFiles []string, seriesFiles []metadatafiles.MetadataFile) FilterResult {
	// Build the previouslyImported set + a set of those full-paths
	// for the candidate exclusion.
	var previouslyImported []metadatafiles.MetadataFile
	previouslyImportedPaths := make(map[string]struct{}, len(seriesFiles))
	onDiskSet := make(map[string]struct{}, len(filesOnDisk))
	for _, p := range filesOnDisk {
		onDiskSet[normalizePath(p)] = struct{}{}
	}
	for _, sf := range seriesFiles {
		fullPath := filepath.Join(sr.Path, sf.RelativePath)
		if _, ok := onDiskSet[normalizePath(fullPath)]; ok {
			previouslyImported = append(previouslyImported, sf)
			previouslyImportedPaths[normalizePath(fullPath)] = struct{}{}
		}
	}

	// Build the importedFiles set for candidate exclusion.
	importedSet := make(map[string]struct{}, len(importedFiles))
	for _, p := range importedFiles {
		importedSet[normalizePath(p)] = struct{}{}
	}

	var candidates []string
	for _, p := range filesOnDisk {
		k := normalizePath(p)
		if _, isPrev := previouslyImportedPaths[k]; isPrev {
			continue
		}
		if _, isImported := importedSet[k]; isImported {
			continue
		}
		candidates = append(candidates, p)
	}
	return FilterResult{
		PreviouslyImported: previouslyImported,
		FilesOnDisk:        candidates,
	}
}

// upsertIfAny is the same helper Service.upsertIfAny uses — kept
// inline rather than refactored to a shared file for clarity. nil /
// empty slice → no Upsert call.
func (i *ExistingImporter) upsertIfAny(ctx context.Context, files []metadatafiles.MetadataFile) error {
	if len(files) == 0 {
		return nil
	}
	return i.files.Upsert(ctx, files)
}

// pathInSet reports whether `p` appears in `paths` under the
// case-insensitive comparison that mirrors C# PathEqualityComparer.
func pathInSet(p string, paths []string) bool {
	want := normalizePath(p)
	for _, candidate := range paths {
		if normalizePath(candidate) == want {
			return true
		}
	}
	return false
}

// normalizePath returns the case-folded clean form of `p` — the
// canonical key the set helpers use. Mirrors C#
// `PathEqualityComparer.Instance` which is case-insensitive on all
// platforms (documented Linux divergence; faithful to C# parity).
func normalizePath(p string) string {
	return strings.ToLower(filepath.Clean(p))
}
