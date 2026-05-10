package httpclient

import "testing"

// Mirrors src/NzbDrone.Common.Test/Http/HttpUriFixture.cs cases for the
// `+` operator and CombinePath. The C# fixture uses NUnit TestCase rows;
// we transcribe them as table-driven Go subtests.

func TestParseURI_Roundtrip(t *testing.T) {
	cases := []string{
		"https://example.com",
		"https://example.com/api/v1?key=val#frag",
		"http://localhost:8989/sonarr",
		"",
	}
	for _, in := range cases {
		got, err := ParseURI(in)
		if err != nil {
			t.Fatalf("ParseURI(%q) errored: %v", in, err)
		}
		if got.String() != in {
			t.Fatalf("ParseURI(%q).String() = %q, want %q", in, got.String(), in)
		}
	}
}

func TestURI_PartGetters(t *testing.T) {
	u := MustParseURI("https://example.com:8443/foo/bar?a=1#frag")
	if u.Scheme() != "https" {
		t.Errorf("Scheme: %q", u.Scheme())
	}
	if u.Host() != "example.com" {
		t.Errorf("Host: %q", u.Host())
	}
	port, ok := u.Port()
	if !ok || port != 8443 {
		t.Errorf("Port: %d %v", port, ok)
	}
	if u.Path() != "/foo/bar" {
		t.Errorf("Path: %q", u.Path())
	}
	if u.Query() != "a=1" {
		t.Errorf("Query: %q", u.Query())
	}
	if u.Fragment() != "frag" {
		t.Errorf("Fragment: %q", u.Fragment())
	}
}

func TestURI_PortAbsentReturnsFalse(t *testing.T) {
	u := MustParseURI("https://example.com/foo")
	if _, ok := u.Port(); ok {
		t.Errorf("expected no port, got one")
	}
}

func TestCombinePath_Static(t *testing.T) {
	cases := []struct {
		base, rel, want string
	}{
		{"/a/b", "c", "/a/b/c"},
		{"/a/b/", "c", "/a/b/c"},
		{"/a/b", "/c", "/a/b/c"},
		{"/a/b/", "/c", "/a/b/c"},
		{"", "c", "c"},
		{"/a/b", "", "/a/b"},
		{"/a/b", "  ", "/a/b"}, // whitespace-only relative collapses to base
	}
	for _, c := range cases {
		got := CombinePath(c.base, c.rel)
		if got != c.want {
			t.Errorf("CombinePath(%q,%q) = %q, want %q", c.base, c.rel, got, c.want)
		}
	}
}

func TestURI_Join_RelativePathSemantics(t *testing.T) {
	// HttpUri.cs:158-178 — combineRelativePath uses LastIndexOf('/') so
	// merging "/a/b/c" with "x" yields "/a/b/x", NOT "/a/b/c/x" (RFC 3986
	// path-merge would do the same, so this is a sanity check).
	base := MustParseURI("https://example.com/a/b/c")
	rel := MustParseURI("x")
	got := base.Join(rel)
	if got.String() != "https://example.com/a/b/x" {
		t.Errorf("Join: got %q, want https://example.com/a/b/x", got.String())
	}
}

func TestURI_Join_RelativeWithLeadingSlash(t *testing.T) {
	base := MustParseURI("https://example.com/a/b/c")
	rel := MustParseURI("/x/y")
	got := base.Join(rel)
	if got.String() != "https://example.com/x/y" {
		t.Errorf("Join: got %q, want https://example.com/x/y", got.String())
	}
}

func TestURI_Join_AbsoluteOverridesScheme(t *testing.T) {
	base := MustParseURI("https://example.com/a")
	rel := MustParseURI("http://other.example/b")
	got := base.Join(rel)
	if got.String() != "http://other.example/b" {
		t.Errorf("Join with absolute rel: got %q, want http://other.example/b", got.String())
	}
}

func TestURI_Join_RelativeHostKeepsBaseScheme(t *testing.T) {
	// HttpUri.cs:265-268 — when relative has Host but no Scheme, base.Scheme
	// is preserved and the rest comes from relative.
	base := MustParseURI("https://example.com/a")
	rel := MustParseURI("//other.example/b?q=1")
	got := base.Join(rel)
	if got.String() != "https://other.example/b?q=1" {
		t.Errorf("Join host-only rel: got %q", got.String())
	}
}

func TestURI_Join_OnlyQueryOrFragment(t *testing.T) {
	// HttpUri.cs:274-275 — when relative has neither path/host/scheme, the
	// query/fragment from relative replace the base's query/fragment, and the
	// path is retained.
	base := MustParseURI("https://example.com/a/b?old=1#oldfrag")
	rel := MustParseURI("?new=2")
	got := base.Join(rel)
	if got.String() != "https://example.com/a/b?new=2" {
		t.Errorf("Join query-only: got %q", got.String())
	}
}

func TestURI_AddQueryParam_AppendsAndEscapes(t *testing.T) {
	u := MustParseURI("https://example.com/api?existing=1")
	u2 := u.AddQueryParam("key with space", "v&val")
	want := "https://example.com/api?existing=1&key+with+space=v%26val"
	if u2.String() != want {
		t.Errorf("AddQueryParam: got %q, want %q", u2.String(), want)
	}
}

func TestURI_AddQueryParams_OrderPreserved(t *testing.T) {
	u := MustParseURI("https://example.com/api")
	u2 := u.AddQueryParams([]QueryPair{{"a", "1"}, {"b", "2"}})
	if u2.String() != "https://example.com/api?a=1&b=2" {
		t.Errorf("AddQueryParams: got %q", u2.String())
	}
}

func TestURI_SetQueryReplaces(t *testing.T) {
	u := MustParseURI("https://example.com/api?old=1")
	u2 := u.SetQuery("new=2")
	if u2.String() != "https://example.com/api?new=2" {
		t.Errorf("SetQuery: got %q", u2.String())
	}
}

func TestURI_CombinePath_Method(t *testing.T) {
	u := MustParseURI("https://example.com/api?keep=1")
	u2 := u.CombinePath("episode/42")
	want := "https://example.com/api/episode/42?keep=1"
	if u2.String() != want {
		t.Errorf("CombinePath method: got %q, want %q", u2.String(), want)
	}
}

func TestURI_Equal_StringCompare(t *testing.T) {
	a := MustParseURI("https://example.com/a")
	b := MustParseURI("https://example.com/a")
	c := MustParseURI("https://example.com/A") // case-sensitive per C# string compare
	if !a.Equal(b) {
		t.Error("expected a==b")
	}
	if a.Equal(c) {
		t.Error("expected a!=c (case differs)")
	}
}
