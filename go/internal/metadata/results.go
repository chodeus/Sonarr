package metadata

// MetadataFileResult is the value the metadata-writer producers return
// from [Provider.SeriesMetadata] / [Provider.EpisodeMetadata]. Mirrors
// C# `MetadataFileResult` at
// `src/NzbDrone.Core/Extras/Metadata/Files/MetadataFileResult.cs:3-13`.
//
// RelativePath is rooted at the series folder (NOT the application
// data folder) — every writer concrete builds it via
// `Path.Combine(seriesFolder, ...)`-equivalent semantics. Contents is
// the full text to write — a complete XML/NFO body; the writer does
// NOT stream-emit.
//
// Slice 4.BA (queue [W1] — METADATA-WRITERS foundation): paired with
// [ImageFileResult] for the per-section dispatch in the consumer-
// facing service (queue [W7]).
type MetadataFileResult struct {
	// RelativePath is the per-file path relative to the series
	// folder. Examples: "tvshow.nfo", "Season 1/S01E02.nfo".
	RelativePath string

	// Contents is the entire file body to write. The consumer-facing
	// service is responsible for writing this to disk and recording
	// the resulting row in internal/metadatafiles.
	Contents string
}

// NewMetadataFileResult is the value-type constructor mirroring the C#
// 2-arg constructor at `MetadataFileResult.cs:8-12`. Provided for
// readability at call sites; equivalent to a struct literal.
func NewMetadataFileResult(relativePath, contents string) MetadataFileResult {
	return MetadataFileResult{RelativePath: relativePath, Contents: contents}
}

// ImageFileResult is the value the metadata-writer producers return
// from [Provider.SeriesImages] / [Provider.SeasonImages] /
// [Provider.EpisodeImages]. Mirrors C# `ImageFileResult` at
// `src/NzbDrone.Core/Extras/Metadata/Files/ImageFileResult.cs:3-13`.
//
// RelativePath is rooted at the series folder. URL is the absolute
// remote URL the consumer-facing service downloads from before
// writing to disk — writers themselves do NOT perform HTTP I/O.
//
// Slice 4.BA (queue [W1] — METADATA-WRITERS foundation): paired with
// [MetadataFileResult] for the per-section dispatch in the consumer-
// facing service.
type ImageFileResult struct {
	// RelativePath is the per-image path relative to the series
	// folder. Examples: "poster.jpg", "Season 1/banner.jpg".
	RelativePath string

	// URL is the remote source the consumer-facing service downloads
	// the image bytes from. Writers populate this from
	// `series.Images` / `season.Images` (per the SkyHook resource
	// shape — see internal/metadatasource).
	URL string
}

// NewImageFileResult is the value-type constructor mirroring the C#
// 2-arg constructor at `ImageFileResult.cs:8-12`. Provided for
// readability at call sites; equivalent to a struct literal.
func NewImageFileResult(relativePath, url string) ImageFileResult {
	return ImageFileResult{RelativePath: relativePath, URL: url}
}
