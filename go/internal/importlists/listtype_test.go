package importlists_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/importlists"
)

// ListType ordinals are frozen — they are the v3 wire-format the
// React UI keys icons + filter labels off. Renumbering would silently
// break consumers.
func TestListType_Ordinals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		t    importlists.ListType
		want int
	}{
		{"Program", importlists.ListTypeProgram, 0},
		{"Plex", importlists.ListTypePlex, 1},
		{"Trakt", importlists.ListTypeTrakt, 2},
		{"Simkl", importlists.ListTypeSimkl, 3},
		{"Other", importlists.ListTypeOther, 4},
		{"Advanced", importlists.ListTypeAdvanced, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if int(tc.t) != tc.want {
				t.Errorf("%s = %d, want %d", tc.name, int(tc.t), tc.want)
			}
		})
	}
}

func TestListType_String(t *testing.T) {
	t.Parallel()
	cases := []struct {
		t    importlists.ListType
		want string
	}{
		{importlists.ListTypeProgram, "Program"},
		{importlists.ListTypePlex, "Plex"},
		{importlists.ListTypeTrakt, "Trakt"},
		{importlists.ListTypeSimkl, "Simkl"},
		{importlists.ListTypeOther, "Other"},
		{importlists.ListTypeAdvanced, "Advanced"},
		{importlists.ListType(99), "ListType(99)"},
		{importlists.ListType(-1), "ListType(-1)"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			if got := tc.t.String(); got != tc.want {
				t.Errorf("ListType(%d).String() = %q, want %q", int(tc.t), got, tc.want)
			}
		})
	}
}
