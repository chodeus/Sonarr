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
	"sonarr.tv/sonarr/internal/ospath"
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
	srv := httptest.NewServer(testSuccessHandler(testHandlerCfg{}))
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

// TestSabnzbd_RemoveItemNilItemErrors locks the nil-item guard added
// in slice 4.BS. Other previously-stubbed methods (GetItems / GetStatus
// / MarkItemAsImported) are now ported — their behavior is exercised
// in the dedicated tests above.
func TestSabnzbd_RemoveItemNilItemErrors(t *testing.T) {
	t.Parallel()
	prov := newSabnzbdForURL(t, nil)
	if err := prov.RemoveItem(context.Background(), nil, false); err == nil {
		t.Errorf("RemoveItem(nil) should error")
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

// ============================================================================
// Slice 4.BS (C-cleanup sub-arc C2a): SAB Proxy.GetQueue / GetHistory /
// GetConfig / GetFullStatus / RemoveFromQueue / RemoveFromHistory +
// Sabnzbd.GetItems / RemoveItem / GetStatus / MarkItemAsImported.
// ============================================================================

func TestProxy_GetQueueDecodesItems(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("mode"); got != "queue" {
			t.Errorf("mode = %q, want queue", got)
		}
		if got := r.URL.Query().Get("start"); got != "0" {
			t.Errorf("start = %q, want 0", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"queue":{"paused":false,"slots":[{"status":"Downloading","filename":"Show.S01E01","mb":100.5,"mbleft":42.25,"timeleft":"1:30:00","priority":"Normal","cat":"tv","nzo_id":"nzo_001"}]}}`))
	}))
	defer srv.Close()
	settings := buildSettings(srv.URL, "k3y")
	p, _ := sabnzbd.NewProxy(srv.Client(), slog.Default())
	got, err := p.GetQueue(context.Background(), 0, 0, settings)
	if err != nil {
		t.Fatalf("GetQueue: %v", err)
	}
	if len(got.Items) != 1 {
		t.Fatalf("Items = %d, want 1", len(got.Items))
	}
	if got.Items[0].ID != "nzo_001" || got.Items[0].Status != sabnzbd.SabStatusDownloading {
		t.Errorf("unexpected first item: %+v", got.Items[0])
	}
}

func TestProxy_GetHistoryDecodesItems(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("mode"); got != "history" {
			t.Errorf("mode = %q, want history", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"history":{"paused":false,"slots":[{"name":"Show.S01E02","bytes":1048576,"category":"tv","nzo_id":"nzo_002","status":"Completed","storage":"/data/complete/Show.S01E02"}]}}`))
	}))
	defer srv.Close()
	settings := buildSettings(srv.URL, "k3y")
	p, _ := sabnzbd.NewProxy(srv.Client(), slog.Default())
	got, err := p.GetHistory(context.Background(), 0, 30, settings)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(got.Items) != 1 {
		t.Fatalf("Items = %d, want 1", len(got.Items))
	}
	if got.Items[0].ID != "nzo_002" || got.Items[0].Status != sabnzbd.SabStatusCompleted {
		t.Errorf("unexpected item: %+v", got.Items[0])
	}
}

func TestProxy_GetConfigDecodesCategoriesAndMisc(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"config":{"misc":{"complete_dir":"/data/complete","tv_categories":["tv","tv-sonarr"],"enable_tv_sorting":true,"date_categories":"daily"},"categories":[{"name":"tv","priority":0,"dir":"tv/"}],"sorters":[{"name":"S1","is_active":true,"sort_cats":["tv"]}]}}`))
	}))
	defer srv.Close()
	settings := buildSettings(srv.URL, "k3y")
	p, _ := sabnzbd.NewProxy(srv.Client(), slog.Default())
	got, err := p.GetConfig(context.Background(), settings)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if got.Misc.CompleteDir != "/data/complete" {
		t.Errorf("CompleteDir = %q", got.Misc.CompleteDir)
	}
	if len(got.Categories) != 1 || got.Categories[0].Name != "tv" {
		t.Errorf("Categories = %+v", got.Categories)
	}
	if len(got.Misc.DateCategories) != 1 || got.Misc.DateCategories[0] != "daily" {
		t.Errorf("DateCategories unmarshal (single-string form): %+v", got.Misc.DateCategories)
	}
}

func TestProxy_GetFullStatusDecodesCompleteDir(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("skip_dashboard"); got != "1" {
			t.Errorf("skip_dashboard = %q, want 1", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":{"completedir":"/data/done"}}`))
	}))
	defer srv.Close()
	settings := buildSettings(srv.URL, "k3y")
	p, _ := sabnzbd.NewProxy(srv.Client(), slog.Default())
	got, err := p.GetFullStatus(context.Background(), settings)
	if err != nil {
		t.Fatalf("GetFullStatus: %v", err)
	}
	if got.CompleteDir != "/data/done" {
		t.Errorf("CompleteDir = %q", got.CompleteDir)
	}
}

func TestProxy_RemoveFromQueueEncodesParams(t *testing.T) {
	t.Parallel()
	var seenQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":true}`))
	}))
	defer srv.Close()
	settings := buildSettings(srv.URL, "k3y")
	p, _ := sabnzbd.NewProxy(srv.Client(), slog.Default())
	if err := p.RemoveFromQueue(context.Background(), "nzo_x", true, settings); err != nil {
		t.Fatalf("RemoveFromQueue: %v", err)
	}
	if !strings.Contains(seenQuery, "name=delete") || !strings.Contains(seenQuery, "del_files=1") || !strings.Contains(seenQuery, "value=nzo_x") {
		t.Errorf("seenQuery = %q", seenQuery)
	}
}

func TestProxy_RemoveFromHistoryArchiveFlag(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		permanent bool
		wantArch  string
	}{
		{"PermanentSetsArchiveZero", true, "archive=0"},
		{"NonPermanentSetsArchiveOne", false, "archive=1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var seenQuery string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seenQuery = r.URL.RawQuery
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"status":true}`))
			}))
			defer srv.Close()
			settings := buildSettings(srv.URL, "k3y")
			p, _ := sabnzbd.NewProxy(srv.Client(), slog.Default())
			if err := p.RemoveFromHistory(context.Background(), "nzo_y", false, tc.permanent, settings); err != nil {
				t.Fatalf("RemoveFromHistory: %v", err)
			}
			if !strings.Contains(seenQuery, tc.wantArch) {
				t.Errorf("seenQuery = %q, missing %q", seenQuery, tc.wantArch)
			}
		})
	}
}

// fakeSAB serves canned queue + history + config replies based on the
// mode query param. Lets multi-call tests verify the combined SAB
// methods (GetItems / RemoveItem / GetStatus) in one server.
type fakeSAB struct {
	queueJSON   string
	historyJSON string
	configJSON  string
	deleteQuery string
}

func (f *fakeSAB) serve(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	w.Header().Set("Content-Type", "application/json")
	switch q.Get("mode") {
	case "queue":
		if q.Get("name") == "delete" {
			f.deleteQuery = r.URL.RawQuery
			_, _ = w.Write([]byte(`{"status":true}`))
			return
		}
		_, _ = w.Write([]byte(f.queueJSON))
	case "history":
		if q.Get("name") == "delete" {
			f.deleteQuery = r.URL.RawQuery
			_, _ = w.Write([]byte(`{"status":true}`))
			return
		}
		_, _ = w.Write([]byte(f.historyJSON))
	case "get_config":
		_, _ = w.Write([]byte(f.configJSON))
	default:
		_, _ = w.Write([]byte(`{"status":true}`))
	}
}

func TestSabnzbd_GetItemsUnionsQueueAndHistory(t *testing.T) {
	t.Parallel()
	fake := &fakeSAB{
		queueJSON:   `{"queue":{"paused":false,"slots":[{"status":"Downloading","filename":"Show.S01E01","mb":100,"mbleft":42,"timeleft":"0:10:00","priority":"Normal","cat":"tv","nzo_id":"q_001"},{"status":"Deleted","filename":"Skip","cat":"tv","nzo_id":"skip"},{"status":"Downloading","filename":"Other.S01E01","cat":"other","nzo_id":"q_other"}]}}`,
		historyJSON: `{"history":{"paused":false,"slots":[{"name":"Show.S01E02","bytes":1048576,"category":"tv","nzo_id":"h_001","status":"Completed","storage":"/data/complete/x"}]}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.serve(w, r)
	}))
	defer srv.Close()

	sab := newSabnzbdForURL(t, srv)
	got, err := sab.GetItems(context.Background())
	if err != nil {
		t.Fatalf("GetItems: %v", err)
	}
	// Expect 2 items: q_001 + h_001. The "Deleted" and the wrong-category
	// "q_other" should both be filtered out.
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2; got=%+v", len(got), got)
	}
	if got[0].DownloadID != "q_001" || got[0].Status != downloadclients.DownloadItemStatusDownloading {
		t.Errorf("first item = %+v", got[0])
	}
	if got[1].DownloadID != "h_001" || got[1].Status != downloadclients.DownloadItemStatusCompleted {
		t.Errorf("second item = %+v", got[1])
	}
}

func TestSabnzbd_GetItemsTitleStripsEncryptedPrefix(t *testing.T) {
	t.Parallel()
	fake := &fakeSAB{
		queueJSON:   `{"queue":{"paused":false,"slots":[{"status":"Downloading","filename":"ENCRYPTED /Real.Title","cat":"tv","nzo_id":"e_001","priority":"Normal"}]}}`,
		historyJSON: `{"history":{"paused":false,"slots":[]}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fake.serve(w, r) }))
	defer srv.Close()

	sab := newSabnzbdForURL(t, srv)
	got, _ := sab.GetItems(context.Background())
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Title != "Real.Title" || !got[0].IsEncrypted {
		t.Errorf("Title=%q IsEncrypted=%v", got[0].Title, got[0].IsEncrypted)
	}
}

func TestSabnzbd_GetItemsPausedQueueMarksItemsPaused(t *testing.T) {
	t.Parallel()
	fake := &fakeSAB{
		queueJSON:   `{"queue":{"paused":true,"slots":[{"status":"Downloading","filename":"Show.S01E01","cat":"tv","nzo_id":"p_001","priority":"Normal"}]}}`,
		historyJSON: `{"history":{"paused":false,"slots":[]}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fake.serve(w, r) }))
	defer srv.Close()
	sab := newSabnzbdForURL(t, srv)
	got, _ := sab.GetItems(context.Background())
	if len(got) != 1 || got[0].Status != downloadclients.DownloadItemStatusPaused {
		t.Errorf("expected single Paused item, got %+v", got)
	}
}

func TestSabnzbd_GetItemsForceOverridesQueuePause(t *testing.T) {
	t.Parallel()
	fake := &fakeSAB{
		queueJSON:   `{"queue":{"paused":true,"slots":[{"status":"Downloading","filename":"Force.S01E01","cat":"tv","nzo_id":"f_001","priority":"Force"}]}}`,
		historyJSON: `{"history":{"paused":false,"slots":[]}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fake.serve(w, r) }))
	defer srv.Close()
	sab := newSabnzbdForURL(t, srv)
	got, _ := sab.GetItems(context.Background())
	if len(got) != 1 || got[0].Status != downloadclients.DownloadItemStatusDownloading {
		t.Errorf("Force priority should bypass paused-queue; got %+v", got)
	}
}

func TestSabnzbd_GetItemsHistoryFailDemotedToWarning(t *testing.T) {
	t.Parallel()
	fake := &fakeSAB{
		queueJSON:   `{"queue":{"paused":false,"slots":[]}}`,
		historyJSON: `{"history":{"paused":false,"slots":[{"name":"Show.X","bytes":1,"category":"tv","nzo_id":"h_warn","status":"Failed","fail_message":"Unpacking failed, write error or disk is full?"}]}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fake.serve(w, r) }))
	defer srv.Close()
	sab := newSabnzbdForURL(t, srv)
	got, _ := sab.GetItems(context.Background())
	if len(got) != 1 || got[0].Status != downloadclients.DownloadItemStatusWarning {
		t.Errorf("Unpacking-failed message should demote to Warning; got %+v", got)
	}
}

func TestSabnzbd_RemoveItemQueueBranch(t *testing.T) {
	t.Parallel()
	fake := &fakeSAB{
		queueJSON:   `{"queue":{"paused":false,"slots":[{"status":"Downloading","filename":"Live.S01E01","cat":"tv","nzo_id":"live_1","priority":"Normal"}]}}`,
		historyJSON: `{"history":{"paused":false,"slots":[]}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fake.serve(w, r) }))
	defer srv.Close()
	sab := newSabnzbdForURL(t, srv)
	item := &downloadclients.Item{DownloadID: "live_1"}
	if err := sab.RemoveItem(context.Background(), item, true); err != nil {
		t.Fatalf("RemoveItem: %v", err)
	}
	if !strings.Contains(fake.deleteQuery, "mode=queue") {
		t.Errorf("deleteQuery = %q, want a queue mode call", fake.deleteQuery)
	}
}

func TestSabnzbd_RemoveItemHistoryBranchPermanentFlagOnFailed(t *testing.T) {
	t.Parallel()
	fake := &fakeSAB{
		queueJSON:   `{"queue":{"paused":false,"slots":[]}}`,
		historyJSON: `{"history":{"paused":false,"slots":[]}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fake.serve(w, r) }))
	defer srv.Close()
	sab := newSabnzbdForURL(t, srv)
	item := &downloadclients.Item{DownloadID: "gone_1", Status: downloadclients.DownloadItemStatusFailed}
	if err := sab.RemoveItem(context.Background(), item, false); err != nil {
		t.Fatalf("RemoveItem: %v", err)
	}
	if !strings.Contains(fake.deleteQuery, "mode=history") {
		t.Errorf("deleteQuery = %q, want a history mode call", fake.deleteQuery)
	}
	if !strings.Contains(fake.deleteQuery, "archive=0") {
		t.Errorf("deleteQuery = %q, want archive=0 for permanent-delete on Failed", fake.deleteQuery)
	}
}

// --- J116 RemoveItem deleteData history-branch DiskProvider arm ---

type sabStubDisk struct {
	called bool
	path   string
	err    error
}

func (s *sabStubDisk) DeleteFolder(path string, _ bool) error {
	s.called = true
	s.path = path
	return s.err
}

func TestSabnzbd_RemoveItem_J116_HistoryCompletedDeleteDataCallsDisk(t *testing.T) {
	t.Parallel()
	fake := &fakeSAB{
		queueJSON:   `{"queue":{"paused":false,"slots":[]}}`,
		historyJSON: `{"history":{"paused":false,"slots":[]}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fake.serve(w, r) }))
	defer srv.Close()
	sab := newSabnzbdForURL(t, srv)
	disk := &sabStubDisk{}
	sab.SetDiskProvider(disk)
	item := &downloadclients.Item{
		DownloadID: "abc",
		Status:     downloadclients.DownloadItemStatusCompleted,
		OutputPath: ospath.New("/data/tv/Show.S01E02"),
	}
	if err := sab.RemoveItem(context.Background(), item, true); err != nil {
		t.Fatal(err)
	}
	if !disk.called {
		t.Errorf("DiskProvider.DeleteFolder was not called")
	}
	if disk.path != "/data/tv/Show.S01E02" {
		t.Errorf("disk path=%q, want /data/tv/Show.S01E02", disk.path)
	}
	if !strings.Contains(fake.deleteQuery, "mode=history") {
		t.Errorf("deleteQuery=%q, want mode=history", fake.deleteQuery)
	}
}

func TestSabnzbd_RemoveItem_J116_HistoryDeleteDataFalseSkipsDisk(t *testing.T) {
	t.Parallel()
	fake := &fakeSAB{
		queueJSON:   `{"queue":{"paused":false,"slots":[]}}`,
		historyJSON: `{"history":{"paused":false,"slots":[]}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fake.serve(w, r) }))
	defer srv.Close()
	sab := newSabnzbdForURL(t, srv)
	disk := &sabStubDisk{}
	sab.SetDiskProvider(disk)
	item := &downloadclients.Item{
		DownloadID: "abc",
		Status:     downloadclients.DownloadItemStatusCompleted,
		OutputPath: ospath.New("/data/tv/Show"),
	}
	if err := sab.RemoveItem(context.Background(), item, false); err != nil {
		t.Fatal(err)
	}
	if disk.called {
		t.Errorf("DiskProvider fired despite deleteData=false")
	}
}

func TestSabnzbd_RemoveItem_J116_HistoryNotCompletedSkipsDisk(t *testing.T) {
	t.Parallel()
	// Failed status → SAB does the permanent-delete via archive=0;
	// the Sonarr-side disk arm only applies to Completed (matches
	// C# `:203` `status == DownloadItemStatus.Completed` guard).
	fake := &fakeSAB{
		queueJSON:   `{"queue":{"paused":false,"slots":[]}}`,
		historyJSON: `{"history":{"paused":false,"slots":[]}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fake.serve(w, r) }))
	defer srv.Close()
	sab := newSabnzbdForURL(t, srv)
	disk := &sabStubDisk{}
	sab.SetDiskProvider(disk)
	item := &downloadclients.Item{
		DownloadID: "abc",
		Status:     downloadclients.DownloadItemStatusFailed,
		OutputPath: ospath.New("/data/tv/Show"),
	}
	if err := sab.RemoveItem(context.Background(), item, true); err != nil {
		t.Fatal(err)
	}
	if disk.called {
		t.Errorf("DiskProvider fired on Failed status; only Completed should trigger disk arm")
	}
}

func TestSabnzbd_RemoveItem_J116_DiskErrorDoesNotAbortHistoryDelete(t *testing.T) {
	t.Parallel()
	fake := &fakeSAB{
		queueJSON:   `{"queue":{"paused":false,"slots":[]}}`,
		historyJSON: `{"history":{"paused":false,"slots":[]}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fake.serve(w, r) }))
	defer srv.Close()
	sab := newSabnzbdForURL(t, srv)
	disk := &sabStubDisk{err: errors.New("permission denied")}
	sab.SetDiskProvider(disk)
	item := &downloadclients.Item{
		DownloadID: "abc",
		Status:     downloadclients.DownloadItemStatusCompleted,
		OutputPath: ospath.New("/data/tv/Show"),
	}
	if err := sab.RemoveItem(context.Background(), item, true); err != nil {
		t.Errorf("RemoveItem must not abort on disk-arm failure: %v", err)
	}
	if !disk.called {
		t.Errorf("disk arm did not fire")
	}
	if !strings.Contains(fake.deleteQuery, "mode=history") {
		t.Errorf("SAB history delete didn't run after disk failure; deleteQuery=%q", fake.deleteQuery)
	}
}

func TestSabnzbd_RemoveItem_J116_QueueBranchUnaffected(t *testing.T) {
	t.Parallel()
	// Item still in queue → queue-branch path, no disk arm even with
	// deleteData=true (matches C# `:210-212` which goes straight to
	// RemoveFromQueue).
	fake := &fakeSAB{
		queueJSON:   `{"queue":{"paused":false,"slots":[{"nzo_id":"abc","status":"Downloading","filename":"x","sizeleft":"0 B","size":"0 B","cat":"tv"}]}}`,
		historyJSON: `{"history":{"paused":false,"slots":[]}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fake.serve(w, r) }))
	defer srv.Close()
	sab := newSabnzbdForURL(t, srv)
	disk := &sabStubDisk{}
	sab.SetDiskProvider(disk)
	item := &downloadclients.Item{
		DownloadID: "abc",
		Status:     downloadclients.DownloadItemStatusDownloading,
		OutputPath: ospath.New("/data/tv/Show"),
	}
	if err := sab.RemoveItem(context.Background(), item, true); err != nil {
		t.Fatal(err)
	}
	if disk.called {
		t.Errorf("disk arm fired on queue-branch path")
	}
}

func TestSabnzbd_MarkItemAsImportedReturnsUnsupportedSentinel(t *testing.T) {
	t.Parallel()
	sab := newSabnzbdForURL(t, nil)
	err := sab.MarkItemAsImported(context.Background(), &downloadclients.Item{DownloadID: "x"})
	if !errors.Is(err, downloadclients.ErrMarkAsImportedUnsupported) {
		t.Errorf("err = %v, want ErrMarkAsImportedUnsupported", err)
	}
}

func TestSabnzbd_GetStatusResolvesAbsoluteCompleteDir(t *testing.T) {
	t.Parallel()
	fake := &fakeSAB{
		configJSON: `{"config":{"misc":{"complete_dir":"/data/done","tv_categories":["tv"],"enable_tv_sorting":true},"categories":[{"name":"tv","priority":0,"dir":"sonarr"}],"sorters":[]}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fake.serve(w, r) }))
	defer srv.Close()
	sab := newSabnzbdForURL(t, srv)
	// Override TvCategory to "tv" to match the test fixture.
	got, err := sab.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if !got.IsLocalhost {
		t.Errorf("IsLocalhost = false, want true (Host=127.0.0.1)")
	}
	// SortingMode "TV": enable_tv_sorting=true && tv in tv_categories.
	if got.SortingMode != "TV" {
		t.Errorf("SortingMode = %q, want TV", got.SortingMode)
	}
	if len(got.OutputRootFolders) != 1 {
		t.Errorf("OutputRootFolders = %+v, want 1 entry", got.OutputRootFolders)
	}
}

func TestSabnzbd_GetStatusFetchesFullStatusForRelativeCompleteDir(t *testing.T) {
	t.Parallel()
	served := struct {
		gotFullStatus bool
	}{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("mode") {
		case "get_config":
			_, _ = w.Write([]byte(`{"config":{"misc":{"complete_dir":"relative/Downloads","tv_categories":["tv"]},"categories":[{"name":"tv","dir":"sonarr"}],"sorters":[]}}`))
		case "fullstatus":
			served.gotFullStatus = true
			_, _ = w.Write([]byte(`{"status":{"completedir":"/abs/Downloads"}}`))
		default:
			_, _ = w.Write([]byte(`{"status":true}`))
		}
	}))
	defer srv.Close()
	sab := newSabnzbdForURL(t, srv)
	if _, err := sab.GetStatus(context.Background()); err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if !served.gotFullStatus {
		t.Errorf("expected fullstatus call when complete_dir is relative")
	}
}

// ============================================================================
// Slice 6.B3: Sabnzbd RemotePathMapper wire-up — historyItemToItem +
// GetStatus consult the mapper to translate SAB-side absolute paths back
// to Sonarr-side paths when the off-host SAB instance writes to a
// directory that isn't mounted at the same path on the Sonarr host.
// ============================================================================

// stubMapper satisfies downloadclients.RemotePathMapper with a fixed
// prefix-swap rule. Returns the input unchanged when the input doesn't
// start with the configured remote prefix.
type stubMapper struct {
	host         string
	remotePrefix string
	localPrefix  string
	err          error
}

func (m *stubMapper) RemapRemoteToLocal(_ context.Context, host string, remotePath ospath.OsPath) (ospath.OsPath, error) {
	if m.err != nil {
		return remotePath, m.err
	}
	if !strings.EqualFold(host, m.host) {
		return remotePath, nil
	}
	raw := remotePath.FullPath()
	if !strings.HasPrefix(raw, m.remotePrefix) {
		return remotePath, nil
	}
	return ospath.New(m.localPrefix + strings.TrimPrefix(raw, m.remotePrefix)), nil
}

func TestSabnzbd_GetItems_RemapsHistoryOutputPath(t *testing.T) {
	t.Parallel()
	fake := &fakeSAB{
		queueJSON:   `{"queue":{"paused":false,"slots":[]}}`,
		historyJSON: `{"history":{"paused":false,"slots":[{"name":"Show.S01E02","bytes":1048576,"category":"tv","nzo_id":"h_001","status":"Completed","storage":"/sab/storage/Show.S01E02"}]}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fake.serve(w, r) }))
	defer srv.Close()

	sab := newSabnzbdForURL(t, srv)
	sab.SetRemotePathMapper(&stubMapper{
		host:         "127.0.0.1",
		remotePrefix: "/sab/storage",
		localPrefix:  "/sonarr/storage",
	})

	got, err := sab.GetItems(context.Background())
	if err != nil {
		t.Fatalf("GetItems: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if want := "/sonarr/storage/Show.S01E02"; got[0].OutputPath.FullPath() != want {
		t.Errorf("OutputPath = %q, want %q", got[0].OutputPath.FullPath(), want)
	}
}

func TestSabnzbd_GetItems_NilMapperKeepsRawSabPath(t *testing.T) {
	t.Parallel()
	fake := &fakeSAB{
		queueJSON:   `{"queue":{"paused":false,"slots":[]}}`,
		historyJSON: `{"history":{"paused":false,"slots":[{"name":"Show.S01E02","bytes":1048576,"category":"tv","nzo_id":"h_001","status":"Completed","storage":"/sab/storage/Show.S01E02"}]}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fake.serve(w, r) }))
	defer srv.Close()

	sab := newSabnzbdForURL(t, srv)
	// No SetRemotePathMapper — default nil. Verifies the slice-6.B3
	// degraded path matches the pre-6.B raw-path behaviour.
	got, err := sab.GetItems(context.Background())
	if err != nil {
		t.Fatalf("GetItems: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if want := "/sab/storage/Show.S01E02"; got[0].OutputPath.FullPath() != want {
		t.Errorf("OutputPath = %q, want %q (nil mapper must pass through)", got[0].OutputPath.FullPath(), want)
	}
}

func TestSabnzbd_GetStatus_RemapsOutputRootFolders(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("mode") == "get_config" {
			_, _ = w.Write([]byte(`{"config":{"misc":{"complete_dir":"/sab/storage","tv_categories":["tv"]},"categories":[{"name":"tv","dir":"sonarr"}],"sorters":[]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":true}`))
	}))
	defer srv.Close()
	sab := newSabnzbdForURL(t, srv)
	sab.SetRemotePathMapper(&stubMapper{
		host:         "127.0.0.1",
		remotePrefix: "/sab/storage",
		localPrefix:  "/sonarr/storage",
	})

	got, err := sab.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if len(got.OutputRootFolders) != 1 {
		t.Fatalf("OutputRootFolders = %+v, want 1 entry", got.OutputRootFolders)
	}
	if want := "/sonarr/storage/sonarr"; got.OutputRootFolders[0].FullPath() != want {
		t.Errorf("OutputRootFolders[0] = %q, want %q", got.OutputRootFolders[0].FullPath(), want)
	}
}

func TestSabnzbd_RemapErrorFallsBackToRawPath(t *testing.T) {
	t.Parallel()
	fake := &fakeSAB{
		queueJSON:   `{"queue":{"paused":false,"slots":[]}}`,
		historyJSON: `{"history":{"paused":false,"slots":[{"name":"X","bytes":1,"category":"tv","nzo_id":"id","status":"Completed","storage":"/sab/x"}]}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fake.serve(w, r) }))
	defer srv.Close()

	sab := newSabnzbdForURL(t, srv)
	sab.SetRemotePathMapper(&stubMapper{host: "127.0.0.1", err: errors.New("rpm lookup blew up")})

	got, err := sab.GetItems(context.Background())
	if err != nil {
		t.Fatalf("GetItems: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if want := "/sab/x"; got[0].OutputPath.FullPath() != want {
		t.Errorf("OutputPath = %q, want raw %q on remap error", got[0].OutputPath.FullPath(), want)
	}
}

// Compile-time guard: *stubMapper satisfies the narrow interface.
var _ downloadclients.RemotePathMapper = (*stubMapper)(nil)

// --- J103 SAB Test-arm helper + tests -----------------------------

type testHandlerCfg struct {
	version       string // version string returned by ?mode=version (default "3.7.2")
	preCheck      bool   // misc.pre_check in get_config response
	categories    string // raw JSON for the config.categories slice; default `[{"name":"tv","dir":"/data/tv"}]`
	apiError      string // when non-empty, ?mode=get_config returns this error string
	versionAPIErr string // when non-empty, ?mode=version returns this error string
}

func testSuccessHandler(c testHandlerCfg) http.HandlerFunc {
	if c.version == "" {
		c.version = "3.7.2"
	}
	if c.categories == "" {
		c.categories = `[{"name":"tv","dir":"/data/tv"}]`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mode := r.URL.Query().Get("mode")
		switch mode {
		case "version":
			if c.versionAPIErr != "" {
				_, _ = w.Write([]byte(`{"status":"false","error":"` + c.versionAPIErr + `"}`))
				return
			}
			_, _ = w.Write([]byte(`{"version":"` + c.version + `"}`))
		case "get_config":
			if c.apiError != "" {
				_, _ = w.Write([]byte(`{"status":"false","error":"` + c.apiError + `"}`))
				return
			}
			preCheck := "false"
			if c.preCheck {
				preCheck = "true"
			}
			_, _ = w.Write([]byte(`{"config":{"misc":{"complete_dir":"/data/done","tv_categories":["tv"],"pre_check":` + preCheck + `},"categories":` + c.categories + `}}`))
		default:
			_, _ = w.Write([]byte(`{"version":"` + c.version + `"}`))
		}
	}
}

func TestSabnzbd_TestJ103_APIKeyIncorrectPinsToAPIKey(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(testSuccessHandler(testHandlerCfg{apiError: "API Key Incorrect"}))
	defer srv.Close()
	prov := newSabnzbdForURL(t, srv)
	failures, err := prov.Test(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range failures {
		if f.PropertyName == "APIKey" {
			found = true
		}
	}
	if !found {
		t.Errorf("failures = %+v, want APIKey failure", failures)
	}
}

func TestSabnzbd_TestJ103_APIKeyRequiredPinsToAPIKey(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(testSuccessHandler(testHandlerCfg{apiError: "API Key Required"}))
	defer srv.Close()
	prov := newSabnzbdForURL(t, srv)
	failures, err := prov.Test(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range failures {
		if f.PropertyName == "APIKey" {
			found = true
		}
	}
	if !found {
		t.Errorf("failures = %+v, want APIKey failure", failures)
	}
}

func TestSabnzbd_TestJ103_GlobalConfigPreCheckOldVersionFails(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(testSuccessHandler(testHandlerCfg{
		version:  "1.0.0", // < 1.1
		preCheck: true,
	}))
	defer srv.Close()
	prov := newSabnzbdForURL(t, srv)
	failures, err := prov.Test(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range failures {
		if f.PropertyName == "" && strings.Contains(f.ErrorMessage, "Check before download") {
			found = true
		}
	}
	if !found {
		t.Errorf("failures = %+v, want pre-check whole-form failure", failures)
	}
}

func TestSabnzbd_TestJ103_GlobalConfigPreCheckNewVersionPasses(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(testSuccessHandler(testHandlerCfg{
		version:  "1.2.0", // >= 1.1
		preCheck: true,
	}))
	defer srv.Close()
	prov := newSabnzbdForURL(t, srv)
	failures, err := prov.Test(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range failures {
		if f.PropertyName == "" && strings.Contains(f.ErrorMessage, "Check before download") {
			t.Errorf("failures = %+v, want no pre-check failure on >=1.1", failures)
		}
	}
}

func TestSabnzbd_TestJ103_GlobalConfigPreCheckOff(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(testSuccessHandler(testHandlerCfg{
		version:  "1.0.0",
		preCheck: false, // no failure regardless of version
	}))
	defer srv.Close()
	prov := newSabnzbdForURL(t, srv)
	failures, err := prov.Test(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range failures {
		if f.PropertyName == "" && strings.Contains(f.ErrorMessage, "Check before download") {
			t.Errorf("failures = %+v, want no pre-check failure when off", failures)
		}
	}
}

func TestSabnzbd_TestJ103_CategoryMissingFails(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(testSuccessHandler(testHandlerCfg{
		categories: `[{"name":"other","dir":"/data/other"}]`,
	}))
	defer srv.Close()
	prov := newSabnzbdForURL(t, srv)
	failures, err := prov.Test(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range failures {
		if f.PropertyName == "TvCategory" && strings.Contains(f.ErrorMessage, "does not exist") {
			found = true
		}
	}
	if !found {
		t.Errorf("failures = %+v, want TvCategory-missing failure", failures)
	}
}

func TestSabnzbd_TestJ103_CategoryJobFoldersDisabledFails(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(testSuccessHandler(testHandlerCfg{
		categories: `[{"name":"tv","dir":"/data/tv/*"}]`, // dir ends in '*'
	}))
	defer srv.Close()
	prov := newSabnzbdForURL(t, srv)
	failures, err := prov.Test(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range failures {
		if f.PropertyName == "TvCategory" && strings.Contains(f.ErrorMessage, "Job Folders") {
			found = true
		}
	}
	if !found {
		t.Errorf("failures = %+v, want JobFolders-disabled failure", failures)
	}
}

func TestSabnzbd_TestJ103_EmptyTvCategorySkipsProbe(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(testSuccessHandler(testHandlerCfg{
		categories: `[{"name":"other","dir":"/data/other"}]`, // no "tv"
	}))
	defer srv.Close()
	// Override TvCategory to empty via the settings injection.
	prov := newSabnzbdForURLMutate(t, srv, func(s *sabnzbd.Settings) {
		s.TvCategory = ""
	})
	failures, err := prov.Test(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range failures {
		if f.PropertyName == "TvCategory" && strings.Contains(f.ErrorMessage, "does not exist") {
			t.Errorf("failures = %+v, want no missing-category failure when TvCategory empty", failures)
		}
	}
}

// newSabnzbdForURLMutate is a variant of newSabnzbdForURL that
// applies a per-test settings mutator (TvCategory override etc).
func newSabnzbdForURLMutate(t *testing.T, srv *httptest.Server, mutate func(*sabnzbd.Settings)) *sabnzbd.Sabnzbd {
	t.Helper()
	s := buildSettings(srv.URL, "k3y")
	if mutate != nil {
		mutate(&s)
	}
	p, err := sabnzbd.NewProxy(srv.Client(), slog.Default())
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	return sabnzbd.New(nil, s, p, slog.Default())
}
