package mediafiles

import (
	"regexp"

	"sonarr.tv/sonarr/internal/ospath"
)

// excludedExtrasSubFolderRegex matches subfolders that hold "extras"
// content (trailers, behind-the-scenes, deleted scenes, ...) that the
// scanner should skip when filterExtras=true. Ports the C# regex at
// `src/NzbDrone.Core/MediaFiles/DiskScanService.cs:72`. RE2-compatible
// — no lookarounds; the C# `^|\\|/` head alternation transposes
// directly to `[\\/]|^`.
var excludedExtrasSubFolderRegex = regexp.MustCompile(
	`(?i)(?:[\\/]|^)(?:extras|extrafanart|behind the scenes|deleted scenes|featurettes|interviews|other|scenes|samples|shorts|trailers|theme[-_. ]music|backdrops)[\\/]`,
)

// excludedSubFoldersRegex matches subfolders that the scanner should
// always skip — `@eadir`, `.@__thumb`, Plex metadata folders, and any
// hidden (dot-prefixed) folder. Ports the C# regex at
// `DiskScanService.cs:73`. The dot-prefix arm `\.[^\\/]+` matches any
// hidden folder name (one or more non-separator characters).
var excludedSubFoldersRegex = regexp.MustCompile(
	`(?i)(?:[\\/]|^)(?:@eadir|\.@__thumb|plex versions|\.[^\\/]+)[\\/]`,
)

// excludedExtraFilesRegex matches files whose name encodes their role
// as a non-episode extra — `<base>-trailer.<ext>`, `-behindthescenes`,
// `-deleted`, etc. Ports the C# regex at `DiskScanService.cs:74`.
// Anchored at end-of-string; matches against the file name only.
var excludedExtraFilesRegex = regexp.MustCompile(
	`(?i)-(trailer|other|behindthescenes|deleted|featurette|interview|scene|short)\.[^.]+$`,
)

// excludedFilesRegex matches OS-noise filenames that are not media
// content — macOS resource forks (`._*`), the unmanic sidecar, the
// `.DS_Store` index, and Windows `Thumbs.db`. Ports the C# regex at
// `DiskScanService.cs:75`.
var excludedFilesRegex = regexp.MustCompile(
	`(?i)^\.(_|unmanic|DS_Store$)|^Thumbs\.db$`,
)

// FilterPaths drops paths under `basePath` that match any of the
// exclusion regexes. Mirrors `IDiskScanService.FilterPaths` at
// `DiskScanService.cs:232-246` — two unconditional passes
// (`excludedSubFoldersRegex` against the relative path,
// `excludedFilesRegex` against the file name) followed by two more
// (`excludedExtrasSubFolderRegex`, `excludedExtraFilesRegex`) gated on
// `filterExtras=true`.
//
// All input paths must be under `basePath`; misuse returns the wrapped
// [ospath.ErrNotParent] from [ospath.Relative]. The C# tree throws
// `NotParentException` and the call sites all pass `series.Path` as
// `basePath` plus its own disk-walk results, so the wrapped sentinel
// is the same loud signal a future caller bug would surface.
//
// Returns a fresh slice; the input is not mutated. Defensive empty-
// input short-circuit returns an empty (non-nil) slice so callers can
// uniformly range over the result.
func FilterPaths(basePath string, paths []string, filterExtras bool) ([]string, error) {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		rel, err := ospath.Relative(basePath, p)
		if err != nil {
			return nil, err
		}
		if excludedSubFoldersRegex.MatchString(rel) {
			continue
		}
		if excludedFilesRegex.MatchString(ospath.Base(p)) {
			continue
		}
		if filterExtras {
			if excludedExtrasSubFolderRegex.MatchString(rel) {
				continue
			}
			if excludedExtraFilesRegex.MatchString(ospath.Base(p)) {
				continue
			}
		}
		out = append(out, p)
	}
	return out, nil
}
