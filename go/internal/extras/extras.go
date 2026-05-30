// Package extras ports the C# extra-file import subsystem at
// `src/NzbDrone.Core/Extras/`. After an episode file is imported, the
// ExtraService scans the download folder for adjacent companion files —
// subtitles, `.nfo` sidecars, artwork, etc. — and imports the ones that
// belong to the episode next to the imported media file.
//
// Layering (smallest-first port):
//   - this file: the pure foundation — the subtitle extension set, the
//     ExtraFileExtensions config parser, the [TransferMode] +
//     [FileTransferrer] transfer seam, and the shared [importFile] helper
//     (dest-path naming + transfer + relative-path result) that mirrors
//     the C# `ExtraFileManager<TExtraFile>.ImportFile` at
//     `Extras/Files/ExtraFileManager.cs:57-87`.
//   - manager.go (later slice): the per-type managers (Other, Subtitle).
//   - service.go (later slice): the ExtraService orchestrator +
//     ImportEpisode folder enumeration / extension filter / delegation.
package extras

import (
	"fmt"
	"path/filepath"
	"strings"
)

// TransferMode selects how [importFile] relocates an extra file. Mirrors
// the C# `TransferMode` values used by `ExtraFileManager.ImportFile`
// (`:78-83`): a non-read-only import MOVES the companion (it came from a
// to-be-cleaned download folder); a read-only import HARDLINK-OR-COPYs
// (when CopyUsingHardlinks) or COPYs (the original must be preserved).
type TransferMode int

const (
	// TransferModeMove relocates src → dst (default for a normal import).
	TransferModeMove TransferMode = iota
	// TransferModeCopy duplicates src → dst, leaving src in place.
	TransferModeCopy
	// TransferModeHardLinkOrCopy hardlinks when possible, else copies.
	TransferModeHardLinkOrCopy
)

// FileTransferrer relocates a single file. Production binds an adapter
// over the existing disk transfer surface; tests pass a recorder. dst's
// parent directory already exists (it's the imported episode's folder).
type FileTransferrer interface {
	TransferFile(src, dst string, mode TransferMode) error
}

// subtitleFileExtensions ports `Subtitles/SubtitleFileExtensions.cs:9-22`
// (case-insensitive). Membership is keyed by the lower-cased, dot-prefixed
// extension.
var subtitleFileExtensions = map[string]struct{}{
	".aqt": {}, ".ass": {}, ".idx": {}, ".jss": {}, ".psb": {},
	".rt": {}, ".smi": {}, ".srt": {}, ".ssa": {}, ".sub": {},
	".txt": {}, ".utf": {}, ".utf8": {}, ".utf-8": {}, ".vtt": {},
}

// IsSubtitleExtension reports whether ext (with or without a leading dot,
// any case) is a recognised subtitle extension. Mirrors the C#
// `SubtitleFileExtensions.Extensions.Contains(extension.ToLowerInvariant())`
// membership test at `SubtitleService.cs:109`.
func IsSubtitleExtension(ext string) bool {
	_, ok := subtitleFileExtensions[normalizeExtension(ext)]
	return ok
}

// ParseExtraFileExtensions parses the comma-separated
// `IConfigService.ExtraFileExtensions` value into a deduped set of
// lower-cased, dot-prefixed extensions. Mirrors the split+normalize at
// `ExtraService.cs:70-73` (`extensions.Split(',').Select(e => e.Trim())`
// with a leading-dot guard). Blank entries are dropped.
func ParseExtraFileExtensions(config string) []string {
	out := make([]string, 0)
	seen := make(map[string]struct{})
	for _, raw := range strings.Split(config, ",") {
		ext := normalizeExtension(raw)
		if ext == "." || ext == "" {
			continue
		}
		if _, dup := seen[ext]; dup {
			continue
		}
		seen[ext] = struct{}{}
		out = append(out, ext)
	}
	return out
}

// normalizeExtension lower-cases + dot-prefixes an extension token
// ("SRT" / ".SRT" / " srt " → ".srt"). An empty token returns "".
func normalizeExtension(ext string) string {
	ext = strings.ToLower(strings.TrimSpace(ext))
	if ext == "" {
		return ""
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return ext
}

// importFile relocates a single extra file next to the imported episode
// file and returns its new series-relative path. Mirrors the C# shared
// helper `ExtraFileManager.ImportFile` (`:57-87`):
//
//   - dest folder = the imported episode file's folder
//     (dir of seriesPath/episodeFileRelativePath).
//   - dest filename = the episode file's stem + suffix + extension
//     (suffix carries the subtitle language/copy tags; "" for "other").
//   - transfer mode = Move on a normal import; HardLinkOrCopy (when
//     copyUsingHardlinks) / Copy when readOnly.
//
// Returns the new path relative to seriesPath (the value persisted on the
// SubtitleFile/ExtraFile row).
func importFile(t FileTransferrer, copyUsingHardlinks bool, seriesPath, episodeFileRelativePath, srcPath, suffix, extension string, readOnly bool) (string, error) {
	if t == nil {
		return "", fmt.Errorf("extras: importFile: nil FileTransferrer")
	}
	episodeFullPath := filepath.Join(seriesPath, episodeFileRelativePath)
	destFolder := filepath.Dir(episodeFullPath)
	stem := strings.TrimSuffix(filepath.Base(episodeFileRelativePath), filepath.Ext(episodeFileRelativePath))
	destName := stem + suffix + extension
	destPath := filepath.Join(destFolder, destName)

	mode := TransferModeMove
	if readOnly {
		if copyUsingHardlinks {
			mode = TransferModeHardLinkOrCopy
		} else {
			mode = TransferModeCopy
		}
	}
	if err := t.TransferFile(srcPath, destPath, mode); err != nil {
		return "", fmt.Errorf("extras: transfer %q → %q: %w", srcPath, destPath, err)
	}

	rel, err := filepath.Rel(seriesPath, destPath)
	if err != nil {
		return "", fmt.Errorf("extras: relpath(%q, %q): %w", seriesPath, destPath, err)
	}
	return rel, nil
}
