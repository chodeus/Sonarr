package v3

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/httperr"
)

// LogFileResource mirrors `src/Sonarr.Api.V3/Logs/LogFileResource.cs`.
// 5 fields: id (1-indexed list position), filename, lastWriteTime,
// contentsUrl ("{urlBase}/api/v1/log/file/{filename}"), downloadUrl
// ("{urlBase}/logfile/{filename}").
type LogFileResource struct {
	ID            int       `json:"id"`
	Filename      string    `json:"filename"`
	LastWriteTime time.Time `json:"lastWriteTime"`
	ContentsURL   string    `json:"contentsUrl"`
	DownloadURL   string    `json:"downloadUrl"`
}

// logFilenameRegex mirrors the C# constant
// `LOGFILE_ROUTE = "/(?<filename>[-.a-zA-Z0-9]+?\.txt)"`. Path-traversal
// protection: only ASCII letters/digits/dash/dot may appear in the
// filename, and the file must end in `.txt`.
var logFilenameRegex = regexp.MustCompile(`^[-.a-zA-Z0-9]+\.txt$`)

// LogFileLister returns the log folder + URL base the handler needs to
// build resource URLs. Stubbable in tests via the small surface.
type LogFileLister interface {
	LogFolder() string
	URLBase() string
}

// LogFileHandler returns GET /api/v3/log/file. Mirrors
// `LogFileControllerBase.GetLogFilesResponse` at `:30-52` — walks the
// log folder for *.txt entries, projects to LogFileResource sorted by
// LastWriteTime descending. Empty folder / missing folder returns `[]`.
func LogFileHandler(svc LogFileLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		folder := svc.LogFolder()
		entries, err := os.ReadDir(folder)
		if err != nil && !os.IsNotExist(err) {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("log/file: read %q: %w", folder, err), nil)
			return
		}
		urlBase := svc.URLBase()
		out := make([]LogFileResource, 0, len(entries))
		i := 0
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if !strings.HasSuffix(name, ".txt") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			i++
			out = append(out, LogFileResource{
				ID:            i,
				Filename:      name,
				LastWriteTime: info.ModTime(),
				ContentsURL:   fmt.Sprintf("%s/api/v1/log/file/%s", urlBase, name),
				DownloadURL:   fmt.Sprintf("%s/logfile/%s", urlBase, name),
			})
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].LastWriteTime.After(out[j].LastWriteTime) })
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

// UpdateLogFileLister is the read surface for /api/v3/log/file/update.
// Same shape as LogFileLister - production wiring snapshots the
// update-log folder at boot.
type UpdateLogFileLister interface {
	UpdateLogFolder() string
	URLBase() string
}

// UpdateLogFileHandler returns GET /api/v3/log/file/update. Mirrors
// `UpdateLogFileController` which extends LogFileControllerBase
// pointing at `<AppData>/UpdateLogs` instead of the regular logs
// folder. Wire shape matches LogFileHandler.
func UpdateLogFileHandler(svc UpdateLogFileLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		folder := svc.UpdateLogFolder()
		urlBase := svc.URLBase()
		entries, err := os.ReadDir(folder)
		if err != nil && !os.IsNotExist(err) {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("log/file/update: read %q: %w", folder, err), nil)
			return
		}
		out := make([]LogFileResource, 0, len(entries))
		i := 0
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if !strings.HasSuffix(name, ".txt") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			i++
			out = append(out, LogFileResource{
				ID:            i,
				Filename:      name,
				LastWriteTime: info.ModTime(),
				ContentsURL:   fmt.Sprintf("%s/api/v1/log/file/update/%s", urlBase, name),
				DownloadURL:   fmt.Sprintf("%s/updatelogfile/%s", urlBase, name),
			})
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].LastWriteTime.After(out[j].LastWriteTime) })
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

// UpdateLogFileByNameHandler returns
// GET /api/v3/log/file/update/{filename}. Mirrors
// `LogFileControllerBase.GetLogFileResponse` at `:54-68` against the
// update-log folder. Defence-in-depth: regex + Dir == Clean check.
func UpdateLogFileByNameHandler(svc UpdateLogFileLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		filename := r.PathValue("filename")
		if !logFilenameRegex.MatchString(filename) {
			httperr.BadRequest(w, "log/file/update: invalid filename")
			return
		}
		folder := svc.UpdateLogFolder()
		path := filepath.Join(folder, filename)
		if filepath.Dir(path) != filepath.Clean(folder) {
			httperr.BadRequest(w, "log/file/update: filename outside log folder")
			return
		}
		f, err := os.Open(path)
		if err != nil {
			if os.IsNotExist(err) {
				httperr.NotFound(w, fmt.Sprintf("log/file/update %q not found", filename))
				return
			}
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("log/file/update: open %q: %w", path, err), nil)
			return
		}
		defer func() { _ = f.Close() }()
		info, err := f.Stat()
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("log/file/update: stat %q: %w", path, err), nil)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		http.ServeContent(w, r, info.Name(), info.ModTime(), f)
	})
}

// LogFileByNameHandler returns GET /api/v3/log/file/{filename}.
// Mirrors `LogFileControllerBase.GetLogFileResponse` at `:54-68` —
// serves the file contents as text/plain. 404 when the file doesn't
// exist; 400 when the filename violates the C# regex (path traversal
// guard).
func LogFileByNameHandler(svc LogFileLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		filename := r.PathValue("filename")
		if !logFilenameRegex.MatchString(filename) {
			httperr.BadRequest(w, "log/file: invalid filename")
			return
		}
		path := filepath.Join(svc.LogFolder(), filename)
		// Defence-in-depth: filepath.Join cannot escape since the
		// regex already excludes '/', but Clean ensures the comparison
		// holds.
		if filepath.Dir(path) != filepath.Clean(svc.LogFolder()) {
			httperr.BadRequest(w, "log/file: filename outside log folder")
			return
		}
		f, err := os.Open(path)
		if err != nil {
			if os.IsNotExist(err) {
				httperr.NotFound(w, fmt.Sprintf("log/file %q not found", filename))
				return
			}
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("log/file: open %q: %w", path, err), nil)
			return
		}
		defer func() { _ = f.Close() }()
		info, err := f.Stat()
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("log/file: stat %q: %w", path, err), nil)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		http.ServeContent(w, r, info.Name(), info.ModTime(), f)
	})
}
