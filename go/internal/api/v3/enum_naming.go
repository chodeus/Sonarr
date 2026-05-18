package v3

import (
	"fmt"
	"strings"
	"unicode"

	"sonarr.tv/sonarr/internal/quality"
)

// CamelCaseEnumName mirrors `System.Text.Json.JsonNamingPolicy.CamelCase`
// applied to enum identifier strings. Used at the REST edge for fields
// whose Go-side getter returns the persisted PascalCase enum form
// ("Always", "All", "PreferAndUpgrade") but whose C# wire shape uses
// the camelCase form.
//
// Algorithm matches the .NET runtime implementation:
//   - empty input or non-uppercase first char → unchanged
//   - all-uppercase string → all-lowercase ("DVD" → "dvd")
//   - single leading uppercase → lowercase that char ("All" → "all",
//     "PreferAndUpgrade" → "preferAndUpgrade")
//   - multi-char uppercase run before a lowercase tail → lowercase
//     all-but-the-last of the leading run ("IPAddress" → "ipAddress",
//     "TVShow" → "tVShow"? — actually "tvShow", because i-1=1 lowercased
//     leaves the last cap as the word boundary)
//
// Wait the algorithm gives "TVShow" (T+V upper, S+how → lowercase
// "TV"→"tv", keep "Show"): result "tvShow". ✓
func CamelCaseEnumName(name string) string {
	if name == "" {
		return name
	}
	if !unicode.IsUpper(rune(name[0])) {
		return name
	}
	i := 0
	for i < len(name) && unicode.IsUpper(rune(name[i])) {
		i++
	}
	if i == len(name) {
		return strings.ToLower(name)
	}
	if i > 1 {
		return strings.ToLower(name[:i-1]) + name[i-1:]
	}
	return strings.ToLower(name[:1]) + name[1:]
}

// ProperDownloadTypeName maps the persisted quality.ProperDownloadType
// to its C# JsonStringEnumConverter+CamelCase wire form
// ("preferAndUpgrade" / "doNotUpgrade" / "doNotPrefer"). The Go
// quality.ProperDownloadType.String() returns the hyphen-cased form
// for log/internal use; this mapper covers the REST edge.
func ProperDownloadTypeName(p quality.ProperDownloadType) string {
	switch p {
	case quality.ProperPreferAndUpgrade:
		return "preferAndUpgrade"
	case quality.ProperDoNotUpgrade:
		return "doNotUpgrade"
	case quality.ProperDoNotPrefer:
		return "doNotPrefer"
	}
	return "unknown"
}

// ProperDownloadTypeFromName reverses ProperDownloadTypeName. Used by
// the /api/v3/config/mediamanagement PUT handler (slice 5.B13c).
// Unknown values surface as an error rather than defaulting silently
// — the wire shape is a closed enum and a bad value should reject.
func ProperDownloadTypeFromName(s string) (quality.ProperDownloadType, error) {
	switch s {
	case "preferAndUpgrade":
		return quality.ProperPreferAndUpgrade, nil
	case "doNotUpgrade":
		return quality.ProperDoNotUpgrade, nil
	case "doNotPrefer":
		return quality.ProperDoNotPrefer, nil
	}
	return 0, fmt.Errorf("unknown DownloadPropersAndRepacks value %q (want preferAndUpgrade|doNotUpgrade|doNotPrefer)", s)
}
