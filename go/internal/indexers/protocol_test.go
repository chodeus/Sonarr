package indexers_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/indexers"
)

func TestDownloadProtocol_Ordinals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		got  indexers.DownloadProtocol
		want int
	}{
		{"Unknown", indexers.DownloadProtocolUnknown, 0},
		{"Usenet", indexers.DownloadProtocolUsenet, 1},
		{"Torrent", indexers.DownloadProtocolTorrent, 2},
	}
	for _, tc := range cases {
		if int(tc.got) != tc.want {
			t.Errorf("DownloadProtocol%s = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
}

func TestDownloadProtocol_String(t *testing.T) {
	t.Parallel()
	cases := []struct {
		got  indexers.DownloadProtocol
		want string
	}{
		{indexers.DownloadProtocolUnknown, "unknown"},
		{indexers.DownloadProtocolUsenet, "usenet"},
		{indexers.DownloadProtocolTorrent, "torrent"},
		{indexers.DownloadProtocol(99), "unknown"},
	}
	for _, tc := range cases {
		if got := tc.got.String(); got != tc.want {
			t.Errorf("DownloadProtocol(%d).String() = %q, want %q", tc.got, got, tc.want)
		}
	}
}
