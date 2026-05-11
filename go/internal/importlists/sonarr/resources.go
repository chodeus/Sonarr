package sonarr

// API resource types ported from
// `src/NzbDrone.Core/ImportLists/Sonarr/SonarrAPIResource.cs:5-45`.
// These mirror the JSON shape the remote /api/v3/* endpoints return;
// only the fields the C# code reads are populated. The Go port keeps
// every C# field for symmetry — fields the Go Fetch path doesn't
// consume (Overview / SortTitle / TitleSlug / Monitored / Images)
// are included so a future feature can lift them without re-shaping
// the resource.

// Series mirrors C# `SonarrSeries` at
// `SonarrAPIResource.cs:5-20`. JSON keys are lowerCamelCase — the
// remote /api/v3/series response that Sonarr emits.
type Series struct {
	Title             string   `json:"title"`
	SortTitle         string   `json:"sortTitle"`
	TvdbID            int      `json:"tvdbId"`
	Overview          string   `json:"overview"`
	Monitored         bool     `json:"monitored"`
	Year              int      `json:"year"`
	TitleSlug         string   `json:"titleSlug"`
	QualityProfileID  int      `json:"qualityProfileId"`
	LanguageProfileID int      `json:"languageProfileId"`
	RootFolderPath    string   `json:"rootFolderPath"`
	Seasons           []Season `json:"seasons"`
	Tags              []int    `json:"tags"`
	// Images is intentionally left as []map[string]any rather than a
	// typed struct — the C# `MediaCover.MediaCover` type isn't
	// consumed by the import-list Fetch path. The Go port carries
	// it as opaque JSON for future use.
	Images []map[string]any `json:"images,omitempty"`
}

// Profile mirrors C# `SonarrProfile` at `SonarrAPIResource.cs:22-26`.
// Used by both GetQualityProfiles and GetLanguageProfiles (the C#
// proxy reuses the same type for both — the JSON shape is identical).
type Profile struct {
	Name string `json:"name"`
	ID   int    `json:"id"`
}

// Tag mirrors C# `SonarrTag` at `SonarrAPIResource.cs:28-32`.
type Tag struct {
	Label string `json:"label"`
	ID    int    `json:"id"`
}

// RootFolder mirrors C# `SonarrRootFolder` at
// `SonarrAPIResource.cs:34-38`.
type RootFolder struct {
	Path string `json:"path"`
	ID   int    `json:"id"`
}

// Season mirrors C# `SonarrSeason` at `SonarrAPIResource.cs:40-44`.
type Season struct {
	SeasonNumber int  `json:"seasonNumber"`
	Monitored    bool `json:"monitored"`
}
