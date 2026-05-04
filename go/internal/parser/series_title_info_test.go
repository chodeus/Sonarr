package parser

import (
	"reflect"
	"testing"
)

func TestGetSeriesTitleInfo_YearInTitle(t *testing.T) {
	cases := []struct {
		name            string
		title           string
		wantTitle       string
		wantWithoutYear string
		wantYear        int
	}{
		{
			name:            "title with year",
			title:           "Series Title 2004",
			wantTitle:       "Series Title 2004",
			wantWithoutYear: "Series Title",
			wantYear:        2004,
		},
		{
			name:            "title without year",
			title:           "Series Title",
			wantTitle:       "Series Title",
			wantWithoutYear: "Series Title",
			wantYear:        0,
		},
		{
			name:            "title with bracketed year",
			title:           "Series Title (2004)",
			wantTitle:       "Series Title (2004)",
			wantWithoutYear: "Series Title",
			wantYear:        2004,
		},
		{
			name:            "title with bracketed year via dot separator",
			title:           "Series.Title.2004",
			wantTitle:       "Series.Title.2004",
			wantWithoutYear: "Series.Title",
			wantYear:        2004,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := GetSeriesTitleInfo(tc.title)
			if got.Title != tc.wantTitle {
				t.Errorf("Title = %q, want %q", got.Title, tc.wantTitle)
			}
			if got.TitleWithoutYear != tc.wantWithoutYear {
				t.Errorf("TitleWithoutYear = %q, want %q", got.TitleWithoutYear, tc.wantWithoutYear)
			}
			if got.Year != tc.wantYear {
				t.Errorf("Year = %d, want %d", got.Year, tc.wantYear)
			}
		})
	}
}

func TestGetSeriesTitleInfo_AllTitles(t *testing.T) {
	cases := []struct {
		name  string
		title string
		want  []string
	}{
		{
			name:  "paren form",
			title: "Series Title (Alt Name)",
			want:  []string{"Series Title", "Alt Name"},
		},
		{
			name:  "pipe form",
			title: "Series Title | Alt Name",
			want:  []string{"Series Title", "Alt Name"},
		},
		{
			name:  "AKA form",
			title: "Series Title AKA Alt Name",
			want:  []string{"Series Title", "Alt Name"},
		},
		{
			name:  "no components",
			title: "Series Title",
			want:  nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := GetSeriesTitleInfo(tc.title)
			if !reflect.DeepEqual(got.AllTitles, tc.want) {
				t.Errorf("AllTitles = %v, want %v", got.AllTitles, tc.want)
			}
		})
	}
}
