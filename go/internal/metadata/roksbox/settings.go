// Package roksbox ports the C# `NzbDrone.Core.Extras.Metadata.Consumers
// .Roksbox.RoksboxMetadata` concrete metadata-writer — the Roksbox
// video-player consumer that emits per-episode `<stem>.xml` sidecar
// files (NOT .nfo) plus per-episode / per-season / per-series JPG
// thumbnails. Roksbox does NOT support series-level metadata
// emission; SeriesMetadata always returns zero-value per C# parity.
//
// Queue [W4] of Phase 4 focused-subset 6 (METADATA-WRITERS family).
// Slice 4.BG ships [W4] as a single slice — Roksbox is comparable
// in C# size to Plex (~250 C# LOC total) but with richer XML
// emission than Plex's text manifest.
//
// All C# cites lead each type or function with a file:line reference.
package roksbox

import "sonarr.tv/sonarr/internal/metadata"

// Settings ports `RoksboxMetadataSettings` at
// `src/NzbDrone.Core/Extras/Metadata/Consumers/Roksbox/RoksboxMetadataSettings.cs:12-37`.
//
// 4 non-nullable C# bools → Go bools per Option A (no omitempty).
// Defaults mirror the C# constructor at `:16-23` — every flag
// defaults to true.
type Settings struct {
	// EpisodeMetadata gates emission of the per-episode `<stem>.xml`
	// sidecar inside the season folder. C# `:28`. Default true.
	EpisodeMetadata bool `json:"episodeMetadata"`

	// SeriesImages gates emission of the per-series poster JPG at the
	// series root (named after the series folder). C# `:31`. Default
	// true.
	SeriesImages bool `json:"seriesImages"`

	// SeasonImages gates emission of the per-season poster JPG inside
	// each season folder (named after the season folder). C# `:34`.
	// Default true.
	SeasonImages bool `json:"seasonImages"`

	// EpisodeImages gates emission of the per-episode screenshot JPG
	// next to the episode file (just the stem + ".jpg", no -thumb
	// suffix). C# `:37`. Default true.
	EpisodeImages bool `json:"episodeImages"`
}

// ImplementationName is the C# class name of the RoksboxMetadata
// provider — stored verbatim in the Metadata.Implementation column
// and used as the [metadata.Registry] lookup key.
const ImplementationName = "RoksboxMetadata"

// ConfigContractName is the C# class name of the Settings type —
// stored verbatim in the Metadata.ConfigContract column and used as
// the [metadata.Registry] lookup key.
const ConfigContractName = "RoksboxMetadataSettings"

// DefaultSettings returns a Settings struct populated with the C#
// constructor defaults — every flag true.
func DefaultSettings() Settings {
	return Settings{
		EpisodeMetadata: true,
		SeriesImages:    true,
		SeasonImages:    true,
		EpisodeImages:   true,
	}
}

// Validate runs the FluentValidation-equivalent rules from the C#
// `RoksboxSettingsValidator` at `RoksboxMetadataSettings.cs:8-10`.
//
// The C# validator is empty. Returns nil failures always.
func (s *Settings) Validate() []metadata.TestFailure {
	return nil
}
