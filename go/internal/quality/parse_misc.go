package quality

import "regexp"

// rawHDRe is the C# `RawHDRegex` from `QualityParser.cs:32`:
//
//	\b(?<rawhd>RawHD|Raw[-_. ]HD)\b
//	flags: c, i
//
// RE2-clean. The named group is unused by the consumer (which calls
// `RawHDRegex.IsMatch(...)` only at `QualityParser.cs:105`), so the
// Go port elides the name.
var rawHDRe = regexp.MustCompile(`(?i)\b(?:RawHD|Raw[-_. ]HD)\b`)

// IsRawHD reports whether a release name advertises "RawHD" — an
// uncompressed HD broadcast capture. Mirrors the bare
// `RawHDRegex.IsMatch(name)` at `QualityParser.cs:105`.
//
// True for `RawHD`, `Raw-HD`, `Raw_HD`, `Raw.HD`, `Raw HD` (any
// of the 4 separator chars from the `[-_. ]` class). False for
// substrings like `RawHDStuff` or `xRawHDx` because of the `\b`
// anchors.
func IsRawHD(name string) bool {
	return rawHDRe.MatchString(name)
}

// mpeg2Re is the C# `MPEG2Regex` from `QualityParser.cs:35`:
//
//	\b(?<mpeg2>MPEG[-_. ]?2)\b
//	flags: (none — case-sensitive)
//
// RE2-clean. The C# regex has NO RegexOptions.IgnoreCase flag, so the
// Go port matches case-sensitive `MPEG2`, `MPEG-2`, `MPEG.2` etc. and
// rejects `mpeg2`, `Mpeg2`, etc. Preserved verbatim — only the
// uppercase form appears in real release names per the fixture.
var mpeg2Re = regexp.MustCompile(`\b(?:MPEG[-_. ]?2)\b`)

// IsMPEG2 reports whether a release name advertises MPEG-2 video
// encoding (typically a TS/PS rip from broadcast, treated as RAWHD
// when paired with HDTV). Mirrors `MPEG2Regex.IsMatch(name)` at
// `QualityParser.cs:231`.
//
// Case-SENSITIVE per the C# regex declaration: `MPEG2`, `MPEG-2`,
// `MPEG_2`, `MPEG.2`, `MPEG 2` all match; `mpeg2` does not.
func IsMPEG2(name string) bool {
	return mpeg2Re.MatchString(name)
}

// highDefPdtvRe is the C# `HighDefPdtvRegex` from `QualityParser.cs:64`:
//
//	hr[-_. ]ws
//	flags: c, i
//
// RE2-clean. Matches the `hr-ws` / `hr.ws` / `hr ws` / `hr_ws` token
// that some HR (high-rate) PDTV captures advertise. No anchors or
// word boundaries on either side — the C# regex matches as a
// substring. Preserved verbatim.
var highDefPdtvRe = regexp.MustCompile(`(?i)hr[-_. ]ws`)

// IsHighDefPdtv reports whether a release name advertises a "HR-WS"
// token (high-rate widescreen PDTV capture, treated as 720p HDTV
// when paired with PDTV/SDTV/DSR/TVRip). Mirrors
// `HighDefPdtvRegex.IsMatch(name)` at `QualityParser.cs:308`.
func IsHighDefPdtv(name string) bool {
	return highDefPdtvRe.MatchString(name)
}

// animeWebDLRe is the C# `AnimeWebDlRegex` from `QualityParser.cs:62`:
//
//	\[WEB\]|[\[\(]WEB[ .]
//	flags: c, i
//
// RE2-clean. Two arms: literal `[WEB]` token, or `[`/`(` followed by
// `WEB` followed by space/period.
var animeWebDLRe = regexp.MustCompile(`(?i)\[WEB\]|[\[(]WEB[ .]`)

// IsAnimeWebDL reports whether a release name carries the anime-web
// release-tag shape (`[WEB]`, `[WEB ...`, `(WEB...`, etc.). Mirrors
// `AnimeWebDlRegex.Match(name).Success` at `QualityParser.cs:392`.
//
// Used by ParseQualityName's anime-WebDL branch to override the
// resolution-only fallback for anime releases. False for bare `WEB`
// without the bracket/paren prefix (those are caught by the main
// SourceRegex / ParseSource).
func IsAnimeWebDL(name string) bool {
	return animeWebDLRe.MatchString(name)
}

// animeBlurayResRe is arm 1 of the C# `AnimeBlurayRegex` at
// `QualityParser.cs:61`: `bd(?:720|1080|2160)`. RE2-clean as written.
//
// Matches `bd720`, `bd1080`, `bd2160` — the no-separator anime-bluray
// resolution tag.
var animeBlurayResRe = regexp.MustCompile(`(?i)bd(?:720|1080|2160)`)

// animeBluraySepRe is the RE2 rewrite of arm 2 of the C# regex:
//
//	(?<=[-_. (\[])bd(?=[-_. )\]])
//
// RE2 rejects both lookbehind and lookahead, so the surrounding
// separator chars are folded into the match itself. Functionally
// equivalent for IsMatch (the only consumer at `QualityParser.cs:350`)
// because the C# lookarounds are zero-width assertions on bd's
// surrounding chars — pulling them into the match consumes those
// chars but doesn't affect match-success on a release name where the
// `bd` token appears with the documented separators on both sides.
//
// The character classes `[-_. (\[]` (leading) and `[-_. )\]]`
// (trailing) are kept verbatim. Note the asymmetric `(`/`)` and
// `[`/`]` pairing — the C# regex requires an OPEN bracket/paren
// before `bd` and a CLOSE bracket/paren after, mirroring the
// release-naming convention `[BD]` or `(BD)` for anime sources.
var animeBluraySepRe = regexp.MustCompile(`(?i)[-_. (\[]bd[-_. )\]]`)

// IsAnimeBluray reports whether a release name carries the anime-
// bluray release-tag shape. Mirrors `AnimeBlurayRegex.Match(name).Success`
// at `QualityParser.cs:350`.
//
// Two arms (matching the C# alternation):
//
//  1. `bd720` / `bd1080` / `bd2160` — the no-separator concatenated
//     resolution tag.
//  2. `bd` surrounded by separator chars `[-_. (\[]` (before) and
//     `[-_. )\]]` (after) — the release-name `BD` token.
//
// Used by ParseQualityName's anime-Bluray branch to override the
// resolution-only fallback for anime releases. Case-insensitive per
// the C# RegexOptions.IgnoreCase, so `BD`, `bd`, `Bd`, `bD` all
// match.
func IsAnimeBluray(name string) bool {
	if animeBlurayResRe.MatchString(name) {
		return true
	}
	return animeBluraySepRe.MatchString(name)
}

// otherSourceRe is the C# `OtherSourceRegex` from `QualityParser.cs:59`:
//
//	(?<hdtv>HD[-_. ]TV)|(?<sdtv>SD[-_. ]TV)
//	flags: c, i
//
// RE2-clean. Two named groups discriminate between `HD-TV`/`HD.TV`/
// `HD TV`/`HD_TV` (mapping to HDTV720p) and `SD-TV`/etc. (mapping to
// SDTV). Used as a final fallback in ParseQualityName at
// `QualityParser.cs:589-595` after all other source-detection arms
// fail.
var otherSourceRe = regexp.MustCompile(`(?i)(?P<hdtv>HD[-_. ]TV)|(?P<sdtv>SD[-_. ]TV)`)

// otherSourceHDIdx + otherSourceSDIdx pre-compute SubexpIndex.
var (
	otherSourceHDIdx = otherSourceRe.SubexpIndex("hdtv")
	otherSourceSDIdx = otherSourceRe.SubexpIndex("sdtv")
)

// ClassifyOtherSource maps the C# `OtherSourceMatch` private helper
// at `QualityParser.cs:653-674` from a release name to a SourceTag —
// `SourceTagHDTV` for `HD-TV` shapes, `SourceTagSDTV` for `SD-TV`
// shapes, or `SourceTagUnknown` if neither matches.
//
// The C# helper returns `Quality.HDTV720p` / `Quality.SDTV` /
// `Quality.Unknown`; the Go split returns the SourceTag instead so
// the resolution-mapping decision lives in the (still-unported)
// ParseQualityName layer rather than being baked into this primitive.
func ClassifyOtherSource(name string) SourceTag {
	m := otherSourceRe.FindStringSubmatch(name)
	if m == nil {
		return SourceTagUnknown
	}
	if otherSourceHDIdx > 0 && otherSourceHDIdx < len(m) && m[otherSourceHDIdx] != "" {
		return SourceTagHDTV
	}
	if otherSourceSDIdx > 0 && otherSourceSDIdx < len(m) && m[otherSourceSDIdx] != "" {
		return SourceTagSDTV
	}
	return SourceTagUnknown
}
