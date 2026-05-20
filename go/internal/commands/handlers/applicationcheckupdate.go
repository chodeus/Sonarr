// ApplicationCheckUpdate handler. Ports the
// IExecute<ApplicationUpdateCheckCommand> implementation at
// `src/NzbDrone.Core/Update/InstallUpdateService.cs:279-285`.
//
// In C# the command pushes an ApplicationUpdateCommand (the install
// trigger) when AvailableUpdate returns non-nil. The Go port stops
// at the check — the install pipeline isn't ported yet. When an
// update is available the handler logs at Info level; the operator
// sees the available-update banner via the UpdateCheck healthcheck
// (slice 5.J35 + this slice's banner-fetcher wiring) rather than an
// auto-install.
//
// Audit fix B.3 close-out + closes the deferred update-availability
// banner arm from 5.J35.

package handlers

import (
	"context"
	"log/slog"
)

// ApplicationCheckUpdateName is the registry key + Commands.Name +
// ScheduledTasks.TypeName. Mirrors the C# short form
// `ApplicationUpdateCheckCommand` → `ApplicationUpdateCheck`.
const ApplicationCheckUpdateName = "ApplicationUpdateCheck"

// ApplicationCheckUpdateIntervalDefault is the 6-hour scheduler
// cadence. Matches C# `TaskManager.cs:127` (interval = 360 min, the
// fallback when no per-instance ScheduledTasks row exists).
const ApplicationCheckUpdateIntervalDefault = 6 * 60

// UpdatePackageInfo is the narrow projection
// ApplicationCheckUpdate consumes from the upstream services.sonarr.tv
// response. `*update.Package` satisfies this via Go's structural
// typing; the handler only reads Version (the upstream-fetcher
// projection in internal/update carries more — Branch / URL / Hash /
// ReleaseDate — that the install-side will use later).
type UpdatePackageInfo interface {
	GetVersion() string
}

// UpdateChecker is the narrow lookup the handler depends on. The
// production binding is a small adapter around
// `*update.Service.AvailableUpdate` that returns the package's
// Version string only — the handler doesn't need the rest of the
// projection.
type UpdateChecker interface {
	AvailableVersion(ctx context.Context) (string, error)
}

// ApplicationCheckUpdate is the IExecute<ApplicationUpdateCheckCommand>
// port. Logs the discovery result; doesn't push the install command
// (Go port's install pipeline isn't ported — see file doc-comment).
type ApplicationCheckUpdate struct {
	checker UpdateChecker
	log     *slog.Logger
}

// NewApplicationCheckUpdate builds the handler. checker may be nil
// — the handler falls back to the stub-log behaviour so hosts
// without a wired update.Service still satisfy the registry slot.
// log defaults to slog.Default.
func NewApplicationCheckUpdate(checker UpdateChecker, log *slog.Logger) *ApplicationCheckUpdate {
	if log == nil {
		log = slog.Default()
	}
	return &ApplicationCheckUpdate{checker: checker, log: log}
}

// Execute fetches the available-update package + logs the outcome.
// Network failures absorbed (warn + return nil) so the scheduler /
// API doesn't classify the command as failed; a transient outage of
// services.sonarr.tv shouldn't surface as a failed-command in the
// UI.
func (h *ApplicationCheckUpdate) Execute(ctx context.Context, _ string) error {
	if h.checker == nil {
		h.log.LogAttrs(ctx, slog.LevelInfo,
			"applicationcheckupdate: stub Execute (no UpdateChecker wired)",
			slog.String("command", ApplicationCheckUpdateName))
		return nil
	}
	version, err := h.checker.AvailableVersion(ctx)
	if err != nil {
		h.log.LogAttrs(ctx, slog.LevelWarn,
			"applicationcheckupdate: feed lookup failed (absorbed)",
			slog.String("command", ApplicationCheckUpdateName),
			slog.Any("err", err))
		return nil
	}
	if version == "" {
		h.log.LogAttrs(ctx, slog.LevelInfo,
			"applicationcheckupdate: no update available",
			slog.String("command", ApplicationCheckUpdateName))
		return nil
	}
	h.log.LogAttrs(ctx, slog.LevelInfo,
		"applicationcheckupdate: update available",
		slog.String("command", ApplicationCheckUpdateName),
		slog.String("version", version))
	return nil
}
