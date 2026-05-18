package qbittorrent_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients/qbittorrent"
)

// ============================================================================
// QBittorrent.GetStatus() — slice 5.G1g. Ports C# QBittorrent
// GetStatus at QBittorrent.cs:400-438 + QBittorrentProxyV2.GetConfig
// at :88-94.
// ============================================================================

type prefsServer struct {
	body   string
	status atomic.Int32
	calls  atomic.Int32
}

func newPrefsServer(t *testing.T, body string) (*prefsServer, *httptest.Server) {
	h := &prefsServer{body: body}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "sid"})
			_, _ = io.WriteString(w, "Ok.")
		case "/api/v2/app/preferences":
			h.calls.Add(1)
			s := int(h.status.Load())
			if s == 0 {
				s = http.StatusOK
			}
			w.WriteHeader(s)
			_, _ = io.WriteString(w, h.body)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return h, srv
}

func newQBitForStatus(t *testing.T, srv *httptest.Server, mutate func(*qbittorrent.Settings)) *qbittorrent.QBittorrent {
	t.Helper()
	s := settingsFor(t, srv)
	if mutate != nil {
		mutate(&s)
	}
	proxy, err := qbittorrent.NewProxy(srv.Client(), nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	return qbittorrent.New(nil, s, proxy, nil)
}

func TestGetStatus_LocalhostBindIsLocalhost(t *testing.T) {
	t.Parallel()
	// httptest.NewServer binds to 127.0.0.1 — settingsFor() reflects
	// that host into Settings.Host so IsLocalhost == true.
	_, srv := newPrefsServer(t, `{"save_path":"/downloads","max_ratio_enabled":false,"max_seeding_time_enabled":false,"max_ratio_act":0}`)
	q := newQBitForStatus(t, srv, func(s *qbittorrent.Settings) {
		s.ApiKey = "k"
	})
	info, err := q.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus() err = %v", err)
	}
	if !info.IsLocalhost {
		t.Errorf("IsLocalhost = false, want true (httptest binds to 127.0.0.1)")
	}
	if len(info.OutputRootFolders) != 1 || info.OutputRootFolders[0].String() != "/downloads" {
		t.Errorf("OutputRootFolders = %v, want [/downloads]", info.OutputRootFolders)
	}
	if info.RemovesCompletedDownloads {
		t.Errorf("RemovesCompletedDownloads = true, want false (no caps enabled)")
	}
}

// hostRedirectingDoer routes every request to the httptest server
// while preserving the caller's URL host — lets the test override
// Settings.Host (which the proxy uses for IsLocalhost computation
// AND URL building) without losing test-server reachability.
type hostRedirectingDoer struct {
	target *httptest.Server
}

func (d *hostRedirectingDoer) Do(req *http.Request) (*http.Response, error) {
	tgt, _ := url.Parse(d.target.URL)
	req.URL.Scheme = tgt.Scheme
	req.URL.Host = tgt.Host
	req.Host = tgt.Host
	return d.target.Client().Do(req)
}

func TestGetStatus_HostnameOverrideNotLocalhost(t *testing.T) {
	t.Parallel()
	_, srv := newPrefsServer(t, `{"save_path":"/downloads","max_ratio_act":0}`)
	s := settingsFor(t, srv)
	s.ApiKey = "k"
	s.Host = "qbit.example.com"
	proxy, err := qbittorrent.NewProxy(&hostRedirectingDoer{target: srv}, nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	q := qbittorrent.New(nil, s, proxy, nil)
	info, err := q.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus() err = %v", err)
	}
	if info.IsLocalhost {
		t.Errorf("IsLocalhost = true, want false (host=qbit.example.com)")
	}
}

func TestGetStatus_LocalhostHostnameIsLocalhost(t *testing.T) {
	t.Parallel()
	_, srv := newPrefsServer(t, `{"save_path":"/d","max_ratio_act":0}`)
	s := settingsFor(t, srv)
	s.ApiKey = "k"
	s.Host = "LocalHost"
	proxy, _ := qbittorrent.NewProxy(&hostRedirectingDoer{target: srv}, nil)
	q := qbittorrent.New(nil, s, proxy, nil)
	info, err := q.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus() err = %v", err)
	}
	if !info.IsLocalhost {
		t.Errorf("IsLocalhost = false for Host=LocalHost, want case-insensitive true")
	}
}

func TestGetStatus_RemovesCompletedDownloads_RatioEnabled_ActionRemove(t *testing.T) {
	t.Parallel()
	_, srv := newPrefsServer(t, `{"save_path":"/d","max_ratio_enabled":true,"max_ratio":2.0,"max_seeding_time_enabled":false,"max_ratio_act":1}`)
	q := newQBitForStatus(t, srv, func(s *qbittorrent.Settings) { s.ApiKey = "k" })
	info, _ := q.GetStatus(context.Background())
	if !info.RemovesCompletedDownloads {
		t.Errorf("RemovesCompletedDownloads = false, want true (ratio enabled + action=Remove)")
	}
}

func TestGetStatus_RemovesCompletedDownloads_SeedTimeUnderFloor_ActionDelete(t *testing.T) {
	t.Parallel()
	// MaxSeedingTime = 1000 minutes < 14*24*60 floor → counted as
	// "limit reached". Action=DeleteFiles (3).
	_, srv := newPrefsServer(t, `{"save_path":"/d","max_ratio_enabled":false,"max_seeding_time_enabled":true,"max_seeding_time":1000,"max_ratio_act":3}`)
	q := newQBitForStatus(t, srv, func(s *qbittorrent.Settings) { s.ApiKey = "k" })
	info, _ := q.GetStatus(context.Background())
	if !info.RemovesCompletedDownloads {
		t.Errorf("RemovesCompletedDownloads = false, want true (seedtime=1000m + action=DeleteFiles)")
	}
}

func TestGetStatus_RemovesCompletedDownloads_SeedTimeAboveFloorNotRemoved(t *testing.T) {
	t.Parallel()
	// 14 days = 20160 minutes. Anything >= 20160 is above floor →
	// not "limit reached".
	_, srv := newPrefsServer(t, `{"save_path":"/d","max_ratio_enabled":false,"max_seeding_time_enabled":true,"max_seeding_time":30000,"max_ratio_act":1}`)
	q := newQBitForStatus(t, srv, func(s *qbittorrent.Settings) { s.ApiKey = "k" })
	info, _ := q.GetStatus(context.Background())
	if info.RemovesCompletedDownloads {
		t.Errorf("RemovesCompletedDownloads = true, want false (seedtime=30000m > 14-day floor)")
	}
}

func TestGetStatus_RemovesCompletedDownloads_ActionPauseNotRemoved(t *testing.T) {
	t.Parallel()
	// Caps reached but action=Pause (0) → qBittorrent doesn't
	// actually delete → Sonarr can still safely remove.
	_, srv := newPrefsServer(t, `{"save_path":"/d","max_ratio_enabled":true,"max_ratio":1.0,"max_ratio_act":0}`)
	q := newQBitForStatus(t, srv, func(s *qbittorrent.Settings) { s.ApiKey = "k" })
	info, _ := q.GetStatus(context.Background())
	if info.RemovesCompletedDownloads {
		t.Errorf("RemovesCompletedDownloads = true, want false (action=Pause)")
	}
}

func TestGetStatus_EmptySavePathOmitsOutputRootFolders(t *testing.T) {
	t.Parallel()
	_, srv := newPrefsServer(t, `{"save_path":"","max_ratio_act":0}`)
	q := newQBitForStatus(t, srv, func(s *qbittorrent.Settings) { s.ApiKey = "k" })
	info, err := q.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus() err = %v", err)
	}
	if len(info.OutputRootFolders) != 0 {
		t.Errorf("OutputRootFolders = %v, want empty when save_path is blank", info.OutputRootFolders)
	}
}

func TestGetStatus_Server500ReturnsError(t *testing.T) {
	t.Parallel()
	h, srv := newPrefsServer(t, "")
	h.status.Store(http.StatusInternalServerError)
	q := newQBitForStatus(t, srv, func(s *qbittorrent.Settings) { s.ApiKey = "k" })
	_, err := q.GetStatus(context.Background())
	if !errors.Is(err, qbittorrent.ErrUnexpected) {
		t.Errorf("GetStatus() err = %v, want ErrUnexpected", err)
	}
}

func TestGetStatus_AuthBubbles(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "sid"})
			_, _ = io.WriteString(w, "Ok.")
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	q := newQBitForStatus(t, srv, func(s *qbittorrent.Settings) {
		s.Username = "u"
		s.Password = "p"
	})
	_, err := q.GetStatus(context.Background())
	if !errors.Is(err, qbittorrent.ErrAuthFailed) {
		t.Errorf("GetStatus() err = %v, want ErrAuthFailed", err)
	}
}

func TestGetStatus_NilProxyReturnsError(t *testing.T) {
	t.Parallel()
	q := qbittorrent.New(nil, qbittorrent.NewSettings(), nil, nil)
	_, err := q.GetStatus(context.Background())
	if err == nil {
		t.Fatal("GetStatus(nil proxy) err = nil, want error")
	}
}

func TestGetStatus_MalformedJSONErrors(t *testing.T) {
	t.Parallel()
	_, srv := newPrefsServer(t, "{not-json")
	q := newQBitForStatus(t, srv, func(s *qbittorrent.Settings) { s.ApiKey = "k" })
	_, err := q.GetStatus(context.Background())
	if err == nil {
		t.Fatal("GetStatus(malformed) err = nil, want parse error")
	}
}
