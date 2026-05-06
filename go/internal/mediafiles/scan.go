package mediafiles

import (
	"sonarr.tv/sonarr/internal/ospath"
)

// GetVideoFiles walks `path` via `disk.GetFiles` and returns only those
// whose extension is registered in [fileExtensions] (the
// MediaFileExtensions registry). Mirrors
// `DiskScanService.GetVideoFiles` at
// `src/NzbDrone.Core/MediaFiles/DiskScanService.cs:202-215`.
//
// `recursive=true` matches the C# default-arg `allDirectories=true` —
// the recursive walk feeds the main scan path; the false form is used
// by the extras-pass and a couple of test sites.
//
// Allocates a fresh slice; preserves the order returned by the
// disk-provider walk. Returns an empty (non-nil) slice when the walk
// returns no files so callers can uniformly range over the result.
// Disk-provider errors propagate verbatim.
func GetVideoFiles(disk DiskProvider, path string, recursive bool) ([]string, error) {
	all, err := disk.GetFiles(path, recursive)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(all))
	for _, f := range all {
		if IsMediaExtension(ospath.Ext(f)) {
			out = append(out, f)
		}
	}
	return out, nil
}

// GetNonVideoFiles is the inverse of [GetVideoFiles] — returns only
// the files whose extension is NOT in the MediaFileExtensions
// registry. Mirrors `DiskScanService.GetNonVideoFiles` at
// `DiskScanService.cs:217-230`. Used by the extras pass to surface
// candidate metadata / subtitle / thumbnail files for downstream
// classification.
func GetNonVideoFiles(disk DiskProvider, path string, recursive bool) ([]string, error) {
	all, err := disk.GetFiles(path, recursive)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(all))
	for _, f := range all {
		if !IsMediaExtension(ospath.Ext(f)) {
			out = append(out, f)
		}
	}
	return out, nil
}
