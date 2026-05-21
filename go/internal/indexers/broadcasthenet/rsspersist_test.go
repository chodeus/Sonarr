package broadcasthenet_test

import (
	"encoding/json"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/indexers/broadcasthenet"
	"sonarr.tv/sonarr/internal/parser"
)

// TestParseLastRecentTorrentID exercises every documented swallow
// arm of the parser. Mirrors the silence-on-failure tolerance of C#
// `BroadcastheNet.cs:25-37` — every non-success path leaves the
// watermark at the 0 sentinel that downstream callers treat as
// "no watermark known".
func TestParseLastRecentTorrentID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"empty", "", 0},
		{"happy path BTN-12245", `{"guid":"BTN-12245"}`, 12245},
		{"happy path with siblings", `{"guid":"BTN-7","title":"Foo.S01E01","publishDate":"2026-05-01T00:00:00Z","size":1234}`, 7},
		{"non-BTN- prefix swallowed", `{"guid":"nzbgeek-7"}`, 0},
		{"empty guid swallowed", `{"guid":""}`, 0},
		{"missing guid key swallowed", `{"title":"Foo.S01E01"}`, 0},
		{"BTN- with non-numeric payload swallowed", `{"guid":"BTN-abc"}`, 0},
		{"BTN- with empty payload swallowed", `{"guid":"BTN-"}`, 0},
		{"malformed JSON swallowed", `{not json`, 0},
		{"case-sensitive prefix (btn- != BTN-)", `{"guid":"btn-7"}`, 0},
		{"negative id parsed", `{"guid":"BTN--5"}`, -5},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := broadcasthenet.ParseLastRecentTorrentID(tc.in)
			if got != tc.want {
				t.Errorf("ParseLastRecentTorrentID(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// EncodeLastRecentRelease on an empty / nil slice returns "" — the
// caller (J178 wiring) treats "" as "skip UpdateRssSyncStatus",
// matching C# `if (isRecent && !releases.Empty())` at
// HttpIndexerBase.cs:207.
func TestEncodeLastRecentRelease_EmptyOrNilReturnsEmpty(t *testing.T) {
	t.Parallel()
	if got := broadcasthenet.EncodeLastRecentRelease(nil); got != "" {
		t.Errorf("nil slice → %q, want empty", got)
	}
	if got := broadcasthenet.EncodeLastRecentRelease([]*parser.TorrentInfo{}); got != "" {
		t.Errorf("empty slice → %q, want empty", got)
	}
}

// Single-item slice serialises the item's embedded ReleaseInfo —
// torrent extras (InfoHash, MagnetURL, Seeders, Peers) are NOT
// emitted because the C# column type is `ReleaseInfo` and the
// Go-port matches the declared-type shape.
func TestEncodeLastRecentRelease_SingleItemOmitsTorrentExtras(t *testing.T) {
	t.Parallel()
	seeders := 99
	rels := []*parser.TorrentInfo{
		{
			ReleaseInfo: parser.ReleaseInfo{
				Guid:        "BTN-42",
				Title:       "Foo.S01E01.1080p",
				PublishDate: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
				Size:        1234567,
			},
			InfoHash:  "deadbeef",
			MagnetURL: "magnet:?xt=urn:btih:deadbeef",
			Seeders:   &seeders,
		},
	}
	got := broadcasthenet.EncodeLastRecentRelease(rels)
	if got == "" {
		t.Fatal("expected non-empty JSON")
	}
	// Decode into a probe struct that captures both the wanted
	// (ReleaseInfo) fields and the unwanted (TorrentInfo) extras.
	var probe struct {
		Guid      string `json:"guid"`
		Title     string `json:"title"`
		InfoHash  string `json:"infoHash"`
		MagnetURL string `json:"magnetUrl"`
		Seeders   *int   `json:"seeders"`
	}
	if err := json.Unmarshal([]byte(got), &probe); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if probe.Guid != "BTN-42" || probe.Title != "Foo.S01E01.1080p" {
		t.Errorf("declared-type fields lost: %+v", probe)
	}
	if probe.InfoHash != "" || probe.MagnetURL != "" || probe.Seeders != nil {
		t.Errorf("torrent extras leaked into ReleaseInfo encode: %+v", probe)
	}
}

// Newest-by-PublishDate selection survives across a multi-item
// slice in arbitrary input order. Mirrors C#
// `OrderByDescending(v => v.PublishDate).First()`.
func TestEncodeLastRecentRelease_PicksNewestByPublishDate(t *testing.T) {
	t.Parallel()
	mk := func(guid string, day int) *parser.TorrentInfo {
		return &parser.TorrentInfo{ReleaseInfo: parser.ReleaseInfo{
			Guid:        guid,
			PublishDate: time.Date(2026, 5, day, 0, 0, 0, 0, time.UTC),
		}}
	}
	// Out-of-order input — middle slot is newest.
	rels := []*parser.TorrentInfo{
		mk("BTN-1", 5),
		mk("BTN-99", 20),
		mk("BTN-3", 10),
	}
	got := broadcasthenet.EncodeLastRecentRelease(rels)
	if got == "" {
		t.Fatal("expected non-empty JSON")
	}
	if broadcasthenet.ParseLastRecentTorrentID(got) != 99 {
		t.Errorf("newest watermark = %d, want 99 (BTN-99 has 2026-05-20)", broadcasthenet.ParseLastRecentTorrentID(got))
	}
}

// nil entries inside the slice are skipped — defensive against the
// fetchPaged accumulator pattern that pre-allocates and may leave
// holes under partial-page failures. Mirrors the C# `Where(IsValid)`
// filter that runs before OrderByDescending.
func TestEncodeLastRecentRelease_SkipsNilEntries(t *testing.T) {
	t.Parallel()
	rels := []*parser.TorrentInfo{
		nil,
		{ReleaseInfo: parser.ReleaseInfo{
			Guid:        "BTN-5",
			PublishDate: time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC),
		}},
		nil,
	}
	got := broadcasthenet.EncodeLastRecentRelease(rels)
	if broadcasthenet.ParseLastRecentTorrentID(got) != 5 {
		t.Errorf("nil-skipping selection = %d, want 5", broadcasthenet.ParseLastRecentTorrentID(got))
	}
}

// Encode → Parse round-trip is the integration contract the J178
// wiring relies on. The encoded JSON written via
// indexerstatus.Service.UpdateRssSyncStatus must parse back to the
// same torrent id on the next FetchRecent boot.
func TestEncodeThenParse_RoundTrips(t *testing.T) {
	t.Parallel()
	rels := []*parser.TorrentInfo{
		{ReleaseInfo: parser.ReleaseInfo{
			Guid:        "BTN-31415",
			Title:       "Pi.S01E01",
			PublishDate: time.Date(2026, 5, 4, 8, 30, 0, 0, time.UTC),
			Size:        2718281828,
		}},
	}
	encoded := broadcasthenet.EncodeLastRecentRelease(rels)
	if encoded == "" {
		t.Fatal("expected non-empty encode")
	}
	if id := broadcasthenet.ParseLastRecentTorrentID(encoded); id != 31415 {
		t.Errorf("round-trip id = %d, want 31415", id)
	}
}
