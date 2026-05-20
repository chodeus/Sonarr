package blackhole_test

import (
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/blackhole"
)

// fakeDisk + fakeScanner drive ScanWatchFolder deterministically.
// Every entry's mtime + size + lock state is configured up-front;
// time progression is via a func clock the test increments.

type fakeDiskEntry struct {
	isDir  bool
	mtime  time.Time
	size   int64
	locked bool
}

type fakeDisk struct {
	dirs     map[string][]string      // path → child dir paths
	files    map[string][]string      // path → recursive file paths
	topFiles map[string][]string      // path → non-recursive file paths
	entries  map[string]fakeDiskEntry // per-path stat
	dirErr   error
}

func newFakeDisk() *fakeDisk {
	return &fakeDisk{
		dirs:     map[string][]string{},
		files:    map[string][]string{},
		topFiles: map[string][]string{},
		entries:  map[string]fakeDiskEntry{},
	}
}

func (f *fakeDisk) ListDirectories(path string) ([]string, error) {
	if f.dirErr != nil {
		return nil, f.dirErr
	}
	return append([]string(nil), f.dirs[path]...), nil
}

func (f *fakeDisk) ListFiles(path string, recursive bool) ([]string, error) {
	if recursive {
		return append([]string(nil), f.files[path]...), nil
	}
	return append([]string(nil), f.topFiles[path]...), nil
}

func (f *fakeDisk) FileSize(path string) (int64, error) {
	e, ok := f.entries[path]
	if !ok {
		return 0, errors.New("not found")
	}
	return e.size, nil
}

func (f *fakeDisk) FileModTime(path string) (time.Time, error) {
	e, ok := f.entries[path]
	if !ok {
		return time.Time{}, errors.New("not found")
	}
	return e.mtime, nil
}

func (f *fakeDisk) FolderModTime(path string) (time.Time, error) {
	e, ok := f.entries[path]
	if !ok {
		return time.Time{}, errors.New("not found")
	}
	return e.mtime, nil
}

func (f *fakeDisk) IsFileLocked(path string) (bool, error) {
	return f.entries[path].locked, nil
}

type fakeScanner struct {
	videoFiles map[string][]string
	ignored    map[string]bool
}

func newFakeScanner() *fakeScanner {
	return &fakeScanner{videoFiles: map[string][]string{}, ignored: map[string]bool{}}
}

func (s *fakeScanner) GetVideoFiles(path string, _ bool) []string {
	return append([]string(nil), s.videoFiles[path]...)
}

func (s *fakeScanner) FilterPaths(_ string, paths []string) []string {
	out := paths[:0:0]
	for _, p := range paths {
		if !s.ignored[p] {
			out = append(out, p)
		}
	}
	return out
}

func newScanner(t *testing.T, fd *fakeDisk, fs *fakeScanner, now *time.Time) *blackhole.ScanWatchFolder {
	t.Helper()
	clock := func() time.Time { return *now }
	return blackhole.NewScanWatchFolder(fd, fs, nil, clock)
}

func TestScanWatchFolder_EmptyPathErrors(t *testing.T) {
	fd := newFakeDisk()
	fs := newFakeScanner()
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	sw := newScanner(t, fd, fs, &now)
	_, err := sw.GetItems("", time.Second)
	if err == nil {
		t.Fatal("err = nil, want empty-path error")
	}
}

func TestScanWatchFolder_FolderArm_CompletedAfterGrace(t *testing.T) {
	fd := newFakeDisk()
	fs := newFakeScanner()
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	// /watch contains one download folder /watch/Show.S01E01
	folder := "/watch/Show.S01E01"
	file := "/watch/Show.S01E01/episode.mkv"
	fd.dirs["/watch"] = []string{folder}
	fd.files[folder] = []string{file}
	fd.entries[folder] = fakeDiskEntry{isDir: true, mtime: now.Add(-2 * time.Minute)}
	fd.entries[file] = fakeDiskEntry{mtime: now.Add(-2 * time.Minute), size: 1024}

	sw := newScanner(t, fd, fs, &now)
	items, err := sw.GetItems("/watch", 30*time.Second)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d (%+v)", len(items), items)
	}
	got := items[0]
	if got.Title == "" || got.Hash == "" {
		t.Errorf("incomplete item: %+v", got)
	}
	if got.TotalSize != 1024 {
		t.Errorf("TotalSize = %d, want 1024", got.TotalSize)
	}

	// Second call after the grace period elapses → still completed,
	// hash unchanged → LastChanged carried forward; remaining=0
	// (zero pointer present).
	now = now.Add(45 * time.Second)
	items, err = sw.GetItems("/watch", 30*time.Second)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("second call len(items) = %d", len(items))
	}
	if items[0].Status != downloadclients.DownloadItemStatusCompleted {
		t.Errorf("Status = %v, want Completed (grace elapsed)", items[0].Status)
	}
}

func TestScanWatchFolder_FolderArm_DownloadingDuringGrace(t *testing.T) {
	fd := newFakeDisk()
	fs := newFakeScanner()
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	folder := "/w/InFlight"
	file := "/w/InFlight/part01.rar"
	fd.dirs["/w"] = []string{folder}
	fd.files[folder] = []string{file}
	fd.entries[folder] = fakeDiskEntry{isDir: true, mtime: now}
	fd.entries[file] = fakeDiskEntry{mtime: now, size: 100}

	sw := newScanner(t, fd, fs, &now)
	items, err := sw.GetItems("/w", 60*time.Second)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d", len(items))
	}
	if items[0].Status != downloadclients.DownloadItemStatusDownloading {
		t.Errorf("Status = %v, want Downloading inside grace", items[0].Status)
	}
	if items[0].RemainingTime == nil || *items[0].RemainingTime <= 0 {
		t.Errorf("RemainingTime = %v, want positive", items[0].RemainingTime)
	}
}

func TestScanWatchFolder_FolderArm_HashChangeResetsGrace(t *testing.T) {
	fd := newFakeDisk()
	fs := newFakeScanner()
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	folder := "/w/Changing"
	file := "/w/Changing/episode.mkv"
	fd.dirs["/w"] = []string{folder}
	fd.files[folder] = []string{file}
	fd.entries[folder] = fakeDiskEntry{isDir: true, mtime: now}
	fd.entries[file] = fakeDiskEntry{mtime: now, size: 100}

	sw := newScanner(t, fd, fs, &now)
	first, _ := sw.GetItems("/w", 60*time.Second)
	if len(first) != 1 {
		t.Fatalf("first len = %d", len(first))
	}

	// 40s later — past the original grace deadline — but the file
	// grew, so hash changes and the grace clock resets.
	now = now.Add(40 * time.Second)
	fd.entries[file] = fakeDiskEntry{mtime: now, size: 250}
	second, _ := sw.GetItems("/w", 60*time.Second)
	if len(second) != 1 {
		t.Fatalf("second len = %d", len(second))
	}
	if second[0].Hash == first[0].Hash {
		t.Errorf("hash didn't change after file growth")
	}
	if second[0].Status != downloadclients.DownloadItemStatusDownloading {
		t.Errorf("Status = %v, want Downloading after hash flip", second[0].Status)
	}
}

func TestScanWatchFolder_FolderArm_LockedFileFlipsDownloading(t *testing.T) {
	fd := newFakeDisk()
	fs := newFakeScanner()
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	folder := "/w/Locked"
	file := "/w/Locked/big.bin"
	fd.dirs["/w"] = []string{folder}
	fd.files[folder] = []string{file}
	fd.entries[folder] = fakeDiskEntry{isDir: true, mtime: now.Add(-5 * time.Minute)}
	fd.entries[file] = fakeDiskEntry{mtime: now.Add(-5 * time.Minute), size: 100, locked: true}

	sw := newScanner(t, fd, fs, &now)
	// First call sets LastChanged=now; second call past grace +
	// hash-stable verifies the lock probe holds Status at Downloading
	// even after the timer would otherwise have ended.
	first, _ := sw.GetItems("/w", 5*time.Second)
	if len(first) != 1 || first[0].Status != downloadclients.DownloadItemStatusDownloading {
		t.Fatalf("first = %+v, want one Downloading", first)
	}
	now = now.Add(20 * time.Second)
	items, _ := sw.GetItems("/w", 5*time.Second)
	if len(items) != 1 {
		t.Fatalf("len = %d", len(items))
	}
	if items[0].Status != downloadclients.DownloadItemStatusDownloading {
		t.Errorf("Status = %v, want Downloading from lock probe even past grace", items[0].Status)
	}
	if items[0].RemainingTime != nil {
		t.Errorf("RemainingTime = %v, want nil under locked branch past grace", items[0].RemainingTime)
	}
}

func TestScanWatchFolder_VideoFileArm(t *testing.T) {
	fd := newFakeDisk()
	fs := newFakeScanner()
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	video := "/w/loose.mkv"
	fd.entries[video] = fakeDiskEntry{mtime: now.Add(-2 * time.Minute), size: 4096}
	fs.videoFiles["/w"] = []string{video}

	sw := newScanner(t, fd, fs, &now)
	first, err := sw.GetItems("/w", 30*time.Second)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("len(first) = %d", len(first))
	}
	if !strings.Contains(first[0].DownloadID, "loose.mkv") {
		t.Errorf("DownloadID = %q, want to embed basename", first[0].DownloadID)
	}
	if first[0].TotalSize != 4096 {
		t.Errorf("TotalSize = %d, want 4096", first[0].TotalSize)
	}
	if first[0].Status != downloadclients.DownloadItemStatusDownloading {
		t.Errorf("first-pass Status = %v, want Downloading (grace clock starts now)", first[0].Status)
	}

	// Advance past the grace period; same hash → LastChanged carries
	// forward from the first call → grace elapsed → Completed.
	now = now.Add(45 * time.Second)
	items, _ := sw.GetItems("/w", 30*time.Second)
	if len(items) != 1 {
		t.Fatalf("len(items) = %d", len(items))
	}
	if items[0].Status != downloadclients.DownloadItemStatusCompleted {
		t.Errorf("Status = %v, want Completed (grace elapsed second pass)", items[0].Status)
	}
}

func TestScanWatchFolder_SpecialFolderSkipped(t *testing.T) {
	fd := newFakeDisk()
	fs := newFakeScanner()
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	special := "/w/@eadir"
	ok := "/w/Show"
	fd.dirs["/w"] = []string{special, ok}
	fd.entries[special] = fakeDiskEntry{isDir: true, mtime: now}
	fd.entries[ok] = fakeDiskEntry{isDir: true, mtime: now.Add(-5 * time.Minute)}

	sw := newScanner(t, fd, fs, &now)
	items, _ := sw.GetItems("/w", time.Second)
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1 (special folder filtered)", len(items))
	}
	if !strings.Contains(items[0].DownloadID, "Show") {
		t.Errorf("DownloadID = %q, want Show entry", items[0].DownloadID)
	}
}

func TestScanWatchFolder_FilterPathsHonoured(t *testing.T) {
	fd := newFakeDisk()
	fs := newFakeScanner()
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	ignored := "/w/IgnoreMe"
	kept := "/w/Keep"
	fd.dirs["/w"] = []string{ignored, kept}
	fd.entries[ignored] = fakeDiskEntry{isDir: true, mtime: now}
	fd.entries[kept] = fakeDiskEntry{isDir: true, mtime: now.Add(-5 * time.Minute)}
	fs.ignored[ignored] = true

	sw := newScanner(t, fd, fs, &now)
	items, _ := sw.GetItems("/w", time.Second)
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if !strings.Contains(items[0].DownloadID, filepath.Base(kept)) {
		t.Errorf("DownloadID = %q, want %q", items[0].DownloadID, kept)
	}
}

func TestScanWatchFolder_CacheTTLEvictsAfter5Minutes(t *testing.T) {
	fd := newFakeDisk()
	fs := newFakeScanner()
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	folder := "/w/Show"
	file := "/w/Show/ep.mkv"
	fd.dirs["/w"] = []string{folder}
	fd.files[folder] = []string{file}
	fd.entries[folder] = fakeDiskEntry{isDir: true, mtime: now}
	fd.entries[file] = fakeDiskEntry{mtime: now, size: 10}

	sw := newScanner(t, fd, fs, &now)
	first, _ := sw.GetItems("/w", 60*time.Second)
	if len(first) != 1 || first[0].Status != downloadclients.DownloadItemStatusDownloading {
		t.Fatalf("first = %+v, want one Downloading", first)
	}

	// Jump well past cacheTTL — the cache entry expires. The next
	// call rehydrates from scratch (no prior LastChanged) so the
	// item is Downloading again even though wall time advanced
	// past the original grace deadline.
	now = now.Add(6 * time.Minute)
	second, _ := sw.GetItems("/w", 60*time.Second)
	if len(second) != 1 {
		t.Fatalf("second len = %d", len(second))
	}
	if second[0].Status != downloadclients.DownloadItemStatusDownloading {
		t.Errorf("Status after TTL eviction = %v, want fresh Downloading", second[0].Status)
	}
}

func TestScanWatchFolder_StableIDAcrossCalls(t *testing.T) {
	fd := newFakeDisk()
	fs := newFakeScanner()
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	folder := "/w/S"
	file := "/w/S/e.mkv"
	fd.dirs["/w"] = []string{folder}
	fd.files[folder] = []string{file}
	fd.entries[folder] = fakeDiskEntry{isDir: true, mtime: now}
	fd.entries[file] = fakeDiskEntry{mtime: now, size: 1}

	sw := newScanner(t, fd, fs, &now)
	first, _ := sw.GetItems("/w", time.Second)
	now = now.Add(5 * time.Second)
	second, _ := sw.GetItems("/w", time.Second)
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("lens = %d, %d", len(first), len(second))
	}
	if first[0].DownloadID != second[0].DownloadID {
		t.Errorf("DownloadID drifted: %q vs %q (folder mtime unchanged)", first[0].DownloadID, second[0].DownloadID)
	}
}

func TestScanWatchFolder_ConcurrentWatchFoldersIndependent(t *testing.T) {
	fd := newFakeDisk()
	fs := newFakeScanner()
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	fd.dirs["/a"] = []string{"/a/Show1"}
	fd.dirs["/b"] = []string{"/b/Show2"}
	fd.entries["/a/Show1"] = fakeDiskEntry{isDir: true, mtime: now.Add(-2 * time.Hour)}
	fd.entries["/b/Show2"] = fakeDiskEntry{isDir: true, mtime: now}
	fd.files["/a/Show1"] = nil
	fd.files["/b/Show2"] = nil

	sw := newScanner(t, fd, fs, &now)
	a, _ := sw.GetItems("/a", time.Minute)
	b, _ := sw.GetItems("/b", time.Minute)
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("lens a=%d b=%d", len(a), len(b))
	}
	// Per-folder cache: second call to /a does not see /b's state.
	a2, _ := sw.GetItems("/a", time.Minute)
	if a[0].DownloadID != a2[0].DownloadID {
		t.Errorf("per-folder cache leaked: ID drifted on /a")
	}
}

func TestScanWatchFolder_RequiresDisk(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on nil disk")
		}
	}()
	blackhole.NewScanWatchFolder(nil, nil, nil, nil)
}

func TestScanWatchFolder_SortedOutput(t *testing.T) {
	fd := newFakeDisk()
	fs := newFakeScanner()
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	for _, f := range []string{"/w/C", "/w/A", "/w/B"} {
		fd.entries[f] = fakeDiskEntry{isDir: true, mtime: now.Add(-2 * time.Hour)}
	}
	fd.dirs["/w"] = []string{"/w/C", "/w/A", "/w/B"}

	sw := newScanner(t, fd, fs, &now)
	items, _ := sw.GetItems("/w", time.Second)
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.DownloadID
	}
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	for i := range ids {
		if ids[i] != sorted[i] {
			t.Fatalf("output not sorted: %v", ids)
		}
	}
}

// TestDefaultDisk_LockReportsFalse confirms the documented
// divergence — the production Disk default reports no lock.
func TestDefaultDisk_LockReportsFalse(t *testing.T) {
	locked, err := blackhole.DefaultDisk().IsFileLocked("/anything")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if locked {
		t.Errorf("locked = true, want false (POSIX has no portable equivalent)")
	}
}
