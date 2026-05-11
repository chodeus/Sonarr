package importlists

import "strconv"

// ListType mirrors the C# `NzbDrone.Core.ImportLists.ImportListType`
// enum at `src/NzbDrone.Core/ImportLists/ImportListType.cs:3-11`.
// Integer values are frozen because they are persisted in the v3 API
// "schema" endpoint output and the v3 React UI keys per-type icons
// off them. Mirrors C# enum ordinals 1:1 — DO NOT renumber.
//
// The C# enum has no explicit values; it relies on C# default-enum
// ordering (Program=0, Plex=1, ...). The Go port pins these
// explicitly to make the wire-format coupling visible.
//
// Slice 4.AK (sub-arc I2a of queue [I2]) — foundation slice.
type ListType int

const (
	// ListTypeProgram is the C# Program (=0) — application-style
	// imports (Sonarr-peer, etc.).
	ListTypeProgram ListType = 0

	// ListTypePlex is the C# Plex (=1) — Plex watchlist family
	// (PlexImport + PlexRssImport).
	ListTypePlex ListType = 1

	// ListTypeTrakt is the C# Trakt (=2) — Trakt list family
	// (TraktUserImport + TraktPopularImport + TraktListImport).
	ListTypeTrakt ListType = 2

	// ListTypeSimkl is the C# Simkl (=3) — Simkl user list.
	ListTypeSimkl ListType = 3

	// ListTypeOther is the C# Other (=4) — anime-focused lists
	// (AniList + MyAnimeList).
	ListTypeOther ListType = 4

	// ListTypeAdvanced is the C# Advanced (=5) — the catch-all for
	// the CustomImport HTTP-poke endpoint.
	ListTypeAdvanced ListType = 5
)

// String returns the C# enum name for the given ListType.
// Unknown values render as "ListType(<n>)" — the C# default
// Enum.ToString() behaviour ports as "n" alone; the Go port wraps
// to make the unknown obvious in log lines and error strings.
func (t ListType) String() string {
	switch t {
	case ListTypeProgram:
		return "Program"
	case ListTypePlex:
		return "Plex"
	case ListTypeTrakt:
		return "Trakt"
	case ListTypeSimkl:
		return "Simkl"
	case ListTypeOther:
		return "Other"
	case ListTypeAdvanced:
		return "Advanced"
	default:
		return "ListType(" + strconv.Itoa(int(t)) + ")"
	}
}
