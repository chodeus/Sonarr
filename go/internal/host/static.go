package host

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// staticHandler returns the http.Handler that serves the built
// frontend bundle with SPA-style routing — unknown paths fall back
// to index.html so the client-side router can resolve them.
//
// Mirrors the C# `app.UseStaticFiles(...) + app.UseSpa(...)` pattern
// in `src/NzbDrone.Host/Startup.cs` where the SPA fallback handler
// serves index.html for any GET that didn't match a static file or
// a controller route.
//
// Security: serveStatic refuses to follow paths that escape `root`
// via `../` — the request URL is cleaned + the resulting absolute
// path must remain under the absolute root. Symlinks inside the
// root resolve normally (e.g. the build output's vendored JS); the
// guard only blocks path-traversal attacks.
//
// Index-html fallback rules:
//
//  1. The exact path "/" serves "<root>/index.html".
//  2. If the cleaned path matches an existing file → serve the file.
//  3. If the cleaned path matches a directory → serve
//     "<dir>/index.html" if present; else fall back to root index.html.
//  4. Otherwise → serve root index.html (SPA fallback).
//
// API + ping routes are NOT served by this handler; the host
// registers staticHandler under the bare "/" prefix, AFTER the
// per-method API routes. Go's http.ServeMux matches longer patterns
// first, so "/api/v3/..." continues to hit its dedicated handler.
//
// The handler delegates to http.ServeFile for the file-serving path
// (preserves Content-Type sniffing + Range support + Last-Modified
// caching headers).
func staticHandler(root string) (http.Handler, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Clean the URL path + reject any traversal attempt.
		cleaned := filepath.Clean(r.URL.Path)
		if cleaned == "" || cleaned == "." {
			cleaned = "/"
		}
		// Resolve under root + guard against `..` escapes.
		full := filepath.Join(absRoot, cleaned)
		if !strings.HasPrefix(full, absRoot+string(filepath.Separator)) && full != absRoot {
			// Path-traversal attempt → fall through to SPA index.
			serveIndex(w, r, absRoot)
			return
		}

		fi, err := os.Stat(full)
		switch {
		case err == nil && !fi.IsDir():
			// Exact file match — serve via ServeFile.
			http.ServeFile(w, r, full)
		case err == nil && fi.IsDir():
			// Directory match — serve <dir>/index.html if present.
			dirIndex := filepath.Join(full, "index.html")
			if dfi, derr := os.Stat(dirIndex); derr == nil && !dfi.IsDir() {
				http.ServeFile(w, r, dirIndex)
				return
			}
			serveIndex(w, r, absRoot)
		default:
			// Missing → SPA fallback.
			serveIndex(w, r, absRoot)
		}
	}), nil
}

// serveIndex serves the root index.html. If index.html is also
// missing the response is 404 (the static dir is empty or
// misconfigured — surface visibly rather than emit a confusing
// empty 200).
func serveIndex(w http.ResponseWriter, r *http.Request, absRoot string) {
	index := filepath.Join(absRoot, "index.html")
	if _, err := os.Stat(index); err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, index)
}
