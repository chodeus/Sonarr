package aria2

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
// Slice 5.J50 scope: Name / Protocol / Definition / RequestAction
// live; Test surfaces Settings.Validate failures; all other Provider
// methods return [downloadclients.ErrNotImplementedYet] except
// GetImportItem (passthrough). Subsequent slices wire the XML-RPC
// proxy + per-method calls.
type Aria2 struct {
	settings Settings
	def      *downloadclients.DownloadClient
	log      *slog.Logger
}

// New constructs an Aria2 Provider against the resolved settings.
func New(def *downloadclients.DownloadClient, settings Settings, log *slog.Logger) *Aria2 {
	if log == nil {
		log = slog.Default()
	}
	return &Aria2{settings: settings, def: def, log: log}
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

// Download is a stub.
func (a *Aria2) Download(_ context.Context, _ *downloadclients.GrabRequest) (string, error) {
	return "", downloadclients.ErrNotImplementedYet
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

// Test runs the per-client connection self-check. Slice 5.J50 scope
// — returns Settings.Validate failures only; the real network probe
// (aria2.getVersion via the XML-RPC proxy) lands in the follow-up
// proxy slice.
func (a *Aria2) Test(_ context.Context) ([]downloadclients.TestFailure, error) {
	return a.settings.Validate(), nil
}

// RequestAction returns (nil, nil) for every action.
func (a *Aria2) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// Compile-time guard.
var _ downloadclients.Provider = (*Aria2)(nil)

// constructor is the registry factory.
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
	return New(def, settings, log), nil
}

// init registers Aria2 with the global registry.
func init() {
	downloadclients.Default().MustRegister(Implementation, ConfigContract, constructor)
}
