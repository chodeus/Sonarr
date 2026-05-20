package aria2_test

import (
	"context"
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/aria2"
	"sonarr.tv/sonarr/internal/indexers"
)

// ============================================================================
// Settings validation — slice 5.J50.
// ============================================================================

func TestSettings_DefaultsValid(t *testing.T) {
	t.Parallel()
	s := aria2.NewSettings()
	if failures := s.Validate(); len(failures) != 0 {
		t.Errorf("Validate(default): %+v, want no failures", failures)
	}
	if s.Host != "localhost" || s.Port != 6800 || s.RpcPath != "/rpc" || s.SecretToken != "MySecretToken" {
		t.Errorf("NewSettings: %+v (want Host=localhost, Port=6800, RpcPath=/rpc, SecretToken=MySecretToken)", s)
	}
}

func TestSettings_HostRequired(t *testing.T) {
	t.Parallel()
	s := aria2.NewSettings()
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
			s := aria2.NewSettings()
			s.Port = port
			if !hasFailure(s.Validate(), "Port") {
				t.Errorf("Port=%d: want Port failure", port)
			}
		})
	}
}

func TestSettings_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	def := &downloadclients.DownloadClient{
		Implementation: aria2.Implementation,
		ConfigContract: aria2.ConfigContract,
		Settings: `{
			"host":"aria.example",
			"port":7443,
			"rpcPath":"/jsonrpc",
			"useSsl":true,
			"secretToken":"hunter2",
			"directory":"/downloads"
		}`,
	}
	p, err := downloadclients.Default().Construct(def, downloadclients.Dependencies{})
	if err != nil {
		t.Fatalf("Construct: %v", err)
	}
	rt, ok := p.(*aria2.Aria2)
	if !ok {
		t.Fatalf("Construct: %T, want *aria2.Aria2", p)
	}
	got := rt.Settings()
	if got.Host != "aria.example" || got.Port != 7443 || got.RpcPath != "/jsonrpc" {
		t.Errorf("transport: %+v", got)
	}
	if !got.UseSsl || got.SecretToken != "hunter2" {
		t.Errorf("auth/ssl: %+v", got)
	}
	if got.Directory != "/downloads" {
		t.Errorf("directory: %+v", got)
	}
}

// ============================================================================
// Provider identity + stubs — slice 5.J50.
// ============================================================================

func TestProvider_NameAndProtocol(t *testing.T) {
	t.Parallel()
	a := aria2.New(nil, aria2.NewSettings(), nil, nil)
	if a.Name() != "Aria2" {
		t.Errorf("Name: %q, want Aria2", a.Name())
	}
	if a.Protocol() != indexers.DownloadProtocolTorrent {
		t.Errorf("Protocol: %v, want torrent", a.Protocol())
	}
}

func TestProvider_StubsReturnNotImplementedYet(t *testing.T) {
	t.Parallel()
	a := aria2.New(nil, aria2.NewSettings(), nil, nil)
	ctx := context.Background()
	// Download is wired in 5.J52 — nil-proxy surfaces a generic error.
	if _, err := a.Download(ctx, &downloadclients.GrabRequest{}); err == nil {
		t.Errorf("Download: want error (nil proxy)")
	}
	if _, err := a.GetItems(ctx); !errors.Is(err, downloadclients.ErrNotImplementedYet) {
		t.Errorf("GetItems: %v, want ErrNotImplementedYet", err)
	}
	if err := a.RemoveItem(ctx, &downloadclients.Item{}, false); !errors.Is(err, downloadclients.ErrNotImplementedYet) {
		t.Errorf("RemoveItem: %v, want ErrNotImplementedYet", err)
	}
	if _, err := a.GetStatus(ctx); !errors.Is(err, downloadclients.ErrNotImplementedYet) {
		t.Errorf("GetStatus: %v, want ErrNotImplementedYet", err)
	}
	if err := a.MarkItemAsImported(ctx, &downloadclients.Item{}); !errors.Is(err, downloadclients.ErrNotImplementedYet) {
		t.Errorf("MarkItemAsImported: %v, want ErrNotImplementedYet", err)
	}
}

func TestProvider_GetImportItemPassthrough(t *testing.T) {
	t.Parallel()
	a := aria2.New(nil, aria2.NewSettings(), nil, nil)
	in := &downloadclients.Item{DownloadID: "abc"}
	got, err := a.GetImportItem(context.Background(), in, nil)
	if err != nil {
		t.Fatalf("GetImportItem: %v", err)
	}
	if got != in {
		t.Errorf("GetImportItem: %+v, want passthrough", got)
	}
}

func TestProvider_RequestActionReturnsNilNil(t *testing.T) {
	t.Parallel()
	a := aria2.New(nil, aria2.NewSettings(), nil, nil)
	out, err := a.RequestAction(context.Background(), "anything", nil)
	if err != nil || out != nil {
		t.Errorf("RequestAction: (%v, %v), want (nil, nil)", out, err)
	}
}

func TestProvider_TestSurfacesSettingsFailures(t *testing.T) {
	t.Parallel()
	s := aria2.NewSettings()
	s.Host = ""
	a := aria2.New(nil, s, nil, nil)
	failures, err := a.Test(context.Background())
	if err != nil {
		t.Errorf("Test: %v", err)
	}
	if !hasFailure(failures, "Host") {
		t.Errorf("Test: %+v, want Host failure surfaced", failures)
	}
}

// ============================================================================
// Registry registration — slice 5.J50.
// ============================================================================

func TestRegistry_Aria2Registered(t *testing.T) {
	t.Parallel()
	if !downloadclients.Default().Has(aria2.Implementation, aria2.ConfigContract) {
		t.Fatalf("registry has no entry for (%s, %s)", aria2.Implementation, aria2.ConfigContract)
	}
}

func TestRegistry_ConstructBadJSON(t *testing.T) {
	t.Parallel()
	def := &downloadclients.DownloadClient{
		Implementation: aria2.Implementation,
		ConfigContract: aria2.ConfigContract,
		Settings:       `{bad json`,
	}
	if _, err := downloadclients.Default().Construct(def, downloadclients.Dependencies{}); err == nil {
		t.Errorf("Construct(bad JSON): want error")
	}
}

func TestRegistry_DefaultsUsedWhenSettingsEmpty(t *testing.T) {
	t.Parallel()
	def := &downloadclients.DownloadClient{
		Implementation: aria2.Implementation,
		ConfigContract: aria2.ConfigContract,
		Settings:       "",
	}
	p, err := downloadclients.Default().Construct(def, downloadclients.Dependencies{})
	if err != nil {
		t.Fatalf("Construct: %v", err)
	}
	got := p.(*aria2.Aria2).Settings()
	if got.Host != "localhost" || got.Port != 6800 || got.RpcPath != "/rpc" {
		t.Errorf("default Settings: %+v, want NewSettings defaults", got)
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
