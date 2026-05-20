package utorrent

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
// for the uTorrent download client. Mirrors C# class names
// `UTorrent` + `UTorrentSettings` (see
// `src/NzbDrone.Core/Download/Clients/uTorrent/UTorrent.cs`
// and `UTorrentSettings.cs:19`). The (Implementation, ConfigContract)
// pair is the registry key — see [downloadclients.Registry] doc.
const (
	Implementation = "UTorrent"
	ConfigContract = "UTorrentSettings"
)

// UTorrent is the [downloadclients.Provider] implementation for the
// uTorrent torrent client. Ports C# `UTorrent` at
// `src/NzbDrone.Core/Download/Clients/uTorrent/UTorrent.cs`.
//
// Slice 5.J47 scope: Name / Protocol / Definition / RequestAction
// are live; Test runs the token+cookie auth flow + the `getsettings`
// version probe via the bound proxy + minBuild floor. Download /
// GetItems / GetStatus / RemoveItem / GetImportItem /
// MarkItemAsImported still return [downloadclients.ErrNotImplementedYet].
type UTorrent struct {
	settings Settings
	proxy    *Proxy
	def      *downloadclients.DownloadClient
	log      *slog.Logger
}

// minBuild is the uTorrent build-number floor [Test] enforces.
// uTorrent build 25406 (3.0+) was the first release shipping the
// /gui/ WebUI surface Sonarr's code targets. Older builds may work
// in practice but aren't supported.
const minBuild = 25406

// New constructs a uTorrent Provider against the resolved settings
// + an optional Proxy. Used by both the registry [Constructor] and
// by tests. nil proxy → Test surfaces Settings.Validate failures
// only (matches the pre-5.J47 stub).
func New(def *downloadclients.DownloadClient, settings Settings, proxy *Proxy, log *slog.Logger) *UTorrent {
	if log == nil {
		log = slog.Default()
	}
	return &UTorrent{settings: settings, proxy: proxy, def: def, log: log}
}

// Name returns the user-facing label. Mirrors C# `UTorrent.Name`
// — the class name itself ("UTorrent" → "uTorrent" lower-case-u
// short form).
func (u *UTorrent) Name() string { return "uTorrent" }

// Protocol returns the protocol family. Mirrors C#
// `TorrentClientBase.Protocol`.
func (u *UTorrent) Protocol() indexers.DownloadProtocol {
	return indexers.DownloadProtocolTorrent
}

// Definition returns the persisted DownloadClient row. Nil for
// test-constructed providers.
func (u *UTorrent) Definition() *downloadclients.DownloadClient { return u.def }

// Settings returns the bound Settings. Exposed for tests that need
// to assert against the round-tripped JSON without re-decoding.
func (u *UTorrent) Settings() Settings { return u.settings }

// Download is a stub. See the package doc-comment for the deferred-
// impl rationale.
func (u *UTorrent) Download(_ context.Context, _ *downloadclients.GrabRequest) (string, error) {
	return "", downloadclients.ErrNotImplementedYet
}

// GetItems is a stub.
func (u *UTorrent) GetItems(_ context.Context) ([]*downloadclients.Item, error) {
	return nil, downloadclients.ErrNotImplementedYet
}

// GetImportItem passes through unchanged. Mirrors C#
// `DownloadClientBase.GetImportItem` default behaviour.
func (u *UTorrent) GetImportItem(_ context.Context, item *downloadclients.Item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

// RemoveItem is a stub.
func (u *UTorrent) RemoveItem(_ context.Context, _ *downloadclients.Item, _ bool) error {
	return downloadclients.ErrNotImplementedYet
}

// GetStatus is a stub.
func (u *UTorrent) GetStatus(_ context.Context) (*downloadclients.ClientInfo, error) {
	return nil, downloadclients.ErrNotImplementedYet
}

// MarkItemAsImported is a stub. The real impl will fire
// `action=setprops&hash=<hash>&s=label&v=<TvImportedCategory>` via
// the proxy.
func (u *UTorrent) MarkItemAsImported(_ context.Context, _ *downloadclients.Item) error {
	return downloadclients.ErrNotImplementedYet
}

// Test runs the per-client connection self-check. Validates the
// Settings first, then (when no failures and a proxy is wired)
// runs the token+cookie auth flow + the `getsettings` version
// probe and gates on [minBuild]. Mirrors C#
// `DownloadClientBase.Test()` posture.
func (u *UTorrent) Test(ctx context.Context) ([]downloadclients.TestFailure, error) {
	failures := u.settings.Validate()
	if len(failures) > 0 || u.proxy == nil {
		return failures, nil
	}
	build, err := u.proxy.GetVersion(ctx, u.settings)
	if err != nil {
		return []downloadclients.TestFailure{{
			PropertyName: "Host",
			ErrorMessage: fmt.Sprintf("Unable to connect to uTorrent: %v", err),
		}}, nil
	}
	if build < minBuild {
		return []downloadclients.TestFailure{{
			PropertyName: "Host",
			ErrorMessage: fmt.Sprintf("uTorrent build %d is too old; minimum supported is %d", build, minBuild),
		}}, nil
	}
	return nil, nil
}

// RequestAction returns (nil, nil) for every action — the default
// C# `DownloadClientBase.RequestAction` shape. uTorrent has no
// settings-class actions exposed today.
func (u *UTorrent) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// Compile-time guard: UTorrent satisfies the Provider interface.
var _ downloadclients.Provider = (*UTorrent)(nil)

// constructor is the registry factory. Mirrors the qBittorrent +
// Transmission + rTorrent factory pattern. When an HTTPDoer is
// present in deps.HTTPClient the constructor builds the
// token-cookie Proxy.
func constructor(def *downloadclients.DownloadClient, deps downloadclients.Dependencies) (downloadclients.Provider, error) {
	if def == nil {
		return nil, errors.New("utorrent: nil DownloadClient row")
	}
	settings := NewSettings()
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("utorrent: settings unmarshal: %w", err)
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
			return nil, fmt.Errorf("utorrent: deps.HTTPClient (%T) does not satisfy HTTPDoer", deps.HTTPClient)
		}
		proxy = NewProxy(doer, log)
	}
	return New(def, settings, proxy, log), nil
}

// init registers uTorrent with the global registry.
func init() {
	downloadclients.Default().MustRegister(Implementation, ConfigContract, constructor)
}
