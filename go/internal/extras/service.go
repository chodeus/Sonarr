package extras

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
)

// ExtraConfig narrows the `IConfigService` settings the orchestrator
// reads. *config.Provider satisfies it.
type ExtraConfig interface {
	// ImportExtraFiles is the master on/off switch (default false).
	ImportExtraFiles() bool
	// ExtraFileExtensions is the comma-separated wanted-extension list
	// (default "srt").
	ExtraFileExtensions() string
}

// FolderEnumerator lists the files in the download/source folder.
// *disk.Provider satisfies it via GetFiles.
type FolderEnumerator interface {
	GetFiles(folder string, recursive bool) ([]string, error)
}

// ExtraService ports the C# `ExtraService` orchestrator
// (`Extras/ExtraService.cs`). At import time it enumerates the source
// folder, keeps the files whose extension is in the configured
// ExtraFileExtensions, lets each [Manager] (in Order) claim the ones it
// handles (first claim wins), and hands each manager its claimed files.
type ExtraService struct {
	managers []Manager
	folders  FolderEnumerator
	cfg      ExtraConfig
	log      *slog.Logger
}

// NewExtraService builds the orchestrator. managers are sorted by Order
// (ascending) so the claim order is deterministic regardless of the
// caller's slice order. folders + cfg MUST be non-nil; log defaults to
// [slog.Default].
func NewExtraService(managers []Manager, folders FolderEnumerator, cfg ExtraConfig, log *slog.Logger) (*ExtraService, error) {
	if folders == nil {
		return nil, fmt.Errorf("extras: NewExtraService: nil FolderEnumerator")
	}
	if cfg == nil {
		return nil, fmt.Errorf("extras: NewExtraService: nil ExtraConfig")
	}
	if log == nil {
		log = slog.Default()
	}
	sorted := append([]Manager(nil), managers...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Order() < sorted[j].Order() })
	return &ExtraService{managers: sorted, folders: folders, cfg: cfg, log: log}, nil
}

// ImportEpisode ports `ExtraService.ImportExtraFiles` (`:61-103`).
// Returns the total number of extra files imported across all managers.
// A nil/empty managers list, the config switch being off, or a folder
// read error all yield (0, nil) — extras are best-effort and must never
// fail the episode import.
func (s *ExtraService) ImportEpisode(ctx context.Context, in ImportInput) (int, error) {
	if s == nil || len(s.managers) == 0 {
		return 0, nil
	}
	if !s.cfg.ImportExtraFiles() {
		return 0, nil
	}
	if in.Series == nil || in.EpisodeFile == nil || in.SourcePath == "" {
		return 0, nil
	}

	wanted := make(map[string]struct{})
	for _, ext := range ParseExtraFileExtensions(s.cfg.ExtraFileExtensions()) {
		wanted[ext] = struct{}{}
	}
	if len(wanted) == 0 {
		return 0, nil
	}

	sourceFolder := filepath.Dir(in.SourcePath)
	files, err := s.folders.GetFiles(sourceFolder, in.Recursive)
	if err != nil {
		s.log.Warn("extras: enumerate source folder failed", "folder", sourceFolder, "err", err)
		return 0, nil
	}

	// First-claim-wins delegation (`:78-96`).
	managed := make([][]string, len(s.managers))
	for _, f := range files {
		ext := normalizeExtension(filepath.Ext(f))
		if _, ok := wanted[ext]; !ok {
			continue
		}
		for i, m := range s.managers {
			if m.CanImportFile(f, ext) {
				managed[i] = append(managed[i], f)
				break
			}
		}
	}

	total := 0
	for i, m := range s.managers {
		if len(managed[i]) == 0 {
			continue
		}
		n, err := m.ImportFiles(ctx, in, managed[i])
		if err != nil {
			s.log.Warn("extras: manager ImportFiles failed", "order", m.Order(), "err", err)
		}
		total += n
	}
	return total, nil
}
