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
	"log/slog"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/disk"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/ospath"
	"sonarr.tv/sonarr/internal/parser"
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

// Compile-time guard.
var _ apiv3.ManualImportDecisionRejector = manualImportDecisionAdapter{}
