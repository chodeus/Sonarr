package deluge_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/deluge"
)

// ============================================================================
// Deluge.GetItems() — slice 5.G2d. Ports C# Deluge.GetItems at
// Deluge.cs:114-215 + DelugeProxy.GetTorrents at :68-94.
// ============================================================================

// updateUIServer is the canonical handler for web.update_ui round-
// tripping. Test cases pin the captured filter argument via
// h.lastFilter.Load() and the response shape via h.torrentsJSON.
type updateUIServer struct {
	torrentsJSON string

	updateCalls atomic.Int32
	lastFields  atomic.Value // []string
	lastFilter  atomic.Value // map[string]any
}

func newUpdateUIServer(t *testing.T, torrentsJSON string) (*updateUIServer, *httptest.Server) {
	h := &updateUIServer{torrentsJSON: torrentsJSON}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var env struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			ID     int             `json:"id"`
		}
		_ = json.Unmarshal(body, &env)
		switch env.Method {
		case "auth.login":
			http.SetCookie(w, &http.Cookie{Name: "_session_id", Value: "sid"})
			_, _ = io.WriteString(w, `{"result":true,"error":null,"id":1}`)
		case "web.update_ui":
			h.updateCalls.Add(1)
			var args []json.RawMessage
			_ = json.Unmarshal(env.Params, &args)
			if len(args) >= 1 {
				var fields []string
				_ = json.Unmarshal(args[0], &fields)
				h.lastFields.Store(fields)
			}
			if len(args) >= 2 {
				var filter map[string]any
				_ = json.Unmarshal(args[1], &filter)
				h.lastFilter.Store(filter)
			}
			body := h.torrentsJSON
			if body == "" {
				body = "{}"
			}
			_, _ = io.WriteString(w, `{"result":{"connected":true,"torrents":`+body+`},"error":null,"id":2}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return h, srv
}

func newDelugeForGetItems(t *testing.T, srv *httptest.Server, mutate func(*deluge.Settings)) *deluge.Deluge {
	t.Helper()
	s := settingsFor(t, srv)
	if mutate != nil {
		mutate(&s)
	}
	proxy, err := deluge.NewProxy(srv.Client(), nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	return deluge.New(&downloadclients.DownloadClient{ID: 42, Name: "deluge-test"}, s, proxy, nil)
}

func TestGetItems_EmptyTorrents(t *testing.T) {
	t.Parallel()
	_, srv := newUpdateUIServer(t, `{}`)
	d := newDelugeForGetItems(t, srv, nil)
	got, err := d.GetItems(context.Background())
	if err != nil {
		t.Fatalf("GetItems() err = %v", err)
	}
	if got == nil {
		t.Fatal("GetItems() = nil, want empty slice")
	}
	if len(got) != 0 {
		t.Errorf("GetItems() = %v, want empty", got)
	}
}

func TestGetItems_TwoTorrents(t *testing.T) {
	t.Parallel()
	body := `{
		"abc123":{"hash":"abc123","name":"Show.S01E01","state":"Downloading","progress":50.0,"eta":120,"message":"","is_finished":false,"save_path":"/downloads","total_size":2147483648,"total_done":1073741824,"time_added":1700000000,"active_time":3600,"ratio":0.0,"is_auto_managed":true,"stop_at_ratio":false,"remove_at_ratio":false,"stop_ratio":2.0},
		"def456":{"hash":"def456","name":"Show.S01E02","state":"Seeding","progress":100.0,"eta":0,"message":"","is_finished":true,"save_path":"/downloads","total_size":1073741824,"total_done":1073741824,"time_added":1700000100,"active_time":7200,"ratio":1.5,"is_auto_managed":true,"stop_at_ratio":true,"remove_at_ratio":false,"stop_ratio":1.0}
	}`
	_, srv := newUpdateUIServer(t, body)
	d := newDelugeForGetItems(t, srv, nil)
	got, err := d.GetItems(context.Background())
	if err != nil {
		t.Fatalf("GetItems() err = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}

	// Index items by DownloadID for stable assertions (map iteration).
	byID := map[string]*downloadclients.Item{}
	for _, item := range got {
		byID[item.DownloadID] = item
	}

	d1, ok := byID["ABC123"]
	if !ok {
		t.Fatalf("ABC123 missing")
	}
	if d1.Title != "Show.S01E01" {
		t.Errorf("[abc].Title = %q", d1.Title)
	}
	if d1.TotalSize != 2147483648 {
		t.Errorf("[abc].TotalSize = %d", d1.TotalSize)
	}
	if d1.RemainingSize != 1073741824 {
		t.Errorf("[abc].RemainingSize = %d, want 1073741824 (size-done)", d1.RemainingSize)
	}
	if d1.Status != downloadclients.DownloadItemStatusDownloading {
		t.Errorf("[abc].Status = %v, want Downloading", d1.Status)
	}
	if d1.OutputPath.String() != "/downloads/Show.S01E01" {
		t.Errorf("[abc].OutputPath = %q, want /downloads/Show.S01E01", d1.OutputPath.String())
	}
	if d1.SeedRatio == nil || *d1.SeedRatio != 0 {
		t.Errorf("[abc].SeedRatio = %v", d1.SeedRatio)
	}
	if d1.DownloadClientInfo == nil || d1.DownloadClientInfo.Type != deluge.Implementation {
		t.Errorf("[abc].DownloadClientInfo = %+v", d1.DownloadClientInfo)
	}

	c1, ok := byID["DEF456"]
	if !ok {
		t.Fatalf("DEF456 missing")
	}
	if c1.Status != downloadclients.DownloadItemStatusCompleted {
		t.Errorf("[def].Status = %v, want Completed (IsFinished + State=Seeding)", c1.Status)
	}
}

func TestGetItems_CanBeRemovedGating(t *testing.T) {
	t.Parallel()
	// All five gates true (Paused + StopAtRatio + Ratio>=StopRatio +
	// IsAutoManaged) AND the row's RemoveCompletedDownloads is true.
	body := `{"h":{"hash":"h","name":"X","state":"Paused","progress":100.0,"is_finished":true,"save_path":"/dl","total_size":100,"total_done":100,"ratio":2.0,"is_auto_managed":true,"stop_at_ratio":true,"stop_ratio":1.0}}`
	_, srv := newUpdateUIServer(t, body)
	s := settingsFor(t, srv)
	proxy, _ := deluge.NewProxy(srv.Client(), nil)
	def := &downloadclients.DownloadClient{ID: 1, Name: "d", RemoveCompletedDownloads: true}
	d := deluge.New(def, s, proxy, nil)

	got, err := d.GetItems(context.Background())
	if err != nil {
		t.Fatalf("GetItems() err = %v", err)
	}
	if len(got) != 1 || !got[0].CanBeRemoved {
		t.Errorf("CanBeRemoved = false, want true (5-way AND gate satisfied)")
	}
	if !got[0].CanMoveFiles {
		t.Errorf("CanMoveFiles = false, want true (mirrors CanBeRemoved)")
	}
}

func TestGetItems_RemoveCompletedDownloadsFalseGates(t *testing.T) {
	t.Parallel()
	body := `{"h":{"hash":"h","name":"X","state":"Paused","is_finished":true,"save_path":"/dl","total_size":100,"total_done":100,"ratio":2.0,"is_auto_managed":true,"stop_at_ratio":true,"stop_ratio":1.0}}`
	_, srv := newUpdateUIServer(t, body)
	d := newDelugeForGetItems(t, srv, nil) // default def has RemoveCompletedDownloads=false
	got, _ := d.GetItems(context.Background())
	if got[0].CanBeRemoved {
		t.Errorf("CanBeRemoved = true, want false (def.RemoveCompletedDownloads=false gates the AND)")
	}
}

func TestGetItems_StateMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		state      string
		isFinished bool
		wantStatus downloadclients.DownloadItemStatus
	}{
		{"Error", false, downloadclients.DownloadItemStatusWarning},
		{"Queued", false, downloadclients.DownloadItemStatusQueued},
		{"Paused", false, downloadclients.DownloadItemStatusPaused},
		{"Paused", true, downloadclients.DownloadItemStatusCompleted}, // IsFinished wins
		{"Seeding", true, downloadclients.DownloadItemStatusCompleted},
		{"Seeding", false, downloadclients.DownloadItemStatusDownloading},
		{"Downloading", false, downloadclients.DownloadItemStatusDownloading},
		{"Checking", false, downloadclients.DownloadItemStatusDownloading}, // default-arm at C# :181-184
		{"Checking", true, downloadclients.DownloadItemStatusDownloading},  // C# :169-172 excludes Checking
		{"weirdNewState", false, downloadclients.DownloadItemStatusDownloading},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.state, func(t *testing.T) {
			t.Parallel()
			body := `{"h":{"hash":"h","name":"X","state":"` + tc.state +
				`","is_finished":` + boolJSON(tc.isFinished) +
				`,"save_path":"/dl","total_size":100,"total_done":50}}`
			_, srv := newUpdateUIServer(t, body)
			d := newDelugeForGetItems(t, srv, nil)
			got, err := d.GetItems(context.Background())
			if err != nil {
				t.Fatalf("GetItems() err = %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("len = %d, want 1", len(got))
			}
			if got[0].Status != tc.wantStatus {
				t.Errorf("Status[%s, finished=%v] = %v, want %v",
					tc.state, tc.isFinished, got[0].Status, tc.wantStatus)
			}
		})
	}
}

func boolJSON(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func TestGetItems_ErrorStateSurfacesTorrentMessage(t *testing.T) {
	t.Parallel()
	body := `{"h":{"hash":"h","name":"X","state":"Error","message":"missing tracker"}}`
	_, srv := newUpdateUIServer(t, body)
	d := newDelugeForGetItems(t, srv, nil)
	got, _ := d.GetItems(context.Background())
	if got[0].Message != "missing tracker" {
		t.Errorf("Message = %q, want %q (torrent.Message takes precedence in error arm)",
			got[0].Message, "missing tracker")
	}
}

func TestGetItems_CategoryFilterPassed(t *testing.T) {
	t.Parallel()
	h, srv := newUpdateUIServer(t, `{}`)
	d := newDelugeForGetItems(t, srv, func(s *deluge.Settings) {
		s.TvCategory = "tv-sonarr"
	})
	if _, err := d.GetItems(context.Background()); err != nil {
		t.Fatalf("GetItems() err = %v", err)
	}
	filter, _ := h.lastFilter.Load().(map[string]any)
	if filter == nil || filter["label"] != "tv-sonarr" {
		t.Errorf("filter = %v, want {label: tv-sonarr}", filter)
	}
}

func TestGetItems_NoCategoryEmptyFilter(t *testing.T) {
	t.Parallel()
	h, srv := newUpdateUIServer(t, `{}`)
	d := newDelugeForGetItems(t, srv, func(s *deluge.Settings) {
		s.TvCategory = ""
	})
	if _, err := d.GetItems(context.Background()); err != nil {
		t.Fatalf("GetItems() err = %v", err)
	}
	filter, _ := h.lastFilter.Load().(map[string]any)
	if filter == nil {
		t.Fatal("filter map not captured")
	}
	if _, present := filter["label"]; present {
		t.Errorf("filter unexpectedly has 'label' key when TvCategory empty: %v", filter)
	}
}

func TestGetItems_RequiredFieldsForwarded(t *testing.T) {
	t.Parallel()
	h, srv := newUpdateUIServer(t, `{}`)
	d := newDelugeForGetItems(t, srv, nil)
	if _, err := d.GetItems(context.Background()); err != nil {
		t.Fatalf("GetItems() err = %v", err)
	}
	fields, _ := h.lastFields.Load().([]string)
	if len(fields) == 0 {
		t.Fatal("fields arg not captured")
	}
	// Spot-check a few critical fields the projection needs.
	want := []string{"hash", "name", "state", "total_size", "total_done", "save_path", "is_finished", "stop_at_ratio"}
	got := strings.Join(fields, ",")
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("fields = %v, missing %q", fields, w)
		}
	}
}

func TestGetItems_HashFromMapKeyFallback(t *testing.T) {
	t.Parallel()
	// Some deluge builds omit the in-value hash field.
	body := `{"keyhash":{"name":"X","state":"Downloading","save_path":"/dl","total_size":100,"total_done":50}}`
	_, srv := newUpdateUIServer(t, body)
	d := newDelugeForGetItems(t, srv, nil)
	got, _ := d.GetItems(context.Background())
	if len(got) != 1 || got[0].DownloadID != "KEYHASH" {
		t.Errorf("DownloadID = %q, want KEYHASH (from map key)", got[0].DownloadID)
	}
}

func TestGetItems_IgnoresTorrentsMissingHashOrName(t *testing.T) {
	t.Parallel()
	body := `{"":{"name":"X","state":"Downloading"},"h":{"hash":"h","name":"","state":"Downloading"},"good":{"hash":"good","name":"Y","state":"Downloading"}}`
	_, srv := newUpdateUIServer(t, body)
	d := newDelugeForGetItems(t, srv, nil)
	got, _ := d.GetItems(context.Background())
	if len(got) != 1 || got[0].DownloadID != "GOOD" {
		t.Errorf("expected only the well-formed torrent; got %+v", got)
	}
}

func TestGetItems_AuthFailureBubbles(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bs := string(body)
		if strings.Contains(bs, `"auth.login"`) {
			// Bad-password path.
			_, _ = io.WriteString(w, `{"result":false,"error":null,"id":1}`)
			return
		}
		_, _ = io.WriteString(w, `{"result":null,"error":{"code":3,"message":"boom"},"id":2}`)
	}))
	defer srv.Close()
	d := newDelugeForGetItems(t, srv, nil)
	_, err := d.GetItems(context.Background())
	if !errors.Is(err, deluge.ErrAuthFailed) {
		t.Errorf("GetItems() err = %v, want ErrAuthFailed", err)
	}
}

func TestGetItems_NilProxyErrors(t *testing.T) {
	t.Parallel()
	d := deluge.New(nil, deluge.NewSettings(), nil, nil)
	_, err := d.GetItems(context.Background())
	if err == nil {
		t.Fatal("GetItems(nil proxy) err = nil, want error")
	}
}

func TestGetItems_HasPostImportCategoryFlag(t *testing.T) {
	t.Parallel()
	body := `{"h":{"hash":"h","name":"X","state":"Downloading","save_path":"/dl","total_size":100,"total_done":50}}`
	_, srv := newUpdateUIServer(t, body)
	d := newDelugeForGetItems(t, srv, func(s *deluge.Settings) {
		s.TvImportedCategory = "imported"
	})
	got, _ := d.GetItems(context.Background())
	if !got[0].DownloadClientInfo.HasPostImportCategory {
		t.Error("HasPostImportCategory = false, want true")
	}
}
