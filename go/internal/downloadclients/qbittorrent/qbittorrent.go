package qbittorrent

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
// for the qBittorrent download client. Mirrors C# class names
// `QBittorrent` + `QBittorrentSettings` (see
// `src/NzbDrone.Core/Download/Clients/QBittorrent/QBittorrent.cs:23`
// and `QBittorrentSettings.cs:31`). The (Implementation,
// ConfigContract) pair is the registry key — see
// [downloadclients.Registry] doc.
const (
	Implementation = "QBittorrent"
	ConfigContract = "QBittorrentSettings"
)

// QBittorrent is the [downloadclients.Provider] implementation for
// the qBittorrent torrent client. Ports C# `QBittorrent` at
// `src/NzbDrone.Core/Download/Clients/QBittorrent/QBittorrent.cs:23-450`
// + the V1/V2 proxy bodies at `QBittorrentProxyV1.cs` /
// `QBittorrentProxyV2.cs`.
//
// Slice 5.G1a scope: Name / Protocol / Definition / RequestAction
// live; Download / Test / GetItems / GetStatus / RemoveItem /
// GetImportItem / MarkItemAsImported return
// [downloadclients.ErrNotImplementedYet]. Subsequent slices fill in
// each method.
type QBittorrent struct {
	settings Settings
	def      *downloadclients.DownloadClient
	log      *slog.Logger
}

// New constructs a QBittorrent Provider against the resolved
// settings. Used by both the registry [Constructor] and by tests
// that pin a stub HTTP doer at later slices.
func New(def *downloadclients.DownloadClient, settings Settings, log *slog.Logger) *QBittorrent {
	if log == nil {
		log = slog.Default()
	}
	return &QBittorrent{settings: settings, def: def, log: log}
}

// Name returns the user-facing label. Mirrors C# `QBittorrent.Name`
// (inherited default — the class name itself; see
// `DownloadClientBase.cs:62`).
func (q *QBittorrent) Name() string { return "qBittorrent" }

// Protocol returns the protocol family. Mirrors C#
// `TorrentClientBase.Protocol` at `TorrentClientBase.cs:44`.
func (q *QBittorrent) Protocol() indexers.DownloadProtocol {
	return indexers.DownloadProtocolTorrent
}

// Definition returns the persisted DownloadClients row.
func (q *QBittorrent) Definition() *downloadclients.DownloadClient { return q.def }

// Settings returns the resolved Settings struct. Exposed for the
// 5.G1b+ slices and tests; concrete proxy code in this package
// reads these fields directly via the package-internal accessor at
// slice land time.
func (q *QBittorrent) Settings() Settings { return q.settings }

// Download is filled in by slice 5.G1e. Returns
// [downloadclients.ErrNotImplementedYet] until then.
func (q *QBittorrent) Download(_ context.Context, _ *downloadclients.GrabRequest) (string, error) {
	return "", downloadclients.ErrNotImplementedYet
}

// GetItems is filled in by slice 5.G1d. Returns
// [downloadclients.ErrNotImplementedYet] until then.
func (q *QBittorrent) GetItems(_ context.Context) ([]*downloadclients.Item, error) {
	return nil, downloadclients.ErrNotImplementedYet
}

// GetImportItem mirrors the C# base default — pass input through.
// `DownloadClientBase.cs:102-105`.
func (q *QBittorrent) GetImportItem(_ context.Context, item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

// RemoveItem is filled in by slice 5.G1f. Returns
// [downloadclients.ErrNotImplementedYet] until then.
func (q *QBittorrent) RemoveItem(_ context.Context, _ *downloadclients.Item, _ bool) error {
	return downloadclients.ErrNotImplementedYet
}

// GetStatus is filled in by slice 5.G1g. Returns
// [downloadclients.ErrNotImplementedYet] until then.
func (q *QBittorrent) GetStatus(_ context.Context) (*downloadclients.ClientInfo, error) {
	return nil, downloadclients.ErrNotImplementedYet
}

// MarkItemAsImported is filled in by slice 5.G1h. Returns
// [downloadclients.ErrNotImplementedYet] until then.
func (q *QBittorrent) MarkItemAsImported(_ context.Context, _ *downloadclients.Item) error {
	return downloadclients.ErrNotImplementedYet
}

// Test is filled in by slice 5.G1c (login + version-compat probe).
// Until then surface the Settings.Validate output so the registry
// can at least round-trip the row and the v3 API surface the field
// failures.
func (q *QBittorrent) Test(_ context.Context) ([]downloadclients.TestFailure, error) {
	failures := q.settings.Validate()
	failures = append(failures, downloadclients.TestFailure{
		PropertyName: "",
		ErrorMessage: "qBittorrent Test is not yet implemented; tracked under slice 5.G1c",
	})
	return failures, nil
}

// RequestAction is the per-impl action map. Filled in by slice
// 5.G1h. Until then mirrors `DownloadClientBase.cs:165-167` and
// returns (nil, nil).
func (q *QBittorrent) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// Compile-time guard: QBittorrent satisfies the Provider interface.
var _ downloadclients.Provider = (*QBittorrent)(nil)

// constructor is the registry factory. Mirrors the Transmission +
// SAB factory pattern from sub-arc 14a/b.
func constructor(def *downloadclients.DownloadClient, deps downloadclients.Dependencies) (downloadclients.Provider, error) {
	if def == nil {
		return nil, errors.New("qbittorrent: nil DownloadClient row")
	}
	settings := NewSettings()
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("qbittorrent: settings unmarshal: %w", err)
		}
	}
	var log *slog.Logger
	if l, ok := deps.Logger.(*slog.Logger); ok {
		log = l
	}
	return New(def, settings, log), nil
}

// init registers qBittorrent with the global registry.
func init() {
	downloadclients.Default().MustRegister(Implementation, ConfigContract, constructor)
}
