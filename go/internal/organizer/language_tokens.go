package organizer

import (
	"strings"

	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/mediainfo"
)

// FormatLanguagesToken ports
// `FileNameBuilder.GetLanguagesToken(List<string>, string, bool, bool)`
// at `src/NzbDrone.Core/Organizer/FileNameBuilder.cs:747-814`.
//
// Takes a list of MediaInfo language codes (typically 3-letter ISO
// 639-2 like "eng", "jpn"), normalises each to a 2-letter UPPERCASE
// ISO 639-1 code (`EN`, `JP`), de-duplicates, applies an optional
// filter, and joins with `+`.
//
// Filter syntax (`filter` parameter):
//
//   - "" or whitespace → no filter applied.
//   - "X-Y-Z" with leading "-" → exclude these codes from the result.
//     The `-` separates codes (the leading `-` is the marker; the
//     remainder is split on `-` again — this is what the C# does at
//     `:785` via `filter.Split('-')`).
//   - "X+Y+Z" without leading "-" → intersect with these codes.
//     If the filter ALSO ends in `+` and any tokens were excluded,
//     append the literal `--` wildcard to the output. Mirrors C#
//     `:794-797`.
//
// Behavioural flags:
//
//   - skipEnglishOnly: when the filtered result is exactly ["EN"],
//     return empty string. Common case: a single-language English
//     audio track shouldn't pollute the rendered name.
//   - quoted: wrap the joined non-empty result in `[...]`. Used by
//     every MediaInfo language token in the C# tree.
//
// Codes that don't resolve to an Iso row (typo'd, "und", whitespace,
// or genuinely unknown) are silently dropped — matches the C# `try
// { ... } catch { }` swallow at `:760-773`. The Go port logs nothing.
func FormatLanguagesToken(codes []string, filter string, skipEnglishOnly, quoted bool) string {
	tokens := make([]string, 0, len(codes))
	for _, c := range codes {
		t := strings.TrimSpace(c)
		if t == "" || t == "und" {
			continue
		}
		two, ok := normalizeIsoToken(t)
		if !ok {
			continue
		}
		tokens = append(tokens, two)
	}
	tokens = dedupeStrings(tokens)
	filtered := tokens

	hasFilter := strings.TrimSpace(filter) != ""
	if hasFilter {
		if strings.HasPrefix(filter, "-") {
			// Exclude: tokens \ filter[1:].split('-')
			excludes := strings.Split(filter[1:], "-")
			filtered = exceptStrings(tokens, excludes)
		} else {
			// Intersect: filter.split('+') ∩ tokens
			wanted := strings.Split(filter, "+")
			filtered = intersectStrings(wanted, tokens)
		}
	}

	// Trailing-`+` wildcard rule: if filter ends with `+` AND something
	// was filtered out, append the literal "--" sentinel. Matches C#
	// `:794-797` exactly.
	if hasFilter && strings.HasSuffix(filter, "+") && len(filtered) != len(tokens) {
		filtered = append(filtered, "--")
	}

	if skipEnglishOnly && len(filtered) == 1 && filtered[0] == "EN" {
		return ""
	}

	response := strings.Join(filtered, "+")
	if quoted && response != "" {
		return "[" + response + "]"
	}
	return response
}

// normalizeIsoToken resolves a raw MediaInfo language code (e.g. "eng",
// "fre", "GER", or even a 2-letter "en") to its 2-letter UPPERCASE
// ISO 639-1 code. Returns ("", false) when the code can't be
// resolved.
//
// Lookup chain (mirrors C# `:758-773`):
//
//  1. lowercase the input
//  2. language.FindIso applies iso639BTMap (B→T) for 3-letter codes
//     internally and resolves to an Iso row via TwoLetter / ThreeLetter
//     match
//  3. take iso.TwoLetter, uppercase
//
// The C# version uses `new CultureInfo(token).TwoLetterISOLanguageName`
// which goes through the OS locale data; the Go port goes through
// our own ISO registry (53 entries — the 52 named languages + the
// duplicate Norwegian/Dutch entries). Coverage is similar for the
// codes that reach this code path (MediaInfo emits 3-letter ISO
// 639-2 codes for known languages and "und" for unknown).
func normalizeIsoToken(code string) (string, bool) {
	iso, ok := language.FindIso(strings.ToLower(code))
	if !ok || iso.TwoLetter == "" {
		return "", false
	}
	return strings.ToUpper(iso.TwoLetter), true
}

// dedupeStrings preserves the original order while dropping
// duplicate entries (case-sensitive). Mirrors the C# `Distinct()`
// at `:776`, which on a `List<string>` preserves first-occurrence
// ordering.
func dedupeStrings(in []string) []string {
	if len(in) <= 1 {
		return in
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, hit := seen[s]; hit {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// exceptStrings returns `a` with any entry in `b` removed.
// Mirrors `tokens.Except(filter.Split('-'))` at `:785`.
func exceptStrings(a, b []string) []string {
	exclude := make(map[string]struct{}, len(b))
	for _, s := range b {
		exclude[s] = struct{}{}
	}
	out := make([]string, 0, len(a))
	for _, s := range a {
		if _, hit := exclude[s]; hit {
			continue
		}
		out = append(out, s)
	}
	return out
}

// intersectStrings returns the entries of `a` that are also in `b`,
// preserving `a`'s order. Mirrors `filter.Split('+').Intersect(tokens)`
// at `:789`.
func intersectStrings(a, b []string) []string {
	have := make(map[string]struct{}, len(b))
	for _, s := range b {
		have[s] = struct{}{}
	}
	out := make([]string, 0, len(a))
	for _, s := range a {
		if _, hit := have[s]; hit {
			out = append(out, s)
		}
	}
	return out
}

// languagesFromAudioStreams pulls the Language field from each audio
// stream. Empty / "und" entries are NOT pre-filtered here — that's
// FormatLanguagesToken's responsibility, so the same dropout-on-empty
// semantic applies whether the caller goes through this helper or
// passes a raw []string.
func languagesFromAudioStreams(streams []mediainfo.AudioStream) []string {
	out := make([]string, len(streams))
	for i, s := range streams {
		out[i] = s.Language
	}
	return out
}

// languagesFromSubtitleStreams pulls the Language field from each
// subtitle stream. See [languagesFromAudioStreams].
func languagesFromSubtitleStreams(streams []mediainfo.SubtitleStream) []string {
	out := make([]string, len(streams))
	for i, s := range streams {
		out[i] = s.Language
	}
	return out
}

// languageTokenHandler returns a TokenHandler that consults the
// per-call CustomFormat field on the TokenMatch to drive the filter.
// Mirrors C# `m => GetLanguagesToken(langs, m.CustomFormat, X, true)`
// at `:680-684` — every call site passes `quoted=true`, so the
// helper hardcodes that.
func languageTokenHandler(langs []string, skipEnglishOnly bool) TokenHandler {
	return func(m TokenMatch) string {
		return FormatLanguagesToken(langs, m.CustomFormat, skipEnglishOnly, true)
	}
}
