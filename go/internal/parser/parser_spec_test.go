package parser_test

import (
	"reflect"
	"testing"

	"sonarr.tv/sonarr/internal/parser"
)

// Bulk fixture spec: every test case from the relevant
// `src/NzbDrone.Core.Test/ParserTests/*Fixture.cs` files lifted into
// a single Go table-driven test. Each row carries the C# fixture's
// expected outcome plus a `XFail` flag indicating whether the row is
// known to NOT match the current Go ParseTitle output (a future slice
// will move it from xfail to passing).
//
// The dispatcher reports four buckets:
//
//   - PASS         — XFail=false AND actual matches expected.
//   - XFAIL        — XFail=true  AND actual does NOT match expected.
//   - REGRESSION   — XFail=false AND actual does NOT match expected.
//                    Test fails. Indicates a previously-passing row
//                    just broke (e.g. a new rule shadowed an existing
//                    matcher and we forgot to reorder).
//   - NEWLY-PASSING — XFail=true AND actual matches expected. Test
//                    still passes but logs a reminder to flip XFail
//                    to false. Tracks fixture coverage gains over time.
//
// As the ParseTitle port expands, the xfail count should monotonically
// decrease. New regressions are caught immediately by `REGRESSION`.

// expected captures the C# fixture's `result.X.Should().Be(...)`
// assertions. Fields with their zero value mean "don't check"
// (so a row that asserts only AirDate can leave Season/Episodes empty
// and the dispatcher won't compare them).
type expected struct {
	Title    string
	Season   int
	Episodes []int
	Abs      []int
	AirDate  string
	Special  bool
	Mini     bool
	SplitEp  bool
	Daily    bool
}

type specRow struct {
	Source string    // fixture file name + identifying suffix
	Input  string    // ParseTitle input
	Want   *expected // nil means expect ParseTitle to return nil
	XFail  bool      // currently expected to NOT match
}

// Helpers to keep row literals one line each.

func se(input, title string, season int, episodes ...int) specRow {
	return specRow{Input: input, Want: &expected{Title: title, Season: season, Episodes: episodes}}
}
func sex(input, title string, season int, episodes ...int) specRow {
	return specRow{Input: input, Want: &expected{Title: title, Season: season, Episodes: episodes}, XFail: true}
}
func me(input, title string, season int, episodes []int) specRow {
	return specRow{Input: input, Want: &expected{Title: title, Season: season, Episodes: episodes}}
}
func mex(input, title string, season int, episodes []int) specRow {
	return specRow{Input: input, Want: &expected{Title: title, Season: season, Episodes: episodes}, XFail: true}
}
func daily(input, title, airdate string) specRow {
	return specRow{Input: input, Want: &expected{Title: title, AirDate: airdate, Daily: true}}
}
func dailyx(input, title, airdate string) specRow {
	return specRow{Input: input, Want: &expected{Title: title, AirDate: airdate, Daily: true}, XFail: true}
}
func mini(input, title string, episode int) specRow {
	// MiniSeriesEpisodeParserFixture asserts season=1 + episode +
	// title only — NOT IsMiniSeries. The Mini flag is asserted only
	// in tests that explicitly require it (currently none in the
	// ported fixture set).
	return specRow{Input: input, Want: &expected{Title: title, Season: 1, Episodes: []int{episode}}}
}
func minix(input, title string, episode int) specRow {
	return specRow{Input: input, Want: &expected{Title: title, Season: 1, Episodes: []int{episode}}, XFail: true}
}
func anime(input, title string, abs int) specRow {
	return specRow{Input: input, Want: &expected{Title: title, Abs: []int{abs}}}
}
func animex(input, title string, abs int) specRow {
	return specRow{Input: input, Want: &expected{Title: title, Abs: []int{abs}}, XFail: true}
}

func TestParserSpec(t *testing.T) {
	rows := allSpecRows()

	var pass, xfail, regression, newlyPassing int
	for i, row := range rows {
		got := parser.ParseTitle(row.Input)
		match := matchesExpected(got, row.Want)
		switch {
		case match && !row.XFail:
			pass++
		case !match && row.XFail:
			xfail++
		case !match && !row.XFail:
			regression++
			t.Errorf("REGRESSION row %d %q: input=%q\n  want=%s\n  got =%s",
				i, row.Source, row.Input, formatExpected(row.Want), formatActual(got))
		case match && row.XFail:
			newlyPassing++
			t.Logf("NEWLY-PASSING row %d %q: input=%q (flip XFail to false)", i, row.Source, row.Input)
		}
	}
	t.Logf("spec summary: pass=%d xfail=%d regression=%d newly-passing=%d (total=%d)",
		pass, xfail, regression, newlyPassing, len(rows))
}

func matchesExpected(got *parser.ParsedEpisodeInfo, want *expected) bool {
	if want == nil {
		return got == nil
	}
	if got == nil {
		return false
	}
	if want.Title != "" && got.SeriesTitle != want.Title {
		return false
	}
	if want.Season != 0 && got.SeasonNumber != want.Season {
		return false
	}
	if want.Episodes != nil && !reflect.DeepEqual(got.EpisodeNumbers, want.Episodes) {
		return false
	}
	if want.Abs != nil && !reflect.DeepEqual(got.AbsoluteEpisodeNumbers, want.Abs) {
		return false
	}
	if want.AirDate != "" && got.AirDate != want.AirDate {
		return false
	}
	if want.Special && !got.Special {
		return false
	}
	if want.Mini && !got.IsMiniSeries {
		return false
	}
	if want.SplitEp && !got.IsSplitEpisode {
		return false
	}
	if want.Daily && !got.IsDaily() {
		return false
	}
	return true
}

func formatExpected(e *expected) string {
	if e == nil {
		return "nil"
	}
	return formatEx(e.Title, e.Season, e.Episodes, e.Abs, e.AirDate, e.Special, e.Mini, e.SplitEp, e.Daily)
}

func formatActual(p *parser.ParsedEpisodeInfo) string {
	if p == nil {
		return "nil"
	}
	return formatEx(p.SeriesTitle, p.SeasonNumber, p.EpisodeNumbers, p.AbsoluteEpisodeNumbers, p.AirDate, p.Special, p.IsMiniSeries, p.IsSplitEpisode, p.IsDaily())
}

func formatEx(title string, season int, eps, abs []int, airdate string, special, mini, split, dailyflag bool) string {
	flags := ""
	if special {
		flags += " special"
	}
	if mini {
		flags += " mini"
	}
	if split {
		flags += " split"
	}
	if dailyflag {
		flags += " daily"
	}
	return "{title=" + quote(title) +
		" season=" + itoa(season) +
		" eps=" + intsString(eps) +
		" abs=" + intsString(abs) +
		" airdate=" + quote(airdate) +
		flags + "}"
}

func quote(s string) string {
	return "\"" + s + "\""
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func intsString(xs []int) string {
	if xs == nil {
		return "nil"
	}
	if len(xs) == 0 {
		return "[]"
	}
	s := "["
	for i, x := range xs {
		if i > 0 {
			s += ","
		}
		s += itoa(x)
	}
	return s + "]"
}
