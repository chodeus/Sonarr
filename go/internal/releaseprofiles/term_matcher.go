package releaseprofiles

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// TermMatcher infrastructure ports
// `src/NzbDrone.Core/Profiles/Releases/`:
//
//   - `TermMatchers/ITermMatcher.cs` (interface)
//   - `TermMatchers/CaseInsensitiveTermMatcher.cs` (substring match)
//   - `TermMatchers/RegexTermMatcher.cs` (regex match)
//   - `PerlRegexFactory.cs` (`/pattern/modifiers` parser)
//   - `TermMatcherService.cs` (cached factory)
//
// Used by the ReleaseProfile machinery to test whether a release
// title satisfies a Required / Ignored term. Each term is either a
// plain substring (case-insensitive) or a Perl-style regex literal
// (`/pattern/imsxn`). The factory dispatches based on the term
// shape; cache-on-first-use mirrors the C# `_matcherCache`.

// TermMatcher mirrors the C# `ITermMatcher` interface at
// `Profiles/Releases/TermMatchers/ITermMatcher.cs`. Implementations
// answer "does `value` match my term?" (IsMatch) and "if it does,
// what substring/group satisfied the match?" (MatchingTerm).
type TermMatcher interface {
	IsMatch(value string) bool
	MatchingTerm(value string) string
}

// caseInsensitiveMatcher ports
// `TermMatchers/CaseInsensitiveTermMatcher.cs`. Substring contains
// after lowercasing both sides. MatchingTerm returns the ORIGINAL
// (un-lowercased) term when matched — mirrors the C#
// `_originalTerm` field that stashes the input pre-ToLowerInvariant.
type caseInsensitiveMatcher struct {
	original string
	lower    string
}

// IsMatch reports whether the lowercased value contains the
// lowercased term.
func (m *caseInsensitiveMatcher) IsMatch(value string) bool {
	return strings.Contains(strings.ToLower(value), m.lower)
}

// MatchingTerm returns the original term (case preserved) when the
// term substring is present in value (case-insensitive). Returns
// empty string when no match.
func (m *caseInsensitiveMatcher) MatchingTerm(value string) string {
	if m.IsMatch(value) {
		return m.original
	}
	return ""
}

// regexMatcher ports `TermMatchers/RegexTermMatcher.cs`. Wraps a
// compiled *regexp.Regexp.
type regexMatcher struct {
	re *regexp.Regexp
}

// IsMatch reports whether the regex matches anywhere in value.
func (m *regexMatcher) IsMatch(value string) bool {
	return m.re.MatchString(value)
}

// MatchingTerm returns the matched substring (the regex's full
// match group), or empty string when no match. Mirrors the C#
// `_regex.Match(value).Value` semantic.
func (m *regexMatcher) MatchingTerm(value string) string {
	return m.re.FindString(value)
}

// perlRegexShapeRe matches the C# `_perlRegexFormat` regex at
// `PerlRegexFactory.cs:7`. A Perl-style term is `/pattern/modifiers`
// where modifiers is zero or more `[a-z]`. The pattern body can
// contain anything (including embedded `/`); since the regex is
// anchored at the OUTER `/`s only, an embedded `/` inside a pattern
// (e.g. `/foo\/bar/i`) would confuse this regex — the C# version
// has the same limitation, so the Go port preserves it verbatim.
var perlRegexShapeRe = regexp.MustCompile(`^/(?P<pattern>.*)/(?P<modifiers>[a-z]*)$`)

// tryParsePerlRegex ports `PerlRegexFactory.TryCreateRegex` at
// `:11-22`. Returns a compiled *regexp.Regexp on the Perl-style
// `/pattern/modifiers` shape, or nil + ok=false otherwise.
//
// Returns an error (instead of throwing like C#) when the modifiers
// contain unsupported characters. The C# version throws
// `ArgumentException` for unknown modifiers; the Go port surfaces
// the same condition as a typed error so callers can decide
// whether to fall back to the case-insensitive matcher or surface
// to the user.
func tryParsePerlRegex(term string) (*regexp.Regexp, bool, error) {
	m := perlRegexShapeRe.FindStringSubmatch(term)
	if m == nil {
		return nil, false, nil
	}
	pattern := m[perlRegexShapeRe.SubexpIndex("pattern")]
	modifiers := m[perlRegexShapeRe.SubexpIndex("modifiers")]

	flags, err := perlModifiersToInline(modifiers)
	if err != nil {
		return nil, true, err
	}
	full := flags + pattern
	re, err := regexp.Compile(full)
	if err != nil {
		return nil, true, fmt.Errorf("releaseprofiles: compile %q: %w", full, err)
	}
	return re, true, nil
}

// perlModifiersToInline maps C# `RegexOptions` flags to Go RE2
// inline-flag prefixes. Mirrors `PerlRegexFactory.GetOptions` at
// `:30-58`.
//
// Supported by both engines: `i` (case-insensitive), `m`
// (multi-line), `s` (single-line / dot-matches-newline).
//
// NOT supported by Go RE2:
//
//   - `x` (IgnorePatternWhitespace) — Go's `regexp` package has no
//     equivalent. Returns an error.
//   - `n` (ExplicitCapture) — no equivalent. Returns an error.
//
// Unknown modifiers always error.
func perlModifiersToInline(modifiers string) (string, error) {
	if modifiers == "" {
		return "", nil
	}
	var flags []byte
	for _, c := range modifiers {
		switch c {
		case 'i':
			flags = append(flags, 'i')
		case 'm':
			flags = append(flags, 'm')
		case 's':
			flags = append(flags, 's')
		case 'x':
			return "", fmt.Errorf("perl regex modifier 'x' (IgnorePatternWhitespace) is unsupported by Go RE2")
		case 'n':
			return "", fmt.Errorf("perl regex modifier 'n' (ExplicitCapture) is unsupported by Go RE2")
		default:
			return "", fmt.Errorf("unknown perl regex modifier: %q", c)
		}
	}
	return "(?" + string(flags) + ")", nil
}

// MatcherFor ports `TermMatcherService.CreateMatcherInternal` at
// `TermMatcherService.cs:34-43`. Returns a regex matcher when the
// term is a Perl-style `/pattern/modifiers` literal AND the regex
// compiles successfully; falls back to case-insensitive substring
// otherwise.
//
// Errors from `tryParsePerlRegex` are silently swallowed and the
// case-insensitive fallback is returned — mirrors the C# behaviour
// where a regex that throws at compile time would not propagate
// (the C# `TryCreateRegex` returns false, and the service falls
// through to the CaseInsensitiveTermMatcher branch).
func MatcherFor(term string) TermMatcher {
	re, looksPerl, err := tryParsePerlRegex(term)
	if err == nil && looksPerl && re != nil {
		return &regexMatcher{re: re}
	}
	return &caseInsensitiveMatcher{original: term, lower: strings.ToLower(term)}
}

// MatcherService ports `TermMatcherService.cs:11-46`. A
// concurrency-safe cache mapping term-string → TermMatcher so the
// regex compile cost is paid at most once per term.
//
// The C# version uses `ICached<ITermMatcher>` with a 24-hour TTL.
// The Go port uses a `sync.Map`-backed cache without expiration:
// terms come from persisted ReleaseProfile rows (bounded set,
// changes only on user edit) so an unbounded process-lifetime
// cache is fine in practice.
type MatcherService struct {
	cache sync.Map // map[string]TermMatcher
}

// NewMatcherService returns a fresh, empty MatcherService.
func NewMatcherService() *MatcherService { return &MatcherService{} }

// IsMatch ports `TermMatcherService.IsMatch` at `:18-21`.
func (s *MatcherService) IsMatch(term, value string) bool {
	return s.GetMatcher(term).IsMatch(value)
}

// MatchingTerm ports `TermMatcherService.MatchingTerm` at `:23-26`.
func (s *MatcherService) MatchingTerm(term, value string) string {
	return s.GetMatcher(term).MatchingTerm(value)
}

// GetMatcher returns the cached TermMatcher for `term`, building
// one on first access. Mirrors `TermMatcherService.GetMatcher` at
// `:28-31`. Concurrent first-access calls may briefly construct
// duplicate matchers; the second one is discarded by the
// `LoadOrStore` race-resolution.
func (s *MatcherService) GetMatcher(term string) TermMatcher {
	if v, ok := s.cache.Load(term); ok {
		return v.(TermMatcher)
	}
	m := MatcherFor(term)
	actual, _ := s.cache.LoadOrStore(term, m)
	return actual.(TermMatcher)
}
