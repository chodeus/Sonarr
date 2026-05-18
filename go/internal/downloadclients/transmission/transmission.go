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
	"time"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/ospath"
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

// GetImportItem mirrors the C# base default — pass input through.
// `DownloadClientBase.cs:102-105`.
func (t *Transmission) GetImportItem(_ context.Context, item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

// GetItems ports the C# `TransmissionBase.GetItems` at
// `TransmissionBase.cs:40-143` — fetches every torrent visible to the
// RPC endpoint, filters to the configured category (label-based when
// the client supports labels at v4.0+, directory-prefix-based for
// older versions), projects each torrent into the orchestrator's
// canonical [downloadclients.Item] shape.
//
// Slice 4.BT (C-cleanup sub-arc C2b).
func (t *Transmission) GetItems(ctx context.Context) ([]*downloadclients.Item, error) {
	torrents, err := t.proxy.GetTorrents(ctx, nil, t.settings)
	if err != nil {
		return nil, fmt.Errorf("transmission: GetItems: GetTorrents: %w", err)
	}

	// SupportsLabels is gated on Transmission >= 4.0. Probe once; if
	// the version probe fails we conservatively assume false (matches
	// C# behaviour at `Transmission.cs:21` which returns false on a
	// failed HasClientVersion call).
	supportsLabels := t.supportsLabels(ctx)
	info := t.itemClientInfo(supportsLabels)

	// Lazy-load the session config — only fetched when we actually
	// hit the seed-limit gate. C# uses `Lazy<TransmissionConfig>` at
	// `:42`.
	var (
		cfg     *TransmissionConfig
		cfgErr  error
		cfgOnce bool
	)
	loadCfg := func() *TransmissionConfig {
		if cfgOnce {
			return cfg
		}
		cfgOnce = true
		cfg, cfgErr = t.proxy.GetConfig(ctx, t.settings)
		if cfgErr != nil {
			t.log.Warn("transmission: GetItems: GetConfig failed; seed-limit gate degrades to false",
				"err", cfgErr)
		}
		return cfg
	}

	out := make([]*downloadclients.Item, 0, len(torrents))
	for i := range torrents {
		tor := torrents[i]
		if !t.matchesCategory(tor, supportsLabels) {
			continue
		}
		item := t.torrentToItem(tor, info)
		// Seed-limit + can-move gating uses the session config.
		item.CanBeRemoved = info.RemoveCompletedDownloads && hasReachedSeedLimit(tor, item.SeedRatio, loadCfg)
		item.CanMoveFiles = item.CanBeRemoved && tor.Status == TorrentStatusStopped
		out = append(out, item)
	}
	return out, nil
}

// matchesCategory mirrors the C# filter at `TransmissionBase.cs:51-75`.
// When labels are supported AND the user has TvCategory + the torrent
// has labels: keep only torrents whose labels contain TvCategory
// (case-insensitive). Otherwise: directory-based fallback — when
// TvDirectory is set, keep torrents whose downloadDir is under it;
// else when TvCategory is set, keep torrents whose downloadDir
// contains a path segment equal to TvCategory; else keep all.
func (t *Transmission) matchesCategory(tor TransmissionTorrent, supportsLabels bool) bool {
	tvCat := strings.TrimSpace(t.settings.TvCategory)
	if tvCat != "" && supportsLabels && len(tor.Labels) > 0 {
		for _, l := range tor.Labels {
			if strings.EqualFold(l, tvCat) {
				return true
			}
		}
		return false
	}
	if dir := strings.TrimSpace(t.settings.TvDirectory); dir != "" {
		return pathContains(dir, tor.DownloadDir)
	}
	if tvCat != "" {
		for _, seg := range strings.FieldsFunc(tor.DownloadDir, func(r rune) bool { return r == '/' || r == '\\' }) {
			if seg == tvCat {
				return true
			}
		}
		return false
	}
	return true
}

// torrentToItem builds the typed Item from a Transmission torrent.
func (t *Transmission) torrentToItem(tor TransmissionTorrent, info *downloadclients.ItemClientInfo) *downloadclients.Item {
	outputPath := ospath.New(tor.DownloadDir)
	// Append the torrent name (C# `:259-262` GetOutputPath). Replace
	// ':' to dodge Windows filename rules — match C# behaviour.
	safeName := strings.ReplaceAll(tor.Name, ":", "_")
	if safeName != "" {
		joined := joinPath(tor.DownloadDir, safeName)
		outputPath = ospath.New(joined)
	}

	var ratio *float64
	if tor.DownloadedEver > 0 {
		r := float64(tor.UploadedEver) / float64(tor.DownloadedEver)
		ratio = &r
	} else {
		zero := 0.0
		ratio = &zero
	}

	item := &downloadclients.Item{
		DownloadClientInfo: info,
		DownloadID:         strings.ToUpper(tor.HashString),
		Category:           t.settings.TvCategory,
		Title:              tor.Name,
		OutputPath:         outputPath,
		TotalSize:          tor.TotalSize,
		RemainingSize:      tor.LeftUntilDone,
		SeedRatio:          ratio,
	}

	if tor.Eta >= 0 {
		d := time.Duration(tor.Eta) * time.Second
		// Guard against the C# overflow catch at `:97-100`: if Eta
		// exceeds time.Duration's int64-nanosecond range, fall back
		// to milliseconds (matches the C# fallback behavior).
		if tor.Eta > int64(time.Duration(1<<62)/time.Second) {
			d = time.Duration(tor.Eta) * time.Millisecond
		}
		item.RemainingTime = &d
	}

	switch {
	case strings.TrimSpace(tor.ErrorString) != "":
		item.Status = downloadclients.DownloadItemStatusWarning
		item.Message = fmt.Sprintf("%s: %s", t.Name(), tor.ErrorString)
	case tor.TotalSize == 0:
		item.Status = downloadclients.DownloadItemStatusQueued
	case tor.LeftUntilDone == 0 && (tor.Status == TorrentStatusStopped ||
		tor.Status == TorrentStatusSeeding ||
		tor.Status == TorrentStatusSeedingWait):
		item.Status = downloadclients.DownloadItemStatusCompleted
	case tor.IsFinished && tor.Status != TorrentStatusCheck && tor.Status != TorrentStatusCheckWait:
		item.Status = downloadclients.DownloadItemStatusCompleted
	case tor.Status == TorrentStatusQueued:
		item.Status = downloadclients.DownloadItemStatusQueued
	default:
		item.Status = downloadclients.DownloadItemStatusDownloading
	}
	return item
}

// hasReachedSeedLimit ports C# `:145-183`. Reads the global config
// lazily via the supplied loader.
func hasReachedSeedLimit(tor TransmissionTorrent, ratio *float64, loadCfg func() *TransmissionConfig) bool {
	isStopped := tor.Status == TorrentStatusStopped
	isSeeding := tor.Status == TorrentStatusSeeding

	// Per-torrent ratio limit.
	switch tor.SeedRatioMode {
	case 1:
		if isStopped && ratio != nil && *ratio >= tor.SeedRatioLimit {
			return true
		}
	case 0:
		if isStopped {
			cfg := loadCfg()
			if cfg != nil && cfg.SeedRatioLimited && ratio != nil && *ratio >= cfg.SeedRatioLimit {
				return true
			}
		}
	}

	// Per-torrent idle seeding limit. Transmission expresses
	// SeedIdleLimit in minutes; SecondsSeeding in seconds.
	switch tor.SeedIdleMode {
	case 1:
		if (isStopped || isSeeding) && tor.SecondsSeeding > tor.SeedIdleLimit*60 {
			return true
		}
	case 0:
		if isStopped {
			cfg := loadCfg()
			if cfg != nil && cfg.IdleSeedingLimitEnabled {
				return true
			}
		}
	}
	return false
}

// RemoveItem ports C# `TransmissionBase.RemoveItem` at `:185-188` —
// a thin wrapper over the proxy's torrent-remove RPC.
//
// Slice 4.BT (C-cleanup sub-arc C2b).
func (t *Transmission) RemoveItem(ctx context.Context, item *downloadclients.Item, deleteData bool) error {
	if item == nil {
		return errors.New("transmission: RemoveItem: nil item")
	}
	hash := strings.ToLower(item.DownloadID)
	if err := t.proxy.RemoveTorrent(ctx, hash, deleteData, t.settings); err != nil {
		return fmt.Errorf("transmission: RemoveItem(%s): %w", hash, err)
	}
	return nil
}

// GetStatus ports C# `TransmissionBase.GetStatus` at `:190-214`.
// Returns the destDir-derived OutputRootFolders + IsLocalhost flag.
// RemotePathMappingService remap is deferred (same as SAB sub-arc
// C2a) — destDir is the raw RPC-side path.
//
// Slice 4.BT (C-cleanup sub-arc C2b).
func (t *Transmission) GetStatus(ctx context.Context) (*downloadclients.ClientInfo, error) {
	destDir := strings.TrimSpace(t.settings.TvDirectory)
	if destDir == "" {
		cfg, err := t.proxy.GetConfig(ctx, t.settings)
		if err != nil {
			return nil, fmt.Errorf("transmission: GetStatus: GetConfig: %w", err)
		}
		destDir = cfg.DownloadDir
		if tvCat := strings.TrimSpace(t.settings.TvCategory); tvCat != "" {
			destDir = joinPath(destDir, tvCat)
		}
	}
	info := &downloadclients.ClientInfo{
		IsLocalhost: t.settings.Host == "127.0.0.1" || strings.EqualFold(t.settings.Host, "localhost"),
	}
	if destDir != "" {
		info.OutputRootFolders = []ospath.OsPath{ospath.New(destDir)}
	}
	return info, nil
}

// MarkItemAsImported ports C# `Transmission.MarkItemAsImported` at
// `Transmission.cs:36-73`. Requires version 4.0+ (label support);
// otherwise returns [downloadclients.ErrMarkAsImportedUnsupported].
// When TvImportedCategory is set and differs from TvCategory, fetches
// the torrent's current labels, adds TvImportedCategory, removes
// TvCategory, and posts a torrent-set RPC.
//
// Slice 4.BT (C-cleanup sub-arc C2b).
func (t *Transmission) MarkItemAsImported(ctx context.Context, item *downloadclients.Item) error {
	if item == nil {
		return errors.New("transmission: MarkItemAsImported: nil item")
	}
	if !t.supportsLabels(ctx) {
		return fmt.Errorf("%w: %s requires Transmission 4.0+", downloadclients.ErrMarkAsImportedUnsupported, t.Name())
	}
	imported := strings.TrimSpace(t.settings.TvImportedCategory)
	if imported == "" || imported == strings.TrimSpace(t.settings.TvCategory) {
		// Nothing to swap. C# silently no-ops at `:44-45` when the
		// imported category is empty or equal to the current one.
		return nil
	}

	hash := strings.ToLower(item.DownloadID)
	torrents, err := t.proxy.GetTorrents(ctx, []string{hash}, t.settings)
	if err != nil {
		return fmt.Errorf("transmission: MarkItemAsImported: GetTorrents: %w", err)
	}
	if len(torrents) == 0 {
		t.log.Warn("transmission: MarkItemAsImported: torrent not found",
			"hash", hash)
		return nil
	}

	// Set-style dedup, case-insensitive matches C# `:58`.
	labels := make([]string, 0, len(torrents[0].Labels)+1)
	seen := make(map[string]struct{}, len(torrents[0].Labels)+1)
	add := func(l string) {
		k := strings.ToLower(l)
		if _, dup := seen[k]; dup {
			return
		}
		seen[k] = struct{}{}
		labels = append(labels, l)
	}
	currentCat := strings.TrimSpace(t.settings.TvCategory)
	for _, l := range torrents[0].Labels {
		if currentCat != "" && strings.EqualFold(l, currentCat) {
			continue
		}
		add(l)
	}
	add(imported)

	if err := t.proxy.SetTorrentLabels(ctx, hash, labels, t.settings); err != nil {
		t.log.Warn("transmission: MarkItemAsImported: SetTorrentLabels failed",
			"hash", hash, "err", err)
		return nil
	}
	return nil
}

// supportsLabels probes the Transmission version. Mirrors the C#
// `HasClientVersion(4, 0)` predicate at `Transmission.cs:21` and
// `TransmissionBase.cs:330-338`. Returns false on any probe error
// (matches the C# conservative posture — the RPC client treats
// unreachable version probes as "no label support" so the orchestrator
// degrades gracefully).
func (t *Transmission) supportsLabels(ctx context.Context) bool {
	raw, err := t.proxy.GetClientVersion(ctx, t.settings)
	if err != nil {
		return false
	}
	v := parseVersion(raw)
	if v == nil {
		return false
	}
	return v.major >= 4
}

// itemClientInfo mirrors the C# `DownloadClientItemClientInfo.FromDownloadClient`
// projection. HasPostImportCategory is true when TvImportedCategory
// is set AND the version supports labels.
func (t *Transmission) itemClientInfo(supportsLabels bool) *downloadclients.ItemClientInfo {
	info := &downloadclients.ItemClientInfo{
		Protocol: t.Protocol(),
		Type:     Implementation,
		Name:     t.Name(),
	}
	if t.def != nil {
		info.ID = t.def.ID
		info.Name = t.def.Name
		info.RemoveCompletedDownloads = t.def.RemoveCompletedDownloads
	}
	if supportsLabels && strings.TrimSpace(t.settings.TvImportedCategory) != "" {
		info.HasPostImportCategory = true
	}
	return info
}

// pathContains reports whether candidate is at-or-under parent.
// Mirrors the C# `OsPath.Contains` semantic without dragging in a
// full OsPath port. Compares lexically; assumes both are absolute or
// both are relative.
func pathContains(parent, candidate string) bool {
	if parent == "" || candidate == "" {
		return false
	}
	p := strings.TrimRight(parent, "/\\")
	c := strings.TrimRight(candidate, "/\\")
	if p == c {
		return true
	}
	return strings.HasPrefix(c, p+"/") || strings.HasPrefix(c, p+"\\")
}

// joinPath glues a relative tail onto a base path using whichever
// separator the base uses. Mirrors the OsPath + operator at C#
// without a full OsPath port.
func joinPath(base, tail string) string {
	if base == "" {
		return tail
	}
	if tail == "" {
		return base
	}
	sep := "/"
	if strings.ContainsRune(base, '\\') && !strings.ContainsRune(base, '/') {
		sep = "\\"
	}
	b := strings.TrimRight(base, "/\\")
	t := strings.TrimLeft(tail, "/\\")
	return b + sep + t
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

// RequestAction is the per-impl action map. Transmission ships
// `getCustomTorrentFolders` in C# — the Go port keeps the entry
// point but returns the C# default (nil, nil) until the per-action
// body ports. Mirrors `DownloadClientBase.cs:165-167`.
func (t *Transmission) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
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
