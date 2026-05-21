package deluge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/ospath"
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
// Slice 5.G2c scope: Name / Protocol / Definition / RequestAction /
// GetImportItem / Test live. Download / GetItems / RemoveItem /
// GetStatus / MarkItemAsImported return
// [downloadclients.ErrNotImplementedYet]. Subsequent slices fill in
// each method.
type Deluge struct {
	settings Settings
	proxy    *Proxy
	def      *downloadclients.DownloadClient
	rpm      downloadclients.RemotePathMapper
	log      *slog.Logger
}

// New constructs a Deluge Provider against the resolved settings +
// proxy. Used by both the registry [Constructor] and by tests that
// pin a stub HTTPDoer. Remote-path mapping is bound via
// [Deluge.SetRemotePathMapper] after construction.
func New(def *downloadclients.DownloadClient, settings Settings, proxy *Proxy, log *slog.Logger) *Deluge {
	if log == nil {
		log = slog.Default()
	}
	return &Deluge{settings: settings, proxy: proxy, def: def, log: log}
}

// SetRemotePathMapper binds the [downloadclients.RemotePathMapper]
// the torrentToItem and GetStatus paths consult to translate the
// deluge-side absolute paths (DownloadPath + the resolved destDir)
// back to Sonarr-side paths. Optional — nil mapper degrades to the
// raw RPC-side path. Slice 6.B6 (Phase 6.B wire-up arc).
//
// Returns the receiver for chainable construction.
func (d *Deluge) SetRemotePathMapper(rpm downloadclients.RemotePathMapper) *Deluge {
	d.rpm = rpm
	return d
}

// remapOutput translates a deluge-side absolute path back to a
// Sonarr-side path via the wired [downloadclients.RemotePathMapper].
// Returns the raw path unchanged when the mapper is nil, when the
// input is empty, or when the mapper returns an error (debug-log
// the error).
func (d *Deluge) remapOutput(ctx context.Context, raw string) ospath.OsPath {
	if raw == "" {
		return ospath.OsPath{}
	}
	remote := ospath.New(raw)
	if d.rpm == nil {
		return remote
	}
	local, err := d.rpm.RemapRemoteToLocal(ctx, d.settings.Host, remote)
	if err != nil {
		d.log.Debug("deluge: RemapRemoteToLocal failed; using raw path",
			slog.String("host", d.settings.Host),
			slog.String("path", raw),
			slog.Any("err", err))
		return remote
	}
	return local
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
// 5.G2d+ slices and tests.
func (d *Deluge) Settings() Settings { return d.settings }

// Proxy returns the bound proxy.
func (d *Deluge) Proxy() *Proxy { return d.proxy }

// Download ports the C# Deluge add orchestrator at
// `Download/Clients/Deluge/Deluge.cs:58-110` — both AddFromMagnetLink
// and AddFromTorrentFile share the same post-add steps (seed-config,
// label, queue-top), so the Go port collapses them into one method.
//
// Add-path selection:
//
//   - TorrentBody non-empty → AddTorrentFromFile (base64-encoded).
//   - MagnetURL non-empty → AddTorrentFromMagnet.
//   - DownloadURL only, no MagnetURL and no TorrentBody → error.
//     Deluge has no "fetch this URL" add — the orchestrator should
//     pre-fetch the .torrent bytes into req.TorrentBody before
//     dispatch. The C# tree's TorrentClientBase pre-fetches at a
//     layer above the per-impl Download method.
//
// Post-add best-effort RPCs (log + continue on failure, matches
// the C# pattern that doesn't propagate post-add failures since
// the torrent is already loaded into deluge):
//
//   - SetTorrentSeedingConfiguration when req.SeedConfiguration != nil.
//   - SetTorrentLabel when settings.TvCategory is non-empty.
//   - MoveTorrentToTopInQueue when (IsRecent + RecentTvPriority==First)
//     OR (!IsRecent + OlderTvPriority==First). Matches C#
//     `:74-80,101-107`.
//
// Returns the upper-cased info-hash for the DownloadID handoff
// (matches the GetItems DownloadID convention).
func (d *Deluge) Download(ctx context.Context, req *downloadclients.GrabRequest) (string, error) {
	if req == nil {
		return "", errors.New("deluge: Download: nil request")
	}
	if d.proxy == nil {
		return "", errors.New("deluge: Download: nil proxy")
	}

	var (
		hash string
		err  error
	)
	switch {
	case len(req.TorrentBody) > 0:
		hash, err = d.proxy.AddTorrentFromFile(ctx, fallbackFilename(req), req.TorrentBody, d.settings)
	case strings.TrimSpace(req.MagnetURL) != "":
		hash, err = d.proxy.AddTorrentFromMagnet(ctx, req.MagnetURL, d.settings)
	default:
		return "", errors.New("deluge: Download: GrabRequest has neither TorrentBody nor MagnetURL; orchestrator must pre-fetch DownloadURL")
	}
	if err != nil {
		return "", fmt.Errorf("deluge: Download(%q): %w", req.ReleaseTitle, err)
	}

	// Post-add steps — warn-and-continue (C# tree at :67-79,94-106
	// doesn't propagate post-add failures; the torrent is already
	// loaded).
	if req.SeedConfiguration != nil {
		if seedErr := d.proxy.SetTorrentSeedingConfiguration(ctx, hash, req.SeedConfiguration, d.settings); seedErr != nil {
			d.log.Warn("deluge: SetTorrentSeedingConfiguration failed",
				"hash", hash, "err", seedErr)
		}
	}
	if cat := strings.TrimSpace(d.settings.TvCategory); cat != "" {
		if labErr := d.proxy.SetTorrentLabel(ctx, hash, cat, d.settings); labErr != nil {
			d.log.Warn("deluge: SetTorrentLabel failed",
				"hash", hash, "label", cat, "err", labErr)
		}
	}
	if shouldMoveToTop(req.IsRecent, d.settings) {
		if qErr := d.proxy.MoveTorrentToTopInQueue(ctx, hash, d.settings); qErr != nil {
			d.log.Warn("deluge: MoveTorrentToTopInQueue failed",
				"hash", hash, "err", qErr)
		}
	}

	return strings.ToUpper(hash), nil
}

// shouldMoveToTop ports the priority gate from C# `:76-77,103-104`:
// move-to-top fires when the relevant priority setting is set to
// First (1). Recent episodes use RecentTvPriority; older use
// OlderTvPriority.
func shouldMoveToTop(isRecent bool, settings Settings) bool {
	if isRecent {
		return settings.RecentTvPriority == PriorityFirst
	}
	return settings.OlderTvPriority == PriorityFirst
}

// fallbackFilename returns a reasonable .torrent filename for the
// AddTorrentFromFile request. Deluge uses the filename only for the
// WebUI's pending-torrent display + as the suggested rename — any
// non-empty value works.
func fallbackFilename(req *downloadclients.GrabRequest) string {
	if t := strings.TrimSpace(req.ReleaseTitle); t != "" {
		return t + ".torrent"
	}
	return "release.torrent"
}

// GetItems ports C# `Deluge.GetItems` at
// `Download/Clients/Deluge/Deluge.cs:114-215`. Fetches every torrent
// the WebUI reports (filtered server-side by label when
// settings.TvCategory is non-empty per the C# `GetTorrentsByLabel`
// branch at `:118-121`) and projects each into the orchestrator's
// canonical [downloadclients.Item] shape.
//
// Differences from the C# port:
//
//   - The ReconnectToDaemon arm at C# `:198-208` is documented as
//     buggy upstream — the outer `&& _hasAttemptedReconnecting`
//     guard makes the reconnect-arm dead code (the inner `else`
//     branch can never fire), so the C# tree only ever logs the
//     warn line. The Go port intentionally omits the reconnect dance
//     for the same observable behavior.
//   - Remote-path-mapping translation at C# `:147` is wired via
//     [Deluge.SetRemotePathMapper] (Phase 6.B) — the constructor
//     casts [downloadclients.Dependencies.RemotePathMappingService]
//     to [downloadclients.RemotePathMapper] and OutputPath flows
//     through `remapOutput` per-call.
//   - Ignored-torrent counter at C# `:128,136-138` collapses to a
//     single slog.Warn per call (the reconnect dance is unreachable
//     in C# so the stateful "have we tried reconnecting yet" branch
//     buys nothing).
func (d *Deluge) GetItems(ctx context.Context) ([]*downloadclients.Item, error) {
	if d.proxy == nil {
		return nil, errors.New("deluge: GetItems: nil proxy")
	}
	torrents, err := d.proxy.GetTorrents(ctx, d.settings)
	if err != nil {
		return nil, fmt.Errorf("deluge: GetItems: %w", err)
	}

	info := d.itemClientInfo()
	items := make([]*downloadclients.Item, 0, len(torrents))
	ignored := 0
	for i := range torrents {
		t := torrents[i]
		if strings.TrimSpace(t.Hash) == "" || strings.TrimSpace(t.Name) == "" {
			ignored++
			continue
		}
		items = append(items, d.torrentToItem(ctx, t, info))
	}
	if ignored > 0 {
		// Mirrors the C# `:198-202` warn line but without the
		// reconnect-state arm. The Provider's caller can still see
		// the per-torrent items it does have.
		d.log.Warn("deluge: GetItems: torrents missing hash or name were skipped",
			"count", ignored)
	}
	return items, nil
}

// torrentToItem builds the typed Item from a deluge torrent.
// Slice 6.B6 routes the resolved OutputPath through
// [Deluge.remapOutput] so off-host deluge instances surface a
// Sonarr-side path; nil mapper / no match → raw RPC-side path.
func (d *Deluge) torrentToItem(ctx context.Context, t DelugeTorrent, info *downloadclients.ItemClientInfo) *downloadclients.Item {
	ratio := t.Ratio
	ratioPtr := &ratio

	remaining := t.Size - t.BytesDownloaded
	if remaining < 0 {
		remaining = 0
	}

	item := &downloadclients.Item{
		DownloadClientInfo: info,
		DownloadID:         strings.ToUpper(t.Hash),
		Category:           d.settings.TvCategory,
		Title:              t.Name,
		TotalSize:          t.Size,
		RemainingSize:      remaining,
		SeedRatio:          ratioPtr,
	}

	// ETA: deluge emits the float64 value as seconds. The C# tree
	// at `:154-160` catches OverflowException and falls back to
	// TimeSpan.MaxValue; the Go port mirrors with a max-duration
	// fallback so the UI doesn't render NaN.
	if t.Eta > 0 && !math.IsInf(t.Eta, 0) && !math.IsNaN(t.Eta) {
		secs := t.Eta
		// time.Duration ceiling is ~292 years; deluge sometimes
		// returns 8640000 ("never" sentinel) — accept up to that
		// scale, clamp anything bigger.
		const maxEtaSeconds = float64(1<<31 - 1)
		if secs > maxEtaSeconds {
			secs = maxEtaSeconds
		}
		d := time.Duration(secs * float64(time.Second))
		item.RemainingTime = &d
	}

	// OutputPath: deluge save_path + torrent name. C# `:147-148`
	// composes via OsPath operator+; the Go port uses a simple
	// '/'-separated join (deluge always emits '/' separators).
	// Slice 6.B6: route the joined path through remapOutput so
	// off-host deluge instances surface a Sonarr-side path.
	if strings.TrimSpace(t.DownloadPath) != "" {
		item.OutputPath = d.remapOutput(ctx, joinTorrentPath(t.DownloadPath, t.Name))
	}

	// Status mapping per C# `:164-184`.
	switch {
	case t.State == StatusError:
		item.Status = downloadclients.DownloadItemStatusWarning
		item.Message = "Deluge reported an error on this torrent"
		if strings.TrimSpace(t.Message) != "" {
			item.Message = t.Message
		}
	case t.IsFinished && t.State != StatusChecking:
		// C# `:169-172`: completed-paused torrents stay state="Paused"
		// per deluge convention; IsFinished true → Completed
		// unconditionally except mid-recheck.
		item.Status = downloadclients.DownloadItemStatusCompleted
	case t.State == StatusQueued:
		item.Status = downloadclients.DownloadItemStatusQueued
	case t.State == StatusPaused:
		item.Status = downloadclients.DownloadItemStatusPaused
	default:
		item.Status = downloadclients.DownloadItemStatusDownloading
	}

	// CanBeRemoved + CanMoveFiles per C# `:188-193`. Five-way AND:
	// RemoveCompletedDownloads (on the row) + IsAutoManaged +
	// StopAtRatio + Ratio >= StopRatio + State == "Paused".
	item.CanBeRemoved = info != nil && info.RemoveCompletedDownloads &&
		t.IsAutoManaged && t.StopAtRatio &&
		t.Ratio >= t.StopRatio && t.State == StatusPaused
	item.CanMoveFiles = item.CanBeRemoved

	return item
}

// itemClientInfo mirrors C#
// `DownloadClientItemClientInfo.FromDownloadClient` at
// `DownloadClientItemClientInfo.cs`. HasPostImportCategory is true
// when settings.TvImportedCategory is non-empty per C# `:145`.
func (d *Deluge) itemClientInfo() *downloadclients.ItemClientInfo {
	info := &downloadclients.ItemClientInfo{
		Protocol:              d.Protocol(),
		Type:                  Implementation,
		Name:                  d.Name(),
		HasPostImportCategory: strings.TrimSpace(d.settings.TvImportedCategory) != "",
	}
	if d.def != nil {
		info.ID = d.def.ID
		info.Name = d.def.Name
		info.RemoveCompletedDownloads = d.def.RemoveCompletedDownloads
	}
	return info
}

// joinTorrentPath joins deluge's save_path with the torrent name.
// Deluge always emits '/' separators in its API responses (matches
// the qBittorrent family pattern from 5.G1d).
func joinTorrentPath(base, name string) string {
	if base == "" {
		return name
	}
	if name == "" {
		return base
	}
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(name, "/")
}

// GetImportItem mirrors the C# base default — pass input through.
// `DownloadClientBase.cs:102-105`.
func (d *Deluge) GetImportItem(_ context.Context, item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

// RemoveItem ports C# `Deluge.RemoveItem` at
// `Download/Clients/Deluge/Deluge.cs:217-220`. Lowercases the
// DownloadID (matches C# `:219` `.ToLower()`) and delegates to
// [Proxy.RemoveTorrent].
func (d *Deluge) RemoveItem(ctx context.Context, item *downloadclients.Item, deleteData bool) error {
	if item == nil {
		return errors.New("deluge: RemoveItem: nil item")
	}
	if d.proxy == nil {
		return errors.New("deluge: RemoveItem: nil proxy")
	}
	hash := strings.ToLower(item.DownloadID)
	if err := d.proxy.RemoveTorrent(ctx, hash, deleteData, d.settings); err != nil {
		return fmt.Errorf("deluge: RemoveItem(%s): %w", hash, err)
	}
	return nil
}

// GetStatus ports C# `Deluge.GetStatus` at
// `Download/Clients/Deluge/Deluge.cs:222-262`. Computes the
// effective output directory via a 4-way precedence:
//
//  1. settings.CompletedDirectory (explicit user override on the
//     DownloadClient row) — wins outright.
//  2. settings.DownloadDirectory (explicit override of the
//     pre-completion download path).
//  3. Per-label `move_completed_path` when TvCategory is set AND
//     the label's ApplyMoveCompleted AND MoveCompleted are both
//     true.
//  4. Global preferences: when `move_completed=true`, fall back to
//     `move_completed_path`; else `download_location`.
//
// The label-arm RPC failure (label plugin missing, label not
// found) silently falls through to the config arm — matches the C#
// `label is { ApplyMoveCompleted: true, MoveCompleted: true }` arm
// at `:237-241` which evaluates to false on null label.
//
// IsLocalhost routes through [downloadclients.IsLocalhostHost].
// The C# arm at `:253` is literal `Host == "127.0.0.1" || Host ==
// "localhost"`; the Go port (Phase 5.J175) widens via the shared
// helper to case-insensitive + whitespace-trim + IPv6 loopback
// (`::1`), strictly a superset of the C# behaviour.
//
// RemovesCompletedDownloads is left false — the C# tree doesn't set
// this field for Deluge, matching the absence of a global
// remove-on-completion preference in deluge.
//
// The remote-path-mapping translation at C# `:258` is wired via
// [Deluge.SetRemotePathMapper] (Phase 6.B) — the resolved destDir
// flows through `remapOutput` so off-host Deluge instances surface
// a Sonarr-side path. Nil-mapper falls through to the raw destDir.
func (d *Deluge) GetStatus(ctx context.Context) (*downloadclients.ClientInfo, error) {
	if d.proxy == nil {
		return nil, errors.New("deluge: GetStatus: nil proxy")
	}

	destDir, err := d.resolveDestDir(ctx)
	if err != nil {
		return nil, fmt.Errorf("deluge: GetStatus: %w", err)
	}

	info := &downloadclients.ClientInfo{
		IsLocalhost: downloadclients.IsLocalhostHost(d.settings.Host),
	}
	if strings.TrimSpace(destDir) != "" {
		info.OutputRootFolders = []ospath.OsPath{d.remapOutput(ctx, destDir)}
	}
	return info, nil
}

// resolveDestDir walks the 4-way precedence ladder for the output
// directory. Returns "" when no arm produces a usable path (matches
// the C# behaviour at `:256` — empty destDir surfaces an empty
// OutputRootFolders).
func (d *Deluge) resolveDestDir(ctx context.Context) (string, error) {
	if cd := strings.TrimSpace(d.settings.CompletedDirectory); cd != "" {
		return cd, nil
	}
	if dd := strings.TrimSpace(d.settings.DownloadDirectory); dd != "" {
		return dd, nil
	}

	// Label arm — only when TvCategory is set. RPC failure on
	// label.get_options (plugin missing / label not found) falls
	// through to the config arm rather than propagating; the v3
	// UI's path-mapping helper degrades gracefully without a
	// destDir.
	if cat := strings.TrimSpace(d.settings.TvCategory); cat != "" {
		label, labelErr := d.proxy.GetLabelOptions(ctx, cat, d.settings)
		if labelErr == nil && label != nil && label.ApplyMoveCompleted && label.MoveCompleted {
			if mp := strings.TrimSpace(label.MoveCompletedPath); mp != "" {
				return mp, nil
			}
		}
	}

	// Config arm.
	cfg, err := d.proxy.GetConfig(ctx, d.settings)
	if err != nil {
		return "", err
	}
	if mc, _ := cfg["move_completed"].(bool); mc {
		if mp, _ := cfg["move_completed_path"].(string); strings.TrimSpace(mp) != "" {
			return mp, nil
		}
	}
	if dl, _ := cfg["download_location"].(string); strings.TrimSpace(dl) != "" {
		return dl, nil
	}
	return "", nil
}

// MarkItemAsImported ports C# `Deluge.MarkItemAsImported` at
// `Download/Clients/Deluge/Deluge.cs:39-56`. When the user has
// configured a post-import category distinct from TvCategory, swaps
// the torrent's deluge label to it via label.set_torrent.
//
// Failure swallowed (slog.Warn + return nil): the C# catch arm at
// `:49-54` catches DownloadClientUnavailableException and warns
// "Failed to set torrent post-import label" without propagating —
// the import path has already moved files, so a propagated error
// would block subsequent imports without recovering anything. The
// Go port matches.
func (d *Deluge) MarkItemAsImported(ctx context.Context, item *downloadclients.Item) error {
	if item == nil {
		return errors.New("deluge: MarkItemAsImported: nil item")
	}
	if d.proxy == nil {
		return errors.New("deluge: MarkItemAsImported: nil proxy")
	}
	imported := strings.TrimSpace(d.settings.TvImportedCategory)
	current := strings.TrimSpace(d.settings.TvCategory)
	if imported == "" || imported == current {
		return nil
	}
	hash := strings.ToLower(item.DownloadID)
	if err := d.proxy.SetTorrentLabel(ctx, hash, imported, d.settings); err != nil {
		d.log.Warn("deluge: Failed to set torrent post-import label",
			"hash", hash, "label", imported, "err", err)
		return nil
	}
	return nil
}

// Test ports the connectivity + auth probe from C#
// `Deluge.TestConnection` at
// `src/NzbDrone.Core/Download/Clients/Deluge/Deluge.cs:276-323`.
//
// Flow:
//
//  1. Run Settings.Validate first — structural failures
//     short-circuit so a malformed config never triggers a network
//     round-trip.
//  2. Ensure the proxy is bound. Nil proxy is a programmer-error
//     path (non-nil error vs TestFailure) — the registry
//     constructor guarantees a non-nil proxy in production.
//  3. proxy.EnsureAuth(force=true) → field-level TestFailures pinned
//     per C# branches at `:282-319`: ErrAuthFailed → Password,
//     ErrUnavailable → Host (matches `:288-294`).
//  4. Probe daemon.info via proxy.Call → connectivity + version
//     surface. The C# TestConnection at `:280` calls
//     `_proxy.GetVersion(Settings)` which falls back from
//     daemon.get_version to daemon.info per `DelugeProxy.cs:52-65`
//     — the Go port simplifies to a direct daemon.info probe since
//     daemon.info is present in every Deluge version that ships
//     the WebUI JSON-RPC (1.3.6+).
//
// No version floor enforced — the C# tree's TestConnection doesn't
// check version numerics, just connectivity + auth. Matches.
//
// TestCategory + TestGetTorrents at C# `:325-385` wired at slice
// 5.J101 — both layer on top of the daemon.info probe by issuing
// (a) system.listMethods + label.get_labels for the label-plugin
// probe with TvCategory/TvImportedCategory auto-create fallback,
// and (b) web.update_ui to confirm the GetItems query works against
// the live daemon. See [Deluge.testCategory] + [Deluge.testGetTorrents]
// for the per-probe doc-comments.
func (d *Deluge) Test(ctx context.Context) ([]downloadclients.TestFailure, error) {
	if failures := d.settings.Validate(); len(failures) > 0 {
		return failures, nil
	}
	if d.proxy == nil {
		return nil, errors.New("deluge: Test: nil proxy (constructor not wired)")
	}

	if _, err := d.proxy.EnsureAuth(ctx, d.settings, true); err != nil {
		if failure := classifyAuthError(err); failure != nil {
			return []downloadclients.TestFailure{*failure}, nil
		}
		return nil, err
	}

	raw, err := d.proxy.Call(ctx, "daemon.info", nil, d.settings)
	if err != nil {
		if failure := classifyAuthError(err); failure != nil {
			return []downloadclients.TestFailure{*failure}, nil
		}
		return nil, err
	}

	var version string
	if err := json.Unmarshal(raw, &version); err != nil {
		return []downloadclients.TestFailure{{
			PropertyName: "Version",
			ErrorMessage: fmt.Sprintf("Unable to parse Deluge version response: %v", err),
		}}, nil
	}
	if strings.TrimSpace(version) == "" {
		return []downloadclients.TestFailure{{
			PropertyName: "Version",
			ErrorMessage: "Deluge returned an empty version string",
		}}, nil
	}

	var failures []downloadclients.TestFailure
	if f := d.testCategory(ctx); f != nil {
		failures = append(failures, *f)
	}
	if f := d.testGetTorrents(ctx); f != nil {
		failures = append(failures, *f)
	}
	return failures, nil
}

// testCategory ports C# `Deluge.TestCategory` at `:325-373`.
//
// Algorithm:
//  1. Both TvCategory + TvImportedCategory empty → no probe needed,
//     return nil. Matches C# `:327-330`.
//  2. system.listMethods — confirm the `label.*` plugin RPCs are
//     registered. Missing → "label plugin inactive" failure on
//     TvCategory (C# `:332-340`). The C# message is i18n; the Go
//     port surfaces the English equivalent.
//  3. label.get_labels — fetch the current label set.
//  4. For each configured category that's missing, fire label.add
//     then re-fetch labels. Still missing → "label plugin failure"
//     failure on that property (C# `:344-370`).
//
// RPC errors at any step surface as the corresponding failure
// (transient errors at the label-add boundary mean the plugin is
// installed but uncooperative — same user-facing message).
func (d *Deluge) testCategory(ctx context.Context) *downloadclients.TestFailure {
	tv := strings.TrimSpace(d.settings.TvCategory)
	tvImported := strings.TrimSpace(d.settings.TvImportedCategory)
	if tv == "" && tvImported == "" {
		return nil
	}
	methods, err := d.proxy.GetMethods(ctx, d.settings)
	if err != nil {
		return &downloadclients.TestFailure{
			PropertyName:        "TvCategory",
			ErrorMessage:        "Unable to determine whether the Deluge label plugin is active",
			DetailedDescription: err.Error(),
		}
	}
	hasLabelPlugin := false
	for _, m := range methods {
		if strings.HasPrefix(m, "label.") {
			hasLabelPlugin = true
			break
		}
	}
	if !hasLabelPlugin {
		return &downloadclients.TestFailure{
			PropertyName:        "TvCategory",
			ErrorMessage:        "Label plugin is not active",
			DetailedDescription: fmt.Sprintf("Sonarr's Deluge integration requires Deluge's Label plugin to be enabled to assign the %q category.", tv),
		}
	}

	labels, err := d.proxy.GetAvailableLabels(ctx, d.settings)
	if err != nil {
		return &downloadclients.TestFailure{
			PropertyName:        "TvCategory",
			ErrorMessage:        "Unable to fetch Deluge labels",
			DetailedDescription: err.Error(),
		}
	}
	labelSet := make(map[string]struct{}, len(labels))
	for _, l := range labels {
		labelSet[l] = struct{}{}
	}

	if f := d.ensureLabel(ctx, tv, "TvCategory", labelSet); f != nil {
		return f
	}
	if f := d.ensureLabel(ctx, tvImported, "TvImportedCategory", labelSet); f != nil {
		return f
	}
	return nil
}

// ensureLabel ports the C# label-auto-create arm at `:344-356,358-370`.
// Empty label → noop. Present → noop. Missing → label.add + re-fetch;
// still-missing surfaces a "label plugin failure" failure on the
// configured property. Add-error short-circuits with the same
// failure message so a failed add is indistinguishable from a
// post-add re-fetch miss (matches C# which catches the AddLabel
// error implicitly by checking re-fetched labels).
func (d *Deluge) ensureLabel(ctx context.Context, label, prop string, labelSet map[string]struct{}) *downloadclients.TestFailure {
	if label == "" {
		return nil
	}
	if _, ok := labelSet[label]; ok {
		return nil
	}
	if err := d.proxy.AddLabel(ctx, label, d.settings); err != nil {
		return &downloadclients.TestFailure{
			PropertyName:        prop,
			ErrorMessage:        "Failed to add the configured label to Deluge",
			DetailedDescription: err.Error(),
		}
	}
	refreshed, err := d.proxy.GetAvailableLabels(ctx, d.settings)
	if err != nil {
		return &downloadclients.TestFailure{
			PropertyName:        prop,
			ErrorMessage:        "Unable to re-fetch Deluge labels after add",
			DetailedDescription: err.Error(),
		}
	}
	for _, l := range refreshed {
		labelSet[l] = struct{}{}
	}
	if _, ok := labelSet[label]; ok {
		return nil
	}
	return &downloadclients.TestFailure{
		PropertyName:        prop,
		ErrorMessage:        "Failed to add the configured label to Deluge",
		DetailedDescription: fmt.Sprintf("Deluge accepted the label.add RPC for %q but the label is still missing from label.get_labels. The label plugin may be misconfigured.", label),
	}
}

// testGetTorrents ports C# `Deluge.TestGetTorrents` at `:375-388`.
// Confirms the live daemon answers a GetTorrents (web.update_ui)
// request; surfaces a whole-form failure on any error so the user
// learns the connection works at the WebUI level but not the RPC.
func (d *Deluge) testGetTorrents(ctx context.Context) *downloadclients.TestFailure {
	if _, err := d.proxy.GetTorrents(ctx, d.settings); err != nil {
		return &downloadclients.TestFailure{
			PropertyName:        "",
			ErrorMessage:        fmt.Sprintf("Failed to get the list of torrents: %v", err),
			DetailedDescription: "Sonarr was able to authenticate against Deluge but the web.update_ui RPC failed; check the Deluge log.",
		}
	}
	return nil
}

// classifyAuthError converts a proxy auth/transport error to a
// field-level TestFailure when the error is one of the typed arms.
// Returns nil for other errors so the caller surfaces them as an
// error return.
//
// Pinning mirrors C# `Deluge.TestConnection` at `:282-319`:
// authentication failure → "Password", transport unavailability →
// "Host" with detailed description.
func classifyAuthError(err error) *downloadclients.TestFailure {
	switch {
	case errors.Is(err, ErrAuthFailed):
		return &downloadclients.TestFailure{
			PropertyName:        "Password",
			ErrorMessage:        "Authentication failed",
			DetailedDescription: err.Error(),
		}
	case errors.Is(err, ErrUnavailable):
		return &downloadclients.TestFailure{
			PropertyName:        "Host",
			ErrorMessage:        "Unable to connect to Deluge",
			DetailedDescription: err.Error(),
		}
	}
	return nil
}

// RequestAction is the per-impl action map. The Deluge C# tree has
// no concrete actions — returns (nil, nil) per the C# default at
// `DownloadClientBase.cs:165-167`.
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

	// Build the proxy when an HTTPDoer is available. Tests
	// constructing through the registry without a doer (e.g. the
	// 5.G2a round-trip suite) get a nil proxy + a Provider that
	// surfaces programmer-error returns from Test(); production
	// wiring always supplies one.
	var proxy *Proxy
	if deps.HTTPClient != nil {
		doer, ok := deps.HTTPClient.(HTTPDoer)
		if !ok {
			return nil, fmt.Errorf("deluge: deps.HTTPClient (%T) does not satisfy HTTPDoer", deps.HTTPClient)
		}
		var err error
		proxy, err = NewProxy(doer, log)
		if err != nil {
			return nil, err
		}
	}

	dlg := New(def, settings, proxy, log)
	// Slice 6.B6: cast the typed-as-any RemotePathMappingService to
	// the narrow RemotePathMapper interface. nil dep / cast miss
	// leaves dlg.rpm nil; per-call sites fall through to the raw
	// RPC-side path.
	if rpm, ok := deps.RemotePathMappingService.(downloadclients.RemotePathMapper); ok {
		dlg.SetRemotePathMapper(rpm)
	}
	return dlg, nil
}

// init registers Deluge with the global registry.
func init() {
	downloadclients.Default().MustRegister(Implementation, ConfigContract, constructor)
}
