package utorrent_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/utorrent"
	"sonarr.tv/sonarr/internal/indexers"
)

// ============================================================================
// Settings validation — slice 5.J46.
// ============================================================================

func TestSettings_DefaultsValid(t *testing.T) {
	t.Parallel()
	s := utorrent.NewSettings()
	if failures := s.Validate(); len(failures) != 0 {
		t.Errorf("Validate(default): %+v, want no failures", failures)
	}
	if s.Host != "localhost" || s.Port != 8080 || s.TvCategory != "tv-sonarr" {
		t.Errorf("NewSettings: %+v (want Host=localhost, Port=8080, TvCategory=tv-sonarr)", s)
	}
}

func TestSettings_HostRequired(t *testing.T) {
	t.Parallel()
	s := utorrent.NewSettings()
	s.Host = ""
	if !hasFailure(s.Validate(), "Host") {
		t.Errorf("empty host: want Host failure")
	}
}

func TestSettings_PortOutOfRange(t *testing.T) {
	t.Parallel()
	for _, port := range []int{0, -1, 65536} {
		port := port
		t.Run("", func(t *testing.T) {
			t.Parallel()
			s := utorrent.NewSettings()
			s.Port = port
			if !hasFailure(s.Validate(), "Port") {
				t.Errorf("Port=%d: want Port failure", port)
			}
		})
	}
}

func TestSettings_UrlBaseMustStartWithSlash(t *testing.T) {
	t.Parallel()
	s := utorrent.NewSettings()
	s.UrlBase = "utorrent" // no leading slash
	if !hasFailure(s.Validate(), "UrlBase") {
		t.Errorf("UrlBase without leading /: want UrlBase failure")
	}
}

func TestSettings_UrlBaseSlashPrefixOk(t *testing.T) {
	t.Parallel()
	s := utorrent.NewSettings()
	s.UrlBase = "/utorrent"
	if hasFailure(s.Validate(), "UrlBase") {
		t.Errorf("UrlBase with leading /: should pass")
	}
}

func TestSettings_CategoryRequired(t *testing.T) {
	t.Parallel()
	s := utorrent.NewSettings()
	s.TvCategory = ""
	if !hasFailure(s.Validate(), "TvCategory") {
		t.Errorf("empty category: want TvCategory failure")
	}
}

func TestSettings_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	def := &downloadclients.DownloadClient{
		Implementation: utorrent.Implementation,
		ConfigContract: utorrent.ConfigContract,
		Settings: `{
			"host":"utor.example",
			"port":8443,
			"useSsl":true,
			"urlBase":"/utorrent",
			"username":"sonarr",
			"password":"hunter2",
			"tvCategory":"tv-sonarr",
			"tvImportedCategory":"tv-done",
			"recentTvPriority":1,
			"olderTvPriority":0,
			"intialState":2
		}`,
	}
	p, err := downloadclients.Default().Construct(def, downloadclients.Dependencies{})
	if err != nil {
		t.Fatalf("Construct: %v", err)
	}
	rt, ok := p.(*utorrent.UTorrent)
	if !ok {
		t.Fatalf("Construct: %T, want *utorrent.UTorrent", p)
	}
	got := rt.Settings()
	if got.Host != "utor.example" || got.Port != 8443 || !got.UseSsl || got.UrlBase != "/utorrent" {
		t.Errorf("transport: %+v", got)
	}
	if got.Username != "sonarr" || got.Password != "hunter2" {
		t.Errorf("auth: %+v", got)
	}
	if got.TvCategory != "tv-sonarr" || got.TvImportedCategory != "tv-done" {
		t.Errorf("category: %+v", got)
	}
	if got.RecentTvPriority != int(utorrent.PriorityFirst) || got.OlderTvPriority != int(utorrent.PriorityLast) {
		t.Errorf("priority: %+v", got)
	}
	if got.IntialState != int(utorrent.StatePause) {
		t.Errorf("initial state: %d, want StatePause=2", got.IntialState)
	}
}

// ============================================================================
// Provider identity + stubs — slice 5.J46.
// ============================================================================

func TestProvider_NameAndProtocol(t *testing.T) {
	t.Parallel()
	u := utorrent.New(nil, utorrent.NewSettings(), nil, nil)
	if u.Name() != "uTorrent" {
		t.Errorf("Name: %q, want uTorrent (lowercase u)", u.Name())
	}
	if u.Protocol() != indexers.DownloadProtocolTorrent {
		t.Errorf("Protocol: %v, want torrent", u.Protocol())
	}
}

func TestProvider_StubsReturnNotImplementedYet(t *testing.T) {
	t.Parallel()
	u := utorrent.New(nil, utorrent.NewSettings(), nil, nil)
	ctx := context.Background()
	// Download is wired in 5.J48 — nil-proxy surfaces a generic error.
	if _, err := u.Download(ctx, &downloadclients.GrabRequest{}); err == nil {
		t.Errorf("Download: want error (nil proxy)")
	}
	// GetItems / RemoveItem / MarkItemAsImported are wired in 5.J49
	// — nil-proxy surfaces a generic error. GetStatus returns a
	// minimal ClientInfo without error (matches base posture).
	if _, err := u.GetItems(ctx); err == nil {
		t.Errorf("GetItems: want error (nil proxy)")
	}
	if err := u.RemoveItem(ctx, &downloadclients.Item{DownloadID: "x"}, false); err == nil {
		t.Errorf("RemoveItem: want error (nil proxy)")
	}
	if info, err := u.GetStatus(ctx); err != nil || info == nil {
		t.Errorf("GetStatus: got (%v, %v), want (non-nil, nil)", info, err)
	}
	if err := u.MarkItemAsImported(ctx, &downloadclients.Item{}); err == nil {
		t.Errorf("MarkItemAsImported: want error (nil proxy)")
	}
}

func TestProvider_GetImportItemPassthrough(t *testing.T) {
	t.Parallel()
	u := utorrent.New(nil, utorrent.NewSettings(), nil, nil)
	in := &downloadclients.Item{DownloadID: "abc"}
	got, err := u.GetImportItem(context.Background(), in, nil)
	if err != nil {
		t.Fatalf("GetImportItem: %v", err)
	}
	if got != in {
		t.Errorf("GetImportItem: %+v, want passthrough of in", got)
	}
}

func TestProvider_RequestActionReturnsNilNil(t *testing.T) {
	t.Parallel()
	u := utorrent.New(nil, utorrent.NewSettings(), nil, nil)
	out, err := u.RequestAction(context.Background(), "getCategories", nil)
	if err != nil || out != nil {
		t.Errorf("RequestAction: got (%v, %v), want (nil, nil)", out, err)
	}
}

func TestProvider_TestSurfacesSettingsFailures(t *testing.T) {
	t.Parallel()
	s := utorrent.NewSettings()
	s.Host = ""
	u := utorrent.New(nil, s, nil, nil)
	failures, err := u.Test(context.Background())
	if err != nil {
		t.Errorf("Test: %v", err)
	}
	if !hasFailure(failures, "Host") {
		t.Errorf("Test: %+v, want Host failure surfaced", failures)
	}
}

// --- J109 TestGetTorrents -----------------------------------------

func TestProvider_TestJ109_TestGetTorrentsFires(t *testing.T) {
	t.Parallel()
	h := newUTorrentServer(t, "TOKEN", sampleGetSettingsResponse)
	p := utorrent.NewProxy(h.srv.Client(), nil)
	u := utorrent.New(nil, h.settings(), p, nil)
	failures, err := u.Test(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 0 {
		t.Errorf("failures = %+v, want none", failures)
	}
	// J109 — Test now fires getsettings + list=1 against /gui/.
	if h.calls.Load() != 2 {
		t.Errorf("gui calls = %d, want 2 (getsettings + list=1)", h.calls.Load())
	}
}

func TestProvider_TestJ109_TestGetTorrentsFailureSurfaces(t *testing.T) {
	t.Parallel()
	// Dispatch by action so getsettings succeeds (returns the canned
	// build) while list=1 returns a 500. The Test arm should
	// surface a whole-form torrent-list failure.
	token := "TOKEN"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/token.html") {
			http.SetCookie(w, &http.Cookie{Name: "GUID", Value: "g"})
			_, _ = io.WriteString(w, `<html><div id='token' style='display:none;'>`+token+`</div></html>`)
			return
		}
		action := r.URL.Query().Get("action")
		w.Header().Set("Content-Type", "application/json")
		switch action {
		case "getsettings":
			_, _ = io.WriteString(w, sampleGetSettingsResponse)
		default:
			// list=1 has no action= param — falls through here.
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	settings := utorrent.NewSettings()
	settings.Host = u.Hostname()
	settings.Port = port
	p := utorrent.NewProxy(srv.Client(), nil)
	prov := utorrent.New(nil, settings, p, nil)
	failures, err := prov.Test(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range failures {
		if f.PropertyName == "" && strings.Contains(f.ErrorMessage, "list of torrents") {
			found = true
		}
	}
	if !found {
		t.Errorf("failures = %+v, want whole-form 'list of torrents' failure", failures)
	}
}

func TestProvider_TestJ109_VersionFailureSkipsListProbe(t *testing.T) {
	t.Parallel()
	// Version returns build well below minBuild — Test must short-
	// circuit BEFORE firing list=1.
	h := newUTorrentServer(t, "TOKEN", `{"build":1000}`)
	p := utorrent.NewProxy(h.srv.Client(), nil)
	u := utorrent.New(nil, h.settings(), p, nil)
	if _, err := u.Test(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.calls.Load() != 1 {
		t.Errorf("gui calls = %d, want 1 (list=1 must not fire below minBuild)", h.calls.Load())
	}
}

// ============================================================================
// Registry registration — slice 5.J46.
// ============================================================================

func TestRegistry_UTorrentRegistered(t *testing.T) {
	t.Parallel()
	if !downloadclients.Default().Has(utorrent.Implementation, utorrent.ConfigContract) {
		t.Fatalf("registry has no entry for (%s, %s)", utorrent.Implementation, utorrent.ConfigContract)
	}
}

func TestRegistry_ConstructBadJSON(t *testing.T) {
	t.Parallel()
	def := &downloadclients.DownloadClient{
		Implementation: utorrent.Implementation,
		ConfigContract: utorrent.ConfigContract,
		Settings:       `{bad json`,
	}
	if _, err := downloadclients.Default().Construct(def, downloadclients.Dependencies{}); err == nil {
		t.Errorf("Construct(bad JSON): want error")
	}
}

func TestRegistry_DefaultsUsedWhenSettingsEmpty(t *testing.T) {
	t.Parallel()
	def := &downloadclients.DownloadClient{
		Implementation: utorrent.Implementation,
		ConfigContract: utorrent.ConfigContract,
		Settings:       "",
	}
	p, err := downloadclients.Default().Construct(def, downloadclients.Dependencies{})
	if err != nil {
		t.Fatalf("Construct: %v", err)
	}
	got := p.(*utorrent.UTorrent).Settings()
	if got.Host != "localhost" || got.Port != 8080 {
		t.Errorf("default Settings: %+v, want NewSettings defaults", got)
	}
}

// ============================================================================
// Priority + State String round-trips — slice 5.J46.
// ============================================================================

func TestPriority_String(t *testing.T) {
	t.Parallel()
	cases := []struct {
		p    utorrent.Priority
		want string
	}{
		{utorrent.PriorityLast, "Last"},
		{utorrent.PriorityFirst, "First"},
	}
	for _, tc := range cases {
		if got := tc.p.String(); got != tc.want {
			t.Errorf("(%d).String() = %q, want %q", int(tc.p), got, tc.want)
		}
	}
}

func TestState_String(t *testing.T) {
	t.Parallel()
	cases := []struct {
		s    utorrent.State
		want string
	}{
		{utorrent.StateStart, "Start"},
		{utorrent.StateForceStart, "ForceStart"},
		{utorrent.StatePause, "Pause"},
		{utorrent.StateStop, "Stop"},
	}
	for _, tc := range cases {
		if got := tc.s.String(); got != tc.want {
			t.Errorf("(%d).String() = %q, want %q", int(tc.s), got, tc.want)
		}
	}
}

func hasFailure(failures []downloadclients.TestFailure, prop string) bool {
	for _, f := range failures {
		if f.PropertyName == prop {
			return true
		}
	}
	return false
}
