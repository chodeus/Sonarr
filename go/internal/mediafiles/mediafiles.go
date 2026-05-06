// Package mediafiles is the Go port of `NzbDrone.Core.MediaFiles`'s
// scan-orchestration surface — `DiskScanService` and the helpers it
// builds on (`MediaFileExtensions`, the four exclusion regexes, the
// `FilterPaths` predicate). Stand-alone primitives ship first; the
// `Service` struct + `Scan(series)` happy path land in subsequent
// slices once the disk-provider and import-pipeline narrow interfaces
// are wired.
//
// Persisted media-file rows live in sibling packages — `episodefiles`
// owns the `EpisodeFiles` table, `extrafiles` / `subtitlefiles` /
// `metadatafiles` own the catch-all extras tables. This package
// contains no schema; it is a service-layer port only.
package mediafiles

import (
	"strings"

	"sonarr.tv/sonarr/internal/quality"
)

// fileExtensions is the master registry of known media-file
// extensions, keyed by lowercase extension (with leading dot) and
// valued by the [quality.Quality] each extension implies in the
// absence of better signal (filename or MediaInfo). Mirrors the
// `_fileExtensions` static dictionary at
// `src/NzbDrone.Core/MediaFiles/MediaFileExtensions.cs:13-71`.
//
// Stored lowercase here; lookups must lowercase their input via the
// [QualityForExtension] / [IsMediaExtension] helpers. The C# tree uses
// `StringComparer.OrdinalIgnoreCase` on the dictionary directly; the
// Go port lifts the case-folding into the helpers so the underlying
// map can stay a plain `map[string]quality.Quality`.
var fileExtensions = map[string]quality.Quality{
	// Unknown
	".webm": quality.Unknown,

	// SDTV
	".m4v":    quality.SDTV,
	".3gp":    quality.SDTV,
	".nsv":    quality.SDTV,
	".ty":     quality.SDTV,
	".strm":   quality.SDTV,
	".rm":     quality.SDTV,
	".rmvb":   quality.SDTV,
	".m3u":    quality.SDTV,
	".ifo":    quality.SDTV,
	".mov":    quality.SDTV,
	".qt":     quality.SDTV,
	".divx":   quality.SDTV,
	".xvid":   quality.SDTV,
	".bivx":   quality.SDTV,
	".nrg":    quality.SDTV,
	".pva":    quality.SDTV,
	".wmv":    quality.SDTV,
	".asf":    quality.SDTV,
	".asx":    quality.SDTV,
	".ogm":    quality.SDTV,
	".ogv":    quality.SDTV,
	".m2v":    quality.SDTV,
	".avi":    quality.SDTV,
	".bin":    quality.SDTV,
	".dat":    quality.SDTV,
	".dvr-ms": quality.SDTV,
	".mpg":    quality.SDTV,
	".mpeg":   quality.SDTV,
	".mp4":    quality.SDTV,
	".avc":    quality.SDTV,
	".vp3":    quality.SDTV,
	".svq3":   quality.SDTV,
	".nuv":    quality.SDTV,
	".viv":    quality.SDTV,
	".dv":     quality.SDTV,
	".fli":    quality.SDTV,
	".flv":    quality.SDTV,
	".wpl":    quality.SDTV,

	// DVD
	".img": quality.DVD,
	".iso": quality.DVD,
	".vob": quality.DVD,

	// HD
	".mkv": quality.HDTV720p,
	".ts":  quality.HDTV720p,
	".wtv": quality.HDTV720p,

	// Bluray
	".m2ts": quality.Bluray720p,
}

// diskExtensions ports the C# `DiskExtensions` `HashSet` at
// `MediaFileExtensions.cs:74` — the subset of extensions that name a
// disk image rather than a flat video file. Lowercase keys; lookups
// case-fold via [IsDiskExtension].
var diskExtensions = map[string]struct{}{
	".img": {},
	".iso": {},
	".vob": {},
}

// IsMediaExtension reports whether `ext` (e.g. ".mkv", ".MP4") names
// a known media-file extension. Case-insensitive — the registry is
// stored lowercase and the input is lowered at the call site, matching
// the C# `StringComparer.OrdinalIgnoreCase` semantic.
//
// Empty input returns false.
func IsMediaExtension(ext string) bool {
	if ext == "" {
		return false
	}
	_, ok := fileExtensions[strings.ToLower(ext)]
	return ok
}

// IsDiskExtension reports whether `ext` names a disk-image extension
// (`.img`, `.iso`, `.vob`). Case-insensitive. Empty input returns
// false. Mirrors `MediaFileExtensions.DiskExtensions.Contains` at the
// C# site.
func IsDiskExtension(ext string) bool {
	if ext == "" {
		return false
	}
	_, ok := diskExtensions[strings.ToLower(ext)]
	return ok
}

// QualityForExtension returns the [quality.Quality] associated with
// `ext` (e.g. ".mkv" → Quality.HDTV720p), or [quality.Unknown] when
// `ext` is not registered. Case-insensitive. Mirrors
// `MediaFileExtensions.GetQualityForExtension` at
// `MediaFileExtensions.cs:76-84`.
func QualityForExtension(ext string) quality.Quality {
	if ext == "" {
		return quality.Unknown
	}
	if q, ok := fileExtensions[strings.ToLower(ext)]; ok {
		return q
	}
	return quality.Unknown
}
