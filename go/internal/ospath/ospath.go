// Package ospath ports the C# `NzbDrone.Common.Disk.OsPath` value type
// at `src/NzbDrone.Common/Disk/OsPath.cs`. It models a file-system path
// that knows whether it's Windows-shaped (drive letters / backslashes /
// UNC), Unix-shaped (forward slashes), or unknown — independently of the
// runtime OS — and supports parent-directory walking, path combination,
// relative-path subtraction, and platform-aware case-sensitive equality.
//
// It is the prerequisite for RemotePathMappingService (which juxtaposes
// remote-host paths against local paths whose kinds may differ) and any
// other disk-aware service that needs to treat paths as data rather
// than as runtime-OS-tied values.
package ospath

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Kind enumerates the platform shape of an [OsPath]. Order matches the
// C# `OsPathKind` enum at `src/NzbDrone.Common/Disk/OsPath.cs:507-512`
// so anyone reading both files side-by-side sees the same numeric Ids.
type Kind int

// Kind constants. Order matches the C# enum so numeric values are
// stable across the port boundary.
const (
	KindUnknown Kind = iota
	KindWindows
	KindUnix
)

// OsPath is a value type wrapping a normalized path string and its
// detected [Kind]. Comparable via [OsPath.Equals]; Windows paths
// compare case-insensitively, Unix paths case-sensitively, mirroring
// the C# `Equals` at `OsPath.cs:372-398`.
//
// The zero value is the equivalent of `OsPath.Null` (empty path,
// [KindUnknown]).
type OsPath struct {
	path string
	kind Kind
}

// uncRegex matches the head of a UNC path: either `\\server\share` or
// the long-form `\\?\UNC\server\share`. Capturing group "unc" yields
// the matched prefix (without the trailing slash separator). RE2-clean
// and the case-insensitive flag mirrors the C# regex's IgnoreCase
// option at `OsPath.cs:13`.
var uncRegex = regexp.MustCompile(`(?i)(\\\\(?:\?\\UNC\\)?[^\\]+\\[^\\]+)(?:\\|$)`)

// New parses a path and detects its [Kind]. nil/empty input yields the
// empty zero value. Mirrors the single-arg C# constructor at
// `OsPath.cs:15-27`.
func New(path string) OsPath {
	if path == "" {
		return OsPath{kind: KindUnknown}
	}
	k := detectKind(path)
	return OsPath{path: fixSlashes(path, k), kind: k}
}

// NewWithKind builds an [OsPath] using the caller-supplied kind, then
// normalizes the path's slashes to match. Mirrors the two-arg C#
// constructor at `OsPath.cs:29-41`. Use this when the caller knows the
// platform (e.g. the path came off a Windows download client and the
// server happens to be Unix).
func NewWithKind(path string, kind Kind) OsPath {
	if path == "" {
		return OsPath{kind: kind}
	}
	return OsPath{path: fixSlashes(path, kind), kind: kind}
}

// Null returns the empty OsPath. Equivalent to the C# `OsPath.Null`
// static at `OsPath.cs:299`. The zero value is the same thing; this
// helper exists for cross-port readability.
func Null() OsPath { return OsPath{} }

func detectKind(path string) Kind {
	if strings.HasPrefix(path, "/") {
		return KindUnix
	}
	if hasWindowsDriveLetter(path) || strings.Contains(path, `\`) {
		return KindWindows
	}
	if strings.Contains(path, "/") {
		return KindUnix
	}
	return KindUnknown
}

func hasWindowsDriveLetter(path string) bool {
	if len(path) < 2 {
		return false
	}
	if !isLetter(path[0]) || path[1] != ':' {
		return false
	}
	if len(path) > 2 && path[2] != '\\' && path[2] != '/' {
		return false
	}
	return true
}

func isLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func fixSlashes(path string, kind Kind) string {
	switch kind {
	case KindWindows:
		return strings.ReplaceAll(path, "/", `\`)
	case KindUnix:
		path = strings.ReplaceAll(path, `\`, "/")
		for strings.Contains(path, "//") {
			path = strings.ReplaceAll(path, "//", "/")
		}
		return path
	}
	return path
}

func trimTrailingSlash(path string, kind Kind) string {
	switch kind {
	case KindWindows:
		// Preserve `C:\` and similar drive roots; otherwise strip
		// trailing backslashes. Mirrors `OsPath.cs:106-112`.
		if strings.HasSuffix(path, `:\`) {
			return path
		}
		for !strings.HasSuffix(path, `:\`) && strings.HasSuffix(path, `\`) {
			path = path[:len(path)-1]
		}
		return path
	case KindUnix:
		if path == "/" {
			return path
		}
		for path != "/" && strings.HasSuffix(path, "/") {
			path = path[:len(path)-1]
		}
		return path
	}
	return path
}

// Kind returns the detected platform shape.
func (p OsPath) Kind() Kind { return p.kind }

// IsWindows reports whether the path is Windows-shaped.
func (p OsPath) IsWindows() bool { return p.kind == KindWindows }

// IsUnix reports whether the path is Unix-shaped.
func (p OsPath) IsUnix() bool { return p.kind == KindUnix }

// IsEmpty reports whether the path is empty or whitespace-only.
// Mirrors `OsPath.cs:131` which uses `IsNullOrWhiteSpace`.
func (p OsPath) IsEmpty() bool {
	return strings.TrimSpace(p.path) == ""
}

// IsRooted reports whether the path is absolute. Windows paths are
// rooted if they start with `\\` (UNC) or have a drive letter; Unix
// paths are rooted if they start with `/`. Unknown-kind paths are
// never rooted. Mirrors `OsPath.cs:133-149`.
func (p OsPath) IsRooted() bool {
	if p.IsWindows() {
		return strings.HasPrefix(p.path, `\\`) || hasWindowsDriveLetter(p.path)
	}
	if p.IsUnix() {
		return strings.HasPrefix(p.path, "/")
	}
	return false
}

// FullPath returns the normalized path string.
func (p OsPath) FullPath() string { return p.path }

// PathWithoutTrailingSlash returns the path with any trailing slash
// removed (preserving drive roots like `C:\` and the Unix root `/`).
func (p OsPath) PathWithoutTrailingSlash() string {
	return trimTrailingSlash(p.path, p.kind)
}

// String implements [fmt.Stringer] and matches the C# `ToString`
// override at `OsPath.cs:301-304`.
func (p OsPath) String() string { return p.path }

// FileName returns the trailing path component (file or final
// directory), or empty string if the path has no nameable component.
// The fixture exercises e.g. `\\blaat` → "blaat", `/` → "" (no name).
// Mirrors `OsPath.cs:182-202`.
func (p OsPath) FileName() string {
	idx := p.fileNameIndex()
	if idx == -1 {
		trimmed := strings.Trim(p.path, `\/`)
		if trimmed == "" {
			return ""
		}
		return trimmed
	}
	return strings.Trim(p.path[idx:], `\/`)
}

// Name behaves like .NET's `DirectoryInfo.Name`. The fixture doesn't
// exercise it directly; ported for parity with `OsPath.cs:204-226`.
func (p OsPath) Name() string {
	idx := p.fileNameIndex()
	if idx == -1 {
		return p.PathWithoutTrailingSlash()
	}
	rootLen := p.rootLength()
	if rootLen > idx+1 {
		return p.path[:rootLen]
	}
	return trimTrailingSlash(strings.TrimLeft(p.path[idx:], `/\`), p.kind)
}

func (p OsPath) fileNameIndex() int {
	if len(p.path) < 2 {
		return -1
	}
	idx := strings.LastIndexAny(p.path[:len(p.path)-1], `/\`)
	if idx == -1 {
		return -1
	}
	if strings.HasPrefix(p.path, `\\`) && idx < 2 {
		return -1
	}
	if strings.HasPrefix(p.path, "/") && idx == 0 {
		idx++
	}
	return idx
}

func (p OsPath) rootLength() int {
	if !p.IsRooted() {
		return 0
	}
	if p.kind == KindUnix {
		return 1
	}
	if p.kind == KindWindows {
		if hasWindowsDriveLetter(p.path) {
			return 3
		}
		// `\\server\share` or `\\?\UNC\server\share`.
		if m := uncRegex.FindStringSubmatchIndex(p.path); m != nil {
			// SubexpIndex 1 corresponds to the unnamed first capture.
			return m[3] - m[2]
		}
		// `\\?\C:\` long-form local path prefix.
		if strings.HasPrefix(p.path, `\\?\`) {
			return 7
		}
	}
	return 0
}

func (p OsPath) fragments() []string {
	// Match `Path.Split(['\\','/'], RemoveEmptyEntries)` semantics from
	// `OsPath.cs:294-297`.
	out := make([]string, 0, 4)
	for _, f := range strings.FieldsFunc(p.path, func(r rune) bool { return r == '/' || r == '\\' }) {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// Directory returns the parent directory as an [OsPath], or
// [OsPath.Null] when the path has no parent (e.g. `C:`, `/`,
// `\\unc`). Mirrors `OsPath.cs:151-176`.
func (p OsPath) Directory() OsPath {
	idx := p.fileNameIndex()
	if idx == -1 {
		return Null()
	}
	rootLen := p.rootLength()
	if rootLen == len(p.path) {
		return Null()
	}
	if rootLen > idx+1 {
		return New(p.path[:rootLen])
	}
	return NewWithKind(p.path[:idx], p.kind).AsDirectory()
}

// AsDirectory returns a copy with a trailing platform-appropriate
// separator. Mirrors `OsPath.cs:326-342`.
func (p OsPath) AsDirectory() OsPath {
	if p.IsEmpty() {
		return p
	}
	switch p.kind {
	case KindWindows:
		return NewWithKind(strings.TrimRight(p.path, `\`)+`\`, p.kind)
	case KindUnix:
		return NewWithKind(strings.TrimRight(p.path, `/`)+"/", p.kind)
	}
	return p
}

// Contains reports whether `other` lies under `p`. Both must be
// rooted; the comparison is case-insensitive when either side is
// [KindWindows]. Mirrors `OsPath.cs:344-370`.
func (p OsPath) Contains(other OsPath) bool {
	if !p.IsRooted() || !other.IsRooted() {
		return false
	}
	left := p.fragments()
	right := other.fragments()
	if len(right) < len(left) {
		return false
	}
	caseInsensitive := p.kind == KindWindows || other.kind == KindWindows
	for i := range left {
		if !fragmentEqual(left[i], right[i], caseInsensitive) {
			return false
		}
	}
	return true
}

func fragmentEqual(a, b string, caseInsensitive bool) bool {
	if caseInsensitive {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// Equals reports byte-equality with the platform's case sensitivity:
// case-insensitive when either side is Windows; case-sensitive
// otherwise. Trailing slashes are significant; use
// [OsPath.EqualsIgnoreTrailingSlash] to ignore them. Mirrors
// `OsPath.cs:372-398`.
func (p OsPath) Equals(other OsPath) bool {
	return p.equalsImpl(other, false)
}

// EqualsIgnoreTrailingSlash compares paths after stripping any
// trailing platform separator. Mirrors the two-arg `Equals` overload
// at `OsPath.cs:377-398`.
func (p OsPath) EqualsIgnoreTrailingSlash(other OsPath) bool {
	return p.equalsImpl(other, true)
}

func (p OsPath) equalsImpl(other OsPath, ignoreTrailingSlash bool) bool {
	if p.path == other.path {
		return true
	}
	left := p.path
	right := other.path
	if ignoreTrailingSlash {
		left = p.PathWithoutTrailingSlash()
		right = other.PathWithoutTrailingSlash()
	}
	if p.kind == KindWindows || other.kind == KindWindows {
		return strings.EqualFold(left, right)
	}
	return left == right
}

// Add concatenates two paths, mirroring the C# `+` operator at
// `OsPath.cs:420-448`. Right-rooted absorbs left; mismatched non-
// Unknown kinds panic — programmer error analogous to the C# Exception
// throw.
func (p OsPath) Add(other OsPath) OsPath {
	if p.kind != other.kind && other.kind != KindUnknown {
		panic(fmt.Sprintf("ospath: cannot combine OsPaths of different platforms ('%s' + '%s')", p.path, other.path))
	}
	if other.IsEmpty() {
		return p
	}
	if other.IsRooted() {
		return other
	}
	if p.kind == KindWindows || other.kind == KindWindows {
		return NewWithKind(strings.TrimRight(p.path, `\`)+`\`+strings.TrimLeft(other.path, `\`), KindWindows)
	}
	if p.kind == KindUnix || other.kind == KindUnix {
		return NewWithKind(strings.TrimRight(p.path, "/")+"/"+other.path, KindUnix)
	}
	return NewWithKind(p.path+"/"+other.path, KindUnknown)
}

// AddString is a convenience wrapper around [OsPath.Add] that takes a
// raw string, mirroring `OsPath.cs:450-453`.
func (p OsPath) AddString(other string) OsPath {
	return p.Add(New(other))
}

// Sub returns the relative path from `right` to `left` (i.e. how to
// navigate from right to left), mirroring the C# `-` operator at
// `OsPath.cs:455-504`. Both sides must be rooted; otherwise it panics
// (analogous to the C# ArgumentException).
//
// The returned [OsPath] is always [KindUnknown] — it's a relative path
// fragment, not a rooted path. Trailing-slash carryover from the left
// operand is preserved.
func (p OsPath) Sub(other OsPath) OsPath {
	left := p
	right := other
	if !left.IsRooted() || !right.IsRooted() {
		panic("ospath: cannot determine relative path for unrooted paths")
	}
	leftFrags := left.fragments()
	rightFrags := right.fragments()
	caseInsensitive := left.kind == KindWindows || right.kind == KindWindows
	common := 0
	for common < len(leftFrags) && common < len(rightFrags) {
		if !fragmentEqual(leftFrags[common], rightFrags[common], caseInsensitive) {
			break
		}
		common++
	}
	if common == 0 {
		return right
	}
	parts := make([]string, 0, len(leftFrags)+len(rightFrags))
	for j := common; j < len(rightFrags); j++ {
		parts = append(parts, "..")
	}
	for j := common; j < len(leftFrags); j++ {
		parts = append(parts, leftFrags[j])
	}
	if strings.HasSuffix(left.path, `\`) || strings.HasSuffix(left.path, "/") {
		parts = append(parts, "")
	}
	sep := "/"
	if left.kind == KindWindows || right.kind == KindWindows {
		sep = `\`
	}
	return NewWithKind(strings.Join(parts, sep), KindUnknown)
}

// Ext returns the file-name extension (including the leading dot) of
// the supplied path, mirroring `System.IO.Path.GetExtension` on the
// .NET side. Both `/` and `\` are recognised as separators so the
// behaviour is identical regardless of which platform shape the path
// uses — matching the cross-shape posture of the rest of this package.
//
// Returns the empty string when the trailing path component contains
// no dot. A trailing dot yields "."; a leading-dot file like
// `.bashrc` is treated as the whole name being the extension, mirroring
// .NET's `Path.GetExtension(".bashrc") == ".bashrc"`.
func Ext(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		c := path[i]
		if c == '/' || c == '\\' {
			return ""
		}
		if c == '.' {
			return path[i:]
		}
	}
	return ""
}

// Base returns the trailing component of the supplied path, mirroring
// `System.IO.Path.GetFileName` on .NET 7+. Both `/` and `\` are
// recognised as separators. A trailing separator yields the empty
// string; a path with no separator is returned verbatim.
func Base(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[i+1:]
		}
	}
	return path
}

// ErrNotParent is returned by [Relative] when the supplied parent
// path is not actually an ancestor of the supplied child path.
// Mirrors the `NotParentException` sentinel raised by
// `NzbDrone.Common.Extensions.PathExtensions.GetRelativePath` at
// `PathExtensions.cs:85-92`.
var ErrNotParent = errors.New("ospath: not a parent path")

// IsParent reports whether `parent` is a strict ancestor of `child`.
// Returns false if the two paths are equal, if either side is
// malformed, or if they live on different platform shapes. Walks the
// `child` ancestry via [OsPath.Directory], comparing each ancestor to
// `parent` using case-insensitive trailing-slash-tolerant equality
// (case-insensitive only when either side is Windows-shaped, matching
// the C# `OsPath.Equals(_, ignoreTrailingSlash: true)` contract).
//
// Mirrors `NzbDrone.Common.Extensions.PathExtensions.IsParentPath` at
// `PathExtensions.cs:123-139` verbatim — including the "a path is not
// its own parent" semantic (an exact-equal pair returns false because
// the loop walks `child.Directory`, never comparing `child` to
// `parent` directly).
func IsParent(parent, child string) bool {
	p := New(parent)
	c := New(child)
	for {
		d := c.Directory()
		if d.IsEmpty() {
			return false
		}
		if d.EqualsIgnoreTrailingSlash(p) {
			return true
		}
		c = d
	}
}

// Relative returns the path of `child` expressed relative to its
// ancestor `parent`. Mirrors
// `NzbDrone.Common.Extensions.PathExtensions.GetRelativePath` at
// `PathExtensions.cs:85-93`: substring after parent's length with
// leading/trailing platform separators trimmed.
//
// Returns [ErrNotParent] (wrapped via fmt) when [IsParent] reports
// the relationship doesn't hold — Go-port substitute for the C#
// `NotParentException` throw.
func Relative(parent, child string) (string, error) {
	if !IsParent(parent, child) {
		return "", fmt.Errorf("%w: %q is not a child of %q", ErrNotParent, child, parent)
	}
	return strings.Trim(child[len(parent):], `\/`), nil
}
