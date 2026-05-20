package rtorrent

import "testing"

func TestParseVersion(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in       string
		wantOK   bool
		wantPair versionPair
	}{
		{"0.9.8", true, versionPair{0, 9}},
		{"v0.9.8", true, versionPair{0, 9}},
		{"1.0.0", true, versionPair{1, 0}},
		{"0.9-rc1.5", true, versionPair{0, 9}}, // strips non-digit suffix from minor
		{"  0.13.0  ", true, versionPair{0, 13}},
		{"junk", false, versionPair{}},
		{"0", false, versionPair{}}, // missing minor
		{"", false, versionPair{}},
	}
	for _, tc := range cases {
		got := parseVersion(tc.in)
		if !tc.wantOK {
			if got != (versionPair{}) {
				t.Errorf("parseVersion(%q): got %+v, want zero", tc.in, got)
			}
			continue
		}
		if got != tc.wantPair {
			t.Errorf("parseVersion(%q): got %+v, want %+v", tc.in, got, tc.wantPair)
		}
	}
}

func TestVersionAtLeast(t *testing.T) {
	t.Parallel()
	floor := versionPair{0, 9}
	cases := []struct {
		actual string
		want   bool
	}{
		{"0.9.0", true},
		{"0.9.8", true},
		{"0.10.0", true},
		{"1.0.0", true},
		{"0.8.99", false},
		{"0.0.1", false},
		{"junk", false},
	}
	for _, tc := range cases {
		if got := versionAtLeast(tc.actual, floor); got != tc.want {
			t.Errorf("versionAtLeast(%q, 0.9): got %v, want %v", tc.actual, got, tc.want)
		}
	}
}
