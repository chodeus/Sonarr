package importlists_test

import (
	"reflect"
	"testing"

	"sonarr.tv/sonarr/internal/importlists"
)

// Dedup mirrors C# `releases.DistinctBy(r => new { r.Title, r.TvdbId, r.ImdbId })`
// at ImportListBase.cs:87 — first occurrence wins, input order
// preserved.
func TestDedup(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   []importlists.ItemInfo
		want []importlists.ItemInfo
	}{
		{
			name: "empty",
			in:   nil,
			want: []importlists.ItemInfo{},
		},
		{
			name: "no_duplicates",
			in: []importlists.ItemInfo{
				{Title: "A", TvdbID: 1, ImdbID: "tt1"},
				{Title: "B", TvdbID: 2, ImdbID: "tt2"},
			},
			want: []importlists.ItemInfo{
				{Title: "A", TvdbID: 1, ImdbID: "tt1"},
				{Title: "B", TvdbID: 2, ImdbID: "tt2"},
			},
		},
		{
			name: "exact_duplicate_drops_second",
			in: []importlists.ItemInfo{
				{Title: "A", TvdbID: 1, ImdbID: "tt1", Year: 2020},
				{Title: "A", TvdbID: 1, ImdbID: "tt1", Year: 2021},
			},
			want: []importlists.ItemInfo{
				{Title: "A", TvdbID: 1, ImdbID: "tt1", Year: 2020}, // first wins
			},
		},
		{
			name: "differs_by_year_only_is_not_dedup",
			in: []importlists.ItemInfo{
				{Title: "A", TvdbID: 1, ImdbID: "tt1"},
				{Title: "A", TvdbID: 2, ImdbID: "tt1"}, // tvdb differs
			},
			want: []importlists.ItemInfo{
				{Title: "A", TvdbID: 1, ImdbID: "tt1"},
				{Title: "A", TvdbID: 2, ImdbID: "tt1"},
			},
		},
		{
			name: "imdb_distinguishes",
			in: []importlists.ItemInfo{
				{Title: "A", TvdbID: 0, ImdbID: "tt1"},
				{Title: "A", TvdbID: 0, ImdbID: "tt2"},
			},
			want: []importlists.ItemInfo{
				{Title: "A", TvdbID: 0, ImdbID: "tt1"},
				{Title: "A", TvdbID: 0, ImdbID: "tt2"},
			},
		},
		{
			name: "preserves_input_order",
			in: []importlists.ItemInfo{
				{Title: "C", TvdbID: 3},
				{Title: "A", TvdbID: 1},
				{Title: "B", TvdbID: 2},
				{Title: "A", TvdbID: 1}, // dup of #2
			},
			want: []importlists.ItemInfo{
				{Title: "C", TvdbID: 3},
				{Title: "A", TvdbID: 1},
				{Title: "B", TvdbID: 2},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := importlists.Dedup(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Dedup(%+v) =\n  %+v\nwant\n  %+v", tc.in, got, tc.want)
			}
		})
	}
}

// FetchResult is a plain value struct; pin its zero-value shape so
// future refactors that add fields surface as a test breakage.
func TestFetchResult_Zero(t *testing.T) {
	t.Parallel()
	var r importlists.FetchResult
	if r.AnyFailure {
		t.Errorf("zero FetchResult.AnyFailure: got true want false")
	}
	if len(r.Series) != 0 {
		t.Errorf("zero FetchResult.Series: got len=%d want 0", len(r.Series))
	}
}
