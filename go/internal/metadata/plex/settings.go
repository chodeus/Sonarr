// Package plex ports the C# `NzbDrone.Core.Extras.Metadata.Consumers
// .Plex.PlexMetadata` concrete metadata-writer. Plex consumes a
// single `.plexmatch` text manifest at the series root, NOT per-NFO
// XML sidecars. The format is line-oriented `Key: Value` pairs plus
// an optional list of `Episode: <fmt>: <relativePath>` entries used
// to hint Plex's scanner about episode-to-file mappings.
//
// Queue [W3] of Phase 4 focused-subset 6 (METADATA-WRITERS family).
// Slice 4.BF ships [W3] as a single slice — Plex is the smallest
// writer (~100 C# LOC + 30 settings LOC); no sub-arc split needed.
//
// All C# cites lead each type or function with a file:line reference.
package plex

import "sonarr.tv/sonarr/internal/metadata"

// Settings ports `PlexMetadataSettings` at
// `src/NzbDrone.Core/Extras/Metadata/Consumers/Plex/PlexMetadataSettings.cs:12-29`.
//
// Two non-nullable C# bools → Go bools per Option A (no omitempty).
// Defaults mirror the C# constructor at `:16-19`:
// SeriesPlexMatchFile=true, EpisodeMappings=false.
type Settings struct {
	// SeriesPlexMatchFile gates emission of the `.plexmatch` text
	// manifest at the series root. C# `:23`. Default true.
	SeriesPlexMatchFile bool `json:"seriesPlexMatchFile"`

	// EpisodeMappings extends the `.plexmatch` body with per-episode
	// `Episode: SXXEYY[-EYY]: <relativePath>` lines that hint Plex's
	// scanner about explicit episode-to-file mappings. C# `:26`.
	// Default false (the typical Plex setup auto-detects).
	EpisodeMappings bool `json:"episodeMappings"`
}

// ImplementationName is the C# class name of the PlexMetadata
// provider — stored verbatim in the Metadata.Implementation column
// and used as the [metadata.Registry] lookup key. Mirrors C#
// `Type.Name` == "PlexMetadata".
const ImplementationName = "PlexMetadata"

// ConfigContractName is the C# class name of the Settings type —
// stored verbatim in the Metadata.ConfigContract column and used as
// the [metadata.Registry] lookup key.
const ConfigContractName = "PlexMetadataSettings"

// DefaultSettings returns a Settings struct populated with the C#
// constructor defaults at `PlexMetadataSettings.cs:16-19`.
func DefaultSettings() Settings {
	return Settings{
		SeriesPlexMatchFile: true,
		EpisodeMappings:     false,
	}
}

// Validate runs the FluentValidation-equivalent rules from the C#
// `PlexMetadataSettingsValidator` at `PlexMetadataSettings.cs:8-10`.
//
// The C# validator is empty — PlexMetadataSettings carries only bool
// toggles with no per-field rules. Kept on the [Settings] type for
// API parity with notifications / importlists / xbmc; always returns
// nil failures.
func (s *Settings) Validate() []metadata.TestFailure {
	return nil
}
