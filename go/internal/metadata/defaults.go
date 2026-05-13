package metadata

import (
	"context"
	"fmt"
)

// DefaultDefinition declares one metadata-provider row the seed pass
// inserts on a fresh DB. Mirrors the C# `MetadataFactory
// .InitializeProviders` loop at `src/NzbDrone.Core/Extras/Metadata/
// MetadataFactory.cs:25-48` — each registered IMetadata implementation
// becomes a Metadata row with Enable=false + the Settings struct's
// constructor defaults serialised to JSON.
//
// Insert order is the C# DI assembly-scan order which on the
// recording host produced IDs Xbmc=1, Wdtv=2, Roksbox=3, Plex=4. The
// Go port pins that order here so the wire-shape IDs match the
// recording.
type DefaultDefinition struct {
	Name           string
	Implementation string
	ConfigContract string
	Settings       string
}

// defaultDefinitions is the canonical 4-entry table in the order the
// seed pass inserts them. The Settings JSON is hand-crafted to match
// the C# IProviderConfig constructor defaults — every bool toggle
// the constructor sets to `true` lands here as `true`, everything
// else as the json.Unmarshal zero-value (false for bools).
//
// Name = ImplementationName per the C# `Name = provider.Name`
// assignment in MetadataFactory.InitializeProviders.
var defaultDefinitions = []DefaultDefinition{
	{
		// Insertion #1 → ID 1.
		Name:           "Kodi (XBMC) / Emby",
		Implementation: "XbmcMetadata",
		ConfigContract: "XbmcMetadataSettings",
		Settings:       `{"seriesMetadata":true,"seriesMetadataEpisodeGuide":false,"seriesMetadataUrl":false,"episodeMetadata":true,"episodeImageThumb":false,"seriesImages":true,"seasonImages":true,"episodeImages":true}`,
	},
	{
		// Insertion #2 → ID 2.
		Name:           "WDTV",
		Implementation: "WdtvMetadata",
		ConfigContract: "WdtvMetadataSettings",
		Settings:       `{"episodeMetadata":true,"seriesImages":true,"seasonImages":true,"episodeImages":true}`,
	},
	{
		// Insertion #3 → ID 3.
		Name:           "Roksbox",
		Implementation: "RoksboxMetadata",
		ConfigContract: "RoksboxMetadataSettings",
		Settings:       `{"episodeMetadata":true,"seriesImages":true,"seasonImages":true,"episodeImages":true}`,
	},
	{
		// Insertion #4 → ID 4.
		Name:           "Plex",
		Implementation: "PlexMetadata",
		ConfigContract: "PlexMetadataSettings",
		Settings:       `{"seriesPlexMatchFile":true,"episodeMappings":false}`,
	},
}

// SeedDefaults inserts every default metadata-provider row that's
// not already present (matched by Implementation column). Mirrors the
// C# `MetadataFactory.InitializeProviders` no-op-when-present semantic
// — re-running is safe.
//
// Returns nil when every default is already present (the steady-state
// path for a non-fresh DB).
func (r *Repo) SeedDefaults(ctx context.Context) error {
	existing, err := r.All(ctx)
	if err != nil {
		return fmt.Errorf("metadata: SeedDefaults: list: %w", err)
	}
	present := make(map[string]bool, len(existing))
	for _, m := range existing {
		present[m.Implementation] = true
	}
	for _, def := range defaultDefinitions {
		if present[def.Implementation] {
			continue
		}
		row := &Metadata{
			Name:           def.Name,
			Implementation: def.Implementation,
			ConfigContract: def.ConfigContract,
			Settings:       def.Settings,
			Enable:         false,
		}
		if _, err := r.Insert(ctx, row); err != nil {
			return fmt.Errorf("metadata: SeedDefaults: insert %q: %w", def.Name, err)
		}
	}
	return nil
}
