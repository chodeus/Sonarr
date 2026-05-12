// Package xbmc ports the C# `NzbDrone.Core.Extras.Metadata.Consumers
// .Xbmc.XbmcMetadata` concrete metadata-writer — the Kodi / Emby /
// Jellyfin family that emits `tvshow.nfo` / `<episode>.nfo` /
// `poster.jpg` / `season01-poster.jpg` / `<episode>-thumb.jpg`
// sidecar files alongside imported episodes.
//
// Queue [W2] of Phase 4 focused-subset 6 (METADATA-WRITERS family).
// Slice 4.BB ships sub-arc W2a — Settings, NfoDetector, KodiEpisodeGuide,
// and the [XbmcMetadata] struct foundation: Name, GetFilenameAfterMove,
// FindMetadataFile, Test, RequestAction.
//
// Out-of-scope for sub-arc W2a (lands in sub-arc W2b):
//   - [XbmcMetadata.SeriesMetadata] XML emission (~135 LOC of XElement
//     build-up plus actor / genre / tag rendering — gated on decoded
//     series.Actors / series.Genres / series.Tags lookup);
//   - [XbmcMetadata.EpisodeMetadata] XML emission (~145 LOC including
//     the MediaInfo streamdetails sub-document);
//   - [XbmcMetadata.SeriesImages] / SeasonImages / EpisodeImages
//     remote-URL emission (gated on decoded series.Images +
//     mediacover.Service.GetCoverPath wiring);
//   - init() registration into [metadata.Default] (deferred until the
//     XML body lands so an admin enabling the writer doesn't silently
//     get no NFO output).
//
// Stubbed methods in this slice return the [metadata.Provider]
// zero-value-skip sentinel (RelativePath == "") matching the C# null
// return — the consumer-facing MetadataService treats that as "writer
// chose not to emit", so the foundation is safe to consume even while
// the body is still being filled in.
//
// All C# cites lead each type or function with a file:line reference.
package xbmc

import "sonarr.tv/sonarr/internal/metadata"

// Settings ports `XbmcMetadataSettings` at
// `src/NzbDrone.Core/Extras/Metadata/Consumers/Xbmc/XbmcMetadataSettings.cs:12-57`.
//
// All 8 fields are non-nullable C# bools → Go bools per JSON
// Option A. JSON tags match the persisted Settings JSON the React UI
// posts (lowerCamelCase C# property names via Newtonsoft's default
// contract resolver). NO `omitempty` per go/JSON_CONVENTIONS.md.
//
// Default values mirror the C# constructor at `XbmcMetadataSettings.cs:16-25`:
// SeriesMetadata + EpisodeMetadata + SeriesImages + SeasonImages +
// EpisodeImages default to true; the other three default to false.
type Settings struct {
	// SeriesMetadata controls emission of the per-series `tvshow.nfo`
	// sidecar. C# `:28`. Default true.
	SeriesMetadata bool `json:"seriesMetadata"`

	// SeriesMetadataEpisodeGuide controls embedding the Kodi-format
	// `<episodeguide>` JSON sub-document inside `tvshow.nfo`. C# `:31`.
	// Default false (advanced setting).
	SeriesMetadataEpisodeGuide bool `json:"seriesMetadataEpisodeGuide"`

	// SeriesMetadataUrl controls appending a TVDB browser URL to the
	// `tvshow.nfo` body (allows the user to right-click → "open in
	// browser" inside Kodi). C# `:34`. Default false (advanced).
	SeriesMetadataUrl bool `json:"seriesMetadataUrl"`

	// EpisodeMetadata controls emission of the per-episode
	// `<filename>.nfo` sidecar. C# `:37`. Default true.
	EpisodeMetadata bool `json:"episodeMetadata"`

	// EpisodeImageThumb controls embedding a `<thumb>` element with
	// the screenshot remote URL inside the per-episode `.nfo` (a
	// no-op for Kodi/Emby setups that prefer the disk-side
	// `<filename>-thumb.jpg`). C# `:40`. Default false (advanced).
	EpisodeImageThumb bool `json:"episodeImageThumb"`

	// SeriesImages controls emission of the per-series `poster.jpg`
	// / `banner.jpg` / `fanart.jpg` sidecars. C# `:43`. Default true.
	SeriesImages bool `json:"seriesImages"`

	// SeasonImages controls emission of the per-season `seasonNN-
	// poster.jpg` / `seasonNN-banner.jpg` / `season-specials-
	// poster.jpg` sidecars. C# `:46`. Default true.
	SeasonImages bool `json:"seasonImages"`

	// EpisodeImages controls emission of the per-episode
	// `<filename>-thumb.jpg` sidecar. C# `:49`. Default true.
	EpisodeImages bool `json:"episodeImages"`
}

// ConfigContractName is the C# class name of the Settings type —
// stored verbatim in the Metadata.ConfigContract column and used as
// the [metadata.Registry] lookup key. Mirrors C# `Type.Name` ==
// "XbmcMetadataSettings".
const ConfigContractName = "XbmcMetadataSettings"

// ImplementationName is the C# class name of the XbmcMetadata
// provider — stored verbatim in the Metadata.Implementation column
// and used as the [metadata.Registry] lookup key.
const ImplementationName = "XbmcMetadata"

// DefaultSettings returns a Settings struct populated with the C#
// constructor defaults at `XbmcMetadataSettings.cs:16-25`. The five
// true-by-default fields are the "useful subset" for a fresh Kodi
// install; the three false-by-default fields are advanced overrides.
func DefaultSettings() Settings {
	return Settings{
		SeriesMetadata:             true,
		SeriesMetadataEpisodeGuide: false,
		SeriesMetadataUrl:          false,
		EpisodeMetadata:            true,
		EpisodeImageThumb:          false,
		SeriesImages:               true,
		SeasonImages:               true,
		EpisodeImages:              true,
	}
}

// Validate runs the FluentValidation-equivalent rules from the C#
// `XbmcSettingsValidator` at `XbmcMetadataSettings.cs:8-10`.
//
// The C# validator is empty — XbmcMetadataSettings carries only
// bool toggles with no per-field validation rules. The Go port keeps
// the method on the [Settings] type for parity with notifications /
// importlists settings' Validate() shape; returns nil (no failures)
// in every case.
//
// Kept as a method-on-pointer to leave room for a future C# rule
// without changing the API surface.
func (s *Settings) Validate() []metadata.TestFailure {
	return nil
}
