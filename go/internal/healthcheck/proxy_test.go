package healthcheck_test

import (
	"context"
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/configfile"
	"sonarr.tv/sonarr/internal/healthcheck"
)

// stubProxyConfig satisfies ProxyCheckConfig.
type stubProxyConfig struct {
	enabled  bool
	hostname string
}

func (s stubProxyConfig) ProxyEnabled() bool    { return s.enabled }
func (s stubProxyConfig) ProxyHostname() string { return s.hostname }

func makeProxyCheck(t *testing.T, cfg healthcheck.ProxyCheckConfig, resolve healthcheck.ProxyCheckResolver, pinger healthcheck.ProxyCheckPinger) *healthcheck.ProxyCheck {
	t.Helper()
	c, err := healthcheck.NewProxyCheck(cfg, resolve, pinger)
	if err != nil {
		t.Fatalf("NewProxyCheck: %v", err)
	}
	return c
}

func TestProxyCheck_NewRejectsNilConfig(t *testing.T) {
	t.Parallel()
	if _, err := healthcheck.NewProxyCheck(nil, nil, func(_ context.Context) (int, error) { return 200, nil }); err == nil {
		t.Errorf("expected error on nil config")
	}
}

func TestProxyCheck_NewRejectsNilPinger(t *testing.T) {
	t.Parallel()
	if _, err := healthcheck.NewProxyCheck(stubProxyConfig{}, nil, nil); err == nil {
		t.Errorf("expected error on nil pinger")
	}
}

func TestProxyCheck_DefaultsResolverToLookupIP(t *testing.T) {
	t.Parallel()
	// Default resolver wires to net.LookupIP. We don't actually run a
	// DNS query here — just verify the constructor accepts nil resolver
	// and Name returns the canonical identifier.
	c, err := healthcheck.NewProxyCheck(stubProxyConfig{}, nil, func(_ context.Context) (int, error) { return 200, nil })
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	if c.Name() != healthcheck.ProxyCheckName {
		t.Errorf("Name = %q", c.Name())
	}
}

func TestProxyCheck_DisabledReturnsOK(t *testing.T) {
	t.Parallel()
	c := makeProxyCheck(t,
		stubProxyConfig{enabled: false},
		func(_ string) ([]net.IP, error) { return nil, nil },
		func(_ context.Context) (int, error) { return 200, nil },
	)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("expected OK on disabled proxy, got %v", got.Type)
	}
}

func TestProxyCheck_UnresolvedHostnameReturnsResolveIpError(t *testing.T) {
	t.Parallel()
	c := makeProxyCheck(t,
		stubProxyConfig{enabled: true, hostname: "proxy.invalid"},
		func(_ string) ([]net.IP, error) { return nil, errors.New("nxdomain") },
		func(_ context.Context) (int, error) { return 200, nil },
	)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultError {
		t.Errorf("expected Error, got %v", got.Type)
	}
	if got.Reason != healthcheck.ReasonProxyResolveIp {
		t.Errorf("Reason = %v, want ReasonProxyResolveIp", got.Reason)
	}
	if !strings.Contains(got.Message, "proxy.invalid") {
		t.Errorf("Message %q should mention hostname", got.Message)
	}
}

func TestProxyCheck_ZeroAddressesReturnsResolveIpError(t *testing.T) {
	t.Parallel()
	// Resolver returns empty slice without an error — same outcome as
	// the C# `addresses.Any()` predicate failing.
	c := makeProxyCheck(t,
		stubProxyConfig{enabled: true, hostname: "proxy.example"},
		func(_ string) ([]net.IP, error) { return nil, nil },
		func(_ context.Context) (int, error) { return 200, nil },
	)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultError {
		t.Errorf("expected Error on zero addresses, got %v", got.Type)
	}
	if got.Reason != healthcheck.ReasonProxyResolveIp {
		t.Errorf("Reason = %v, want ReasonProxyResolveIp", got.Reason)
	}
}

func TestProxyCheck_PingerErrorReturnsProxyFailed(t *testing.T) {
	t.Parallel()
	c := makeProxyCheck(t,
		stubProxyConfig{enabled: true, hostname: "proxy.example"},
		func(_ string) ([]net.IP, error) { return []net.IP{net.ParseIP("10.0.0.1")}, nil },
		func(_ context.Context) (int, error) { return 0, errors.New("connection refused") },
	)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultError {
		t.Errorf("expected Error, got %v", got.Type)
	}
	if got.Reason != healthcheck.ReasonProxyFailed {
		t.Errorf("Reason = %v, want ReasonProxyFailed", got.Reason)
	}
	if !strings.Contains(got.Message, "connection refused") {
		t.Errorf("Message %q should include underlying error", got.Message)
	}
}

func TestProxyCheck_PingerBadRequestReturnsBadRequestError(t *testing.T) {
	t.Parallel()
	c := makeProxyCheck(t,
		stubProxyConfig{enabled: true, hostname: "proxy.example"},
		func(_ string) ([]net.IP, error) { return []net.IP{net.ParseIP("10.0.0.1")}, nil },
		func(_ context.Context) (int, error) { return 400, nil },
	)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultError {
		t.Errorf("expected Error, got %v", got.Type)
	}
	if got.Reason != healthcheck.ReasonProxyBadRequest {
		t.Errorf("Reason = %v, want ReasonProxyBadRequest", got.Reason)
	}
	if !strings.Contains(got.Message, "400") {
		t.Errorf("Message %q should mention status code", got.Message)
	}
}

func TestProxyCheck_PingerNon400StatusReturnsOK(t *testing.T) {
	t.Parallel()
	// C# only treats 400 as a failure. 5xx, 200, anything else → OK.
	cases := []int{200, 301, 401, 403, 500, 502, 503}
	for _, status := range cases {
		c := makeProxyCheck(t,
			stubProxyConfig{enabled: true, hostname: "proxy.example"},
			func(_ string) ([]net.IP, error) { return []net.IP{net.ParseIP("10.0.0.1")}, nil },
			func(_ context.Context) (int, error) { return status, nil },
		)
		got := c.Run(t.Context())
		if got.Type != healthcheck.ResultOK {
			t.Errorf("status=%d: expected OK, got %v / %s", status, got.Type, got.Message)
		}
	}
}

func TestProxyCheck_CheckOnStartupAndScheduleDefaults(t *testing.T) {
	t.Parallel()
	c := makeProxyCheck(t, stubProxyConfig{}, func(_ string) ([]net.IP, error) { return nil, nil }, func(_ context.Context) (int, error) { return 200, nil })
	if !c.CheckOnStartup() {
		t.Errorf("CheckOnStartup should default true")
	}
	if !c.CheckOnSchedule() {
		t.Errorf("CheckOnSchedule should default true")
	}
}

func TestProxyCheck_EventTriggersIncludesConfigFileSaved(t *testing.T) {
	t.Parallel()
	c := makeProxyCheck(t, stubProxyConfig{}, func(_ string) ([]net.IP, error) { return nil, nil }, func(_ context.Context) (int, error) { return 200, nil })
	want := reflect.TypeOf(configfile.ConfigFileSavedEvent{})
	found := false
	for _, tr := range c.EventTriggers() {
		if tr.EventType == want {
			found = true
		}
	}
	if !found {
		t.Errorf("expected ConfigFileSavedEvent trigger")
	}
}

func TestProxyCheck_InterfaceShape(t *testing.T) {
	t.Parallel()
	c := makeProxyCheck(t, stubProxyConfig{}, func(_ string) ([]net.IP, error) { return nil, nil }, func(_ context.Context) (int, error) { return 200, nil })
	var iface healthcheck.Check = c
	_ = iface
	var iface2 healthcheck.EventDrivenCheck = c
	_ = iface2
}
