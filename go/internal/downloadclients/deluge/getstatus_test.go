package deluge_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients/deluge"
)

// ============================================================================
// Deluge.GetStatus() — slice 5.G2g. Ports C# Deluge.GetStatus at
// Deluge.cs:222-262 — the 4-way destDir precedence (CompletedDirectory
// > DownloadDirectory > label-move > config-move-completed >
// config-download-location).
// ============================================================================

type statusServer struct {
	configBody     string // JSON for core.get_config result
	configErr      string // RPC error message (when non-empty, error returned)
	labelBody      string // JSON for label.get_options result
	labelErr       string // RPC error message
	configCalls    atomic.Int32
	labelCalls     atomic.Int32
	lastLabelParam atomic.Value // string
}

func newStatusServer(t *testing.T) (*statusServer, *httptest.Server) {
	h := &statusServer{
		configBody: `{}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var env struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			ID     int             `json:"id"`
		}
		_ = json.Unmarshal(body, &env)
		respondOK := func(result string) {
			_, _ = io.WriteString(w, `{"result":`+result+`,"error":null,"id":`+intStr(env.ID)+`}`)
		}
		respondErr := func(msg string) {
			_, _ = io.WriteString(w, `{"result":null,"error":{"code":7,"message":"`+msg+`"},"id":`+intStr(env.ID)+`}`)
		}
		switch env.Method {
		case "auth.login":
			http.SetCookie(w, &http.Cookie{Name: "_session_id", Value: "sid"})
			respondOK("true")
		case "core.get_config":
			h.configCalls.Add(1)
			if h.configErr != "" {
				respondErr(h.configErr)
				return
			}
			respondOK(h.configBody)
		case "label.get_options":
			h.labelCalls.Add(1)
			var p []string
			_ = json.Unmarshal(env.Params, &p)
			if len(p) > 0 {
				h.lastLabelParam.Store(p[0])
			}
			if h.labelErr != "" {
				respondErr(h.labelErr)
				return
			}
			respondOK(h.labelBody)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return h, srv
}

func newDelugeForStatus(t *testing.T, srv *httptest.Server, mutate func(*deluge.Settings)) *deluge.Deluge {
	t.Helper()
	s := settingsFor(t, srv)
	if mutate != nil {
		mutate(&s)
	}
	proxy, err := deluge.NewProxy(srv.Client(), nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	return deluge.New(nil, s, proxy, nil)
}

// hostRedirectingDoer routes every request to the httptest server
// while preserving the caller's URL host — lets the test override
// Settings.Host (which the proxy uses for IsLocalhost AND URL
// building) without losing test-server reachability. Same shape as
// the qbittorrent 5.G1g helper.
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

// ----------------------------------------------------------------------------
// 4-way destDir precedence
// ----------------------------------------------------------------------------

func TestGetStatus_CompletedDirectoryWinsOutright(t *testing.T) {
	t.Parallel()
	h, srv := newStatusServer(t)
	h.configBody = `{"move_completed":true,"move_completed_path":"/cfg/completed","download_location":"/cfg/download"}`
	d := newDelugeForStatus(t, srv, func(s *deluge.Settings) {
		s.CompletedDirectory = "/user/completed"
		s.DownloadDirectory = "/user/download"
		s.TvCategory = "tv-sonarr"
	})
	info, err := d.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus() err = %v", err)
	}
	if len(info.OutputRootFolders) != 1 || info.OutputRootFolders[0].String() != "/user/completed" {
		t.Errorf("OutputRootFolders = %v, want /user/completed", info.OutputRootFolders)
	}
	if h.configCalls.Load() != 0 {
		t.Errorf("core.get_config fired despite CompletedDirectory override")
	}
	if h.labelCalls.Load() != 0 {
		t.Errorf("label.get_options fired despite CompletedDirectory override")
	}
}

func TestGetStatus_DownloadDirectoryUsedWhenCompletedEmpty(t *testing.T) {
	t.Parallel()
	h, srv := newStatusServer(t)
	d := newDelugeForStatus(t, srv, func(s *deluge.Settings) {
		s.CompletedDirectory = ""
		s.DownloadDirectory = "/user/download"
		s.TvCategory = "tv-sonarr"
	})
	info, err := d.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus() err = %v", err)
	}
	if info.OutputRootFolders[0].String() != "/user/download" {
		t.Errorf("OutputRootFolders = %v, want /user/download", info.OutputRootFolders)
	}
	if h.configCalls.Load() != 0 {
		t.Errorf("core.get_config fired despite DownloadDirectory override")
	}
}

func TestGetStatus_LabelMoveCompletedPathArm(t *testing.T) {
	t.Parallel()
	h, srv := newStatusServer(t)
	h.labelBody = `{"apply_move_completed":true,"move_completed":true,"move_completed_path":"/label/completed"}`
	h.configBody = `{"download_location":"/cfg/download"}`
	d := newDelugeForStatus(t, srv, func(s *deluge.Settings) {
		s.TvCategory = "tv-sonarr"
	})
	info, err := d.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus() err = %v", err)
	}
	if info.OutputRootFolders[0].String() != "/label/completed" {
		t.Errorf("OutputRootFolders = %v, want /label/completed (label-arm wins)", info.OutputRootFolders)
	}
	got, _ := h.lastLabelParam.Load().(string)
	if got != "tv-sonarr" {
		t.Errorf("label arg = %q, want tv-sonarr", got)
	}
	if h.configCalls.Load() != 0 {
		t.Errorf("core.get_config fired despite label-arm winning")
	}
}

func TestGetStatus_LabelMoveCompletedDisabledFallsThrough(t *testing.T) {
	t.Parallel()
	h, srv := newStatusServer(t)
	// MoveCompleted=false → label arm doesn't fire.
	h.labelBody = `{"apply_move_completed":true,"move_completed":false,"move_completed_path":"/label/path"}`
	h.configBody = `{"download_location":"/cfg/download"}`
	d := newDelugeForStatus(t, srv, func(s *deluge.Settings) {
		s.TvCategory = "tv-sonarr"
	})
	info, _ := d.GetStatus(context.Background())
	if info.OutputRootFolders[0].String() != "/cfg/download" {
		t.Errorf("OutputRootFolders = %v, want /cfg/download (label arm should fall through)", info.OutputRootFolders)
	}
}

func TestGetStatus_LabelPluginMissingFallsThrough(t *testing.T) {
	t.Parallel()
	h, srv := newStatusServer(t)
	h.labelErr = "Method label.get_options doesn't exist"
	h.configBody = `{"download_location":"/cfg/download"}`
	d := newDelugeForStatus(t, srv, func(s *deluge.Settings) {
		s.TvCategory = "tv-sonarr"
	})
	info, err := d.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus() err = %v (label-plugin missing should fall through, not propagate)", err)
	}
	if info.OutputRootFolders[0].String() != "/cfg/download" {
		t.Errorf("OutputRootFolders = %v, want /cfg/download", info.OutputRootFolders)
	}
}

func TestGetStatus_ConfigMoveCompletedArm(t *testing.T) {
	t.Parallel()
	h, srv := newStatusServer(t)
	h.configBody = `{"move_completed":true,"move_completed_path":"/cfg/completed","download_location":"/cfg/download"}`
	d := newDelugeForStatus(t, srv, func(s *deluge.Settings) {
		s.TvCategory = "" // skip label arm so config arm runs
	})
	info, err := d.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus() err = %v", err)
	}
	if info.OutputRootFolders[0].String() != "/cfg/completed" {
		t.Errorf("OutputRootFolders = %v, want /cfg/completed (move_completed=true)", info.OutputRootFolders)
	}
}

func TestGetStatus_ConfigDownloadLocationFallback(t *testing.T) {
	t.Parallel()
	h, srv := newStatusServer(t)
	h.configBody = `{"move_completed":false,"download_location":"/cfg/download"}`
	d := newDelugeForStatus(t, srv, func(s *deluge.Settings) {
		s.TvCategory = ""
	})
	info, err := d.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus() err = %v", err)
	}
	if info.OutputRootFolders[0].String() != "/cfg/download" {
		t.Errorf("OutputRootFolders = %v, want /cfg/download", info.OutputRootFolders)
	}
}

func TestGetStatus_EmptyConfigOmitsOutputRootFolders(t *testing.T) {
	t.Parallel()
	_, srv := newStatusServer(t)
	d := newDelugeForStatus(t, srv, func(s *deluge.Settings) {
		s.TvCategory = ""
	})
	info, err := d.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus() err = %v", err)
	}
	if len(info.OutputRootFolders) != 0 {
		t.Errorf("OutputRootFolders = %v, want empty", info.OutputRootFolders)
	}
}

// ----------------------------------------------------------------------------
// IsLocalhost detection
// ----------------------------------------------------------------------------

func TestGetStatus_LocalhostBindIsLocalhost(t *testing.T) {
	t.Parallel()
	_, srv := newStatusServer(t)
	d := newDelugeForStatus(t, srv, func(s *deluge.Settings) {
		s.CompletedDirectory = "/x"
	})
	info, _ := d.GetStatus(context.Background())
	if !info.IsLocalhost {
		t.Errorf("IsLocalhost = false, want true (httptest binds to 127.0.0.1)")
	}
}

func TestGetStatus_LocalhostHostnameCaseInsensitive(t *testing.T) {
	t.Parallel()
	_, srv := newStatusServer(t)
	s := settingsFor(t, srv)
	s.CompletedDirectory = "/x"
	s.Host = "LocalHost"
	proxy, _ := deluge.NewProxy(&hostRedirectingDoer{target: srv}, nil)
	d := deluge.New(nil, s, proxy, nil)
	info, err := d.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus() err = %v", err)
	}
	if !info.IsLocalhost {
		t.Errorf("IsLocalhost = false for Host=LocalHost, want case-insensitive true")
	}
}

func TestGetStatus_NonLocalhostHostname(t *testing.T) {
	t.Parallel()
	_, srv := newStatusServer(t)
	s := settingsFor(t, srv)
	s.CompletedDirectory = "/x"
	s.Host = "deluge.example.com"
	proxy, _ := deluge.NewProxy(&hostRedirectingDoer{target: srv}, nil)
	d := deluge.New(nil, s, proxy, nil)
	info, err := d.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus() err = %v", err)
	}
	if info.IsLocalhost {
		t.Errorf("IsLocalhost = true, want false (Host=deluge.example.com)")
	}
}

// ----------------------------------------------------------------------------
// Failure surfaces
// ----------------------------------------------------------------------------

func TestGetStatus_ConfigRPCFailureBubbles(t *testing.T) {
	t.Parallel()
	h, srv := newStatusServer(t)
	h.configErr = "boom"
	d := newDelugeForStatus(t, srv, func(s *deluge.Settings) {
		s.TvCategory = ""
	})
	_, err := d.GetStatus(context.Background())
	if !errors.Is(err, deluge.ErrRPCFailure) {
		t.Errorf("GetStatus() err = %v, want ErrRPCFailure", err)
	}
}

func TestGetStatus_AuthBubbles(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// auth.login returns false → bad-password.
		_, _ = io.WriteString(w, `{"result":false,"error":null,"id":1}`)
	}))
	defer srv.Close()
	d := newDelugeForStatus(t, srv, nil)
	_, err := d.GetStatus(context.Background())
	if !errors.Is(err, deluge.ErrAuthFailed) {
		t.Errorf("GetStatus() err = %v, want ErrAuthFailed", err)
	}
}

func TestGetStatus_NilProxy(t *testing.T) {
	t.Parallel()
	d := deluge.New(nil, deluge.NewSettings(), nil, nil)
	_, err := d.GetStatus(context.Background())
	if err == nil {
		t.Fatal("GetStatus(nil proxy) err = nil, want error")
	}
}
