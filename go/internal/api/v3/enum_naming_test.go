package v3

import (
	"testing"

	"sonarr.tv/sonarr/internal/quality"
)

func TestCamelCaseEnumName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"all", "all"},                           // already lowercase
		{"All", "all"},                           // single leading upper
		{"Always", "always"},                     // single leading upper
		{"None", "none"},                         // single leading upper
		{"PreferAndUpgrade", "preferAndUpgrade"}, // single leading upper
		{"DoNotUpgrade", "doNotUpgrade"},         // single leading upper
		{"DVD", "dvd"},                           // all caps
		{"WebRip", "webRip"},                     // single leading upper
		{"LocalAirDate", "localAirDate"},         // single leading upper
		{"IPAddress", "ipAddress"},               // multi-cap leading run
		{"TVShow", "tvShow"},                     // multi-cap leading run
	}
	for _, tc := range cases {
		if got := CamelCaseEnumName(tc.in); got != tc.want {
			t.Errorf("CamelCaseEnumName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestProperDownloadTypeName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   quality.ProperDownloadType
		want string
	}{
		{quality.ProperPreferAndUpgrade, "preferAndUpgrade"},
		{quality.ProperDoNotUpgrade, "doNotUpgrade"},
		{quality.ProperDoNotPrefer, "doNotPrefer"},
		{quality.ProperDownloadType(99), "unknown"},
	}
	for _, tc := range cases {
		if got := ProperDownloadTypeName(tc.in); got != tc.want {
			t.Errorf("ProperDownloadTypeName(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
