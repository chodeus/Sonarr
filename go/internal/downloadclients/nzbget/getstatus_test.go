package nzbget_test

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

	"sonarr.tv/sonarr/internal/downloadclients/nzbget"
)

// ============================================================================
// Nzbget.GetStatus() — slice 5.G3g. Ports C# Nzbget.GetStatus +
// GetCategories at Nzbget.cs:212-263 + NzbgetProxy.GetConfig at
// NzbgetProxy.cs:144-147.
// ============================================================================

// statusServer serves the `config` RPC with the caller-supplied
// config items.
type statusServer struct {
	configItems []nzbget.NzbgetConfigItem
	configErr   string

	calls atomic.Int32
}

func newStatusServer(t *testing.T, items []nzbget.NzbgetConfigItem) (*statusServer, *httptest.Server) {
	h := &statusServer{configItems: items}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var env struct {
			Method string `json:"method"`
			ID     int    `json:"id"`
		}
		_ = json.Unmarshal(body, &env)
		if env.Method != "config" {
			http.NotFound(w, r)
			return
		}
		h.calls.Add(1)
		if h.configErr != "" {
			_, _ = io.WriteString(w, `{"version":"1.1","error":{"name":"E","code":1,"message":"`+h.configErr+`"}}`)
			return
		}
		raw, _ := json.Marshal(h.configItems)
		_, _ = io.WriteString(w, `{"version":"1.1","result":`+string(raw)+`}`)
	}))
	t.Cleanup(srv.Close)
	return h, srv
}

func newNzbgetForStatus(t *testing.T, srv *httptest.Server, mutate func(*nzbget.Settings)) *nzbget.Nzbget {
	t.Helper()
	s := settingsFor(t, srv)
	if mutate != nil {
		mutate(&s)
	}
	proxy, err := nzbget.NewProxy(srv.Client(), nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	return nzbget.New(nil, s, proxy, nil)
}

// hostRedirectingDoer routes every request to the httptest server
// while preserving the caller's URL host — same shape as the
// qbittorrent 5.G1g / deluge 5.G2g helpers.
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

func TestGetStatus_MatchingCategoryDestDir(t *testing.T) {
	t.Parallel()
	_, srv := newStatusServer(t, []nzbget.NzbgetConfigItem{
		{Name: "Category1.Name", Value: "tv"},
		{Name: "Category1.DestDir", Value: "/downloads/tv"},
	})
	n := newNzbgetForStatus(t, srv, func(s *nzbget.Settings) {
		s.TvCategory = "tv"
	})
	info, err := n.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus() err = %v", err)
	}
	if len(info.OutputRootFolders) != 1 || info.OutputRootFolders[0].String() != "/downloads/tv" {
		t.Errorf("OutputRootFolders = %v, want /downloads/tv", info.OutputRootFolders)
	}
}

func TestGetStatus_MultipleCategoriesOneMatches(t *testing.T) {
	t.Parallel()
	_, srv := newStatusServer(t, []nzbget.NzbgetConfigItem{
		{Name: "Category1.Name", Value: "movies"},
		{Name: "Category1.DestDir", Value: "/downloads/movies"},
		{Name: "Category2.Name", Value: "tv"},
		{Name: "Category2.DestDir", Value: "/downloads/tv"},
		{Name: "Category3.Name", Value: "music"},
		{Name: "Category3.DestDir", Value: "/downloads/music"},
	})
	n := newNzbgetForStatus(t, srv, func(s *nzbget.Settings) {
		s.TvCategory = "tv"
	})
	info, _ := n.GetStatus(context.Background())
	if len(info.OutputRootFolders) != 1 || info.OutputRootFolders[0].String() != "/downloads/tv" {
		t.Errorf("OutputRootFolders = %v, want only /downloads/tv", info.OutputRootFolders)
	}
}

func TestGetStatus_NoMatchingCategoryOmits(t *testing.T) {
	t.Parallel()
	_, srv := newStatusServer(t, []nzbget.NzbgetConfigItem{
		{Name: "Category1.Name", Value: "movies"},
		{Name: "Category1.DestDir", Value: "/downloads/movies"},
	})
	n := newNzbgetForStatus(t, srv, func(s *nzbget.Settings) {
		s.TvCategory = "tv"
	})
	info, _ := n.GetStatus(context.Background())
	if len(info.OutputRootFolders) != 0 {
		t.Errorf("OutputRootFolders = %v, want empty (no matching category)", info.OutputRootFolders)
	}
}

func TestGetStatus_EmptyTvCategorySkipsLookup(t *testing.T) {
	t.Parallel()
	_, srv := newStatusServer(t, []nzbget.NzbgetConfigItem{
		{Name: "Category1.Name", Value: "tv"},
		{Name: "Category1.DestDir", Value: "/downloads/tv"},
	})
	n := newNzbgetForStatus(t, srv, func(s *nzbget.Settings) {
		s.TvCategory = ""
	})
	info, _ := n.GetStatus(context.Background())
	if len(info.OutputRootFolders) != 0 {
		t.Errorf("OutputRootFolders = %v, want empty (no TvCategory)", info.OutputRootFolders)
	}
}

func TestGetStatus_EmptyDestDirFallsBackToMainDir(t *testing.T) {
	t.Parallel()
	_, srv := newStatusServer(t, []nzbget.NzbgetConfigItem{
		{Name: "MainDir", Value: "/nzbget"},
		{Name: "DestDir", Value: "${MainDir}/dl"},
		{Name: "AppendCategoryDir", Value: "yes"},
		{Name: "Category1.Name", Value: "tv"},
		{Name: "Category1.DestDir", Value: ""}, // empty → fallback
	})
	n := newNzbgetForStatus(t, srv, func(s *nzbget.Settings) {
		s.TvCategory = "tv"
	})
	info, _ := n.GetStatus(context.Background())
	if len(info.OutputRootFolders) != 1 || info.OutputRootFolders[0].String() != "/nzbget/dl/tv" {
		t.Errorf("OutputRootFolders = %v, want /nzbget/dl/tv (MainDir substitution + AppendCategoryDir)",
			info.OutputRootFolders)
	}
}

func TestGetStatus_AppendCategoryDirNoSkipsNameAppend(t *testing.T) {
	t.Parallel()
	_, srv := newStatusServer(t, []nzbget.NzbgetConfigItem{
		{Name: "MainDir", Value: "/nzbget"},
		{Name: "DestDir", Value: "${MainDir}/dl"},
		{Name: "AppendCategoryDir", Value: "no"},
		{Name: "Category1.Name", Value: "tv"},
		{Name: "Category1.DestDir", Value: ""},
	})
	n := newNzbgetForStatus(t, srv, func(s *nzbget.Settings) {
		s.TvCategory = "tv"
	})
	info, _ := n.GetStatus(context.Background())
	if len(info.OutputRootFolders) != 1 || info.OutputRootFolders[0].String() != "/nzbget/dl" {
		t.Errorf("OutputRootFolders = %v, want /nzbget/dl (no append)", info.OutputRootFolders)
	}
}

func TestGetStatus_AppendCategoryDirDefaultIsYes(t *testing.T) {
	t.Parallel()
	// Missing AppendCategoryDir key → defaults to "yes" per C# :249.
	_, srv := newStatusServer(t, []nzbget.NzbgetConfigItem{
		{Name: "MainDir", Value: "/nzbget"},
		{Name: "DestDir", Value: "${MainDir}/dl"},
		{Name: "Category1.Name", Value: "tv"},
		{Name: "Category1.DestDir", Value: ""},
	})
	n := newNzbgetForStatus(t, srv, func(s *nzbget.Settings) {
		s.TvCategory = "tv"
	})
	info, _ := n.GetStatus(context.Background())
	if info.OutputRootFolders[0].String() != "/nzbget/dl/tv" {
		t.Errorf("OutputRootFolders = %v, want /nzbget/dl/tv (default-yes append)",
			info.OutputRootFolders)
	}
}

func TestGetStatus_CategoryWalkStopsAtFirstMissing(t *testing.T) {
	t.Parallel()
	// Category1 present, Category3 present but Category2 missing.
	// Walk stops at index 2 — Category3 is never inspected.
	_, srv := newStatusServer(t, []nzbget.NzbgetConfigItem{
		{Name: "Category1.Name", Value: "movies"},
		{Name: "Category1.DestDir", Value: "/m"},
		{Name: "Category3.Name", Value: "tv"}, // hidden — walk stops at 2
		{Name: "Category3.DestDir", Value: "/should-not-find"},
	})
	n := newNzbgetForStatus(t, srv, func(s *nzbget.Settings) {
		s.TvCategory = "tv"
	})
	info, _ := n.GetStatus(context.Background())
	if len(info.OutputRootFolders) != 0 {
		t.Errorf("OutputRootFolders = %v, want empty (walk should stop at Category2 missing)",
			info.OutputRootFolders)
	}
}

// ----------------------------------------------------------------------------
// IsLocalhost
// ----------------------------------------------------------------------------

func TestGetStatus_LocalhostBindIsLocalhost(t *testing.T) {
	t.Parallel()
	// httptest binds to 127.0.0.1 → IsLocalhost true via settingsFor.
	_, srv := newStatusServer(t, []nzbget.NzbgetConfigItem{})
	n := newNzbgetForStatus(t, srv, nil)
	info, _ := n.GetStatus(context.Background())
	if !info.IsLocalhost {
		t.Errorf("IsLocalhost = false, want true")
	}
}

func TestGetStatus_LocalhostHostnameCaseInsensitive(t *testing.T) {
	t.Parallel()
	_, srv := newStatusServer(t, []nzbget.NzbgetConfigItem{})
	s := settingsFor(t, srv)
	s.Host = "LocalHost"
	proxy, _ := nzbget.NewProxy(&hostRedirectingDoer{target: srv}, nil)
	n := nzbget.New(nil, s, proxy, nil)
	info, err := n.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !info.IsLocalhost {
		t.Errorf("IsLocalhost = false for Host=LocalHost, want true (case-insensitive)")
	}
}

func TestGetStatus_NonLocalhostHostname(t *testing.T) {
	t.Parallel()
	_, srv := newStatusServer(t, []nzbget.NzbgetConfigItem{})
	s := settingsFor(t, srv)
	s.Host = "nzbget.example.com"
	proxy, _ := nzbget.NewProxy(&hostRedirectingDoer{target: srv}, nil)
	n := nzbget.New(nil, s, proxy, nil)
	info, _ := n.GetStatus(context.Background())
	if info.IsLocalhost {
		t.Errorf("IsLocalhost = true, want false")
	}
}

// ----------------------------------------------------------------------------
// Failure surfaces
// ----------------------------------------------------------------------------

func TestGetStatus_ConfigRPCFailureBubbles(t *testing.T) {
	t.Parallel()
	h, srv := newStatusServer(t, nil)
	h.configErr = "boom"
	n := newNzbgetForStatus(t, srv, nil)
	_, err := n.GetStatus(context.Background())
	if !errors.Is(err, nzbget.ErrRPCFailure) {
		t.Errorf("err = %v, want ErrRPCFailure", err)
	}
}

func TestGetStatus_AuthFailureBubbles(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	n := newNzbgetForStatus(t, srv, nil)
	_, err := n.GetStatus(context.Background())
	if !errors.Is(err, nzbget.ErrAuthFailed) {
		t.Errorf("err = %v, want ErrAuthFailed", err)
	}
}

func TestGetStatus_NilProxy(t *testing.T) {
	t.Parallel()
	n := nzbget.New(nil, nzbget.NewSettings(), nil, nil)
	_, err := n.GetStatus(context.Background())
	if err == nil {
		t.Fatal("GetStatus(nil proxy) err = nil, want error")
	}
}
