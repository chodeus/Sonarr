package extras

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/extrafiles"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

// HardlinkConfig narrows `IConfigService.CopyUsingHardlinks` (one
// method). *config.Provider satisfies it.
type HardlinkConfig interface {
	CopyUsingHardlinks() bool
}

// ImportInput is the per-import context the managers need — the shared
// subset of the C# `LocalEpisode` + `EpisodeFile` arguments to
// `IManageExtraFiles.ImportFiles`.
type ImportInput struct {
	// Series is the imported episode's series (Path + ID).
	Series *series.Series
	// EpisodeFile is the just-imported media file (RelativePath /
	// SeasonNumber / ID) the companions are named + linked against.
	EpisodeFile *episodefiles.File
	// SourcePath is the original downloaded media path
	// (`localEpisode.Path`) — its stem drives the filename-prefix match.
	SourcePath string
	// FileInfo is the parsed episode info of the source file
	// (`localEpisode.FileEpisodeInfo`) — drives the season/episode match.
	FileInfo *parser.ParsedEpisodeInfo
	// ReadOnly mirrors the C# `isReadOnly` import flag (copy vs move).
	ReadOnly bool
}

// Manager is the Go-port equivalent of C# `IManageExtraFiles` /
// `ExtraFileManager<TExtraFile>`. The orchestrator asks each manager (in
// [Manager.Order]) whether it claims a candidate file
// ([Manager.CanImportFile]) — first claim wins — then hands each manager
// the files it claimed via [Manager.ImportFiles].
type Manager interface {
	// Order is the deterministic claim order (lower = earlier). Mirrors
	// C# `ExtraFileManager.Order` (Metadata=0, Subtitle=1, Other=2).
	Order() int
	// CanImportFile reports whether this manager claims the file. Only
	// path + extension matter for the claim decision.
	CanImportFile(path, extension string) bool
	// ImportFiles imports the claimed files that belong to the episode
	// (filename-prefix or season/episode match), returning the count
	// imported. Per-file failures are logged and skipped (mirrors the C#
	// per-file try/catch).
	ImportFiles(ctx context.Context, in ImportInput, files []string) (int, error)
}

// matchesEpisode reports whether an extra file belongs to the imported
// episode. Mirrors the shared C# matching in
// `OtherExtraService.ImportFiles` (`:115-133`) + `SubtitleService`:
//
//  1. the extra file's stem starts with the source file's stem
//     (case-insensitive) — the common "Show.S01E02.*" companion case; OR
//  2. the extra file PARSES to the same season + episode-number set as
//     the source file (`ParsePath`); a file with no episode numbers
//     never matches via this arm.
func matchesEpisode(sourceFileName string, info *parser.ParsedEpisodeInfo, filePath string) bool {
	stem := stemOf(filePath)
	if sourceFileName != "" && strings.HasPrefix(strings.ToLower(stem), strings.ToLower(sourceFileName)) {
		return true
	}
	parsed := parser.ParsePath(filePath)
	if parsed == nil || len(parsed.EpisodeNumbers) == 0 || info == nil {
		return false
	}
	return parsed.SeasonNumber == info.SeasonNumber && sameInts(parsed.EpisodeNumbers, info.EpisodeNumbers)
}

// stemOf returns a path's base filename without its extension.
func stemOf(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ExtraFileStore persists "other" extra-file rows. Production binds an
// adapter over `*extrafiles.Repo`. Mirrors C# `IOtherExtraFileService.Upsert`.
type ExtraFileStore interface {
	Upsert(ctx context.Context, f *extrafiles.ExtraFile) error
}

// OtherExtraManager ports `Extras/Others/OtherExtraService.cs` — the
// catch-all manager (order 2) that imports any companion file the earlier
// managers didn't claim (`.nfo`, `.txt`, artwork, etc.). It claims every
// file ([CanImportFile] always true) and imports the ones matching the
// episode, with a single-`.nfo` de-duplication.
type OtherExtraManager struct {
	store ExtraFileStore
	t     FileTransferrer
	cfg   HardlinkConfig
	log   *slog.Logger
}

// NewOtherExtraManager builds the manager. store / t / cfg MUST be
// non-nil; log defaults to [slog.Default].
func NewOtherExtraManager(store ExtraFileStore, t FileTransferrer, cfg HardlinkConfig, log *slog.Logger) *OtherExtraManager {
	if log == nil {
		log = slog.Default()
	}
	return &OtherExtraManager{store: store, t: t, cfg: cfg, log: log}
}

// Order returns 2 (C# `OtherExtraService.Order`).
func (*OtherExtraManager) Order() int { return 2 }

// CanImportFile always returns true — the catch-all claims anything the
// earlier managers passed up. Mirrors C# `OtherExtraService.CanImportFile`
// (`:84-87`).
func (*OtherExtraManager) CanImportFile(string, string) bool { return true }

// ImportFiles ports `OtherExtraService.ImportFiles` (`:89-156`).
func (m *OtherExtraManager) ImportFiles(ctx context.Context, in ImportInput, files []string) (int, error) {
	if in.Series == nil || in.EpisodeFile == nil {
		return 0, nil
	}
	sourceName := stemOf(in.SourcePath)

	// Match pass (with the single-.nfo de-dup from `:104-133`).
	matching := make([]string, 0, len(files))
	hasNfo := false
	for _, f := range files {
		if strings.EqualFold(filepath.Ext(f), ".nfo") {
			if hasNfo {
				continue
			}
			hasNfo = true
		}
		if matchesEpisode(sourceName, in.FileInfo, f) {
			matching = append(matching, f)
		}
	}

	// Import pass (`:135-154`).
	count := 0
	for _, f := range matching {
		ext := filepath.Ext(f)
		rel, err := importFile(m.t, m.cfg.CopyUsingHardlinks(), in.Series.Path, in.EpisodeFile.RelativePath, f, "", ext, in.ReadOnly)
		if err != nil {
			m.log.Warn("extras: import other-extra failed", "file", f, "err", err)
			continue
		}
		row := &extrafiles.ExtraFile{
			SeriesID:      in.Series.ID,
			SeasonNumber:  in.EpisodeFile.SeasonNumber,
			EpisodeFileID: in.EpisodeFile.ID,
			RelativePath:  rel,
			Extension:     ext,
		}
		if err := m.store.Upsert(ctx, row); err != nil {
			m.log.Warn("extras: persist other-extra failed", "path", rel, "err", err)
			continue
		}
		count++
	}
	return count, nil
}

// Compile-time interface satisfaction.
var _ Manager = (*OtherExtraManager)(nil)
