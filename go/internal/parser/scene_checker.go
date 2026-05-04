package parser

import (
	"strings"

	"sonarr.tv/sonarr/internal/quality"
)

// IsSceneTitle ports `SceneChecker.IsSceneTitle` at
// `src/NzbDrone.Core/Parser/SceneChecker.cs:7-30`. Reports whether
// the given release title looks like a scene-named release.
//
// Per the C# comment: "This method should prefer false negatives
// over false positives. It's better not to use a title that might
// be scene than to use one that isn't scene." The conservative
// gauntlet:
//
//  1. Must contain a `.` (scene names use dot separators).
//  2. Must NOT contain a space (scene names don't use spaces).
//  3. Must parse via ParseTitle.
//  4. Parsed result must have a non-nil ReleaseGroup.
//  5. Parsed Quality must not be Quality.Unknown.
//  6. Parsed SeriesTitle must not be empty (after trim).
//
// All six conditions must hold; failure of any one returns false.
func IsSceneTitle(title string) bool {
	if !strings.Contains(title, ".") {
		return false
	}
	if strings.Contains(title, " ") {
		return false
	}

	parsed := ParseTitle(title)
	if parsed == nil {
		return false
	}
	if parsed.ReleaseGroup == "" {
		return false
	}
	if parsed.Quality.Quality.ID == quality.Unknown.ID {
		return false
	}
	if strings.TrimSpace(parsed.SeriesTitle) == "" {
		return false
	}

	return true
}
