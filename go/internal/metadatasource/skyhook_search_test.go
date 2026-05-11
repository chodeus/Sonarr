package metadatasource

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/series"
)

// fakeSeriesLookup records GetByTvdbID calls and answers from a
// configured map. Setting `existing[id] = nil` makes the lookup
// return series.ErrNotFound for that id; a non-nil *series.Series
// returns the value.
type fakeSeriesLookup struct {
	existing map[int]*series.Series
	err      error // optional override for non-NotFound errors
	calls    atomic.Int32
}

func (f *fakeSeriesLookup) GetByTvdbID(_ context.Context, tvdbID int) (*series.Series, error) {
	f.calls.Add(1)
	if f.err != nil {
		return nil, f.err
	}
	if s, ok := f.existing[tvdbID]; ok && s != nil {
		return s, nil
	}
	return nil, series.ErrNotFound
}

// searchHandler is a handler matrix for the search arm — answers
// `/search/en/?term=...` with `body`, also forwards to fixtureHandler
// for `/shows/en/<id>/` when the prefix short-circuit cascades to
// GetSeriesInfo.
func searchHandler(t *testing.T, searchBody string, showsByID map[int]ShowResource) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/search/en/") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(searchBody))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/shows/en/") {
			parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
			if len(parts) < 3 {
				http.NotFound(w, r)
				return
			}
			id := 0
			_, _ = fmtSscan(parts[2], &id)
			show, ok := showsByID[id]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(show)
			return
		}
		http.NotFound(w, r)
	}
}

// fmtSscan is a tiny stand-in so the test file doesn't need to
// import fmt twice (the search handler does it inline).
func fmtSscan(s string, p *int) (int, error) {
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errors.New("not numeric")
		}
		*p = *p*10 + int(r-'0')
	}
	return 1, nil
}

func newProxyWithLookup(t *testing.T, srv *httptest.Server, lookup SeriesLookup) *Proxy {
	t.Helper()
	p, err := NewProxy(srv.Client(), srv.URL, nil, lookup, nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	return p
}

// TestProxy_SearchForNewSeries_FreeText round-trips a 2-result
// search response and asserts the proxy returns both mapped Series
// in order.
func TestProxy_SearchForNewSeries_FreeText(t *testing.T) {
	body := `[
		{"tvdbId":111,"title":"Breaking Bad","status":"ended"},
		{"tvdbId":222,"title":"Better Call Saul","status":"ended"}
	]`
	srv := httptest.NewServer(searchHandler(t, body, nil))
	defer srv.Close()

	p := newProxyWithLookup(t, srv, nil)
	results, err := p.SearchForNewSeries(context.Background(), "breaking bad")
	if err != nil {
		t.Fatalf("SearchForNewSeries: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	if results[0].TvdbID != 111 || results[1].TvdbID != 222 {
		t.Errorf("results = %+v", results)
	}
}

// TestProxy_SearchForNewSeries_EncodesTerm verifies the term is
// URL-encoded + lowercased + trimmed in the outgoing request.
func TestProxy_SearchForNewSeries_EncodesTerm(t *testing.T) {
	var seenQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	p := newProxyWithLookup(t, srv, nil)
	if _, err := p.SearchForNewSeries(context.Background(), "  Breaking Bad & Friends  "); err != nil {
		t.Fatalf("SearchForNewSeries: %v", err)
	}
	// Expected encoded form of "breaking bad & friends" (trimmed,
	// lowercased, query-escaped). url.QueryEscape encodes "&" as
	// "%26" and " " as "+".
	want := "term=breaking+bad+%26+friends"
	if seenQuery != want {
		t.Errorf("seenQuery = %q, want %q", seenQuery, want)
	}
}

// TestProxy_SearchForNewSeries_PathInjectionRejected verifies the
// path-injection guard returns ErrInvalidSearchTerm.
func TestProxy_SearchForNewSeries_PathInjectionRejected(t *testing.T) {
	cases := []string{
		"/etc/passwd",
		`\\server\share`,
		`C:\Windows\System32`,
		`c:/foo`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Errorf("server should not be hit for path-injection input")
	}))
	defer srv.Close()

	p := newProxyWithLookup(t, srv, nil)
	for _, tc := range cases {
		t.Run(tc, func(t *testing.T) {
			_, err := p.SearchForNewSeries(context.Background(), tc)
			if err == nil {
				t.Fatalf("err = nil for %q, want ErrInvalidSearchTerm", tc)
			}
			if !errors.Is(err, ErrInvalidSearchTerm) {
				t.Errorf("err = %v, want wraps ErrInvalidSearchTerm", err)
			}
		})
	}
}

// TestProxy_SearchForNewSeries_TvdbPrefixHitsLocalSeries: when a
// SeriesLookup is wired and the local DB has the row, the proxy
// returns the local Series without hitting the network.
func TestProxy_SearchForNewSeries_TvdbPrefixHitsLocalSeries(t *testing.T) {
	local := &series.Series{ID: 42, TvdbID: 81189, Title: "Breaking Bad (local copy)"}
	lookup := &fakeSeriesLookup{existing: map[int]*series.Series{81189: local}}

	var serverHit int32
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&serverHit, 1)
	}))
	defer srv.Close()

	p := newProxyWithLookup(t, srv, lookup)
	results, err := p.SearchForNewSeries(context.Background(), "tvdbid:81189")
	if err != nil {
		t.Fatalf("SearchForNewSeries: %v", err)
	}
	if len(results) != 1 || results[0].ID != 42 || results[0].Title != "Breaking Bad (local copy)" {
		t.Errorf("results = %+v, want [Breaking Bad (local copy)]", results)
	}
	if hit := atomic.LoadInt32(&serverHit); hit != 0 {
		t.Errorf("server hit %d times, want 0 (prefix short-circuit should not call SkyHook)", hit)
	}
	if calls := lookup.calls.Load(); calls != 1 {
		t.Errorf("lookup.calls = %d, want 1", calls)
	}
}

// TestProxy_SearchForNewSeries_TvdbPrefixFallsBackToGetSeriesInfo:
// when SeriesLookup misses (or is nil), the proxy falls back to
// GetSeriesInfo and returns the mapped Series.
func TestProxy_SearchForNewSeries_TvdbPrefixFallsBackToGetSeriesInfo(t *testing.T) {
	show := fixtureShow() // TvdbID 81189
	srv := httptest.NewServer(searchHandler(t, "[]", map[int]ShowResource{81189: show}))
	defer srv.Close()

	// No SeriesLookup → fall through directly to GetSeriesInfo.
	p := newProxyWithLookup(t, srv, nil)
	results, err := p.SearchForNewSeries(context.Background(), "tvdb:81189")
	if err != nil {
		t.Fatalf("SearchForNewSeries: %v", err)
	}
	if len(results) != 1 || results[0].TvdbID != 81189 {
		t.Errorf("results = %+v, want one entry with TvdbID 81189", results)
	}
	if results[0].Title != "Breaking Bad" {
		t.Errorf("Title = %q, want Breaking Bad (mapped fresh)", results[0].Title)
	}
}

// TestProxy_SearchForNewSeries_TvdbPrefixNotFoundReturnsEmpty: when
// GetSeriesInfo returns ErrSeriesNotFound, the search arm swallows
// it and returns []. Matches C# `:138-141`.
func TestProxy_SearchForNewSeries_TvdbPrefixNotFoundReturnsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p := newProxyWithLookup(t, srv, nil)
	results, err := p.SearchForNewSeries(context.Background(), "tvdbid:999")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if len(results) != 0 {
		t.Errorf("results = %+v, want empty []", results)
	}
}

// TestProxy_SearchForNewSeries_TvdbPrefixInvalidSlugs: each of the 5
// validation gates must produce an empty list with NO error (matches
// C# `:123-126`).
func TestProxy_SearchForNewSeries_TvdbPrefixInvalidSlugs(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{"empty_slug", "tvdb:"},
		{"whitespace_only", "tvdb:   "},
		{"non_numeric", "tvdb:abc"},
		{"zero", "tvdb:0"},
		{"negative", "tvdb:-5"},
		{"with_inner_whitespace", "tvdb:81 189"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Errorf("server should not be hit for invalid slug")
	}))
	defer srv.Close()

	p := newProxyWithLookup(t, srv, nil)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			results, err := p.SearchForNewSeries(context.Background(), tc.query)
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if len(results) != 0 {
				t.Errorf("results = %+v, want empty []", results)
			}
		})
	}
}

// TestProxy_SearchForNewSeries_LookupErrorPropagates: when the
// SeriesLookup returns a non-NotFound error (e.g. DB connection
// failure), the proxy wraps it as ErrServiceUnavailable instead of
// silently falling through to GetSeriesInfo.
func TestProxy_SearchForNewSeries_LookupErrorPropagates(t *testing.T) {
	lookup := &fakeSeriesLookup{err: errors.New("db connection refused")}
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Errorf("server should not be hit when local lookup hard-errors")
	}))
	defer srv.Close()

	p := newProxyWithLookup(t, srv, lookup)
	_, err := p.SearchForNewSeries(context.Background(), "tvdbid:81189")
	if err == nil {
		t.Fatal("err = nil, want ErrServiceUnavailable")
	}
	if !errors.Is(err, ErrServiceUnavailable) {
		t.Errorf("err = %v, want wraps ErrServiceUnavailable", err)
	}
}

// TestProxy_SearchForNewSeries_MapSearchResultUsesLookup: when a
// free-text search returns a TVDB id the local DB knows about, the
// proxy returns the local Series row (PROV-Q34 transparent
// library-hit semantics).
func TestProxy_SearchForNewSeries_MapSearchResultUsesLookup(t *testing.T) {
	body := `[{"tvdbId":111,"title":"Remote shape"}]`
	local := &series.Series{ID: 7, TvdbID: 111, Title: "LOCAL row wins"}
	lookup := &fakeSeriesLookup{existing: map[int]*series.Series{111: local}}

	srv := httptest.NewServer(searchHandler(t, body, nil))
	defer srv.Close()

	p := newProxyWithLookup(t, srv, lookup)
	results, err := p.SearchForNewSeries(context.Background(), "remote")
	if err != nil {
		t.Fatalf("SearchForNewSeries: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].ID != 7 || results[0].Title != "LOCAL row wins" {
		t.Errorf("results[0] = %+v, want local row", results[0])
	}
}

// TestProxy_SearchForNewSeries_ServerErrorMapsToServiceUnavailable.
func TestProxy_SearchForNewSeries_ServerErrorMapsToServiceUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	p := newProxyWithLookup(t, srv, nil)
	_, err := p.SearchForNewSeries(context.Background(), "anything")
	if err == nil {
		t.Fatal("err = nil, want ErrServiceUnavailable")
	}
	if !errors.Is(err, ErrServiceUnavailable) {
		t.Errorf("err = %v, want wraps ErrServiceUnavailable", err)
	}
}

// TestProxy_SearchForNewSeries_DecodeErrorMapsToServiceUnavailable.
func TestProxy_SearchForNewSeries_DecodeErrorMapsToServiceUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"not":"an array"`))
	}))
	defer srv.Close()

	p := newProxyWithLookup(t, srv, nil)
	_, err := p.SearchForNewSeries(context.Background(), "anything")
	if err == nil {
		t.Fatal("err = nil, want ErrServiceUnavailable")
	}
	if !errors.Is(err, ErrServiceUnavailable) {
		t.Errorf("err = %v, want wraps ErrServiceUnavailable", err)
	}
}

// TestProxy_SearchForNewSeriesByImdbID covers normalisation:
//
//   - valid bare-digits → routes through with imdb:tt-padded form;
//   - valid tt-prefixed → routes through;
//   - invalid → returns empty [] with no error (matches C# `:75-80`).
func TestProxy_SearchForNewSeriesByImdbID(t *testing.T) {
	var lastQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	p := newProxyWithLookup(t, srv, nil)

	t.Run("bare_digits_padded_to_tt_7digit", func(t *testing.T) {
		lastQuery = ""
		_, err := p.SearchForNewSeriesByImdbID(context.Background(), "903747")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		// expect imdb:tt0903747 in the URL-encoded term
		if !strings.Contains(lastQuery, "imdb%3Att0903747") {
			t.Errorf("lastQuery = %q, want contains imdb:tt0903747 (encoded)", lastQuery)
		}
	})

	t.Run("tt_prefixed_passes_through", func(t *testing.T) {
		lastQuery = ""
		_, err := p.SearchForNewSeriesByImdbID(context.Background(), "tt0903747")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if !strings.Contains(lastQuery, "imdb%3Att0903747") {
			t.Errorf("lastQuery = %q, want contains imdb:tt0903747", lastQuery)
		}
	})

	t.Run("invalid_returns_empty_no_error", func(t *testing.T) {
		lastQuery = "<unchanged>"
		results, err := p.SearchForNewSeriesByImdbID(context.Background(), "nope")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if len(results) != 0 {
			t.Errorf("results = %+v, want empty", results)
		}
		if lastQuery != "<unchanged>" {
			t.Errorf("server hit on invalid input — lastQuery = %q", lastQuery)
		}
	})
}

// TestProxy_TypedIdWrappers covers the AniList / Tmdb / MyAnimeList
// wrappers compose the right prefix.
func TestProxy_TypedIdWrappers(t *testing.T) {
	cases := []struct {
		name string
		call func(*Proxy) error
		want string
	}{
		{
			name: "anilist",
			call: func(p *Proxy) error {
				_, err := p.SearchForNewSeriesByAniListID(context.Background(), 99)
				return err
			},
			want: "anilist%3A99",
		},
		{
			name: "tmdb",
			call: func(p *Proxy) error {
				_, err := p.SearchForNewSeriesByTmdbID(context.Background(), 1396)
				return err
			},
			want: "tmdb%3A1396",
		},
		{
			name: "myanimelist",
			call: func(p *Proxy) error {
				_, err := p.SearchForNewSeriesByMyAnimeListID(context.Background(), 9876)
				return err
			},
			want: "mal%3A9876",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var lastQuery string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				lastQuery = r.URL.RawQuery
				_, _ = w.Write([]byte(`[]`))
			}))
			defer srv.Close()
			p := newProxyWithLookup(t, srv, nil)
			if err := tc.call(p); err != nil {
				t.Fatalf("err = %v", err)
			}
			if !strings.Contains(lastQuery, tc.want) {
				t.Errorf("lastQuery = %q, want contains %q", lastQuery, tc.want)
			}
		})
	}
}

// TestContainsWhitespace covers the slug-validation helper.
func TestContainsWhitespace(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"abc", false},
		{"", false},
		{"a b", true},
		{"a\tb", true},
		{"a\nb", true},
		{" ", true}, // NBSP — Unicode whitespace per unicode.IsSpace
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := containsWhitespace(tc.in); got != tc.want {
				t.Errorf("containsWhitespace(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestLooksLikePath covers the path-injection heuristic.
func TestLooksLikePath(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"normal title", false},
		{"the/walking/dead", false}, // contains slashes but not leading
		{"/etc/passwd", true},
		{"/", true},
		{`\\server\share`, true},
		{`\\`, true},
		{`C:\foo`, true},
		{`c:/foo`, true},
		{`Z:\bar`, true},
		{`C:foo`, false},    // no trailing slash
		{`1:\foo`, false},   // digit, not letter
		{`Co:\foo`, false},  // two letters
		{`\noslash`, false}, // single backslash
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := looksLikePath(tc.in); got != tc.want {
				t.Errorf("looksLikePath(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestUrlEncodeTerm covers the C# `title.ToLower().Trim()` parity +
// url.QueryEscape encoding.
func TestUrlEncodeTerm(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"breaking bad", "breaking+bad"},
		{"  Breaking Bad  ", "breaking+bad"},
		{"Marvel's S.H.I.E.L.D.", "marvel%27s+s.h.i.e.l.d."},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := urlEncodeTerm(tc.in); got != tc.want {
				t.Errorf("urlEncodeTerm(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
