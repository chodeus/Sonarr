package parser

import (
	"regexp"
	"strings"

	"sonarr.tv/sonarr/internal/quality"
)

// First slice of the ReleaseGroupParser port. Lays the foundation by
// porting `NzbDrone.Core.Parser.ParserCommon` (three regex helpers used
// across the parser surface) plus `NzbDrone.Core.MediaFiles.FileExtensions.RemoveFileExtension`
// (which ParseReleaseGroup invokes before its main pipeline). The
// `PreSubstitutionRegex` array (14 patterns at `ParserCommon.cs:9-52`)
// is deferred to a later slice — it is the heaviest single chunk in
// `ParserCommon` and lands separately so the surrounding pipeline
// can be tested without it first.

// websitePrefixRe ports the C# `WebsitePrefixRegex` at
// `ParserCommon.cs:54`. Strips a website-name prefix at the start of a
// title (e.g. `[ www.Torrenting.com ] - Series.S01...` →
// `Series.S01...`).
//
// C# original:
//
//	^(?:(?:\[|\()\s*)?(?:www\.)?[-a-z0-9-]{1,256}\.(?<!Naruto-Kun\.)
//	(?:[a-z]{2,6}\.[a-z]{2,6}|xn--[a-z0-9-]{4,}|[a-z]{2,})\b
//	(?:\s*(?:\]|\))|[ -]{2,})[ -]*
//	flags: i, c
//
// RE2 reformulation: the `(?<!Naruto-Kun\.)` negative lookbehind sits
// immediately after the literal dot following the domain stem. It
// rejects matches where the stem is the literal `Naruto-Kun` (an
// anime release-naming convention that contains a literal dot).
// Implemented post-match: the domain stem is captured into the
// `stem` named group and ApplyWebsitePrefix verifies it is not
// `Naruto-Kun` (case-insensitive) before emitting the strip.
var websitePrefixRe = regexp.MustCompile(
	`(?i)^(?:(?:\[|\()\s*)?(?:www\.)?(?P<stem>[-a-z0-9]{1,256})` +
		`\.(?:[a-z]{2,6}\.[a-z]{2,6}|xn--[a-z0-9-]{4,}|[a-z]{2,})\b` +
		`(?:\s*(?:\]|\))|[ -]{2,})[ -]*`,
)

// websitePostfixRe ports the C# `WebsitePostfixRegex` at
// `ParserCommon.cs:58`. Strips a `[www.example.com]` suffix at the
// end of a title.
//
// RE2-clean (no lookarounds; `xn--` punycode + standard TLD).
var websitePostfixRe = regexp.MustCompile(
	`(?i)(?:\[\s*)?(?:www\.)?[-a-z0-9]{1,256}` +
		`\.(?:xn--[a-z0-9-]{4,}|[a-z]{2,6})\b` +
		`(?:\s*\])$`,
)

// cleanTorrentSuffixRe ports the C# `CleanTorrentSuffixRegex` at
// `ParserCommon.cs:62`. Strips a trailing `[ettv]` / `[rartv]` /
// `[rarbg]` / `[cttv]` / `[publichd]` torrent-tracker suffix.
//
// RE2-clean.
var cleanTorrentSuffixRe = regexp.MustCompile(
	`(?i)\[(?:ettv|rartv|rarbg|cttv|publichd)\]$`,
)

// fileExtensionRe ports `FileExtensions.FileExtensionRegex` at
// `FileExtensions.cs:9`. Matches a 2-4 char alphanumeric extension at
// the end of the input.
var fileExtensionRe = regexp.MustCompile(`(?i)\.[a-z0-9]{2,4}$`)

// usenetExtensions mirrors `FileExtensions.UsenetExtensions` at
// `FileExtensions.cs:12-16`.
var usenetExtensions = map[string]bool{
	".par2": true,
	".nzb":  true,
}

// ApplyWebsitePrefix strips a website-name prefix from the start of
// title. If matched and the captured stem is NOT the literal
// `Naruto-Kun` (case-insensitive), the matched portion is removed.
// Mirrors `ParserCommon.WebsitePrefixRegex.Replace(title, "")` at
// `ReleaseGroupParser.cs:40` with the `(?<!Naruto-Kun\.)` lookbehind
// applied post-match.
func ApplyWebsitePrefix(title string) string {
	loc := websitePrefixRe.FindStringSubmatchIndex(title)
	if loc == nil {
		return title
	}
	stemIdx := websitePrefixRe.SubexpIndex("stem")
	if stemIdx > 0 && 2*stemIdx+1 < len(loc) {
		stem := title[loc[2*stemIdx]:loc[2*stemIdx+1]]
		if strings.EqualFold(stem, "Naruto-Kun") {
			return title
		}
	}
	return title[:loc[0]] + title[loc[1]:]
}

// ApplyWebsitePostfix strips a `[www.example.com]` suffix from the
// end of title. Mirrors `WebsitePostfixRegex.Replace(title, "")`.
//
// Currently unused by the production pipeline (the C# tree defines
// this regex but does not invoke it from ReleaseGroupParser; it is
// reserved for a parallel parser path). Exported here for the same
// future-use reason and so the test suite can pin its behaviour.
func ApplyWebsitePostfix(title string) string {
	return websitePostfixRe.ReplaceAllString(title, "")
}

// ApplyCleanTorrentSuffix strips a trailing torrent-tracker suffix
// from title. Mirrors `CleanTorrentSuffixRegex.Replace(title, "")` at
// `ReleaseGroupParser.cs:41`.
func ApplyCleanTorrentSuffix(title string) string {
	return cleanTorrentSuffixRe.ReplaceAllString(title, "")
}

// RemoveFileExtension strips a 2-4 char extension from the end of a
// title IF the extension is a recognised media or usenet extension.
// Mirrors `FileExtensions.RemoveFileExtension` at `FileExtensions.cs:51-65`.
//
// The C# code uses `Regex.Replace` with a MatchEvaluator that returns
// the original match unchanged when the extension isn't recognised
// (so non-media extensions like `.com` or `.org` are preserved). The
// Go port replicates this by checking the captured extension against
// MediaFileExtensions + usenetExtensions; non-matches return the input
// unchanged.
func RemoveFileExtension(title string) string {
	return fileExtensionRe.ReplaceAllStringFunc(title, func(match string) string {
		ext := strings.ToLower(match)
		if isMediaExtension(ext) || usenetExtensions[ext] {
			return ""
		}
		return match
	})
}

// isMediaExtension reports whether ext is a recognised media file
// extension. Mirrors `MediaFileExtensions.Extensions.Contains(ext)`
// at `MediaFileExtensions.cs:11-78`. The Go port's MediaFileExtensions
// data lives in `internal/quality/extensions.go` (colocated to avoid
// an import cycle with quality.Quality — see the package comment
// there); this helper queries it via the public lookup which returns
// a non-Unknown Quality iff the extension is in the map.
//
// Lower-cased extension expected — the C# HashSet is
// case-insensitive but the Go port stores keys lowercased and the
// internal callers always pre-lowercase via strings.ToLower.
func isMediaExtension(ext string) bool {
	q := quality.GetQualityForExtension(ext)
	return q.ID != quality.Unknown.ID
}

// IsMediaExtension is the exported alias of [isMediaExtension] for
// callers outside the parser package (e.g.
// `internal/downloadedimport.processFile`'s unsupported-extension
// gate). Pre-lowercase the extension before calling.
func IsMediaExtension(ext string) bool {
	return isMediaExtension(ext)
}
