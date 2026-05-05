package pendingreleases_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/pendingreleases"
)

func TestReason_Ordinals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		got  pendingreleases.Reason
		want int
	}{
		{"Delay", pendingreleases.ReasonDelay, 0},
		{"DownloadClientUnavailable", pendingreleases.ReasonDownloadClientUnavailable, 1},
		{"Fallback", pendingreleases.ReasonFallback, 2},
	}
	for _, tc := range cases {
		if int(tc.got) != tc.want {
			t.Errorf("Reason%s = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
}

func TestReason_String(t *testing.T) {
	t.Parallel()
	cases := []struct {
		got  pendingreleases.Reason
		want string
	}{
		{pendingreleases.ReasonDelay, "delay"},
		{pendingreleases.ReasonDownloadClientUnavailable, "download-client-unavailable"},
		{pendingreleases.ReasonFallback, "fallback"},
		{pendingreleases.Reason(99), "unknown"},
	}
	for _, tc := range cases {
		if got := tc.got.String(); got != tc.want {
			t.Errorf("Reason(%d).String() = %q, want %q", tc.got, got, tc.want)
		}
	}
}
