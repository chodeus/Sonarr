// Package frontend embeds the built React UI bundle into the Go
// binary via `//go:embed all:bundle`. The directory at
// `internal/frontend/bundle/` is treated as the frontend's build
// output — production deploys typically run:
//
//	npm run build                                          # builds frontend/build/
//	cp -r frontend/build/* go/internal/frontend/bundle/    # stage for embed
//	go build ./cmd/sonarr                                  # single-binary distribution
//
// Development can skip the copy step and pass
// `--frontenddir=./frontend/build` at runtime — the embedded
// fallback only kicks in when the runtime flag is empty AND the
// embedded bundle's index.html is reachable.
//
// The shipped placeholder bundle has a single index.html the
// staticHandlerFS will serve for any path; deploys that forget
// the copy step land on the explanation page rather than 404.
package frontend

import (
	"embed"
	"io/fs"
)

//go:embed all:bundle
var bundle embed.FS

// FS returns an fs.FS rooted at the bundle directory — i.e.
// index.html sits at the root of the returned filesystem. Pass
// directly to host.WithStaticFS.
//
// fs.Sub strips the `bundle/` prefix so the resulting filesystem
// satisfies host.staticHandlerFS's contract that index.html lives
// at the FS root.
func FS() fs.FS {
	sub, err := fs.Sub(bundle, "bundle")
	if err != nil {
		// fs.Sub on a fixed string only errors when the string is
		// not a valid path — unreachable here.
		return bundle
	}
	return sub
}
