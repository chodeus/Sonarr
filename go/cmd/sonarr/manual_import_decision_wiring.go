// Boot-time adapter for the J356 ManualImport reprocess GetDecision
// rejections. The reprocess handler discovers this via optional-
// extension type-assertion on its `calc` dep (the
// parsingServiceWithPopulator carrier delegates to this adapter), so
// the handler signature stays unchanged.
//
// Mirrors C# `ManualImportService.ReprocessItem` at
// `src/NzbDrone.Core/MediaFiles/EpisodeImport/Manual/ManualImportService.cs:157-200`
// (the episodeIds branch): assemble a LocalEpisode from the user's
// reprocessed item values + resolved series/episodes, run the SAME
// production AggregationService the real download-import flow uses, run
// the ImportDecisionMaker over it, and surface the decision's
// rejections. Reusing the production aggregation service keeps the
// manual-import rejection list consistent with what the real import
// pipeline would produce for the same inputs.

package main

import (
	"context"
	"crypto/sha1" //nolint:gosec // C# parity: HashConverter.GetHashInt31 uses SHA-1 for stable wire ids (NOT a security primitive).
	"encoding/binary"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/disk"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/mediafiles"
	"sonarr.tv/sonarr/internal/ospath"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/parsingservice"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
)

// manualImportDecisionAdapter satisfies
// [apiv3.ManualImportDecisionRejector]. Holds the production
// ImportDecisionMaker + AggregationService + the series/episode/disk
// lookups needed to assemble a LocalEpisode from a reprocess item.
type manualImportDecisionAdapter struct {
	idm      *episodeimport.ImportDecisionMaker
	agg      *episodeimport.AggregationService
	series   *series.Service
	episodes *episodes.Service
	disk     *disk.Provider
	parsing  *parsingservice.Service
	log      *slog.Logger
}

// ReprocessDecisionRejections assembles a LocalEpisode from the
// reprocessed item, runs Augment + GetDecision, and projects the
// resulting rejections. Empty slice (never nil) is returned on every
// path so the handler emits `[]` not `null`.
func (a manualImportDecisionAdapter) ReprocessDecisionRejections(ctx context.Context, item *apiv3.ManualImportReprocessItem, parsed *parser.ParsedEpisodeInfo, episodeIDs []int) []apiv3.ManualImportRejectionResource {
	empty := []apiv3.ManualImportRejectionResource{}
	if a.idm == nil || item == nil || item.SeriesID == 0 {
		return empty
	}
	ser, err := a.series.GetSeries(ctx, item.SeriesID)
	if err != nil || ser == nil {
		a.log.Debug("manualImportDecision: series lookup failed; no rejections", "seriesId", item.SeriesID, "err", err)
		return empty
	}

	// Resolve the user's selected episodes to full rows (carrying
	// EpisodeFileID, AirDateUtc, etc. — the UpgradeSpecification +
	// EpisodeTitleSpecification need those fields).
	var eps []episodes.Episode
	if a.episodes != nil {
		for _, id := range episodeIDs {
			ep, err := a.episodes.GetEpisode(ctx, id)
			if err != nil || ep == nil {
				continue
			}
			eps = append(eps, *ep)
		}
	}

	le := episodeimport.NewLocalEpisode()
	le.Series = ser
	le.Episodes = eps
	le.FileEpisodeInfo = parsed
	le.Path = item.Path
	if a.disk != nil {
		if size, err := a.disk.GetFileSize(item.Path); err == nil {
			le.Size = size
		}
	}
	le.ExistingFile = ospath.IsParent(ser.Path, item.Path)
	le.Quality = qualityModelFromResource(item.Quality)
	le.Languages = manualImportLanguages(item.Languages)
	le.ReleaseGroup = item.ReleaseGroup
	le.IndexerFlags = parser.IndexerFlags(item.IndexerFlags)
	le.ReleaseType = parser.ParseReleaseType(item.ReleaseType)

	// Augment with the production aggregation service (C#
	// `ManualImportService.cs:189`). A nil download item matches the
	// bare-path reprocess flow (no tracked-download resolution). An
	// AugmentingFailedException maps to an UnableToParse rejection
	// (C# catch at the ImportDecisionMaker level).
	var dci *downloadclients.Item
	if a.agg != nil {
		if _, err := a.agg.Augment(le, dci); err != nil {
			a.log.Debug("manualImportDecision: augment failed; UnableToParse", "path", item.Path, "err", err)
			return []apiv3.ManualImportRejectionResource{{
				Reason: "Unable to parse file",
				Type:   "permanent",
			}}
		}
	}

	// Re-apply the user-chosen values after aggregation (C#
	// `:191-198`) — Augment may overwrite them from file-derived data.
	le.Series = ser
	le.Episodes = eps
	le.Quality = qualityModelFromResource(item.Quality)
	le.Languages = manualImportLanguages(item.Languages)
	le.ReleaseGroup = item.ReleaseGroup
	le.IndexerFlags = parser.IndexerFlags(item.IndexerFlags)
	le.ReleaseType = parser.ParseReleaseType(item.ReleaseType)

	decision := a.idm.GetDecision(le, dci)
	if decision == nil || len(decision.Rejections) == 0 {
		return empty
	}
	out := make([]apiv3.ManualImportRejectionResource, 0, len(decision.Rejections))
	for _, r := range decision.Rejections {
		out = append(out, apiv3.ManualImportRejectionResource{
			Reason: r.Message,
			Type:   r.Kind.String(),
		})
	}
	return out
}

// qualityModelFromResource reconstructs a [quality.Model] from the
// reprocessed item's expanded wire shape. Resolves the registry Quality
// by id (falling back to a minimal Quality carrying the wire fields when
// the id is unknown), and copies the Revision verbatim.
func qualityModelFromResource(res *apiv3.QualityModelResource) quality.Model {
	if res == nil {
		return quality.Model{}
	}
	q, ok := quality.FindByID(res.Quality.ID)
	if !ok {
		q = quality.Quality{ID: res.Quality.ID, Name: res.Quality.Name, Resolution: res.Quality.Resolution}
	}
	return quality.Model{
		Quality: q,
		Revision: quality.Revision{
			Version:  res.Revision.Version,
			Real:     res.Revision.Real,
			IsRepack: res.Revision.IsRepack,
		},
	}
}

// manualImportLanguages maps the item's []LanguageRef to typed
// []language.Language, skipping unresolvable ids (matches the
// reprocess handler's own languagesFromLanguageRefs helper).
func manualImportLanguages(refs []apiv3.LanguageRef) []language.Language {
	if len(refs) == 0 {
		return nil
	}
	out := make([]language.Language, 0, len(refs))
	for _, ref := range refs {
		if l, ok := language.FindByID(ref.ID); ok {
			out = append(out, l)
		}
	}
	return out
}

// GetMediaFiles ports the disk-mode arm of C#
// `ManualImportService.GetMediaFiles(path, downloadId, seriesId,
// filterExistingFiles)` (`ManualImportService.cs:116-142`) via the
// controller dispatch at `ManualImportController.cs:25-35`.
//
// J387a scope — the series-resolved disk walk:
//   - folder path → ProcessFolder: resolve series by parsing the folder
//     name, scan video files, run GetImportDecisions, map each decision.
//   - single file path → ProcessFile: resolve series by parsing the file
//     name, run GetImportDecisions over the one file, map.
//
// Deferred to J387b (each returns an empty list with a debug log here):
//   - library mode (seriesId set + no downloadId → GetFilesBySeries/Season
//   - unmapped disk scan; controller's first branch).
//   - downloadId mode (tracked-download output path).
//   - the unknown-series fallback (per-file UnknownSeries rejections +
//     recursive subfolder descent + the >100-file ProcessDownloadDirectory
//     short-circuit). When the series can't be resolved this returns `[]`,
//     matching the pre-J387 stub for that case.
func (a manualImportDecisionAdapter) GetMediaFiles(ctx context.Context, q apiv3.ManualImportQuery) ([]apiv3.ManualImportResource, error) {
	empty := []apiv3.ManualImportResource{}
	if a.idm == nil || a.disk == nil {
		return empty, nil
	}

	// Controller dispatch: seriesId + no downloadId → library mode (J387b).
	if q.SeriesID != nil && strings.TrimSpace(q.DownloadID) == "" {
		a.log.Debug("manualImport.GetMediaFiles: library mode deferred to J387b", "seriesId", *q.SeriesID)
		return empty, nil
	}
	// downloadId mode (J387b).
	if strings.TrimSpace(q.DownloadID) != "" {
		a.log.Debug("manualImport.GetMediaFiles: downloadId mode deferred to J387b", "downloadId", q.DownloadID)
		return empty, nil
	}

	path := strings.TrimSpace(q.Folder)
	if path == "" {
		return empty, nil
	}
	if a.disk.FolderExists(path) {
		return a.processManualFolder(ctx, path, q.FilterExistingFiles)
	}
	if a.disk.FileExists(path) {
		return a.processManualFile(ctx, path)
	}
	return empty, nil
}

// processManualFolder ports the series-resolved arm of C# ProcessFolder
// (`ManualImportService.cs:293-298`).
func (a manualImportDecisionAdapter) processManualFolder(ctx context.Context, folder string, filterExisting bool) ([]apiv3.ManualImportResource, error) {
	empty := []apiv3.ManualImportResource{}
	folderName := ospath.Base(folder)

	ser := a.resolveSeries(ctx, folderName)
	if ser == nil {
		a.log.Debug("manualImport: series unresolved from folder; unknown-series fallback deferred to J387b", "folder", folder)
		return empty, nil
	}

	videoFiles, err := mediafiles.GetVideoFiles(a.disk, folder, true)
	if err != nil {
		a.log.Debug("manualImport: GetVideoFiles failed", "folder", folder, "err", err)
		return empty, nil
	}
	videoFiles, err = mediafiles.FilterPaths(folder, videoFiles, true)
	if err != nil {
		a.log.Debug("manualImport: FilterPaths failed", "folder", folder, "err", err)
		return empty, nil
	}

	decisions, err := a.idm.GetImportDecisions(ctx, videoFiles, ser, episodeimport.GetImportDecisionsOptions{
		FolderInfo:          parser.ParseTitle(folderName),
		SceneSource:         a.sceneSource(ser, folder),
		FilterExistingFiles: filterExisting,
	})
	if err != nil {
		return empty, fmt.Errorf("GetImportDecisions: %w", err)
	}
	return a.mapDecisions(decisions, folder, "", folderName), nil
}

// processManualFile ports the series-resolved arm of C# ProcessFile
// (`ManualImportService.cs:349-361`) for a single file path.
func (a manualImportDecisionAdapter) processManualFile(ctx context.Context, file string) ([]apiv3.ManualImportResource, error) {
	empty := []apiv3.ManualImportResource{}
	rootFolder := filepath.Dir(file)

	ser := a.resolveSeries(ctx, nameWithoutExt(file))
	if ser == nil {
		a.log.Debug("manualImport: series unresolved from file; unknown-series fallback deferred to J387b", "file", file)
		return empty, nil
	}

	decisions, err := a.idm.GetImportDecisions(ctx, []string{file}, ser, episodeimport.GetImportDecisionsOptions{
		SceneSource: a.sceneSource(ser, rootFolder),
	})
	if err != nil {
		return empty, fmt.Errorf("GetImportDecisions: %w", err)
	}
	return a.mapDecisions(decisions, rootFolder, "", ""), nil
}

// resolveSeries runs the production parsing-service title resolution.
// (nil, nil) / error both map to nil (unknown series).
func (a manualImportDecisionAdapter) resolveSeries(ctx context.Context, title string) *series.Series {
	if a.parsing == nil {
		return nil
	}
	ser, err := a.parsing.GetSeriesFromTitle(ctx, title)
	if err != nil {
		a.log.Debug("manualImport: GetSeriesFromTitle failed", "title", title, "err", err)
		return nil
	}
	return ser
}

// sceneSource mirrors C# `ManualImportService.SceneSource` at `:397-400`:
// a file is scene-sourced unless it lives at or under the series folder.
func (a manualImportDecisionAdapter) sceneSource(ser *series.Series, folder string) bool {
	if ser == nil {
		return false
	}
	return !(filepath.Clean(ser.Path) == filepath.Clean(folder) || ospath.IsParent(ser.Path, folder))
}

// mapDecisions maps a decision slice to wire resources.
func (a manualImportDecisionAdapter) mapDecisions(decisions []*episodeimport.ImportDecision, rootFolder, downloadID, folderName string) []apiv3.ManualImportResource {
	out := make([]apiv3.ManualImportResource, 0, len(decisions))
	for _, d := range decisions {
		if d == nil || d.LocalEpisode == nil {
			continue
		}
		out = append(out, a.mapDecision(d, rootFolder, downloadID, folderName))
	}
	return out
}

// mapDecision ports C# `ManualImportService.MapItem(ImportDecision, ...)`
// at `:414-459` for the J387a field subset. CustomFormats / CustomFormatScore
// (the LocalEpisodeFormatCalculator pass) and QualityWeight (J387c) emit
// zero values.
func (a manualImportDecisionAdapter) mapDecision(d *episodeimport.ImportDecision, rootFolder, downloadID, folderName string) apiv3.ManualImportResource {
	le := d.LocalEpisode
	res := apiv3.ManualImportResource{
		ID:            hashInt31(le.Path),
		Path:          le.Path,
		RelativePath:  manualImportRelativePath(rootFolder, le.Path),
		FolderName:    folderName,
		Name:          nameWithoutExt(le.Path),
		DownloadID:    downloadID,
		ReleaseGroup:  le.ReleaseGroup,
		IndexerFlags:  int(le.IndexerFlags),
		ReleaseType:   apiv3.CamelCaseEnumName(le.ReleaseType.String()),
		Quality:       apiv3.ToQualityModelResource(le.Quality),
		Languages:     languageRefsFromLanguages(le.Languages),
		Episodes:      []apiv3.EpisodeResource{},
		CustomFormats: []apiv3.CustomFormatResource{},
		Rejections:    mapImportRejections(d.Rejections),
	}
	if size, err := a.disk.GetFileSize(le.Path); err == nil {
		res.Size = size
	}
	// Single-season episodes → SeasonNumber + Episodes (C# `:424-441`).
	if sn, ok := singleSeason(le.Episodes); ok {
		res.SeasonNumber = &sn
		eps := make([]apiv3.EpisodeResource, 0, len(le.Episodes))
		for _, e := range le.Episodes {
			eps = append(eps, apiv3.ToEpisodeResource(e))
		}
		res.Episodes = eps
	}
	if le.Series != nil {
		sr := apiv3.ToSeriesResource(*le.Series)
		res.Series = &sr
	}
	return res
}

// hashInt31 ports C# `HashConverter.GetHashInt31(s)`: SHA1(s) → first 4
// bytes little-endian as int32 with the high bit cleared. Matches the
// backupID helper in api/v3.
func hashInt31(s string) int {
	h := sha1.Sum([]byte(s)) //nolint:gosec // stable wire id, not a security primitive
	return int(binary.LittleEndian.Uint32(h[:4]) & 0x7fffffff)
}

// manualImportRelativePath mirrors C# `rootFolder.GetRelativePath(path)`,
// falling back to the base name when the relation can't be computed.
func manualImportRelativePath(rootFolder, path string) string {
	rel, err := ospath.Relative(rootFolder, path)
	if err != nil {
		return ospath.Base(path)
	}
	return rel
}

// nameWithoutExt mirrors C# `Path.GetFileNameWithoutExtension(path)`.
func nameWithoutExt(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// languageRefsFromLanguages projects the LocalEpisode languages to the
// wire `[{id,name}]` shape (C# serializes `List<Language>` that way).
func languageRefsFromLanguages(langs []language.Language) []apiv3.LanguageRef {
	out := make([]apiv3.LanguageRef, 0, len(langs))
	for _, l := range langs {
		out = append(out, apiv3.ToLanguageRef(l.ID))
	}
	return out
}

// mapImportRejections projects decision rejections to the wire shape.
func mapImportRejections(rs []episodeimport.ImportRejection) []apiv3.ManualImportRejectionResource {
	out := make([]apiv3.ManualImportRejectionResource, 0, len(rs))
	for _, r := range rs {
		out = append(out, apiv3.ManualImportRejectionResource{
			Reason: r.Message,
			Type:   r.Kind.String(),
		})
	}
	return out
}

// singleSeason returns (season, true) when every episode shares one
// season number (C# `:424` distinct-season-count == 1 guard). Empty
// episode lists return (0, false).
func singleSeason(eps []episodes.Episode) (int, bool) {
	if len(eps) == 0 {
		return 0, false
	}
	season := eps[0].SeasonNumber
	for _, e := range eps[1:] {
		if e.SeasonNumber != season {
			return 0, false
		}
	}
	return season, true
}

// Compile-time guards.
var (
	_ apiv3.ManualImportDecisionRejector = manualImportDecisionAdapter{}
	_ apiv3.ManualImportLister           = manualImportDecisionAdapter{}
)
