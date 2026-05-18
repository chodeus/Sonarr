package deluge_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/deluge"
)

// ============================================================================
// Deluge.Download() — slice 5.G2e. Ports C# Deluge.AddFromMagnetLink /
// AddFromTorrentFile at Deluge.cs:58-110 + DelugeProxy.AddTorrent* at
// :103-150.
// ============================================================================

// addServer captures every JSON-RPC method invocation so per-test
// assertions can inspect the params + counts.
type addServer struct {
	addMagnetCalls atomic.Int32
	addFileCalls   atomic.Int32
	labelCalls     atomic.Int32
	queueTopCalls  atomic.Int32
	seedingCalls   atomic.Int32

	addMagnetParams atomic.Value // []any
	addFileParams   atomic.Value // []any
	labelParams     atomic.Value // []any
	queueTopParams  atomic.Value // []any
	seedingParams   atomic.Value // []any

	// labelResponseError: when non-empty, the label.set_torrent
	// RPC returns an error-object so the failure-swallow branch
	// can be exercised.
	labelResponseError string
}

func newAddServer(t *testing.T) (*addServer, *httptest.Server) {
	h := &addServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var env struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			ID     int             `json:"id"`
		}
		_ = json.Unmarshal(body, &env)

		respondRPCOK := func(result string) {
			_, _ = io.WriteString(w, `{"result":`+result+`,"error":null,"id":`+intStr(env.ID)+`}`)
		}
		respondRPCErr := func(code int, msg string) {
			_, _ = io.WriteString(w, `{"result":null,"error":{"code":`+intStr(code)+`,"message":"`+msg+`"},"id":`+intStr(env.ID)+`}`)
		}
		captureParams := func() []any {
			var p []any
			_ = json.Unmarshal(env.Params, &p)
			return p
		}

		switch env.Method {
		case "auth.login":
			http.SetCookie(w, &http.Cookie{Name: "_session_id", Value: "sid"})
			respondRPCOK("true")
		case "core.add_torrent_magnet":
			h.addMagnetCalls.Add(1)
			h.addMagnetParams.Store(captureParams())
			respondRPCOK(`"DEADBEEFCAFEBABEDEADBEEFCAFEBABEDEADBEEF"`)
		case "core.add_torrent_file":
			h.addFileCalls.Add(1)
			h.addFileParams.Store(captureParams())
			respondRPCOK(`"ABCDEF0011223344556677889900AABBCCDDEEFF"`)
		case "label.set_torrent":
			h.labelCalls.Add(1)
			h.labelParams.Store(captureParams())
			if h.labelResponseError != "" {
				respondRPCErr(7, h.labelResponseError)
				return
			}
			respondRPCOK("null")
		case "core.queue_top":
			h.queueTopCalls.Add(1)
			h.queueTopParams.Store(captureParams())
			respondRPCOK("null")
		case "core.set_torrent_options":
			h.seedingCalls.Add(1)
			h.seedingParams.Store(captureParams())
			respondRPCOK("null")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return h, srv
}

func intStr(n int) string {
	if n == 0 {
		return "0"
	}
	// Avoid pulling strconv into the test_helpers file again.
	digits := []byte{}
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}

func newDelugeForDownload(t *testing.T, srv *httptest.Server, mutate func(*deluge.Settings)) *deluge.Deluge {
	t.Helper()
	s := settingsFor(t, srv)
	if mutate != nil {
		mutate(&s)
	}
	proxy, err := deluge.NewProxy(srv.Client(), nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	return deluge.New(&downloadclients.DownloadClient{ID: 7, Name: "d"}, s, proxy, nil)
}

func TestDownload_NilRequest(t *testing.T) {
	t.Parallel()
	d := deluge.New(nil, deluge.NewSettings(), nil, nil)
	if _, err := d.Download(context.Background(), nil); err == nil {
		t.Fatal("Download(nil) err = nil, want error")
	}
}

func TestDownload_NilProxy(t *testing.T) {
	t.Parallel()
	d := deluge.New(nil, deluge.NewSettings(), nil, nil)
	_, err := d.Download(context.Background(), &downloadclients.GrabRequest{MagnetURL: "magnet:?xt=urn:btih:abc"})
	if err == nil {
		t.Fatal("Download(nil proxy) err = nil, want error")
	}
}

func TestDownload_NoSourceErrors(t *testing.T) {
	t.Parallel()
	_, srv := newAddServer(t)
	d := newDelugeForDownload(t, srv, nil)
	_, err := d.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "Show.S01E01",
		DownloadURL:  "https://indexer.example/dl.torrent",
		// No MagnetURL, no TorrentBody — deluge has no fetch-url add.
	})
	if err == nil {
		t.Fatal("Download(DownloadURL only) err = nil, want error")
	}
}

func TestDownload_Magnet(t *testing.T) {
	t.Parallel()
	h, srv := newAddServer(t)
	d := newDelugeForDownload(t, srv, nil)

	hash, err := d.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "Show.S01E01",
		MagnetURL:    "magnet:?xt=urn:btih:abc",
	})
	if err != nil {
		t.Fatalf("Download() err = %v", err)
	}
	if hash != "DEADBEEFCAFEBABEDEADBEEFCAFEBABEDEADBEEF" {
		t.Errorf("Download() = %q, want upper-cased server hash", hash)
	}
	if h.addMagnetCalls.Load() != 1 {
		t.Errorf("add_torrent_magnet fired %d times, want 1", h.addMagnetCalls.Load())
	}
	if h.addFileCalls.Load() != 0 {
		t.Errorf("add_torrent_file fired despite magnet path")
	}

	params, _ := h.addMagnetParams.Load().([]any)
	if len(params) != 2 {
		t.Fatalf("params = %v, want 2 args", params)
	}
	if params[0] != "magnet:?xt=urn:btih:abc" {
		t.Errorf("params[0] = %v, want the magnet", params[0])
	}
	opts, _ := params[1].(map[string]any)
	if opts == nil {
		t.Fatal("options map missing")
	}
	if got := opts["remove_at_ratio"]; got != false {
		t.Errorf("remove_at_ratio = %v, want false (Sonarr controls removal)", got)
	}
}

func TestDownload_TorrentBytes(t *testing.T) {
	t.Parallel()
	h, srv := newAddServer(t)
	d := newDelugeForDownload(t, srv, nil)
	payload := []byte("d4:infod4:name3:abceee")

	hash, err := d.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "Show.S01E01",
		TorrentBody:  payload,
	})
	if err != nil {
		t.Fatalf("Download() err = %v", err)
	}
	if hash != "ABCDEF0011223344556677889900AABBCCDDEEFF" {
		t.Errorf("Download() = %q", hash)
	}
	if h.addFileCalls.Load() != 1 {
		t.Errorf("add_torrent_file fired %d times, want 1", h.addFileCalls.Load())
	}
	params, _ := h.addFileParams.Load().([]any)
	if len(params) != 3 {
		t.Fatalf("params = %v, want 3 args (filename, base64, options)", params)
	}
	if !strings.HasSuffix(params[0].(string), ".torrent") {
		t.Errorf("filename = %v, want suffix .torrent", params[0])
	}
	encoded, _ := params[1].(string)
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if string(decoded) != string(payload) {
		t.Errorf("decoded payload mismatch: got %q, want %q", decoded, payload)
	}
}

func TestDownload_TvCategoryFiresLabelSet(t *testing.T) {
	t.Parallel()
	h, srv := newAddServer(t)
	d := newDelugeForDownload(t, srv, func(s *deluge.Settings) {
		s.TvCategory = "tv-sonarr"
	})
	_, err := d.Download(context.Background(), &downloadclients.GrabRequest{
		MagnetURL: "magnet:?xt=urn:btih:abc",
	})
	if err != nil {
		t.Fatalf("Download() err = %v", err)
	}
	if h.labelCalls.Load() != 1 {
		t.Errorf("label.set_torrent fired %d times, want 1", h.labelCalls.Load())
	}
	params, _ := h.labelParams.Load().([]any)
	if len(params) != 2 {
		t.Fatalf("label params = %v, want 2", params)
	}
	if params[0] != "deadbeefcafebabedeadbeefcafebabedeadbeef" {
		t.Errorf("label[0] = %v, want lowercase hash", params[0])
	}
	if params[1] != "tv-sonarr" {
		t.Errorf("label[1] = %v, want tv-sonarr", params[1])
	}
}

func TestDownload_NoTvCategorySkipsLabelSet(t *testing.T) {
	t.Parallel()
	h, srv := newAddServer(t)
	d := newDelugeForDownload(t, srv, func(s *deluge.Settings) {
		s.TvCategory = ""
	})
	_, err := d.Download(context.Background(), &downloadclients.GrabRequest{
		MagnetURL: "magnet:?xt=urn:btih:abc",
	})
	if err != nil {
		t.Fatalf("Download() err = %v", err)
	}
	if h.labelCalls.Load() != 0 {
		t.Errorf("label.set_torrent fired despite empty TvCategory")
	}
}

func TestDownload_LabelFailureSwallowed(t *testing.T) {
	t.Parallel()
	h, srv := newAddServer(t)
	h.labelResponseError = "label plugin not loaded"
	d := newDelugeForDownload(t, srv, func(s *deluge.Settings) {
		s.TvCategory = "tv-sonarr"
	})
	hash, err := d.Download(context.Background(), &downloadclients.GrabRequest{
		MagnetURL: "magnet:?xt=urn:btih:abc",
	})
	if err != nil {
		t.Fatalf("Download() err = %v (label failure should be swallowed)", err)
	}
	if hash == "" {
		t.Error("hash empty despite label failure")
	}
}

func TestDownload_SeedConfigurationFiresOptions(t *testing.T) {
	t.Parallel()
	h, srv := newAddServer(t)
	d := newDelugeForDownload(t, srv, nil)

	ratio := 1.5
	seedTime := 90 * time.Minute
	_, err := d.Download(context.Background(), &downloadclients.GrabRequest{
		MagnetURL: "magnet:?xt=urn:btih:abc",
		SeedConfiguration: &downloadclients.TorrentSeedConfiguration{
			Ratio:    &ratio,
			SeedTime: &seedTime,
		},
	})
	if err != nil {
		t.Fatalf("Download() err = %v", err)
	}
	if h.seedingCalls.Load() != 1 {
		t.Errorf("core.set_torrent_options fired %d times, want 1", h.seedingCalls.Load())
	}
	params, _ := h.seedingParams.Load().([]any)
	if len(params) != 2 {
		t.Fatalf("seeding params = %v, want 2", params)
	}
	args, _ := params[1].(map[string]any)
	if args == nil {
		t.Fatal("args map missing")
	}
	if args["stop_ratio"] != 1.5 {
		t.Errorf("stop_ratio = %v, want 1.5", args["stop_ratio"])
	}
	if args["stop_at_ratio"] != float64(1) {
		t.Errorf("stop_at_ratio = %v, want 1", args["stop_at_ratio"])
	}
}

func TestDownload_NoSeedConfigSkipsOptions(t *testing.T) {
	t.Parallel()
	h, srv := newAddServer(t)
	d := newDelugeForDownload(t, srv, nil)
	_, err := d.Download(context.Background(), &downloadclients.GrabRequest{
		MagnetURL: "magnet:?xt=urn:btih:abc",
	})
	if err != nil {
		t.Fatalf("Download() err = %v", err)
	}
	if h.seedingCalls.Load() != 0 {
		t.Errorf("set_torrent_options fired despite nil seedConfig")
	}
}

func TestDownload_RecentPriorityFirstFiresQueueTop(t *testing.T) {
	t.Parallel()
	h, srv := newAddServer(t)
	d := newDelugeForDownload(t, srv, func(s *deluge.Settings) {
		s.RecentTvPriority = deluge.PriorityFirst
	})
	_, err := d.Download(context.Background(), &downloadclients.GrabRequest{
		MagnetURL: "magnet:?xt=urn:btih:abc",
		IsRecent:  true,
	})
	if err != nil {
		t.Fatalf("Download() err = %v", err)
	}
	if h.queueTopCalls.Load() != 1 {
		t.Errorf("queue_top fired %d times, want 1", h.queueTopCalls.Load())
	}
}

func TestDownload_OlderPriorityFirstFiresQueueTop(t *testing.T) {
	t.Parallel()
	h, srv := newAddServer(t)
	d := newDelugeForDownload(t, srv, func(s *deluge.Settings) {
		s.OlderTvPriority = deluge.PriorityFirst
	})
	_, err := d.Download(context.Background(), &downloadclients.GrabRequest{
		MagnetURL: "magnet:?xt=urn:btih:abc",
		IsRecent:  false,
	})
	if err != nil {
		t.Fatalf("Download() err = %v", err)
	}
	if h.queueTopCalls.Load() != 1 {
		t.Errorf("queue_top fired %d times, want 1", h.queueTopCalls.Load())
	}
}

func TestDownload_PriorityLastSkipsQueueTop(t *testing.T) {
	t.Parallel()
	h, srv := newAddServer(t)
	d := newDelugeForDownload(t, srv, nil) // both priorities default to Last (0)
	_, err := d.Download(context.Background(), &downloadclients.GrabRequest{
		MagnetURL: "magnet:?xt=urn:btih:abc",
		IsRecent:  true,
	})
	if err != nil {
		t.Fatalf("Download() err = %v", err)
	}
	if h.queueTopCalls.Load() != 0 {
		t.Errorf("queue_top fired %d times, want 0", h.queueTopCalls.Load())
	}
}

func TestDownload_AddPausedWiredIntoOptions(t *testing.T) {
	t.Parallel()
	h, srv := newAddServer(t)
	d := newDelugeForDownload(t, srv, func(s *deluge.Settings) {
		s.AddPaused = true
	})
	_, err := d.Download(context.Background(), &downloadclients.GrabRequest{
		MagnetURL: "magnet:?xt=urn:btih:abc",
	})
	if err != nil {
		t.Fatalf("Download() err = %v", err)
	}
	params, _ := h.addMagnetParams.Load().([]any)
	opts := params[1].(map[string]any)
	if opts["add_paused"] != true {
		t.Errorf("add_paused = %v, want true", opts["add_paused"])
	}
}

func TestDownload_DownloadAndCompletedDirectoriesWired(t *testing.T) {
	t.Parallel()
	h, srv := newAddServer(t)
	d := newDelugeForDownload(t, srv, func(s *deluge.Settings) {
		s.DownloadDirectory = "/dl/incoming"
		s.CompletedDirectory = "/dl/completed"
	})
	_, err := d.Download(context.Background(), &downloadclients.GrabRequest{
		MagnetURL: "magnet:?xt=urn:btih:abc",
	})
	if err != nil {
		t.Fatalf("Download() err = %v", err)
	}
	params, _ := h.addMagnetParams.Load().([]any)
	opts := params[1].(map[string]any)
	if opts["download_location"] != "/dl/incoming" {
		t.Errorf("download_location = %v", opts["download_location"])
	}
	if opts["move_completed_path"] != "/dl/completed" {
		t.Errorf("move_completed_path = %v", opts["move_completed_path"])
	}
	if opts["move_completed"] != true {
		t.Errorf("move_completed = %v, want true", opts["move_completed"])
	}
}

func TestDownload_AddFailureReturnsError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bs := string(body)
		if strings.Contains(bs, `"auth.login"`) {
			http.SetCookie(w, &http.Cookie{Name: "_session_id", Value: "sid"})
			_, _ = io.WriteString(w, `{"result":true,"error":null,"id":1}`)
			return
		}
		// add_torrent_magnet returns null → ErrAddFailed.
		_, _ = io.WriteString(w, `{"result":null,"error":null,"id":2}`)
	}))
	defer srv.Close()
	d := newDelugeForDownload(t, srv, nil)
	_, err := d.Download(context.Background(), &downloadclients.GrabRequest{
		MagnetURL: "magnet:?xt=urn:btih:abc",
	})
	if !errors.Is(err, deluge.ErrAddFailed) {
		t.Errorf("Download() err = %v, want ErrAddFailed", err)
	}
}
