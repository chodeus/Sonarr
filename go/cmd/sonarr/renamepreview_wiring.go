package main

import (
	"context"
	"log/slog"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/namingconfig"
	"sonarr.tv/sonarr/internal/organizer"
	"sonarr.tv/sonarr/internal/renamepreview"
	"sonarr.tv/sonarr/internal/series"
)

// renamePreviewPathBuilder implements [renamepreview.PathBuilder] by
// loading the active NamingConfig row per call and forwarding to
// [organizer.BuildFilePath]. Mirrors the slice-3.96 nameBuilderAdapter
// pattern, but additionally threads the file's parsed MediaInfo into
// BuildOptions so {MediaInfo ...} tokens render in the preview path
// (the import-path nameBuilderAdapter leaves MediaInfo nil because it
// receives a LocalEpisode, not a persisted file).
//
// CustomFormats stays nil — the persisted form lacks the typed
// Specifications slice BuildOptions wants, same documented divergence
// as nameBuilderAdapter.BuildFilePath. A GetConfig error surfaces as
// a logged warning + a zero-value BuildOptions (RenameEpisodes=false
// ⇒ original filename), so a preview row is suppressed rather than
// reporting a spurious rename during a transient namingconfig outage.
type renamePreviewPathBuilder struct {
	svc *namingconfig.Service
	log *slog.Logger
}

func (b renamePreviewPathBuilder) BuildFilePath(eps []episodes.Episode, s *series.Series, file *episodefiles.File, extension string) (string, error) {
	opts := organizer.BuildOptions{}
	cfg, err := b.svc.GetConfig(context.Background())
	if err != nil || cfg == nil {
		b.log.Warn("renamepreview: GetConfig failed; using zero BuildOptions", "err", err)
	} else {
		opts = organizer.BuildOptions{
			RenameEpisodes:               cfg.RenameEpisodes,
			StandardEpisodeFormat:        cfg.StandardEpisodeFormat,
			DailyEpisodeFormat:           cfg.DailyEpisodeFormat,
			AnimeEpisodeFormat:           cfg.AnimeEpisodeFormat,
			SeriesFolderFormat:           cfg.SeriesFolderFormat,
			SeasonFolderFormat:           cfg.SeasonFolderFormat,
			SpecialsFolderFormat:         cfg.SpecialsFolderFormat,
			MultiEpisodeStyle:            organizer.MultiEpisodeStyle(cfg.MultiEpisodeStyle),
			ReplaceIllegalCharacters:     cfg.ReplaceIllegalCharacters,
			ColonReplacementFormat:       organizer.ColonReplacementFormat(cfg.ColonReplacementFormat),
			CustomColonReplacementFormat: cfg.CustomColonReplacementFormat,
		}
	}
	if file != nil {
		opts.MediaInfo = file.MediaInfo
	}
	return organizer.BuildFilePath(eps, s, file, extension, opts)
}

// renamePreviewerAdapter wraps the *renamepreview.Service driver and
// projects its [renamepreview.Preview] rows onto the API-layer-neutral
// [apiv3.RenamePreview] shape so the rename handlers depend only on
// the apiv3.RenamePreviewer interface.
type renamePreviewerAdapter struct {
	svc *renamepreview.Service
}

func (a renamePreviewerAdapter) GetBySeries(ctx context.Context, seriesID int) ([]apiv3.RenamePreview, error) {
	rows, err := a.svc.GetBySeries(ctx, seriesID)
	if err != nil {
		return nil, err
	}
	return toAPIRenamePreviews(rows), nil
}

func (a renamePreviewerAdapter) GetBySeason(ctx context.Context, seriesID, seasonNumber int) ([]apiv3.RenamePreview, error) {
	rows, err := a.svc.GetBySeason(ctx, seriesID, seasonNumber)
	if err != nil {
		return nil, err
	}
	return toAPIRenamePreviews(rows), nil
}

func (a renamePreviewerAdapter) GetBulk(ctx context.Context, seriesIDs []int) ([]apiv3.RenamePreview, error) {
	rows, err := a.svc.GetBulk(ctx, seriesIDs)
	if err != nil {
		return nil, err
	}
	return toAPIRenamePreviews(rows), nil
}

func toAPIRenamePreviews(rows []renamepreview.Preview) []apiv3.RenamePreview {
	out := make([]apiv3.RenamePreview, 0, len(rows))
	for _, r := range rows {
		out = append(out, apiv3.RenamePreview{
			SeriesID:       r.SeriesID,
			SeasonNumber:   r.SeasonNumber,
			EpisodeNumbers: r.EpisodeNumbers,
			EpisodeFileID:  r.EpisodeFileID,
			ExistingPath:   r.ExistingPath,
			NewPath:        r.NewPath,
		})
	}
	return out
}

// newRenamePreviewer constructs the GET /rename + /rename/bulk preview
// driver from the existing series/episodes/episodefiles services + a
// NamingConfig-backed path builder. Returns nil when any required
// service is unset (handlers then emit `[]`).
func newRenamePreviewer(seriesSvc *series.Service, episodesSvc *episodes.Service, episodeFilesSvc *episodefiles.Service, namingConfigSvc *namingconfig.Service, log *slog.Logger) apiv3.RenamePreviewer {
	if seriesSvc == nil || episodesSvc == nil || episodeFilesSvc == nil || namingConfigSvc == nil {
		return nil
	}
	builder := renamePreviewPathBuilder{svc: namingConfigSvc, log: log}
	svc := renamepreview.NewService(seriesSvc, episodesSvc, episodeFilesSvc, builder, log)
	return renamePreviewerAdapter{svc: svc}
}
