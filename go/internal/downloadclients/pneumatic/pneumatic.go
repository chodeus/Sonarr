// Package pneumatic ports the C# Pneumatic download client at
// `src/NzbDrone.Core/Download/Clients/Pneumatic/`.
//
// Pneumatic is the XBMC/Kodi pneumatic-plugin adapter: it writes the
// raw .nzb bytes to a folder AND writes a companion `.strm` file
// containing a `plugin://plugin.program.pneumatic/?mode=strm&...`
// URL that Kodi reads to trigger its own download. Filesystem-only,
// no RPC.
//
// Slice 5.J61 ships the full Provider end to end:
//
//   - [Settings] + [NewSettings] + Validate (NzbFolder + StrmFolder,
//     both required).
//   - [Pneumatic] Provider with full Download (writes both the .nzb
//     and the .strm file via the bound [FS]) + GetItems (walks
//     StrmFolder, filters to `.strm`, projects each file to an
//     `Item`) + Test + GetStatus.
//   - RemoveItem returns [ErrRemoveNotSupported] — mirrors C#
//     `Pneumatic.cs:103-106` which throws NotSupportedException
//     unconditionally. The Blackhole pair's
//     ErrRemoveWithoutDelete distinction does NOT apply here:
//     Pneumatic has no operator escape hatch for deleting the strm
//     pointer.
//   - MarkItemAsImported returns [downloadclients.ErrMarkAsImportedUnsupported].
//
// Two divergences from the C# behaviour are intentional and called
// out at the relevant sites:
//
//   - [Pneumatic.Download] does NOT guard against FullSeason
//     releases the way C# `:43-46` does (throws NotSupportedException
//     when ParsedEpisodeInfo.FullSeason). The Go port's GrabRequest
//     does not yet carry a pre-derived `IsFullSeason` flag; pulling
//     decisionengine into this package just to type-assert
//     Subject.ParsedEpisodeInfo.FullSeason adds significant
//     transitive imports. Documented; follow-up: surface
//     IsFullSeason on GrabRequest (same pattern as IsRecent +
//     SeriesTitle) and gate Download.
//
//   - [defaultFS.IsFileLocked] returns (false, nil) — same POSIX
//     limitation as the blackhole.DefaultDisk default; the grace-
//     period state machine that Blackhole layers on top of its
//     scan is absent here because the C# Pneumatic doesn't ship
//     one (the C# version surfaces Downloading only when the
//     lock probe trips). Net effect on POSIX: in-flight .strm
//     pointers surface as Completed; the manual-import flow is
//     gated by file size / mtime rather than the queue Status.
package pneumatic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/organizer"
	"sonarr.tv/sonarr/internal/ospath"
)

// Implementation + ConfigContract are the persisted-row identifiers.
// Mirrors C# class names `Pneumatic` + `PneumaticSettings`.
const (
	Implementation = "Pneumatic"
	ConfigContract = "PneumaticSettings"
)

// strmExt is the extension Pneumatic uses for the Kodi-plugin
// pointer file. Defined as a const so the GetItems filter + the
// Download writer agree on the suffix.
const strmExt = ".strm"

// strmTemplate is the C# `string.Format(...)` literal at
// `Pneumatic.cs:131`. Two %s slots: nzb path + display title (in
// that order — matches the `{0}, {1}` order in the C# format
// string).
const strmTemplate = "plugin://plugin.program.pneumatic/?mode=strm&type=add_file&nzb=%s&nzbname=%s"

// Settings ports `PneumaticSettings` at
// `src/NzbDrone.Core/Download/Clients/Pneumatic/PneumaticSettings.cs:17-32`.
//
// Two folders; both required. Validate mirrors the C# FluentValidation
// rules (`IsValidPath` → non-empty in the Go port, same divergence
// pattern as [internal/downloadclients/blackhole.TorrentSettings.Validate]).
type Settings struct {
	downloadclients.BaseUsenetSettings

	// NzbFolder is the directory Download writes the .nzb file
	// into. C# `:22`.
	NzbFolder string `json:"nzbFolder"`

	// StrmFolder is the directory Download writes the companion
	// .strm pointer into AND the directory GetItems walks. C#
	// `:25`.
	StrmFolder string `json:"strmFolder"`
}

// NewSettings returns a zero-valued Settings (matches the C#
// default constructor — both paths blank).
func NewSettings() Settings { return Settings{} }

// Validate ports `PneumaticSettingsValidator` at C# `:8-15`.
func (s Settings) Validate() []downloadclients.TestFailure {
	var failures []downloadclients.TestFailure
	if strings.TrimSpace(s.NzbFolder) == "" {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "NzbFolder",
			ErrorMessage: "NzbFolder is required",
		})
	}
	if strings.TrimSpace(s.StrmFolder) == "" {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "StrmFolder",
			ErrorMessage: "StrmFolder is required",
		})
	}
	return failures
}

// FS is the narrow filesystem surface [Pneumatic] consumes.
// Wider than the [blackhole.FS] because Pneumatic owns its own
// GetItems walk (no separate ScanWatchFolder service): it needs
// directory listing + per-file size + mtime + lock probe in
// addition to the write side.
//
// Production binds [defaultFS]. Tests inject a fake to assert the
// write side + observe queue projection without touching real disk.
type FS interface {
	WriteFile(path string, body []byte, perm os.FileMode) error
	IsDir(path string) (bool, error)
	ListFiles(path string) ([]string, error)
	FileSize(path string) (int64, error)
	FileModTime(path string) (time.Time, error)
	IsFileLocked(path string) (bool, error)
}

// defaultFS is the production [FS] implementation. Delegates to
// the Go stdlib. [defaultFS.IsFileLocked] always returns false —
// see the package-level divergence note.
type defaultFS struct{}

// WriteFile writes body to path with perm. Creates any missing
// parent directories with mode 0o755 (matches the C#
// `IDiskProvider.WriteAllText` path which the Pneumatic-folder
// init step pre-creates).
func (defaultFS) WriteFile(path string, body []byte, perm os.FileMode) error {
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	return os.WriteFile(path, body, perm)
}

// IsDir reports whether path is an extant directory.
func (defaultFS) IsDir(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return info.IsDir(), nil
}

// ListFiles returns absolute paths of every top-level file under
// path (non-recursive — matches C# `_diskProvider.GetFiles(path,
// recursive=false)` at `Pneumatic.cs:67`).
func (defaultFS) ListFiles(path string) ([]string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		out = append(out, filepath.Join(path, e.Name()))
	}
	return out, nil
}

// FileSize returns the byte size from os.Stat.
func (defaultFS) FileSize(path string) (int64, error) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

// FileModTime returns the mtime from os.Stat.
func (defaultFS) FileModTime(path string) (time.Time, error) {
	st, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return st.ModTime(), nil
}

// IsFileLocked always returns (false, nil) — see the documented
// divergence at the package head.
func (defaultFS) IsFileLocked(string) (bool, error) { return false, nil }

// Pneumatic is the [downloadclients.Provider] implementation for
// the C# `Pneumatic` client at
// `src/NzbDrone.Core/Download/Clients/Pneumatic/Pneumatic.cs:19-144`.
type Pneumatic struct {
	settings Settings
	def      *downloadclients.DownloadClient
	log      *slog.Logger
	fs       FS
}

// New constructs a Pneumatic Provider. fs=nil binds [defaultFS];
// log=nil binds [slog.Default].
func New(def *downloadclients.DownloadClient, settings Settings, fs FS, log *slog.Logger) *Pneumatic {
	if log == nil {
		log = slog.Default()
	}
	if fs == nil {
		fs = defaultFS{}
	}
	return &Pneumatic{settings: settings, def: def, log: log, fs: fs}
}

// Name returns the user-facing label. C# `:34`.
func (p *Pneumatic) Name() string { return "Pneumatic" }

// Protocol returns Usenet. C# `:36`.
func (p *Pneumatic) Protocol() indexers.DownloadProtocol {
	return indexers.DownloadProtocolUsenet
}

// Definition returns the persisted DownloadClient row.
func (p *Pneumatic) Definition() *downloadclients.DownloadClient { return p.def }

// Settings returns the bound Settings (exposed for tests).
func (p *Pneumatic) Settings() Settings { return p.settings }

// Download ports `Pneumatic.Download` at C# `:38-61`. Writes the
// fetched NzbBody to `<NzbFolder>/<title>.nzb` AND writes the
// companion `.strm` pointer to `<StrmFolder>/<title>.strm`. The
// .strm body is the Kodi-plugin URL the C# code composes at `:131`.
//
// Returns a synthesized DownloadID matching C#
// `GetDownloadClientId(strmFile)` at `:139-142`:
// `<Definition.Name>_<basename>_<utc-stamp>`. UTC-stamp substitutes
// .NET Ticks (same monotonic ordering across rename/recreation).
//
// Divergence (5.J61): the FullSeason guard at C# `:43-46` is
// deferred — see the package-level note. The check would require
// either a new GrabRequest field or a decisionengine type-assertion
// pulling significant transitive imports; the slice ships without
// the guard and documents the gap.
func (p *Pneumatic) Download(_ context.Context, req *downloadclients.GrabRequest) (string, error) {
	if req == nil {
		return "", errors.New("pneumatic: nil GrabRequest")
	}
	if strings.TrimSpace(req.ReleaseTitle) == "" {
		return "", errors.New("pneumatic: GrabRequest.ReleaseTitle is empty")
	}
	if len(req.NzbBody) == 0 {
		return "", errors.New("pneumatic: GrabRequest.NzbBody is empty")
	}
	if strings.TrimSpace(p.settings.StrmFolder) == "" {
		return "", ErrStrmFolderRequired
	}

	title := organizer.CleanFileNameDefault(req.ReleaseTitle)
	nzbPath := filepath.Join(p.settings.NzbFolder, title+".nzb")
	strmPath := filepath.Join(p.settings.StrmFolder, title+strmExt)

	if err := p.fs.WriteFile(nzbPath, req.NzbBody, 0o644); err != nil {
		return "", fmt.Errorf("pneumatic: write nzb file: %w", err)
	}
	p.log.Debug("pneumatic: nzb saved", "path", nzbPath)

	body := []byte(fmt.Sprintf(strmTemplate, nzbPath, title))
	if err := p.fs.WriteFile(strmPath, body, 0o644); err != nil {
		return "", fmt.Errorf("pneumatic: write strm file: %w", err)
	}
	p.log.Debug("pneumatic: strm pointer saved", "path", strmPath)

	return p.downloadID(strmPath), nil
}

// downloadID composes the per-item identifier from the bound
// Definition.Name + the strm basename + the strm's mtime. Mirrors
// C# `GetDownloadClientId(filename)` at `:139-142` with UnixNano
// in place of .NET Ticks.
func (p *Pneumatic) downloadID(file string) string {
	defName := "Pneumatic"
	if p.def != nil && p.def.Name != "" {
		defName = p.def.Name
	}
	stamp := ""
	if mt, err := p.fs.FileModTime(file); err == nil {
		stamp = mt.UTC().Format("20060102T150405000000000")
	}
	return defName + "_" + filepath.Base(file) + "_" + stamp
}

// GetItems ports `Pneumatic.GetItems` at C# `:65-101`. Walks the
// StrmFolder, filters to `.strm` files, builds one Item per entry.
// Items default to Completed; the IsFileLocked probe (always false
// under [defaultFS] — see the package divergence) flips Status to
// Downloading when true.
func (p *Pneumatic) GetItems(_ context.Context) ([]*downloadclients.Item, error) {
	if strings.TrimSpace(p.settings.StrmFolder) == "" {
		return []*downloadclients.Item{}, nil
	}
	files, err := p.fs.ListFiles(p.settings.StrmFolder)
	if err != nil {
		return nil, fmt.Errorf("pneumatic: list strm folder: %w", err)
	}
	info := p.itemClientInfo()
	out := make([]*downloadclients.Item, 0, len(files))
	for _, f := range files {
		if filepath.Ext(f) != strmExt {
			continue
		}
		title := organizer.CleanFileNameDefault(filepath.Base(f))
		size, _ := p.fs.FileSize(f)
		locked, _ := p.fs.IsFileLocked(f)
		status := downloadclients.DownloadItemStatusCompleted
		if locked {
			status = downloadclients.DownloadItemStatusDownloading
		}
		out = append(out, &downloadclients.Item{
			DownloadClientInfo: info,
			DownloadID:         p.downloadID(f),
			Title:              title,
			TotalSize:          size,
			OutputPath:         ospath.New(f),
			Status:             status,
			CanBeRemoved:       true,
			CanMoveFiles:       true,
		})
	}
	return out, nil
}

// GetImportItem is the C# base passthrough (no per-Pneumatic
// override at `Pneumatic.cs`).
func (p *Pneumatic) GetImportItem(_ context.Context, item *downloadclients.Item, _ *downloadclients.Item) (*downloadclients.Item, error) {
	return item, nil
}

// RemoveItem returns [ErrRemoveNotSupported] unconditionally —
// matches C# `Pneumatic.cs:103-106` which throws
// NotSupportedException with no message. The C# behaviour ignores
// the deleteData arg; the Go port surfaces a single error variant
// so callers can distinguish from the
// [downloadclients.ErrMarkAsImportedUnsupported] family.
func (p *Pneumatic) RemoveItem(_ context.Context, _ *downloadclients.Item, _ bool) error {
	return ErrRemoveNotSupported
}

// GetStatus ports `Pneumatic.GetStatus` at C# `:108-116`. Always
// IsLocalhost=true; no OutputRootFolders (the C# version doesn't
// populate the list either — Pneumatic doesn't have a single
// canonical output root the way Blackhole's WatchFolder does).
func (p *Pneumatic) GetStatus(_ context.Context) (*downloadclients.ClientInfo, error) {
	return &downloadclients.ClientInfo{IsLocalhost: true}, nil
}

// MarkItemAsImported returns
// [downloadclients.ErrMarkAsImportedUnsupported]. Pneumatic has no
// client-side queue (matches C# base default).
func (p *Pneumatic) MarkItemAsImported(_ context.Context, _ *downloadclients.Item) error {
	return downloadclients.ErrMarkAsImportedUnsupported
}

// Test ports `Pneumatic.Test(failures)` at C# `:118-122`. Settings
// validation runs first; the disk probe runs only when no Settings
// failures — same ordering as the Blackhole pair.
func (p *Pneumatic) Test(_ context.Context) ([]downloadclients.TestFailure, error) {
	failures := p.settings.Validate()
	if len(failures) > 0 {
		return failures, nil
	}
	failures = append(failures, testFolder(p.fs, p.settings.NzbFolder, "NzbFolder")...)
	failures = append(failures, testFolder(p.fs, p.settings.StrmFolder, "StrmFolder")...)
	return failures, nil
}

// testFolder returns a single folder-missing failure (or empty
// when the folder exists). Mirrors C# `TestFolder` at
// `DownloadClientBase.cs:170-184`.
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

// RequestAction is the C# base no-op.
func (p *Pneumatic) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

// itemClientInfo mirrors C# `DownloadClientItemClientInfo.FromDownloadClient(this, false)`.
func (p *Pneumatic) itemClientInfo() *downloadclients.ItemClientInfo {
	info := &downloadclients.ItemClientInfo{
		Protocol: p.Protocol(),
		Type:     Implementation,
		Name:     p.Name(),
	}
	if p.def != nil {
		info.ID = p.def.ID
		info.Name = p.def.Name
		info.RemoveCompletedDownloads = p.def.RemoveCompletedDownloads
	}
	return info
}

// ErrRemoveNotSupported is returned by RemoveItem. Mirrors C#
// `NotSupportedException` at `Pneumatic.cs:105` (no message).
var ErrRemoveNotSupported = errors.New("pneumatic: RemoveItem is not supported")

// ErrStrmFolderRequired is returned by Download when StrmFolder is
// blank — mirrors C# `DownloadClientException("Strm Folder needs
// to be set for Pneumatic Downloader")` at `:128`.
var ErrStrmFolderRequired = errors.New("pneumatic: StrmFolder is required to write the strm pointer")

// Compile-time guard.
var _ downloadclients.Provider = (*Pneumatic)(nil)

// constructor is the registry factory.
func constructor(def *downloadclients.DownloadClient, deps downloadclients.Dependencies) (downloadclients.Provider, error) {
	if def == nil {
		return nil, errors.New("pneumatic: nil DownloadClient row")
	}
	settings := NewSettings()
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("pneumatic: settings unmarshal: %w", err)
		}
	}
	var log *slog.Logger
	if l, ok := deps.Logger.(*slog.Logger); ok {
		log = l
	}
	return New(def, settings, nil, log), nil
}

func init() {
	downloadclients.Default().MustRegister(Implementation, ConfigContract, constructor)
}
