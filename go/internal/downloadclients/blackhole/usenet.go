package blackhole

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/organizer"
	"sonarr.tv/sonarr/internal/ospath"
)

// UsenetImplementation + UsenetConfigContract are the persisted-row
// identifiers. Mirrors C# class names `UsenetBlackhole` +
// `UsenetBlackholeSettings`.
const (
	UsenetImplementation = "UsenetBlackhole"
	UsenetConfigContract = "UsenetBlackholeSettings"
)

// UsenetSettings ports `UsenetBlackholeSettings` at
// `src/NzbDrone.Core/Download/Clients/Blackhole/UsenetBlackholeSettings.cs:17-33`.
//
// Two folders, no extra knobs — the C# class has no per-instance
// defaults (the no-arg constructor leaves both paths blank for the
// operator to fill in).
type UsenetSettings struct {
	downloadclients.BaseUsenetSettings

	// NzbFolder is the directory the Download method writes .nzb
	// files into. C# `:23`.
	NzbFolder string `json:"nzbFolder"`

	// WatchFolder is the directory GetItems scans for in-flight /
	// completed downloads. C# `:26`.
	WatchFolder string `json:"watchFolder"`
}

// NewUsenetSettings returns a zero-valued UsenetSettings (matches the
// C# default constructor — both paths blank).
func NewUsenetSettings() UsenetSettings { return UsenetSettings{} }

// Validate ports `UsenetBlackholeSettingsValidator` at C# `:8-15`.
// Both NzbFolder and WatchFolder run through `IsValidPath`. The Go
// port mirrors with non-empty checks — full path-syntax validation
// is the host's responsibility (the C# rule degrades to non-empty in
// practice outside Windows, same as [TorrentSettings.Validate]).
func (s UsenetSettings) Validate() []downloadclients.TestFailure {
	var failures []downloadclients.TestFailure
	if strings.TrimSpace(s.NzbFolder) == "" {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "NzbFolder",
			ErrorMessage: "NzbFolder is required",
		})
	}
	if strings.TrimSpace(s.WatchFolder) == "" {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "WatchFolder",
			ErrorMessage: "WatchFolder is required",
		})
	}
	return failures
}

// UsenetBlackhole is the [downloadclients.Provider] implementation
// for the C# `UsenetBlackhole` client at
// `src/NzbDrone.Core/Download/Clients/Blackhole/UsenetBlackhole.cs:17-109`.
//
// Symmetric to [TorrentBlackhole]: Download writes the .nzb bytes to
// `<NzbFolder>/<CleanFileName(title)>.nzb`; a separate operator-
// configured process (NZBGet/SABnzbd/etc.) picks the file up. Shares
// the [FS] interface + [osFS] default with the torrent half — both
// providers live in the same package so the surface is reused
// directly rather than duplicated.
type UsenetBlackhole struct {
	settings UsenetSettings
	def      *downloadclients.DownloadClient
	log      *slog.Logger
	fs       FS
}

// NewUsenet constructs a UsenetBlackhole Provider against the
// resolved settings + an optional [FS] override (nil → [osFS]).
func NewUsenet(def *downloadclients.DownloadClient, settings UsenetSettings, fs FS, log *slog.Logger) *UsenetBlackhole {
	if log == nil {
		log = slog.Default()
	}
	if fs == nil {
		fs = osFS{}
	}
	return &UsenetBlackhole{settings: settings, def: def, log: log, fs: fs}
}

// Name returns the user-facing label. Mirrors C# `Name` at
// `UsenetBlackhole.cs:56` (`localizationService.GetLocalizedString
// ("UsenetBlackhole")`). The Go port returns the literal English
// label — localization is plumbed at the API edge, not the Provider
// edge.
func (u *UsenetBlackhole) Name() string { return "Usenet Blackhole" }

// Protocol returns the protocol family. UsenetBlackhole inherits
// from C# `UsenetClientBase` → Usenet.
func (u *UsenetBlackhole) Protocol() indexers.DownloadProtocol {
	return indexers.DownloadProtocolUsenet
}

// Definition returns the persisted DownloadClient row. Nil for
// test-constructed providers.
func (u *UsenetBlackhole) Definition() *downloadclients.DownloadClient { return u.def }

// Settings returns the bound UsenetSettings. Exposed for tests that
// need to assert against the round-tripped JSON without re-decoding.
func (u *UsenetBlackhole) Settings() UsenetSettings { return u.settings }

// Download ports `UsenetBlackhole.AddFromNzbFile` at C# `:38-54`.
// Writes the .nzb bytes to `<NzbFolder>/<CleanFileName(title)>.nzb`
// and returns "" for the DownloadID (matches C# `return null` at
// `:53` — same divergence rationale as [TorrentBlackhole.Download],
// resolved by the ScanWatchFolder follow-up).
//
// The C# code calls `IValidateNzbs.Validate(fileContent)` before the
// write (base UsenetClientBase machinery). The Go port skips that
// validation — same call-out as sabnzbd/nzbget; the fetcher upstream
// of the Provider is expected to have already produced a well-formed
// NZB.
func (u *UsenetBlackhole) Download(_ context.Context, req *downloadclients.GrabRequest) (string, error) {
	if req == nil {
		return "", errors.New("blackhole: nil GrabRequest")
	}
	if strings.TrimSpace(req.ReleaseTitle) == "" {
		return "", errors.New("blackhole: GrabRequest.ReleaseTitle is empty")
	}
	if len(req.NzbBody) == 0 {
		return "", errors.New("blackhole: GrabRequest.NzbBody is empty")
	}
	title := organizer.CleanFileNameDefault(req.ReleaseTitle)
	path := filepath.Join(u.settings.NzbFolder, title+".nzb")
	if err := u.fs.WriteFile(path, req.NzbBody, 0o644); err != nil {
		return "", fmt.Errorf("blackhole: write nzb file: %w", err)
	}
	u.log.Debug("blackhole: nzb download succeeded", "path", path)
	return "", nil
}

// GetItems ports `UsenetBlackhole.GetItems` at C# `:58-82`.
//
// Same divergence as [TorrentBlackhole.GetItems]: the C# code walks
// `_scanWatchFolder.GetItems(Settings.WatchFolder, ScanGracePeriod)`
// and yields filesystem-derived items. ScanWatchFolder (C#
// `ScanWatchFolder.cs:1-202`) is the ~200 LOC file-walk + locking +
// grace-period + cache-TTL service that warrants its own slice.
// Until it ports, GetItems returns an empty slice (never nil).
func (u *UsenetBlackhole) GetItems(_ context.Context) ([]*downloadclients.Item, error) {
	return []*downloadclients.Item{}, nil
}

// GetImportItem is the C# base passthrough (see
// `DownloadClientBase.cs:102-105`).
func (u *UsenetBlackhole) GetImportItem(_ context.Context, item *downloadclients.Item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

// RemoveItem ports `UsenetBlackhole.RemoveItem` at C# `:84-92`.
// Identical contract to the torrent half — no client-side queue;
// deleteData=true removes the on-disk folder, false returns
// [ErrRemoveWithoutDelete].
func (u *UsenetBlackhole) RemoveItem(_ context.Context, item *downloadclients.Item, deleteData bool) error {
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
	if err := u.fs.RemoveAll(path); err != nil {
		return fmt.Errorf("blackhole: remove item data: %w", err)
	}
	return nil
}

// GetStatus ports `UsenetBlackhole.GetStatus` at C# `:94-101`.
// Always IsLocalhost=true; OutputRootFolders is `[WatchFolder]`.
func (u *UsenetBlackhole) GetStatus(_ context.Context) (*downloadclients.ClientInfo, error) {
	info := &downloadclients.ClientInfo{IsLocalhost: true}
	if strings.TrimSpace(u.settings.WatchFolder) != "" {
		info.OutputRootFolders = append(info.OutputRootFolders, ospath.New(u.settings.WatchFolder))
	}
	return info, nil
}

// MarkItemAsImported returns [downloadclients.ErrMarkAsImportedUnsupported].
// Blackhole has no client-side queue concept (matches C# base
// `DownloadClientBase.cs:189-192` default).
func (u *UsenetBlackhole) MarkItemAsImported(_ context.Context, _ *downloadclients.Item) error {
	return downloadclients.ErrMarkAsImportedUnsupported
}

// Test ports `UsenetBlackhole.Test(failures)` at C# `:103-107`.
// Validates that NzbFolder + WatchFolder both exist on disk.
//
// Settings.Validate runs first; the disk probe runs only when no
// Settings failures — same ordering as [TorrentBlackhole.Test].
func (u *UsenetBlackhole) Test(_ context.Context) ([]downloadclients.TestFailure, error) {
	failures := u.settings.Validate()
	if len(failures) > 0 {
		return failures, nil
	}
	failures = append(failures, testFolder(u.fs, u.settings.NzbFolder, "NzbFolder")...)
	failures = append(failures, testFolder(u.fs, u.settings.WatchFolder, "WatchFolder")...)
	return failures, nil
}

// RequestAction is the C# base no-op (see `DownloadClientBase.cs:165-167`).
func (u *UsenetBlackhole) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// Compile-time guard.
var _ downloadclients.Provider = (*UsenetBlackhole)(nil)

// usenetConstructor is the registry factory for UsenetBlackhole.
func usenetConstructor(def *downloadclients.DownloadClient, deps downloadclients.Dependencies) (downloadclients.Provider, error) {
	if def == nil {
		return nil, errors.New("blackhole: nil DownloadClient row")
	}
	settings := NewUsenetSettings()
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("blackhole: settings unmarshal: %w", err)
		}
	}
	var log *slog.Logger
	if l, ok := deps.Logger.(*slog.Logger); ok {
		log = l
	}
	return NewUsenet(def, settings, nil, log), nil
}

func init() {
	downloadclients.Default().MustRegister(UsenetImplementation, UsenetConfigContract, usenetConstructor)
}
