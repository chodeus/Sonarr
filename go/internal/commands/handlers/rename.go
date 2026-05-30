// RenameFiles + RenameSeries handlers. Port the
// IExecute<RenameFilesCommand> + IExecute<RenameSeriesCommand>
// implementations at
// `src/NzbDrone.Core/MediaFiles/RenameEpisodeFileService.cs:131-208`.
//
// Both rename episode files to their canonical NamingConfig path by
// delegating each file to [episodefilemoving.Service.MoveEpisodeFile]
// (which recomputes the destination + transfers the file) then
// persisting the updated row via the episode-file service Update.
// RenameFiles renames a caller-selected file list for one series;
// RenameSeries renames every file of each requested series.

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"sonarr.tv/sonarr/internal/episodefilemoving"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/series"
)

// RenameFilesName / RenameSeriesName are the registry keys + Commands.Name
// (C# short forms `RenameFilesCommand` → `RenameFiles`, etc.).
const (
	RenameFilesName  = "RenameFiles"
	RenameSeriesName = "RenameSeries"
)

// RenameSeriesLookup resolves a series by id. *series.Service satisfies it.
type RenameSeriesLookup interface {
	GetSeries(ctx context.Context, id int) (*series.Series, error)
}

// RenameFileLookup loads episode-file rows by id or by series.
// *episodefiles.Service satisfies it.
type RenameFileLookup interface {
	GetFiles(ctx context.Context, ids []int) ([]episodefiles.File, error)
	GetFilesBySeries(ctx context.Context, seriesID int) ([]episodefiles.File, error)
}

// EpisodeFileMover renames/moves a file to its canonical path, returning
// the file with its updated RelativePath. *episodefilemoving.Service
// satisfies it.
type EpisodeFileMover interface {
	MoveEpisodeFile(ctx context.Context, file *episodefiles.File, ser *series.Series) (*episodefiles.File, error)
}

// EpisodeFileRowUpdater persists a moved file's new RelativePath.
// *episodefiles.Service satisfies it.
type EpisodeFileRowUpdater interface {
	Update(ctx context.Context, file *episodefiles.File) error
}

// RenameEventPublisher publishes the rename-path events (J453): the
// per-file [episodefiles.EpisodeFileRenamedEvent] (→ history row) and
// the per-series [episodefiles.SeriesRenamedEvent] (→ metadata/extras
// MoveFilesAfterRename). Optional — when nil the rename still moves +
// persists files, just without firing the events (prior behavior). A
// boot-time bus adapter satisfies it. Ports the two PublishEvent calls
// in C# `RenameEpisodeFileService.RenameFiles` (`:155` + `:174`).
type RenameEventPublisher interface {
	PublishEpisodeFileRenamed(ctx context.Context, ev episodefiles.EpisodeFileRenamedEvent) error
	PublishSeriesRenamed(ctx context.Context, ev episodefiles.SeriesRenamedEvent) error
}

// EmptySubfolderRemover prunes now-empty directories left under the
// series folder after files are renamed/moved out of them. *disk.Provider
// satisfies it. Optional — when nil the cleanup is skipped. Ports the C#
// `_diskProvider.RemoveEmptySubfolders(series.Path)` call in
// `RenameEpisodeFileService.RenameFiles` (`:173`), which runs once after
// the per-file loop when at least one file was renamed.
type EmptySubfolderRemover interface {
	RemoveEmptySubfolders(path string) error
}

// RenameDeps bundles the collaborators both rename handlers share.
type RenameDeps struct {
	Series  RenameSeriesLookup
	Files   RenameFileLookup
	Mover   EpisodeFileMover
	Updater EpisodeFileRowUpdater
	// Publisher is optional (J453); nil disables rename-event publication.
	Publisher RenameEventPublisher
	// Disk is optional (J463); nil disables the post-rename
	// empty-subfolder cleanup (C# RenameEpisodeFileService.cs:173).
	Disk EmptySubfolderRemover
}

func (d RenameDeps) valid() bool {
	return d.Series != nil && d.Files != nil && d.Mover != nil && d.Updater != nil
}

// RenameFiles is the IExecute<RenameFilesCommand> port.
type RenameFiles struct {
	deps RenameDeps
	log  *slog.Logger
}

// NewRenameFiles builds the handler. All four deps must be non-nil.
func NewRenameFiles(deps RenameDeps, log *slog.Logger) (*RenameFiles, error) {
	if !deps.valid() {
		return nil, errors.New("handlers: NewRenameFiles: nil RenameDeps field")
	}
	if log == nil {
		log = slog.Default()
	}
	return &RenameFiles{deps: deps, log: log}, nil
}

type renameFilesBody struct {
	SeriesID int   `json:"seriesId"`
	Files    []int `json:"files"`
}

// Execute renames the caller-selected files for one series (C# `:187-196`).
func (h *RenameFiles) Execute(ctx context.Context, body string) error {
	var cmd renameFilesBody
	if err := decodeCommandBody(body, &cmd); err != nil {
		return fmt.Errorf("handlers: RenameFiles: parse body: %w", err)
	}
	if cmd.SeriesID <= 0 || len(cmd.Files) == 0 {
		return nil
	}
	ser, err := h.deps.Series.GetSeries(ctx, cmd.SeriesID)
	if err != nil || ser == nil {
		return fmt.Errorf("handlers: RenameFiles: resolve series %d: %w", cmd.SeriesID, err)
	}
	files, err := h.deps.Files.GetFiles(ctx, cmd.Files)
	if err != nil {
		return fmt.Errorf("handlers: RenameFiles: load files: %w", err)
	}
	renameEpisodeFiles(ctx, h.deps, ser, files, h.log)
	return nil
}

// RenameSeries is the IExecute<RenameSeriesCommand> port.
type RenameSeries struct {
	deps RenameDeps
	log  *slog.Logger
}

// NewRenameSeries builds the handler. All four deps must be non-nil.
func NewRenameSeries(deps RenameDeps, log *slog.Logger) (*RenameSeries, error) {
	if !deps.valid() {
		return nil, errors.New("handlers: NewRenameSeries: nil RenameDeps field")
	}
	if log == nil {
		log = slog.Default()
	}
	return &RenameSeries{deps: deps, log: log}, nil
}

type renameSeriesBody struct {
	SeriesIDs []int `json:"seriesIds"`
}

// Execute renames every file of each requested series (C# `:198-208`).
func (h *RenameSeries) Execute(ctx context.Context, body string) error {
	var cmd renameSeriesBody
	if err := decodeCommandBody(body, &cmd); err != nil {
		return fmt.Errorf("handlers: RenameSeries: parse body: %w", err)
	}
	for _, seriesID := range cmd.SeriesIDs {
		ser, err := h.deps.Series.GetSeries(ctx, seriesID)
		if err != nil || ser == nil {
			h.log.Warn("renameseries: resolve series failed", "seriesId", seriesID, "err", err)
			continue
		}
		files, err := h.deps.Files.GetFilesBySeries(ctx, seriesID)
		if err != nil {
			h.log.Warn("renameseries: list files failed", "seriesId", seriesID, "err", err)
			continue
		}
		renameEpisodeFiles(ctx, h.deps, ser, files, h.log)
	}
	return nil
}

// renameEpisodeFiles ports the per-file loop of C# `RenameFiles(...)`
// at `:130-178`: move each file to its canonical path, persist the
// updated row, and publish a per-file EpisodeFileRenamedEvent. A
// same-source-and-destination result is skipped quietly (C#
// SameFilenameException); any other move error is logged and the loop
// continues (C# broad catch). After the loop, when at least one file
// renamed, a SeriesRenamedEvent is published (C# `:172-176`). Both
// publications are gated on an optional publisher (nil → silent).
func renameEpisodeFiles(ctx context.Context, deps RenameDeps, ser *series.Series, files []episodefiles.File, log *slog.Logger) {
	var renamed []episodefiles.RenamedEpisodeFile
	for i := range files {
		f := files[i]
		previousRelativePath := f.RelativePath
		previousPath := filepath.Join(ser.Path, f.RelativePath)
		moved, err := deps.Mover.MoveEpisodeFile(ctx, &f, ser)
		if err != nil {
			if errors.Is(err, episodefilemoving.ErrSameFilename) {
				log.Debug("rename: source and destination identical; skipping", "fileId", f.ID)
			} else {
				log.Warn("rename: move failed", "fileId", f.ID, "err", err)
			}
			continue
		}
		if err := deps.Updater.Update(ctx, moved); err != nil {
			log.Error("rename: persist moved file failed", "fileId", moved.ID, "err", err)
		}
		renamed = append(renamed, episodefiles.RenamedEpisodeFile{
			EpisodeFile:          moved,
			PreviousPath:         previousPath,
			PreviousRelativePath: previousRelativePath,
		})
		// Per-file event (C# `:155`) → history "renamed" row.
		if deps.Publisher != nil {
			ev := episodefiles.EpisodeFileRenamedEvent{Series: ser, EpisodeFile: moved, OriginalPath: previousPath}
			if err := deps.Publisher.PublishEpisodeFileRenamed(ctx, ev); err != nil {
				log.Warn("rename: publish EpisodeFileRenamedEvent failed", "fileId", moved.ID, "err", err)
			}
		}
	}
	if len(renamed) == 0 {
		return
	}
	// C# RenameEpisodeFileService.cs:173 — after at least one rename,
	// prune empty directories left behind (e.g. an old season folder the
	// files moved out of), BEFORE publishing SeriesRenamedEvent.
	if deps.Disk != nil {
		if err := deps.Disk.RemoveEmptySubfolders(ser.Path); err != nil {
			log.Warn("rename: remove empty subfolders failed", "seriesId", ser.ID, "path", ser.Path, "err", err)
		}
	}
	// Per-series event (C# `:172-176`) → metadata/extras MoveFilesAfterRename.
	if deps.Publisher != nil {
		ev := episodefiles.SeriesRenamedEvent{Series: ser, RenamedFiles: renamed}
		if err := deps.Publisher.PublishSeriesRenamed(ctx, ev); err != nil {
			log.Warn("rename: publish SeriesRenamedEvent failed", "seriesId", ser.ID, "err", err)
		}
	}
}

// decodeCommandBody trims + unmarshals a command body, treating
// empty/`{}` as the zero value.
func decodeCommandBody(body string, out any) error {
	body = strings.TrimSpace(body)
	if body == "" || body == "{}" {
		return nil
	}
	return json.Unmarshal([]byte(body), out)
}
