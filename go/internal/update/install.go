package update

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"

	"sonarr.tv/sonarr/internal/deploymentinfo"
	"sonarr.tv/sonarr/internal/ospath"
)

// This file ports the DECISION layer of C# `InstallUpdateService`
// (`Update/InstallUpdateService.cs`): GetUpdatePackage (the "should we
// install, and may we?" gate), EnsureAppDataSafety (the abort-if-config-
// would-be-clobbered guard), and EnsureValidBranch (redirect the config
// branch when the package's branch differs). The destructive
// download → verify → extract → self-replace steps land in later slices
// behind injected seams; this layer is pure decision + a single
// config-branch write, with no filesystem mutation or process spawn.

// ErrUpdateAborted mirrors C# `UpdateAbortedException` /
// `UpdateFailedException` for the EnsureAppDataSafety abort — the update
// must not proceed because the app's data folder lives inside the
// startup folder and would be destroyed by the in-place upgrade.
var ErrUpdateAborted = errors.New("update: aborted")

// Checker is the available-update lookup the installer gates on.
// *Service satisfies it via AvailableUpdate.
type Checker interface {
	AvailableUpdate(ctx context.Context) (*Package, error)
}

// InstallConfig is the config surface the installer reads. Production
// binds *configfile.Provider via a small adapter (its UpdateMechanism
// returns the persisted PascalCase string, parsed here via
// deploymentinfo.ParseUpdateMechanism — matching C#
// `ConfigFileProvider.UpdateMechanism`'s GetValueEnum).
type InstallConfig interface {
	UpdateAutomatically(ctx context.Context) (bool, error)
	UpdateMechanism(ctx context.Context) (string, error)
	UpdateScriptPath(ctx context.Context) (string, error)
	Branch(ctx context.Context) (string, error)
	SetBranch(ctx context.Context, branch string) error
}

// DeploymentProvider is the "how was this installed" surface. Production
// binds *deploymentinfo.Provider directly.
type DeploymentProvider interface {
	PackageUpdateMechanism() deploymentinfo.UpdateMechanism
	IsExternalUpdateMechanism() bool
}

// Installer ports the decision layer of `InstallUpdateService`. It is
// constructed with the running build's version + the startup/appdata
// folders (plain strings, kept off appenv for testability).
type Installer struct {
	checker      Checker
	cfg          InstallConfig
	deployment   DeploymentProvider
	startupDir   string
	appDataDir   string
	buildVersion string
	logger       *slog.Logger
	run          *RunDeps // wired via SetRunDeps; nil until the install path is needed
}

// NewInstaller binds the decision-layer deps. checker/cfg/deployment are
// required; logger defaults to slog.Default.
func NewInstaller(checker Checker, cfg InstallConfig, deployment DeploymentProvider, startupDir, appDataDir, buildVersion string, logger *slog.Logger) (*Installer, error) {
	if checker == nil {
		return nil, errors.New("update: nil Checker")
	}
	if cfg == nil {
		return nil, errors.New("update: nil InstallConfig")
	}
	if deployment == nil {
		return nil, errors.New("update: nil DeploymentProvider")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Installer{
		checker:      checker,
		cfg:          cfg,
		deployment:   deployment,
		startupDir:   startupDir,
		appDataDir:   appDataDir,
		buildVersion: buildVersion,
		logger:       logger,
	}, nil
}

var majorVersionPattern = regexp.MustCompile(`^(\d+)\.`)

// majorOf parses the leading major component of a dotted version
// string (e.g. "5.0.0.1234" → 5). Returns -1 when unparseable (the
// "dev" build), so a dev build never trips the major-update gate.
func majorOf(version string) int {
	m := majorVersionPattern.FindStringSubmatch(version)
	if len(m) < 2 {
		return -1
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return -1
	}
	return n
}

// GetUpdatePackage ports `InstallUpdateService.GetUpdatePackage`
// (`:234-277`). It fetches the latest available package and applies
// every gate that decides whether an automatic install may proceed,
// returning the package to install or nil when no install should
// happen. Pure decision: only logging side effects.
//
//   - manual is true when the triggering command was operator-initiated
//     (C# `updateTrigger == CommandTrigger.Manual`); it bypasses the
//     UpdateAutomatically gate.
//   - installMajorUpdate permits crossing a major-version boundary
//     (C# `ApplicationUpdateCommand.InstallMajorUpdate`).
func (i *Installer) GetUpdatePackage(ctx context.Context, manual, installMajorUpdate bool) (*Package, error) {
	i.logger.Debug("update: checking for updates")

	latest, err := i.checker.AvailableUpdate(ctx)
	if err != nil {
		return nil, fmt.Errorf("update: check available: %w", err)
	}
	if latest == nil {
		i.logger.Debug("update: no update available")
		return nil, nil
	}

	if majorOf(latest.Version) > majorOf(i.buildVersion) && !installMajorUpdate {
		i.logger.Info("update: unable to install major update, please update manually from System: Updates")
		return nil, nil
	}

	auto, err := i.cfg.UpdateAutomatically(ctx)
	if err != nil {
		return nil, fmt.Errorf("update: read UpdateAutomatically: %w", err)
	}
	if !auto && !manual {
		i.logger.Debug("update: auto-update not enabled, not installing available update")
		return nil, nil
	}

	mech, err := i.mechanism(ctx)
	if err != nil {
		return nil, err
	}
	pkgMech := i.deployment.PackageUpdateMechanism()
	external := i.deployment.IsExternalUpdateMechanism()

	if mech == deploymentinfo.MechanismBuiltIn && pkgMech == deploymentinfo.MechanismDocker {
		i.logger.Debug("update: built-in updater disabled inside a docker container, please update the container image")
		return nil, nil
	}

	// Safety net — ConfigureUpdateMechanism should already prevent
	// invalid settings (C#:264-274).
	if mech == deploymentinfo.MechanismBuiltIn && external {
		i.logger.Debug("update: built-in updater disabled, please use the package manager to install",
			slog.String("mechanism", pkgMech.String()))
		return nil, nil
	} else if mech != deploymentinfo.MechanismScript && external {
		i.logger.Debug("update: update available, please use the package manager to install",
			slog.String("mechanism", pkgMech.String()))
		return nil, nil
	}

	return latest, nil
}

// mechanism reads + parses the configured UpdateMechanism (the config
// getter returns the persisted PascalCase string; an unrecognized value
// falls back to BuiltIn, matching C# GetValueEnum's default).
func (i *Installer) mechanism(ctx context.Context) (deploymentinfo.UpdateMechanism, error) {
	s, err := i.cfg.UpdateMechanism(ctx)
	if err != nil {
		return deploymentinfo.MechanismBuiltIn, fmt.Errorf("update: read UpdateMechanism: %w", err)
	}
	if m, ok := deploymentinfo.ParseUpdateMechanism(s); ok {
		return m, nil
	}
	return deploymentinfo.MechanismBuiltIn, nil
}

// EnsureAppDataSafety ports `InstallUpdateService.EnsureAppDataSafety`
// (`:225-232`). Aborts the update when the app's data folder is the
// startup folder or lives inside it — an in-place upgrade would destroy
// the user's configuration + databases. NEVER weaken this guard.
func (i *Installer) EnsureAppDataSafety() error {
	if ospath.IsParent(i.startupDir, i.appDataDir) ||
		ospath.New(i.startupDir).EqualsIgnoreTrailingSlash(ospath.New(i.appDataDir)) {
		return fmt.Errorf("%w: your Sonarr configuration '%s' is stored in the application folder '%s' which would cause data loss during the upgrade; remove any symlinks or redirects before trying again",
			ErrUpdateAborted, i.appDataDir, i.startupDir)
	}
	return nil
}

// EnsureValidBranch ports `InstallUpdateService.EnsureValidBranch`
// (`:177-194`). When the package's branch differs from the configured
// branch, the config is redirected to the package's branch (the feed
// can move a user between branches). A save failure is logged but not
// propagated — faithful to the C# try/catch which swallows the error.
func (i *Installer) EnsureValidBranch(ctx context.Context, pkg *Package) {
	if pkg == nil {
		return
	}
	current, err := i.cfg.Branch(ctx)
	if err != nil {
		i.logger.Warn("update: read current branch", slog.Any("err", err))
		return
	}
	// Faithful to C#:180 `if (package.Branch != currentBranch)` — redirect
	// on ANY difference (the feed can move a user between branches). No
	// empty-branch short-circuit; the package always carries its branch.
	if pkg.Branch == current {
		return
	}
	i.logger.Info("update: branch is being redirected",
		slog.String("from", current), slog.String("to", pkg.Branch))
	if err := i.cfg.SetBranch(ctx, pkg.Branch); err != nil {
		i.logger.Error("update: could not change branch",
			slog.String("from", current), slog.String("to", pkg.Branch), slog.Any("err", err))
	}
}
