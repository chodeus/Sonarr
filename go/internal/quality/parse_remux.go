package quality

import "regexp"

// remuxRe is the C# `RemuxRegex` from `QualityParser.cs:66`:
//
//	(?:[_. ]|\d{4}p-|\bHybrid-)(?<remux>(?:(BD|UHD)[-_. ]?)?Remux)\b
//	|(?<remux>(?:(BD|UHD)[-_. ]?)?Remux[_. ]\d{4}p)
//
// The C# original captures the matched substring into a `(?<remux>...)`
// named group on each alternation arm — RE2 rejects duplicate named
// groups, so the Go port elides the names since the only consumer
// (`RemuxRegex.IsMatch(...)` at `QualityParser.cs:118`) checks
// match-success only and never reads the captured text.
//
// Branch A matches the prefix-anchored shape: a `_`/`.`/space, or
// a 4-digit-resolution `1080p-` lead, or `Hybrid-` literally, then
// optional `BD`/`UHD` separator, then `Remux` followed by a word
// boundary.
//
// Branch B matches the suffix-anchored shape: optional `BD`/`UHD`
// separator, `Remux`, separator, then 4-digit-`p` resolution.
//
// (?i) preserves the C# RegexOptions.IgnoreCase semantics, so
// `REMUX`, `remux`, and `Remux` all match equivalently.
var remuxRe = regexp.MustCompile(
	`(?i)(?:[_. ]|\d{4}p-|\bHybrid-)(?:(?:BD|UHD)[-_. ]?)?Remux\b` +
		`|(?:(?:BD|UHD)[-_. ]?)?Remux[_. ]\d{4}p`,
)

// IsRemux reports whether a release name advertises Remux source
// material (uncompressed Bluray/UHD source). Mirrors the C# bare
// `RemuxRegex.IsMatch(name)` at `QualityParser.cs:118` — the boolean
// is the only thing ParseQualityName consumes from this regex.
//
// True for names like `BluRay.1080p.AVC.DTS-HD.MA.5.1.REMUX-FraMeSToR`,
// `Bluray.Remux.AVC.AC3`, `BDRemux`, `UHD.BLURAY.REMUX.DV`,
// `Hybrid-REMUX`, `2160p Remux`, `Remux.1080p`. False for
// `[TMS-Remux].Series.Title` (a release-group token that uses Remux
// in its name but doesn't match either anchored shape) and
// `[RandomRemux] Series` (same reason).
func IsRemux(name string) bool {
	return remuxRe.MatchString(name)
}
