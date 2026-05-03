package quality

import (
	"regexp"
	"strings"
)

// SourceTag classifies which source-detection branch of the C#
// `SourceRegex` matched a release name. Mirrors the 11 named capture
// groups at `QualityParser.cs:17-29` (bluray / webdl / webrip / hdtv
// / bdrip / brrip / dvd / dsr / pdtv / sdtv / tvrip). SourceTagUnknown
// is the no-match sentinel.
//
// Distinct from `QualitySource` (the persisted source-medium enum:
// Television / Web / Bluray / DVD / WebRip / TelevisionRaw / BlurayRaw).
// SourceTag is a parser-side classification — multiple SourceTag
// values map to the same QualitySource (e.g. SourceTagDSR / PDTV /
// SDTV / TVRip all map to Television; SourceTagBluray / BDRip / BRRip
// all map to Bluray). The decision matrix at `ParseQualityName`
// branches on the parser-side tag because each tag has different
// resolution-fallback rules.
type SourceTag int

const (
	// SourceTagUnknown is the no-match sentinel.
	SourceTagUnknown SourceTag = iota
	// SourceTagBluray covers `BluRay` / `Blu-Ray` / `HD-?DVD` / `BDMux` / bare `BD`.
	SourceTagBluray
	// SourceTagWebDL covers all 11 webdl-arm spellings (WEB-DL, AmazonHD, Netflix*HD, etc.).
	SourceTagWebDL
	// SourceTagWebRip covers `WebRip` / `Web-Rip` / `WEBMux`.
	SourceTagWebRip
	// SourceTagHDTV covers literal `HDTV`.
	SourceTagHDTV
	// SourceTagBDRip covers `BDRip` / `BDLight`.
	SourceTagBDRip
	// SourceTagBRRip covers `BRRip`.
	SourceTagBRRip
	// SourceTagDVD covers `DVD` / `DVDRip` / `NTSC` / `PAL` / `xvidvd`.
	SourceTagDVD
	// SourceTagDSR covers `WS[-_. ]DSR` / `DSR`.
	SourceTagDSR
	// SourceTagPDTV covers literal `PDTV`.
	SourceTagPDTV
	// SourceTagSDTV covers literal `SDTV`.
	SourceTagSDTV
	// SourceTagTVRip covers literal `TVRip`.
	SourceTagTVRip
)

// sourceRe is the C# `SourceRegex` rewritten for RE2.
//
// The C# original at `QualityParser.cs:17-30`:
//
//	\b(?:
//	  (?<bluray>BluRay|Blu-Ray|HD-?DVD|BDMux|BD(?!$))|
//	  (?<webdl>WEB[-_. ]DL(?:mux)?|WEBDL|AmazonHD|AmazonSD|iTunesHD|MaxdomeHD|NetflixU?HD|WebHD|HBOMaxHD|DisneyHD|[. ]WEB[. ](?:[xh][ .]?26[45]|AVC|HEVC|DDP?[ .]?5[. ]1)|[. ](?-i:WEB)$|(?:720|1080|2160)p[-. ]WEB[-. ]|[-. ]WEB[-. ](?:720|1080|2160)p|\b\s\/\sWEB\s\/\s\b|(?:AMZN|NF|DP)[. -]WEB[. -](?!Rip))|
//	  (?<webrip>WebRip|Web-Rip|WEBMux)|
//	  (?<hdtv>HDTV)|
//	  (?<bdrip>BDRip|BDLight)|
//	  (?<brrip>BRRip)|
//	  (?<dvd>DVD|DVDRip|NTSC|PAL|xvidvd)|
//	  (?<dsr>WS[-_. ]DSR|DSR)|
//	  (?<pdtv>PDTV)|
//	  (?<sdtv>SDTV)|
//	  (?<tvrip>TVRip)
//	)(?:\b|$|[ .])
//	flags: c, i, x
//
// Three RE2 reformulations:
//
//  1. RegexOptions.IgnorePatternWhitespace (`x`) — Go's RE2 has no
//     equivalent inline flag (REGEX-Q6 in analysis/parser/regex-corpus.md).
//     The Go literal is written as a single line with all formatting
//     whitespace removed; whitespace inside character classes (`[ .]`,
//     `[. ]`, `[. -]`, `[-_. ]`, `[-. ]`) is preserved verbatim
//     because the C# `x` flag only ignores whitespace OUTSIDE character
//     classes.
//
//  2. `BD(?!$)` — RE2 has no negative lookahead. The bare `BD` is
//     pulled into its own named group `blurayBD` so the post-match
//     filter in ParseSource can reject matches where this group's
//     captured text ends at the end of the input string. Functionally
//     equivalent to the C# lookahead because the surrounding regex is
//     anchored by `(?:\b|$|[ .])` after the named group, so a BD-at-end
//     position would otherwise be admitted by the boundary alone.
//
//  3. `(?:AMZN|NF|DP)[. -]WEB[. -](?!Rip)` — RE2 has no negative
//     lookahead. The AMZN/NF/DP arm is split into its own named group
//     `webdlamzn` so the post-match filter can reject matches where
//     the next 3 bytes after the captured group spell `Rip`
//     (case-insensitive). Both `webdl` and `webdlamzn` map to
//     SourceTagWebDL.
//
// `(?-i:WEB)` is preserved verbatim — Go's RE2 supports inline flag
// scoping and disabling, so this keeps the case-sensitive WEB$ branch
// (REGEX-Q7 verified by hand: `(?i)foo(?-i:WEB)$` matches `fooWEB` /
// `FOOWEB` but not `fooweb`).
var sourceRe = regexp.MustCompile(
	`(?i)\b(?:` +
		`(?P<bluray>BluRay|Blu-Ray|HD-?DVD|BDMux)` +
		`|(?P<blurayBD>BD)` +
		`|(?P<webdl>WEB[-_. ]DL(?:mux)?|WEBDL|AmazonHD|AmazonSD|iTunesHD|MaxdomeHD|NetflixU?HD|WebHD|HBOMaxHD|DisneyHD|[. ]WEB[. ](?:[xh][ .]?26[45]|AVC|HEVC|DDP?[ .]?5[. ]1)|[. ](?-i:WEB)$|(?:720|1080|2160)p[-. ]WEB[-. ]|[-. ]WEB[-. ](?:720|1080|2160)p|\b\s/\sWEB\s/\s\b)` +
		`|(?P<webdlamzn>(?:AMZN|NF|DP)[. -]WEB[. -])` +
		`|(?P<webrip>WebRip|Web-Rip|WEBMux)` +
		`|(?P<hdtv>HDTV)` +
		`|(?P<bdrip>BDRip|BDLight)` +
		`|(?P<brrip>BRRip)` +
		`|(?P<dvd>DVD|DVDRip|NTSC|PAL|xvidvd)` +
		`|(?P<dsr>WS[-_. ]DSR|DSR)` +
		`|(?P<pdtv>PDTV)` +
		`|(?P<sdtv>SDTV)` +
		`|(?P<tvrip>TVRip)` +
		`)(?:\b|$|[ .])`,
)

// sourceGroupOrder maps the regex's named groups (in declaration
// order, which matches the C# alternation order) to their SourceTag.
// `blurayBD` and `webdlamzn` are the RE2-rewrite splits — both map to
// the same SourceTag as their parent group.
var sourceGroupOrder = []struct {
	name string
	tag  SourceTag
}{
	{"bluray", SourceTagBluray},
	{"blurayBD", SourceTagBluray},
	{"webdl", SourceTagWebDL},
	{"webdlamzn", SourceTagWebDL},
	{"webrip", SourceTagWebRip},
	{"hdtv", SourceTagHDTV},
	{"bdrip", SourceTagBDRip},
	{"brrip", SourceTagBRRip},
	{"dvd", SourceTagDVD},
	{"dsr", SourceTagDSR},
	{"pdtv", SourceTagPDTV},
	{"sdtv", SourceTagSDTV},
	{"tvrip", SourceTagTVRip},
}

// sourceGroupIndex pre-computes the SubexpIndex for each named group.
var sourceGroupIndex = func() map[string]int {
	m := make(map[string]int, len(sourceGroupOrder))
	for _, g := range sourceGroupOrder {
		m[g.name] = sourceRe.SubexpIndex(g.name)
	}
	return m
}()

// ParseSource extracts the release's source tag from a name. Returns
// SourceTagUnknown when no source token is present.
//
// Mirrors the C# usage at `QualityParser.cs:114-115`:
//
//	var sourceMatches = SourceRegex.Matches(normalizedName);
//	var sourceMatch = sourceMatches.OfType<Match>().LastOrDefault();
//
// Two semantic preservations beyond pattern parity:
//
//  1. LAST match wins. Multiple source tokens in one release name
//     (e.g. `WEBRip` early + `HDTV` late) resolve to the rightmost
//     hit, mirroring `LastOrDefault`.
//
//  2. The two RE2 lookaround rewrites (BD-at-end, AMZN/NF/DP-WEB-Rip)
//     are filtered post-match: a match ending in either reformulated
//     branch is dropped from the candidate list before selecting the
//     last surviving match.
func ParseSource(name string) SourceTag {
	matches := sourceRe.FindAllStringSubmatchIndex(name, -1)
	if len(matches) == 0 {
		return SourceTagUnknown
	}

	blurayBDIdx := sourceGroupIndex["blurayBD"]
	webdlAmznIdx := sourceGroupIndex["webdlamzn"]

	var lastMatch []int
	var lastTag SourceTag
	for _, m := range matches {
		// blurayBD post-filter: bare BD must NOT end at end-of-string
		// (the C# `BD(?!$)` semantic). Group end == len(name) when
		// the captured BD is the trailing token; the outer regex's
		// `(?:\b|$|[ .])` consumes 0 bytes via `$` in that case.
		if blurayBDIdx > 0 && 2*blurayBDIdx+1 < len(m) && m[2*blurayBDIdx] != -1 {
			if m[2*blurayBDIdx+1] == len(name) {
				continue
			}
		}
		// webdlamzn post-filter: the captured `(AMZN|NF|DP)[. -]WEB[. -]`
		// must NOT be followed by `Rip` (case-insensitive). The C#
		// `(?!Rip)` semantic. Group end + 3 bytes lookahead.
		if webdlAmznIdx > 0 && 2*webdlAmznIdx+1 < len(m) && m[2*webdlAmznIdx] != -1 {
			groupEnd := m[2*webdlAmznIdx+1]
			if groupEnd+3 <= len(name) && strings.EqualFold(name[groupEnd:groupEnd+3], "Rip") {
				continue
			}
		}
		// Determine which tag this match corresponds to. Multiple
		// groups may have non-empty captures across the indexed slice
		// in pathological inputs, but RE2 alternation guarantees only
		// one named group from the outer alternation matches per
		// position — so the first non-empty wins, in declaration order.
		for _, g := range sourceGroupOrder {
			idx := sourceGroupIndex[g.name]
			if idx <= 0 || 2*idx+1 >= len(m) {
				continue
			}
			if m[2*idx] != -1 {
				lastMatch = m
				lastTag = g.tag
				break
			}
		}
	}
	if lastMatch == nil {
		return SourceTagUnknown
	}
	return lastTag
}
