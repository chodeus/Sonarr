package mediafiles

import (
	"errors"
	"reflect"
	"testing"
)

// stubDiskProvider implements [DiskProvider] for the scan tests. Only
// `GetFiles` carries behaviour; the other methods panic if called so
// drift in the surface under test is loud. The recursive flag is
// captured for the allDirectories-toggle test.
type stubDiskProvider struct {
	files        []string
	err          error
	calls        int
	gotPath      string
	gotRecursive bool
}

func (s *stubDiskProvider) FolderExists(string) bool { panic("FolderExists: not exercised") }
func (s *stubDiskProvider) FolderEmpty(string) bool  { panic("FolderEmpty: not exercised") }
func (s *stubDiskProvider) GetFiles(path string, recursive bool) ([]string, error) {
	s.calls++
	s.gotPath = path
	s.gotRecursive = recursive
	if s.err != nil {
		return nil, s.err
	}
	return s.files, nil
}
func (s *stubDiskProvider) CreateFolder(string) error { panic("CreateFolder: not exercised") }
func (s *stubDiskProvider) RemoveEmptySubfolders(string) error {
	panic("RemoveEmptySubfolders: not exercised")
}
func (s *stubDiskProvider) DeleteFolder(string, bool) error { panic("DeleteFolder: not exercised") }
func (s *stubDiskProvider) SetPermissions(string, string, string) error {
	panic("SetPermissions: not exercised")
}
func (s *stubDiskProvider) GetFileSize(string) (int64, error) { panic("GetFileSize: not exercised") }

// TestGetVideoFilesFiltersNonMedia pins the predicate — only files
// whose extension is in the MediaFileExtensions registry survive.
// Mirrors the LINQ Where at `DiskScanService.cs:208`.
func TestGetVideoFilesFiltersNonMedia(t *testing.T) {
	disk := &stubDiskProvider{
		files: []string{
			"/series/Show/S01/episode.mkv",
			"/series/Show/S01/episode.nfo", // not a media extension
			"/series/Show/S01/poster.jpg",  // not a media extension
			"/series/Show/S01/episode.mp4",
			"/series/Show/S01/sample.txt", // not a media extension
		},
	}
	got, err := GetVideoFiles(disk, "/series/Show", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{
		"/series/Show/S01/episode.mkv",
		"/series/Show/S01/episode.mp4",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GetVideoFiles: got %v, want %v", got, want)
	}
}

// TestGetVideoFilesPreservesOrder pins that the disk-provider walk
// order is preserved verbatim — the C# tree relies on the underlying
// file-system enumeration order downstream (extras pass, decision
// engine), so reordering here would silently change behaviour.
func TestGetVideoFilesPreservesOrder(t *testing.T) {
	disk := &stubDiskProvider{
		files: []string{
			"/p/c.mkv",
			"/p/a.mp4",
			"/p/b.avi",
		},
	}
	got, err := GetVideoFiles(disk, "/p", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"/p/c.mkv", "/p/a.mp4", "/p/b.avi"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order: got %v want %v", got, want)
	}
}

// TestGetVideoFilesRecursiveFlag pins that the recursive parameter is
// passed through to the disk provider unchanged. Mirrors the
// `allDirectories` C# parameter that the C# scan-skipped vs main path
// toggles.
func TestGetVideoFilesRecursiveFlag(t *testing.T) {
	for _, recursive := range []bool{true, false} {
		disk := &stubDiskProvider{}
		_, err := GetVideoFiles(disk, "/p", recursive)
		if err != nil {
			t.Fatalf("recursive=%v: unexpected error: %v", recursive, err)
		}
		if disk.gotRecursive != recursive {
			t.Fatalf("recursive=%v: disk.GetFiles got recursive=%v", recursive, disk.gotRecursive)
		}
		if disk.gotPath != "/p" {
			t.Fatalf("recursive=%v: disk.GetFiles got path=%q want %q", recursive, disk.gotPath, "/p")
		}
	}
}

// TestGetVideoFilesEmptyResult pins that an empty walk yields a
// non-nil empty slice — callers can range over the result without a
// nil-guard.
func TestGetVideoFilesEmptyResult(t *testing.T) {
	disk := &stubDiskProvider{files: nil}
	got, err := GetVideoFiles(disk, "/p", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil empty slice; got nil")
	}
	if len(got) != 0 {
		t.Fatalf("expected empty slice; got %v", got)
	}
}

// TestGetNonVideoFilesInvertsPredicate pins the inversion — only files
// whose extension is NOT in the MediaFileExtensions registry survive.
// Mirrors the negated LINQ Where at `DiskScanService.cs:223`.
func TestGetNonVideoFilesInvertsPredicate(t *testing.T) {
	disk := &stubDiskProvider{
		files: []string{
			"/p/episode.mkv",
			"/p/episode.nfo",
			"/p/poster.jpg",
			"/p/episode.mp4",
			"/p/subtitle.srt",
		},
	}
	got, err := GetNonVideoFiles(disk, "/p", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"/p/episode.nfo", "/p/poster.jpg", "/p/subtitle.srt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GetNonVideoFiles: got %v want %v", got, want)
	}
}

// TestGetVideoFilesPropagatesErr pins that disk-provider errors
// surface unwrapped — the Service layer (slice 4) decides how to react;
// the helpers don't swallow.
func TestGetVideoFilesPropagatesErr(t *testing.T) {
	wantErr := errors.New("disk: permission denied")
	disk := &stubDiskProvider{err: wantErr}
	got, err := GetVideoFiles(disk, "/p", true)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected error %v, got %v", wantErr, err)
	}
	if got != nil {
		t.Fatalf("expected nil slice on error; got %v", got)
	}
}

// TestGetNonVideoFilesPropagatesErr — same contract for the inverse.
func TestGetNonVideoFilesPropagatesErr(t *testing.T) {
	wantErr := errors.New("disk: i/o error")
	disk := &stubDiskProvider{err: wantErr}
	got, err := GetNonVideoFiles(disk, "/p", true)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected error %v, got %v", wantErr, err)
	}
	if got != nil {
		t.Fatalf("expected nil slice on error; got %v", got)
	}
}

// TestGetVideoFilesCaseInsensitiveExt pins the case-insensitivity of
// the extension match — the underlying registry is lowercase but
// real filesystems frequently emit uppercase or mixed-case extensions
// (especially on Windows / macOS HFS+).
func TestGetVideoFilesCaseInsensitiveExt(t *testing.T) {
	disk := &stubDiskProvider{
		files: []string{"/p/A.MKV", "/p/B.Mp4", "/p/C.AVI"},
	}
	got, err := GetVideoFiles(disk, "/p", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"/p/A.MKV", "/p/B.Mp4", "/p/C.AVI"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("case-insensitive: got %v want %v", got, want)
	}
}
