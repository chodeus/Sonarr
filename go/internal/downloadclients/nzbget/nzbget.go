package nzbget

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
// for the NZBGet download client. Mirrors C# class names `Nzbget` +
// `NzbgetSettings` (see
// `src/NzbDrone.Core/Download/Clients/Nzbget/Nzbget.cs` and
// `NzbgetSettings.cs:22`). The (Implementation, ConfigContract) pair
// is the registry key — see [downloadclients.Registry] doc.
const (
	Implementation = "Nzbget"
	ConfigContract = "NzbgetSettings"
)

// Nzbget is the [downloadclients.Provider] implementation for the
// NZBGet usenet client. Ports C# `Nzbget` at
// `src/NzbDrone.Core/Download/Clients/Nzbget/Nzbget.cs` + the
// `NzbgetProxy.cs` JSON-RPC body.
//
// Slice 5.G3a scope: Name / Protocol / Definition / RequestAction /
// GetImportItem live; Download / Test / GetItems / RemoveItem /
// GetStatus / MarkItemAsImported return
// [downloadclients.ErrNotImplementedYet]. Subsequent slices fill in
// each method against a JSON-RPC + HTTP-Basic-auth proxy at 5.G3b.
type Nzbget struct {
	settings Settings
	def      *downloadclients.DownloadClient
	log      *slog.Logger
}

// New constructs an Nzbget Provider against the resolved settings.
// Used by both the registry [Constructor] and by tests.
func New(def *downloadclients.DownloadClient, settings Settings, log *slog.Logger) *Nzbget {
	if log == nil {
		log = slog.Default()
	}
	return &Nzbget{settings: settings, def: def, log: log}
}

// Name returns the user-facing label. Mirrors C# `Nzbget.Name` —
// the class name is "Nzbget" but the display label is "NZBGet"
// (capital-N + Z + G + lowercase et).
func (n *Nzbget) Name() string { return "NZBGet" }

// Protocol returns the protocol family. Mirrors C#
// `UsenetClientBase.Protocol` (NZBGet is usenet, not torrent).
func (n *Nzbget) Protocol() indexers.DownloadProtocol {
	return indexers.DownloadProtocolUsenet
}

// Definition returns the persisted DownloadClients row.
func (n *Nzbget) Definition() *downloadclients.DownloadClient { return n.def }

// Settings returns the resolved Settings struct. Exposed for the
// 5.G3b+ slices and tests.
func (n *Nzbget) Settings() Settings { return n.settings }

// Download is filled in by slice 5.G3e. Returns
// [downloadclients.ErrNotImplementedYet] until then.
func (n *Nzbget) Download(_ context.Context, _ *downloadclients.GrabRequest) (string, error) {
	return "", downloadclients.ErrNotImplementedYet
}

// GetItems is filled in by slice 5.G3d. Returns
// [downloadclients.ErrNotImplementedYet] until then.
func (n *Nzbget) GetItems(_ context.Context) ([]*downloadclients.Item, error) {
	return nil, downloadclients.ErrNotImplementedYet
}

// GetImportItem mirrors the C# base default — pass input through.
// `DownloadClientBase.cs:102-105`.
func (n *Nzbget) GetImportItem(_ context.Context, item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

// RemoveItem is filled in by slice 5.G3f. Returns
// [downloadclients.ErrNotImplementedYet] until then.
func (n *Nzbget) RemoveItem(_ context.Context, _ *downloadclients.Item, _ bool) error {
	return downloadclients.ErrNotImplementedYet
}

// GetStatus is filled in by slice 5.G3g. Returns
// [downloadclients.ErrNotImplementedYet] until then.
func (n *Nzbget) GetStatus(_ context.Context) (*downloadclients.ClientInfo, error) {
	return nil, downloadclients.ErrNotImplementedYet
}

// MarkItemAsImported is filled in by slice 5.G3h. Returns
// [downloadclients.ErrNotImplementedYet] until then.
func (n *Nzbget) MarkItemAsImported(_ context.Context, _ *downloadclients.Item) error {
	return downloadclients.ErrNotImplementedYet
}

// Test is filled in by slice 5.G3c (version + connectivity probe).
// Until then surface the Settings.Validate output so the registry
// can at least round-trip the row and the v3 API surface the field
// failures.
func (n *Nzbget) Test(_ context.Context) ([]downloadclients.TestFailure, error) {
	failures := n.settings.Validate()
	failures = append(failures, downloadclients.TestFailure{
		PropertyName: "",
		ErrorMessage: "NZBGet Test is not yet implemented; tracked under slice 5.G3c",
	})
	return failures, nil
}

// RequestAction is the per-impl action map. The NZBGet C# tree has
// no concrete actions — returns (nil, nil) per the C# default at
// `DownloadClientBase.cs:165-167`.
func (n *Nzbget) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// Compile-time guard: Nzbget satisfies the Provider interface.
var _ downloadclients.Provider = (*Nzbget)(nil)

// constructor is the registry factory. Mirrors the qBittorrent +
// Deluge + Transmission + Sabnzbd factory pattern.
func constructor(def *downloadclients.DownloadClient, deps downloadclients.Dependencies) (downloadclients.Provider, error) {
	if def == nil {
		return nil, errors.New("nzbget: nil DownloadClient row")
	}
	settings := NewSettings()
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("nzbget: settings unmarshal: %w", err)
		}
	}
	var log *slog.Logger
	if l, ok := deps.Logger.(*slog.Logger); ok {
		log = l
	}
	return New(def, settings, log), nil
}

// init registers Nzbget with the global registry.
func init() {
	downloadclients.Default().MustRegister(Implementation, ConfigContract, constructor)
}
