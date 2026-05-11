package transmission

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
)

// Implementation + ConfigContract are the persisted-row identifiers
// for the Transmission download client. Mirrors C# class names
// `Transmission` + `TransmissionSettings`. Same ConfigContract is
// reused by `Vuze` — the queue [13] Registry keys on
// (Implementation, ConfigContract) to disambiguate.
const (
	Implementation = "Transmission"
	ConfigContract = "TransmissionSettings"
)

// Transmission is the [downloadclients.Provider] implementation for
// the Transmission torrent client. Ports C# `Transmission` at
// `src/NzbDrone.Core/Download/Clients/Transmission/Transmission.cs:18-92`
// + the shared `TransmissionBase` body at `TransmissionBase.cs:22-340`.
//
// Sub-arc 14b scope: Name / Protocol / Definition / Download / Test
// are live. GetItems / GetStatus / RemoveItem / GetImportItem /
// MarkItemAsImported return [downloadclients.ErrNotImplementedYet]
// (the family-pattern stubs sub-arc 14a established). Queue [15]
// consumer demand will fill these in.
type Transmission struct {
	settings Settings
	proxy    *Proxy
	def      *downloadclients.DownloadClient
	log      *slog.Logger
}

// New constructs a Transmission Provider against the resolved
// settings + proxy. Used by both the registry [Constructor] and by
// tests that pin a stub HTTPDoer.
func New(def *downloadclients.DownloadClient, settings Settings, proxy *Proxy, log *slog.Logger) *Transmission {
	if log == nil {
		log = slog.Default()
	}
	return &Transmission{settings: settings, proxy: proxy, def: def, log: log}
}

// Name returns the user-facing label. Mirrors C# `Transmission.Name`
// at `:20`.
func (t *Transmission) Name() string { return "Transmission" }

// Protocol returns the protocol family. Mirrors C#
// `TorrentClientBase.Protocol` at `TorrentClientBase.cs:44`.
func (t *Transmission) Protocol() indexers.DownloadProtocol {
	return indexers.DownloadProtocolTorrent
}

// Definition returns the persisted DownloadClients row.
func (t *Transmission) Definition() *downloadclients.DownloadClient { return t.def }

// Download fires the Transmission `torrent-add` RPC. Mirrors the
// torrent-half of `TorrentClientBase.Download` at
// `TorrentClientBase.cs:51-160`. Magnet links route to
// AddTorrentFromURL with the magnet URL; plain torrent URLs also
// route through AddTorrentFromURL (Transmission fetches the .torrent
// itself). Pre-fetched .torrent bytes route through
// AddTorrentFromData.
//
// After the add, applies the per-grab seed configuration via
// SetTorrentSeedingConfiguration when present.
//
// Returns the torrent's hashString (lowercase hex), which becomes
// the Sonarr-side DownloadID.
func (t *Transmission) Download(ctx context.Context, req *downloadclients.GrabRequest) (string, error) {
	if req == nil {
		return "", errors.New("transmission: Download: nil request")
	}

	// Pick the URL the C# code prefers: magnet first when present
	// (matching the C# RemoteEpisode.Release shape at
	// `TorrentClientBase.cs:58-69`); else the configured download
	// URL.
	addURL := req.MagnetURL
	if addURL == "" {
		addURL = req.DownloadURL
	}

	directory := strings.TrimSpace(t.settings.TvDirectory)

	var (
		hash string
		err  error
	)
	switch {
	case len(req.TorrentBody) > 0:
		hash, err = t.proxy.AddTorrentFromData(ctx, req.TorrentBody, directory, t.settings)
	case addURL != "":
		hash, err = t.proxy.AddTorrentFromURL(ctx, addURL, directory, t.settings)
	default:
		return "", errors.New("transmission: Download: GrabRequest has neither MagnetURL/DownloadURL nor TorrentBody")
	}

	if err != nil {
		return "", fmt.Errorf("transmission: Download(%q): %w", req.ReleaseTitle, err)
	}

	if req.SeedConfiguration != nil {
		if seedErr := t.proxy.SetTorrentSeedingConfiguration(ctx, hash, req.SeedConfiguration, t.settings); seedErr != nil {
			// Match C# behaviour at `TorrentClientBase.cs:155-159` —
			// log + continue. The torrent was added; the seed
			// override is best-effort.
			t.log.Warn("transmission: SetTorrentSeedingConfiguration failed",
				"hash", hash, "err", seedErr)
		}
	}

	return strings.ToLower(hash), nil
}

// GetItems / GetStatus / RemoveItem / GetImportItem /
// MarkItemAsImported — same sub-arc 14a stub strategy as SABnzbd.
// Queue [15] consumer demand drives the actual implementations.

// GetItems is deferred. Returns ErrNotImplementedYet.
func (t *Transmission) GetItems(_ context.Context) ([]*downloadclients.Item, error) {
	return nil, downloadclients.ErrNotImplementedYet
}

// GetImportItem mirrors the C# base default — pass input through.
// `DownloadClientBase.cs:102-105`.
func (t *Transmission) GetImportItem(_ context.Context, item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

// RemoveItem is deferred. Returns ErrNotImplementedYet.
func (t *Transmission) RemoveItem(_ context.Context, _ *downloadclients.Item, _ bool) error {
	return downloadclients.ErrNotImplementedYet
}

// GetStatus is deferred. Returns ErrNotImplementedYet.
func (t *Transmission) GetStatus(_ context.Context) (*downloadclients.ClientInfo, error) {
	return nil, downloadclients.ErrNotImplementedYet
}

// MarkItemAsImported is deferred. The C# implementation at
// `Transmission.cs:36-73` requires the labels-support probe
// (HasClientVersion(4, 0)). Sub-arc 14b consumer demand will revive.
func (t *Transmission) MarkItemAsImported(_ context.Context, _ *downloadclients.Item) error {
	return downloadclients.ErrNotImplementedYet
}

// Test ports `Transmission.ValidateVersion` at `:75-90` plus the
// general "can we reach the RPC endpoint" probe. The C# Test has
// additional checks (download-dir writable / category-folder
// existence) under the base TransmissionBase — those are sub-arc
// 14b consumer-demand follow-ups.
func (t *Transmission) Test(ctx context.Context) ([]downloadclients.TestFailure, error) {
	var failures []downloadclients.TestFailure
	if vFails := t.settings.Validate(); len(vFails) > 0 {
		failures = append(failures, vFails...)
	}

	rawVersion, err := t.proxy.GetClientVersion(ctx, t.settings)
	if err != nil {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName:        "Host",
			ErrorMessage:        fmt.Sprintf("Unable to connect to Transmission: %v", err),
			DetailedDescription: err.Error(),
		})
		return failures, nil
	}

	version := parseVersion(rawVersion)
	if version == nil {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "Version",
			ErrorMessage: fmt.Sprintf("Unknown Transmission version: %s", rawVersion),
		})
		return failures, nil
	}

	// Version-floor at C# `:84-87`: Transmission >= 2.40 required.
	if version.major < 2 || (version.major == 2 && version.minor < 40) {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "Version",
			ErrorMessage: fmt.Sprintf("Transmission %s is too old; required version is 2.40 or newer", rawVersion),
		})
	}

	return failures, nil
}

// versionPair holds a parsed Transmission version. Mirrors C# use of
// `Version` in `Transmission.ValidateVersion` at `:81-87`.
type versionPair struct {
	major, minor int
}

// versionPattern extracts the leading "major.minor" pair from a
// Transmission version string like "3.00 (bb6b5a062e)" or
// "2.94+ (bb6b5a062e)". Mirrors C# regex `(?<!\(|(\d|\.)+)(\d|\.)+(?!\)|(\d|\.)+)`
// at `:81`. The Go port uses a simpler "first digit-dot-digit" form
// which handles every real-world Transmission output the C# regex
// targets.
var versionPattern = regexp.MustCompile(`^(\d+)\.(\d+)`)

func parseVersion(version string) *versionPair {
	m := versionPattern.FindStringSubmatch(strings.TrimSpace(version))
	if len(m) < 3 {
		return nil
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return &versionPair{major: major, minor: minor}
}

// Compile-time guard: Transmission satisfies the Provider interface.
var _ downloadclients.Provider = (*Transmission)(nil)

// constructor is the registry factory. Mirrors the SAB factory
// pattern from sub-arc 14a.
func constructor(def *downloadclients.DownloadClient, deps downloadclients.Dependencies) (downloadclients.Provider, error) {
	if def == nil {
		return nil, errors.New("transmission: nil DownloadClient row")
	}
	settings := NewSettings()
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("transmission: settings unmarshal: %w", err)
		}
	}
	doer, ok := deps.HTTPClient.(HTTPDoer)
	if !ok || doer == nil {
		return nil, errors.New("transmission: deps.HTTPClient does not satisfy HTTPDoer")
	}
	var log *slog.Logger
	if l, ok := deps.Logger.(*slog.Logger); ok {
		log = l
	}
	proxy, err := NewProxy(doer, log)
	if err != nil {
		return nil, err
	}
	return New(def, settings, proxy, log), nil
}

// init registers Transmission with the global registry.
func init() {
	downloadclients.Default().MustRegister(Implementation, ConfigContract, constructor)
}
