package quality

import "regexp"

// Resolution mirrors the C# `Resolution` enum at
// `QualityParser.cs:714-724`. Integer values are the pixel height
// the resolution canonically refers to; `Unknown` is 0. Persisted
// in passing only — the actual stored value is the resolution
// surface of the surrounding Quality (see Quality.Resolution).
type Resolution int

const (
	// ResolutionUnknown is the no-detection sentinel.
	ResolutionUnknown Resolution = 0
	// Resolution360p covers 360p sources.
	Resolution360p Resolution = 360
	// Resolution480p covers 480p / 480i / 640x480 / 848x480.
	Resolution480p Resolution = 480
	// Resolution540p covers 540p sources.
	Resolution540p Resolution = 540
	// Resolution576p covers 576p sources.
	Resolution576p Resolution = 576
	// Resolution720p covers 720p / 1280x720 / 960p.
	Resolution720p Resolution = 720
	// Resolution1080p covers 1080p / 1920x1080 / 1440p / FHD / 1080i / 4kto1080p.
	Resolution1080p Resolution = 1080
	// Resolution2160p covers 2160p / 3840x2160 / 4k variants / UHD / [4K].
	Resolution2160p Resolution = 2160
)

// resolutionRe is the C# `ResolutionRegex` from `QualityParser.cs:49`.
// Each branch has a UNIQUE group name (R360p..R2160p), so the
// regex compiles directly under RE2 — these are not duplicate
// named groups even though the spelling looks similar to the
// duplicate-name issue in VersionRegex (which had multiple
// `(?<version>...)` groups).
var resolutionRe = regexp.MustCompile(
	`(?i)\b(?:` +
		`(?P<R360p>360p)` +
		`|(?P<R480p>480p|480i|640x480|848x480)` +
		`|(?P<R540p>540p)` +
		`|(?P<R576p>576p)` +
		`|(?P<R720p>720p|1280x720|960p)` +
		`|(?P<R1080p>1080p|1920x1080|1440p|FHD|1080i|4kto1080p)` +
		`|(?P<R2160p>2160p|3840x2160|4k[-_. ](?:UHD|HEVC|BD|H265)|(?:UHD|HEVC|BD|H265)[-_. ]4k)` +
		`)\b`,
)

// alternativeResolutionRe ports the C# `AlternativeResolutionRegex`
// at `QualityParser.cs:53` for releases that don't carry an
// explicit resolution: a bare `UHD` token or a `[4K]` literal
// implies 2160p. The C# original uses two `(?<R2160p>...)` groups
// with the SAME name (one of only a handful of duplicate-named
// groups in the parser corpus per analysis/parser/regex-corpus.md);
// RE2 rejects duplicate names, so the Go port elides the names —
// any match in this regex maps to Resolution2160p.
var alternativeResolutionRe = regexp.MustCompile(`(?i)\bUHD\b|\[4K\]`)

// resolutionGroupOrder is the priority order ParseResolution scans
// the resolutionRe match groups. Mirrors the C# group-success check
// chain at `QualityParser.cs:610-643` (R360p → R480p → R540p →
// R576p → R720p → R1080p → R2160p). The order is meaningful only
// if more than one group could ever match a single match, which RE2
// alternation prevents — but the ordering preserves byte-equivalence
// with the C# code on regex-engine quirks.
var resolutionGroupOrder = []struct {
	name string
	res  Resolution
}{
	{"R360p", Resolution360p},
	{"R480p", Resolution480p},
	{"R540p", Resolution540p},
	{"R576p", Resolution576p},
	{"R720p", Resolution720p},
	{"R1080p", Resolution1080p},
	{"R2160p", Resolution2160p},
}

// resolutionGroupIndex pre-computes the SubexpIndex for each named
// group so ParseResolution doesn't recompute on every call.
var resolutionGroupIndex = func() map[string]int {
	m := make(map[string]int, len(resolutionGroupOrder))
	for _, g := range resolutionGroupOrder {
		m[g.name] = resolutionRe.SubexpIndex(g.name)
	}
	return m
}()

// ParseResolution extracts the release's resolution from a name.
// Mirrors `QualityParser.ParseResolution` at `QualityParser.cs:600-651`:
//
//  1. Try the main `ResolutionRegex`. The first non-empty named
//     group, scanned in priority order R360p → R2160p, returns its
//     mapped Resolution.
//  2. Fall back to `AlternativeResolutionRegex` (`UHD` or `[4K]`);
//     a match implies 2160p.
//  3. Otherwise return ResolutionUnknown.
func ParseResolution(name string) Resolution {
	if m := resolutionRe.FindStringSubmatch(name); m != nil {
		for _, g := range resolutionGroupOrder {
			idx := resolutionGroupIndex[g.name]
			if idx > 0 && idx < len(m) && m[idx] != "" {
				return g.res
			}
		}
	}
	if alternativeResolutionRe.MatchString(name) {
		return Resolution2160p
	}
	return ResolutionUnknown
}
