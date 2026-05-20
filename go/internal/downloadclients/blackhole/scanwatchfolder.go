package blackhole

import (
	"crypto/sha1" //nolint:gosec // C# parity: HashConverter uses SHA-1 for stable identifiers (NOT a security primitive).
	"encoding/hex"
	"errors"
	"log/slog"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/organizer"
	"sonarr.tv/sonarr/internal/ospath"
)

// WatchFolderItem ports C# `WatchFolderItem` at
// `src/NzbDrone.Core/Download/Clients/Blackhole/WatchFolderItem.cs:6-18`.
//
// The cached state behind [ScanWatchFolder.GetItems]: tracks the
// per-folder identity (DownloadID + Title + OutputPath) plus the
// hash-and-grace-period machinery (Hash + LastChanged) the
// downloading→completed promotion drives off.
type WatchFolderItem struct {
	// DownloadID is the per-watch-folder stable identifier.
	// Composed as `<basename>_<modtime-unixnano>` so renames produce
	// a fresh ID (matches C# `_<Ticks>` suffix at
	// `ScanWatchFolder.cs:65,101`). UnixNano replaces .NET's Ticks
	// (both monotonically order moves/recreations).
	DownloadID string

	// Title is the cleaned folder/file basename (passes through
	// [organizer.CleanFileNameDefault]).
	Title string

	// TotalSize is the byte sum of all files under the folder (or
	// the single file's size for the video-file path). Zero until
	// the PreCheck arm runs.
	TotalSize int64

	// RemainingTime is the wait-period countdown while the item is
	// inside the grace period. nil means "completed" / no
	// countdown.
	RemainingTime *time.Duration

	// OutputPath is the on-disk folder (or video file) the item
	// represents.
	OutputPath ospath.OsPath

	// Status is the runtime state — Completed unless the file-lock
	// probe or the grace-period check flipped it to Downloading.
	Status downloadclients.DownloadItemStatus

	// LastChanged is the wall-clock timestamp the item's hash was
	// last seen change. Drives the grace-period maths in
	// [updateWatchItemExpiry].
	LastChanged time.Time

	// Hash is the deterministic content/metadata fingerprint
	// derived from folder/file paths + mtimes + sizes. Used to
	// detect "still actively being written" between two GetItems
	// calls.
	Hash string
}

// Disk is the narrow filesystem surface [ScanWatchFolder] consumes.
// Split out from the [FS] interface in `blackhole.go` because the
// scanner needs richer per-entry metadata than the file-writer half
// (directory listing, mtimes, file-locking probe). Production binds
// [defaultDisk] which delegates to [os.Stat]+filepath.Walk-style
// helpers; tests inject a fake to drive deterministic timestamps +
// lock states.
type Disk interface {
	// ListDirectories returns the absolute paths of every direct
	// child directory under path. Empty slice when path is empty
	// or unreachable. Mirrors C# `IDiskProvider.GetDirectories` at
	// `ScanWatchFolder.cs:53`.
	ListDirectories(path string) ([]string, error)

	// ListFiles returns the absolute paths of every file under
	// path. recursive=true descends into subdirectories. Mirrors
	// C# `IDiskProvider.GetFiles(folder, recursive=true)` at
	// `:78`.
	ListFiles(path string, recursive bool) ([]string, error)

	// FileSize returns the byte size of path. Errors collapse to 0
	// at the call site so a transient probe failure doesn't void
	// the whole scan.
	FileSize(path string) (int64, error)

	// FileModTime returns the last-write timestamp of path.
	FileModTime(path string) (time.Time, error)

	// FolderModTime returns the last-write timestamp of path
	// (folder). The Go port collapses C# `FolderGetCreationTime`
	// and `FolderGetLastWrite` into one call — Go stdlib doesn't
	// expose creation time portably (Linux ext4 stores it but
	// `os.Stat` doesn't surface it), so mtime carries both roles.
	// The DownloadID composition stays stable (mtime moves only
	// when the folder's contents change — same coarse behaviour
	// the C# Ticks-based ID relied on for folder renames).
	FolderModTime(path string) (time.Time, error)

	// IsFileLocked reports whether path has an exclusive lock /
	// open writer. C# `IDiskProvider.IsFileLocked` opens the file
	// in FileShare.None and returns true on `IOException`. The
	// Go-port default ([defaultDisk]) returns false (no portable
	// equivalent on POSIX); tests can override via the fake to
	// exercise the "still writing → Downloading" arm. Documented
	// divergence: on a real POSIX deployment, a long .nzb upload
	// will surface as Completed instead of Downloading until the
	// grace-period elapses — the grace-period itself still gates
	// the import.
	IsFileLocked(path string) (bool, error)
}

// Scanner narrows the C# `IDiskScanService` for the two methods
// ScanWatchFolder reaches for. Production binds the future shared
// scanner (the [downloadedimport] package's DiskScanner has the
// matching surface).
type Scanner interface {
	// GetVideoFiles returns absolute paths of every video-extension
	// file under path. recursive=false stops at the top level.
	// Mirrors C# `IDiskScanService.GetVideoFiles` at `:95`.
	GetVideoFiles(path string, recursive bool) []string

	// FilterPaths drops paths the scanner has marked as ignored
	// (sub-paths matching the RemotePathMappings filter set + the
	// configured ignore-folder list). Mirrors C#
	// `IDiskScanService.FilterPaths` at `:53,95`.
	FilterPaths(rootPath string, paths []string) []string
}

// ScanGracePeriodDefault is the C# `TimeSpan.FromSeconds(30)`
// default (TorrentBlackhole.cs:41, UsenetBlackhole.cs:35). The
// 30-second window guards against importing a still-incomplete
// transfer when the file-lock probe can't see the writer.
const ScanGracePeriodDefault = 30 * time.Second

// cacheTTL is the per-watch-folder snapshot TTL the C# `ICacheManager`
// instance enforces at `ScanWatchFolder.cs:46`. After 5 minutes of
// no GetItems calls, the cached LastChanged/Hash state evaporates and
// the next call starts the grace-period clock fresh.
const cacheTTL = 5 * time.Minute

// preCheckExpiry mirrors C# `PreCheckWatchItemExpiry` at
// `ScanWatchFolder.cs:131`: items whose `LastChanged` is more than
// an hour stale skip the expensive disk re-hash on this pass and
// inherit the cached TotalSize + Hash. (The cache hot-path: once a
// download has stayed quiet for an hour, GetItems treats it as
// settled and stops probing.)
const preCheckExpiry = time.Hour

// ScanWatchFolder is the Go port of the C# `ScanWatchFolder` service
// at `src/NzbDrone.Core/Download/Clients/Blackhole/ScanWatchFolder.cs:21-202`.
//
// One instance is shared across both Blackhole halves — the cache is
// keyed by watch-folder path so a single host serving both
// usenet+torrent watch folders gets independent state. The cache
// expires per-folder on a 5-minute idle TTL (matches the C#
// `cacheManager.GetCache<...>` TTL); inside the active window,
// repeated GetItems calls re-use the LastChanged/Hash tail so the
// grace-period countdown survives across polls.
//
// Concurrency: GetItems calls per watch-folder are serialized via
// [ScanWatchFolder.mu]. The cache map is read+written under the
// same lock. The expected production caller is the Phase 4 polling
// loop which fires per-Provider GetItems sequentially per tick.
type ScanWatchFolder struct {
	disk    Disk
	scanner Scanner
	log     *slog.Logger
	clock   func() time.Time

	mu    sync.Mutex
	cache map[string]*cacheEntry
}

// cacheEntry is one watch-folder's snapshot — keyed by DownloadID.
// The `expires` field carries the absolute deadline; calls past
// the deadline treat the snapshot as empty (and overwrite it).
type cacheEntry struct {
	items   map[string]*WatchFolderItem
	expires time.Time
}

// NewScanWatchFolder constructs a ScanWatchFolder with the supplied
// dependencies. clock=nil binds [time.Now] (production); tests
// inject a deterministic clock to drive the grace-period state
// machine. disk=nil panics — there's no sensible default that
// doesn't reach for real filesystem state. scanner=nil binds a
// pass-through (FilterPaths returns input as-is; GetVideoFiles
// returns nil) — that keeps unit tests of clients that don't care
// about video-file probing terse.
func NewScanWatchFolder(disk Disk, scanner Scanner, log *slog.Logger, clock func() time.Time) *ScanWatchFolder {
	if disk == nil {
		panic("blackhole: NewScanWatchFolder requires a non-nil Disk")
	}
	if log == nil {
		log = slog.Default()
	}
	if clock == nil {
		clock = time.Now
	}
	if scanner == nil {
		scanner = passThroughScanner{}
	}
	return &ScanWatchFolder{
		disk:    disk,
		scanner: scanner,
		log:     log,
		clock:   clock,
		cache:   map[string]*cacheEntry{},
	}
}

// passThroughScanner is the no-op Scanner default — FilterPaths
// returns the input unchanged, GetVideoFiles returns nil. Used when
// the caller doesn't supply a real scanner.
type passThroughScanner struct{}

// GetVideoFiles always returns nil for the no-op scanner.
func (passThroughScanner) GetVideoFiles(string, bool) []string { return nil }

// FilterPaths returns its input unchanged for the no-op scanner.
func (passThroughScanner) FilterPaths(_ string, p []string) []string { return p }

// GetItems ports C# `ScanWatchFolder.GetItems(watchFolder, waitPeriod)`
// at `:36-49`. Walks every direct-child folder + every top-level
// video file under watchFolder, hashes each, and emits a
// WatchFolderItem per entry. Repeated calls share state via the
// per-watchFolder cache so the grace-period countdown carries
// across polls.
//
// Empty / blank watchFolder → nil, nil (matches the C# behaviour —
// the C# iterator yields nothing when GetDirectories returns
// nothing).
func (s *ScanWatchFolder) GetItems(watchFolder string, waitPeriod time.Duration) ([]*WatchFolderItem, error) {
	if strings.TrimSpace(watchFolder) == "" {
		return nil, errors.New("blackhole: ScanWatchFolder.GetItems: empty watchFolder")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.clock()
	last := s.lastItemsLocked(watchFolder, now)
	fresh := map[string]*WatchFolderItem{}

	dirs, err := s.disk.ListDirectories(watchFolder)
	if err != nil {
		s.log.Debug("blackhole: ListDirectories failed", "path", watchFolder, "err", err)
	}
	for _, folder := range s.scanner.FilterPaths(watchFolder, dirs) {
		item := s.scanFolder(folder, last, waitPeriod, now)
		if item != nil {
			fresh[item.DownloadID] = item
		}
	}

	videos := s.scanner.FilterPaths(watchFolder, s.scanner.GetVideoFiles(watchFolder, false))
	for _, vf := range videos {
		item := s.scanVideoFile(vf, last, waitPeriod, now)
		if item != nil {
			fresh[item.DownloadID] = item
		}
	}

	s.cache[watchFolder] = &cacheEntry{items: fresh, expires: now.Add(cacheTTL)}
	out := make([]*WatchFolderItem, 0, len(fresh))
	for _, v := range fresh {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DownloadID < out[j].DownloadID })
	return out, nil
}

// lastItemsLocked returns the cached items for watchFolder if the
// entry is still inside its TTL window; otherwise an empty map.
// Caller holds s.mu.
func (s *ScanWatchFolder) lastItemsLocked(watchFolder string, now time.Time) map[string]*WatchFolderItem {
	entry, ok := s.cache[watchFolder]
	if !ok || now.After(entry.expires) {
		return map[string]*WatchFolderItem{}
	}
	return entry.items
}

// scanFolder builds a WatchFolderItem for one subfolder of the watch
// folder. Mirrors the folder arm of C# `GetDownloadItems` at
// `:53-93`. Returns nil for special / reserved folders (the C# code
// `continue`s past them).
func (s *ScanWatchFolder) scanFolder(folder string, last map[string]*WatchFolderItem, waitPeriod time.Duration, now time.Time) *WatchFolderItem {
	name := filepath.Base(folder)
	if isSpecialFolder(name) {
		return nil
	}
	ctime, err := s.disk.FolderModTime(folder)
	if err != nil {
		s.log.Debug("blackhole: FolderModTime failed", "path", folder, "err", err)
		return nil
	}
	item := &WatchFolderItem{
		DownloadID:    name + "_" + downloadIDSuffix(ctime),
		Title:         cleanTitle(name),
		OutputPath:    ospath.New(folder),
		Status:        downloadclients.DownloadItemStatusCompleted,
		RemainingTime: zeroDuration(),
	}

	old := last[item.DownloadID]
	if preCheckExpiryGate(item, old, now) {
		files, err := s.disk.ListFiles(folder, true)
		if err != nil {
			s.log.Debug("blackhole: ListFiles failed", "path", folder, "err", err)
		}
		var total int64
		for _, f := range files {
			sz, err := s.disk.FileSize(f)
			if err != nil {
				s.log.Debug("blackhole: FileSize failed", "path", f, "err", err)
				continue
			}
			total += sz
		}
		item.TotalSize = total
		item.Hash = hashFolder(s, folder, files)
		if anyLocked(s, files) {
			item.Status = downloadclients.DownloadItemStatusDownloading
			item.RemainingTime = nil
		}
		updateWatchItemExpiry(item, old, waitPeriod, now)
	}
	return item
}

// scanVideoFile builds a WatchFolderItem for one top-level video
// file. Mirrors the file arm of C# `GetDownloadItems` at `:95-126`.
func (s *ScanWatchFolder) scanVideoFile(file string, last map[string]*WatchFolderItem, waitPeriod time.Duration, now time.Time) *WatchFolderItem {
	mtime, err := s.disk.FileModTime(file)
	if err != nil {
		s.log.Debug("blackhole: FileModTime failed", "path", file, "err", err)
		return nil
	}
	name := filepath.Base(file)
	item := &WatchFolderItem{
		DownloadID:    name + "_" + downloadIDSuffix(mtime),
		Title:         cleanTitle(name),
		OutputPath:    ospath.New(file),
		Status:        downloadclients.DownloadItemStatusCompleted,
		RemainingTime: zeroDuration(),
	}

	old := last[item.DownloadID]
	if preCheckExpiryGate(item, old, now) {
		size, err := s.disk.FileSize(file)
		if err != nil {
			s.log.Debug("blackhole: FileSize failed", "path", file, "err", err)
		}
		item.TotalSize = size
		item.Hash = hashFile(s, file)
		locked, _ := s.disk.IsFileLocked(file)
		if locked {
			item.Status = downloadclients.DownloadItemStatusDownloading
		}
		updateWatchItemExpiry(item, old, waitPeriod, now)
	}
	return item
}

// preCheckExpiryGate mirrors C# `PreCheckWatchItemExpiry` at
// `:129-140`: returns true when the item should be re-hashed this
// pass; false when the cached state is fresh enough to skip the
// disk probe (and the new item inherits TotalSize+Hash from old).
func preCheckExpiryGate(fresh, old *WatchFolderItem, now time.Time) bool {
	if old == nil || old.LastChanged.Add(preCheckExpiry).After(now) {
		return true
	}
	fresh.TotalSize = old.TotalSize
	fresh.Hash = old.Hash
	return false
}

// updateWatchItemExpiry mirrors C# `UpdateWatchItemExpiry` at
// `:142-160`: carries LastChanged forward when the hash matches the
// cached state (item is stable); resets to `now` when the hash
// shifts (item is actively being written). If `now - LastChanged`
// is still inside the wait-period, the item stays Downloading with
// a positive RemainingTime; otherwise it surfaces as Completed.
func updateWatchItemExpiry(fresh, old *WatchFolderItem, waitPeriod time.Duration, now time.Time) {
	if old != nil && fresh.Hash == old.Hash {
		fresh.LastChanged = old.LastChanged
	} else {
		fresh.LastChanged = now
	}
	remaining := waitPeriod - now.Sub(fresh.LastChanged)
	if remaining > 0 {
		d := remaining
		fresh.RemainingTime = &d
		fresh.Status = downloadclients.DownloadItemStatusDownloading
	}
}

// hashFolder ports the folder-arm of C# `GetHash(folder, files)` at
// `:162-182`. Composes a deterministic string over folder + folder
// mtime + sorted file hashes, then SHA-1 + hex.
func hashFolder(s *ScanWatchFolder, folder string, files []string) string {
	var b strings.Builder
	b.WriteString(folder)
	if mt, err := s.disk.FolderModTime(folder); err == nil {
		b.WriteString(mt.UTC().Format(time.RFC3339Nano))
	}
	sorted := append([]string(nil), files...)
	sort.Strings(sorted)
	for _, f := range sorted {
		b.WriteString(hashFile(s, f))
	}
	sum := sha1.Sum([]byte(b.String())) //nolint:gosec // C# parity: SHA-1 used for stable identifier, not security.
	return hex.EncodeToString(sum[:])
}

// hashFile ports the file-arm of C# `GetHash(file)` at `:184-200`.
// Composes path + mtime + size, then SHA-1 + hex. Errors are
// swallowed (matches the C# try/catch trace behaviour).
func hashFile(s *ScanWatchFolder, file string) string {
	var b strings.Builder
	b.WriteString(file)
	if mt, err := s.disk.FileModTime(file); err == nil {
		b.WriteString(mt.UTC().Format(time.RFC3339Nano))
	}
	if sz, err := s.disk.FileSize(file); err == nil {
		b.WriteString(strconv.FormatInt(sz, 10))
	}
	sum := sha1.Sum([]byte(b.String())) //nolint:gosec // C# parity: SHA-1 used for stable identifier, not security.
	return hex.EncodeToString(sum[:])
}

// downloadIDSuffix mirrors the `_<Ticks>` suffix the C# code
// appends to folder/file basenames. UnixNano replaces .NET's Ticks
// — both order moves/recreations the same way.
func downloadIDSuffix(t time.Time) string {
	return strings.ReplaceAll(t.UTC().Format("20060102T150405.000000000"), ".", "")
}

// cleanTitle wraps [organizer.CleanFileNameDefault] — kept as a
// local helper so the call sites stay terse. Mirrors the C# arms
// at `:56,97` which both feed `Path.GetFileName(...)` through
// `FileNameBuilder.CleanFileName` before yielding the
// WatchFolderItem.Title.
func cleanTitle(name string) string {
	return organizer.CleanFileNameDefault(name)
}

// anyLocked iterates files reporting true on the first IsFileLocked
// hit. Errors from the probe collapse to "not locked" (the C# code
// does the same — the IDiskProvider implementation logs+returns
// false on probe failure).
func anyLocked(s *ScanWatchFolder, files []string) bool {
	for _, f := range files {
		if locked, _ := s.disk.IsFileLocked(f); locked {
			return true
		}
	}
	return false
}

// zeroDuration returns a pointer to a zero time.Duration. The
// WatchFolderItem.RemainingTime field is a *time.Duration so the
// Completed default carries `&0` (matches the C# `RemainingTime =
// TimeSpan.Zero` semantics — distinguishable from "unknown / nil").
func zeroDuration() *time.Duration {
	d := time.Duration(0)
	return &d
}

// specialFolders ports the C# `NzbDrone.Common.Disk.SpecialFolders`
// list at `src/NzbDrone.Common/Disk/SpecialFolders.cs:7-36`. The
// scanner skips any direct-child folder whose basename (case-
// insensitive) is in this set.
var specialFolders = map[string]bool{
	// Windows
	"boot":                      true,
	"bootmgr":                   true,
	"cache":                     true,
	"msocache":                  true,
	"recovery":                  true,
	"$recycle.bin":              true,
	"recycler":                  true,
	"system volume information": true,
	"temporary internet files":  true,
	"windows":                   true,
	// OS X
	".fseventd":      true,
	".spotlight":     true,
	".trashes":       true,
	".vol":           true,
	"cachedmessages": true,
	"caches":         true,
	"trash":          true,
	// QNAP
	".@__thumb": true,
	// Synology
	"@eadir":   true,
	"#recycle": true,
}

// isSpecialFolder is the case-insensitive lookup the scanner uses
// before walking a subfolder.
func isSpecialFolder(name string) bool {
	return specialFolders[strings.ToLower(name)]
}
