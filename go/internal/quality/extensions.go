package quality

import "strings"

// extensionQualities ports `MediaFileExtensions._fileExtensions` from
// `src/NzbDrone.Core/MediaFiles/MediaFileExtensions.cs:11-72`.
//
// **Naming note**: the C# tree puts this in `NzbDrone.Core.MediaFiles`
// rather than `NzbDrone.Core.Qualities`. The Go port colocates here
// because `internal/quality.ParseQuality` is the only consumer and a
// separate `internal/mediafiles` package would create an import
// cycle (mediafiles would import quality for the Quality type;
// quality.ParseQuality needs mediafiles for the lookup). When the
// broader MediaFiles namespace lands in a later phase, this map can
// be moved out and re-exported via a small forwarding package.
//
// Lookup is case-insensitive on the extension key (matching the C#
// `StringComparer.OrdinalIgnoreCase` used in the source dictionary).
// The dictionary is keyed by extension WITH the leading dot
// (e.g. ".mkv", not "mkv").
//
// Changes to this map are persisted-state-affecting because the
// resolved Quality flows into the QualityModel that gets written to
// the EpisodeFiles row's Quality column. New extensions are safe to
// add; renaming existing ones would corrupt rows on next parse.
var extensionQualities = map[string]Quality{
	// Unknown
	".webm": Unknown,

	// SDTV — 35 entries.
	".m4v":    SDTV,
	".3gp":    SDTV,
	".nsv":    SDTV,
	".ty":     SDTV,
	".strm":   SDTV,
	".rm":     SDTV,
	".rmvb":   SDTV,
	".m3u":    SDTV,
	".ifo":    SDTV,
	".mov":    SDTV,
	".qt":     SDTV,
	".divx":   SDTV,
	".xvid":   SDTV,
	".bivx":   SDTV,
	".nrg":    SDTV,
	".pva":    SDTV,
	".wmv":    SDTV,
	".asf":    SDTV,
	".asx":    SDTV,
	".ogm":    SDTV,
	".ogv":    SDTV,
	".m2v":    SDTV,
	".avi":    SDTV,
	".bin":    SDTV,
	".dat":    SDTV,
	".dvr-ms": SDTV,
	".mpg":    SDTV,
	".mpeg":   SDTV,
	".mp4":    SDTV,
	".avc":    SDTV,
	".vp3":    SDTV,
	".svq3":   SDTV,
	".nuv":    SDTV,
	".viv":    SDTV,
	".dv":     SDTV,
	".fli":    SDTV,
	".flv":    SDTV,
	".wpl":    SDTV,

	// DVD — 3 entries.
	".img": DVD,
	".iso": DVD,
	".vob": DVD,

	// HD — 3 entries (mkv, ts, wtv treated as 720p HDTV by default).
	".mkv": HDTV720p,
	".ts":  HDTV720p,
	".wtv": HDTV720p,

	// Bluray — 1 entry.
	".m2ts": Bluray720p,
}

// GetQualityForExtension looks up the canonical Quality associated
// with a file extension. Mirrors the C# `MediaFileExtensions.
// GetQualityForExtension` at `MediaFileExtensions.cs:76-84`.
//
// Lookup is case-insensitive on the extension. Empty extension or
// unknown extension returns `Unknown`. The C# original throws
// `ArgumentException` from `Path.GetInvalidPathChars` if `extension`
// is null or contains invalid path chars; the Go port returns
// `Unknown` for the empty case (matching the dictionary-miss path)
// and the caller is expected to validate path-char shape upstream.
func GetQualityForExtension(extension string) Quality {
	if extension == "" {
		return Unknown
	}
	q, ok := extensionQualities[strings.ToLower(extension)]
	if !ok {
		return Unknown
	}
	return q
}
