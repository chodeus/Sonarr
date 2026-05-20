package rqbit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/ospath"
)

// RQBit is the [downloadclients.Provider] implementation for the
// C# `RQBit` class at
// `src/NzbDrone.Core/Download/Clients/RQBit/RQBit.cs:18-211`.
//
// Functionally complete at 5.J80 — Test (version probe with the
// 8.0.0 floor), Download (URL + file dispatch via [Proxy.AddTorrentFromURL]
// / [Proxy.AddTorrentFromFile]), GetItems (3-arm status table +
// RemotePathMapper-translated OutputPath), RemoveItem (delete /
// forget endpoints), GetStatus (IsLocalhost).
//
// Documented divergence: the seed-config cleanup gate at C#
// `:98-122` is approximated — see [RQBit.GetItems].
type RQBit struct {
	settings Settings
	def      *downloadclients.DownloadClient
	log      *slog.Logger
	proxy    *Proxy
	rpm      downloadclients.RemotePathMapper
	seedCfg  downloadclients.SeedConfigProvider
	now      func() time.Time
}

// New constructs an RQBit Provider. proxy=nil binds a default
// [Proxy]; log=nil binds [slog.Default]. The seed-config binding
// is plumbed via [SetSeedConfigProvider] post-construction (matches
// the existing RemotePathMapper pattern at [SetRemotePathMapper]).
func New(def *downloadclients.DownloadClient, settings Settings, proxy *Proxy, log *slog.Logger) *RQBit {
	if log == nil {
		log = slog.Default()
	}
	if proxy == nil {
		proxy = NewProxy(nil, log)
	}
	return &RQBit{settings: settings, def: def, log: log, proxy: proxy, now: time.Now}
}

// Name returns "RQBit" matching C# `:23`.
func (r *RQBit) Name() string { return "RQBit" }

// Protocol returns Torrent.
func (r *RQBit) Protocol() indexers.DownloadProtocol {
	return indexers.DownloadProtocolTorrent
}

// Definition returns the persisted DownloadClient row.
func (r *RQBit) Definition() *downloadclients.DownloadClient { return r.def }

// Settings returns the bound Settings.
func (r *RQBit) Settings() Settings { return r.settings }

// Download ports C# `RQBit.cs:201-209`. Dispatches on the
// GrabRequest body:
//
//   - TorrentBody non-empty → [Proxy.AddTorrentFromFile] (POST
//     raw bytes).
//   - MagnetURL non-empty → [Proxy.AddTorrentFromURL] (POST as
//     plain-text body).
//
// Returns the InfoHash from the RQBit response.
func (r *RQBit) Download(ctx context.Context, req *downloadclients.GrabRequest) (string, error) {
	if req == nil {
		return "", errors.New("rqbit: nil GrabRequest")
	}
	if r.proxy == nil {
		return "", errors.New("rqbit: nil Proxy")
	}
	switch {
	case len(req.TorrentBody) > 0:
		hash, err := r.proxy.AddTorrentFromFile(ctx, r.settings, req.TorrentBody)
		if err != nil {
			return "", fmt.Errorf("rqbit: AddTorrentFromFile: %w", err)
		}
		return hash, nil
	case strings.TrimSpace(req.MagnetURL) != "":
		hash, err := r.proxy.AddTorrentFromURL(ctx, r.settings, req.MagnetURL)
		if err != nil {
			return "", fmt.Errorf("rqbit: AddTorrentFromURL: %w", err)
		}
		return hash, nil
	default:
		return "", errors.New("rqbit: GrabRequest has neither TorrentBody nor MagnetURL")
	}
}

// GetItems ports C# `RQBit.GetItems` at `:41-128`. Skips torrents
// with empty / dot-prefixed paths (the C# warn-and-continue
// branches at `:51-61`), applies the 3-arm status table at
// `:85-97`, computes RemainingTime via DownRate.
//
// Cleanup gate (C# `:101-122`) wired via [downloadclients.SeedConfigProvider]
// at slice 5.J94 — per-torrent seed-config lookup gates
// CanMoveFiles+CanBeRemoved on (RemoveCompletedDownloads AND
// IsFinished AND seedConfig != nil AND (ratio-arm OR time-arm)).
// See [RQBit.applyCleanupGate] for the per-arm port.
//
// nil seedCfg binding (composition root hasn't injected the
// dependency) degrades the gate to "never satisfy" — preserves the
// pre-J94 false-default posture so a partial-wiring environment
// doesn't aggressively remove torrents the user expected to keep.
func (r *RQBit) GetItems(ctx context.Context) ([]*downloadclients.Item, error) {
	if r.proxy == nil {
		return []*downloadclients.Item{}, nil
	}
	torrents, err := r.proxy.GetTorrents(ctx, r.settings)
	if err != nil {
		return nil, fmt.Errorf("rqbit: GetTorrents: %w", err)
	}
	info := r.itemClientInfo()
	out := make([]*downloadclients.Item, 0, len(torrents))
	for _, t := range torrents {
		path := strings.TrimSpace(t.Path)
		if path == "" {
			r.log.Warn("rqbit: torrent has empty download path; skipping", "name", t.Name)
			continue
		}
		if strings.HasPrefix(path, ".") {
			r.log.Warn("rqbit: torrent path starts with '.'; skipping", "name", t.Name, "path", path)
			continue
		}
		out = append(out, r.toItem(ctx, info, t))
	}
	return out, nil
}

// toItem projects one [Torrent] into a [downloadclients.Item].
func (r *RQBit) toItem(ctx context.Context, info *downloadclients.ItemClientInfo, t *Torrent) *downloadclients.Item {
	output := ospath.New(t.Path)
	if r.rpm != nil {
		if mapped, err := r.rpm.RemapRemoteToLocal(ctx, r.settings.Host, output); err == nil {
			output = mapped
		} else {
			r.log.Debug("rqbit: RemapRemoteToLocal failed", "path", t.Path, "err", err)
		}
	}

	ratio := t.Ratio
	item := &downloadclients.Item{
		DownloadClientInfo: info,
		DownloadID:         t.Hash,
		Title:              t.Name,
		Message:            t.Message,
		OutputPath:         output,
		TotalSize:          t.TotalSize,
		RemainingSize:      t.RemainingSize,
		SeedRatio:          &ratio,
	}
	if t.DownRate > 0 {
		d := time.Duration(t.RemainingSize/t.DownRate) * time.Second
		item.RemainingTime = &d
	} else {
		zero := time.Duration(0)
		item.RemainingTime = &zero
	}

	switch {
	case t.IsFinished:
		item.Status = downloadclients.DownloadItemStatusCompleted
	case t.IsActive:
		item.Status = downloadclients.DownloadItemStatusDownloading
	default:
		item.Status = downloadclients.DownloadItemStatusPaused
	}

	r.applyCleanupGate(ctx, info, t, item)
	return item
}

// applyCleanupGate ports the C# `RQBit.cs:101-122` predicate that
// gates the post-import disk-cleanup arm.
//
// Algorithm:
//  1. If RemoveCompletedDownloads is off OR the torrent isn't
//     finished OR no seed-config binding is wired, the gate fails
//     immediately — matches C# `:101` AND-chain.
//  2. Look up the per-torrent seed configuration via
//     [downloadclients.SeedConfigProvider]. C# `:99`. nil-config
//     fails the gate (no recorded grab → no override → leave the
//     torrent in place). Lookup errors are logged at debug + the
//     gate fails — a transient DB error mustn't aggressively remove
//     a torrent that might be retained on retry.
//  3. Two-arm check at C# `:103-118`:
//     a. Ratio arm: `torrent.Ratio / 1000.0 >= seedConfig.Ratio`.
//     Ported verbatim INCLUDING the `/1000.0` divisor — the
//     C# code carries this and we mirror it for behavioural
//     parity even though the proxy's Ratio is already
//     denominated as a real number (UploadedBytes/ProgressBytes
//     at proxy.go:240). The discrepancy looks like a C# bug
//     upstream, but the port preserves it so behaviour matches
//     the reference until upstream fixes the divisor.
//     b. SeedTime arm: `now - epoch(FinishedTime) >= seedConfig.SeedTime`.
//     The RQBit wire shape doesn't surface FinishedTime (C#
//     `RQBitTorrent.cs:16` declares the field but
//     `RQbitProxy.cs:95-130` never populates it; the Go proxy
//     omits it for the same reason). With FinishedTime
//     unpopulated (zero), the arm degenerates to "now since
//     1970 >= seedConfig.SeedTime" which is trivially true for
//     any positive SeedTime — preserving the C# observable
//     behaviour where the time arm fires once the torrent is
//     finished regardless of when it actually finished.
//  4. Either-arm match → CanMoveFiles = CanBeRemoved = true. C#
//     `:121` couples them.
//
// Nullable arms: `seedConfig.Ratio` / `seedConfig.SeedTime` are
// optional. When nil, the corresponding arm doesn't fire — matches
// C# nullable-comparison semantics where `>=` against a null
// returns false.
func (r *RQBit) applyCleanupGate(ctx context.Context, info *downloadclients.ItemClientInfo, t *Torrent, item *downloadclients.Item) {
	if info == nil || !info.RemoveCompletedDownloads || !t.IsFinished || r.seedCfg == nil {
		return
	}

	seedConfig, err := r.seedCfg.GetSeedConfiguration(ctx, t.Hash)
	if err != nil {
		r.log.Debug("rqbit: GetSeedConfiguration failed; cleanup gate denied", "hash", t.Hash, "err", err)
		return
	}
	if seedConfig == nil {
		return
	}

	if seedConfig.Ratio != nil && (t.Ratio/1000.0) >= *seedConfig.Ratio {
		r.log.Debug("rqbit: torrent met seed ratio goal", "hash", t.Hash, "goal", *seedConfig.Ratio)
		item.CanMoveFiles = true
		item.CanBeRemoved = true
		return
	}
	if seedConfig.SeedTime != nil {
		finishedAt := time.Unix(t.FinishedTime, 0)
		if r.now().Sub(finishedAt) >= *seedConfig.SeedTime {
			r.log.Debug("rqbit: torrent met seed time goal", "hash", t.Hash, "goal", seedConfig.SeedTime.String())
			item.CanMoveFiles = true
			item.CanBeRemoved = true
			return
		}
	}
	r.log.Debug("rqbit: torrent seeding goals not yet reached", "hash", t.Hash)
}

// itemClientInfo mirrors C# `DownloadClientItemClientInfo.FromDownloadClient(this, false)`.
func (r *RQBit) itemClientInfo() *downloadclients.ItemClientInfo {
	info := &downloadclients.ItemClientInfo{
		Protocol: r.Protocol(),
		Type:     Implementation,
		Name:     r.Name(),
	}
	if r.def != nil {
		info.ID = r.def.ID
		info.Name = r.def.Name
		info.RemoveCompletedDownloads = r.def.RemoveCompletedDownloads
	}
	return info
}

// GetImportItem is the C# base passthrough.
func (r *RQBit) GetImportItem(_ context.Context, item *downloadclients.Item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

// RemoveItem ports C# `RQBit.cs:130-133`. Forwards to
// [Proxy.RemoveTorrent] with the deleteData flag → /forget or
// /delete endpoint.
func (r *RQBit) RemoveItem(ctx context.Context, item *downloadclients.Item, deleteData bool) error {
	if r.proxy == nil {
		return errors.New("rqbit: nil Proxy")
	}
	if item == nil {
		return errors.New("rqbit: nil item")
	}
	id := strings.TrimSpace(item.DownloadID)
	if id == "" {
		return errors.New("rqbit: item has empty DownloadID")
	}
	if err := r.proxy.RemoveTorrent(ctx, r.settings, id, deleteData); err != nil {
		return fmt.Errorf("rqbit: RemoveTorrent(%s): %w", id, err)
	}
	return nil
}

// GetStatus ports C# `RQBit.cs:135-141` — IsLocalhost-only.
func (r *RQBit) GetStatus(_ context.Context) (*downloadclients.ClientInfo, error) {
	return &downloadclients.ClientInfo{IsLocalhost: isLocalHost(r.settings.Host)}, nil
}

// MarkItemAsImported is the C# base default. RQBit has no
// post-import-category concept (the SetTorrentLabel C# stub at
// `:194-197` logs a warning and no-ops).
func (r *RQBit) MarkItemAsImported(_ context.Context, _ *downloadclients.Item) error {
	return downloadclients.ErrMarkAsImportedUnsupported
}

// RequestAction is the C# base no-op.
func (r *RQBit) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// Test ports `RQBit.Test(failures)` at C# `:143-153`. Runs
// Settings.Validate first, then drives the version probe — fetches
// the version string, parses `X.Y.Z`, rejects anything below 8.0.0.
//
// Connection failure surfaces as a Host-property failure; version
// parse / floor failures as a generic empty-property failure
// (matches C# `:189`).
func (r *RQBit) Test(ctx context.Context) ([]downloadclients.TestFailure, error) {
	failures := r.settings.Validate()
	if len(failures) > 0 {
		return failures, nil
	}
	version, err := r.proxy.GetVersion(ctx, r.settings)
	if err != nil {
		return []downloadclients.TestFailure{{
			PropertyName: "Host",
			ErrorMessage: fmt.Sprintf("Unable to reach RQBit: %v", err),
		}}, nil
	}
	if strings.TrimSpace(version) == "" {
		return []downloadclients.TestFailure{{
			PropertyName: "",
			ErrorMessage: "Unable to determine RQBit version. Please check that RQBit is running and accessible.",
		}}, nil
	}
	got, ok := parseVersion(version)
	if !ok {
		return []downloadclients.TestFailure{{
			PropertyName: "",
			ErrorMessage: fmt.Sprintf("Unable to parse RQBit version %q. Please check that RQBit is running and accessible.", version),
		}}, nil
	}
	min, _ := parseVersion(minVersion)
	if compareVersion(got, min) < 0 {
		return []downloadclients.TestFailure{{
			PropertyName: "",
			ErrorMessage: fmt.Sprintf("RQBit version %s is not supported. Please upgrade to version %s or higher.", version, minVersion),
		}}, nil
	}
	return nil, nil
}

// SetRemotePathMapper binds the optional [downloadclients.RemotePathMapper].
// Same Phase 6.B pattern as the other DCs.
func (r *RQBit) SetRemotePathMapper(rpm downloadclients.RemotePathMapper) {
	r.rpm = rpm
}

// SetSeedConfigProvider binds the optional
// [downloadclients.SeedConfigProvider]. nil-binding leaves
// [RQBit.applyCleanupGate] in its degraded "never satisfy" state
// (matches the pre-J94 false-default posture). Phase 5.J94 wire-up.
func (r *RQBit) SetSeedConfigProvider(s downloadclients.SeedConfigProvider) {
	r.seedCfg = s
}

// versionPattern matches the `X.Y.Z` prefix the RQBit `version`
// field reports. Mirrors C# `Regex.Match(versionString,
// @"(\d+)\.(\d+)\.(\d+)")` at `RQBit.cs:173`.
var versionPattern = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// parseVersion extracts the major.minor.patch tuple from s. ok=false
// when the regex doesn't match.
func parseVersion(s string) ([3]int, bool) {
	m := versionPattern.FindStringSubmatch(s)
	if len(m) != 4 {
		return [3]int{}, false
	}
	maj, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	pat, _ := strconv.Atoi(m[3])
	return [3]int{maj, min, pat}, true
}

// compareVersion returns -1/0/+1 for a vs b.
func compareVersion(a, b [3]int) int {
	for i := 0; i < 3; i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}

// isLocalHost mirrors C# `Settings.Host == "127.0.0.1" || == "localhost"`.
func isLocalHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

// Compile-time guard.
var _ downloadclients.Provider = (*RQBit)(nil)

// constructor is the registry factory.
func constructor(def *downloadclients.DownloadClient, deps downloadclients.Dependencies) (downloadclients.Provider, error) {
	if def == nil {
		return nil, errors.New("rqbit: nil DownloadClient row")
	}
	settings := NewSettings()
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("rqbit: settings unmarshal: %w", err)
		}
	}
	var log *slog.Logger
	if l, ok := deps.Logger.(*slog.Logger); ok {
		log = l
	}
	r := New(def, settings, nil, log)
	if rpm, ok := deps.RemotePathMappingService.(downloadclients.RemotePathMapper); ok {
		r.SetRemotePathMapper(rpm)
	}
	if seedCfg, ok := deps.SeedConfigProvider.(downloadclients.SeedConfigProvider); ok {
		r.SetSeedConfigProvider(seedCfg)
	}
	return r, nil
}

func init() {
	downloadclients.Default().MustRegister(Implementation, ConfigContract, constructor)
}
