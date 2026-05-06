package episodeimport

import (
	"strings"

	"sonarr.tv/sonarr/internal/parser"
)

// GetSceneName ports `SceneNameCalculator.cs:10-38` —
// computes the canonical scene name for a LocalEpisode by walking
// three sources in priority order:
//
//  1. The download-client-supplied release title, when the file came
//     from a single-episode (non-FullSeason) grab and there's exactly
//     one video file in the batch. Strip the ext via
//     [parser.RemoveFileExtension] so a name like
//     `Series.S01E01.PROPER.mkv` becomes `Series.S01E01.PROPER`.
//
//  2. The on-disk file's basename-without-extension, when that name
//     looks like a scene-format title per [parser.IsSceneTitle].
//
//  3. The containing folder's release title, when the folder name
//     parsed cleanly (`!FullSeason`) AND looks scene-format AND there's
//     only one video file in the batch.
//
// Returns the empty string when none of the three rules match — the
// C# version returns null; the Go port returns "" so callers can use
// a single empty-check rather than nil-vs-empty handling.
//
// Pure-logic helper (no I/O, no DB), so it lives next to its only
// caller — the orchestrator's per-file pass — rather than in a
// separate scenenamecalculator package. The C# tree uses a static
// class; the Go port uses a free function.
//
// nil-safe: nil LocalEpisode returns "".
func GetSceneName(le *LocalEpisode) string {
	if le == nil {
		return ""
	}

	otherVideoFiles := le.OtherVideoFiles
	dlInfo := le.DownloadClientEpisodeInfo

	// Rule 1: single-file grab from a non-FullSeason download.
	if !otherVideoFiles && dlInfo != nil && !dlInfo.FullSeason {
		return parser.RemoveFileExtension(dlInfo.ReleaseTitle)
	}

	// Rule 2: filename basename when scene-shaped.
	cleaned := cleanFilePath(le.Path)
	fileName := basenameNoExt(cleaned)
	if parser.IsSceneTitle(fileName) {
		return fileName
	}

	// Rule 3: containing folder when scene-shaped AND non-FullSeason
	// AND single-file batch.
	if le.FolderEpisodeInfo != nil && !otherVideoFiles && !le.FolderEpisodeInfo.FullSeason {
		folderTitle := strings.TrimSpace(le.FolderEpisodeInfo.ReleaseTitle)
		if folderTitle != "" && parser.IsSceneTitle(folderTitle) {
			return folderTitle
		}
	}

	return ""
}

// cleanFilePath ports the slice of `PathExtensions.CleanFilePath`
// (`src/NzbDrone.Common/Extensions/PathExtensions.cs:28-49`) that
// matters here: trim leading/trailing whitespace, then strip
// trailing `/` and (when not a UNC head) trailing `\`. Skips the
// `FileInfo.FullName` round-trip — that helper canonicalises Windows
// short-paths and resolves `.` / `..` segments which neither the
// SceneNameCalculator nor downstream specs care about.
func cleanFilePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	// UNC head: `\\server\share` — preserve the leading double-backslash.
	if strings.HasPrefix(p, `\\`) && !strings.Contains(p, "/") {
		return strings.TrimRight(p, `/\`)
	}
	return strings.TrimRight(p, `/\`)
}

// basenameNoExt is the path/filepath.Base + path.Ext-strip composite
// that's platform-shape-agnostic — handles both `/` and `\` so a
// Windows-shaped path going through this helper returns the right
// basename even on a Unix daemon. Mirrors
// `Path.GetFileNameWithoutExtension(...)` in the C# tree (which is
// platform-shape-locked in .NET BCL).
func basenameNoExt(p string) string {
	if p == "" {
		return ""
	}
	if idx := strings.LastIndexAny(p, `\/`); idx >= 0 {
		p = p[idx+1:]
	}
	if dot := strings.LastIndex(p, "."); dot > 0 {
		p = p[:dot]
	}
	return p
}
