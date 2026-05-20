package parser

import (
	"strconv"
	"strings"
)

// IndexerFlags ports the C# `[Flags] enum IndexerFlags` at
// `src/NzbDrone.Core/Parser/Model/IndexerFlags.cs`. Persisted as
// an integer bitmask in indexer-output JSON; integer values are
// frozen.
//
// Use with bitwise ops:
//
//	flags |= parser.IndexerFlagFreeleech
//	if flags&parser.IndexerFlagInternal != 0 { ... }
type IndexerFlags int

const (
	// IndexerFlagFreeleech — torrent download amount does not count.
	IndexerFlagFreeleech IndexerFlags = 1
	// IndexerFlagHalfleech — torrent download amount only counts 50%.
	IndexerFlagHalfleech IndexerFlags = 2
	// IndexerFlagDoubleUpload — torrent upload amount is doubled.
	IndexerFlagDoubleUpload IndexerFlags = 4
	// IndexerFlagInternal — uploader is an internal release group.
	IndexerFlagInternal IndexerFlags = 8
	// IndexerFlagScene — release comes from a scene group.
	IndexerFlagScene IndexerFlags = 16
	// IndexerFlagFreeleech75 — torrent download amount only counts 75%.
	IndexerFlagFreeleech75 IndexerFlags = 32
	// IndexerFlagFreeleech25 — torrent download amount only counts 25%.
	IndexerFlagFreeleech25 IndexerFlags = 64
	// IndexerFlagNuked — release is nuked.
	IndexerFlagNuked IndexerFlags = 128
	// IndexerFlagSubtitles — release contains subtitles.
	IndexerFlagSubtitles IndexerFlags = 256
)

// HasFlag reports whether all bits in `flag` are set in `f`.
// Mirrors C#'s `Enum.HasFlag(...)` for `[Flags]` enums.
func (f IndexerFlags) HasFlag(flag IndexerFlags) bool {
	return f&flag == flag
}

// indexerFlagsDescending lists the named bits in descending integer
// value — matches the order .NET's `[Flags] enum.ToString()` uses when
// composing the comma-separated combination name (the runtime walks
// declared values high-to-low, consuming each subset and clearing its
// bits).
var indexerFlagsDescending = []struct {
	value IndexerFlags
	name  string
}{
	{IndexerFlagSubtitles, "Subtitles"},
	{IndexerFlagNuked, "Nuked"},
	{IndexerFlagFreeleech25, "Freeleech25"},
	{IndexerFlagFreeleech75, "Freeleech75"},
	{IndexerFlagScene, "Scene"},
	{IndexerFlagInternal, "Internal"},
	{IndexerFlagDoubleUpload, "DoubleUpload"},
	{IndexerFlagHalfleech, "Halfleech"},
	{IndexerFlagFreeleech, "Freeleech"},
}

// String mirrors C# `[Flags] enum.ToString()` for [IndexerFlags] —
// emits a comma-separated list of named bits in descending-value order
// (e.g. value 9 → `"Internal, Freeleech"`, value 1 → `"Freeleech"`).
// Zero returns `"0"` (the enum declares no `None = 0` entry, matching
// the C# fallback). When bits remain unaccounted-for after consuming
// every named value, falls through to the integer string (the exact
// .NET behaviour for unnamed bits in a `[Flags]` value). Wire-shape
// parity with C# `HistoryService.cs:179,232,296,329` which call
// `IndexerFlags.ToString()` when packing history.Data payloads.
//
// Slice 5.J160 sweep — replaces the previous int-string emit
// (`strconv.Itoa(int(f))`) across all 4 history handlers that emit
// IndexerFlags. [ParseIndexerFlags] already accepts both the numeric
// and comma-separated forms case-insensitively, so consumers
// round-tripping through it stay compatible.
func (f IndexerFlags) String() string {
	if f == 0 {
		return "0"
	}
	var parts []string
	remaining := f
	for _, e := range indexerFlagsDescending {
		if remaining&e.value == e.value {
			parts = append(parts, e.name)
			remaining &^= e.value
		}
	}
	if remaining != 0 {
		// Unknown bits left — .NET falls back to the integer string
		// rather than mixing names with an unaccounted-for residue.
		return strconv.Itoa(int(f))
	}
	return strings.Join(parts, ", ")
}
