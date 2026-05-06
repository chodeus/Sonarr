package quality

import "strings"

// ProperDownloadType mirrors the C# enum at
// `src/NzbDrone.Core/Qualities/ProperDownloadTypes.cs:1-9`. Captures
// the user's preference for proper / repack revisions of an existing
// release. Persisted as the integer value of the
// `DownloadPropersAndRepacks` config row; order is wire-stable.
//
// The three modes:
//
//   - PreferAndUpgrade — propers/repacks UPGRADE the existing file.
//     A proper of the same quality replaces the non-proper. Default.
//   - DoNotUpgrade — propers/repacks are DOWNLOADED (the proper might
//     be picked over a non-proper of the same quality at grab time)
//     but DO NOT trigger an upgrade of an already-imported file.
//   - DoNotPrefer — propers/repacks have no special treatment;
//     revision number is ignored except via custom-format / preferred-
//     word scoring.
type ProperDownloadType int

// ProperDownloadType enumeration. Order matches C# `:5-7`.
const (
	// ProperPreferAndUpgrade is the default. Propers upgrade.
	ProperPreferAndUpgrade ProperDownloadType = 0
	// ProperDoNotUpgrade — propers are grabbed but don't trigger upgrade.
	ProperDoNotUpgrade ProperDownloadType = 1
	// ProperDoNotPrefer — revision number is ignored except via
	// preferred-word scoring.
	ProperDoNotPrefer ProperDownloadType = 2
)

// String returns the lower-case name used in log lines + the v3 JSON
// payload. The C# JSON converter emits PascalCase tokens; the Go port
// matches the project convention of lower-case enum strings.
func (p ProperDownloadType) String() string {
	switch p {
	case ProperPreferAndUpgrade:
		return "prefer-and-upgrade"
	case ProperDoNotUpgrade:
		return "do-not-upgrade"
	case ProperDoNotPrefer:
		return "do-not-prefer"
	}
	return "unknown"
}

// ParseProperDownloadType resolves a persisted enum identifier
// (case-insensitive) back to the typed value. Recognises both the C#
// `Enum.ToString()` form (`"PreferAndUpgrade"`, `"DoNotUpgrade"`,
// `"DoNotPrefer"`) used by `Configuration/ConfigService.cs:130-136` AND
// the kebab-case form used by [ProperDownloadType.String]. Numeric
// strings (`"0"`, `"1"`, `"2"`) round-trip the ordinal.
//
// Empty / unrecognised input returns [ProperPreferAndUpgrade] —
// matches C# `GetValueEnum(..., PreferAndUpgrade)` default.
func ParseProperDownloadType(s string) ProperDownloadType {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "preferandupgrade", "prefer-and-upgrade", "0":
		return ProperPreferAndUpgrade
	case "donotupgrade", "do-not-upgrade", "1":
		return ProperDoNotUpgrade
	case "donotprefer", "do-not-prefer", "2":
		return ProperDoNotPrefer
	default:
		return ProperPreferAndUpgrade
	}
}
