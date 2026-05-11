package sabnzbd_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/sabnzbd"
)

// ============================================================================
// Settings.Validate — ports the validation cases from
// `Download/Clients/Sabnzbd/SabnzbdSettings.cs:8-32`.
// ============================================================================

func TestSettings_DefaultsValidWithAPIKey(t *testing.T) {
	t.Parallel()
	s := sabnzbd.NewSettings()
	s.APIKey = "secret"
	got := s.Validate()
	if len(got) != 0 {
		t.Errorf("Validate = %+v, want no failures", got)
	}
}

func TestSettings_MissingAuthFlagged(t *testing.T) {
	t.Parallel()
	s := sabnzbd.NewSettings()
	got := s.Validate()
	// Expect at least one ApiKey failure (no API key + no
	// Username).
	found := false
	for _, f := range got {
		if f.PropertyName == "ApiKey" {
			found = true
		}
	}
	if !found {
		t.Errorf("Validate failures = %+v, want one with PropertyName=ApiKey", got)
	}
}

func TestSettings_UsernameOnlyRequiresPassword(t *testing.T) {
	t.Parallel()
	s := sabnzbd.NewSettings()
	s.Username = "user"
	got := s.Validate()
	found := false
	for _, f := range got {
		if f.PropertyName == "Password" {
			found = true
		}
	}
	if !found {
		t.Errorf("Validate failures = %+v, want Password failure when only Username set", got)
	}
}

func TestSettings_PortOutOfRange(t *testing.T) {
	t.Parallel()
	for _, port := range []int{0, -1, 65536, 99999} {
		s := sabnzbd.NewSettings()
		s.APIKey = "secret"
		s.Port = port
		got := s.Validate()
		found := false
		for _, f := range got {
			if f.PropertyName == "Port" {
				found = true
			}
		}
		if !found {
			t.Errorf("Validate(Port=%d) failures = %+v, want Port failure", port, got)
		}
	}
}

func TestSettings_UrlBaseMustStartWithSlash(t *testing.T) {
	t.Parallel()
	s := sabnzbd.NewSettings()
	s.APIKey = "secret"
	s.UrlBase = "sabnzbd"
	got := s.Validate()
	found := false
	for _, f := range got {
		if f.PropertyName == "UrlBase" {
			found = true
		}
	}
	if !found {
		t.Errorf("Validate failures = %+v, want UrlBase failure", got)
	}
}

func TestSettings_EmptyCategoryWarning(t *testing.T) {
	t.Parallel()
	s := sabnzbd.NewSettings()
	s.APIKey = "secret"
	s.TvCategory = ""
	got := s.Validate()
	found := false
	for _, f := range got {
		if f.PropertyName == "TvCategory" {
			found = true
		}
	}
	if !found {
		t.Errorf("Validate failures = %+v, want TvCategory warning", got)
	}
}

// ============================================================================
// Proxy.BuildBaseURL — ports `SabnzbdProxy.GetBaseUrl` at `:38-44`.
// ============================================================================

func TestProxy_BuildBaseURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		settings sabnzbd.Settings
		rel      string
		want     string
	}{
		{
			name:     "default localhost",
			settings: sabnzbd.Settings{Host: "localhost", Port: 8080},
			rel:      "api",
			want:     "http://localhost:8080/api",
		},
		{
			name:     "with TLS",
			settings: sabnzbd.Settings{Host: "sab.example.com", Port: 443, UseSsl: true},
			rel:      "api",
			want:     "https://sab.example.com:443/api",
		},
		{
			name:     "with url base",
			settings: sabnzbd.Settings{Host: "10.0.0.1", Port: 8080, UrlBase: "/sabnzbd"},
			rel:      "api",
			want:     "http://10.0.0.1:8080/sabnzbd/api",
		},
		{
			name:     "url base without leading slash gets one",
			settings: sabnzbd.Settings{Host: "10.0.0.1", Port: 8080, UrlBase: "sabnzbd/"},
			rel:      "api",
			want:     "http://10.0.0.1:8080/sabnzbd/api",
		},
		{
			name:     "empty relative",
			settings: sabnzbd.Settings{Host: "localhost", Port: 8080},
			rel:      "",
			want:     "http://localhost:8080",
		},
	}

	p, err := sabnzbd.NewProxy(http.DefaultClient, slog.Default())
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := p.BuildBaseURL(c.settings, c.rel); got != c.want {
				t.Errorf("BuildBaseURL = %q, want %q", got, c.want)
			}
		})
	}
}

// ============================================================================
// Proxy.DownloadNzb — uses httptest.Server to record SAB requests.
// ============================================================================

func TestProxy_DownloadNzbSuccess(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the request shape matches what the C# proxy
		// emits at `:46-62` — POST /api?mode=addfile&...&output=json.
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if got := r.URL.Path; got != "/api" {
			t.Errorf("path = %q, want /api", got)
		}
		q := r.URL.Query()
		if q.Get("mode") != "addfile" {
			t.Errorf("mode = %q, want addfile", q.Get("mode"))
		}
		if q.Get("apikey") != "k3y" {
			t.Errorf("apikey = %q, want k3y", q.Get("apikey"))
		}
		if q.Get("cat") != "tv" {
			t.Errorf("cat = %q, want tv", q.Get("cat"))
		}
		if q.Get("priority") != "-100" {
			t.Errorf("priority = %q, want -100", q.Get("priority"))
		}
		if q.Get("output") != "json" {
			t.Errorf("output = %q, want json", q.Get("output"))
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			t.Errorf("Content-Type = %q, want multipart/form-data", r.Header.Get("Content-Type"))
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":true,"nzo_ids":["SABnzbd_nzo_abc123"]}`))
	}))
	defer srv.Close()

	settings := buildSettings(srv.URL, "k3y")
	p, _ := sabnzbd.NewProxy(srv.Client(), slog.Default())

	id, err := p.DownloadNzb(context.Background(), []byte("NZB-CONTENT"), "release.nzb", "tv", sabnzbd.PriorityDefault, settings)
	if err != nil {
		t.Fatalf("DownloadNzb: %v", err)
	}
	if id != "SABnzbd_nzo_abc123" {
		t.Errorf("nzo_id = %q, want SABnzbd_nzo_abc123", id)
	}
}

func TestProxy_DownloadNzbEmptyResponse(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":true,"nzo_ids":[]}`))
	}))
	defer srv.Close()

	settings := buildSettings(srv.URL, "k3y")
	p, _ := sabnzbd.NewProxy(srv.Client(), slog.Default())

	_, err := p.DownloadNzb(context.Background(), []byte("NZB"), "rel.nzb", "tv", sabnzbd.PriorityDefault, settings)
	if !errors.Is(err, sabnzbd.ErrEmptyResponse) {
		t.Errorf("err = %v, want ErrEmptyResponse", err)
	}
}

func TestProxy_DownloadNzbAuthError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"false","error":"API Key Incorrect"}`))
	}))
	defer srv.Close()

	settings := buildSettings(srv.URL, "bad-key")
	p, _ := sabnzbd.NewProxy(srv.Client(), slog.Default())

	_, err := p.DownloadNzb(context.Background(), []byte("NZB"), "rel.nzb", "tv", sabnzbd.PriorityDefault, settings)
	if !errors.Is(err, sabnzbd.ErrSabError) {
		t.Errorf("err = %v, want ErrSabError", err)
	}
	if !strings.Contains(err.Error(), "API Key Incorrect") {
		t.Errorf("err = %v, want SAB error message in chain", err)
	}
}

func TestProxy_GetVersion(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"3.7.2"}`))
	}))
	defer srv.Close()

	settings := buildSettings(srv.URL, "k3y")
	p, _ := sabnzbd.NewProxy(srv.Client(), slog.Default())

	got, err := p.GetVersion(context.Background(), settings)
	if err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if got != "3.7.2" {
		t.Errorf("version = %q, want 3.7.2", got)
	}
}

func TestProxy_GetVersionLegacyAuth(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// When APIKey is empty, the request should carry
		// ma_username + ma_password. Mirrors C# `:176-180`.
		q := r.URL.Query()
		if q.Get("ma_username") != "user" {
			t.Errorf("ma_username = %q, want user", q.Get("ma_username"))
		}
		if q.Get("ma_password") != "pass" {
			t.Errorf("ma_password = %q, want pass", q.Get("ma_password"))
		}
		if q.Get("apikey") != "" {
			t.Errorf("apikey unexpectedly sent on legacy auth: %q", q.Get("apikey"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"3.7.2"}`))
	}))
	defer srv.Close()

	settings := buildSettings(srv.URL, "")
	settings.Username = "user"
	settings.Password = "pass"
	p, _ := sabnzbd.NewProxy(srv.Client(), slog.Default())

	if _, err := p.GetVersion(context.Background(), settings); err != nil {
		t.Errorf("GetVersion: %v", err)
	}
}

// ============================================================================
// Sabnzbd.Test — covers connection + version-floor failure modes.
// ============================================================================

func TestSabnzbd_TestSuccess(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"3.7.2"}`))
	}))
	defer srv.Close()

	prov := newSabnzbdForURL(t, srv)

	failures, err := prov.Test(context.Background())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("failures = %+v, want none", failures)
	}
}

func TestSabnzbd_TestSurfacesUnreachable(t *testing.T) {
	t.Parallel()
	// Point at a closed port (httptest server already closed).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	srv.Close()

	prov := newSabnzbdForURL(t, srv)
	failures, err := prov.Test(context.Background())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(failures) == 0 {
		t.Fatal("expected at least one failure for unreachable host")
	}
	found := false
	for _, f := range failures {
		if f.PropertyName == "Host" {
			found = true
		}
	}
	if !found {
		t.Errorf("failures = %+v, want Host failure", failures)
	}
}

func TestSabnzbd_TestVersionFloor(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"0.6.0"}`))
	}))
	defer srv.Close()

	prov := newSabnzbdForURL(t, srv)
	failures, err := prov.Test(context.Background())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	found := false
	for _, f := range failures {
		if f.PropertyName == "Version" {
			found = true
		}
	}
	if !found {
		t.Errorf("failures = %+v, want Version failure for SAB 0.6.0", failures)
	}
}

// ============================================================================
// Sabnzbd.Download — end-to-end via the Provider surface.
// ============================================================================

func TestSabnzbd_DownloadSurfacesNzoID(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":true,"nzo_ids":["SABnzbd_nzo_abc"]}`))
	}))
	defer srv.Close()

	prov := newSabnzbdForURL(t, srv)
	req := &downloadclients.GrabRequest{
		ReleaseTitle: "Show.S01E01-Group",
		NzbBody:      []byte("NZB-CONTENTS"),
	}
	id, err := prov.Download(context.Background(), req)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if id != "SABnzbd_nzo_abc" {
		t.Errorf("id = %q, want SABnzbd_nzo_abc", id)
	}
}

func TestSabnzbd_DownloadRejectsEmptyBody(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	prov := newSabnzbdForURL(t, srv)
	_, err := prov.Download(context.Background(), &downloadclients.GrabRequest{ReleaseTitle: "X"})
	if err == nil {
		t.Error("Download with empty NzbBody should error")
	}
}

func TestSabnzbd_StubbedMethodsReturnNotImplementedYet(t *testing.T) {
	t.Parallel()
	prov := newSabnzbdForURL(t, nil)
	if _, err := prov.GetItems(context.Background()); !errors.Is(err, downloadclients.ErrNotImplementedYet) {
		t.Errorf("GetItems err = %v, want ErrNotImplementedYet", err)
	}
	if _, err := prov.GetStatus(context.Background()); !errors.Is(err, downloadclients.ErrNotImplementedYet) {
		t.Errorf("GetStatus err = %v, want ErrNotImplementedYet", err)
	}
	if err := prov.RemoveItem(context.Background(), nil, false); !errors.Is(err, downloadclients.ErrNotImplementedYet) {
		t.Errorf("RemoveItem err = %v, want ErrNotImplementedYet", err)
	}
	if err := prov.MarkItemAsImported(context.Background(), nil); !errors.Is(err, downloadclients.ErrNotImplementedYet) {
		t.Errorf("MarkItemAsImported err = %v, want ErrNotImplementedYet", err)
	}
}

// ============================================================================
// Registry — init() registers Sabnzbd in Default().
// ============================================================================

func TestRegistry_SabnzbdRegistered(t *testing.T) {
	t.Parallel()
	if !downloadclients.Default().Has(sabnzbd.Implementation, sabnzbd.ConfigContract) {
		t.Errorf("Default registry missing %s/%s", sabnzbd.Implementation, sabnzbd.ConfigContract)
	}
}

// ============================================================================
// Helpers
// ============================================================================

func buildSettings(serverURL, apiKey string) sabnzbd.Settings {
	s := sabnzbd.NewSettings()
	host, port := splitHostPort(t_helperURL(serverURL))
	s.Host = host
	s.Port = port
	s.APIKey = apiKey
	return s
}

func newSabnzbdForURL(t *testing.T, srv *httptest.Server) *sabnzbd.Sabnzbd {
	t.Helper()
	url := ""
	var client *http.Client
	if srv != nil {
		url = srv.URL
		client = srv.Client()
	} else {
		client = http.DefaultClient
	}
	proxy, err := sabnzbd.NewProxy(client, slog.Default())
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	settings := sabnzbd.NewSettings()
	if url != "" {
		host, port := splitHostPort(t_helperURL(url))
		settings.Host = host
		settings.Port = port
	}
	settings.APIKey = "k3y"
	def := &downloadclients.DownloadClient{
		ID:             1,
		Name:           "test",
		Implementation: sabnzbd.Implementation,
		ConfigContract: sabnzbd.ConfigContract,
		Settings:       mustMarshal(settings),
	}
	return sabnzbd.New(def, settings, proxy, slog.Default())
}

func t_helperURL(s string) string {
	// strip "http://" / "https://" prefix for downstream Host/Port
	// parsing.
	for _, p := range []string{"http://", "https://"} {
		s = strings.TrimPrefix(s, p)
	}
	return s
}

func splitHostPort(hostPort string) (string, int) {
	parts := strings.SplitN(hostPort, ":", 2)
	if len(parts) != 2 {
		return hostPort, 0
	}
	port := 0
	for _, c := range parts[1] {
		if c < '0' || c > '9' {
			break
		}
		port = port*10 + int(c-'0')
	}
	return parts[0], port
}

func mustMarshal(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
