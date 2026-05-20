package blackhole

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/organizer"
	"sonarr.tv/sonarr/internal/ospath"
)

// TorrentBlackhole is the [downloadclients.Provider] implementation
// for the C# `TorrentBlackhole` client at
// `src/NzbDrone.Core/Download/Clients/Blackhole/TorrentBlackhole.cs:20-138`.
//
// Persists a download to disk: `AddFromTorrentFile` writes the
// .torrent bytes to `<TorrentFolder>/<CleanFileName(title)>.torrent`;
// `AddFromMagnetLink` (when SaveMagnetFiles) writes the magnet URL
// to `<TorrentFolder>/<CleanFileName(title)>.<MagnetFileExtension>`.
type TorrentBlackhole struct {
	settings TorrentSettings
	def      *downloadclients.DownloadClient
	log      *slog.Logger

	// fs is the filesystem-write surface — exposed for tests so the
	// write side is observable without an actual disk write. Nil →
	// the default os.WriteFile / os.Stat-based implementation is
	// used. Production binds nil.
	fs FS
}

// FS is the narrow filesystem surface [TorrentBlackhole] consumes.
// Two operations:
//
//   - [FS.WriteFile] writes the magnet/torrent body to disk.
//   - [FS.IsDir] reports whether a path exists and is a directory
//     (used by Test).
//
// Production binds the [osFS] default which delegates to
// `os.WriteFile` / `os.Stat`; tests inject a fake to assert the
// write side without touching real disk.
type FS interface {
	WriteFile(path string, body []byte, perm os.FileMode) error
	IsDir(path string) (bool, error)
	RemoveAll(path string) error
}

// osFS is the default filesystem implementation. Delegates to
// `os.WriteFile` + `os.Stat` + `os.RemoveAll`. Mode 0o644 matches
// the C# `IDiskProvider.OpenWriteStream` default at
// `Disk/DiskProviderBase.cs:189-195`.
type osFS struct{}

// WriteFile writes body to path with the given perm. Creates any
// missing parent directories with mode 0o755 (the C# code at
// `OpenWriteStream` opens with FileMode.Create which does NOT
// create parents — the C# operator manually creates the folder
// before configuring the client; this Go-port behaviour is
// strictly more permissive but matches Sonarr's "first run cleans
// up" intent).
func (osFS) WriteFile(path string, body []byte, perm os.FileMode) error {
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	return os.WriteFile(path, body, perm)
}

// IsDir reports whether path exists and is a directory. Returns
// (false, nil) for "does not exist"; returns (false, err) for any
// other stat failure.
func (osFS) IsDir(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return info.IsDir(), nil
}

// RemoveAll removes path and everything under it. Mirrors the C#
// `DiskProvider.DeleteFolder(path, recursive: true)` arm of
// `RemoveItem` when `deleteData=true`.
func (osFS) RemoveAll(path string) error { return os.RemoveAll(path) }

// New constructs a TorrentBlackhole Provider against the resolved
// settings + an optional [FS] override (nil → [osFS]).
func New(def *downloadclients.DownloadClient, settings TorrentSettings, fs FS, log *slog.Logger) *TorrentBlackhole {
	if log == nil {
		log = slog.Default()
	}
	if fs == nil {
		fs = osFS{}
	}
	return &TorrentBlackhole{settings: settings, def: def, log: log, fs: fs}
}

// Name returns the user-facing label. Mirrors C# `Name` at
// `TorrentBlackhole.cs:86` (`localizationService.GetLocalizedString
// ("TorrentBlackhole")`). The Go port returns the literal English
// label — localization is plumbed at the API edge, not the
// Provider edge.
func (t *TorrentBlackhole) Name() string { return "Torrent Blackhole" }

// Protocol returns the protocol family. TorrentBlackhole inherits
// from C# `TorrentClientBase` → Torrent.
func (t *TorrentBlackhole) Protocol() indexers.DownloadProtocol {
	return indexers.DownloadProtocolTorrent
}

// Definition returns the persisted DownloadClient row. Nil for
// test-constructed providers.
func (t *TorrentBlackhole) Definition() *downloadclients.DownloadClient { return t.def }

// Settings returns the bound TorrentSettings. Exposed for tests
// that need to assert against the round-tripped JSON without
// re-decoding.
func (t *TorrentBlackhole) Settings() TorrentSettings { return t.settings }

// Download ports `TorrentBlackhole.AddFromMagnetLink` +
// `AddFromTorrentFile` at C# `:44-84`. Picks the add path:
//
//   - GrabRequest.TorrentBody bytes set → AddFromTorrentFile
//     writes `<TorrentFolder>/<title>.torrent`.
//   - GrabRequest.MagnetURL set → AddFromMagnetLink writes
//     `<TorrentFolder>/<title>.<MagnetFileExtension>` when
//     SaveMagnetFiles=true; otherwise returns ErrMagnetNotSupported.
//
// Returns "" for the DownloadID (matches the C# `return null` at
// `:65, :83`). The Blackhole client doesn't know the per-client
// download ID — the ScanWatchFolder follow-up resolves a synthetic
// hash from the file contents when GetItems lists the queue.
func (t *TorrentBlackhole) Download(_ context.Context, req *downloadclients.GrabRequest) (string, error) {
	if req == nil {
		return "", errors.New("blackhole: nil GrabRequest")
	}
	if strings.TrimSpace(req.ReleaseTitle) == "" {
		return "", errors.New("blackhole: GrabRequest.ReleaseTitle is empty")
	}
	title := organizer.CleanFileNameDefault(req.ReleaseTitle)

	switch {
	case len(req.TorrentBody) > 0:
		path := filepath.Join(t.settings.TorrentFolder, title+".torrent")
		if err := t.fs.WriteFile(path, req.TorrentBody, 0o644); err != nil {
			return "", fmt.Errorf("blackhole: write torrent file: %w", err)
		}
		t.log.Debug("blackhole: torrent download succeeded", "path", path)
		return "", nil

	case strings.TrimSpace(req.MagnetURL) != "":
		if !t.settings.SaveMagnetFiles {
			return "", ErrMagnetNotSupported
		}
		ext := strings.TrimLeft(strings.TrimSpace(t.settings.MagnetFileExtension), ".")
		if ext == "" {
			ext = "magnet"
		}
		path := filepath.Join(t.settings.TorrentFolder, title+"."+ext)
		if err := t.fs.WriteFile(path, []byte(req.MagnetURL), 0o644); err != nil {
			return "", fmt.Errorf("blackhole: write magnet file: %w", err)
		}
		t.log.Debug("blackhole: magnet link saved", "path", path)
		return "", nil

	default:
		return "", errors.New("blackhole: GrabRequest has neither TorrentBody nor MagnetURL")
	}
}

// GetItems ports `TorrentBlackhole.GetItems` at C# `:88-112`.
//
// Divergence (5.J57): the C# code walks `_scanWatchFolder.GetItems
// (Settings.WatchFolder, ScanGracePeriod)` and yields the file-
// system-derived items. The ScanWatchFolder service (C#
// `ScanWatchFolder.cs:1-202`) is ~200 LOC of disk-scanning,
// file-locking detection, hashing, 30s grace-period gating, and
// 5-min cache TTL — out of scope for the package-skeleton slice.
// Until ScanWatchFolder ports, GetItems returns an empty slice
// (never nil). The queue UI shows no in-flight Blackhole items;
// the manual-import flow still discovers completed downloads by
// walking the watch folder directly.
func (t *TorrentBlackhole) GetItems(_ context.Context) ([]*downloadclients.Item, error) {
	return []*downloadclients.Item{}, nil
}

// GetImportItem is the C# base passthrough (see
// `DownloadClientBase.cs:102-105`).
func (t *TorrentBlackhole) GetImportItem(_ context.Context, item *downloadclients.Item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

// RemoveItem ports `TorrentBlackhole.RemoveItem` at C# `:114-122`.
//
// Blackhole has no client-side queue — RemoveItem deletes the
// on-disk folder when deleteData=true and returns
// ErrRemoveWithoutDelete otherwise (matching the C#
// NotSupportedException at `:118`).
func (t *TorrentBlackhole) RemoveItem(_ context.Context, item *downloadclients.Item, deleteData bool) error {
	if !deleteData {
		return ErrRemoveWithoutDelete
	}
	if item == nil {
		return errors.New("blackhole: nil item")
	}
	path := item.OutputPath.FullPath()
	if strings.TrimSpace(path) == "" {
		return errors.New("blackhole: item has empty OutputPath")
	}
	if err := t.fs.RemoveAll(path); err != nil {
		return fmt.Errorf("blackhole: remove item data: %w", err)
	}
	return nil
}

// GetStatus ports `TorrentBlackhole.GetStatus` at C# `:124-131`.
// Always IsLocalhost=true (Blackhole is disk-only — there's no
// concept of a remote endpoint). OutputRootFolders is the single-
// element list `[WatchFolder]`.
func (t *TorrentBlackhole) GetStatus(_ context.Context) (*downloadclients.ClientInfo, error) {
	info := &downloadclients.ClientInfo{
		IsLocalhost: true,
	}
	if strings.TrimSpace(t.settings.WatchFolder) != "" {
		info.OutputRootFolders = append(info.OutputRootFolders, ospath.New(t.settings.WatchFolder))
	}
	return info, nil
}

// MarkItemAsImported returns ErrMarkAsImportedUnsupported.
// Blackhole has no client-side queue concept (matches C# base
// `DownloadClientBase.cs:189-192` default).
func (t *TorrentBlackhole) MarkItemAsImported(_ context.Context, _ *downloadclients.Item) error {
	return downloadclients.ErrMarkAsImportedUnsupported
}

// Test ports `TorrentBlackhole.Test(failures)` at C# `:133-137`.
// Validates that TorrentFolder + WatchFolder both exist on disk.
//
// Settings.Validate runs first (mirrors the
// FluentValidation-before-Test contract C# uses); the disk probe
// runs only when no Settings failures.
func (t *TorrentBlackhole) Test(_ context.Context) ([]downloadclients.TestFailure, error) {
	failures := t.settings.Validate()
	if len(failures) > 0 {
		return failures, nil
	}
	failures = append(failures, testFolder(t.fs, t.settings.TorrentFolder, "TorrentFolder")...)
	if strings.TrimSpace(t.settings.WatchFolder) != "" {
		failures = append(failures, testFolder(t.fs, t.settings.WatchFolder, "WatchFolder")...)
	} else {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "WatchFolder",
			ErrorMessage: "WatchFolder is required",
		})
	}
	return failures, nil
}

// testFolder mirrors C# `TestFolder(path, propertyName)` at
// `DownloadClientBase.cs:170-184` — returns a single
// folder-missing failure (or empty when the folder exists).
func testFolder(fs FS, path, propertyName string) []downloadclients.TestFailure {
	ok, err := fs.IsDir(path)
	if err != nil {
		return []downloadclients.TestFailure{{
			PropertyName: propertyName,
			ErrorMessage: fmt.Sprintf("Folder check failed: %v", err),
		}}
	}
	if !ok {
		return []downloadclients.TestFailure{{
			PropertyName: propertyName,
			ErrorMessage: fmt.Sprintf("Folder does not exist: %s", path),
		}}
	}
	return nil
}

// RequestAction is the C# base no-op (see `DownloadClientBase.cs:165-167`).
func (t *TorrentBlackhole) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// ErrMagnetNotSupported is returned by Download when the request
// has only a MagnetURL but [TorrentSettings.SaveMagnetFiles] is
// false. Mirrors C# `NotSupportedException("Blackhole does not
// support magnet links.")` at `TorrentBlackhole.cs:48`.
var ErrMagnetNotSupported = errors.New("blackhole: magnet links not supported (set SaveMagnetFiles to enable)")

// ErrRemoveWithoutDelete is returned by RemoveItem when
// deleteData=false. Mirrors C# `NotSupportedException("Blackhole
// cannot remove DownloadItem without deleting the data...")` at
// `:118`.
var ErrRemoveWithoutDelete = errors.New("blackhole: cannot remove item without deleting the data")

// Compile-time guard.
var _ downloadclients.Provider = (*TorrentBlackhole)(nil)

// constructor is the registry factory.
func constructor(def *downloadclients.DownloadClient, deps downloadclients.Dependencies) (downloadclients.Provider, error) {
	if def == nil {
		return nil, errors.New("blackhole: nil DownloadClient row")
	}
	settings := NewTorrentSettings()
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("blackhole: settings unmarshal: %w", err)
		}
	}
	var log *slog.Logger
	if l, ok := deps.Logger.(*slog.Logger); ok {
		log = l
	}
	return New(def, settings, nil, log), nil
}

// init registers TorrentBlackhole with the global registry.
func init() {
	downloadclients.Default().MustRegister(TorrentImplementation, TorrentConfigContract, constructor)
}
