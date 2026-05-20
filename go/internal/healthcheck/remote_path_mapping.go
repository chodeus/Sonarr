package healthcheck

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"

	"sonarr.tv/sonarr/internal/disk"
	"sonarr.tv/sonarr/internal/ospath"
)

// RemotePathMappingCheckName is the canonical Source identifier.
const RemotePathMappingCheckName = "RemotePathMappingCheck"

// RemotePathMappingCheckConfig surfaces the
// EnableCompletedDownloadHandling guard the check consults before
// inspecting any download client. Matches the C# `:51-54` early-exit
// — when CDH is disabled, the check has nothing to do.
type RemotePathMappingCheckConfig interface {
	EnableCompletedDownloadHandling() bool
}

// RemotePathMappingCheckOsInfo surfaces the IsDocker flag the
// check branches on. The runtime OS name (used in the error
// messages) is captured at construction time.
type RemotePathMappingCheckOsInfo interface {
	IsDocker() bool
}

// RemotePathMappingFolderExister narrows
// [*disk.Provider.FolderExists] for the check. The check uses this
// to distinguish "OutputRootFolder is a valid OS-shaped path but
// doesn't exist on disk" from "OutputRootFolder is not even a
// valid path for the current OS".
type RemotePathMappingFolderExister interface {
	FolderExists(path string) bool
}

// RemotePathMappingCheck ports the C# scheduled-Check() path at
// `RemotePathMappingCheck.cs:48-182`. Walks every enabled
// download client's `OutputRootFolders` and surfaces the first
// failure with the most-specific Reason:
//
//   - !folder.IsValid (cross-OS path) + !IsLocalhost →
//     [ReasonRemotePathMappingWrongOSPath].
//   - !folder.IsValid + IsDocker → [ReasonRemotePathMappingBadDockerPath].
//   - !folder.IsValid + (localhost, non-docker) →
//     [ReasonRemotePathMappingLocalWrongOSPath].
//   - !FolderExists + IsDocker → [ReasonRemotePathMappingDockerFolderMissing].
//   - !FolderExists + !IsLocalhost → [ReasonRemotePathMappingLocalFolderMissing].
//   - !FolderExists + (localhost, non-docker) → [ReasonRemotePathMappingGenericPermissions].
//
// The C# event-driven Check(IEvent) path that handles
// EpisodeImportFailedEvent (further 7 reasons —
// DownloadPermissionsEpisode, FileRemoved, ImportEpisodeFailed,
// FilesWrongOSPath, FilesBadDockerPath, FilesLocalWrongOSPath,
// FilesGenericPermissions, FolderPermissions,
// RemoteDownloadClient) is deferred — that's the
// import-pipeline-driven fan-out which the Go-port import service
// can wire up in a follow-up slice. The scheduled Check() arm is
// the most operationally valuable since it runs on the periodic
// healthcheck cadence + on every DC-config change.
//
// Audit fix B.5 (last 387-LOC C# check to port).
type RemotePathMappingCheck struct {
	cfg      RemotePathMappingCheckConfig
	clients  DownloadClientStatusEnumerator
	folderFn RemotePathMappingFolderExister
	osInfo   RemotePathMappingCheckOsInfo
	osName   string
	log      *slog.Logger
}

// NewRemotePathMappingCheck builds the check. All four narrow
// interfaces are required; nil log defaults to slog.Default.
//
// osName is the human-readable OS string (e.g. "linux" / "windows"
// / "darwin") surfaced in the cross-OS-path error messages. Pass
// `runtime.GOOS` at the wiring site; tests pass a pinned string
// for deterministic message assertions.
func NewRemotePathMappingCheck(
	cfg RemotePathMappingCheckConfig,
	clients DownloadClientStatusEnumerator,
	folderFn RemotePathMappingFolderExister,
	osInfo RemotePathMappingCheckOsInfo,
	osName string,
	log *slog.Logger,
) (*RemotePathMappingCheck, error) {
	if cfg == nil {
		return nil, fmt.Errorf("healthcheck: NewRemotePathMappingCheck: nil cfg")
	}
	if clients == nil {
		return nil, fmt.Errorf("healthcheck: NewRemotePathMappingCheck: nil clients")
	}
	if folderFn == nil {
		return nil, fmt.Errorf("healthcheck: NewRemotePathMappingCheck: nil folderFn")
	}
	if osInfo == nil {
		return nil, fmt.Errorf("healthcheck: NewRemotePathMappingCheck: nil osInfo")
	}
	if osName == "" {
		osName = runtime.GOOS
	}
	if log == nil {
		log = slog.Default()
	}
	return &RemotePathMappingCheck{
		cfg:      cfg,
		clients:  clients,
		folderFn: folderFn,
		osInfo:   osInfo,
		osName:   osName,
		log:      log,
	}, nil
}

// Name returns the canonical identifier.
func (*RemotePathMappingCheck) Name() string { return RemotePathMappingCheckName }

// CheckOnStartup defaults true (HealthCheckBase default).
func (*RemotePathMappingCheck) CheckOnStartup() bool { return true }

// CheckOnSchedule defaults true (HealthCheckBase default).
func (*RemotePathMappingCheck) CheckOnSchedule() bool { return true }

// Run returns the healthcheck result. Short-circuits to OK when
// EnableCompletedDownloadHandling is disabled — matches C# `:51-54`.
// Walks each enabled DC's OutputRootFolders; the FIRST failure
// short-circuits the loop and surfaces (matches C# `:48-182` which
// returns the first failing HealthCheck).
func (c *RemotePathMappingCheck) Run(ctx context.Context) HealthCheck {
	if c == nil {
		return OK(RemotePathMappingCheckName)
	}
	if !c.cfg.EnableCompletedDownloadHandling() {
		return OK(RemotePathMappingCheckName)
	}
	clients, err := c.clients.EnabledDownloadClients(ctx)
	if err != nil {
		// Enumerate failure → pass silently (matches the C# tree's
		// per-client try/catch — failure to enumerate isn't a
		// blocking condition itself).
		c.log.LogAttrs(ctx, slog.LevelWarn,
			"remotepathmapping: enumerate clients failed",
			slog.Any("err", err))
		return OK(RemotePathMappingCheckName)
	}
	for _, dc := range clients {
		if dc.Err != nil || dc.Status == nil {
			c.log.LogAttrs(ctx, slog.LevelDebug,
				"remotepathmapping: skipping failed client",
				slog.String("client", dc.Name),
				slog.Any("err", dc.Err))
			continue
		}
		if hc, hit := c.evaluateClient(dc); hit {
			return hc
		}
	}
	return OK(RemotePathMappingCheckName)
}

// evaluateClient walks the OutputRootFolders for one DC and
// returns (healthcheck, true) on the first failure. Returns
// (zero, false) when all folders pass.
func (c *RemotePathMappingCheck) evaluateClient(dc EnumeratedDownloadClient) (HealthCheck, bool) {
	for _, folder := range dc.Status.OutputRootFolders {
		if !isValidForCurrentOS(folder) {
			return c.invalidPathFailure(dc, folder), true
		}
		if !c.folderFn.FolderExists(folder.FullPath()) {
			return c.missingFolderFailure(dc, folder), true
		}
	}
	return HealthCheck{}, false
}

// isValidForCurrentOS reports whether the path's OsPath kind
// matches the runtime OS — i.e. a Unix-shaped path on Linux/Darwin
// or a Windows-shaped path on Windows. Mirrors the C# `OsPath.IsValid`
// semantic (which fails when a path is the wrong shape for the
// current host).
func isValidForCurrentOS(p ospath.OsPath) bool {
	if p.IsEmpty() || !p.IsRooted() {
		return false
	}
	if runtime.GOOS == "windows" {
		return p.IsWindows()
	}
	// Linux / Darwin / *BSD — Unix-shaped only.
	return p.IsUnix()
}

func (c *RemotePathMappingCheck) invalidPathFailure(dc EnumeratedDownloadClient, folder ospath.OsPath) HealthCheck {
	full := folder.FullPath()
	if !dc.Status.IsLocalhost {
		return New(RemotePathMappingCheckName,
			ResultError,
			ReasonRemotePathMappingWrongOSPath,
			fmt.Sprintf("Download client %s places downloads in %s but this is not a valid %s path. Review your remote path mappings and download client settings.", dc.Name, full, c.osName),
			"#bad-remote-path-mapping")
	}
	if c.osInfo.IsDocker() {
		return New(RemotePathMappingCheckName,
			ResultError,
			ReasonRemotePathMappingBadDockerPath,
			fmt.Sprintf("Docker: download client %s places downloads in %s but this is not a valid %s path. Review your remote path mappings and Docker volume mounts.", dc.Name, full, c.osName),
			"#docker-bad-remote-path-mapping")
	}
	return New(RemotePathMappingCheckName,
		ResultError,
		ReasonRemotePathMappingLocalWrongOSPath,
		fmt.Sprintf("Local download client %s places downloads in %s but this is not a valid %s path. Review your download client settings.", dc.Name, full, c.osName),
		"#bad-download-client-settings")
}

func (c *RemotePathMappingCheck) missingFolderFailure(dc EnumeratedDownloadClient, folder ospath.OsPath) HealthCheck {
	full := folder.FullPath()
	if c.osInfo.IsDocker() {
		return New(RemotePathMappingCheckName,
			ResultError,
			ReasonRemotePathMappingDockerFolderMissing,
			fmt.Sprintf("Docker: download client %s places downloads in %s but this directory does not appear to exist inside the container. Review your Docker volume mounts and remote path mappings.", dc.Name, full),
			"#docker-bad-remote-path-mapping")
	}
	if !dc.Status.IsLocalhost {
		return New(RemotePathMappingCheckName,
			ResultError,
			ReasonRemotePathMappingLocalFolderMissing,
			fmt.Sprintf("Remote download client %s places downloads in %s but Sonarr cannot see this directory. Review your remote path mappings and ensure the directory is mounted locally.", dc.Name, full),
			"#bad-remote-path-mapping")
	}
	return New(RemotePathMappingCheckName,
		ResultError,
		ReasonRemotePathMappingGenericPermissions,
		fmt.Sprintf("Local download client %s places downloads in %s but Sonarr cannot see this directory. Verify the path exists and that the user running Sonarr has read access.", dc.Name, full),
		"#permissions-error")
}

// Compile-time guard that *disk.Provider satisfies the narrow
// folder-exister contract this check expects. Pinned here so a
// regression to disk.Provider.FolderExists's signature surfaces at
// build time, not at boot.
var _ RemotePathMappingFolderExister = (*disk.Provider)(nil)
