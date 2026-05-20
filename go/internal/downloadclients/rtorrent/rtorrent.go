package rtorrent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
)

// Implementation + ConfigContract are the persisted-row identifiers
// for the rTorrent download client. Mirrors C# class names
// `RTorrent` + `RTorrentSettings` (see
// `src/NzbDrone.Core/Download/Clients/RTorrent/RTorrent.cs:14`
// and `RTorrentSettings.cs:19`). The (Implementation, ConfigContract)
// pair is the registry key — see [downloadclients.Registry] doc.
const (
	Implementation = "RTorrent"
	ConfigContract = "RTorrentSettings"
)

// RTorrent is the [downloadclients.Provider] implementation for the
// rTorrent torrent client. Ports C# `RTorrent` at
// `src/NzbDrone.Core/Download/Clients/RTorrent/RTorrent.cs:14-322`.
//
// Slice 5.J42 scope: Name / Protocol / Definition / RequestAction
// are live; Test runs the system.client_version XML-RPC probe via
// the bound proxy + minVersion floor. Download / GetItems /
// GetStatus / RemoveItem / GetImportItem / MarkItemAsImported still
// return [downloadclients.ErrNotImplementedYet]. Subsequent slices
// fill in each method.
type RTorrent struct {
	settings Settings
	proxy    *Proxy
	def      *downloadclients.DownloadClient
	log      *slog.Logger
}

// minVersion is the rTorrent product-version floor [Test] enforces.
// rTorrent 0.9.0 was the first release shipping the XML-RPC surface
// Sonarr's d.multicall.2 / load.start_magnet calls target. Older
// builds may work in practice but aren't supported.
var minVersion = versionPair{major: 0, minor: 9}

// New constructs an rTorrent Provider against the resolved settings
// + an optional Proxy. Used by both the registry [Constructor] and
// by tests. nil proxy → Test surfaces Settings.Validate failures
// only (matches the pre-5.J42 stub).
func New(def *downloadclients.DownloadClient, settings Settings, proxy *Proxy, log *slog.Logger) *RTorrent {
	if log == nil {
		log = slog.Default()
	}
	return &RTorrent{settings: settings, proxy: proxy, def: def, log: log}
}

// Name returns the user-facing label. Mirrors the C# `RTorrent.Name`
// override at `RTorrent.cs:25-28` — returns "rTorrent" (lower-case r).
func (r *RTorrent) Name() string { return "rTorrent" }

// Protocol returns the protocol family. Mirrors C#
// `TorrentClientBase.Protocol` at `TorrentClientBase.cs:44`.
func (r *RTorrent) Protocol() indexers.DownloadProtocol {
	return indexers.DownloadProtocolTorrent
}

// Definition returns the persisted DownloadClient row. Nil for
// test-constructed providers.
func (r *RTorrent) Definition() *downloadclients.DownloadClient { return r.def }

// Settings returns the bound Settings. Exposed for tests that need
// to assert against the round-tripped JSON without re-decoding.
func (r *RTorrent) Settings() Settings { return r.settings }

// Download is a stub. Returns [downloadclients.ErrNotImplementedYet]
// pending the XML-RPC proxy port (slice 5.J41-follow-up).
func (r *RTorrent) Download(_ context.Context, _ *downloadclients.GrabRequest) (string, error) {
	return "", downloadclients.ErrNotImplementedYet
}

// GetItems is a stub. See [Download] for the deferred-impl
// rationale.
func (r *RTorrent) GetItems(_ context.Context) ([]*downloadclients.Item, error) {
	return nil, downloadclients.ErrNotImplementedYet
}

// GetImportItem passes through unchanged. Mirrors C#
// `DownloadClientBase.GetImportItem` default behaviour
// (`DownloadClientBase.cs:102-105`).
func (r *RTorrent) GetImportItem(_ context.Context, item *downloadclients.Item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

// RemoveItem is a stub.
func (r *RTorrent) RemoveItem(_ context.Context, _ *downloadclients.Item, _ bool) error {
	return downloadclients.ErrNotImplementedYet
}

// GetStatus is a stub.
func (r *RTorrent) GetStatus(_ context.Context) (*downloadclients.ClientInfo, error) {
	return nil, downloadclients.ErrNotImplementedYet
}

// MarkItemAsImported applies the optional TvImportedCategory.
// Stub — depends on the XML-RPC proxy for the
// `d.custom1.set` (label change) call.
func (r *RTorrent) MarkItemAsImported(_ context.Context, _ *downloadclients.Item) error {
	return downloadclients.ErrNotImplementedYet
}

// Test runs the per-client connection self-check. Mirrors C#
// `DownloadClientBase.Test()` at `:148-163`: validates the Settings
// first, then (when no failures and a proxy is wired) probes
// `system.client_version` and gates on [minVersion].
func (r *RTorrent) Test(ctx context.Context) ([]downloadclients.TestFailure, error) {
	failures := r.settings.Validate()
	if len(failures) > 0 || r.proxy == nil {
		return failures, nil
	}
	version, err := r.proxy.GetVersion(ctx, r.settings)
	if err != nil {
		// Network / auth / fault failures surface as a Host-attached
		// connection failure (the C# tree's
		// `_logger.Error(ex, ...) + return new ValidationFailure("Host", ...)`
		// shape at `RTorrent.cs:280-300`).
		return []downloadclients.TestFailure{{
			PropertyName: "Host",
			ErrorMessage: fmt.Sprintf("Unable to connect to rTorrent: %v", err),
		}}, nil
	}
	if !versionAtLeast(version, minVersion) {
		return []downloadclients.TestFailure{{
			PropertyName: "Host",
			ErrorMessage: fmt.Sprintf("rTorrent version %s is too old; minimum supported is %d.%d", version, minVersion.major, minVersion.minor),
		}}, nil
	}
	return nil, nil
}

// RequestAction returns (nil, nil) for every action — the default
// C# `DownloadClientBase.RequestAction` shape at `:165-167`.
// rTorrent has no Settings-class actions exposed today (no
// "getCategories"-equivalent — rTorrent labels are free-form text
// the user types).
func (r *RTorrent) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// Compile-time guard: RTorrent satisfies the Provider interface.
var _ downloadclients.Provider = (*RTorrent)(nil)

// constructor is the registry factory. Mirrors the qBittorrent +
// Transmission factory pattern. When an HTTPDoer is present in
// `deps.HTTPClient` the constructor builds the XML-RPC Proxy; when
// not (e.g. round-trip tests without a wired client), Test surfaces
// only Settings.Validate failures.
func constructor(def *downloadclients.DownloadClient, deps downloadclients.Dependencies) (downloadclients.Provider, error) {
	if def == nil {
		return nil, errors.New("rtorrent: nil DownloadClient row")
	}
	settings := NewSettings()
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("rtorrent: settings unmarshal: %w", err)
		}
	}
	var log *slog.Logger
	if l, ok := deps.Logger.(*slog.Logger); ok {
		log = l
	}
	var proxy *Proxy
	if deps.HTTPClient != nil {
		doer, ok := deps.HTTPClient.(HTTPDoer)
		if !ok {
			return nil, fmt.Errorf("rtorrent: deps.HTTPClient (%T) does not satisfy HTTPDoer", deps.HTTPClient)
		}
		proxy = NewProxy(doer, log)
	}
	return New(def, settings, proxy, log), nil
}

// init registers rTorrent with the global registry.
func init() {
	downloadclients.Default().MustRegister(Implementation, ConfigContract, constructor)
}
