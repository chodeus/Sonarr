package metadata

import "strconv"

// MetadataType mirrors the C# enum at
// `src/NzbDrone.Core/Extras/Metadata/MetadataType.cs:3-11`. Integer
// values are frozen because they are persisted in the `MetadataFiles.Type`
// column (see internal/metadatafiles and analysis/schema/{sqlite,postgres}.sql
// at v230) AND surfaced through the v3 API. Per Sonarr v3 wire shape
// (Option A — go/JSON_CONVENTIONS.md), the integer value is the
// canonical on-wire form for this enum: the C# `MetadataType` is not
// decorated with `[JsonConverter(typeof(StringEnumConverter))]`, so
// .NET emits the integer.
//
// DO NOT renumber. Existing rows reference these ordinals.
//
// Slice 4.BA (queue [W1] — METADATA-WRITERS foundation): paired with
// [SectionMetadata] / [SectionImage] for the per-type dispatch the
// consumer-facing service performs.
type MetadataType int

const (
	// MetadataTypeUnknown is the C# Unknown (=0) sentinel. New rows
	// should never persist Unknown — it is the zero value used when
	// reading an unrecognised int from the database during a
	// downgrade scenario.
	MetadataTypeUnknown MetadataType = 0

	// MetadataTypeSeriesMetadata is the C# SeriesMetadata (=1) — the
	// series-level metadata sidecar (e.g. Kodi `tvshow.nfo`).
	MetadataTypeSeriesMetadata MetadataType = 1

	// MetadataTypeEpisodeMetadata is the C# EpisodeMetadata (=2) —
	// the per-episode metadata sidecar (e.g. Kodi `<episode>.nfo`).
	MetadataTypeEpisodeMetadata MetadataType = 2

	// MetadataTypeSeriesImage is the C# SeriesImage (=3) — the
	// series-level artwork (poster/fanart/banner) sidecar.
	MetadataTypeSeriesImage MetadataType = 3

	// MetadataTypeSeasonImage is the C# SeasonImage (=4) — the
	// per-season artwork (poster/banner) sidecar.
	MetadataTypeSeasonImage MetadataType = 4

	// MetadataTypeEpisodeImage is the C# EpisodeImage (=5) — the
	// per-episode thumbnail sidecar.
	MetadataTypeEpisodeImage MetadataType = 5
)

// String returns the C# enum name. Unknown ordinals render as
// "MetadataType(<n>)" to keep log lines unambiguous; the C# default
// `Enum.ToString()` would emit the integer, but the Go port wraps so
// the unknown is obvious in tracebacks.
func (t MetadataType) String() string {
	switch t {
	case MetadataTypeUnknown:
		return "Unknown"
	case MetadataTypeSeriesMetadata:
		return "SeriesMetadata"
	case MetadataTypeEpisodeMetadata:
		return "EpisodeMetadata"
	case MetadataTypeSeriesImage:
		return "SeriesImage"
	case MetadataTypeSeasonImage:
		return "SeasonImage"
	case MetadataTypeEpisodeImage:
		return "EpisodeImage"
	default:
		return "MetadataType(" + strconv.Itoa(int(t)) + ")"
	}
}

// Section names mirror the C# `MetadataSectionType` static class at
// `src/NzbDrone.Core/Extras/Metadata/MetadataSectionType.cs:3-7`.
// They classify a [MetadataType] into the broad "metadata" vs "image"
// buckets the consumer-facing service uses for per-section dispatch
// (the C# `MetadataService` keys event handlers off these literals).
//
// Kept as `const string` rather than a typed enum to mirror the C#
// `public const string` shape — call sites compare against these
// literals directly.
const (
	// SectionMetadata is the C# `MetadataSectionType.Metadata`
	// literal — applied to [MetadataTypeSeriesMetadata] and
	// [MetadataTypeEpisodeMetadata].
	SectionMetadata = "metadata"

	// SectionImage is the C# `MetadataSectionType.Image` literal —
	// applied to [MetadataTypeSeriesImage] / [MetadataTypeSeasonImage]
	// / [MetadataTypeEpisodeImage].
	SectionImage = "image"
)

// Section reports the [SectionMetadata] vs [SectionImage] bucket the
// given [MetadataType] falls into. Returns "" for
// [MetadataTypeUnknown] so the caller can detect the sentinel.
//
// The mapping mirrors the per-type dispatch in
// `MetadataService.ProcessSeriesMetadata` and related methods at
// `src/NzbDrone.Core/Extras/Metadata/MetadataService.cs` (the C# code
// branches on the type for the section-equivalent handler).
func (t MetadataType) Section() string {
	switch t {
	case MetadataTypeSeriesMetadata, MetadataTypeEpisodeMetadata:
		return SectionMetadata
	case MetadataTypeSeriesImage, MetadataTypeSeasonImage, MetadataTypeEpisodeImage:
		return SectionImage
	default:
		return ""
	}
}
