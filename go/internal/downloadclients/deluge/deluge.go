package deluge

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
// Slice 5.G2a scope: Name / Protocol / Definition / RequestAction /
// GetImportItem live; Download / Test / GetItems / RemoveItem /
// GetStatus / MarkItemAsImported return
// [downloadclients.ErrNotImplementedYet]. Subsequent slices fill in
// each method against a JSON-RPC proxy that lands at 5.G2b.
type Deluge struct {
	settings Settings
	def      *downloadclients.DownloadClient
	log      *slog.Logger
}

// New constructs a Deluge Provider against the resolved settings.
// Used by both the registry [Constructor] and by tests that pin a
// stub HTTPDoer at later slices.
func New(def *downloadclients.DownloadClient, settings Settings, log *slog.Logger) *Deluge {
	if log == nil {
		log = slog.Default()
	}
	return &Deluge{settings: settings, def: def, log: log}
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
// 5.G2b+ slices and tests.
func (d *Deluge) Settings() Settings { return d.settings }

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

// Test is filled in by slice 5.G2c (auth.login + web.update_ui).
// Until then surface the Settings.Validate output so the registry
// can at least round-trip the row and the v3 API surface the field
// failures.
func (d *Deluge) Test(_ context.Context) ([]downloadclients.TestFailure, error) {
	failures := d.settings.Validate()
	failures = append(failures, downloadclients.TestFailure{
		PropertyName: "",
		ErrorMessage: "Deluge Test is not yet implemented; tracked under slice 5.G2c",
	})
	return failures, nil
}

// RequestAction is the per-impl action map. The Deluge tree has no
// concrete actions in the C# source — returns (nil, nil) per the
// C# default at `DownloadClientBase.cs:165-167`.
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
	return New(def, settings, log), nil
}

// init registers Deluge with the global registry.
func init() {
	downloadclients.Default().MustRegister(Implementation, ConfigContract, constructor)
}
