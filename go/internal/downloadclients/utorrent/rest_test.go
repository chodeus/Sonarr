package utorrent_test

import (
	"context"
	"encoding/json"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/utorrent"
)

// listResponse builds a synthetic list=1 response body. Each
// torrentRow is a 27-column-ish array — the trailing columns
// (DownloadURL, RssFeedUrl, ..., RootDownloadPath) are appended
// when set so the row matches the 25406+ uTorrent shape.
type torrentRow struct {
	hash, name, label string
	status            utorrent.Status
	size, remaining   int64
	ratioPerMille     int
	progressPerMille  int
	eta               int
	rootDownloadPath  string
}

func listResponse(rows ...torrentRow) string {
	type envelope struct {
		Build    int     `json:"build"`
		Torrents [][]any `json:"torrents"`
	}
	env := envelope{Build: 45828}
	for _, r := range rows {
		row := []any{
			r.hash,
			int(r.status),
			r.name,
			r.size,
			r.progressPerMille,
			0, // downloaded
			0, // uploaded
			r.ratioPerMille,
			0, // up_speed
			0, // down_speed
			r.eta,
			r.label,
			0, // peers_connected
			0, // peers_in_swarm
			0, // seeds_connected
			0, // seeds_in_swarm
			0, // availability
			0, // queue_order
			r.remaining,
		}
		if r.rootDownloadPath != "" {
			// pad through columns 19..25 then put root_download_path at 26.
			row = append(row, "")                 // 19: downloadURL
			row = append(row, nil)                // 20
			row = append(row, nil)                // 21
			row = append(row, nil)                // 22
			row = append(row, nil)                // 23
			row = append(row, nil)                // 24
			row = append(row, nil)                // 25
			row = append(row, r.rootDownloadPath) // 26
		}
		env.Torrents = append(env.Torrents, row)
	}
	body, _ := json.Marshal(env)
	return string(body)
}

func TestGetItems_HappyPath(t *testing.T) {
	t.Parallel()
	body := listResponse(
		torrentRow{
			hash: "abc123", name: "Show.S01E01", label: "tv-sonarr",
			status: utorrent.StatusStarted | utorrent.StatusLoaded | utorrent.StatusChecked,
			size:   1000, remaining: 500, progressPerMille: 500, ratioPerMille: 1500, eta: 30,
			rootDownloadPath: "/data/downloads",
		},
		torrentRow{
			hash: "def456", name: "Show.S01E02", label: "tv-sonarr",
			status: utorrent.StatusLoaded | utorrent.StatusChecked,
			size:   2000, remaining: 0, progressPerMille: 1000, ratioPerMille: 2500,
			rootDownloadPath: "/data/downloads",
		},
	)
	h := newUTorrentServer(t, "TOKEN", body)
	p := utorrent.NewProxy(h.srv.Client(), nil)
	s := h.settings()
	s.TvCategory = "tv-sonarr"
	u := utorrent.New(nil, s, p, nil)

	items, err := u.GetItems(context.Background())
	if err != nil {
		t.Fatalf("GetItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items: got %d, want 2", len(items))
	}
	// First — downloading (Started flag, remaining > 0).
	if items[0].Status != downloadclients.DownloadItemStatusDownloading {
		t.Errorf("items[0].Status: %v, want Downloading", items[0].Status)
	}
	if items[0].DownloadID != "ABC123" {
		t.Errorf("items[0].DownloadID: %q, want ABC123 (uppercased)", items[0].DownloadID)
	}
	if items[0].RemainingTime == nil || items[0].RemainingTime.Seconds() != 30 {
		t.Errorf("items[0].RemainingTime: %v, want 30s", items[0].RemainingTime)
	}
	// Second — completed (Loaded+Checked, remaining=0, progress=1000/1000=1.0).
	if items[1].Status != downloadclients.DownloadItemStatusCompleted {
		t.Errorf("items[1].Status: %v, want Completed", items[1].Status)
	}
	if items[1].SeedRatio == nil || *items[1].SeedRatio != 2.5 {
		t.Errorf("items[1].SeedRatio: %v, want 2.5 (2500/1000)", items[1].SeedRatio)
	}
}

func TestGetItems_StatusMapping(t *testing.T) {
	t.Parallel()
	body := listResponse(
		torrentRow{hash: "1", label: "tv-sonarr", status: utorrent.StatusError, size: 1, progressPerMille: 0},
		torrentRow{hash: "2", label: "tv-sonarr", status: utorrent.StatusPaused, size: 1, progressPerMille: 0},
		torrentRow{hash: "3", label: "tv-sonarr", status: utorrent.StatusStarted, size: 1, progressPerMille: 500, remaining: 100},
		torrentRow{hash: "4", label: "tv-sonarr", status: utorrent.StatusQueued, size: 1, progressPerMille: 0},
	)
	h := newUTorrentServer(t, "TOKEN", body)
	p := utorrent.NewProxy(h.srv.Client(), nil)
	s := h.settings()
	s.TvCategory = "tv-sonarr"
	u := utorrent.New(nil, s, p, nil)

	items, _ := u.GetItems(context.Background())
	if len(items) != 4 {
		t.Fatalf("items: got %d, want 4", len(items))
	}
	want := []downloadclients.DownloadItemStatus{
		downloadclients.DownloadItemStatusWarning,
		downloadclients.DownloadItemStatusPaused,
		downloadclients.DownloadItemStatusDownloading,
		downloadclients.DownloadItemStatusQueued,
	}
	for i, w := range want {
		if items[i].Status != w {
			t.Errorf("items[%d].Status: %v, want %v", i, items[i].Status, w)
		}
	}
	if items[0].Message == "" {
		t.Errorf("error item should have a Message")
	}
}

func TestGetItems_CategoryFilter(t *testing.T) {
	t.Parallel()
	body := listResponse(
		torrentRow{hash: "1", name: "ok", label: "tv-sonarr", size: 1, status: utorrent.StatusStarted},
		torrentRow{hash: "2", name: "skip", label: "movies", size: 1, status: utorrent.StatusStarted},
	)
	h := newUTorrentServer(t, "TOKEN", body)
	p := utorrent.NewProxy(h.srv.Client(), nil)
	s := h.settings()
	s.TvCategory = "tv-sonarr"
	u := utorrent.New(nil, s, p, nil)
	items, _ := u.GetItems(context.Background())
	if len(items) != 1 || items[0].Title != "ok" {
		t.Errorf("category filter: got %+v, want only the tv-sonarr one", items)
	}
}

func TestGetItems_OutputPathQuirk(t *testing.T) {
	t.Parallel()
	// When the RootDownloadPath's basename matches the torrent
	// name, OutputPath = RootDownloadPath (no rejoin).
	// When it doesn't match, OutputPath = RootDownloadPath + Name.
	body := listResponse(
		torrentRow{hash: "1", name: "Show.S01E01", label: "tv-sonarr",
			status: utorrent.StatusStarted, size: 1, progressPerMille: 100, remaining: 100,
			rootDownloadPath: "/data/downloads/Show.S01E01"}, // basename matches
		torrentRow{hash: "2", name: "Show.S01E02", label: "tv-sonarr",
			status: utorrent.StatusStarted, size: 1, progressPerMille: 100, remaining: 100,
			rootDownloadPath: "/data/downloads"}, // basename ≠ name
	)
	h := newUTorrentServer(t, "TOKEN", body)
	p := utorrent.NewProxy(h.srv.Client(), nil)
	s := h.settings()
	s.TvCategory = "tv-sonarr"
	u := utorrent.New(nil, s, p, nil)
	items, _ := u.GetItems(context.Background())
	if len(items) != 2 {
		t.Fatalf("items: got %d", len(items))
	}
	if items[0].OutputPath.String() != "/data/downloads/Show.S01E01" {
		t.Errorf("basename-matches: OutputPath = %q, want /data/downloads/Show.S01E01", items[0].OutputPath)
	}
	if items[1].OutputPath.String() != "/data/downloads/Show.S01E02" {
		t.Errorf("basename-mismatch: OutputPath = %q, want /data/downloads/Show.S01E02 (path + name)", items[1].OutputPath)
	}
}

func TestGetItems_CanMoveFilesGate(t *testing.T) {
	t.Parallel()
	body := listResponse(
		torrentRow{hash: "queued", label: "tv-sonarr", status: utorrent.StatusQueued, size: 1},
		torrentRow{hash: "started", label: "tv-sonarr", status: utorrent.StatusStarted, size: 1},
		torrentRow{hash: "done", label: "tv-sonarr", status: utorrent.StatusLoaded | utorrent.StatusChecked, size: 1, progressPerMille: 1000},
	)
	h := newUTorrentServer(t, "TOKEN", body)
	p := utorrent.NewProxy(h.srv.Client(), nil)
	s := h.settings()
	s.TvCategory = "tv-sonarr"
	u := utorrent.New(nil, s, p, nil)
	items, _ := u.GetItems(context.Background())
	if len(items) != 3 {
		t.Fatalf("items: got %d", len(items))
	}
	// Queued + Started → both flags inhibit CanMoveFiles/CanBeRemoved.
	if items[0].CanMoveFiles || items[0].CanBeRemoved {
		t.Errorf("Queued: CanMoveFiles/CanBeRemoved should be false")
	}
	if items[1].CanMoveFiles || items[1].CanBeRemoved {
		t.Errorf("Started: CanMoveFiles/CanBeRemoved should be false")
	}
	if !items[2].CanMoveFiles || !items[2].CanBeRemoved {
		t.Errorf("Completed (no Queued, no Started): want CanMoveFiles + CanBeRemoved true")
	}
}

func TestGetItems_NilProxyErrors(t *testing.T) {
	t.Parallel()
	u := utorrent.New(nil, utorrent.NewSettings(), nil, nil)
	if _, err := u.GetItems(context.Background()); err == nil {
		t.Errorf("GetItems: want error when no proxy")
	}
}

// RemoveItem tests.
const okJSON = `{"build":45828}`

func TestRemoveItem_FiresRemove(t *testing.T) {
	t.Parallel()
	h := newUTorrentServer(t, "TOKEN", okJSON)
	p := utorrent.NewProxy(h.srv.Client(), nil)
	u := utorrent.New(nil, h.settings(), p, nil)
	if err := u.RemoveItem(context.Background(), &downloadclients.Item{DownloadID: "ABC"}, false); err != nil {
		t.Fatalf("RemoveItem: %v", err)
	}
	if action, _ := h.lastAction.Load().(string); action != "remove" {
		t.Errorf("action: got %q, want remove", action)
	}
}

func TestRemoveItem_FiresRemoveDataWhenSet(t *testing.T) {
	t.Parallel()
	h := newUTorrentServer(t, "TOKEN", okJSON)
	p := utorrent.NewProxy(h.srv.Client(), nil)
	u := utorrent.New(nil, h.settings(), p, nil)
	if err := u.RemoveItem(context.Background(), &downloadclients.Item{DownloadID: "ABC"}, true); err != nil {
		t.Fatalf("RemoveItem: %v", err)
	}
	if action, _ := h.lastAction.Load().(string); action != "removedata" {
		t.Errorf("action: got %q, want removedata", action)
	}
}

func TestRemoveItem_NilItemErrors(t *testing.T) {
	t.Parallel()
	u := utorrent.New(nil, utorrent.NewSettings(), nil, nil)
	if err := u.RemoveItem(context.Background(), nil, false); err == nil {
		t.Errorf("RemoveItem(nil): want error")
	}
}

func TestGetStatus_IsLocalhost(t *testing.T) {
	t.Parallel()
	for _, host := range []string{"localhost", "127.0.0.1", "::1"} {
		host := host
		t.Run(host, func(t *testing.T) {
			t.Parallel()
			s := utorrent.NewSettings()
			s.Host = host
			u := utorrent.New(nil, s, nil, nil)
			info, err := u.GetStatus(context.Background())
			if err != nil {
				t.Fatalf("GetStatus: %v", err)
			}
			if info == nil || !info.IsLocalhost {
				t.Errorf("Host=%q: IsLocalhost = %v, want true", host, info)
			}
		})
	}
}

func TestGetStatus_NotLocalhostForRemoteHost(t *testing.T) {
	t.Parallel()
	s := utorrent.NewSettings()
	s.Host = "utor.example.com"
	u := utorrent.New(nil, s, nil, nil)
	info, err := u.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if info == nil || info.IsLocalhost {
		t.Errorf("remote host: %+v, want !IsLocalhost", info)
	}
}

// MarkItemAsImported tests.
func TestMarkItemAsImported_AppliesLabel(t *testing.T) {
	t.Parallel()
	h := newUTorrentServer(t, "TOKEN", okJSON)
	p := utorrent.NewProxy(h.srv.Client(), nil)
	s := h.settings()
	s.TvCategory = "tv-sonarr"
	s.TvImportedCategory = "tv-done"
	u := utorrent.New(nil, s, p, nil)
	if err := u.MarkItemAsImported(context.Background(), &downloadclients.Item{DownloadID: "ABC"}); err != nil {
		t.Fatalf("MarkItemAsImported: %v", err)
	}
	if action, _ := h.lastAction.Load().(string); action != "setprops" {
		t.Errorf("action: got %q, want setprops", action)
	}
}

func TestMarkItemAsImported_NoOpWhenLabelEqualTvCategory(t *testing.T) {
	t.Parallel()
	h := newUTorrentServer(t, "TOKEN", okJSON)
	p := utorrent.NewProxy(h.srv.Client(), nil)
	s := h.settings()
	s.TvCategory = "tv-sonarr"
	s.TvImportedCategory = "tv-sonarr"
	u := utorrent.New(nil, s, p, nil)
	if err := u.MarkItemAsImported(context.Background(), &downloadclients.Item{DownloadID: "ABC"}); err != nil {
		t.Fatalf("MarkItemAsImported: %v", err)
	}
	if h.calls.Load() != 0 {
		t.Errorf("expected 0 RPC calls when label matches TvCategory, got %d", h.calls.Load())
	}
}

func TestMarkItemAsImported_NoOpWhenLabelEmpty(t *testing.T) {
	t.Parallel()
	h := newUTorrentServer(t, "TOKEN", okJSON)
	p := utorrent.NewProxy(h.srv.Client(), nil)
	s := h.settings()
	s.TvImportedCategory = ""
	u := utorrent.New(nil, s, p, nil)
	if err := u.MarkItemAsImported(context.Background(), &downloadclients.Item{DownloadID: "ABC"}); err != nil {
		t.Fatalf("MarkItemAsImported: %v", err)
	}
	if h.calls.Load() != 0 {
		t.Errorf("expected 0 RPC calls when TvImportedCategory empty, got %d", h.calls.Load())
	}
}

// Status bitflag — verify Has() semantics.
func TestStatus_HasBitflag(t *testing.T) {
	t.Parallel()
	combined := utorrent.StatusStarted | utorrent.StatusLoaded | utorrent.StatusChecked
	if !combined.Has(utorrent.StatusStarted) {
		t.Errorf("combined.Has(StatusStarted): false, want true")
	}
	if !combined.Has(utorrent.StatusLoaded) {
		t.Errorf("combined.Has(StatusLoaded): false, want true")
	}
	if combined.Has(utorrent.StatusError) {
		t.Errorf("combined.Has(StatusError): true, want false")
	}
}
