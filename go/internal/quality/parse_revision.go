package quality

import (
	"regexp"
	"strconv"
	"strings"
)

// ParseRevision extracts proper / repack / version / real metadata
// from a release name. Mirrors the revision-detection portion of the
// C# `QualityParser.ParseQualityModifiers` at
// `QualityParser.cs:675-708` — the standalone slice that determines
// `Revision.Version`, `Revision.IsRepack`, and `Revision.Real`
// without touching source / resolution / codec detection (those
// land in subsequent commits as the rest of QualityParser is
// ported).
//
// Algorithm, exactly matching the C#:
//
//  1. Trim the name; build `normalized = name.Replace('_', ' ').Trim()`.
//  2. Run VersionRegex against the normalized name. If matched, the
//     captured version int is the starting Revision.Version and
//     `foundVersion` is true.
//  3. Run ProperRegex (\bproper\b, case-insensitive) against the
//     normalized name. If matched: Revision.Version becomes
//     `foundVersion ? capturedVersion+1 : 2`.
//  4. Run RepackRegex (\b(repack\d?|rerip\d?)\b, case-insensitive)
//     against the normalized name. If matched: Revision.Version
//     becomes `foundVersion ? capturedVersion+1 : 2`, AND
//     Revision.IsRepack becomes true. NOTE: when both Proper and
//     Repack match, the C# code sets IsRepack=true but the Version
//     was already bumped by ProperRegex; the Repack branch
//     re-bumps it — so a release tagged both "PROPER" and "REPACK"
//     with no v-tag becomes Version=2, IsRepack=true (both
//     branches independently set Version=2; the bump is idempotent
//     when foundVersion is false).
//  5. Run RealRegex (\bREAL\b, case-SENSITIVE — note no IgnoreCase
//     in the C# declaration) against the ORIGINAL name (not
//     normalized). Revision.Real = match count.
//
// Default Revision.Version is 1 (matches the C# `new Revision()`
// constructor default); Real defaults to 0; IsRepack defaults to
// false.
func ParseRevision(name string) Revision {
	name = strings.TrimSpace(name)
	normalized := strings.TrimSpace(strings.ReplaceAll(name, "_", " "))

	rev := NewRevision()

	capturedVersion, foundVersion := parseVersionTag(normalized)
	if foundVersion {
		rev.Version = capturedVersion
	}
	if properRe.MatchString(normalized) {
		if foundVersion {
			rev.Version = capturedVersion + 1
		} else {
			rev.Version = 2
		}
	}
	if repackRe.MatchString(normalized) {
		if foundVersion {
			rev.Version = capturedVersion + 1
		} else {
			rev.Version = 2
		}
		rev.IsRepack = true
	}

	// RealRegex matches against the ORIGINAL (not normalized) name and
	// is case-SENSITIVE — the C# RealRegex declaration omits
	// RegexOptions.IgnoreCase. Count of matches = Revision.Real (so
	// "REAL.PROPER REAL" → Real=2). The C# carries a TODO at
	// QualityParser.cs:700-701 noting this is over-eager; preserved
	// for byte-equivalence.
	rev.Real = len(realRe.FindAllStringIndex(name, -1))

	return rev
}

// properRe matches `\bproper\b` case-insensitive. Used by
// ParseRevision step 3.
var properRe = regexp.MustCompile(`(?i)\bproper\b`)

// repackRe matches `\b(repack\d?|rerip\d?)\b` case-insensitive. Used
// by ParseRevision step 4.
var repackRe = regexp.MustCompile(`(?i)\b(?:repack\d?|rerip\d?)\b`)

// realRe matches `\bREAL\b` case-SENSITIVE. Used by ParseRevision
// step 5.
var realRe = regexp.MustCompile(`\bREAL\b`)

// versionRe is the C# `VersionRegex` rewritten with unique-named
// groups so RE2 can compile it. The C# original uses duplicate
// `(?<version>\d)` group names across all 5 alternation branches —
// RE2 rejects duplicate named captures, so each branch gets its own
// group name `v1`..`v5` and the helper below scans for the first
// non-empty.
//
// C# original:
//
//	\d[-._ ]?v(?<version>\d)[-._ ]
//	|\[v(?<version>\d)\]
//	|repack(?<version>\d)
//	|rerip(?<version>\d)
//	|(?:480|576|720|1080|2160)p[._ ]v(?<version>\d)
//
// Go rewrite uses identical alternation order so leftmost-match
// semantics produce the same captured digit for any input.
var versionRe = regexp.MustCompile(
	`(?i)\d[-._ ]?v(?P<v1>\d)[-._ ]` +
		`|\[v(?P<v2>\d)\]` +
		`|repack(?P<v3>\d)` +
		`|rerip(?P<v4>\d)` +
		`|(?:480|576|720|1080|2160)p[._ ]v(?P<v5>\d)`,
)

// parseVersionTag returns the captured version number and whether
// any of the 5 branches matched. Matching multiple groups: only one
// can be non-empty per match (alternation), so we scan for the
// first non-empty.
func parseVersionTag(s string) (int, bool) {
	m := versionRe.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	for i := 1; i < len(m); i++ {
		if m[i] == "" {
			continue
		}
		n, err := strconv.Atoi(m[i])
		if err != nil {
			continue
		}
		return n, true
	}
	return 0, false
}
