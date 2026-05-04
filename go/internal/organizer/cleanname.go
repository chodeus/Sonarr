package organizer

import (
	"regexp"
	"strings"
)

// ColonReplacementFormat enumerates the strategies the C# tree exposes
// for substituting colons in filenames. Persisted as integer Ids in
// `NamingConfig.ColonReplacementFormat`. Order matches the C# enum at
// `FileNameBuilder.cs:1269-1277` so numeric values are stable across
// the port boundary.
type ColonReplacementFormat int

// Colon-replacement constants. Match the C# `ColonReplacementFormat`
// enum values exactly.
const (
	ColonDelete         ColonReplacementFormat = 0
	ColonDash           ColonReplacementFormat = 1
	ColonSpaceDash      ColonReplacementFormat = 2
	ColonSpaceDashSpace ColonReplacementFormat = 3
	ColonSmart          ColonReplacementFormat = 4
	ColonCustom         ColonReplacementFormat = 5
)

// MultiEpisodeStyle enumerates the strategies for formatting multi-episode
// filenames. Mirrors the C# enum at `FileNameBuilder.cs:1259-1267`.
type MultiEpisodeStyle int

// Multi-episode style constants.
const (
	MultiEpisodeExtend        MultiEpisodeStyle = 0
	MultiEpisodeDuplicate     MultiEpisodeStyle = 1
	MultiEpisodeRepeat        MultiEpisodeStyle = 2
	MultiEpisodeScene         MultiEpisodeStyle = 3
	MultiEpisodeRange         MultiEpisodeStyle = 4
	MultiEpisodePrefixedRange MultiEpisodeStyle = 5
)

// CleanNameOptions encapsulates the [CleanFileName]-affecting fields of
// `NamingConfig`. Pulled out as a value-type so callers don't have to
// drag in the full namingconfig.Config (and the package import that
// would create a cycle once organizer is wired into the host).
type CleanNameOptions struct {
	ReplaceIllegalCharacters     bool
	ColonReplacementFormat       ColonReplacementFormat
	CustomColonReplacementFormat string
}

// DefaultCleanNameOptions returns the C# `NamingConfig.Default`
// shape — `ReplaceIllegalCharacters=true, ColonReplacementFormat=Smart,
// CustomColonReplacementFormat=""`.
func DefaultCleanNameOptions() CleanNameOptions {
	return CleanNameOptions{
		ReplaceIllegalCharacters:     true,
		ColonReplacementFormat:       ColonSmart,
		CustomColonReplacementFormat: "",
	}
}

// badCharacters mirrors `FileNameBuilder.BadCharacters` at `:114`.
// Order is significant — pairs with goodCharacters by index.
var badCharacters = []string{`\`, `/`, `<`, `>`, `?`, `*`, `|`, `"`}

// goodCharacters mirrors `FileNameBuilder.GoodCharacters` at `:115`.
// Empty entries mean "delete with no replacement". The replacement is
// ONLY applied when `CleanNameOptions.ReplaceIllegalCharacters=true`;
// otherwise BadCharacters are deleted regardless of the slot.
var goodCharacters = []string{"+", "+", "", "", "!", "-", "", ""}

// runCollapseRegexes is the per-character expansion of the C# regex
// `([- ._])(\1)+` at `:71` — RE2 doesn't support backreferences, so
// the run-collapse is split into one regex per separator character.
// Order doesn't matter; each pass collapses runs of its own char to
// a single instance.
var runCollapseRegexes = []*regexp.Regexp{
	regexp.MustCompile(`-{2,}`),
	regexp.MustCompile(` {2,}`),
	regexp.MustCompile(`\.{2,}`),
	regexp.MustCompile(`_{2,}`),
}

// CleanFileName applies the C# `CleanFileName(name, namingConfig)`
// transform from `:1159-1205`. Order:
//
//  1. Colon handling: if ReplaceIllegalCharacters, apply the configured
//     ColonReplacementFormat (Smart special-cases `: ` → ` - ` then
//     `:` → `-`); else strip colons entirely.
//  2. BadCharacters[i] → GoodCharacters[i] when ReplaceIllegalCharacters,
//     else BadCharacters[i] → "".
//  3. Trim leading spaces/dots and trailing spaces.
func CleanFileName(name string, opts CleanNameOptions) string {
	result := name
	if opts.ReplaceIllegalCharacters {
		switch opts.ColonReplacementFormat {
		case ColonSmart:
			result = strings.ReplaceAll(result, ": ", " - ")
			result = strings.ReplaceAll(result, ":", "-")
		case ColonDash:
			result = strings.ReplaceAll(result, ":", "-")
		case ColonSpaceDash:
			result = strings.ReplaceAll(result, ":", " -")
		case ColonSpaceDashSpace:
			result = strings.ReplaceAll(result, ":", " - ")
		case ColonCustom:
			result = strings.ReplaceAll(result, ":", opts.CustomColonReplacementFormat)
		default: // ColonDelete + any unknown numeric value
			result = strings.ReplaceAll(result, ":", "")
		}
	} else {
		result = strings.ReplaceAll(result, ":", "")
	}

	for i, bad := range badCharacters {
		repl := ""
		if opts.ReplaceIllegalCharacters {
			repl = goodCharacters[i]
		}
		result = strings.ReplaceAll(result, bad, repl)
	}
	return strings.TrimRight(strings.TrimLeft(result, " ."), " ")
}

// CleanFileNameDefault is the default-NamingConfig shorthand for
// [CleanFileName] — mirrors the public C# `CleanFileName(string)`
// overload at `:387-390` that delegates with `NamingConfig.Default`.
func CleanFileNameDefault(name string) string {
	return CleanFileName(name, DefaultCleanNameOptions())
}

// CleanFolderName collapses runs of separator chars to a single
// occurrence and trims leading/trailing spaces and dots. Mirrors
// `:392-397`.
//
// Note: unlike [CleanFileName] this does NOT call into the
// BadCharacters / GoodCharacters substitution. The C# tree applies
// those upstream of CleanFolderName.
func CleanFolderName(name string) string {
	for i, re := range runCollapseRegexes {
		single := []string{"-", " ", ".", "_"}[i]
		name = re.ReplaceAllString(name, single)
	}
	return strings.Trim(name, " .")
}
