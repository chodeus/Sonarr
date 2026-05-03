package parser

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/language"
)

// Subtitle entry points for the LanguageParser port. Mirrors the
// `ParseSubtitleLanguage` / `ParseBasicSubtitle` /
// `ParseSubtitleLanguageInformation` / `UpdateTitleAndCopyFromTitle` /
// `ParseLanguageTags` block at `LanguageParser.cs:229-347` plus the
// three subtitle regexes at `:34-38`.

// SubtitleTitleInfo is the output of ParseSubtitleLanguageInformation
// and ParseBasicSubtitle. Mirrors `NzbDrone.Core.Parser.Model.SubtitleTitleInfo`.
//
// The C# `Language` field is a Language reference; the Go port stores
// it by value (Language is a comparable struct). `LanguageTags` is the
// list of `forced|foreign|default|cc|psdh|sdh` tokens captured from
// the filename, in match order.
type SubtitleTitleInfo struct {
	LanguageTags []string
	Language     language.Language
	RawTitle     string
	Title        string
	Copy         int
	TitleFirst   bool
}

// subtitleLanguageRe ports `LanguageParser.SubtitleLanguageRegex` at
// `:34`. The C# regex carries duplicate `<tags>` groups (one for the
// pre-iso prefix, one for the post-iso suffix). RE2 rejects duplicate
// named groups, so the Go port renames the second occurrence to
// `tags2` and merges the captures in ParseLanguageTags.
//
// C# original:
//
//	.+?([-_. ](?<tags>forced|foreign|default|cc|psdh|sdh))*
//	[-_. ](?<iso_code>[a-z]{2,3})
//	([-_. ](?<tags>forced|foreign|default|cc|psdh|sdh))*$
//	flags: i, c
var subtitleLanguageRe = regexp.MustCompile(
	`(?i).+?([-_. ](?P<tags1>forced|foreign|default|cc|psdh|sdh))*` +
		`[-_. ](?P<iso_code>[a-z]{2,3})` +
		`([-_. ](?P<tags2>forced|foreign|default|cc|psdh|sdh))*$`,
)

// subtitleLanguageTitleRe ports
// `LanguageParser.SubtitleLanguageTitleRegex` at `:36`. The C# regex
// carries duplicate `<iso_code>` groups (one in tags1's optional arm,
// one in tags2's). RE2 rejects duplicate named groups, so the Go port
// renames the second to `iso_code2`. ParseSubtitleLanguageInformation
// reads whichever one fired (only one can match per the C# alternation
// structure since `tags1` and `tags2` are sibling repetitions; the
// consumer at `:286-289` reads `iso_code` blindly because both forms
// share the same group name in C#).
//
// C# original:
//
//	.+?(\.((?<tags1>forced|foreign|default|cc|psdh|sdh)|(?<iso_code>[a-z]{2,3})))*
//	[-_. ](?<title>[^.]*)
//	(\.((?<tags2>forced|foreign|default|cc|psdh|sdh)|(?<iso_code>[a-z]{2,3})))*$
//	flags: i, c
var subtitleLanguageTitleRe = regexp.MustCompile(
	`(?i).+?(\.((?P<tags1>forced|foreign|default|cc|psdh|sdh)|(?P<iso_code>[a-z]{2,3})))*` +
		`[-_. ](?P<title>[^.]*)` +
		`(\.((?P<tags2>forced|foreign|default|cc|psdh|sdh)|(?P<iso_code2>[a-z]{2,3})))*$`,
)

// subtitleTitleRe ports `LanguageParser.SubtitleTitleRegex` at `:38`.
//
// C# original:
//
//	^((?<title>.+) - )?(?<copy>(?<!\d+)\d{1,3}(?!\d+))$
//	flags: c
//
// RE2 reformulation: the `(?<!\d+)\d{1,3}(?!\d+)` lookarounds are
// unsupported. Both are dropped because the surrounding regex shape
// preserves the same intent: the `^...$` anchoring + the literal ` - `
// trailer of the optional title prefix + the `\d{1,3}$` suffix
// guarantees the copy slot is exactly 1-3 digits with non-digit (or
// start of string) before and end of string after. The Go regex's
// backtracking on `\d{1,3}` against `^...$` rejects `1234` as the C#
// lookahead would: `\d{1,3}` matches greedily then backtracks; no
// 1-3 digit prefix of a 4+ digit run can satisfy `$`.
var subtitleTitleRe = regexp.MustCompile(`^((?P<title>.+) - )?(?P<copy>\d{1,3})$`)

// subtitleTagsAlternation lists the canonical tag tokens — kept here
// rather than re-derived from the regex group names so a future regex
// edit doesn't silently drift the consumer.
var subtitleTagsAlternation = []string{"forced", "foreign", "default", "cc", "psdh", "sdh"}

// ParseSubtitleLanguage extracts the language code embedded in a
// subtitle filename (e.g. `Series.S01E01.en.sub` → English). Mirrors
// `LanguageParser.ParseSubtitleLanguage` at `:229-262`.
//
// Algorithm:
//  1. Strip directory components and extension.
//  2. Run subtitleLanguageRe; if matched, look up the iso_code via
//     IsoLanguages.Find (lowercased to match the C# call at `:241`).
//  3. Otherwise fall back to enumerating every Language by Name and
//     checking whether the simple filename ends with that name
//     (case-insensitive, mirrors the `EndsWith(..., OrdinalIgnoreCase)`
//     scan at `:248-252`).
//  4. Otherwise return Language.Unknown.
//
// The C# code wraps everything in a try/catch on Exception. The Go
// port has no analogous panic surface — the regex compiles at package
// init, the slice walk is bounded — so the catch is implicit.
func ParseSubtitleLanguage(fileName string) language.Language {
	simpleFilename := stripExtension(filepath.Base(fileName))

	if m := subtitleLanguageRe.FindStringSubmatch(simpleFilename); m != nil {
		isoIdx := subtitleLanguageRe.SubexpIndex("iso_code")
		if isoIdx > 0 && isoIdx < len(m) {
			iso := strings.ToLower(m[isoIdx])
			if found, ok := language.FindIso(iso); ok {
				return found.Language
			}
			// Found an iso_code but no matching language — return
			// Unknown (matches the C# null-coalesce at `:243`).
			return language.Unknown
		}
	}

	for _, l := range language.All() {
		if hasSuffixFold(simpleFilename, l.Name) {
			return l
		}
	}

	return language.Unknown
}

// ParseLanguageTags extracts the `forced|foreign|default|cc|psdh|sdh`
// tokens embedded in a subtitle filename. Mirrors
// `LanguageParser.ParseLanguageTags` at `:330-347`.
//
// The C# helper merges captures from both `<tags>` group occurrences;
// the Go port merges captures from `tags1` and `tags2` in match order
// (tags1's matches first, then tags2's), preserving the C# Union
// behaviour at `:336-338`. Empty captures are skipped.
//
// Returns nil on no match or no tags. The C# return is a fresh
// List<string>; the Go port returns nil for the empty case (callers
// using len() / for-range work the same on either).
func ParseLanguageTags(fileName string) []string {
	simpleFilename := stripExtension(filepath.Base(fileName))
	m := subtitleLanguageRe.FindStringSubmatch(simpleFilename)
	if m == nil {
		return nil
	}
	// Capture-group all-instances: subtitleLanguageRe is `(...)*`-shaped
	// so FindStringSubmatch returns only the LAST instance per group.
	// The C# `Captures` collection returns ALL instances. To recover
	// every instance we re-scan with FindAllStringSubmatch over the
	// inner repeating subgroup. Since both `(?P<tags1>...)` and
	// `(?P<tags2>...)` are inside `([-_. ](?P<tagsN>...))*`, we just
	// capture the alternation directly with a tag-only regex.
	return findAllSubtitleTags(simpleFilename)
}

// tagOnlyRe matches one `[-_. ]<tag>` occurrence per call. Used by
// ParseLanguageTags and ParseSubtitleLanguageInformation to recover
// every captured tag (Go RE2 `(...)*` only retains the last instance,
// so a follow-up scan is required to enumerate all of them).
var tagOnlyRe = regexp.MustCompile(`(?i)[-_. ](forced|foreign|default|cc|psdh|sdh)\b`)

// findAllSubtitleTags returns every tag token in the filename in match
// order, lowercased. Used by ParseLanguageTags as the fixup for Go RE2
// `(...)*` retaining only the last capture.
func findAllSubtitleTags(simpleFilename string) []string {
	matches := tagOnlyRe.FindAllStringSubmatch(simpleFilename, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) >= 2 && m[1] != "" {
			out = append(out, strings.ToLower(m[1]))
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ParseBasicSubtitle is the cheap form: fileName → SubtitleTitleInfo
// with TitleFirst=false, language tags, and detected language. Mirrors
// `LanguageParser.ParseBasicSubtitle` at `:264-272`.
func ParseBasicSubtitle(fileName string) SubtitleTitleInfo {
	return SubtitleTitleInfo{
		TitleFirst:   false,
		LanguageTags: ParseLanguageTags(fileName),
		Language:     ParseSubtitleLanguage(fileName),
	}
}

// ParseSubtitleLanguageInformation parses a subtitle filename into the
// full SubtitleTitleInfo with title + language + tags + copy. Mirrors
// `LanguageParser.ParseSubtitleLanguageInformation` at `:274-309`.
//
// Algorithm:
//  1. Strip the extension; run subtitleLanguageTitleRe.
//  2. If the match has no `title` group OR has multiple iso_code
//     captures (`Captures.Count != 1` in C#), fall back to
//     ParseBasicSubtitle (no title-aware parsing).
//  3. Otherwise extract the iso_code, look up the language, gather
//     all tag captures from both arm positions, set TitleFirst per the
//     `tags1.Captures.Empty()` flag, set RawTitle from `title`.
//  4. Run UpdateTitleAndCopyFromTitle to split the raw title into
//     Title + Copy.
//
// Captures.Count handling: Go's RE2 `(...)*` retains only the last
// match per group, so we re-scan with `subtitleLanguageTitleAllIso`
// to count iso occurrences. If exactly one iso_code occurrence in the
// filename, the C# guard passes and we proceed with title parsing.
func ParseSubtitleLanguageInformation(fileName string) SubtitleTitleInfo {
	simpleFilename := stripExtension(filepath.Base(fileName))
	m := subtitleLanguageTitleRe.FindStringSubmatch(simpleFilename)

	titleIdx := subtitleLanguageTitleRe.SubexpIndex("title")
	if m == nil || titleIdx <= 0 || titleIdx >= len(m) || m[titleIdx] == "" {
		return ParseBasicSubtitle(fileName)
	}

	// C# `Captures.Count != 1`: count iso_code occurrences across the
	// repeating subgroup (Go's `(...)*` doesn't expose all captures).
	// In the C# regex, iso_code is matched only when the alternation's
	// tags arm fails — so only `[a-z]{2,3}` tokens NOT in the tag set
	// (`forced|foreign|default|cc|psdh|sdh`) count toward iso captures.
	if countIsoCodeCaptures(simpleFilename) != 1 {
		return ParseBasicSubtitle(fileName)
	}

	// Pull the iso from either named group (the C# tree's duplicate
	// `<iso_code>` collapses to one; the Go port keeps both).
	isoIdx1 := subtitleLanguageTitleRe.SubexpIndex("iso_code")
	isoIdx2 := subtitleLanguageTitleRe.SubexpIndex("iso_code2")
	var iso string
	if isoIdx1 > 0 && isoIdx1 < len(m) && m[isoIdx1] != "" {
		iso = m[isoIdx1]
	} else if isoIdx2 > 0 && isoIdx2 < len(m) && m[isoIdx2] != "" {
		iso = m[isoIdx2]
	}
	lang := language.Unknown
	if iso != "" {
		if found, ok := language.FindIso(strings.ToLower(iso)); ok {
			lang = found.Language
		}
	}

	// Collect every tag occurrence (both `tags1` and `tags2` arm
	// positions), in match order.
	tags := findAllSubtitleTags(simpleFilename)

	// TitleFirst = (tags1's captures are empty). The C# guard at `:300`
	// reads `matchTitle.Groups["tags1"].Captures.Empty()` — true iff
	// no tag occurred BEFORE the title. We recover this by scanning
	// the prefix before the title boundary; if no tag landed there,
	// TitleFirst is true.
	rawTitle := m[titleIdx]
	titleFirst := !hasTagBeforeTitle(simpleFilename, rawTitle)

	info := SubtitleTitleInfo{
		TitleFirst:   titleFirst,
		LanguageTags: tags,
		RawTitle:     rawTitle,
		Language:     lang,
	}
	UpdateTitleAndCopyFromTitle(&info)
	return info
}

// dotTokenRe matches `.<token>` where token is 2-3 lowercase
// alphabetic. countIsoCodeCaptures filters tag tokens out of the
// returned set.
var dotTokenRe = regexp.MustCompile(`(?i)\.([a-z]{2,3})\b`)

// countIsoCodeCaptures returns the number of `[a-z]{2,3}` tokens in
// simpleFilename that are NOT subtitle tags. Mirrors the C# regex's
// effective iso_code Captures.Count: tokens like `eng` / `fra` / `ru`
// count toward iso (not in tag alternation); tokens like `cc` / `sdh`
// match the tags arm first and never produce an iso capture.
func countIsoCodeCaptures(simpleFilename string) int {
	matches := dotTokenRe.FindAllStringSubmatch(simpleFilename, -1)
	count := 0
	for _, m := range matches {
		if len(m) >= 2 && !isSubtitleTag(strings.ToLower(m[1])) {
			count++
		}
	}
	return count
}

func isSubtitleTag(token string) bool {
	for _, t := range subtitleTagsAlternation {
		if token == t {
			return true
		}
	}
	return false
}

// hasTagBeforeTitle reports whether the filename has a
// `[-_. ](forced|foreign|default|cc|psdh|sdh)` token positioned BEFORE
// the title boundary in the input. Used to derive TitleFirst.
func hasTagBeforeTitle(simpleFilename, rawTitle string) bool {
	if rawTitle == "" {
		return false
	}
	idx := strings.Index(simpleFilename, rawTitle)
	if idx <= 0 {
		return false
	}
	prefix := simpleFilename[:idx]
	return tagOnlyRe.MatchString(prefix)
}

// UpdateTitleAndCopyFromTitle post-processes a SubtitleTitleInfo's
// RawTitle into a Title + Copy via subtitleTitleRe. Mirrors
// `LanguageParser.UpdateTitleAndCopyFromTitle` at `:311-328`.
//
//   - RawTitle nil/empty → Title=nil, Copy=0.
//   - RawTitle matches `^((title>.+) - )?(?<copy>\d{1,3})$` → Title
//     = `title` group (or nil), Copy = parsed `copy` group.
//   - Otherwise → Title = RawTitle, Copy = 0.
func UpdateTitleAndCopyFromTitle(info *SubtitleTitleInfo) {
	if info.RawTitle == "" {
		info.Title = ""
		info.Copy = 0
		return
	}
	m := subtitleTitleRe.FindStringSubmatch(info.RawTitle)
	if m == nil {
		info.Title = info.RawTitle
		info.Copy = 0
		return
	}
	titleIdx := subtitleTitleRe.SubexpIndex("title")
	copyIdx := subtitleTitleRe.SubexpIndex("copy")
	if titleIdx > 0 && titleIdx < len(m) {
		info.Title = m[titleIdx]
	}
	if copyIdx > 0 && copyIdx < len(m) {
		if n, err := strconv.Atoi(m[copyIdx]); err == nil {
			info.Copy = n
		}
	}
}

// stripExtension removes the trailing dotted extension from a basename.
// Mirrors `Path.GetFileNameWithoutExtension(fileName)`.
func stripExtension(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[:i]
	}
	return name
}

// hasSuffixFold reports whether s ends with suffix, case-insensitive.
// Mirrors `string.EndsWith(suffix, StringComparison.OrdinalIgnoreCase)`.
func hasSuffixFold(s, suffix string) bool {
	if len(suffix) > len(s) {
		return false
	}
	return strings.EqualFold(s[len(s)-len(suffix):], suffix)
}
