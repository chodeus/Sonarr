package extras

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"sonarr.tv/sonarr/internal/extrafiles"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
	"sonarr.tv/sonarr/internal/subtitlefiles"
)

// EpisodeFileResolver maps a companion file already on disk to the
// EpisodeFile it belongs to. Mirrors the C# `ExistingExtraImporter`
// resolution (`_aggregationService.Augment(localEpisode)` →
// `localEpisode.Episodes` → a single `EpisodeFileId`). ok is false when
// the file doesn't parse to a known episode, or maps to episodes spanning
// more than one episode file (ambiguous — C# skips those).
type EpisodeFileResolver interface {
	ResolveEpisodeFile(ctx context.Context, ser *series.Series, path string) (episodeFileID, seasonNumber int, ok bool)
}

// ExistingExtraService ports `Extras/ExistingExtraFileService.cs` + the
// per-type `Existing*Importer.ProcessFiles`: it imports companion files
// that are ALREADY on disk (a custom import-script left them next to the
// imported media) into the DB, matched to their EpisodeFile — WITHOUT
// moving them (unlike the download→library import managers). Subtitle
// extensions become SubtitleFile rows (with parsed language/tags); every
// other extension becomes an ExtraFile row. Metadata sidecars are left to
// the metadata-writer subsystem (the C# ExistingMetadataImporter), so they
// are imported here as plain "other" extras — faithful for the .nfo case.
type ExistingExtraService struct {
	subStore   SubtitleStore
	extraStore ExtraFileStore
	resolver   EpisodeFileResolver
	log        *slog.Logger
}

// NewExistingExtraService builds the service. All deps MUST be non-nil
// except log (defaults to [slog.Default]).
func NewExistingExtraService(subStore SubtitleStore, extraStore ExtraFileStore, resolver EpisodeFileResolver, log *slog.Logger) (*ExistingExtraService, error) {
	if subStore == nil || extraStore == nil || resolver == nil {
		return nil, fmt.Errorf("extras: NewExistingExtraService: nil dependency")
	}
	if log == nil {
		log = slog.Default()
	}
	return &ExistingExtraService{subStore: subStore, extraStore: extraStore, resolver: resolver, log: log}, nil
}

// ImportExisting imports the given on-disk companion files for the series,
// returning the count tracked. Ports `ExistingExtraFileService.ImportExtraFiles`.
func (s *ExistingExtraService) ImportExisting(ctx context.Context, ser *series.Series, files []string) (int, error) {
	if s == nil || ser == nil || len(files) == 0 {
		return 0, nil
	}
	count := 0
	for _, f := range files {
		ext := filepath.Ext(f)
		if ext == "" {
			continue
		}
		fileID, season, ok := s.resolver.ResolveEpisodeFile(ctx, ser, f)
		if !ok {
			s.log.Debug("extras: existing-import: no single matching episode file", "file", f)
			continue
		}
		rel, err := filepath.Rel(ser.Path, f)
		if err != nil {
			s.log.Debug("extras: existing-import: relpath failed", "file", f, "err", err)
			continue
		}

		if IsSubtitleExtension(ext) {
			row := &subtitlefiles.SubtitleFile{
				SeriesID:      ser.ID,
				SeasonNumber:  season,
				EpisodeFileID: fileID,
				RelativePath:  rel,
				Extension:     ext,
				Language:      parser.ParseSubtitleLanguage(f).ID,
				LanguageTags:  parser.ParseLanguageTags(f),
			}
			if err := s.subStore.Upsert(ctx, row); err != nil {
				s.log.Warn("extras: existing-import: persist subtitle failed", "path", rel, "err", err)
				continue
			}
		} else {
			row := &extrafiles.ExtraFile{
				SeriesID:      ser.ID,
				SeasonNumber:  season,
				EpisodeFileID: fileID,
				RelativePath:  rel,
				Extension:     ext,
			}
			if err := s.extraStore.Upsert(ctx, row); err != nil {
				s.log.Warn("extras: existing-import: persist extra failed", "path", rel, "err", err)
				continue
			}
		}
		count++
	}
	return count, nil
}
