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
			http.ServeFileFS(w, r, filesystem, relPath)
		case err == nil && info.IsDir():
			dirIndex := relPath + "/index.html"
			if dinfo, derr := fs.Stat(filesystem, dirIndex); derr == nil && !dinfo.IsDir() {
				http.ServeFileFS(w, r, filesystem, dirIndex)
				return
			}
			serveIndexFS(w, r, filesystem)
		default:
			serveIndexFS(w, r, filesystem)
		}
	})
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
