package parser

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
