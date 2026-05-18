package deluge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
)

// Implementation + ConfigContract are the persisted-row identifiers
// for the Deluge download client. Mirrors C# class names `Deluge` +
// `DelugeSettings` (see
// `src/NzbDrone.Core/Download/Clients/Deluge/Deluge.cs` and
// `DelugeSettings.cs:18`). The (Implementation, ConfigContract) pair
// is the registry key — see [downloadclients.Registry] doc.
const (
	Implementation = "Deluge"
	ConfigContract = "DelugeSettings"
)

// Deluge is the [downloadclients.Provider] implementation for the
// Deluge torrent client. Ports C# `Deluge` at
// `src/NzbDrone.Core/Download/Clients/Deluge/Deluge.cs` + the
// `DelugeProxy.cs` JSON-RPC body.
//
// Slice 5.G2c scope: Name / Protocol / Definition / RequestAction /
// GetImportItem / Test live. Download / GetItems / RemoveItem /
// GetStatus / MarkItemAsImported return
// [downloadclients.ErrNotImplementedYet]. Subsequent slices fill in
// each method.
type Deluge struct {
	settings Settings
	proxy    *Proxy
	def      *downloadclients.DownloadClient
	log      *slog.Logger
}

// New constructs a Deluge Provider against the resolved settings +
// proxy. Used by both the registry [Constructor] and by tests that
// pin a stub HTTPDoer.
func New(def *downloadclients.DownloadClient, settings Settings, proxy *Proxy, log *slog.Logger) *Deluge {
	if log == nil {
		log = slog.Default()
	}
	return &Deluge{settings: settings, proxy: proxy, def: def, log: log}
}

// Name returns the user-facing label. Mirrors C# `Deluge.Name`
// (inherited default — the class name itself; see
// `DownloadClientBase.cs:62`).
func (d *Deluge) Name() string { return "Deluge" }

// Protocol returns the protocol family. Mirrors C#
// `TorrentClientBase.Protocol` at `TorrentClientBase.cs:44`.
func (d *Deluge) Protocol() indexers.DownloadProtocol {
	return indexers.DownloadProtocolTorrent
}

// Definition returns the persisted DownloadClients row.
func (d *Deluge) Definition() *downloadclients.DownloadClient { return d.def }

// Settings returns the resolved Settings struct. Exposed for the
// 5.G2d+ slices and tests.
func (d *Deluge) Settings() Settings { return d.settings }

// Proxy returns the bound proxy.
func (d *Deluge) Proxy() *Proxy { return d.proxy }

// Download is filled in by slice 5.G2e. Returns
// [downloadclients.ErrNotImplementedYet] until then.
func (d *Deluge) Download(_ context.Context, _ *downloadclients.GrabRequest) (string, error) {
	return "", downloadclients.ErrNotImplementedYet
}

// GetItems is filled in by slice 5.G2d. Returns
// [downloadclients.ErrNotImplementedYet] until then.
func (d *Deluge) GetItems(_ context.Context) ([]*downloadclients.Item, error) {
	return nil, downloadclients.ErrNotImplementedYet
}

// GetImportItem mirrors the C# base default — pass input through.
// `DownloadClientBase.cs:102-105`.
func (d *Deluge) GetImportItem(_ context.Context, item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

// RemoveItem is filled in by slice 5.G2f. Returns
// [downloadclients.ErrNotImplementedYet] until then.
func (d *Deluge) RemoveItem(_ context.Context, _ *downloadclients.Item, _ bool) error {
	return downloadclients.ErrNotImplementedYet
}

// GetStatus is filled in by slice 5.G2g. Returns
// [downloadclients.ErrNotImplementedYet] until then.
func (d *Deluge) GetStatus(_ context.Context) (*downloadclients.ClientInfo, error) {
	return nil, downloadclients.ErrNotImplementedYet
}

// MarkItemAsImported is filled in by slice 5.G2h. Returns
// [downloadclients.ErrNotImplementedYet] until then.
func (d *Deluge) MarkItemAsImported(_ context.Context, _ *downloadclients.Item) error {
	return downloadclients.ErrNotImplementedYet
}

// Test ports the connectivity + auth probe from C#
// `Deluge.TestConnection` at
// `src/NzbDrone.Core/Download/Clients/Deluge/Deluge.cs:276-323`.
//
// Flow:
//
//  1. Run Settings.Validate first — structural failures
//     short-circuit so a malformed config never triggers a network
//     round-trip.
//  2. Ensure the proxy is bound. Nil proxy is a programmer-error
//     path (non-nil error vs TestFailure) — the registry
//     constructor guarantees a non-nil proxy in production.
//  3. proxy.EnsureAuth(force=true) → field-level TestFailures pinned
//     per C# branches at `:282-319`: ErrAuthFailed → Password,
//     ErrUnavailable → Host (matches `:288-294`).
//  4. Probe daemon.info via proxy.Call → connectivity + version
//     surface. The C# TestConnection at `:280` calls
//     `_proxy.GetVersion(Settings)` which falls back from
//     daemon.get_version to daemon.info per `DelugeProxy.cs:52-65`
//     — the Go port simplifies to a direct daemon.info probe since
//     daemon.info is present in every Deluge version that ships
//     the WebUI JSON-RPC (1.3.6+).
//
// No version floor enforced — the C# tree's TestConnection doesn't
// check version numerics, just connectivity + auth. Matches.
//
// TestCategory + TestGetTorrents at C# `:325-385` are deferred —
// they require label-plugin probes that land at 5.G2h + GetItems
// at 5.G2d.
func (d *Deluge) Test(ctx context.Context) ([]downloadclients.TestFailure, error) {
	if failures := d.settings.Validate(); len(failures) > 0 {
		return failures, nil
	}
	if d.proxy == nil {
		return nil, errors.New("deluge: Test: nil proxy (constructor not wired)")
	}

	if _, err := d.proxy.EnsureAuth(ctx, d.settings, true); err != nil {
		if failure := classifyAuthError(err); failure != nil {
			return []downloadclients.TestFailure{*failure}, nil
		}
		return nil, err
	}

	raw, err := d.proxy.Call(ctx, "daemon.info", nil, d.settings)
	if err != nil {
		if failure := classifyAuthError(err); failure != nil {
			return []downloadclients.TestFailure{*failure}, nil
		}
		return nil, err
	}

	var version string
	if err := json.Unmarshal(raw, &version); err != nil {
		return []downloadclients.TestFailure{{
			PropertyName: "Version",
			ErrorMessage: fmt.Sprintf("Unable to parse Deluge version response: %v", err),
		}}, nil
	}
	if strings.TrimSpace(version) == "" {
		return []downloadclients.TestFailure{{
			PropertyName: "Version",
			ErrorMessage: "Deluge returned an empty version string",
		}}, nil
	}

	// TestCategory + TestGetTorrents (C# :272-273) deferred to
	// the GetItems + label-plugin slices.
	return nil, nil
}

// classifyAuthError converts a proxy auth/transport error to a
// field-level TestFailure when the error is one of the typed arms.
// Returns nil for other errors so the caller surfaces them as an
// error return.
//
// Pinning mirrors C# `Deluge.TestConnection` at `:282-319`:
// authentication failure → "Password", transport unavailability →
// "Host" with detailed description.
func classifyAuthError(err error) *downloadclients.TestFailure {
	switch {
	case errors.Is(err, ErrAuthFailed):
		return &downloadclients.TestFailure{
			PropertyName:        "Password",
			ErrorMessage:        "Authentication failed",
			DetailedDescription: err.Error(),
		}
	case errors.Is(err, ErrUnavailable):
		return &downloadclients.TestFailure{
			PropertyName:        "Host",
			ErrorMessage:        "Unable to connect to Deluge",
			DetailedDescription: err.Error(),
		}
	}
	return nil
}

// RequestAction is the per-impl action map. The Deluge C# tree has
// no concrete actions — returns (nil, nil) per the C# default at
// `DownloadClientBase.cs:165-167`.
func (d *Deluge) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// Compile-time guard: Deluge satisfies the Provider interface.
var _ downloadclients.Provider = (*Deluge)(nil)

// constructor is the registry factory. Mirrors the qBittorrent +
// Transmission + Sabnzbd factory pattern from 5.G1 / sub-arc 14a/b.
func constructor(def *downloadclients.DownloadClient, deps downloadclients.Dependencies) (downloadclients.Provider, error) {
	if def == nil {
		return nil, errors.New("deluge: nil DownloadClient row")
	}
	settings := NewSettings()
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("deluge: settings unmarshal: %w", err)
		}
	}
	var log *slog.Logger
	if l, ok := deps.Logger.(*slog.Logger); ok {
		log = l
	}

	// Build the proxy when an HTTPDoer is available. Tests
	// constructing through the registry without a doer (e.g. the
	// 5.G2a round-trip suite) get a nil proxy + a Provider that
	// surfaces programmer-error returns from Test(); production
	// wiring always supplies one.
	var proxy *Proxy
	if deps.HTTPClient != nil {
		doer, ok := deps.HTTPClient.(HTTPDoer)
		if !ok {
			return nil, fmt.Errorf("deluge: deps.HTTPClient (%T) does not satisfy HTTPDoer", deps.HTTPClient)
		}
		var err error
		proxy, err = NewProxy(doer, log)
		if err != nil {
			return nil, err
		}
	}

	return New(def, settings, proxy, log), nil
}

// init registers Deluge with the global registry.
func init() {
	downloadclients.Default().MustRegister(Implementation, ConfigContract, constructor)
}
