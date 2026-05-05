package series_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/series"
)

func TestMatchType_Ordinals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		got  series.MatchType
		want int
	}{
		{"Unknown", series.MatchTypeUnknown, 0},
		{"Title", series.MatchTypeTitle, 1},
		{"Alias", series.MatchTypeAlias, 2},
		{"ID", series.MatchTypeID, 3},
	}
	for _, tc := range cases {
		if int(tc.got) != tc.want {
			t.Errorf("MatchType%s = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
}

func TestNewFindResult_StoresFields(t *testing.T) {
	t.Parallel()
	s := &series.Series{Title: "Test"}
	r := series.NewFindResult(s, series.MatchTypeAlias)
	if r.Series != s {
		t.Errorf("Series mismatch: %v vs %v", r.Series, s)
	}
	if r.MatchType != series.MatchTypeAlias {
		t.Errorf("MatchType mismatch: %v", r.MatchType)
	}
}

func TestNewFindResult_AllowsNilSeries(t *testing.T) {
	t.Parallel()
	r := series.NewFindResult(nil, series.MatchTypeUnknown)
	if r.Series != nil {
		t.Errorf("expected nil Series for no-match: %v", r.Series)
	}
}
