package episodeimport_test

import (
	"errors"
	"log/slog"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/series"
)

// --- stubs ---------------------------------------------------------

type fcStubBuilder struct {
	out  string
	err  error
	last *episodeimport.LocalEpisode
}

func (s *fcStubBuilder) BuildEpisodeFileName(le *episodeimport.LocalEpisode) (string, error) {
	s.last = le
	return s.out, s.err
}

type fcStubParser struct {
	// outByName maps the fileName arg to the formats returned. Lets
	// tests pin "renamed → A,B" / "raw basename → A" semantics.
	outByName map[string][]customformats.CustomFormat
	// fallback is returned when no entry matches. Lets simple tests
	// pin a single return without populating the map.
	fallback []customformats.CustomFormat
	calls    []fcParseCall
}

type fcParseCall struct {
	le       *episodeimport.LocalEpisode
	fileName string
}

func (s *fcStubParser) ParseCustomFormat(le *episodeimport.LocalEpisode, fileName string) []customformats.CustomFormat {
	s.calls = append(s.calls, fcParseCall{le: le, fileName: fileName})
	if v, ok := s.outByName[fileName]; ok {
		return v
	}
	return s.fallback
}

type fcStubScorer struct {
	// scores maps a join-of-format-IDs to a score, e.g.
	// "1,2" → 50. Tests pin per-call results.
	scores map[string]int
	calls  []fcScoreCall
}

type fcScoreCall struct {
	seriesID int
	fmtIDs   []int
}

func (s *fcStubScorer) CalculateCustomFormatScore(seriesID int, formats []customformats.CustomFormat) int {
	ids := make([]int, len(formats))
	for i, f := range formats {
		ids[i] = f.ID
	}
	s.calls = append(s.calls, fcScoreCall{seriesID: seriesID, fmtIDs: ids})
	key := joinIDs(ids)
	if v, ok := s.scores[key]; ok {
		return v
	}
	return 0
}

func joinIDs(ids []int) string {
	parts := make([]string, len(ids))
	for i, v := range ids {
		parts[i] = itoa(v)
	}
	return strings.Join(parts, ",")
}

func itoa(i int) string {
	// avoid strconv import for a 1-liner
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	out := ""
	for i > 0 {
		out = string(rune('0'+(i%10))) + out
		i /= 10
	}
	if neg {
		out = "-" + out
	}
	return out
}

// mkFC is a test helper that builds a calculator with the supplied
// stubs.
func mkFC(t *testing.T, b episodeimport.LocalEpisodeFileNameBuilder, p episodeimport.LocalEpisodeFormatParser, s episodeimport.LocalEpisodeFormatScorer) *episodeimport.LocalEpisodeFormatCalculator {
	t.Helper()
	c, err := episodeimport.NewLocalEpisodeFormatCalculator(b, p, s, slog.Default())
	if err != nil {
		t.Fatalf("NewLocalEpisodeFormatCalculator: %v", err)
	}
	return c
}

// --- constructor ---------------------------------------------------

func TestNewLocalEpisodeFormatCalculatorRequiredNils(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		want string
		args func() (b episodeimport.LocalEpisodeFileNameBuilder, p episodeimport.LocalEpisodeFormatParser, s episodeimport.LocalEpisodeFormatScorer)
	}{
		{
			name: "nil builder",
			want: "nil LocalEpisodeFileNameBuilder",
			args: func() (episodeimport.LocalEpisodeFileNameBuilder, episodeimport.LocalEpisodeFormatParser, episodeimport.LocalEpisodeFormatScorer) {
				return nil, &fcStubParser{}, &fcStubScorer{}
			},
		},
		{
			name: "nil parser",
			want: "nil LocalEpisodeFormatParser",
			args: func() (episodeimport.LocalEpisodeFileNameBuilder, episodeimport.LocalEpisodeFormatParser, episodeimport.LocalEpisodeFormatScorer) {
				return &fcStubBuilder{}, nil, &fcStubScorer{}
			},
		},
		{
			name: "nil scorer",
			want: "nil LocalEpisodeFormatScorer",
			args: func() (episodeimport.LocalEpisodeFileNameBuilder, episodeimport.LocalEpisodeFormatParser, episodeimport.LocalEpisodeFormatScorer) {
				return &fcStubBuilder{}, &fcStubParser{}, nil
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, p, s := tc.args()
			_, err := episodeimport.NewLocalEpisodeFormatCalculator(b, p, s, nil)
			if err == nil {
				t.Fatalf("expected error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}

func TestNewLocalEpisodeFormatCalculatorNilLogDefaults(t *testing.T) {
	t.Parallel()
	c, err := episodeimport.NewLocalEpisodeFormatCalculator(&fcStubBuilder{}, &fcStubParser{}, &fcStubScorer{}, nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if c == nil {
		t.Fatalf("nil calc")
	}
}

// --- ParseEpisodeCustomFormats -------------------------------------

func TestParseEpisodeCustomFormatsBuildsRenamedNameAndReturns(t *testing.T) {
	t.Parallel()

	wantFormats := []customformats.CustomFormat{{ID: 1, Name: "FormatA"}}
	b := &fcStubBuilder{out: "renamed.mkv"}
	p := &fcStubParser{outByName: map[string][]customformats.CustomFormat{
		"renamed.mkv": wantFormats,
	}}
	c := mkFC(t, b, p, &fcStubScorer{})

	le := episodeimport.NewLocalEpisode()
	le.Series = &series.Series{ID: 7}
	le.Path = "/in/raw.mkv"

	got := c.ParseEpisodeCustomFormats(le)
	if len(got) != 1 || got[0].Name != "FormatA" {
		t.Errorf("got %+v, want FormatA only", got)
	}
	if len(p.calls) != 1 || p.calls[0].fileName != "renamed.mkv" {
		t.Errorf("parser called %+v, want fileName=renamed.mkv", p.calls)
	}
}

func TestParseEpisodeCustomFormatsBuilderErrReturnsEmpty(t *testing.T) {
	t.Parallel()
	b := &fcStubBuilder{err: errors.New("boom")}
	p := &fcStubParser{fallback: []customformats.CustomFormat{{ID: 99}}}
	c := mkFC(t, b, p, &fcStubScorer{})

	got := c.ParseEpisodeCustomFormats(&episodeimport.LocalEpisode{Series: &series.Series{ID: 1}})
	if got != nil {
		t.Errorf("got %+v, want nil on builder err", got)
	}
	if len(p.calls) != 0 {
		t.Errorf("parser must not be called when builder errors")
	}
}

func TestParseEpisodeCustomFormatsNilLEReturnsNil(t *testing.T) {
	t.Parallel()
	c := mkFC(t, &fcStubBuilder{}, &fcStubParser{}, &fcStubScorer{})
	if got := c.ParseEpisodeCustomFormats(nil); got != nil {
		t.Errorf("got %+v, want nil", got)
	}
}

// --- UpdateEpisodeCustomFormats ------------------------------------

func TestUpdateEpisodeCustomFormatsHappyPathSetsAllFields(t *testing.T) {
	t.Parallel()

	renamedFormats := []customformats.CustomFormat{{ID: 1, Name: "RenamedHit"}}
	originalFormats := []customformats.CustomFormat{{ID: 2, Name: "OrigHit"}}

	b := &fcStubBuilder{out: "Renamed.S01E01.mkv"}
	p := &fcStubParser{outByName: map[string][]customformats.CustomFormat{
		"Renamed.S01E01.mkv": renamedFormats,
		"raw.mkv":            originalFormats,
	}}
	s := &fcStubScorer{scores: map[string]int{
		"1": 50,
		"2": 25,
	}}
	c := mkFC(t, b, p, s)

	le := episodeimport.NewLocalEpisode()
	le.Series = &series.Series{ID: 7}
	le.Path = "/some/folder/raw.mkv"

	c.UpdateEpisodeCustomFormats(le)

	if len(le.CustomFormats) != 1 || le.CustomFormats[0].Name != "RenamedHit" {
		t.Errorf("CustomFormats = %+v, want RenamedHit", le.CustomFormats)
	}
	if le.FileNameUsedForCustomFormatCalculation != "Renamed.S01E01.mkv" {
		t.Errorf("FileNameUsedForCustomFormatCalculation = %q", le.FileNameUsedForCustomFormatCalculation)
	}
	if le.CustomFormatScore != 50 {
		t.Errorf("CustomFormatScore = %d, want 50", le.CustomFormatScore)
	}
	if len(le.OriginalFileNameCustomFormats) != 1 || le.OriginalFileNameCustomFormats[0].Name != "OrigHit" {
		t.Errorf("OriginalFileNameCustomFormats = %+v", le.OriginalFileNameCustomFormats)
	}
	if le.OriginalFileNameCustomFormatScore != 25 {
		t.Errorf("OriginalFileNameCustomFormatScore = %d, want 25", le.OriginalFileNameCustomFormatScore)
	}

	// Both parser passes were invoked (renamed first, then raw basename).
	if len(p.calls) != 2 {
		t.Fatalf("parser calls = %d, want 2", len(p.calls))
	}
	if p.calls[0].fileName != "Renamed.S01E01.mkv" || p.calls[1].fileName != "raw.mkv" {
		t.Errorf("parser fileNames = [%q, %q], want [Renamed.S01E01.mkv, raw.mkv]",
			p.calls[0].fileName, p.calls[1].fileName)
	}

	// Both scorer passes invoked, with the matching seriesID.
	if len(s.calls) != 2 {
		t.Fatalf("scorer calls = %d, want 2", len(s.calls))
	}
	for i, sc := range s.calls {
		if sc.seriesID != 7 {
			t.Errorf("scorer[%d].seriesID = %d, want 7", i, sc.seriesID)
		}
	}
}

func TestUpdateEpisodeCustomFormatsBuilderErrSkipsRenamedHalfButRunsOriginal(t *testing.T) {
	t.Parallel()

	originalFormats := []customformats.CustomFormat{{ID: 5, Name: "OrigOnly"}}
	b := &fcStubBuilder{err: errors.New("token engine angry")}
	p := &fcStubParser{outByName: map[string][]customformats.CustomFormat{
		"raw.mkv": originalFormats,
	}}
	s := &fcStubScorer{scores: map[string]int{
		"5": 17,
	}}
	c := mkFC(t, b, p, s)

	le := episodeimport.NewLocalEpisode()
	le.Series = &series.Series{ID: 99}
	le.Path = "/in/raw.mkv"

	// Pre-populate to verify the failure path resets the renamed-half fields.
	le.CustomFormats = []customformats.CustomFormat{{ID: 999, Name: "stale"}}
	le.CustomFormatScore = 999
	le.FileNameUsedForCustomFormatCalculation = "stale.mkv"

	c.UpdateEpisodeCustomFormats(le)

	if le.CustomFormats != nil {
		t.Errorf("CustomFormats = %+v, want nil (reset on builder err)", le.CustomFormats)
	}
	if le.CustomFormatScore != 0 {
		t.Errorf("CustomFormatScore = %d, want 0", le.CustomFormatScore)
	}
	if le.FileNameUsedForCustomFormatCalculation != "" {
		t.Errorf("FileNameUsedForCustomFormatCalculation = %q, want empty", le.FileNameUsedForCustomFormatCalculation)
	}

	// Original-name pass STILL runs — that's the Go-port choice
	// (the C# version raises on builder error and never reaches `:39`).
	if len(le.OriginalFileNameCustomFormats) != 1 || le.OriginalFileNameCustomFormats[0].Name != "OrigOnly" {
		t.Errorf("OriginalFileNameCustomFormats = %+v, want OrigOnly (best-effort half should run)", le.OriginalFileNameCustomFormats)
	}
	if le.OriginalFileNameCustomFormatScore != 17 {
		t.Errorf("OriginalFileNameCustomFormatScore = %d, want 17", le.OriginalFileNameCustomFormatScore)
	}

	// Parser was called exactly once (for the original-basename pass).
	if len(p.calls) != 1 || p.calls[0].fileName != "raw.mkv" {
		t.Errorf("parser calls = %+v, want one call for raw.mkv", p.calls)
	}
}

func TestUpdateEpisodeCustomFormatsNilSeriesSkipsRenamedHalf(t *testing.T) {
	t.Parallel()
	b := &fcStubBuilder{out: "should not be called.mkv"}
	p := &fcStubParser{}
	s := &fcStubScorer{}
	c := mkFC(t, b, p, s)

	le := episodeimport.NewLocalEpisode()
	le.Path = "/in/x.mkv" // Series intentionally nil

	c.UpdateEpisodeCustomFormats(le)

	// Builder must not be called when Series is nil — without a
	// QualityProfile lookup, the score side has nothing to feed.
	if b.last != nil {
		t.Errorf("builder called with Series=nil; le=%+v", b.last)
	}
	// NewLocalEpisode pre-fills slice fields to empty (not nil), so
	// "untouched" means len==0 + zero score + empty filename.
	if len(le.CustomFormats) != 0 || le.CustomFormatScore != 0 || le.FileNameUsedForCustomFormatCalculation != "" {
		t.Errorf("CustomFormats half should be untouched: %+v / %d / %q",
			le.CustomFormats, le.CustomFormatScore, le.FileNameUsedForCustomFormatCalculation)
	}
}

func TestUpdateEpisodeCustomFormatsNilLEIsNoOp(t *testing.T) {
	t.Parallel()
	b := &fcStubBuilder{}
	p := &fcStubParser{}
	s := &fcStubScorer{}
	c := mkFC(t, b, p, s)

	c.UpdateEpisodeCustomFormats(nil) // must not panic

	if b.last != nil || len(p.calls) != 0 || len(s.calls) != 0 {
		t.Errorf("nil-LE should be a no-op; deps were called")
	}
}

func TestUpdateEpisodeCustomFormatsBasenameStripsDirectoryPrefix(t *testing.T) {
	t.Parallel()
	// path/filepath.Base on "/foo/bar/raw name.mkv" = "raw name.mkv"
	b := &fcStubBuilder{out: "doesn't matter"}
	p := &fcStubParser{}
	c := mkFC(t, b, p, &fcStubScorer{})

	le := episodeimport.NewLocalEpisode()
	le.Series = &series.Series{ID: 1}
	le.Path = "/library/Show/Season 01/raw name.mkv"

	c.UpdateEpisodeCustomFormats(le)

	// Two parser calls: [0] is renamed-pass with builder.out;
	// [1] is original-pass with basename.
	if len(p.calls) != 2 {
		t.Fatalf("parser calls = %d, want 2", len(p.calls))
	}
	if p.calls[1].fileName != "raw name.mkv" {
		t.Errorf("basename pass fileName = %q, want %q", p.calls[1].fileName, "raw name.mkv")
	}
}
