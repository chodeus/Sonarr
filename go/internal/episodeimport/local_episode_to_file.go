package episodeimport

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/ospath"
)

// ToEpisodeFile ports `Parser.Model.LocalEpisode.ToEpisodeFile()`
// at `LocalEpisode.cs:91-126` — the per-import conversion that
// materialises an [episodefiles.File] row from the parsed-and-
// aggregated [LocalEpisode]. Called by [ImportApprovedEpisodes]
// (slice 3.71) once the orchestrator has approved the import; the
// returned file is what gets handed to the upgrader / persisted via
// [episodefiles.Service.Add].
//
// Was deferred from slice 3.55 (the package's first file) with the
// note "the writer slice is the only caller and needs the full
// upgrade/recycle/IndexerFlags/grab-history derivation logic to
// compose the persisted shape." This slice (3.71) is that writer.
//
// Behavior diverges from C# in ONE documented way: the C# `Quality`
// field is a typed `QualityModel`; the Go [episodefiles.File.Quality]
// field is a JSON string (the persistence form). ToEpisodeFile
// serialises [LocalEpisode.Quality] into JSON via [json.Marshal].
// Empty/zero Quality marshals to `{...}` with zero values rather
// than the empty string — matches C# `JsonConvert.SerializeObject`
// behaviour on the equivalent QualityModel default.
//
// Caller responsibilities:
//
//   - The returned [episodefiles.File] has Size=0; the caller
//     populates Size after the file move (the C# code at
//     `ImportApprovedEpisodes.cs:88` does
//     `episodeFile.Size = _diskProvider.GetFileSize(localEpisode.Path)`
//     immediately after this call).
//
//   - RelativePath is set ONLY when the LocalEpisode's Path lives
//     under the Series.Path (the library-rescan case). For
//     download-client imports (file lives under the download
//     folder), RelativePath is empty until the writer's MoveEpisodeFile
//     pass updates it.
//
// nil receiver / nil Series returns nil — defensive against the
// C# version's NPE on the same inputs.
func (l *LocalEpisode) ToEpisodeFile() *episodefiles.File {
	if l == nil || l.Series == nil {
		return nil
	}

	cleanPath := cleanFilePath(l.Path)

	qualityJSON, err := json.Marshal(l.Quality)
	if err != nil {
		// json.Marshal on quality.Model can't fail under normal
		// circumstances (no maps with non-string keys, no channels,
		// etc.). On a synthetic failure, fall back to the empty
		// JSON object — the persisted form `{}` is the C# default.
		qualityJSON = []byte("{}")
	}

	out := &episodefiles.File{
		DateAdded:        time.Now().UTC(),
		SeriesID:         l.Series.ID,
		Quality:          string(qualityJSON),
		MediaInfo:        l.MediaInfo,
		SeasonNumber:     seasonNumberOrZero(l),
		ReleaseGroup:     l.ReleaseGroup,
		ReleaseHash:      l.ReleaseHash,
		Languages:        languageIDs(l.Languages),
		IndexerFlags:     int(l.IndexerFlags),
		ReleaseType:      int(l.ReleaseType),
		SceneName:        l.SceneName,
		OriginalFilePath: l.GetOriginalFilePath(),
	}

	// C# `:112-115`: only set RelativePath when the file lives
	// under the series root.
	if cleanPath != "" && l.Series.Path != "" && ospath.IsParent(l.Series.Path, cleanPath) {
		if rel, err := filepath.Rel(l.Series.Path, cleanPath); err == nil {
			out.RelativePath = rel
		}
	}

	// C# `:117-123`: ReleaseType fallback walks DownloadClientEpisodeInfo
	// → FolderEpisodeInfo → FileEpisodeInfo when the LocalEpisode's
	// own ReleaseType is Unknown. The orchestrator's pickReleaseType
	// helper at slice 3.62 already runs this chain before
	// AggregationService.Augment. The C# code re-runs it here as a
	// belt-and-braces; the Go port mirrors verbatim because some
	// callers (manual import / library rescan) build the
	// LocalEpisode without going through the orchestrator's
	// pickReleaseType pass.
	if out.ReleaseType == 0 {
		out.ReleaseType = int(pickReleaseType(l))
	}

	return out
}

// GetOriginalFilePath ports the private `LocalEpisode.GetOriginalFilePath`
// at `LocalEpisode.cs:128-149`. Mirrors the three-arm derivation:
//
//  1. If FolderEpisodeInfo is set AND the LocalEpisode's path has an
//     ancestor folder matching the FolderEpisodeInfo.ReleaseTitle,
//     return the path relative to that ancestor's parent.
//  2. Else, if the path's grandparent folder exists, return the path
//     relative to the grandparent.
//  3. Else, return the path's basename.
//
// Exposed because the orchestrator may want to re-derive the
// original path under a different downloadClientItem.OutputPath
// (the C# code at `ImportApprovedEpisodes.cs:128-136` overrides
// OriginalFilePath when the download-client output path is a
// parent of the LocalEpisode.Path).
func (l *LocalEpisode) GetOriginalFilePath() string {
	if l == nil {
		return ""
	}
	cleanPath := cleanFilePath(l.Path)
	if cleanPath == "" {
		return ""
	}

	// Arm 1: walk ancestors looking for FolderEpisodeInfo.ReleaseTitle.
	if l.FolderEpisodeInfo != nil && strings.TrimSpace(l.FolderEpisodeInfo.ReleaseTitle) != "" {
		if ancestor := findAncestor(cleanPath, l.FolderEpisodeInfo.ReleaseTitle); ancestor != "" {
			parent := filepath.Dir(ancestor)
			if rel, err := filepath.Rel(parent, cleanPath); err == nil && rel != "." {
				return rel
			}
		}
	}

	// Arm 2: grandparent-relative.
	parent := filepath.Dir(cleanPath)
	grandparent := filepath.Dir(parent)
	if grandparent != parent && grandparent != "" && grandparent != "." {
		if rel, err := filepath.Rel(grandparent, cleanPath); err == nil {
			return rel
		}
	}

	// Arm 3: basename only.
	return filepath.Base(cleanPath)
}

// findAncestor walks the path's parent chain looking for a directory
// whose basename matches the supplied name. Returns the matching
// path, or empty when no match. Mirrors the C# `Path.GetAncestorPath`
// extension method.
func findAncestor(path, name string) string {
	p := filepath.Dir(path)
	last := ""
	for p != "" && p != last {
		if filepath.Base(p) == name {
			return p
		}
		last = p
		p = filepath.Dir(p)
	}
	return ""
}

// languageIDs converts a typed [language.Language] slice into the
// persisted []int form. Empty input returns empty (non-nil) so the
// JSON column persists as `[]` rather than `null`.
func languageIDs(in []language.Language) []int {
	out := make([]int, 0, len(in))
	for _, l := range in {
		out = append(out, l.ID)
	}
	return out
}

// seasonNumberOrZero unwraps [LocalEpisode.SeasonNumber] to a plain
// int with 0-fallback on the multi-season / no-episode error path.
// Pulled out as a free function so it can be reused by both
// ToEpisodeFile and the import orchestrator.
func seasonNumberOrZero(l *LocalEpisode) int {
	n, err := l.SeasonNumber()
	if err != nil {
		return 0
	}
	return n
}
