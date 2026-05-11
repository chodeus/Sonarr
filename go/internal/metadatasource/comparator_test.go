package metadatasource

import (
	"slices"
	"testing"

	"sonarr.tv/sonarr/internal/series"
)

// seriesWith returns a *series.Series with the given Title and Year.
// year == 0 → leaves Year nil (mirroring the C# default-zero / Go-port
// nil distinction). year != 0 → sets Year via pointer.
func seriesWith(title string, year int) *series.Series {
	s := &series.Series{Title: title}
	if year != 0 {
		y := year
		s.Year = &y
	}
	return s
}

// sortedTitles returns the Title of each entry in input order so test
// assertions read top-to-bottom.
func sortedTitles(xs []*series.Series) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = x.Title
	}
	return out
}

// TestSearchSeriesComparer_FixtureCases ports the four cases from
// `SearchSeriesComparerFixture.cs:27-69`. Each case sorts a 2-element
// slice and asserts the predicted entry comes first.
func TestSearchSeriesComparer_FixtureCases(t *testing.T) {
	cases := []struct {
		name  string
		query string
		input []string // entered in this order; sort should reorder
		first string
	}{
		{
			name:  "prefer_the_walking_dead_over_talking_dead_for_the_walking_dead",
			query: "the walking dead",
			input: []string{"Talking Dead", "The Walking Dead"},
			first: "The Walking Dead",
		},
		{
			name:  "prefer_the_walking_dead_over_talking_dead_for_walking_dead",
			query: "walking dead",
			input: []string{"Talking Dead", "The Walking Dead"},
			first: "The Walking Dead",
		},
		{
			name:  "prefer_blacklist_over_the_blacklist_for_blacklist",
			query: "blacklist",
			input: []string{"The Blacklist", "Blacklist"},
			first: "Blacklist",
		},
		{
			name:  "prefer_the_blacklist_over_blacklist_for_the_blacklist",
			query: "the blacklist",
			input: []string{"Blacklist", "The Blacklist"},
			first: "The Blacklist",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items := make([]*series.Series, 0, len(tc.input))
			for _, title := range tc.input {
				items = append(items, seriesWith(title, 0))
			}
			cmp := NewComparator(tc.query)
			slices.SortStableFunc(items, cmp.Compare)
			if items[0].Title != tc.first {
				t.Errorf("first = %q, want %q (full order: %v)", items[0].Title, tc.first, sortedTitles(items))
			}
		})
	}
}

// TestSearchSeriesComparer_YearAware exercises arm 4 (year-aware
// cleanTitle Levenshtein) — when the query carries a year, candidates
// matching that year sort ahead of equally-edit-close peers.
func TestSearchSeriesComparer_YearAware(t *testing.T) {
	items := []*series.Series{
		seriesWith("Battlestar Galactica", 1978),
		seriesWith("Battlestar Galactica", 2003),
	}
	cmp := NewComparator("Battlestar Galactica 2003")
	slices.SortStableFunc(items, cmp.Compare)

	if items[0].Title != "Battlestar Galactica" {
		t.Fatalf("first.Title = %q, want Battlestar Galactica", items[0].Title)
	}
	if items[0].Year == nil || *items[0].Year != 2003 {
		t.Errorf("first.Year = %v, want 2003 (full order: %+v / years: [%d %d])",
			items[0].Year, sortedTitles(items),
			derefYear(items[0]), derefYear(items[1]))
	}
}

// TestSearchSeriesComparer_PrefixWithColon exercises arm 5 — the
// "CSI:" prefix pattern with year-aware disambiguation.
//
// Both candidates prefix-match "CSI:" so the year-aware tie-breaker
// fires. With no year in the query (yearKnown == false), the
// CompareWithYear fall-through runs `s.Year.CompareTo` — the OUTER
// Compare body's `-result` negation then flips it so the HIGHER
// year sorts first. That's the C# behaviour at
// `SearchSeriesComparer.cs:103` + outer negation at `:70-73`.
// Surprising-but-faithful: newer entries surface above older ones
// when the user gave no year hint.
func TestSearchSeriesComparer_PrefixWithColon(t *testing.T) {
	items := []*series.Series{
		seriesWith("CSI: Miami", 2002),
		seriesWith("CSI: NY", 2004),
	}
	cmp := NewComparator("CSI")
	slices.SortStableFunc(items, cmp.Compare)

	if items[0].Title != "CSI: NY" {
		t.Errorf("first.Title = %q, want CSI: NY (year-aware fallback prefers higher year — full order: %v)",
			items[0].Title, sortedTitles(items))
	}
}

// TestSearchSeriesComparer_ExactMatchWins exercises arm 1 — the
// exact-match arm takes precedence over all later arms. "House"
// must sort ahead of "House M.D." even though "House M.D." has a
// strong cleanTitle match.
func TestSearchSeriesComparer_ExactMatchWins(t *testing.T) {
	items := []*series.Series{
		seriesWith("House M.D.", 2004),
		seriesWith("House", 2004),
	}
	cmp := NewComparator("House")
	slices.SortStableFunc(items, cmp.Compare)

	if items[0].Title != "House" {
		t.Errorf("first.Title = %q, want House (full order: %v)",
			items[0].Title, sortedTitles(items))
	}
}

// TestSearchSeriesComparer_LevenshteinOne exercises arm 3 —
// single-typo tolerance via Levenshtein ≤ 1 on cleanPunctuation.
func TestSearchSeriesComparer_LevenshteinOne(t *testing.T) {
	items := []*series.Series{
		seriesWith("Brakingg Bad", 0), // edit distance 2
		seriesWith("Braking Bad", 0),  // edit distance 1 (typo)
	}
	cmp := NewComparator("breaking bad")
	slices.SortStableFunc(items, cmp.Compare)

	if items[0].Title != "Braking Bad" {
		t.Errorf("first.Title = %q, want Braking Bad (Lev ≤ 1 should win; full order: %v)",
			items[0].Title, sortedTitles(items))
	}
}

// TestSearchSeriesComparer_NilSafe exercises the nil-pointer arm
// — a nil entry should not panic and should sort after a real
// entry (since arms 1-5 evaluate to false for nil and arm 6's
// Levenshtein(empty, query) is large).
func TestSearchSeriesComparer_NilSafe(t *testing.T) {
	items := []*series.Series{
		nil,
		seriesWith("Real Title", 0),
	}
	cmp := NewComparator("Real Title")
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Compare panicked on nil entry: %v", r)
		}
	}()
	slices.SortStableFunc(items, cmp.Compare)

	if items[0] == nil || items[0].Title != "Real Title" {
		t.Errorf("first = %+v, want Real Title", items[0])
	}
}

// TestSearchSeriesComparer_EmptyQuery exercises the empty-query
// path — arms 1-5 trivially tie, arm 6 falls through to weighted
// Levenshtein where shorter titles win (less to edit-delete).
func TestSearchSeriesComparer_EmptyQuery(t *testing.T) {
	items := []*series.Series{
		seriesWith("Longer Title Here", 0),
		seriesWith("Short", 0),
	}
	cmp := NewComparator("")
	slices.SortStableFunc(items, cmp.Compare)

	if items[0].Title != "Short" {
		t.Errorf("first.Title = %q, want Short (empty-query falls to Lev distance; full order: %v)",
			items[0].Title, sortedTitles(items))
	}
}

// TestCleanHelpers covers the three normalisation helpers used by
// the comparator's predicate arms. Each table case is one specific
// behaviour the comparator depends on.
func TestCleanHelpers(t *testing.T) {
	t.Run("cleanPunctuation strips -._: and lowercases", func(t *testing.T) {
		got := cleanPunctuation("CSI: Miami - The Re.boot")
		want := "csi miami  the reboot"
		if got != want {
			t.Errorf("cleanPunctuation = %q, want %q", got, want)
		}
	})
	t.Run("cleanTitle strips country/year postfix and lowercases", func(t *testing.T) {
		cases := []struct {
			in   string
			want string
		}{
			{"Battlestar Galactica (2003)", "battlestar galactica"},
			{"The Office (US)", "the office"},
			{"The Office (US) (2005)", "the office"},
			{"The Office", "the office"},
			{"S.H.I.E.L.D.", "shield"},
		}
		for _, tc := range cases {
			if got := cleanTitle(tc.in); got != tc.want {
				t.Errorf("cleanTitle(%q) = %q, want %q", tc.in, got, tc.want)
			}
		}
	})
	t.Run("cleanArticles drops leading a/an/the", func(t *testing.T) {
		cases := []struct {
			in   string
			want string
		}{
			{"The Walking Dead", "walking dead"},
			{"A Discovery of Witches", "discovery of witches"},
			{"An American Werewolf", "american werewolf"},
			{"Walking Dead", "walking dead"},
		}
		for _, tc := range cases {
			if got := cleanArticles(tc.in); got != tc.want {
				t.Errorf("cleanArticles(%q) = %q, want %q", tc.in, got, tc.want)
			}
		}
	})
}

// TestNewComparator_ParsesYear exercises the constructor's
// year-extraction (reQueryYear). Three forms in C# regex:
//
//   - `"Title 2003"` → year=2003, queryWithoutYear="title"
//   - `"Title (2003)"` → year=2003, queryWithoutYear="title"
//   - `"Title"` → year=0, yearKnown=false, queryWithoutYear="title"
func TestNewComparator_ParsesYear(t *testing.T) {
	cases := []struct {
		query           string
		wantYearKnown   bool
		wantYear        int
		wantQueryNoYear string
	}{
		{"Battlestar Galactica 2003", true, 2003, "battlestar galactica"},
		{"Battlestar Galactica (2003)", true, 2003, "battlestar galactica"},
		{"Battlestar Galactica", false, 0, "battlestar galactica"},
		{"Show 2003 not at end", false, 0, "show 2003 not at end"},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			c := NewComparator(tc.query)
			if c.yearKnown != tc.wantYearKnown {
				t.Errorf("yearKnown = %v, want %v", c.yearKnown, tc.wantYearKnown)
			}
			if c.year != tc.wantYear {
				t.Errorf("year = %d, want %d", c.year, tc.wantYear)
			}
			if c.searchQueryWithoutYear != tc.wantQueryNoYear {
				t.Errorf("searchQueryWithoutYear = %q, want %q",
					c.searchQueryWithoutYear, tc.wantQueryNoYear)
			}
		})
	}
}

// TestYearFactor exercises the arm-6 year-proximity bonus.
//
//	exact-year match  → 20
//	off-by-one match  → 10
//	off-by-two match  →  0
//	no year in query  →  0
func TestYearFactor(t *testing.T) {
	cases := []struct {
		name      string
		query     string
		seriesYr  int
		wantBonus int
	}{
		{"exact_year_yields_20", "Show 2003", 2003, 20},
		{"off_by_one_yields_10", "Show 2003", 2002, 10},
		{"off_by_one_above_yields_10", "Show 2003", 2004, 10},
		{"off_by_two_yields_0", "Show 2003", 2005, 0},
		{"no_query_year_yields_0", "Show", 2003, 0},
		{"nil_series_year_offset_minus_query", "Show 2003", 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := NewComparator(tc.query)
			s := seriesWith("any", tc.seriesYr)
			if got := c.yearFactor(s); got != tc.wantBonus {
				t.Errorf("yearFactor(year=%d) with query=%q = %d, want %d",
					tc.seriesYr, tc.query, got, tc.wantBonus)
			}
		})
	}
}

// derefYear is a test helper for diagnostic messages.
func derefYear(s *series.Series) int {
	if s == nil || s.Year == nil {
		return 0
	}
	return *s.Year
}
