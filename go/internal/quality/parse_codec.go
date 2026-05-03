package quality

import "regexp"

// Codec is the video codec a release name advertises. Mirrors the
// 5-branch capture-group set of the C# `CodecRegex` at
// `QualityParser.cs:56`. CodecUnknown is the no-detection sentinel.
//
// The codec value influences ParseQualityName's branching for two
// edge cases: bluray + (xvid|divx) → forced Bluray480p; bare x264
// with no source/resolution → SDTV (see `QualityParser.cs:131-135`
// and `:499-503`). Other call sites of CodecRegex on the C# side
// use the bare regex for these branches only — there's no public
// `ParseCodec` method on the C# class, but exposing one here keeps
// the regex slice independently testable on the same shape as
// ParseRevision / ParseResolution before the full ParseQualityName
// decision tree lands.
type Codec int

const (
	// CodecUnknown is the no-detection sentinel.
	CodecUnknown Codec = iota
	// CodecX264 covers the `x264` token.
	CodecX264
	// CodecH264 covers the `h264` token.
	CodecH264
	// CodecXvidHD covers the `XvidHD` token. Listed BEFORE Xvid in
	// the C# alternation so `XvidHD` doesn't get partial-matched as
	// plain Xvid.
	CodecXvidHD
	// CodecXvid covers the `Xvid` token.
	CodecXvid
	// CodecDivX covers the `divx` token.
	CodecDivX
)

// codecRe is the C# `CodecRegex` from `QualityParser.cs:56`:
//
//	\b(?:(?<x264>x264)|(?<h264>h264)|(?<xvidhd>XvidHD)|(?<xvid>Xvid)|(?<divx>divx))\b
//
// Each branch has a unique group name, so RE2 compiles directly with
// no rewrite. The (?i) inline flag matches the C# RegexOptions.IgnoreCase.
//
// Branch ORDER matters: `XvidHD` MUST precede `Xvid` so `XvidHD` is
// captured by the `xvidhd` group rather than the `xvid` group. RE2's
// leftmost-match-wins semantics on alternation preserve this.
var codecRe = regexp.MustCompile(
	`(?i)\b(?:` +
		`(?P<x264>x264)` +
		`|(?P<h264>h264)` +
		`|(?P<xvidhd>XvidHD)` +
		`|(?P<xvid>Xvid)` +
		`|(?P<divx>divx)` +
		`)\b`,
)

// codecGroupOrder is the priority order ParseCodec scans the codecRe
// match groups. The order mirrors the C# alternation order so a
// release that contains tokens for multiple codec branches resolves
// to the same codec value as the C# regex would expose via
// `Match.Groups[X].Success` checks; in practice only one branch
// matches per leftmost match because RE2 alternation is exclusive.
var codecGroupOrder = []struct {
	name  string
	codec Codec
}{
	{"x264", CodecX264},
	{"h264", CodecH264},
	{"xvidhd", CodecXvidHD},
	{"xvid", CodecXvid},
	{"divx", CodecDivX},
}

// codecGroupIndex pre-computes the SubexpIndex for each named group
// so ParseCodec doesn't recompute on every call.
var codecGroupIndex = func() map[string]int {
	m := make(map[string]int, len(codecGroupOrder))
	for _, g := range codecGroupOrder {
		m[g.name] = codecRe.SubexpIndex(g.name)
	}
	return m
}()

// ParseCodec extracts the release's video codec from a name. Returns
// CodecUnknown when no codec token is present.
//
// Mirrors the bare regex usage at `QualityParser.cs:117` (the codec
// match feeding ParseQualityName's bluray/sdtv branches). The
// non-greedy leftmost-match semantics of RE2 produce the same
// captured group as the C# `CodecRegex.Match(name)` against any
// input shared between the two engines.
func ParseCodec(name string) Codec {
	m := codecRe.FindStringSubmatch(name)
	if m == nil {
		return CodecUnknown
	}
	for _, g := range codecGroupOrder {
		idx := codecGroupIndex[g.name]
		if idx > 0 && idx < len(m) && m[idx] != "" {
			return g.codec
		}
	}
	return CodecUnknown
}
