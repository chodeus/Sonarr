package rtorrent_test

import (
	"context"
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/rtorrent"
	"sonarr.tv/sonarr/internal/indexers"
)

// ============================================================================
// Settings validation — slice 5.J41.
// ============================================================================

func TestSettings_DefaultsValid(t *testing.T) {
	t.Parallel()
	s := rtorrent.NewSettings()
	if failures := s.Validate(); len(failures) != 0 {
		t.Errorf("Validate(default) = %+v, want no failures", failures)
	}
	if s.Host != "localhost" || s.Port != 8080 || s.UrlBase != "RPC2" {
		t.Errorf("NewSettings: %+v (want Host=localhost, Port=8080, UrlBase=RPC2)", s)
	}
	if s.TvCategory != "tv-sonarr" {
		t.Errorf("NewSettings.TvCategory: %q, want tv-sonarr", s.TvCategory)
	}
	if s.RecentTvPriority != int(rtorrent.PriorityNormal) || s.OlderTvPriority != int(rtorrent.PriorityNormal) {
		t.Errorf("priorities: Recent=%d Older=%d, want both PriorityNormal=2",
			s.RecentTvPriority, s.OlderTvPriority)
	}
}

func TestSettings_HostRequired(t *testing.T) {
	t.Parallel()
	s := rtorrent.NewSettings()
	s.Host = ""
	failures := s.Validate()
	if !containsFailure(failures, "Host") {
		t.Errorf("Validate(empty host): %+v, want Host failure", failures)
	}
}

func TestSettings_PortOutOfRange(t *testing.T) {
	t.Parallel()
	for _, port := range []int{0, -1, 65536, 100000} {
		port := port
		t.Run("", func(t *testing.T) {
			t.Parallel()
			s := rtorrent.NewSettings()
			s.Port = port
			if !containsFailure(s.Validate(), "Port") {
				t.Errorf("Validate(Port=%d): want Port failure", port)
			}
		})
	}
}

func TestSettings_PortBoundaryOk(t *testing.T) {
	t.Parallel()
	for _, port := range []int{1, 65535} {
		port := port
		t.Run("", func(t *testing.T) {
			t.Parallel()
			s := rtorrent.NewSettings()
			s.Port = port
			if containsFailure(s.Validate(), "Port") {
				t.Errorf("Validate(Port=%d): unexpected Port failure", port)
			}
		})
	}
}

func TestSettings_CategoryEmptyFailure(t *testing.T) {
	t.Parallel()
	s := rtorrent.NewSettings()
	s.TvCategory = ""
	if !containsFailure(s.Validate(), "TvCategory") {
		t.Errorf("Validate(empty category): want TvCategory failure")
	}
}

func TestSettings_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	// Spot-check the JSON tag names match the C# wire shape. The
	// React UI's DownloadClient.Settings round-trips against these
	// names — drift here breaks the form.
	def := &downloadclients.DownloadClient{
		Implementation: rtorrent.Implementation,
		ConfigContract: rtorrent.ConfigContract,
		Settings: `{
			"host":"rtor.example",
			"port":8443,
			"useSsl":true,
			"urlBase":"RPC2",
			"username":"sonarr",
			"password":"hunter2",
			"tvCategory":"tv-sonarr",
			"tvImportedCategory":"tv-done",
			"tvDirectory":"/downloads/tv",
			"recentTvPriority":3,
			"olderTvPriority":1,
			"addStopped":true
		}`,
	}
	p, err := downloadclients.Default().Construct(def, downloadclients.Dependencies{})
	if err != nil {
		t.Fatalf("Construct: %v", err)
	}
	rt, ok := p.(*rtorrent.RTorrent)
	if !ok {
		t.Fatalf("Construct: %T, want *rtorrent.RTorrent", p)
	}
	got := rt.Settings()
	if got.Host != "rtor.example" || got.Port != 8443 || !got.UseSsl {
		t.Errorf("host/port/ssl: %+v", got)
	}
	if got.Username != "sonarr" || got.Password != "hunter2" {
		t.Errorf("auth: %+v", got)
	}
	if got.TvCategory != "tv-sonarr" || got.TvImportedCategory != "tv-done" || got.TvDirectory != "/downloads/tv" {
		t.Errorf("category/dir: %+v", got)
	}
	if got.RecentTvPriority != int(rtorrent.PriorityHigh) || got.OlderTvPriority != int(rtorrent.PriorityLow) {
		t.Errorf("priorities: %+v", got)
	}
	if !got.AddStopped {
		t.Errorf("AddStopped: false, want true")
	}
}

// ============================================================================
// Provider identity + stubs — slice 5.J41.
// ============================================================================

func TestProvider_NameAndProtocol(t *testing.T) {
	t.Parallel()
	rt := rtorrent.New(nil, rtorrent.NewSettings(), nil, nil)
	if rt.Name() != "rTorrent" {
		t.Errorf("Name: %q, want rTorrent (lowercase r)", rt.Name())
	}
	if rt.Protocol() != indexers.DownloadProtocolTorrent {
		t.Errorf("Protocol: %v, want torrent", rt.Protocol())
	}
}

func TestProvider_StubsReturnNotImplementedYet(t *testing.T) {
	t.Parallel()
	rt := rtorrent.New(nil, rtorrent.NewSettings(), nil, nil)
	ctx := context.Background()

	if _, err := rt.Download(ctx, &downloadclients.GrabRequest{}); !errors.Is(err, downloadclients.ErrNotImplementedYet) {
		t.Errorf("Download: %v, want ErrNotImplementedYet", err)
	}
	if _, err := rt.GetItems(ctx); !errors.Is(err, downloadclients.ErrNotImplementedYet) {
		t.Errorf("GetItems: %v, want ErrNotImplementedYet", err)
	}
	if err := rt.RemoveItem(ctx, &downloadclients.Item{}, false); !errors.Is(err, downloadclients.ErrNotImplementedYet) {
		t.Errorf("RemoveItem: %v, want ErrNotImplementedYet", err)
	}
	if _, err := rt.GetStatus(ctx); !errors.Is(err, downloadclients.ErrNotImplementedYet) {
		t.Errorf("GetStatus: %v, want ErrNotImplementedYet", err)
	}
	if err := rt.MarkItemAsImported(ctx, &downloadclients.Item{}); !errors.Is(err, downloadclients.ErrNotImplementedYet) {
		t.Errorf("MarkItemAsImported: %v, want ErrNotImplementedYet", err)
	}
}

func TestProvider_GetImportItemPassesThrough(t *testing.T) {
	t.Parallel()
	rt := rtorrent.New(nil, rtorrent.NewSettings(), nil, nil)
	in := &downloadclients.Item{DownloadID: "abc"}
	out, err := rt.GetImportItem(context.Background(), in, nil)
	if err != nil {
		t.Fatalf("GetImportItem: %v", err)
	}
	if out != in {
		t.Errorf("GetImportItem: returned %+v, want passthrough of in (%+v)", out, in)
	}
}

func TestProvider_RequestActionReturnsNilNil(t *testing.T) {
	t.Parallel()
	rt := rtorrent.New(nil, rtorrent.NewSettings(), nil, nil)
	out, err := rt.RequestAction(context.Background(), "getCategories", nil)
	if err != nil {
		t.Errorf("RequestAction: %v", err)
	}
	if out != nil {
		t.Errorf("RequestAction: %+v, want nil", out)
	}
}

func TestProvider_TestSurfacesSettingsFailures(t *testing.T) {
	t.Parallel()
	s := rtorrent.NewSettings()
	s.Host = "" // → Host failure
	rt := rtorrent.New(nil, s, nil, nil)
	failures, err := rt.Test(context.Background())
	if err != nil {
		t.Errorf("Test: %v", err)
	}
	if !containsFailure(failures, "Host") {
		t.Errorf("Test: %+v, want Host failure surfaced", failures)
	}
}

func TestProvider_TestProbesVersionWhenProxyWired(t *testing.T) {
	t.Parallel()
	h := newXMLRPCServer(t, sampleVersionResponse)
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	rt := rtorrent.New(nil, h.settings("RPC2"), p, nil)
	failures, err := rt.Test(context.Background())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("Test on good version: %+v, want no failures", failures)
	}
	if h.calls.Load() != 1 {
		t.Errorf("system.client_version probe didn't fire (%d calls)", h.calls.Load())
	}
}

func TestProvider_TestRejectsBelowMinVersion(t *testing.T) {
	t.Parallel()
	resp := `<?xml version="1.0"?>
<methodResponse><params><param><value><string>0.8.5</string></value></param></params></methodResponse>`
	h := newXMLRPCServer(t, resp)
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	rt := rtorrent.New(nil, h.settings("RPC2"), p, nil)
	failures, err := rt.Test(context.Background())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if !containsFailure(failures, "Host") {
		t.Errorf("Test on old version: %+v, want Host failure (too old)", failures)
	}
}

func TestProvider_TestNetworkFailureSurfacesHostFailure(t *testing.T) {
	t.Parallel()
	// Use a closed httptest server URL — connect should fail.
	h := newXMLRPCServer(t, sampleVersionResponse)
	h.srv.Close() // force connection failure
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	rt := rtorrent.New(nil, h.settings("RPC2"), p, nil)
	failures, err := rt.Test(context.Background())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if !containsFailure(failures, "Host") {
		t.Errorf("Test on dead host: %+v, want Host failure", failures)
	}
}

// ============================================================================
// Registry registration — slice 5.J41.
// ============================================================================

func TestRegistry_RTorrentRegistered(t *testing.T) {
	t.Parallel()
	if !downloadclients.Default().Has(rtorrent.Implementation, rtorrent.ConfigContract) {
		t.Fatalf("Default registry has no entry for (%s,%s)",
			rtorrent.Implementation, rtorrent.ConfigContract)
	}
}

func TestRegistry_ConstructBadJSON(t *testing.T) {
	t.Parallel()
	def := &downloadclients.DownloadClient{
		Implementation: rtorrent.Implementation,
		ConfigContract: rtorrent.ConfigContract,
		Settings:       `{bad json`,
	}
	if _, err := downloadclients.Default().Construct(def, downloadclients.Dependencies{}); err == nil {
		t.Errorf("Construct(bad JSON) err = nil, want error")
	}
}

func TestRegistry_ConstructNilRow(t *testing.T) {
	t.Parallel()
	if _, err := downloadclients.Default().Construct(nil, downloadclients.Dependencies{}); err == nil {
		t.Errorf("Construct(nil): want error")
	}
}

func TestRegistry_DefaultsUsedWhenSettingsEmpty(t *testing.T) {
	t.Parallel()
	def := &downloadclients.DownloadClient{
		Implementation: rtorrent.Implementation,
		ConfigContract: rtorrent.ConfigContract,
		Settings:       "",
	}
	p, err := downloadclients.Default().Construct(def, downloadclients.Dependencies{})
	if err != nil {
		t.Fatalf("Construct: %v", err)
	}
	got := p.(*rtorrent.RTorrent).Settings()
	if got.Host != "localhost" || got.Port != 8080 {
		t.Errorf("default Settings = %+v, want NewSettings defaults", got)
	}
}

// ============================================================================
// Priority enum String — slice 5.J41.
// ============================================================================

func TestPriority_String(t *testing.T) {
	t.Parallel()
	cases := []struct {
		p    rtorrent.Priority
		want string
	}{
		{rtorrent.PriorityVeryLow, "VeryLow"},
		{rtorrent.PriorityLow, "Low"},
		{rtorrent.PriorityNormal, "Normal"},
		{rtorrent.PriorityHigh, "High"},
	}
	for _, tc := range cases {
		if got := tc.p.String(); got != tc.want {
			t.Errorf("(%d).String() = %q, want %q", int(tc.p), got, tc.want)
		}
	}
}

// containsFailure is a tiny helper — the testify-style "any failure
// whose PropertyName matches" check.
func containsFailure(failures []downloadclients.TestFailure, prop string) bool {
	for _, f := range failures {
		if f.PropertyName == prop {
			return true
		}
	}
	return false
}
