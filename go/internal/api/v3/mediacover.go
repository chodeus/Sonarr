package v3

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/httperr"
)

// MediaCoverLister exposes the root MediaCover folder path. Stub-
// friendly so tests can point at a tempdir.
type MediaCoverLister interface {
	MediaCoverFolder() string
}

// mediaCoverFilenameRegex mirrors the C# route constraint
// `(.+)\.(jpg|png|gif)`. Path-traversal protection: combined with
// the no-slash filename match plus the post-Join filepath.Dir check
// below, the file can only resolve within
// <root>/MediaCover/<seriesId>/.
var mediaCoverFilenameRegex = regexp.MustCompile(`^[^/\\]+\.(?i:jpg|png|gif)$`)

// resizedImageRegex mirrors the C# `RegexResizedImage` constant —
// matches the "-NNN.jpg" suffix on resized images so the handler can
// fall back to the base `.jpg` when the resized file is missing.
var resizedImageRegex = regexp.MustCompile(`-\d+\.jpg$`)

// MediaCoverHandler returns GET /api/v3/mediacover/{seriesId}/{filename}.
// Mirrors `MediaCoverController.GetMediaCover` at
// `src/Sonarr.Api.V3/MediaCovers/MediaCoverController.cs:28-47`.
//
// Path resolution: <root>/MediaCover/<seriesId>/<filename>. Returns
// 404 when the file is missing OR is zero-bytes (the C# action checks
// `_diskProvider.GetFileSize(filePath) == 0` as a fresh-install
// guard). For resized-image requests (`<name>-NNN.jpg`), falls back
// to the base `<name>.jpg` when the resized variant is missing.
//
// Content-Type derived from extension: jpg → image/jpeg, png →
// image/png, gif → image/gif.
func MediaCoverHandler(svc MediaCoverLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seriesIDStr := r.PathValue("seriesId")
		filename := r.PathValue("filename")
		seriesID, err := strconv.Atoi(seriesIDStr)
		if err != nil || seriesID <= 0 {
			httperr.BadRequest(w, "mediacover: invalid seriesId")
			return
		}
		if !mediaCoverFilenameRegex.MatchString(filename) {
			httperr.BadRequest(w, "mediacover: invalid filename")
			return
		}
		seriesDir := filepath.Join(svc.MediaCoverFolder(), strconv.Itoa(seriesID))
		path := filepath.Join(seriesDir, filename)
		// Defence-in-depth: even though the regex rejects slashes, the
		// join must resolve to the per-series directory.
		if filepath.Dir(path) != filepath.Clean(seriesDir) {
			httperr.BadRequest(w, "mediacover: filename outside series folder")
			return
		}
		info, err := os.Stat(path)
		if err != nil || info.Size() == 0 {
			fallback := resizedImageRegex.ReplaceAllString(path, ".jpg")
			if fallback == path {
				httperr.NotFound(w, fmt.Sprintf("mediacover %d/%s not found", seriesID, filename))
				return
			}
			if fb, err := os.Stat(fallback); err != nil || fb.Size() == 0 {
				httperr.NotFound(w, fmt.Sprintf("mediacover %d/%s not found", seriesID, filename))
				return
			}
			path = fallback
			info, _ = os.Stat(path)
		}
		f, err := os.Open(path)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("mediacover: open %q: %w", path, err), nil)
			return
		}
		defer func() { _ = f.Close() }()
		w.Header().Set("Content-Type", mediaCoverContentType(path))
		http.ServeContent(w, r, info.Name(), info.ModTime(), f)
	})
}

// mediaCoverContentType maps the file extension to a MIME type. The
// C# action delegates to `FileExtensionContentTypeProvider` which
// covers the full IANA registry; the Go port hard-codes the three
// extensions the route regex allows — anything else can't reach this
// function via the route.
func mediaCoverContentType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	}
	return "application/octet-stream"
}
