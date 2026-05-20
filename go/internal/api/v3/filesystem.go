package v3

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/httperr"
)

// FileSystemModel mirrors `src/NzbDrone.Common/Disk/FileSystemModel.cs`.
// Used as both the directory and file shape inside FileSystemResult.
//
// Field-shape notes (audit fix A.1 flag, partial-close at J164):
//   - Size: C# is `long Size` non-nullable at `:11`. Under STJson's
//     `WhenWritingNull` configuration, non-nullable properties are
//     always emitted — including the zero value. The Go shape is
//     `int64` without `omitempty` so directory entries (which never
//     set Size and would otherwise omit "size":0 under the old
//     omitempty tag) now emit `"size": 0` byte-for-byte with C#.
//   - LastModified: C# is `DateTime? LastModified` nullable at `:12`.
//     Under WhenWritingNull, null values are omitted from the wire.
//     The Go shape carries `*time.Time` without `omitempty` so emits
//     `"lastModified": null` where C# omits the key entirely — known
//     B.7-class divergence per [JSON_CONVENTIONS.md] "Known
//     divergence" callout; the bulk sweep is queued separately.
type FileSystemModel struct {
	Type         string     `json:"type"` // "drive" | "folder" | "file"
	Name         string     `json:"name"`
	Path         string     `json:"path"`
	LastModified *time.Time `json:"lastModified"`
	Extension    string     `json:"extension,omitempty"`
	Size         int64      `json:"size"`
}

// FileSystemResult mirrors `FileSystemResult.cs`.
type FileSystemResult struct {
	Parent      string            `json:"parent"`
	Directories []FileSystemModel `json:"directories"`
	Files       []FileSystemModel `json:"files"`
}

// MediaFileResult is the per-record shape of GET /filesystem/mediafiles.
// Mirrors the C# anonymous object at FileSystemController.cs:57-62.
type MediaFileResult struct {
	Path         string `json:"path"`
	RelativePath string `json:"relativePath"`
	Name         string `json:"name"`
}

// videoExtensions matches the C# `MediaFileExtensions.Extensions` set
// at `MediaFileExtensions.cs`. Lowercased; checked case-insensitively
// against the file's extension. Kept inline because the C# tree's
// large constant set rarely changes; if it grows, hoist this to the
// mediafiles package.
var videoExtensions = map[string]struct{}{
	".mkv": {}, ".mp4": {}, ".avi": {}, ".mov": {}, ".m4v": {},
	".wmv": {}, ".mpg": {}, ".mpeg": {}, ".webm": {}, ".flv": {},
	".ogv": {}, ".m2ts": {}, ".ts": {}, ".vob": {}, ".divx": {},
	".xvid": {}, ".asf": {}, ".rm": {}, ".rmvb": {},
}

// FileSystemHandler returns GET /api/v3/filesystem. Mirrors C#
// `FileSystemController.GetContents` at `:28-33` which delegates to
// `FileSystemLookupService.LookupContents`. Lists directories at the
// given path; when includeFiles=true also lists files.
//
// DEFERRED with rationale: Drive enumeration on Windows (the empty-
// `path` query branch). Cross-platform mount enumeration requires
// per-OS code (`golang.org/x/sys/unix` mountinfo on Linux, IOKit on
// macOS, GetLogicalDriveStrings on Windows). The Linux/macOS
// common case uses `/` when path is empty — preserved here.
func FileSystemHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		path := q.Get("path")
		includeFiles := strings.EqualFold(q.Get("includeFiles"), "true")
		allowNoSlash := strings.EqualFold(q.Get("allowFoldersWithoutTrailingSlashes"), "true")
		writeJSON(w, filesystemLookup(path, includeFiles, allowNoSlash))
	})
}

// FileSystemTypeHandler returns GET /api/v3/filesystem/type. Mirrors
// `GetEntityType` at `:35-46`. Returns `{"type": "file"}` when the
// path is an existing regular file, otherwise `{"type": "folder"}`
// (the C# comment notes this is deliberate — folder is the safe
// fallback to avoid leaking "does X exist" information to the UI).
func FileSystemTypeHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Query().Get("path")
		entityType := "folder"
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			entityType = "file"
		}
		writeJSON(w, struct {
			Type string `json:"type"`
		}{Type: entityType})
	})
}

// FileSystemMediaFilesHandler returns GET /api/v3/filesystem/mediafiles.
// Mirrors `GetMediaFiles` at `:48-63`. Walks the path recursively
// for video files; missing folder returns `[]` (matches C#
// `Array.Empty<string>()` short-circuit).
func FileSystemMediaFilesHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Query().Get("path")
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			writeJSON(w, []MediaFileResult{})
			return
		}
		results := []MediaFileResult{}
		_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil // skip unreadable entries; matches C# silent-tolerance
			}
			if d.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(p))
			if _, ok := videoExtensions[ext]; !ok {
				return nil
			}
			rel, _ := filepath.Rel(path, p)
			results = append(results, MediaFileResult{
				Path:         p,
				RelativePath: rel,
				Name:         filepath.Base(p),
			})
			return nil
		})
		sort.SliceStable(results, func(i, j int) bool { return results[i].Path < results[j].Path })
		writeJSON(w, results)
	})
}

// filesystemLookup ports `FileSystemLookupService.LookupContents`
// at `FileSystemLookupService.cs:26-58`. The argument validation is
// kept compact since the v3 handler is the only caller.
func filesystemLookup(path string, includeFiles, allowFoldersWithoutTrailingSlashes bool) FileSystemResult {
	res := FileSystemResult{Directories: []FileSystemModel{}, Files: []FileSystemModel{}}
	if strings.TrimSpace(path) == "" {
		path = "/" // Unix fallback; Windows drive enumeration deferred.
	}
	// Trailing-slash branch: when allowed and the query is itself a
	// real folder, list it directly.
	if allowFoldersWithoutTrailingSlashes {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return listFolder(path, includeFiles)
		}
	}
	// Default branch: peel back to the last separator and list its
	// contents. Mirrors C# substring-based parent resolution.
	idx := strings.LastIndex(path, string(filepath.Separator))
	if idx < 0 {
		return res
	}
	folder := path[:idx+1]
	return listFolder(folder, includeFiles)
}

// listFolder reads a directory and projects to the FileSystemResult
// shape. Disk errors collapse to a Parent-only response matching the
// C# `DirectoryNotFoundException` / `IOException` arms at
// LookupContents.cs:97-112.
func listFolder(folder string, includeFiles bool) FileSystemResult {
	res := FileSystemResult{
		Parent:      parentOf(folder),
		Directories: []FileSystemModel{},
		Files:       []FileSystemModel{},
	}
	entries, err := os.ReadDir(folder)
	if err != nil {
		return res
	}
	for _, e := range entries {
		name := e.Name()
		full := filepath.Join(folder, name)
		info, err := e.Info()
		if err != nil {
			continue
		}
		mod := info.ModTime().UTC()
		if e.IsDir() {
			if isSpecialFolder(name) {
				continue
			}
			res.Directories = append(res.Directories, FileSystemModel{
				Type:         "folder",
				Name:         name,
				Path:         ensureTrailingSlash(full),
				LastModified: &mod,
			})
			continue
		}
		if !includeFiles {
			continue
		}
		ext := filepath.Ext(name)
		res.Files = append(res.Files, FileSystemModel{
			Type:         "file",
			Name:         name,
			Path:         full,
			LastModified: &mod,
			Extension:    ext,
			Size:         info.Size(),
		})
	}
	sort.SliceStable(res.Directories, func(i, j int) bool { return res.Directories[i].Name < res.Directories[j].Name })
	sort.SliceStable(res.Files, func(i, j int) bool { return res.Files[i].Name < res.Files[j].Name })
	return res
}

// isSpecialFolder mirrors the C# `SpecialFolders.IsSpecialFolder`
// guard at `LookupContents.cs:130`. The C# version filters out
// system folders the user can't read (`$Recycle.Bin`, `System
// Volume Information`, etc.). The Go port matches a small set
// covering the common cases; expand as cross-platform needs arise.
func isSpecialFolder(name string) bool {
	switch name {
	case "$Recycle.Bin", "System Volume Information", ".Trash", ".Trashes", "lost+found":
		return true
	}
	return false
}

// parentOf computes the parent path with a trailing separator,
// matching the C# `GetParent` at `LookupContents.cs:171-193`.
// Root "/" returns "" (C# returns null which serialises to empty).
func parentOf(folder string) string {
	clean := strings.TrimRight(folder, string(filepath.Separator))
	parent := filepath.Dir(clean)
	if parent == clean {
		return "" // already at root
	}
	return ensureTrailingSlash(parent)
}

func ensureTrailingSlash(p string) string {
	if strings.HasSuffix(p, string(filepath.Separator)) {
		return p
	}
	return p + string(filepath.Separator)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	buf, err := json.Marshal(v)
	if err != nil {
		// Best-effort recovery; should be impossible for these tame types.
		httperr.WriteErr(w, nil, http.StatusInternalServerError, err, nil)
		return
	}
	_, _ = w.Write(buf)
}
