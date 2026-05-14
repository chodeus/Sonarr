package v3

import (
	"time"

	"sonarr.tv/sonarr/internal/episodefiles"
)

// EpisodeFileResource is the JSON shape produced by
// GET /api/v3/episodefile + the SignalR `episodefile/created` +
// `/updated` + `/deleted` envelopes. Mirrors
// `src/Sonarr.Api.V3/EpisodeFiles/EpisodeFileResource.cs`.
//
// This slice ships the scalar-passthrough subset the SignalR
// envelope uses. The richer joined fields — Languages (typed
// Language[] DTO), Quality (full QualityModel with revision +
// proper), CustomFormats + CustomFormatScore (decision-engine
// joins), MediaInfo (per-stream codec metadata via the
// MediaInfoResource subtype), IndexerFlags + ReleaseType + Release
// Hash + OriginalFilePath bool-checked fields — are DEFERRED to a
// follow-up slice that lands the LIST endpoint at GET /api/v3/
// episodefile (currently unwired — the recording corpus exercises
// only the 404 stack-trace path which isn't byte-eq-able from Go).
//
// Field-source mapping (C# → Go), scalar subset wired here:
//
//	id            → File.ID
//	seriesId      → File.SeriesID
//	seasonNumber  → File.SeasonNumber
//	relativePath  → File.RelativePath
//	path          → "" (computed from Series.Path + RelativePath
//	                in C#; the Go port DEFERS the join — emit empty)
//	size          → File.Size
//	dateAdded     → File.DateAdded
//	sceneName     → File.SceneName
//	releaseGroup  → File.ReleaseGroup
//
// DEFERRED for the populated-body emit: languages, quality,
// customFormats, customFormatScore, indexerFlags, releaseType,
// mediaInfo, episodeHasFile (per-episode rollup).
type EpisodeFileResource struct {
	ID           int       `json:"id"`
	SeriesID     int       `json:"seriesId"`
	SeasonNumber int       `json:"seasonNumber"`
	RelativePath string    `json:"relativePath"`
	Path         string    `json:"path"`
	Size         int64     `json:"size"`
	DateAdded    time.Time `json:"dateAdded"`
	SceneName    string    `json:"sceneName,omitempty"`
	ReleaseGroup string    `json:"releaseGroup,omitempty"`
}

// ToEpisodeFileResource projects the persisted episodefiles.File
// to the wire shape. `seriesPath` is the parent series' Path field
// which the C# tree joins to build the absolute Path; pass empty
// when the join isn't available (the resulting Path field on the
// wire is then just the bare RelativePath, matching the C#
// `Path.Combine("", relative)` fallback).
func ToEpisodeFileResource(f episodefiles.File, seriesPath string) EpisodeFileResource {
	path := f.RelativePath
	if seriesPath != "" && path != "" {
		path = seriesPath + "/" + path
	}
	return EpisodeFileResource{
		ID:           f.ID,
		SeriesID:     f.SeriesID,
		SeasonNumber: f.SeasonNumber,
		RelativePath: f.RelativePath,
		Path:         path,
		Size:         f.Size,
		DateAdded:    f.DateAdded,
		SceneName:    f.SceneName,
		ReleaseGroup: f.ReleaseGroup,
	}
}
