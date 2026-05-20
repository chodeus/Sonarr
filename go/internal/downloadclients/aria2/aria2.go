package aria2

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
// for the Aria2 download client. Mirrors C# class names `Aria2` +
// `Aria2Settings`.
const (
	Implementation = "Aria2"
	ConfigContract = "Aria2Settings"
)

// Aria2 is the [downloadclients.Provider] implementation for the
// aria2c download client. Ports C# `Aria2` at
// `src/NzbDrone.Core/Download/Clients/Aria2/Aria2.cs`.
//
// Slice 5.J51 scope: Name / Protocol / Definition / RequestAction
// live; Test runs the XML-RPC aria2.getVersion probe via the bound
// proxy + minVersion floor. Download / GetItems / GetStatus /
// RemoveItem / MarkItemAsImported still return
// [downloadclients.ErrNotImplementedYet]; GetImportItem passthrough.
type Aria2 struct {
	settings Settings
	proxy    *Proxy
	def      *downloadclients.DownloadClient
	log      *slog.Logger
}

// minVersion is the aria2c version floor [Test] enforces. aria2c
// 1.18 was the first release with the XML-RPC + JSON-RPC surface
// stable enough for Sonarr's add+monitor flows.
var minVersion = versionPair{major: 1, minor: 18}

// New constructs an Aria2 Provider against the resolved settings
// + an optional Proxy. nil proxy → Test surfaces Settings.Validate
// failures only.
func New(def *downloadclients.DownloadClient, settings Settings, proxy *Proxy, log *slog.Logger) *Aria2 {
	if log == nil {
		log = slog.Default()
	}
	return &Aria2{settings: settings, proxy: proxy, def: def, log: log}
}

// Name returns the user-facing label. Aria2 doesn't override Name
// in C# — the class-name default ("Aria2") surfaces.
func (a *Aria2) Name() string { return "Aria2" }

// Protocol returns the protocol family. Aria2 supports both
// torrent and direct-URL downloads; the Go-port surfaces it as
// torrent for the Provider classification (matches C#
// `TorrentClientBase.Protocol` — Aria2 inherits from
// TorrentClientBase even though the surface is multi-protocol).
func (a *Aria2) Protocol() indexers.DownloadProtocol {
	return indexers.DownloadProtocolTorrent
}

// Definition returns the persisted DownloadClient row. Nil for
// test-constructed providers.
func (a *Aria2) Definition() *downloadclients.DownloadClient { return a.def }

// Settings returns the bound Settings. Exposed for tests that need
// to assert against the round-tripped JSON without re-decoding.
func (a *Aria2) Settings() Settings { return a.settings }

// Download ports `Aria2.AddFromMagnetLink` + `AddFromTorrentFile`
// (mirrors C# `Aria2.cs` Download overrides). Picks the add path
// based on the GrabRequest shape:
//
//   - TorrentBody bytes set → aria2.addTorrent (base64 upload).
//   - MagnetURL set → aria2.addUri with the magnet URL.
//   - DownloadURL set → aria2.addUri with the http URL.
//
// aria2 returns the assigned GID directly as the methodResponse's
// string — different from rtorrent (which returns "0" success +
// requires hash from elsewhere). The Go-port treats the GID as the
// DownloadID and upper-cases it for the orchestrator's queue-item
// row.
func (a *Aria2) Download(ctx context.Context, req *downloadclients.GrabRequest) (string, error) {
	if req == nil {
		return "", errors.New("aria2: nil GrabRequest")
	}
	if a.proxy == nil {
		return "", errors.New("aria2: Download requires a wired Proxy")
	}
	var gid string
	var err error
	switch {
	case len(req.TorrentBody) > 0:
		gid, err = a.proxy.AddTorrent(ctx, req.TorrentBody, a.settings)
	case req.MagnetURL != "":
		gid, err = a.proxy.AddMagnet(ctx, req.MagnetURL, a.settings)
	case req.DownloadURL != "":
		gid, err = a.proxy.AddMagnet(ctx, req.DownloadURL, a.settings)
	default:
		return "", errors.New("aria2: Download: no TorrentBody / MagnetURL / DownloadURL on GrabRequest")
	}
	if err != nil {
		return "", err
	}
	return strings.ToUpper(gid), nil
}

// GetItems is a stub.
func (a *Aria2) GetItems(_ context.Context) ([]*downloadclients.Item, error) {
	return nil, downloadclients.ErrNotImplementedYet
}

// GetImportItem passes through unchanged.
func (a *Aria2) GetImportItem(_ context.Context, item *downloadclients.Item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

// RemoveItem is a stub.
func (a *Aria2) RemoveItem(_ context.Context, _ *downloadclients.Item, _ bool) error {
	return downloadclients.ErrNotImplementedYet
}

// GetStatus is a stub.
func (a *Aria2) GetStatus(_ context.Context) (*downloadclients.ClientInfo, error) {
	return nil, downloadclients.ErrNotImplementedYet
}

// MarkItemAsImported is a stub.
func (a *Aria2) MarkItemAsImported(_ context.Context, _ *downloadclients.Item) error {
	return downloadclients.ErrNotImplementedYet
}

// Test runs the per-client connection self-check. Validates the
// Settings first, then (when no failures and a proxy is wired)
// runs the aria2.getVersion XML-RPC probe and gates on minVersion.
// Mirrors C# `DownloadClientBase.Test()` posture; aria2-side
// authentication is via the SecretToken arg which the proxy
// prepends to every call.
func (a *Aria2) Test(ctx context.Context) ([]downloadclients.TestFailure, error) {
	failures := a.settings.Validate()
	if len(failures) > 0 || a.proxy == nil {
		return failures, nil
	}
	version, err := a.proxy.GetVersion(ctx, a.settings)
	if err != nil {
		return []downloadclients.TestFailure{{
			PropertyName: "Host",
			ErrorMessage: fmt.Sprintf("Unable to connect to Aria2: %v", err),
		}}, nil
	}
	if !versionAtLeast(version, minVersion) {
		return []downloadclients.TestFailure{{
			PropertyName: "Host",
			ErrorMessage: fmt.Sprintf("Aria2 version %s is too old; minimum supported is %d.%d", version, minVersion.major, minVersion.minor),
		}}, nil
	}
	return nil, nil
}

// RequestAction returns (nil, nil) for every action.
func (a *Aria2) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// Compile-time guard.
var _ downloadclients.Provider = (*Aria2)(nil)

// constructor is the registry factory. Builds the XML-RPC Proxy
// when deps.HTTPClient is available.
func constructor(def *downloadclients.DownloadClient, deps downloadclients.Dependencies) (downloadclients.Provider, error) {
	if def == nil {
		return nil, errors.New("aria2: nil DownloadClient row")
	}
	settings := NewSettings()
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("aria2: settings unmarshal: %w", err)
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
			return nil, fmt.Errorf("aria2: deps.HTTPClient (%T) does not satisfy HTTPDoer", deps.HTTPClient)
		}
		proxy = NewProxy(doer, log)
	}
	return New(def, settings, proxy, log), nil
}

// init registers Aria2 with the global registry.
func init() {
	downloadclients.Default().MustRegister(Implementation, ConfigContract, constructor)
}
