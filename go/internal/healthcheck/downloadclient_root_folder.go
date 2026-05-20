package healthcheck

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"

	"sonarr.tv/sonarr/internal/configfile"
	"sonarr.tv/sonarr/internal/ospath"
)

// DownloadClientRootFolderCheckName is the canonical Source identifier.
const DownloadClientRootFolderCheckName = "DownloadClientRootFolderCheck"

// DCRootFolderLister returns the user-configured root-folder paths
// (Path column only — the check doesn't need accessible/free space).
// Production wires this over `*rootfolders.Repo`.
type DCRootFolderLister interface {
	AllRootFolderPaths(ctx context.Context) ([]string, error)
}

// DownloadClientRootFolderCheck warns when an enabled download
// client's output folder OVERLAPS a configured root folder — Sonarr
// imports from the DC's path into the root folder, and if the two
// are the same (or one is a parent of the other) the import becomes
// a same-folder rename that conflicts with how the import pipeline
// distinguishes pre-import vs post-import locations.
//
// Mirrors `src/NzbDrone.Core/HealthCheck/Checks/DownloadClientRootFolderCheck.cs:1-83`.
//
// C# short-circuits on the FIRST DC × root-folder pair that overlaps;
// the Go port keeps the same semantic.
//
// Reuses the `DownloadClientStatusEnumerator` introduced for B.5d
// (the same Provider.GetStatus call returns OutputRootFolders too).
type DownloadClientRootFolderCheck struct {
	clients DownloadClientStatusEnumerator
	roots   DCRootFolderLister
	log     *slog.Logger
}

// NewDownloadClientRootFolderCheck builds the check. clients + roots
// MUST be non-nil. log defaults to slog.Default() when nil.
func NewDownloadClientRootFolderCheck(clients DownloadClientStatusEnumerator, roots DCRootFolderLister, log *slog.Logger) (*DownloadClientRootFolderCheck, error) {
	if clients == nil {
		return nil, fmt.Errorf("healthcheck: NewDownloadClientRootFolderCheck: nil DownloadClientStatusEnumerator")
	}
	if roots == nil {
		return nil, fmt.Errorf("healthcheck: NewDownloadClientRootFolderCheck: nil DCRootFolderLister")
	}
	if log == nil {
		log = slog.Default()
	}
	return &DownloadClientRootFolderCheck{clients: clients, roots: roots, log: log}, nil
}

// Name returns the canonical check identifier.
func (*DownloadClientRootFolderCheck) Name() string { return DownloadClientRootFolderCheckName }

// CheckOnStartup defaults true.
func (*DownloadClientRootFolderCheck) CheckOnStartup() bool { return true }

// CheckOnSchedule defaults true.
func (*DownloadClientRootFolderCheck) CheckOnSchedule() bool { return true }

// EventTriggers mirrors the four C# `[CheckOn]` attributes at
// `DownloadClientRootFolderCheck.cs:17-20`. The C# provider-lifecycle
// + RootFolder + RemotePathMapping events aren't ported yet; closest
// proxy is the config-saved event.
func (*DownloadClientRootFolderCheck) EventTriggers() []EventTrigger {
	return []EventTrigger{
		{EventType: reflect.TypeOf(configfile.ConfigFileSavedEvent{})},
	}
}

// Run mirrors the C# `Check()` method at `:39-81`.
func (c *DownloadClientRootFolderCheck) Run(ctx context.Context) HealthCheck {
	rootPaths, err := c.roots.AllRootFolderPaths(ctx)
	if err != nil {
		return New(
			DownloadClientRootFolderCheckName,
			ResultWarning,
			ReasonDownloadClientRootFolder,
			"Unable to enumerate root folders: "+err.Error(),
			"#downloads-in-root-folder",
		)
	}
	if len(rootPaths) == 0 {
		return OK(DownloadClientRootFolderCheckName)
	}

	clients, err := c.clients.EnabledDownloadClients(ctx)
	if err != nil {
		return New(
			DownloadClientRootFolderCheckName,
			ResultWarning,
			ReasonDownloadClientRootFolder,
			"Unable to enumerate download clients: "+err.Error(),
			"#downloads-in-root-folder",
		)
	}

	for _, dc := range clients {
		if dc.Err != nil {
			c.log.Debug("downloadClient root-folder check: skipping client",
				slog.String("client", dc.Name),
				slog.Any("err", dc.Err),
			)
			continue
		}
		if dc.Status == nil {
			continue
		}
		for _, out := range dc.Status.OutputRootFolders {
			outPath := out.FullPath()
			if outPath == "" {
				continue
			}
			for _, root := range rootPaths {
				if rootOverlapsOutput(root, outPath) {
					return New(
						DownloadClientRootFolderCheckName,
						ResultWarning,
						ReasonDownloadClientRootFolder,
						fmt.Sprintf("Download client %s places downloads in root folder %s; configure a separate download folder to prevent import conflicts", dc.Name, root),
						"#downloads-in-root-folder",
					)
				}
			}
		}
	}
	return OK(DownloadClientRootFolderCheckName)
}

// rootOverlapsOutput returns true when `root` equals OR is a parent
// of `output`. Mirrors C# `r.Path.PathEquals(folder.FullPath) ||
// r.Path.IsParentPath(folder.FullPath)` at
// `DownloadClientRootFolderCheck.cs:51`.
//
// Uses `ospath` for the platform-aware equality + ancestry checks —
// case-insensitive on Windows + macOS, case-sensitive on Linux per
// `ospath.OsPath.Equals`.
func rootOverlapsOutput(root, output string) bool {
	r := ospath.New(root)
	o := ospath.New(output)
	if r.EqualsIgnoreTrailingSlash(o) {
		return true
	}
	return ospath.IsParent(root, output)
}
