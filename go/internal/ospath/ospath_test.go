package ospath

import (
	"errors"
	"testing"
)

// Verbatim port of `src/NzbDrone.Common.Test/OsPathFixture.cs`. Each
// test below names the [Test] / [TestCase] method it ports so future
// drift against the C# tree is line-aligned.

// Ports `should_auto_detect_kind` (17 [TestCase] rows).
func TestAutoDetectKind(t *testing.T) {
	cases := []struct {
		path string
		kind Kind
	}{
		{`C:\rooted\windows\path\`, KindWindows},
		{`C:\rooted\windows\path`, KindWindows},
		{`C:\`, KindWindows},
		{`C:`, KindWindows},
		{`\\rooted\unc\path\`, KindWindows},
		{`\\rooted\unc\path`, KindWindows},
		{`\relative\windows\path\`, KindWindows},
		{`\relative\windows\path`, KindWindows},
		{`relative\windows\path\`, KindWindows},
		{`relative\windows\path`, KindWindows},
		{`relative\`, KindWindows},
		{`relative`, KindUnknown},
		{`/rooted/linux/path/`, KindUnix},
		{`/rooted/linux/path`, KindUnix},
		{`/`, KindUnix},
		{`linux/path`, KindUnix},
		{`Castle:unrooted+linux+path`, KindUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			p := New(tc.path)
			if p.Kind() != tc.kind {
				t.Fatalf("Kind: got %v want %v", p.Kind(), tc.kind)
			}
			switch tc.kind {
			case KindWindows:
				if !p.IsWindows() || p.IsUnix() {
					t.Fatalf("IsWindows/IsUnix mismatch")
				}
			case KindUnix:
				if p.IsWindows() || !p.IsUnix() {
					t.Fatalf("IsWindows/IsUnix mismatch")
				}
			default:
				if p.IsWindows() || p.IsUnix() {
					t.Fatalf("expected neither IsWindows nor IsUnix for unknown")
				}
			}
		})
	}
}

// Ports `should_add_directory_slash` plus `should_return_parent_directory`.
func TestDirectory(t *testing.T) {
	t.Run("should_add_directory_slash", func(t *testing.T) {
		p := New(`C:\rooted\windows\path\`).Directory()
		if p.String() != `C:\rooted\windows\` {
			t.Fatalf("Directory: got %q want %q", p.String(), `C:\rooted\windows\`)
		}
	})
	cases := []struct {
		path, expected string
	}{
		{`C:\rooted\windows\path`, `C:\rooted\windows\`},
		{`C:\rooted\windows\path\`, `C:\rooted\windows\`},
		{`C:\rooted`, `C:\`},
		{`C:`, ``}, // null parent → empty path
		{`/rooted/linux/path`, `/rooted/linux/`},
		{`/rooted`, `/`},
		{`/`, ``}, // null parent → empty path
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			got := New(tc.path).Directory()
			want := New(tc.expected)
			if !got.Equals(want) {
				t.Fatalf("Directory(%q): got %q want %q", tc.path, got.String(), want.String())
			}
		})
	}
}

// Ports `should_return_empty_as_parent_of_root_unc`.
func TestDirectoryRootUNCIsEmpty(t *testing.T) {
	if !New(`\\unc`).Directory().IsEmpty() {
		t.Fatalf("expected Directory of \\\\unc to be empty")
	}
}

// Ports `should_detect_rooted_ospaths` and `should_detect_unrooted_ospaths`.
func TestIsRooted(t *testing.T) {
	rooted := []string{
		`C:\rooted\windows\path`,
		`C:`,
		`\\blaat`,
		`/rooted/linux/path`,
		`/`,
	}
	for _, p := range rooted {
		if !New(p).IsRooted() {
			t.Fatalf("expected %q to be rooted", p)
		}
	}
	unrooted := []string{
		`\rooted\windows\path`,
		`rooted\windows\path`,
		`path`,
		`linux/path`,
		`Castle:unrooted+linux+path`,
		`C:unrooted+linux+path`,
	}
	for _, p := range unrooted {
		if New(p).IsRooted() {
			t.Fatalf("expected %q to NOT be rooted", p)
		}
	}
}

// Ports `should_return_filename` (9 cases). The C# fixture's `null`
// expectation maps to empty string in the Go port — there's no
// nullable-string equivalent and an empty FileName is the natural
// signal that the path has no nameable component.
func TestFileName(t *testing.T) {
	cases := []struct {
		path, expected string
	}{
		{`C:\rooted\windows\path`, `path`},
		{`C:`, `C:`},
		{`\\blaat`, `blaat`},
		{`/rooted/linux/path`, `path`},
		{`/`, ``},
		{`\rooted\windows\path\`, `path`},
		{`rooted\windows\path`, `path`},
		{`path`, `path`},
		{`linux/path`, `path`},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			if got := New(tc.path).FileName(); got != tc.expected {
				t.Fatalf("FileName(%q): got %q want %q", tc.path, got, tc.expected)
			}
		})
	}
}

// Ports `should_compare_windows_ospathkind_case_insensitive`.
func TestEqualsWindowsCaseInsensitive(t *testing.T) {
	left := New(`C:\rooted\Windows\path`)
	right := New(`C:\rooted\windows\path`)
	if !left.Equals(right) {
		t.Fatalf("expected case-insensitive equality on Windows paths")
	}
}

// Ports `should_compare_unix_ospathkind_case_sensitive`.
func TestEqualsUnixCaseSensitive(t *testing.T) {
	left := New(`/rooted/Linux/path`)
	right := New(`/rooted/linux/path`)
	if left.Equals(right) {
		t.Fatalf("expected case-sensitive inequality on Unix paths")
	}
}

// Ports `should_not_ignore_trailing_slash_during_compare`.
func TestEqualsTrailingSlashSignificant(t *testing.T) {
	left := New(`/rooted/linux/path/`)
	right := New(`/rooted/linux/path`)
	if left.Equals(right) {
		t.Fatalf("expected trailing slash to differ")
	}
	if !left.EqualsIgnoreTrailingSlash(right) {
		t.Fatalf("expected EqualsIgnoreTrailingSlash to ignore the slash")
	}
}

// Ports `should_combine_path` (10 cases).
func TestCombinePath(t *testing.T) {
	cases := []struct {
		left, right, expected string
	}{
		{`C:\Test`, `sub`, `C:\Test\sub`},
		{`C:\Test`, `sub\test`, `C:\Test\sub\test`},
		{`C:\Test\`, `\sub`, `C:\Test\sub`},
		{`C:\Test`, `sub\`, `C:\Test\sub\`},
		{`C:\Test`, `C:\Test2\sub`, `C:\Test2\sub`},
		{`/Test`, `sub`, `/Test/sub`},
		{`/Test`, `sub/`, `/Test/sub/`},
		{`/Test/`, `sub/test/`, `/Test/sub/test/`},
		{`/Test/`, `/Test2/`, `/Test2/`},
		{`C:\Test`, ``, `C:\Test`},
	}
	for _, tc := range cases {
		t.Run(tc.left+"+"+tc.right, func(t *testing.T) {
			got := New(tc.left).Add(New(tc.right))
			if got.FullPath() != tc.expected {
				t.Fatalf("Add(%q,%q): got %q want %q", tc.left, tc.right, got.FullPath(), tc.expected)
			}
		})
	}
}

// Ports `should_fix_slashes_windows`.
func TestFixSlashesWindows(t *testing.T) {
	p := New(`C:/on/windows/transmission\uses/forward/slashes`)
	if p.Kind() != KindWindows {
		t.Fatalf("kind: got %v want Windows", p.Kind())
	}
	want := `C:\on\windows\transmission\uses\forward\slashes`
	if p.FullPath() != want {
		t.Fatalf("FullPath: got %q want %q", p.FullPath(), want)
	}
}

// Ports `should_fix_slashes_unix`.
func TestFixSlashesUnix(t *testing.T) {
	p := New(`/just/a/test\to\verify the/slashes\`)
	if p.Kind() != KindUnix {
		t.Fatalf("kind: got %v want Unix", p.Kind())
	}
	want := `/just/a/test/to/verify the/slashes/`
	if p.FullPath() != want {
		t.Fatalf("FullPath: got %q want %q", p.FullPath(), want)
	}
}

// Ports `should_fix_double_slashes_unix`.
func TestFixDoubleSlashesUnix(t *testing.T) {
	p := New(`/just/a//test////to/verify the/slashes/`)
	if p.Kind() != KindUnix {
		t.Fatalf("kind: got %v want Unix", p.Kind())
	}
	want := `/just/a/test/to/verify the/slashes/`
	if p.FullPath() != want {
		t.Fatalf("FullPath: got %q want %q", p.FullPath(), want)
	}
}

// Ports `should_combine_mixed_slashes`. Right is explicitly
// constructed with [KindUnknown] to exercise the
// kind-promotes-to-Windows path.
func TestCombineMixedSlashes(t *testing.T) {
	left := New(`C:/on/windows/transmission`)
	right := NewWithKind(`uses/forward/slashes`, KindUnknown)
	p := left.Add(right)
	if p.Kind() != KindWindows {
		t.Fatalf("kind: got %v want Windows", p.Kind())
	}
	want := `C:\on\windows\transmission\uses\forward\slashes`
	if p.FullPath() != want {
		t.Fatalf("FullPath: got %q want %q", p.FullPath(), want)
	}
}

// Ports `should_create_relative_path` (3 cases).
func TestRelativePath(t *testing.T) {
	cases := []struct {
		parent, child, expected string
	}{
		{`C:\Test\Data\`, `C:\Test\Data\Sub\Folder`, `Sub\Folder`},
		{`C:\Test\Data\`, `C:\Test\Data2\Sub\Folder`, `..\Data2\Sub\Folder`},
		{`/parent/folder`, `/parent/folder/Sub/Folder`, `Sub/Folder`},
	}
	for _, tc := range cases {
		t.Run(tc.parent+"<-"+tc.child, func(t *testing.T) {
			got := New(tc.child).Sub(New(tc.parent))
			if got.Kind() != KindUnknown {
				t.Fatalf("kind: got %v want Unknown", got.Kind())
			}
			if got.FullPath() != tc.expected {
				t.Fatalf("Sub: got %q want %q", got.FullPath(), tc.expected)
			}
		})
	}
}

// Ports `should_parse_null_as_empty`. Go has no nullable-string analogue;
// the empty string is the natural stand-in.
func TestNullParsesAsEmpty(t *testing.T) {
	p := New("")
	if p.FullPath() != "" {
		t.Fatalf("FullPath: got %q want empty", p.FullPath())
	}
	if !p.IsEmpty() {
		t.Fatalf("expected IsEmpty to be true")
	}
}

// Ports `should_evaluate_contains` (3 cases).
func TestContains(t *testing.T) {
	cases := []struct {
		parent, child string
		expected      bool
	}{
		{`C:\Test\`, `C:\Test`, true},
		{`C:\Test\`, `C:\Test\Contains\`, true},
		{`C:\Test\`, `C:\Other\`, false},
	}
	for _, tc := range cases {
		t.Run(tc.parent+"<>"+tc.child, func(t *testing.T) {
			if got := New(tc.parent).Contains(New(tc.child)); got != tc.expected {
				t.Fatalf("Contains: got %v want %v", got, tc.expected)
			}
		})
	}
}

// Extra coverage beyond the C# fixture: verifies the Null helper /
// zero-value invariants and the AsDirectory normalization on each kind.
func TestNullAndAsDirectory(t *testing.T) {
	if !Null().IsEmpty() {
		t.Fatalf("expected Null().IsEmpty() to be true")
	}
	if Null().Kind() != KindUnknown {
		t.Fatalf("expected Null().Kind() == Unknown")
	}
	if got := New(`C:\foo`).AsDirectory().FullPath(); got != `C:\foo\` {
		t.Fatalf("AsDirectory(Windows): got %q want %q", got, `C:\foo\`)
	}
	if got := New(`/foo`).AsDirectory().FullPath(); got != `/foo/` {
		t.Fatalf("AsDirectory(Unix): got %q want %q", got, `/foo/`)
	}
	// Already-trailed paths shouldn't double-up.
	if got := New(`C:\foo\`).AsDirectory().FullPath(); got != `C:\foo\` {
		t.Fatalf("AsDirectory idempotent (Windows): got %q", got)
	}
	if got := New(`/foo/`).AsDirectory().FullPath(); got != `/foo/` {
		t.Fatalf("AsDirectory idempotent (Unix): got %q", got)
	}
}

// Extra coverage: PathWithoutTrailingSlash should preserve drive roots
// `C:\` and the Unix root `/` while stripping ordinary trailing
// separators. These invariants are load-bearing for the Directory
// walk and the relative-path subtraction.
func TestPathWithoutTrailingSlash(t *testing.T) {
	cases := []struct{ in, want string }{
		{`C:\`, `C:\`},
		{`/`, `/`},
		{`C:\foo\`, `C:\foo`},
		{`/foo/`, `/foo`},
		{`C:\foo`, `C:\foo`},
		{`/foo`, `/foo`},
	}
	for _, tc := range cases {
		if got := New(tc.in).PathWithoutTrailingSlash(); got != tc.want {
			t.Fatalf("PathWithoutTrailingSlash(%q): got %q want %q", tc.in, got, tc.want)
		}
	}
}

// Extra coverage: Add panics on truly mismatched kinds, mirroring the
// C# Exception throw at `OsPath.cs:422-425`. Caller-facing programmer
// error; tests pin the behaviour so a future refactor doesn't silently
// soften it.
func TestAddMismatchedKindsPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on mismatched kinds")
		}
	}()
	_ = New(`C:\Test`).Add(New(`/unix/path`))
}

// Extra coverage: Sub panics when either side is unrooted. Mirrors the
// C# ArgumentException at `OsPath.cs:457-460`.
func TestSubUnrootedPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on unrooted Sub")
		}
	}()
	_ = New(`relative\path`).Sub(New(`C:\rooted`))
}

// Pins the contract for the package-level [Ext] helper. Both `/` and
// `\` are recognised as separators so a single helper covers Unix and
// Windows-shaped inputs identically.
func TestExt(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"foo", ""},
		{"foo.mkv", ".mkv"},
		{"foo.tar.gz", ".gz"},
		{"foo.", "."},
		{".bashrc", ".bashrc"},
		{"/tv/series/season 1/episode.mp4", ".mp4"},
		{"/tv/series.with.dots/episode", ""},
		{`C:\tv\series\episode.mkv`, ".mkv"},
		{`C:\tv\series.dotted\episode`, ""},
		{"/tv/series/", ""},
		{`C:\tv\series\`, ""},
		{`/tv/.hidden/file`, ""},
		{`/tv/.hidden/file.mkv`, ".mkv"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := Ext(tc.in); got != tc.want {
				t.Fatalf("Ext(%q): got %q want %q", tc.in, got, tc.want)
			}
		})
	}
}

// Pins the contract for the package-level [Base] helper. Mirrors
// .NET 7+ `Path.GetFileName` semantics: a trailing separator yields
// the empty string.
func TestBase(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"foo", "foo"},
		{"foo.mkv", "foo.mkv"},
		{"/tv/series/episode.mkv", "episode.mkv"},
		{`C:\tv\series\episode.mkv`, "episode.mkv"},
		{"/tv/series", "series"},
		{"/tv/series/", ""},
		{`C:\tv\series\`, ""},
		{"/", ""},
		{`\\share\folder\file.txt`, "file.txt"},
		{"mixed/sep\\file.mkv", "file.mkv"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := Base(tc.in); got != tc.want {
				t.Fatalf("Base(%q): got %q want %q", tc.in, got, tc.want)
			}
		})
	}
}

// Pins the contract for the package-level [IsParent] helper.
// Strict-ancestry: a path is NOT a parent of itself. Case-insensitive
// when either side is Windows-shaped.
func TestIsParent(t *testing.T) {
	cases := []struct {
		name   string
		parent string
		child  string
		want   bool
	}{
		{"unix immediate child", "/tv/series", "/tv/series/season 1", true},
		{"unix grandchild", "/tv/series", "/tv/series/season 1/episode.mkv", true},
		{"unix sibling", "/tv/series", "/tv/other/file", false},
		{"unix prefix-not-ancestor", "/tv/foo", "/tv/foobar/file", false},
		{"unix exact equality", "/tv/series", "/tv/series", false},
		{"unix trailing slash on parent", "/tv/series/", "/tv/series/episode.mkv", true},
		{"unix trailing slash on child", "/tv/series", "/tv/series/episode.mkv/", true},

		{"windows immediate child", `C:\tv\series`, `C:\tv\series\episode.mkv`, true},
		{"windows case-insensitive parent", `C:\TV\Series`, `c:\tv\series\episode.mkv`, true},
		{"windows case-sensitive prefix-not-ancestor", `C:\tv\foo`, `C:\tv\foobar\file`, false},
		{"windows different drive", `C:\tv`, `D:\tv\episode.mkv`, false},
		{"windows UNC", `\\nas\tv`, `\\nas\tv\series\episode.mkv`, true},

		{"mismatched shapes (unix parent, windows child)", "/tv/series", `C:\tv\series\episode.mkv`, false},

		{"empty parent", "", "/tv/series/episode.mkv", false},
		{"empty child", "/tv/series", "", false},
		{"both empty", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsParent(tc.parent, tc.child); got != tc.want {
				t.Fatalf("IsParent(%q, %q): got %v want %v", tc.parent, tc.child, got, tc.want)
			}
		})
	}
}

// Pins the contract for the package-level [Relative] helper.
func TestRelative(t *testing.T) {
	cases := []struct {
		name   string
		parent string
		child  string
		want   string
	}{
		{"unix immediate", "/tv/series", "/tv/series/episode.mkv", "episode.mkv"},
		{"unix nested", "/tv/series", "/tv/series/season 1/episode.mkv", "season 1/episode.mkv"},
		{"unix trailing-slash parent", "/tv/series/", "/tv/series/season 1/episode.mkv", "season 1/episode.mkv"},
		{"unix trailing-slash child", "/tv/series", "/tv/series/episode.mkv/", "episode.mkv"},

		{"windows immediate", `C:\tv\series`, `C:\tv\series\episode.mkv`, "episode.mkv"},
		{"windows case-different parent", `C:\TV\Series`, `C:\tv\series\season 1\ep.mkv`, `season 1\ep.mkv`},
		{"windows UNC", `\\nas\tv`, `\\nas\tv\series\episode.mkv`, `series\episode.mkv`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Relative(tc.parent, tc.child)
			if err != nil {
				t.Fatalf("Relative(%q, %q): unexpected error %v", tc.parent, tc.child, err)
			}
			if got != tc.want {
				t.Fatalf("Relative(%q, %q): got %q want %q", tc.parent, tc.child, got, tc.want)
			}
		})
	}
}

// Pins the error path: not-a-parent produces [ErrNotParent].
func TestRelativeNotParent(t *testing.T) {
	cases := []struct {
		name   string
		parent string
		child  string
	}{
		{"sibling", "/tv/series", "/tv/other/file"},
		{"prefix-not-ancestor", "/tv/foo", "/tv/foobar/file"},
		{"equal paths", "/tv/series", "/tv/series"},
		{"empty parent", "", "/tv/series/episode.mkv"},
		{"empty child", "/tv/series", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Relative(tc.parent, tc.child)
			if err == nil {
				t.Fatalf("Relative(%q, %q): want error, got %q", tc.parent, tc.child, got)
			}
			if !errors.Is(err, ErrNotParent) {
				t.Fatalf("Relative(%q, %q): error not wrapping ErrNotParent: %v", tc.parent, tc.child, err)
			}
			if got != "" {
				t.Fatalf("Relative(%q, %q): want empty string on error, got %q", tc.parent, tc.child, got)
			}
		})
	}
}
