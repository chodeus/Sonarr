package releaseprofiles

import "testing"

func TestCaseInsensitiveMatcher(t *testing.T) {
	m := MatcherFor("HDTV")
	if !m.IsMatch("Series.S01E01.HDTV.x264-LOL") {
		t.Errorf("expected substring match")
	}
	if !m.IsMatch("series.s01e01.hdtv.x264-lol") {
		t.Errorf("expected case-insensitive match")
	}
	if m.IsMatch("Series.S01E01.WEB-DL.x264-LOL") {
		t.Errorf("did not expect match")
	}
	if got := m.MatchingTerm("Series.S01E01.HDTV.x264-LOL"); got != "HDTV" {
		t.Errorf("MatchingTerm = %q, want HDTV (the original cased term)", got)
	}
	if got := m.MatchingTerm("Series.WEB-DL"); got != "" {
		t.Errorf("MatchingTerm on no-match should be empty, got %q", got)
	}
}

func TestRegexMatcher_PerlSyntax(t *testing.T) {
	m := MatcherFor(`/HDTV[-_. ]?720p/i`)
	if !m.IsMatch("Series.S01E01.HDTV-720p.x264-LOL") {
		t.Errorf("expected regex match (with separator)")
	}
	if !m.IsMatch("Series.S01E01.HDTV.720p.x264-LOL") {
		t.Errorf("expected regex match (with dot)")
	}
	if m.IsMatch("Series.S01E01.HDTV.x264-LOL") {
		t.Errorf("did not expect match (no resolution)")
	}
	if got := m.MatchingTerm("HDTV-720p"); got != "HDTV-720p" {
		t.Errorf("MatchingTerm = %q, want HDTV-720p", got)
	}
}

func TestRegexMatcher_CaseInsensitiveModifier(t *testing.T) {
	m := MatcherFor(`/hdtv/i`)
	if !m.IsMatch("Series.HDTV.x264") {
		t.Errorf("expected case-insensitive regex match via /i flag")
	}
}

func TestRegexMatcher_FallsBackOnCompileError(t *testing.T) {
	// `(unclosed` is a Perl-style literal `/`(unclosed/`` but the
	// regex doesn't compile. tryParsePerlRegex returns an error;
	// MatcherFor falls back to case-insensitive substring.
	m := MatcherFor(`/(unclosed/`)
	if _, ok := m.(*caseInsensitiveMatcher); !ok {
		t.Errorf("expected caseInsensitiveMatcher fallback, got %T", m)
	}
	// Substring match still works on the literal `/(unclosed/`.
	if !m.IsMatch("foo/(unclosed/bar") {
		t.Errorf("fallback substring match should work")
	}
}

func TestRegexMatcher_FallsBackOnUnsupportedModifier(t *testing.T) {
	// `x` modifier is unsupported by Go RE2; perlModifiersToInline
	// errors and MatcherFor falls back.
	m := MatcherFor(`/foo/x`)
	if _, ok := m.(*caseInsensitiveMatcher); !ok {
		t.Errorf("expected caseInsensitiveMatcher fallback, got %T", m)
	}
}

func TestNonPerlSyntaxIsCaseInsensitive(t *testing.T) {
	// Term without Perl framing → case-insensitive substring.
	m := MatcherFor("WEB-DL")
	if _, ok := m.(*caseInsensitiveMatcher); !ok {
		t.Errorf("plain term should produce caseInsensitiveMatcher, got %T", m)
	}
}

func TestPerlRegex_AllModifiers(t *testing.T) {
	cases := []struct {
		modifiers string
		wantErr   bool
	}{
		{"", false},
		{"i", false},
		{"m", false},
		{"s", false},
		{"ims", false},
		{"x", true}, // unsupported by Go RE2
		{"n", true}, // unsupported
		{"q", true}, // unknown
	}
	for _, tc := range cases {
		t.Run(tc.modifiers, func(t *testing.T) {
			_, err := perlModifiersToInline(tc.modifiers)
			if (err != nil) != tc.wantErr {
				t.Errorf("perlModifiersToInline(%q) wantErr=%v, got err=%v",
					tc.modifiers, tc.wantErr, err)
			}
		})
	}
}

func TestMatcherService_Caches(t *testing.T) {
	svc := NewMatcherService()
	first := svc.GetMatcher("HDTV")
	second := svc.GetMatcher("HDTV")
	if first != second {
		t.Errorf("GetMatcher should return the same matcher across calls")
	}

	if !svc.IsMatch("HDTV", "Series.HDTV.x264") {
		t.Errorf("IsMatch wrapper should delegate to the cached matcher")
	}
	if got := svc.MatchingTerm("HDTV", "Series.HDTV.x264"); got != "HDTV" {
		t.Errorf("MatchingTerm = %q, want HDTV", got)
	}
}

func TestMatcherService_DifferentTermsCachedSeparately(t *testing.T) {
	svc := NewMatcherService()
	hdtv := svc.GetMatcher("HDTV")
	webdl := svc.GetMatcher("WEB-DL")
	if hdtv == webdl {
		t.Errorf("distinct terms should produce distinct matchers")
	}
}
