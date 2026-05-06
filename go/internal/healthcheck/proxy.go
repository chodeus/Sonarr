package healthcheck

import (
	"context"
	"fmt"
	"net"
	"reflect"

	"sonarr.tv/sonarr/internal/configfile"
)

// ProxyCheckName is the canonical Source identifier.
const ProxyCheckName = "ProxyCheck"

// ProxyCheckConfig is the narrow config dependency the check needs:
// the user's proxy enable flag + hostname. Implementations typically
// wrap the larger config-service surface; the narrow interface keeps
// the check decoupled from a not-yet-ported full IConfigService.
type ProxyCheckConfig interface {
	// ProxyEnabled reports whether the user has the proxy turned on
	// (the C# `IConfigService.ProxyEnabled`).
	ProxyEnabled() bool
	// ProxyHostname returns the configured proxy hostname (the C#
	// `IConfigService.ProxyHostname`).
	ProxyHostname() string
}

// ProxyCheckResolver resolves a hostname to its A/AAAA records.
// Defaults to net.LookupIP at the call site; tests inject a stub.
//
// Returns (nil, nil) when the host has zero records — distinct from
// (nil, error) which signals a lookup failure. The check uses
// `len(addresses) == 0` to mean "host did not resolve" (mirrors the
// C# `Dns.GetHostAddresses(...).Any()` predicate at `:42`).
type ProxyCheckResolver func(host string) ([]net.IP, error)

// ProxyCheckPinger probes the configured cloud `/ping` endpoint
// THROUGH the configured proxy. Returns the HTTP status code; the
// check only treats 400 BadRequest as a failure (per C# `:63-75`),
// any other status (including 5xx network errors that surface as
// non-2xx) is ignored.
//
// Production wiring builds the request via the cloud-request-builder
// (C# `ISonarrCloudRequestBuilder.Services` at `:30`) and sends it
// through the active HttpClient; the Go-port detail lives in the
// callers since the cloud-request-builder hasn't been ported yet.
type ProxyCheckPinger func(ctx context.Context) (statusCode int, err error)

// ProxyCheck warns / errors when the configured HTTP proxy is
// unreachable or returns a 400 BadRequest from the upstream `/ping`
// probe. Mirrors
// `src/NzbDrone.Core/HealthCheck/Checks/ProxyCheck.cs:1-94`.
//
// Five outcomes:
//
//  1. Proxy disabled → OK (no probe).
//  2. Hostname doesn't resolve (zero A/AAAA records) → Error /
//     `ReasonProxyResolveIp`.
//  3. Probe returns HTTP 400 → Error / `ReasonProxyBadRequest`.
//  4. Probe raises any other error → Error / `ReasonProxyFailed`.
//  5. Probe returns any non-400 status → OK (the C# `:63-75` block
//     only considers 400 a failure).
type ProxyCheck struct {
	cfg     ProxyCheckConfig
	resolve ProxyCheckResolver
	pinger  ProxyCheckPinger
}

// NewProxyCheck builds the check. cfg + pinger MUST be non-nil; resolve
// defaults to net.LookupIP when nil (production wiring usually omits
// it).
func NewProxyCheck(cfg ProxyCheckConfig, resolve ProxyCheckResolver, pinger ProxyCheckPinger) (*ProxyCheck, error) {
	if cfg == nil {
		return nil, fmt.Errorf("healthcheck: NewProxyCheck: nil ProxyCheckConfig")
	}
	if pinger == nil {
		return nil, fmt.Errorf("healthcheck: NewProxyCheck: nil ProxyCheckPinger")
	}
	if resolve == nil {
		resolve = net.LookupIP
	}
	return &ProxyCheck{cfg: cfg, resolve: resolve, pinger: pinger}, nil
}

// Name returns the canonical check identifier.
func (*ProxyCheck) Name() string { return ProxyCheckName }

// CheckOnStartup defaults true.
func (*ProxyCheck) CheckOnStartup() bool { return true }

// CheckOnSchedule defaults true.
func (*ProxyCheck) CheckOnSchedule() bool { return true }

// EventTriggers wires the C# `[CheckOn(typeof(ConfigSavedEvent))]`
// attribute at `:14`. The Go port uses `ConfigFileSavedEvent` — the
// configfile package's signal-of-record for any persisted XML
// config change. Once the ConfigService port lands its own
// ConfigSavedEvent, this binding will follow the consolidation
// (CFG-Q5 in `analysis/questions.md`).
func (*ProxyCheck) EventTriggers() []EventTrigger {
	return []EventTrigger{
		{EventType: reflect.TypeOf(configfile.ConfigFileSavedEvent{})},
	}
}

// Run mirrors the C# `Check()` method at `:33-92`. Sequence:
//
//  1. Skip when proxy is disabled.
//  2. Resolve hostname; zero results → ResolveIp error.
//  3. Probe via pinger. Any error → ProxyFailed. Status 400 →
//     BadRequest. Otherwise OK.
func (c *ProxyCheck) Run(ctx context.Context) HealthCheck {
	if !c.cfg.ProxyEnabled() {
		return OK(ProxyCheckName)
	}

	host := c.cfg.ProxyHostname()
	addrs, err := c.resolve(host)
	if err != nil || len(addrs) == 0 {
		return New(
			ProxyCheckName,
			ResultError,
			ReasonProxyResolveIp,
			"Failed to resolve the IP address for the configured proxy host: "+host,
			"#proxy-failed-resolve-ip",
		)
	}

	status, err := c.pinger(ctx)
	if err != nil {
		return New(
			ProxyCheckName,
			ResultError,
			ReasonProxyFailed,
			"Failed to test proxy: "+err.Error(),
			"#proxy-failed-test",
		)
	}
	if status == 400 {
		return New(
			ProxyCheckName,
			ResultError,
			ReasonProxyBadRequest,
			fmt.Sprintf("Proxy returned BadRequest (status=%d) when testing connectivity", status),
			"#proxy-failed-test",
		)
	}
	return OK(ProxyCheckName)
}
