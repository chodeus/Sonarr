package plex

import (
	"reflect"
	"testing"

	"sonarr.tv/sonarr/internal/importlists"
)

func TestFindGUID(t *testing.T) {
	t.Parallel()
	guids := []ItemGuid{
		{ID: "tvdb://12345"},
		{ID: "imdb://tt1234567"},
		{ID: "tmdb://98765"},
	}
	cases := []struct{ prefix, want string }{
		{"tvdb", "12345"},
		{"imdb", "tt1234567"},
		{"tmdb", "98765"},
		{"missing", ""},
	}
	for _, tc := range cases {
		t.Run(tc.prefix, func(t *testing.T) {
			t.Parallel()
			if got := findGUID(guids, tc.prefix); got != tc.want {
				t.Errorf("findGUID(%q) = %q, want %q", tc.prefix, got, tc.want)
			}
		})
	}
}

func TestParseWatchlistItems(t *testing.T) {
	t.Parallel()
	items := []WatchlistItem{
		{
			ID:    "rk1",
			Title: "Show A",
			Year:  2020,
			Guids: []ItemGuid{
				{ID: "tvdb://111"},
				{ID: "tmdb://222"},
				{ID: "imdb://tt333"},
			},
		},
		{
			ID:    "rk2",
			Title: "Show B",
			Year:  0,
			Guids: []ItemGuid{
				{ID: "imdb://tt444"},
			},
		},
		{
			ID:    "rk3",
			Title: "No guids",
			Year:  2024,
			Guids: nil,
		},
	}
	got := parseWatchlistItems(items)
	want := []importlists.ItemInfo{
		{TvdbID: 111, TmdbID: 222, ImdbID: "tt333", Title: "Show A", Year: 2020},
		{TvdbID: 0, TmdbID: 0, ImdbID: "tt444", Title: "Show B", Year: 0},
		{TvdbID: 0, TmdbID: 0, ImdbID: "", Title: "No guids", Year: 2024},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseWatchlistItems:\n got %+v\nwant %+v", got, want)
	}
}
