package mediafiles

// DiskProvider is the slice of `NzbDrone.Common.Disk.IDiskProvider` the
// C# `DiskScanService` reaches for. Eight methods cover the full
// scan-orchestration surface — folder existence + emptiness gating, the
// recursive file walk, the missing-folder create branch, the
// post-scan empty-folder cleanup, the optional Linux permission set,
// and the per-file size lookup the size-update loop needs.
//
// Pulled out as a narrow interface so tests can inject a stub without
// dragging in a real disk-provider port. Same convention as
// `rootfolders.DiskProvider` and `healthcheck.RecyclingBinDiskProvider`
// — the wider real impl ports alongside the rest of `IDiskProvider`
// at the host boundary.
//
// Methods, in the order DiskScanService.cs calls them:
//
//   - FolderExists / FolderEmpty  — the two scan-skipped gates and
//     the post-scan empty-folder check (`DiskScanService.cs:81-97,
//     181, 294`).
//   - GetFiles                    — the recursive walk feeding
//     GetVideoFiles + GetNonVideoFiles (`:206, 221`).
//   - CreateFolder                — the CreateEmptySeriesFolders
//     branch (`:114`).
//   - RemoveEmptySubfolders       — the post-scan cleanup
//     (`:292`).
//   - DeleteFolder                — the trailing top-level delete in
//     RemoveEmptySeriesFolder (`:296`).
//   - SetPermissions              — the optional Linux chmod/chown
//     applied to a freshly-created folder (`:279`).
//   - GetFileSize                 — the per-file lookup driving the
//     size-update loop (`:153`).
type DiskProvider interface {
	// FolderExists mirrors `IDiskProvider.FolderExists(path)` at the C# side.
	FolderExists(path string) bool

	// FolderEmpty mirrors `IDiskProvider.FolderEmpty(path)` — true when
	// the folder exists AND has zero entries (files or subfolders).
	FolderEmpty(path string) bool

	// GetFiles mirrors `IDiskProvider.GetFiles(path, recursive)` —
	// returns the absolute paths of files under `path`. When `recursive`
	// is true the walk descends into subfolders; when false only the
	// top level is returned. Errors propagate from the underlying disk
	// walk (a missing folder, a permissions issue) so callers can
	// distinguish "no files" from "couldn't read".
	GetFiles(path string, recursive bool) ([]string, error)

	// CreateFolder mirrors `IDiskProvider.CreateFolder(path)`. Used by
	// the CreateEmptySeriesFolders branch; idempotent on the C# side
	// (no-op if the folder already exists).
	CreateFolder(path string) error

	// RemoveEmptySubfolders mirrors `IDiskProvider.RemoveEmptySubfolders(path)`.
	// Walks `path` bottom-up, deleting any subfolder that holds zero
	// files. The top-level `path` itself is NOT deleted by this call —
	// DiskScanService follows it with a `FolderEmpty` + `DeleteFolder`
	// pair to delete the series root if it ends up empty.
	RemoveEmptySubfolders(path string) error

	// DeleteFolder mirrors `IDiskProvider.DeleteFolder(path, recursive)`.
	// DiskScanService passes `true` for the trailing series-folder
	// delete in `RemoveEmptySeriesFolder` (`:296`).
	DeleteFolder(path string, recursive bool) error

	// SetPermissions mirrors `IDiskProvider.SetPermissions(path, mask, group)`.
	// Used by the optional Linux permission-set after CreateFolder.
	// Errors propagate; DiskScanService.SetPermissions logs+swallows
	// at the C# site (`:281-285`) and the Go port replicates that
	// suppression at the Service layer (slice 4), not here.
	SetPermissions(path, mask, group string) error

	// GetFileSize mirrors `IDiskProvider.GetFileSize(path)`. Used by
	// the size-update loop (`:153`). Returns the size in bytes; errors
	// surface to the caller (the C# side returns 0 on missing files,
	// but the Go port surfaces the error so the loop can decide).
	GetFileSize(path string) (int64, error)
}
