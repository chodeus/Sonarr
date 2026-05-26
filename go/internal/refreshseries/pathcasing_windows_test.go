//go:build windows

package refreshseries

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGetActualCasing_WalksParents creates a real temp dir tree with
// known casing and asserts getActualCasing recovers the on-disk shape
// when called with a mangled-case input. Only runs on Windows (where
// the function is non-trivial — POSIX builds use the no-op variant
// in pathcasing_other.go).
//
// J248.
func TestGetActualCasing_WalksParents(t *testing.T) {
	root := t.TempDir()
	// Build a tree: <root>/MixedCase/SubDir/leaf.txt
	mixedPath := filepath.Join(root, "MixedCase", "SubDir")
	if err := os.MkdirAll(mixedPath, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	leaf := filepath.Join(mixedPath, "Leaf.txt")
	if err := os.WriteFile(leaf, []byte{}, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Lookup with mangled casing — should recover.
	mangled := filepath.Join(root, "MIXEDCASE", "subdir", "LEAF.txt")
	got := getActualCasing(mangled)
	if got != leaf {
		t.Errorf("getActualCasing(%q) = %q, want %q", mangled, got, leaf)
	}
}

// TestGetActualCasing_PreservesNonExistentTail — when the leaf
// segment doesn't exist on disk, the walker keeps the parent casing
// recovered + the input casing for the tail. Mirrors the C# fallback
// at `PathExtensions.cs:204-207`.
func TestGetActualCasing_PreservesNonExistentTail(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "RealParent")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	mangled := filepath.Join(root, "realparent", "doesNotExist.txt")
	got := getActualCasing(mangled)
	want := filepath.Join(root, "RealParent", "doesNotExist.txt")
	if got != want {
		t.Errorf("getActualCasing(%q) = %q, want %q", mangled, got, want)
	}
}

// TestGetActualCasing_UNCReturnsUnchanged pins the C# StartsWith("\\\\")
// guard at `:215-218` — UNC paths are returned as-is because parent-
// walking via os.ReadDir on a network share is unreliable.
func TestGetActualCasing_UNCReturnsUnchanged(t *testing.T) {
	cases := []string{
		`\\server\share\folder`,
		`\foo\bar`,
	}
	for _, in := range cases {
		if got := getActualCasing(in); got != in {
			t.Errorf("getActualCasing(%q) = %q, want unchanged", in, got)
		}
	}
}

// TestGetActualCasing_Empty pins the zero-input contract — empty
// string returns empty string (avoids a crash inside filepath.Dir
// recursion on the empty string).
func TestGetActualCasing_Empty(t *testing.T) {
	if got := getActualCasing(""); got != "" {
		t.Errorf("getActualCasing(\"\") = %q, want \"\"", got)
	}
}

// TestGetActualCasing_UppercaseDriveLetter — the drive-letter
// segment is upper-cased even if input was lowercase, matching
// C# GetProperCapitalization at `:199` `dirInfo.Name.ToUpper()`.
func TestGetActualCasing_UppercaseDriveLetter(t *testing.T) {
	root := t.TempDir() // typically "C:\\Users\\...\\Tmp..."
	if len(root) < 2 || root[1] != ':' {
		t.Skipf("temp dir not on a drive letter: %q", root)
	}
	// Mangle the drive letter case and walk back.
	lowerRoot := strings.ToLower(root[:1]) + root[1:]
	got := getActualCasing(lowerRoot)
	// First two chars should be the upper-cased drive letter.
	if len(got) < 2 || got[0] != strings.ToUpper(root[:1])[0] || got[1] != ':' {
		t.Errorf("got %q, want drive letter upper-cased (started from %q)", got, lowerRoot)
	}
}
