package quality_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/quality"
)

func TestProperDownloadType_Ordinals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		got  quality.ProperDownloadType
		want int
	}{
		{"PreferAndUpgrade", quality.ProperPreferAndUpgrade, 0},
		{"DoNotUpgrade", quality.ProperDoNotUpgrade, 1},
		{"DoNotPrefer", quality.ProperDoNotPrefer, 2},
	}
	for _, tc := range cases {
		if int(tc.got) != tc.want {
			t.Errorf("Proper%s = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
}

func TestProperDownloadType_String(t *testing.T) {
	t.Parallel()
	cases := []struct {
		got  quality.ProperDownloadType
		want string
	}{
		{quality.ProperPreferAndUpgrade, "prefer-and-upgrade"},
		{quality.ProperDoNotUpgrade, "do-not-upgrade"},
		{quality.ProperDoNotPrefer, "do-not-prefer"},
		{quality.ProperDownloadType(99), "unknown"},
	}
	for _, tc := range cases {
		if got := tc.got.String(); got != tc.want {
			t.Errorf("ProperDownloadType(%d).String() = %q, want %q", tc.got, got, tc.want)
		}
	}
}
