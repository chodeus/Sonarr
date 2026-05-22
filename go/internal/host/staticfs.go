package host

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// staticHandlerFS is the fs.FS-backed variant of staticHandler. Same
// SPA-fallback semantics, but reads from any fs.FS — unlocks
// `go:embed` for single-binary distribution (the cmd/sonarr binary
// can compile the frontend bundle into the executable + mount it
// without a runtime --frontenddir flag).
//
// The fs.FS argument MUST be rooted at the directory that contains
// index.html (matching staticHandler's `root` semantic). For
// `go:embed` this typically means `//go:embed all:frontend/build`
// + an fs.Sub call to peel the leading prefix.
//
// SPA semantics identical to staticHandler:
//
//  1. The exact path "/" serves "/index.html".
//  2. Existing file → serve it.
//  3. Existing directory → "<dir>/index.html" if present, else root index.
//  4. Otherwise → root index.html (SPA fallback).
//
// Path-traversal guard: the io/fs interface forbids paths
// containing `..` segments via fs.ValidPath — equivalent to the
// absRoot-prefix check in the disk variant.
//
// Caching: http.ServeFileFS sets Last-Modified based on the fs.File
// modtime. embed.FS reports the build time, which is fine for
// production deployments where the binary is replaced atomically.
func staticHandlerFS(filesystem fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cleaned := path.Clean(r.URL.Path)
		if cleaned == "" || cleaned == "/" || cleaned == "." {
			serveIndexFS(w, r, filesystem)
			return
		}
		relPath := strings.TrimPrefix(cleaned, "/")
		if !fs.ValidPath(relPath) {
			serveIndexFS(w, r, filesystem)
			return
		}
		info, err := fs.Stat(filesystem, relPath)
		switch {
		case err == nil && !info.IsDir():
			// Existing file → serve it. Asset-extension exclusions
			// only matter on the miss path; an actual /static/app.js
			// or /favicon.ico on disk should serve normally.
			http.ServeFileFS(w, r, filesystem, relPath)
		case err == nil && info.IsDir():
			dirIndex := relPath + "/index.html"
			if dinfo, derr := fs.Stat(filesystem, dirIndex); derr == nil && !dinfo.IsDir() {
				http.ServeFileFS(w, r, filesystem, dirIndex)
				return
			}
			serveIndexFS(w, r, filesystem)
		default:
			// J206 — SPA fallback exclusions on the MISS path. Ports C#
			// `IndexHtmlMapper.CanHandle` at `:35-43`. Without this
			// guard every unmatched route (unimplemented API endpoints,
			// missing MediaCover images, asset paths the bundle hasn't
			// built to expose) returns the SPA index.html with HTTP 200
			// — masking route gaps as "the SPA renders something" and
			// breaking any client that expects 404 on a missing
			// image / asset. C# applies the same exclusions BEFORE
			// trying any file; the Go-port applies them only after the
			// disk-miss because an existing file with a `.` in its name
			// (e.g. /static/app.js) is a legitimate asset.
			if shouldNotSPAFallback(cleaned) {
				http.NotFound(w, r)
				return
			}
			serveIndexFS(w, r, filesystem)
		}
	})
}

// shouldNotSPAFallback returns true when a request path should 404
// rather than fall through to the SPA index.html on a miss.
//
// Ports C# `IndexHtmlMapper.CanHandle` at `IndexHtmlMapper.cs:35-43`:
//
//	return !resourceUrl.StartsWith("/content") &&
//	       !resourceUrl.StartsWith("/mediacover") &&
//	       !resourceUrl.Contains('.') &&
//	       !resourceUrl.StartsWith("/login");
//
// Adapted for the Go-port's mux model:
//
//   - `/api/...` and `/signalr/...` — these are mux-registered routes;
//     the only way they reach staticHandlerFS is when the path doesn't
//     match a registered route (route gap). C# rejects these via a
//     separate Startup pipeline; the Go-port mux falls through to "/",
//     so the exclusion is needed here. Adding /api + /signalr as
//     additional exclusions over the C# list.
//   - Containing "." — any path with a file extension (the regex's
//     heuristic for "this is an asset, not a SPA route"). C# uses the
//     same heuristic to skip e.g. /favicon.ico when the file is missing.
//   - /content, /mediacover, /login — the C# exclusion list verbatim.
//     /content and /mediacover have their own static handlers (deferred
//     in the Go-port — for now serve 404 if not under the main bundle).
//     /login is intentionally a static HTML file (login.html); a miss
//     should 404 rather than show the SPA.
//
// J206.
func shouldNotSPAFallback(cleaned string) bool {
	switch {
	case strings.HasPrefix(cleaned, "/api/"), cleaned == "/api":
		return true
	case strings.HasPrefix(cleaned, "/signalr/"), cleaned == "/signalr":
		return true
	case strings.HasPrefix(cleaned, "/content/"), cleaned == "/content":
		return true
	case strings.HasPrefix(cleaned, "/mediacover/"), cleaned == "/mediacover":
		return true
	case strings.HasPrefix(cleaned, "/login/"):
		// Note: /login.html and bare /login are intentionally NOT
		// excluded here — the React forms-auth flow serves login.html
		// as a real static file. Only the /login/<sub> tree is
		// excluded so e.g. /login/oauth-callback doesn't SPA-fallback.
		return true
	case strings.Contains(cleaned, "."):
		// Asset extensions — missing .ico / .png / .json should 404
		// rather than serve the SPA. The bundle's actual asset paths
		// are reachable via the fs.Stat branch above; only the miss
		// path falls into here.
		return true
	}
	return false
}

// serveIndexFS serves the embedded index.html. 404 when missing
// — the static FS is empty or misconfigured.
func serveIndexFS(w http.ResponseWriter, r *http.Request, filesystem fs.FS) {
	info, err := fs.Stat(filesystem, "index.html")
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	http.ServeFileFS(w, r, filesystem, "index.html")
}
