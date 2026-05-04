package episodefiles_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/episodefiles"
)

func TestDeleteMediaFileReason_String(t *testing.T) {
	t.Parallel()
	cases := []struct {
		reason episodefiles.DeleteMediaFileReason
		want   string
	}{
		{episodefiles.ReasonMissingFromDisk, "MissingFromDisk"},
		{episodefiles.ReasonManual, "Manual"},
		{episodefiles.ReasonUpgrade, "Upgrade"},
		{episodefiles.ReasonNoLinkedEpisodes, "NoLinkedEpisodes"},
		{episodefiles.ReasonManualOverride, "ManualOverride"},
		{episodefiles.DeleteMediaFileReason(99), "Unknown"},
	}
	for _, tc := range cases {
		if got := tc.reason.String(); got != tc.want {
			t.Errorf("DeleteMediaFileReason(%d).String() = %q, want %q", tc.reason, got, tc.want)
		}
	}
}

func TestDeleteMediaFileReason_FrozenValues(t *testing.T) {
	t.Parallel()
	// The numeric values aren't persisted, but they ARE on the wire
	// for any future event-bus envelope. Pin the order so accidental
	// reorders don't silently shift consumer expectations.
	pins := map[episodefiles.DeleteMediaFileReason]int{
		episodefiles.ReasonMissingFromDisk:  0,
		episodefiles.ReasonManual:           1,
		episodefiles.ReasonUpgrade:          2,
		episodefiles.ReasonNoLinkedEpisodes: 3,
		episodefiles.ReasonManualOverride:   4,
	}
	for r, want := range pins {
		if int(r) != want {
			t.Errorf("DeleteMediaFileReason %s: got %d, want %d", r, int(r), want)
		}
	}
}
