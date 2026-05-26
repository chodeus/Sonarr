package refreshseries

import (
	"io/fs"
	"path/filepath"
	"testing"
)

func TestNormalizePath_CleansDotAndDoubleDot(t *testing.T) {
	t.Parallel()
	// filepath.Clean(input) gives the platform-correct expected form;
	// on Linux the literal expectation would be "/tmp/refresh/show",
	// on Windows it would use backslashes — using filepath.Clean as the
	// oracle keeps the test portable.
	in := "/tmp/refresh/./test/../show"
	wantTail := filepath.Clean(in)
	got, err := normalizePath(in)
	if err != nil {
		t.Fatalf("normalizePath: %v", err)
	}
	if got != wantTail {
		t.Errorf("got %q, want %q (cleaned form)", got, wantTail)
	}
}

func TestNormalizePath_CollapsesDoubleSeparators(t *testing.T) {
	t.Parallel()
	in := "/tmp//refresh///show"
	want := filepath.Clean(in)
	got, err := normalizePath(in)
	if err != nil {
		t.Fatalf("normalizePath: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNormalizePath_IdempotentOnCleanInput(t *testing.T) {
	t.Parallel()
	in := filepath.Clean("/tmp/refresh/show")
	got, err := normalizePath(in)
	if err != nil {
		t.Fatalf("normalizePath: %v", err)
	}
	if got != in {
		t.Errorf("got %q, want %q (no-op on already-clean input)", got, in)
	}
}

func TestNormalizePath_EmptyReturnsError(t *testing.T) {
	t.Parallel()
	if _, err := normalizePath(""); err == nil {
		t.Fatal("normalizePath(\"\"): want error, got nil")
	}
}

func TestNormalizePath_ResolvesRelativeAgainstCwd(t *testing.T) {
	t.Parallel()
	got, err := normalizePath("relative/path")
	if err != nil {
		t.Fatalf("normalizePath: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("got %q, want absolute path (filepath.Abs resolution)", got)
	}
}

// fakeReadDir builds a readDir callback suitable for caseFixWalker
// from a `map[parent][]name` fixture. Path comparisons are exact —
// the caller is responsible for using the same separator the walker
// reconstructs (filepath.Clean output style).
type fakeEntry struct {
	name string
	dir  bool
}

func (e fakeEntry) Name() string               { return e.name }
func (e fakeEntry) IsDir() bool                { return e.dir }
func (e fakeEntry) Type() fs.FileMode          { return 0 }
func (e fakeEntry) Info() (fs.FileInfo, error) { return nil, nil }

func fakeReadDir(t *testing.T, fixture map[string][]string) func(string) ([]fs.DirEntry, error) {
	t.Helper()
	return func(p string) ([]fs.DirEntry, error) {
		entries, ok := fixture[p]
		if !ok {
			return nil, fs.ErrNotExist
		}
		out := make([]fs.DirEntry, len(entries))
		for i, e := range entries {
			out[i] = fakeEntry{name: e, dir: true}
		}
		return out, nil
	}
}

// TestCaseFixWalker_RecoversOnDiskCasing — the happy-path Windows
// behaviour: a mangled-case input lookup returns the case present on
// disk. Mirrors the C# regression tests at
// `src/NzbDrone.Common.Test/PathExtensionFixture.cs:247-263`.
func TestCaseFixWalker_RecoversOnDiskCasing(t *testing.T) {
	t.Parallel()
	// Fixture: /tmp contains "Refresh"; /tmp/Refresh contains "Show".
	fixture := map[string][]string{
		"/":            {"tmp"},
		"/tmp":         {"Refresh"},
		"/tmp/Refresh": {"Show"},
	}
	got := caseFixWalker("/tmp/REFRESH/show", fakeReadDir(t, fixture))
	if got != "/tmp/Refresh/Show" {
		t.Errorf("got %q, want /tmp/Refresh/Show", got)
	}
}

// TestCaseFixWalker_PreservesInputOnMissingSegment — when ReadDir
// fails on a parent, the segment retains input casing and the walker
// continues. Mirrors the C# fallback at `:204-207` where folderName
// defaults to `dirInfo.Name`.
func TestCaseFixWalker_PreservesInputOnMissingSegment(t *testing.T) {
	t.Parallel()
	// /tmp exists with "Refresh" inside, but /tmp/Refresh's
	// contents aren't in the fixture → ReadDir fails for that leaf.
	fixture := map[string][]string{
		"/":    {"tmp"},
		"/tmp": {"Refresh"},
	}
	// "show" leaf cannot be matched (no fixture entry for parent
	// /tmp/Refresh) → input casing preserved verbatim.
	got := caseFixWalker("/tmp/REFRESH/show", fakeReadDir(t, fixture))
	if got != "/tmp/Refresh/show" {
		t.Errorf("got %q, want /tmp/Refresh/show", got)
	}
}

// TestCaseFixWalker_PreservesInputOnNoMatch — parent ReadDir succeeds
// but no entry matches case-insensitively → input casing preserved.
func TestCaseFixWalker_PreservesInputOnNoMatch(t *testing.T) {
	t.Parallel()
	fixture := map[string][]string{
		"/":    {"tmp"},
		"/tmp": {"OtherDir"}, // no entry matching "Refresh"
	}
	got := caseFixWalker("/tmp/refresh", fakeReadDir(t, fixture))
	if got != "/tmp/refresh" {
		t.Errorf("got %q, want /tmp/refresh", got)
	}
}

// TestCaseFixWalker_UppercasesRoot — drive letter / root path
// is upper-cased to match C# `:199`. On POSIX root "/" is identity
// under ToUpper so this also pins the no-regression case.
func TestCaseFixWalker_UppercasesRoot(t *testing.T) {
	t.Parallel()
	// We exercise via a path whose recursion terminates at the
	// platform root. On POSIX root is "/" which ToUpper("/") == "/".
	fixture := map[string][]string{
		"/": {"Tmp"},
	}
	got := caseFixWalker("/TMP", fakeReadDir(t, fixture))
	if got != "/Tmp" {
		t.Errorf("got %q, want /Tmp", got)
	}
}

// TestCaseFixWalker_ReadDirAlwaysErrors — every parent lookup fails;
// every segment retains input casing.
func TestCaseFixWalker_ReadDirAlwaysErrors(t *testing.T) {
	t.Parallel()
	readDir := func(string) ([]fs.DirEntry, error) {
		return nil, fs.ErrPermission
	}
	got := caseFixWalker("/tmp/Refresh/Show", readDir)
	if got != "/tmp/Refresh/Show" {
		t.Errorf("got %q, want /tmp/Refresh/Show", got)
	}
}
