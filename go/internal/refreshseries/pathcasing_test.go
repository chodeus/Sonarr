package refreshseries

import (
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

// TestGetActualCasing_IsNoOp pins the Go-port's deferred-Windows-casing
// behaviour: the function returns its input unchanged on all platforms
// until the dedicated Windows slice lands. Documents the contract so
// a future Windows-casing implementation knows to add a build-tagged
// override and adjust this expectation.
func TestGetActualCasing_IsNoOp(t *testing.T) {
	t.Parallel()
	cases := []string{
		"/tmp/refresh/show",
		"/MixedCase/Path",
		"",
		"relative",
	}
	for _, in := range cases {
		if got := getActualCasing(in); got != in {
			t.Errorf("getActualCasing(%q) = %q, want %q (no-op until Windows slice lands)", in, got, in)
		}
	}
}
