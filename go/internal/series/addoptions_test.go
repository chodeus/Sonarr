package series_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/series"
)

func TestDecodeAddOptions_EmptyVariants(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"", "  ", "null", " null "} {
		opts, err := series.DecodeAddOptions(in)
		if err != nil {
			t.Errorf("DecodeAddOptions(%q): unexpected error %v", in, err)
		}
		if opts != nil {
			t.Errorf("DecodeAddOptions(%q): want nil, got %#v", in, opts)
		}
	}
}

func TestDecodeAddOptions_MonitorNameForms(t *testing.T) {
	t.Parallel()
	cases := map[string]series.MonitorType{
		`{"monitor":"none"}`:                series.MonitorNone,
		`{"monitor":"None"}`:                series.MonitorNone, // case-insensitive read
		`{"monitor":"NONE"}`:                series.MonitorNone,
		`{"monitor":"all"}`:                 series.MonitorAll,
		`{"monitor":"firstSeason"}`:         series.MonitorFirstSeason,
		`{"monitor":"firstseason"}`:         series.MonitorFirstSeason,
		`{"monitor":"monitorSpecials"}`:     series.MonitorMonitorSpecials,
		`{"monitor":"unmonitorSpecials"}`:   series.MonitorUnmonitorSpecials,
		`{"monitor":"skip"}`:                series.MonitorSkip,
		`{"monitor":"future"}`:              series.MonitorFuture,
		`{"monitor":"unrecognised-future"}`: series.MonitorUnknown, // forward-compat
		`{}`:                                series.MonitorUnknown, // missing → zero
	}
	for blob, want := range cases {
		opts, err := series.DecodeAddOptions(blob)
		if err != nil {
			t.Errorf("DecodeAddOptions(%q): %v", blob, err)
			continue
		}
		if opts == nil {
			t.Errorf("DecodeAddOptions(%q): nil opts", blob)
			continue
		}
		if opts.Monitor != want {
			t.Errorf("DecodeAddOptions(%q): Monitor got %d, want %d", blob, opts.Monitor, want)
		}
	}
}

func TestDecodeAddOptions_MonitorIntegerForm(t *testing.T) {
	t.Parallel()
	// C# JsonStringEnumConverter(..., allowIntegerValues:true) accepts
	// the integer ordinal as a fallback. The Go-port decoder mirrors.
	opts, err := series.DecodeAddOptions(`{"monitor":12}`)
	if err != nil {
		t.Fatalf("DecodeAddOptions: %v", err)
	}
	if opts.Monitor != series.MonitorNone {
		t.Errorf("Monitor (int 12): got %d, want MonitorNone (%d)", opts.Monitor, series.MonitorNone)
	}
}

func TestDecodeAddOptions_OtherFields(t *testing.T) {
	t.Parallel()
	opts, err := series.DecodeAddOptions(
		`{"monitor":"all","searchForMissingEpisodes":true,"searchForCutoffUnmetEpisodes":false}`,
	)
	if err != nil {
		t.Fatalf("DecodeAddOptions: %v", err)
	}
	if !opts.SearchForMissingEpisodes {
		t.Error("SearchForMissingEpisodes: want true")
	}
	if opts.SearchForCutoffUnmetEpisodes {
		t.Error("SearchForCutoffUnmetEpisodes: want false")
	}
}

func TestDecodeAddOptions_Malformed(t *testing.T) {
	t.Parallel()
	_, err := series.DecodeAddOptions(`{not json`)
	if err == nil {
		t.Fatal("DecodeAddOptions(malformed): want error, got nil")
	}
}
