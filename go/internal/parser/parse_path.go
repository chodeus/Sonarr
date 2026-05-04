package parser

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ParsePath ports `Parser.ParsePath` at `Parser.cs:569-640`. The
// path-aware wrapper around ParseTitle: tries the filename first,
// then several fallback combinations involving the parent directory.
//
// Strategies attempted in order (mirrors C# control flow):
//
//  1. ParseTitle(fileName).
//  2. Season-folder + episode-number combination: when the filename
//     matches SimpleEpisodeNumberRegex AND the parent folder matches
//     SeasonFolderRegex AND (strategy-1 result is nil OR mini OR has
//     absolute episodes), build a synthetic
//     `S<season>E<episode>(-E<last>)? <remaining>` title and re-parse.
//  3. Numeric-only-filename narrowing: when filename-without-ext is
//     a pure integer, parse the directory name; if its abs/episode
//     list contains the integer, narrow to that single value.
//  4. ParseTitle(dirName + " " + fileName).
//  5. ParseTitle(dirName + ext).
//
// Returns nil when every strategy fails.
//
// Path semantics: `filepath.Base` / `filepath.Dir` / `filepath.Ext`
// are platform-aware (`/` on Unix, `\` on Windows). Test fixtures
// targeting both conventions should normalise via `filepath.Clean`
// or use the platform's native separator.
func ParsePath(path string) *ParsedEpisodeInfo {
	fileName := filepath.Base(path)
	result := ParseTitle(fileName)

	dir := filepath.Dir(path)
	dirName := ""
	if dir != "" && dir != "." {
		dirName = filepath.Base(dir)
		if dirName == "." {
			dirName = ""
		}
	}

	// Strategy 2: season-folder + simple-episode-number combo.
	// The C# guard at `Parser.cs:577` is:
	//   episodeNumberMatch.Success
	//     && fileInfo.Directory?.Name != null
	//     && (result == null || result.IsMiniSeries || result.AbsoluteEpisodeNumbers.Any())
	if dirName != "" {
		if first, last, remaining, ok := parseSimpleEpisodeNumber(fileName); ok {
			if result == nil || result.IsMiniSeries || len(result.AbsoluteEpisodeNumbers) > 0 {
				if season, ok := parseSeasonFolder(dirName); ok {
					pathTitle := fmt.Sprintf("S%sE%02d", season, first)
					if first != last {
						pathTitle += fmt.Sprintf("-E%02d", last)
					}
					if remaining != "" {
						pathTitle += " " + remaining
					}
					if r := ParseTitle(pathTitle); r != nil {
						return r
					}
				}
			}
		}
	}

	// Strategy 3: numeric-only-filename narrowing.
	if result == nil && dirName != "" {
		fileNameNoExt := strings.TrimSuffix(fileName, filepath.Ext(fileName))
		if num, err := strconv.Atoi(fileNameNoExt); err == nil {
			dirResult := ParseTitle(dirName)
			if dirResult != nil {
				if containsInt(dirResult.AbsoluteEpisodeNumbers, num) {
					dirResult.AbsoluteEpisodeNumbers = []int{num}
					return dirResult
				}
				if containsInt(dirResult.EpisodeNumbers, num) {
					dirResult.EpisodeNumbers = []int{num}
					return dirResult
				}
			}
		}
	}

	if result != nil {
		return result
	}
	if dirName == "" {
		return nil
	}

	if r := ParseTitle(dirName + " " + fileName); r != nil {
		return r
	}

	ext := filepath.Ext(fileName)
	return ParseTitle(dirName + ext)
}

// seasonFolderRe ports `Parser.SeasonFolderRegex` at `Parser.cs:544`:
//
//	^(?:S|Season|Saison|Series|Stagione)[-_. ]*(?<season>(?<!\d+)\d{1,4}(?!\d+))(?:[_. ]+(?!\d+)|$)
//
// RE2 reformulation: the C# `(?<!\d+)` lookbehind on the season
// digit-run is automatically satisfied — the regex is `^`-anchored
// and the character immediately before `\d{1,4}` is matched by
// `[-_. ]*` (zero or more separators) which yields either start-of-
// string, a separator char, or the trailing letter of the prefix —
// none of which are digits.
//
// The first `(?!\d+)` (right after the season digits) is enforced
// by greedy matching — `\d{1,4}` consumes all consecutive digits
// up to the limit, so the position immediately after is by
// construction either non-digit or beyond the limit (handled by the
// post-match check below).
//
// The second `(?!\d+)` (right after the trailing separators) is
// applied as a post-match check on the byte at `m[1]`: if it's a
// digit, reject. This guards against folder names like `Season 1.5`
// where `1` is followed by `.5`.
//
// Captured group `season` is a string of 1-4 digits.
var seasonFolderRe = regexp.MustCompile(`(?i)^(?:S|Season|Saison|Series|Stagione)[-_. ]*(?P<season>\d{1,4})(?:[_. ]+|$)`)

// parseSeasonFolder reports whether folder is a recognised season
// folder. Returns the season-number STRING (preserving any leading
// zeros — the C# code stitches it back into the synthetic title
// verbatim) and a true/false ok flag. Mirrors the C#
// `SeasonFolderRegex.Match(...).Groups["season"].Value` lookup at
// `Parser.cs:579-581`.
func parseSeasonFolder(folder string) (string, bool) {
	m := seasonFolderRe.FindStringSubmatchIndex(folder)
	if m == nil {
		return "", false
	}
	// Post-match check enforcing the C# `(?!\d+)` lookahead at the
	// trailing position: byte after the regex match must not be a
	// digit. Rejects `Season 1.5` and similar.
	if m[1] < len(folder) {
		c := folder[m[1]]
		if c >= '0' && c <= '9' {
			return "", false
		}
	}
	idx := seasonFolderRe.SubexpIndex("season")
	return folder[m[2*idx]:m[2*idx+1]], true
}

// simpleEpisodeNumberRe ports `Parser.SimpleEpisodeNumberRegex` at
// `Parser.cs:545`:
//
//	^[ex]?(?<episode>(?<!\d+)\d{1,3}(?!\d+))
//	  (?:[ex-](?<episode>(?<!\d+)\d{1,3}(?!\d+)))?
//	  (?:[_. ](?!\d+)(?<remaining>.+)|$)
//
// Two RE2 reformulations vs the C# literal:
//
//  1. Duplicate `(?<episode>...)` named groups — RE2 forbids. The
//     Go port renames to `episode1` and `episode2`; the consumer
//     reconstructs `first` from `episode1` and `last` from
//     `episode2` (falling back to `first` when `episode2` didn't
//     capture). Mirrors the C# `Captures.First()` / `.Last()`
//     semantics on the (single) repeating named group.
//
//  2. The three `(?<!\d+)` lookbehinds + three `(?!\d+)` lookaheads
//     all become AUTOMATICALLY enforced by surrounding context:
//     - First lookbehind is at `^[ex]?\d{1,3}` — preceding char is
//     either start-of-string or `[ex]`, never a digit.
//     - Second lookbehind is after `[ex-]` — preceding char is
//     `[ex-]`, never a digit.
//     - Each lookahead `(?!\d+)` sits after `\d{1,3}`. If the actual
//     digit run is 4+ chars, `\d{1,3}` consumes 3, then the
//     surrounding context (`[ex-]` or `[_. ]` or `$`) requires a
//     non-digit — which is impossible if the 4th char is a digit
//     — so the whole regex fails to match. Equivalent behaviour
//     byte-for-byte.
//     - The trailing `(?<remaining>.+)` lookahead `(?!\d+)` is the
//     ONE that needs explicit reformulation: the trailing arm is
//     rewritten as `[_. ](?P<remaining>[^\d].*)` so the captured
//     remaining starts with a non-digit byte.
var simpleEpisodeNumberRe = regexp.MustCompile(
	`(?i)^[ex]?(?P<episode1>\d{1,3})` +
		`(?:[ex-](?P<episode2>\d{1,3}))?` +
		`(?:[_. ](?P<remaining>[^\d].*)|$)`,
)

// parseSimpleEpisodeNumber reports whether name matches
// SimpleEpisodeNumberRegex. Returns (first, last, remaining, ok)
// where first/last are episode numbers (last == first when the
// optional second group is absent) and remaining is the trailing
// text after the episode number(s) and a separator (or "" if the
// match ends at end-of-string).
func parseSimpleEpisodeNumber(name string) (int, int, string, bool) {
	// SimpleEpisodeNumberRegex is supposed to run on the bare
	// filename (no extension) per `Parser.cs:575`'s `fileInfo.Name`
	// — but `fileInfo.Name` actually INCLUDES the extension. The
	// regex's trailing alternation `[_. ]<remaining>|$` will
	// happily slurp `.mkv` into `<remaining>` if no other text is
	// present; for fixture rows like `01 Pilot (1080p HD).mkv`,
	// `<remaining>` becomes `Pilot (1080p HD).mkv` which is fine —
	// the synthetic title gets re-parsed by ParseTitle which strips
	// the `.mkv` via RemoveFileExtension.
	m := simpleEpisodeNumberRe.FindStringSubmatch(name)
	if m == nil {
		return 0, 0, "", false
	}
	ep1Idx := simpleEpisodeNumberRe.SubexpIndex("episode1")
	ep2Idx := simpleEpisodeNumberRe.SubexpIndex("episode2")
	remIdx := simpleEpisodeNumberRe.SubexpIndex("remaining")

	first, err := strconv.Atoi(m[ep1Idx])
	if err != nil {
		return 0, 0, "", false
	}
	last := first
	if m[ep2Idx] != "" {
		l, err := strconv.Atoi(m[ep2Idx])
		if err == nil {
			last = l
		}
	}
	return first, last, m[remIdx], true
}

func containsInt(xs []int, n int) bool {
	for _, x := range xs {
		if x == n {
			return true
		}
	}
	return false
}
