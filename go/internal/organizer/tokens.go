package organizer

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// titleRegex ports `FileNameBuilder.TitleRegex` at
// `FileNameBuilder.cs:48-49`.
//
// Two top-level alternatives (joined by `|`):
//
//  1. Escaped braces: `{{` or `}}` — captured into the `escaped` group.
//  2. Token form: `\{prefix token (:customFormat)? suffix\}` where
//     the token is `[a-z0-9]+` optionally followed by separator chars
//     plus another `[a-z0-9]+` (e.g. `Series Title` is two
//     alphanumeric runs separated by a space).
//
// RE2 divergences from the C# regex:
//
//   - `(?<![- ])` negative lookbehind on customFormat: not supported.
//     The Go port matches `[ ,a-z0-9+-]+` greedily, then post-processes
//     to trim trailing `-` / ` ` from the captured customFormat.
//   - Named groups use Go's `(?P<name>...)` syntax (vs C# `(?<name>...)`).
//
// Group names match the C# version verbatim so anyone reading both
// implementations side-by-side sees the same group identifiers.
var titleRegex = regexp.MustCompile(
	`(?i)(?P<escaped>\{\{|\}\})|\{(?P<prefix>[- ._\[(]*)(?P<token>(?:[a-z0-9]+)(?:(?P<separator>[- ._]+)(?:[a-z0-9]+))?)(?::(?P<customFormat>[ ,a-z0-9+\-]+))?(?P<suffix>[- ._)\]]*)\}`,
)

// TokenMatch is the parsed shape of one `{Token:format}` occurrence.
// Mirrors the C# inner class at `FileNameBuilder.cs:1232-1257`.
//
// Fields:
//
//   - Prefix / Suffix — the connector chars captured before/after
//     the token name (e.g. ` (` and `) ` for `{ (Episode Title) }`).
//   - Separator — the separator chars between two-word tokens
//     (e.g. ` ` for `{Series Title}`, `.` for `{Series.Title}`,
//     `_` for `{Series_Title}`).
//   - Token — the token name itself, with original separator
//     preserved.
//   - CustomFormat — the optional `:format` value (e.g. `00` for
//     zero-padded numbers, `MM/dd` for date formats).
type TokenMatch struct {
	Prefix       string
	Separator    string
	Suffix       string
	Token        string
	CustomFormat string
}

// DefaultValue ports `TokenMatch.DefaultValue(string)` at
// `:1246-1256`. Returns `defaultValue` when neither prefix nor suffix
// is set; otherwise returns the empty string.
//
// This is the C# tree's "if the user wrote `{Series Title}` we render
// the title; if they wrote ` ({Series Title}) ` and the title is empty,
// the prefix+suffix shouldn't render either" guard.
func (t TokenMatch) DefaultValue(defaultValue string) string {
	if t.Prefix == "" && t.Suffix == "" {
		return defaultValue
	}
	return ""
}

// TokenHandler is the per-token-name resolver. Returns the replacement
// text for the token, or the empty string when the token has no
// meaningful value. Mirrors the C# `Func<TokenMatch, string>` signature
// used at `:836`.
//
// Returning [TokenPreserve] (instead of an empty string) tells
// [ReplaceTokens] to leave the original `{Token}` text in place — the
// equivalent of the C# `m => null` placeholder semantic at `:614-615`
// + `:878-882`. Use this when registering placeholder handlers that a
// later pass will overwrite.
type TokenHandler func(TokenMatch) string

// TokenPreserve is the sentinel return value a [TokenHandler] returns
// to keep the original `{Token}` text in the rendered output.
//
// Mirrors the C# `null` return from `AddEpisodeTitlePlaceholderTokens`
// (`:612-616`) plus the `if (replacementText == null) return match.Value`
// pass-through at `:878-882`. Carrying it as a sentinel string keeps
// the TokenHandler signature unchanged across the package.
//
// The sentinel uses NUL chars so it never collides with a legitimate
// rendered value (filenames may not contain NUL on any supported OS).
const TokenPreserve = "\x00\x00organizer-preserve\x00\x00"

// NormalizeTokenKey ports
// `FileNameBuilderTokenEqualityComparer.SimplifyToken` at
// `FileNameBuilderTokenEqualityComparer.cs:26-29`. Strips every
// non-letter and non-digit (the C# `\s|_|\W` regex), lowercases the
// rest. Lets a single handler key (e.g. `seriestitle`) match
// user-written variants `{Series Title}`, `{Series.Title}`,
// `{Series_Title}`, `{series-title}`, etc.
//
// Apply to handler-map keys at registration time AND to the token
// captured from the user's pattern at lookup time so both sides
// collapse to the same canonical form.
func NormalizeTokenKey(key string) string {
	var sb strings.Builder
	sb.Grow(len(key))
	for _, r := range key {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			sb.WriteRune(unicode.ToLower(r))
		}
	}
	return sb.String()
}

// ReplaceTokens runs the token-replacement pass over `pattern`.
// Mirrors `FileNameBuilder.ReplaceTokens(pattern, handlers, namingConfig, escape)`
// at `:836-839`.
//
// `handlers` maps lower-cased token name → resolver. Lookup is
// case-insensitive (the C# version uses `Dictionary<string, ...>` with
// `StringComparer.InvariantCultureIgnoreCase`); the Go port lower-cases
// the key on both sides.
//
// `escape=true` doubles up curly braces in the replacement text so
// nested ReplaceTokens calls can still unescape them. Used by the
// nested-pattern paths in the SeasonEpisode/AbsoluteEpisode formatters.
func ReplaceTokens(pattern string, handlers map[string]TokenHandler, opts CleanNameOptions, escape bool) string {
	return titleRegex.ReplaceAllStringFunc(pattern, func(match string) string {
		return replaceTokenRaw(match, handlers, opts, escape)
	})
}

// replaceTokenRaw is the inner replace step. Receives the raw matched
// substring (the entire `{...}` or `{{`/`}}`), looks up its capture
// groups via [titleRegex.FindStringSubmatch], and resolves it.
func replaceTokenRaw(match string, handlers map[string]TokenHandler, opts CleanNameOptions, escape bool) string {
	groups := titleRegex.FindStringSubmatch(match)
	if groups == nil {
		return match
	}
	names := titleRegex.SubexpNames()
	get := func(name string) string {
		for i, n := range names {
			if n == name {
				return groups[i]
			}
		}
		return ""
	}

	escaped := get("escaped")
	if escaped != "" {
		if escape {
			return match
		}
		switch escaped {
		case "{{":
			return "{"
		case "}}":
			return "}"
		}
	}

	tm := TokenMatch{
		Prefix:       get("prefix"),
		Separator:    get("separator"),
		Suffix:       get("suffix"),
		Token:        get("token"),
		CustomFormat: get("customFormat"),
	}

	// RE2-divergence post-process: trim trailing `-` and ` ` from
	// customFormat (the C# `(?<![- ])` lookbehind would have prevented
	// these chars from being matched into the customFormat group).
	if tm.CustomFormat != "" {
		trimmed := strings.TrimRight(tm.CustomFormat, "- ")
		if trimmed == "" {
			tm.CustomFormat = ""
		} else {
			tm.CustomFormat = trimmed
		}
	}
	if strings.TrimSpace(tm.CustomFormat) == "" {
		tm.CustomFormat = ""
	}

	handler, ok := handlers[NormalizeTokenKey(tm.Token)]
	if !ok {
		// No handler → drop the token entirely (matches C#
		// `tokenHandlers.GetValueOrDefault(..., m => string.Empty)` at
		// `:874`).
		handler = func(TokenMatch) string { return "" }
	}

	replacement := handler(tm)

	// Sentinel: handler asks to preserve the original token. Used by
	// placeholder registrations (e.g. AddEpisodeTitlePlaceholderTokens)
	// where a later pass will overwrite the registration.
	if replacement == TokenPreserve {
		return match
	}

	// Case-folding: if the user wrote `{series title}` (all-lower or
	// non-letter), force the replacement to lowercase. If `{SERIES TITLE}`
	// (all-upper or non-letter), force uppercase. Mirrors `:886-893`.
	if isAllLower(tm.Token) {
		replacement = strings.ToLower(replacement)
	} else if isAllUpper(tm.Token) {
		replacement = strings.ToUpper(replacement)
	}

	replacement = strings.TrimSpace(replacement)

	// Replace any internal spaces in the replacement with the user's
	// chosen separator (e.g. `.` in `{Series.Title}` → `Series.Title`
	// renders as `My.Show` not `My Show`). Mirrors `:895-898`.
	if strings.TrimSpace(tm.Separator) != "" {
		replacement = strings.ReplaceAll(replacement, " ", tm.Separator)
	}

	replacement = CleanFileName(replacement, opts)

	if strings.TrimSpace(replacement) != "" {
		replacement = tm.Prefix + replacement + tm.Suffix
	}

	if escape {
		replacement = strings.ReplaceAll(replacement, "{", "{{")
		replacement = strings.ReplaceAll(replacement, "}", "}}")
	}

	return replacement
}

// isAllLower reports whether every letter rune in `s` is lower-case.
// Non-letter runes are ignored (matches C# `t => !char.IsLetter(t) || char.IsLower(t)`).
func isAllLower(s string) bool {
	for _, r := range s {
		if !unicode.IsLetter(r) {
			continue
		}
		if !unicode.IsLower(r) {
			return false
		}
	}
	return true
}

// isAllUpper reports whether every letter rune in `s` is upper-case.
func isAllUpper(s string) bool {
	for _, r := range s {
		if !unicode.IsLetter(r) {
			continue
		}
		if !unicode.IsUpper(r) {
			return false
		}
	}
	return true
}

// FormatNumber formats `value` as a decimal number using the given
// padding format (e.g. `"00"` produces `"05"` from value 5). Mirrors
// the C# `value.ToString(format)` semantics for integer
// zero-padding format strings only — this slice doesn't port the full
// .NET numeric format-string repertoire.
//
// Supported format strings:
//
//   - `""` or `"0"` (or any all-`0` string): zero-padded to length(format).
//   - Any other format: returned as decimal with no padding (the
//     `value.ToString("0")` C# default at `:965`).
//
// The wider .NET format-string surface (e.g. `MM/dd/yyyy` for dates,
// `0.00` for decimals) is unused by the integer-token resolvers and
// out of scope for the file-name builder.
func FormatNumber(value int, format string) string {
	if format == "" {
		return fmt.Sprintf("%d", value)
	}
	// Count leading zeros to determine padding width.
	width := 0
	for _, r := range format {
		if r != '0' {
			width = 0
			break
		}
		width++
	}
	if width == 0 {
		return fmt.Sprintf("%d", value)
	}
	if value < 0 {
		return fmt.Sprintf("-%0*d", width, -value)
	}
	return fmt.Sprintf("%0*d", width, value)
}
