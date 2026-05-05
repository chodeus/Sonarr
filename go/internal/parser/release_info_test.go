package parser_test

import (
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/pendingreleases"
)

// TestReleaseInfo_AgePropertiesUseCurrentClock pins the C# Age /
// AgeHours / AgeMinutes derivation at `ReleaseInfo.cs:49-83`.
// Computed at call time against time.Since.
func TestReleaseInfo_AgePropertiesUseCurrentClock(t *testing.T) {
	t.Parallel()
	r := parser.ReleaseInfo{
		Title:       "Some.Show.S01E01.1080p",
		PublishDate: time.Now().Add(-25 * time.Hour),
	}
	if r.Age() != 1 {
		t.Errorf("Age() = %d, want 1 (25h ago)", r.Age())
	}
	if got := r.AgeHours(); got < 24.5 || got > 25.5 {
		t.Errorf("AgeHours() = %v, want approximately 25", got)
	}
	if got := r.AgeMinutes(); got < 1490 || got > 1510 {
		t.Errorf("AgeMinutes() = %v, want approximately 1500", got)
	}
}

// TestReleaseInfo_String pins the C# default ToString shape:
// `[<publishDate>] <title> [<size>]`.
func TestReleaseInfo_String(t *testing.T) {
	t.Parallel()
	r := parser.ReleaseInfo{
		Title:       "S01E01",
		Size:        12345,
		PublishDate: time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC),
	}
	got := r.String()
	if !strings.Contains(got, "S01E01") {
		t.Errorf("String() missing title: %q", got)
	}
	if !strings.Contains(got, "12345") {
		t.Errorf("String() missing size: %q", got)
	}
	if !strings.Contains(got, "2026-05-06") {
		t.Errorf("String() missing publish date: %q", got)
	}
}

// TestReleaseInfo_FieldRoundtrip exercises the data-bag shape: every
// field assigns + reads back. Catches accidental field renames.
func TestReleaseInfo_FieldRoundtrip(t *testing.T) {
	t.Parallel()
	pendingReason := pendingreleases.ReasonDelay
	r := parser.ReleaseInfo{
		Guid:                                "abc-123",
		Title:                               "Show.S01E01",
		Size:                                500_000_000,
		DownloadURL:                         "https://example.com/dl",
		InfoURL:                             "https://example.com/info",
		CommentURL:                          "https://example.com/c",
		IndexerID:                           42,
		Indexer:                             "MyIndexer",
		IndexerPriority:                     25,
		SeasonSearchMaximumSingleEpisodeAge: 7,
		DownloadProtocol:                    indexers.DownloadProtocolUsenet,
		TvdbID:                              999,
		TvRageID:                            12345,
		ImdbID:                              "tt1234567",
		PublishDate:                         time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Origin:                              "Scene",
		Source:                              "WEB-DL",
		Container:                           "MKV",
		Codec:                               "H264",
		Resolution:                          "1080p",
		Languages:                           []language.Language{language.English},
		IndexerFlags:                        parser.IndexerFlags(1),
		PendingReleaseReason:                &pendingReason,
	}
	if r.Indexer != "MyIndexer" || r.DownloadProtocol != indexers.DownloadProtocolUsenet {
		t.Errorf("field round-trip failed: %+v", r)
	}
	if r.PendingReleaseReason == nil || *r.PendingReleaseReason != pendingreleases.ReasonDelay {
		t.Errorf("PendingReleaseReason: %v", r.PendingReleaseReason)
	}
}

// TestTorrentInfo_EmbedsReleaseInfo confirms the composition shape:
// torrent fields land alongside the embedded ReleaseInfo, and the
// inherited fields are reachable through promotion.
func TestTorrentInfo_EmbedsReleaseInfo(t *testing.T) {
	t.Parallel()
	seeders := 42
	peers := 7
	t1 := &parser.TorrentInfo{
		ReleaseInfo: parser.ReleaseInfo{
			Title:            "Show.S01E01.1080p",
			DownloadProtocol: indexers.DownloadProtocolTorrent,
		},
		MagnetURL: "magnet:?xt=urn:btih:abc",
		InfoHash:  "ABC123DEF456",
		Seeders:   &seeders,
		Peers:     &peers,
	}
	// Promoted access through embed.
	if t1.Title != "Show.S01E01.1080p" {
		t.Errorf("promoted Title: %q", t1.Title)
	}
	// Direct torrent fields.
	if t1.MagnetURL != "magnet:?xt=urn:btih:abc" {
		t.Errorf("MagnetURL: %q", t1.MagnetURL)
	}
}

// TestAsTorrentInfo_TypeSwitch — the Go-shape replacement for the
// C# `release as TorrentInfo` cast pattern. Returns *TorrentInfo
// when input is one, nil otherwise.
func TestAsTorrentInfo_TypeSwitch(t *testing.T) {
	t.Parallel()
	seeders := 10
	tor := &parser.TorrentInfo{Seeders: &seeders}
	plain := &parser.ReleaseInfo{Title: "plain"}

	if got := parser.AsTorrentInfo(tor); got == nil {
		t.Errorf("AsTorrentInfo(*TorrentInfo) = nil, want non-nil")
	}
	if got := parser.AsTorrentInfo(plain); got != nil {
		t.Errorf("AsTorrentInfo(*ReleaseInfo) = %v, want nil", got)
	}
	if got := parser.AsTorrentInfo("not even a release"); got != nil {
		t.Errorf("AsTorrentInfo(non-release) = %v, want nil", got)
	}
}

// TestGetSeeders_AndGetPeers pin the C# helpers — they return nil for
// non-torrent input AND for torrents whose Seeders/Peers are themselves
// nil. The double-nil case exists because Newznab releases sometimes
// flow through TorrentInfo with nil seeder counts.
func TestGetSeeders_AndGetPeers(t *testing.T) {
	t.Parallel()
	seeders := 100
	peers := 50
	full := &parser.TorrentInfo{Seeders: &seeders, Peers: &peers}
	empty := &parser.TorrentInfo{}
	plain := &parser.ReleaseInfo{}

	if got := parser.GetSeeders(full); got == nil || *got != 100 {
		t.Errorf("GetSeeders(full): got=%v, want=100", got)
	}
	if got := parser.GetPeers(full); got == nil || *got != 50 {
		t.Errorf("GetPeers(full): got=%v, want=50", got)
	}
	if got := parser.GetSeeders(empty); got != nil {
		t.Errorf("GetSeeders(empty torrent): got=%v, want nil", got)
	}
	if got := parser.GetSeeders(plain); got != nil {
		t.Errorf("GetSeeders(plain): got=%v, want nil", got)
	}
}
