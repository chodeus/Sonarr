package aria2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/ospath"
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

	// disk is the optional [downloadclients.DiskProvider] consumed by
	// RemoveItem's deleteData arm to call DeleteFolder against
	// item.OutputPath. aria2 itself doesn't support file deletion
	// (issue #728), so the C# tree's DeleteItemData arm at
	// `Aria2.cs:185-188` reaches into the shared DiskProvider for
	// the on-disk cleanup. Slice 5.J112 wire-up mirrors the J111
	// rTorrent + NZBGet 6.C3 pattern.
	disk downloadclients.DiskProvider
}

// minVersion is the aria2c version floor [Test] enforces. Mirrors
// C# `Aria2.cs:237` (`new Version("1.34.0")`) — the floor was bumped
// to 1.34 in the C# tree because some BitTorrent-specific RPCs the
// orchestrator's GetItems leans on (aria2.tellActive's full
// bittorrent struct, files[].path canonicalisation) only stabilise
// at that release.
var minVersion = versionPair{major: 1, minor: 34}

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

// GetItems ports `Aria2.GetItems` at `Aria2.cs:76-150`. Fires the
// 3-call fanout (tellActive + tellWaiting + tellStopped), drops
// metadata-only torrents + "removed" status, projects each
// AriaStatus into a downloadclients.Item with the C# status
// mapping:
//
//   - "active" + completed==total → Completed.
//   - "active" otherwise          → Downloading.
//   - "waiting"                   → Queued.
//   - "paused"                    → Paused.
//   - "error"                     → Failed.
//   - "complete"                  → Completed.
//   - anything else               → Failed (defensive).
//
// CanMoveFiles = false (aria2 doesn't support file deletion per
// https://github.com/aria2/aria2/issues/728 — matches C# `:140-142`
// posture). CanBeRemoved = true only when the torrent is in
// "complete" status (matches C# `:147`).
func (a *Aria2) GetItems(ctx context.Context) ([]*downloadclients.Item, error) {
	if a.proxy == nil {
		return nil, errors.New("aria2: GetItems requires a wired Proxy")
	}
	torrents, err := a.proxy.GetTorrents(ctx, a.settings)
	if err != nil {
		return nil, err
	}
	out := make([]*downloadclients.Item, 0, len(torrents))
	for _, t := range torrents {
		// Skip metadata-only downloads (file path contains [METADATA])
		// + already-removed torrents.
		if t.Status == "removed" {
			continue
		}
		if len(t.FilePaths) > 0 && strings.Contains(t.FilePaths[0], "[METADATA]") {
			continue
		}

		totalLength := parseInt64(t.TotalLength)
		completedLength := parseInt64(t.CompletedLength)
		uploadLength := parseInt64(t.UploadLength)
		downloadSpeed := parseInt64(t.DownloadSpeed)

		status := downloadclients.DownloadItemStatusFailed
		switch t.Status {
		case "active":
			if completedLength == totalLength && totalLength > 0 {
				status = downloadclients.DownloadItemStatusCompleted
			} else {
				status = downloadclients.DownloadItemStatusDownloading
			}
		case "waiting":
			status = downloadclients.DownloadItemStatusQueued
		case "paused":
			status = downloadclients.DownloadItemStatusPaused
		case "error":
			status = downloadclients.DownloadItemStatusFailed
		case "complete":
			status = downloadclients.DownloadItemStatusCompleted
		}

		item := &downloadclients.Item{
			DownloadID:    strings.ToUpper(t.InfoHash),
			Title:         t.BittorrentName,
			TotalSize:     totalLength,
			RemainingSize: totalLength - completedLength,
			Status:        status,
			Message:       t.ErrorMessage,
			CanMoveFiles:  false,
			CanBeRemoved:  t.Status == "complete",
		}
		if downloadSpeed > 0 && totalLength-completedLength > 0 {
			eta := time.Duration((totalLength-completedLength)/downloadSpeed) * time.Second
			item.RemainingTime = &eta
		}
		if totalLength > 0 {
			ratio := float64(uploadLength) / float64(totalLength)
			item.SeedRatio = &ratio
		}
		if outputPath := outputPathFor(t); outputPath != "" {
			item.OutputPath = ospath.New(outputPath)
		}

		out = append(out, item)
	}
	return out, nil
}

// outputPathFor returns the OutputPath for a torrent. C#
// `GetOutputPath` at `:260-268`: single-file torrent → that file's
// path; multi-file → longest-common-prefix across all file paths.
func outputPathFor(t AriaStatus) string {
	switch len(t.FilePaths) {
	case 0:
		return ""
	case 1:
		return t.FilePaths[0]
	default:
		return longestCommonPath(t.FilePaths)
	}
}

// longestCommonPath returns the longest path prefix shared by all
// inputs, truncated at the last `/` so the result is a directory
// path. Empty input → empty string. Mirrors C# extension
// `GetLongestCommonPath` (Common.Extensions).
func longestCommonPath(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	if len(paths) == 1 {
		return paths[0]
	}
	prefix := paths[0]
	for _, p := range paths[1:] {
		// Trim prefix from the right until it's a prefix of p.
		for !strings.HasPrefix(p, prefix) {
			if i := strings.LastIndex(prefix, "/"); i >= 0 {
				prefix = prefix[:i]
			} else {
				return ""
			}
		}
	}
	return prefix
}

// parseInt64 parses a string to int64 — aria2's numeric fields
// arrive as strings on the wire. Returns 0 on parse failure
// (matches the C# `long.Parse` posture which throws on bad input;
// the Go-port silently defaults to keep the GetItems loop
// non-fatal).
func parseInt64(s string) int64 {
	if strings.TrimSpace(s) == "" {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// GetImportItem passes through unchanged.
func (a *Aria2) GetImportItem(_ context.Context, item *downloadclients.Item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

// RemoveItem ports `Aria2.RemoveItem` at `:152-189`. aria2 doesn't
// support file deletion via its own API (issue #728), so the
// deleteData arm calls [downloadclients.DiskProvider.DeleteFolder]
// AFTER the aria2-side removal (matches C# `:185-188` ordering
// where DeleteItemData runs as the last step).
//
// The implementation looks up the torrent by InfoHash to get the
// GID, then dispatches to forceRemove (for active downloads) or
// removeDownloadResult (for complete/error/removed). Nil disk-provider
// OR empty OutputPath degrades the deleteData arm to a warn log —
// the aria2-side removal still runs, files just stay on disk
// (same posture as NZBGet/rTorrent).
//
// Returns nil + warn-log when the torrent isn't found (matches C#
// `:160-163` posture — log + return, don't surface as fatal).
func (a *Aria2) RemoveItem(ctx context.Context, item *downloadclients.Item, deleteData bool) error {
	if a.proxy == nil {
		return errors.New("aria2: RemoveItem requires a wired Proxy")
	}
	if item == nil {
		return errors.New("aria2: RemoveItem: nil Item")
	}
	hash := strings.ToLower(strings.TrimSpace(item.DownloadID))
	if hash == "" {
		return errors.New("aria2: RemoveItem: empty DownloadID")
	}
	torrents, err := a.proxy.GetTorrents(ctx, a.settings)
	if err != nil {
		return err
	}
	var match *AriaStatus
	for i, t := range torrents {
		if strings.EqualFold(t.InfoHash, hash) {
			match = &torrents[i]
			break
		}
	}
	if match == nil {
		a.log.Warn("aria2: could not find infoHash for removal",
			slog.String("hash", hash))
		return nil
	}
	if match.Status == "complete" || match.Status == "error" || match.Status == "removed" {
		if err := a.proxy.RemoveCompletedTorrent(ctx, match.GID, a.settings); err != nil {
			return err
		}
	} else {
		if err := a.proxy.RemoveTorrent(ctx, match.GID, a.settings); err != nil {
			return err
		}
	}
	if deleteData {
		switch {
		case a.disk == nil:
			a.log.Warn("aria2: RemoveItem: deleteData=true requested but no DiskProvider is wired; the torrent is removed from aria2 but files remain on disk",
				slog.String("hash", hash))
		case item.OutputPath.IsEmpty():
			a.log.Debug("aria2: RemoveItem: deleteData=true but item.OutputPath is empty; skipping on-disk delete",
				slog.String("hash", hash))
		default:
			if err := a.disk.DeleteFolder(item.OutputPath.FullPath(), true); err != nil {
				a.log.Warn("aria2: RemoveItem: DiskProvider.DeleteFolder failed; the torrent is still removed from aria2",
					slog.String("hash", hash),
					slog.String("path", item.OutputPath.FullPath()),
					slog.Any("err", err))
			}
		}
	}
	return nil
}

// SetDiskProvider binds the optional [downloadclients.DiskProvider]
// the RemoveItem deleteData arm consults. Nil-binding leaves the
// arm in its pre-J112 warn-log state.
// Slice 5.J112 — same pattern as J111 rTorrent + NZBGet 6.C3.
func (a *Aria2) SetDiskProvider(d downloadclients.DiskProvider) *Aria2 {
	a.disk = d
	return a
}

// GetStatus ports `Aria2.GetStatus` at `:191-198`. Returns
// IsLocalhost (host contains "127.0.0.1" or "localhost" — C# uses
// `Contains` so we mirror) + OutputRootFolders sourced from the
// global `dir` option (aria2.getGlobalOption).
func (a *Aria2) GetStatus(ctx context.Context) (*downloadclients.ClientInfo, error) {
	if a.proxy == nil {
		return nil, errors.New("aria2: GetStatus requires a wired Proxy")
	}
	host := a.settings.Host
	info := &downloadclients.ClientInfo{
		IsLocalhost: strings.Contains(host, "127.0.0.1") || strings.Contains(host, "localhost"),
	}
	globals, err := a.proxy.GetGlobals(ctx, a.settings)
	if err != nil {
		return nil, err
	}
	if dir, ok := globals["dir"]; ok && dir != "" {
		info.OutputRootFolders = []ospath.OsPath{ospath.New(dir)}
	}
	return info, nil
}

// MarkItemAsImported isn't supported by aria2 — the C# base class
// `TorrentClientBase.MarkItemAsImported` throws NotImplementedException;
// aria2 doesn't override. Mirrors the Go-port pattern with
// [downloadclients.ErrMarkAsImportedUnsupported].
func (a *Aria2) MarkItemAsImported(_ context.Context, _ *downloadclients.Item) error {
	return downloadclients.ErrMarkAsImportedUnsupported
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
			ErrorMessage: fmt.Sprintf("Aria2 version %s is too old; minimum supported is %d.%d.0", version, minVersion.major, minVersion.minor),
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
	a := New(def, settings, proxy, log)
	if disk, ok := deps.DiskProvider.(downloadclients.DiskProvider); ok {
		a.SetDiskProvider(disk)
	}
	return a, nil
}

// init registers Aria2 with the global registry.
func init() {
	downloadclients.Default().MustRegister(Implementation, ConfigContract, constructor)
}
