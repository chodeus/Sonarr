package aria2

import "testing"

func TestParseVersion(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in       string
		wantOK   bool
		wantPair versionPair
	}{
		{"1.36.0", true, versionPair{1, 36}},
		{"v1.18.4", true, versionPair{1, 18}},
		{"1.18-pre", true, versionPair{1, 18}},
		{"  2.5  ", true, versionPair{2, 5}},
		{"junk", false, versionPair{}},
		{"1", false, versionPair{}},
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
	floor := versionPair{1, 18}
	cases := []struct {
		actual string
		want   bool
	}{
		{"1.18.0", true},
		{"1.18.4", true},
		{"1.19.0", true},
		{"2.0.0", true},
		{"1.17.99", false},
		{"0.99.0", false},
		{"junk", false},
	}
	for _, tc := range cases {
		if got := versionAtLeast(tc.actual, floor); got != tc.want {
			t.Errorf("versionAtLeast(%q, 1.18): got %v, want %v", tc.actual, got, tc.want)
		}
	}
}
