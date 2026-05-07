package parser

import (
	"strconv"
	"strings"
)

// indexerFlagsNameToFlag maps the C# `Enum.ToString()` identifier
// for each [IndexerFlags] bit to its int value. Lower-cased keys
// because the C# `Enum.TryParse(s, ignoreCase: true, out flags)`
// invocation at `ImportApprovedEpisodes.cs:96-102` is case-
// insensitive.
//
// `[Flags]` enum semantics: a value with multiple bits set
// renders to a comma-separated list (`"Freeleech, Internal"`).
// [ParseIndexerFlags] handles both the single-name and
// comma-separated forms.
var indexerFlagsNameToFlag = map[string]IndexerFlags{
	"freeleech":    IndexerFlagFreeleech,
	"halfleech":    IndexerFlagHalfleech,
	"doubleupload": IndexerFlagDoubleUpload,
	"internal":     IndexerFlagInternal,
	"scene":        IndexerFlagScene,
	"freeleech75":  IndexerFlagFreeleech75,
	"freeleech25":  IndexerFlagFreeleech25,
	"nuked":        IndexerFlagNuked,
	"subtitles":    IndexerFlagSubtitles,
}

// ParseIndexerFlags converts a string into an [IndexerFlags] value.
// Mirrors the C# `Enum.TryParse(s, ignoreCase: true, out IndexerFlags)`
// invocation at `ImportApprovedEpisodes.cs:96`.
//
// Three accepted forms:
//   - Numeric integer (`"9"` → `9`). The C# `Enum.TryParse` accepts
//     this directly; the Go port matches.
//   - Single name (`"Freeleech"` → `1`). Case-insensitive.
//   - Comma-separated names (`"Freeleech, Internal"` → `9`). The
//     C# `[Flags]` round-trip produces this form for any value with
//     multiple bits set; spaces around commas are tolerated.
//
// Empty / unrecognised inputs return zero — the C# failure-arm
// behaviour where the `out` var stays at the enum's default
// (`IndexerFlags.None` == 0).
//
// Used by the EpisodeImport history-driven enrichment branch in
// slice 3.71 (replaces the numeric-only parseIntStr fallback).
func ParseIndexerFlags(s string) IndexerFlags {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	// Try numeric form first (the most common payload — the
	// EpisodeGrabbedEvent handler at `HistoryService.cs` writes the
	// integer string when constructing the history.Data payload).
	if n, err := strconv.Atoi(s); err == nil {
		return IndexerFlags(n)
	}
	// Comma-separated names — split + accumulate.
	var out IndexerFlags
	for _, part := range strings.Split(s, ",") {
		key := strings.ToLower(strings.TrimSpace(part))
		if key == "" {
			continue
		}
		if v, ok := indexerFlagsNameToFlag[key]; ok {
			out |= v
		}
		// Unknown names are silently dropped — matches C#
		// `Enum.TryParse` failure arm where the bit is just not set.
	}
	return out
}

// releaseTypeNameToValue maps the C# `Enum.ToString()` identifier
// for each [ReleaseType] value to its int. Lower-cased keys per the
// case-insensitive C# parse at `ImportApprovedEpisodes.cs:102`.
//
// Unlike [IndexerFlags], ReleaseType is NOT a `[Flags]` enum — only
// single-name and integer forms are accepted.
var releaseTypeNameToValue = map[string]ReleaseType{
	"unknown":       ReleaseTypeUnknown,
	"singleepisode": ReleaseTypeSingleEpisode,
	"multiepisode":  ReleaseTypeMultiEpisode,
	"seasonpack":    ReleaseTypeSeasonPack,
}

// ParseReleaseType converts a string into a [ReleaseType] value.
// Mirrors `Enum.TryParse(s, ignoreCase: true, out ReleaseType)` at
// `ImportApprovedEpisodes.cs:102`.
//
// Two accepted forms:
//   - Numeric integer (`"3"` → `ReleaseTypeSeasonPack`).
//   - Single name (`"SeasonPack"` → `3`). Case-insensitive.
//
// Empty / unrecognised inputs return [ReleaseTypeUnknown] — matches
// the C# failure-arm where the `out` var stays at the enum's default.
func ParseReleaseType(s string) ReleaseType {
	s = strings.TrimSpace(s)
	if s == "" {
		return ReleaseTypeUnknown
	}
	if n, err := strconv.Atoi(s); err == nil {
		return ReleaseType(n)
	}
	if v, ok := releaseTypeNameToValue[strings.ToLower(s)]; ok {
		return v
	}
	return ReleaseTypeUnknown
}
