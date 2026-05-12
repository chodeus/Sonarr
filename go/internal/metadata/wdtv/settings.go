// Package wdtv ports the C# `NzbDrone.Core.Extras.Metadata.Consumers
// .Wdtv.WdtvMetadata` concrete metadata-writer — the WDTV
// video-player consumer that emits per-episode `<stem>.xml` sidecars
// (with a `<details>` root unlike Roksbox's `<video>`) plus per-episode
// `<stem>.metathumb` thumbnails and folder-named JPGs for series +
// season images.
//
// Queue [W5] of Phase 4 focused-subset 6 (METADATA-WRITERS family).
// Last concrete writer of the family. Slice 4.BH ships [W5] in one
// slice — WDTV is comparable in size to Roksbox (~290 C# LOC) but
// structurally simpler (no certification handling, no actor cap).
//
// All C# cites lead each type or function with a file:line reference.
package wdtv

import "sonarr.tv/sonarr/internal/metadata"

// Settings ports `WdtvMetadataSettings` at
// `src/NzbDrone.Core/Extras/Metadata/Consumers/Wdtv/WdtvMetadataSettings.cs:12-37`.
//
// 4 non-nullable C# bools → Go bools per Option A (no omitempty).
// Defaults mirror the C# constructor at `:16-23` — every flag
// defaults to true.
type Settings struct {
	// EpisodeMetadata gates emission of the per-episode `<stem>.xml`
	// sidecar inside the season folder. C# `:28`. Default true.
	EpisodeMetadata bool `json:"episodeMetadata"`

	// SeriesImages gates emission of the per-series `folder.jpg` at
	// the series root. C# `:31`. Default true.
	SeriesImages bool `json:"seriesImages"`

	// SeasonImages gates emission of the per-season `folder.jpg`
	// inside each season folder. C# `:34`. Default true.
	SeasonImages bool `json:"seasonImages"`

	// EpisodeImages gates emission of the per-episode screenshot
	// `<stem>.metathumb` (NOT .jpg) next to the episode file.
	// C# `:37`. Default true.
	EpisodeImages bool `json:"episodeImages"`
}

// ImplementationName is the C# class name of the WdtvMetadata
// provider — stored verbatim in the Metadata.Implementation column
// and used as the [metadata.Registry] lookup key.
const ImplementationName = "WdtvMetadata"

// ConfigContractName is the C# class name of the Settings type —
// stored verbatim in the Metadata.ConfigContract column and used as
// the [metadata.Registry] lookup key.
const ConfigContractName = "WdtvMetadataSettings"

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
// `WdtvSettingsValidator` at `WdtvMetadataSettings.cs:8-10`.
//
// The C# validator is empty. Returns nil failures always.
func (s *Settings) Validate() []metadata.TestFailure {
	return nil
}
