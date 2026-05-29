// Package renamepreview ports the read side of C#
// `RenameEpisodeFileService.GetRenamePreviews`
// (`src/NzbDrone.Core/MediaFiles/RenameEpisodeFileService.cs:57-128`).
//
// For a series (optionally one season) or a bulk set of series, it
// joins episode files against their linked episodes, builds the path
// `BuildFileName`/`BuildFilePath` would produce under the active
// NamingConfig, and yields a [Preview] for every file whose current
// path differs from that target. The write side (the actual
// `RenameFilesCommand`/`RenameSeriesCommand` move) is NOT ported here
// — this is the GET /rename + /rename/bulk preview driver only.
package renamepreview

import (
	"context"
	"log/slog"
	"path/filepath"
	"sort"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/ospath"
	"sonarr.tv/sonarr/internal/series"
)

// Preview is one rename-preview row — the diff between an episode
// file's current relative path and the relative path the namer would
// produce. Mirrors C# `RenameEpisodeFilePreview`. Both paths are
// expressed relative to the series folder (the C# resource shape).
type Preview struct {
	SeriesID       int
	SeasonNumber   int
	EpisodeNumbers []int
	EpisodeFileID  int
	ExistingPath   string
	NewPath        string
}

// SeriesGetter is the narrow slice of `series.Service` the driver
// needs.
type SeriesGetter interface {
	GetSeries(ctx context.Context, id int) (*series.Series, error)
	GetMany(ctx context.Context, ids []int) ([]series.Series, error)
}

// EpisodeGetter is the narrow slice of `episodes.Service` the driver
// needs.
type EpisodeGetter interface {
	GetEpisodesBySeries(ctx context.Context, seriesID int) ([]episodes.Episode, error)
	GetEpisodesBySeason(ctx context.Context, seriesID, seasonNumber int) ([]episodes.Episode, error)
	GetEpisodesByMultipleSeries(ctx context.Context, seriesIDs []int) ([]episodes.Episode, error)
}

// FileGetter is the narrow slice of `episodefiles.Service` the driver
// needs.
type FileGetter interface {
	GetFilesBySeries(ctx context.Context, seriesID int) ([]episodefiles.File, error)
	GetFilesBySeason(ctx context.Context, seriesID, seasonNumber int) ([]episodefiles.File, error)
	GetFilesBySeriesIDs(ctx context.Context, seriesIDs []int) ([]episodefiles.File, error)
}

// PathBuilder produces the target full path a set of episodes + file
// would be renamed to, under the active NamingConfig. Mirrors the C#
// `IBuildFileNames.BuildFilePath(episodes, series, file, extension)`
// call at `RenameEpisodeFileService.cs:113`. The implementation
// (cmd/sonarr) loads the NamingConfig + MediaInfo and forwards to
// `organizer.BuildFilePath`. The returned path is rooted at the
// series folder (BuildFilePath joins BuildSeasonPath, which embeds
// `series.Path`).
type PathBuilder interface {
	BuildFilePath(eps []episodes.Episode, s *series.Series, file *episodefiles.File, extension string) (string, error)
}

// Service is the rename-preview driver.
type Service struct {
	series  SeriesGetter
	eps     EpisodeGetter
	files   FileGetter
	builder PathBuilder
	log     *slog.Logger
}

// NewService constructs the driver. `log` may be nil (a discard
// logger is substituted).
func NewService(s SeriesGetter, e EpisodeGetter, f FileGetter, b PathBuilder, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.NewTextHandler(noopWriter{}, nil))
	}
	return &Service{series: s, eps: e, files: f, builder: b, log: log}
}

type noopWriter struct{}

func (noopWriter) Write(p []byte) (int, error) { return len(p), nil }

// GetBySeries ports `GetRenamePreviews(int seriesId)`
// (`:57-67`). Ordered descending by SeasonNumber then by the first
// episode number.
func (s *Service) GetBySeries(ctx context.Context, seriesID int) ([]Preview, error) {
	ser, err := s.series.GetSeries(ctx, seriesID)
	if err != nil {
		return nil, err
	}
	eps, err := s.eps.GetEpisodesBySeries(ctx, seriesID)
	if err != nil {
		return nil, err
	}
	files, err := s.files.GetFilesBySeries(ctx, seriesID)
	if err != nil {
		return nil, err
	}
	previews := s.getPreviews(ser, eps, files)
	sort.SliceStable(previews, func(i, j int) bool {
		if previews[i].SeasonNumber != previews[j].SeasonNumber {
			return previews[i].SeasonNumber > previews[j].SeasonNumber
		}
		return firstEp(previews[i]) > firstEp(previews[j])
	})
	return previews, nil
}

// GetBySeason ports `GetRenamePreviews(int seriesId, int
// seasonNumber)` (`:69-77`). Ordered descending by the first episode
// number.
func (s *Service) GetBySeason(ctx context.Context, seriesID, seasonNumber int) ([]Preview, error) {
	ser, err := s.series.GetSeries(ctx, seriesID)
	if err != nil {
		return nil, err
	}
	eps, err := s.eps.GetEpisodesBySeason(ctx, seriesID, seasonNumber)
	if err != nil {
		return nil, err
	}
	files, err := s.files.GetFilesBySeason(ctx, seriesID, seasonNumber)
	if err != nil {
		return nil, err
	}
	previews := s.getPreviews(ser, eps, files)
	sort.SliceStable(previews, func(i, j int) bool {
		return firstEp(previews[i]) > firstEp(previews[j])
	})
	return previews, nil
}

// GetBulk ports `GetRenamePreviews(List<int> seriesIds)`
// (`:79-96`). Ordered descending by SeriesId, then SeasonNumber,
// then the first episode number.
func (s *Service) GetBulk(ctx context.Context, seriesIDs []int) ([]Preview, error) {
	seriesList, err := s.series.GetMany(ctx, seriesIDs)
	if err != nil {
		return nil, err
	}
	allEps, err := s.eps.GetEpisodesByMultipleSeries(ctx, seriesIDs)
	if err != nil {
		return nil, err
	}
	allFiles, err := s.files.GetFilesBySeriesIDs(ctx, seriesIDs)
	if err != nil {
		return nil, err
	}
	epsBySeries := map[int][]episodes.Episode{}
	for _, e := range allEps {
		epsBySeries[e.SeriesID] = append(epsBySeries[e.SeriesID], e)
	}
	filesBySeries := map[int][]episodefiles.File{}
	for _, f := range allFiles {
		filesBySeries[f.SeriesID] = append(filesBySeries[f.SeriesID], f)
	}
	var previews []Preview
	for i := range seriesList {
		ser := &seriesList[i]
		previews = append(previews, s.getPreviews(ser, epsBySeries[ser.ID], filesBySeries[ser.ID])...)
	}
	sort.SliceStable(previews, func(i, j int) bool {
		if previews[i].SeriesID != previews[j].SeriesID {
			return previews[i].SeriesID > previews[j].SeriesID
		}
		if previews[i].SeasonNumber != previews[j].SeasonNumber {
			return previews[i].SeasonNumber > previews[j].SeasonNumber
		}
		return firstEp(previews[i]) > firstEp(previews[j])
	})
	return previews, nil
}

// getPreviews ports the private `GetPreviews` loop (`:98-128`). For
// each file: collect the episodes it's linked to, compute its current
// path + the target path, and emit a Preview when they differ.
func (s *Service) getPreviews(ser *series.Series, eps []episodes.Episode, files []episodefiles.File) []Preview {
	if ser == nil {
		return nil
	}
	var out []Preview
	for i := range files {
		file := &files[i]
		var episodesInFile []episodes.Episode
		for _, e := range eps {
			if e.EpisodeFileID != nil && *e.EpisodeFileID == file.ID {
				episodesInFile = append(episodesInFile, e)
			}
		}
		episodeFilePath := filepath.Join(ser.Path, file.RelativePath)
		if len(episodesInFile) == 0 {
			s.log.Warn("renamepreview: file is not linked to any episodes", "path", episodeFilePath)
			continue
		}
		seasonNumber := episodesInFile[0].SeasonNumber
		newPath, err := s.builder.BuildFilePath(episodesInFile, ser, file, filepath.Ext(episodeFilePath))
		if err != nil {
			s.log.Warn("renamepreview: BuildFilePath failed", "path", episodeFilePath, "err", err)
			continue
		}
		if pathEquals(episodeFilePath, newPath) {
			continue
		}
		newRel, err := ospath.Relative(ser.Path, newPath)
		if err != nil {
			// newPath isn't under series.Path (unexpected — BuildFilePath
			// roots at BuildSeasonPath(series)); fall back to the raw
			// path rather than dropping the row.
			s.log.Warn("renamepreview: newPath not under series folder", "newPath", newPath, "seriesPath", ser.Path)
			newRel = newPath
		}
		nums := make([]int, 0, len(episodesInFile))
		for _, e := range episodesInFile {
			nums = append(nums, e.EpisodeNumber)
		}
		out = append(out, Preview{
			SeriesID:       ser.ID,
			SeasonNumber:   seasonNumber,
			EpisodeNumbers: nums,
			EpisodeFileID:  file.ID,
			ExistingPath:   file.RelativePath,
			NewPath:        newRel,
		})
	}
	return out
}

// firstEp returns the first episode number of a preview (0 if empty).
// Mirrors the C# `EpisodeNumbers.First()` sort key.
func firstEp(p Preview) int {
	if len(p.EpisodeNumbers) == 0 {
		return 0
	}
	return p.EpisodeNumbers[0]
}

// pathEquals reports whether two paths are equal after cleaning.
// Mirrors C# `PathExtensions.PathEquals(..., StringComparison.Ordinal)`
// — case-sensitive comparison of the cleaned paths.
func pathEquals(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}
