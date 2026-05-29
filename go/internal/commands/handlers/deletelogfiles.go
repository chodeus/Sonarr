// DeleteLogFiles + DeleteUpdateLogFiles handlers. Port the
// IExecute<DeleteLogFilesCommand> + IExecute<DeleteUpdateLogFilesCommand>
// implementations at `src/NzbDrone.Core/Instrumentation/DeleteLogFilesService.cs:26-37`.
//
// These back the Settings → General "Delete log files" / "Delete update
// log files" UI buttons (POST /api/v3/command {"name":"DeleteLogFiles"}
// / {"name":"DeleteUpdateLogFiles"}). Each empties the corresponding log
// folder (EmptyFolder = delete every file + subfolder, keep the folder).

package handlers

import (
	"context"
	"log/slog"
)

// DeleteLogFilesName / DeleteUpdateLogFilesName are the registry keys +
// Commands.Name. Mirror the C# short forms.
const (
	DeleteLogFilesName       = "DeleteLogFiles"
	DeleteUpdateLogFilesName = "DeleteUpdateLogFiles"
)

// LogFolderCleaner empties a folder (files + subfolders, keeping the
// folder). *disk.Provider satisfies it via EmptyFolder.
type LogFolderCleaner interface {
	EmptyFolder(path string) error
}

// folderEmptier is the shared body for both delete-log commands: empty
// one configured folder. A nil cleaner or empty path logs the stub line
// + returns nil (the registry slot still satisfies "command exists").
type folderEmptier struct {
	cleaner LogFolderCleaner
	folder  string
	name    string
	log     *slog.Logger
}

func (h folderEmptier) execute(ctx context.Context) error {
	if h.cleaner == nil || h.folder == "" {
		h.log.LogAttrs(ctx, slog.LevelInfo, "deletelogfiles: stub Execute (cleaner/folder not wired)",
			slog.String("command", h.name))
		return nil
	}
	h.log.LogAttrs(ctx, slog.LevelDebug, "deletelogfiles: emptying folder",
		slog.String("command", h.name), slog.String("folder", h.folder))
	if err := h.cleaner.EmptyFolder(h.folder); err != nil {
		return err
	}
	return nil
}

// DeleteLogFiles is the IExecute<DeleteLogFilesCommand> port.
type DeleteLogFiles struct{ e folderEmptier }

// NewDeleteLogFiles builds the handler from the log folder + cleaner.
func NewDeleteLogFiles(cleaner LogFolderCleaner, logFolder string, log *slog.Logger) *DeleteLogFiles {
	if log == nil {
		log = slog.Default()
	}
	return &DeleteLogFiles{e: folderEmptier{cleaner: cleaner, folder: logFolder, name: DeleteLogFilesName, log: log}}
}

func (h *DeleteLogFiles) Execute(ctx context.Context, _ string) error { return h.e.execute(ctx) }

// DeleteUpdateLogFiles is the IExecute<DeleteUpdateLogFilesCommand> port.
type DeleteUpdateLogFiles struct{ e folderEmptier }

// NewDeleteUpdateLogFiles builds the handler from the update-log folder.
func NewDeleteUpdateLogFiles(cleaner LogFolderCleaner, updateLogFolder string, log *slog.Logger) *DeleteUpdateLogFiles {
	if log == nil {
		log = slog.Default()
	}
	return &DeleteUpdateLogFiles{e: folderEmptier{cleaner: cleaner, folder: updateLogFolder, name: DeleteUpdateLogFilesName, log: log}}
}

func (h *DeleteUpdateLogFiles) Execute(ctx context.Context, _ string) error { return h.e.execute(ctx) }
