package notifications

import (
	"context"
	"fmt"
	"log/slog"

	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/notificationstatus"
)

// Factory ports the C# `NotificationFactory` at
// `src/NzbDrone.Core/Notifications/NotificationFactory.cs:28-209`.
//
// It bridges the persisted [Notification] rows (loaded via the
// [Repo]), the [Registry] of concrete provider constructors, and
// the [notificationstatus.Service] (for back-off filtering) into
// the per-event "list of providers I should fan out to" the
// fan-out [Service] in slice 4.AH consumes.
//
// Slice 4.AG (sub-arc N4a of queue [N4]).
//
// Concurrency: Factory itself is stateless; each per-event
// accessor reads through the underlying Repo + StatusService.
// Safe for concurrent use by multiple event handlers.
//
// The C# Factory derives from `ProviderFactory<INotification,
// NotificationDefinition>` which adds caching + Sync() machinery
// (provider definitions reload on config change). The Go port
// defers caching — each event-bus dispatch in slice 4.AH reads
// through the Repo. That's acceptable because the Notifications
// table is tiny (~10s of rows max) and the fan-out is itself
// rate-limited by the event-bus throughput; the cache layer is
// follow-up work that can land if profiling shows it's needed.
type Factory struct {
	repo     factoryRepo
	registry *Registry
	status   factoryStatus
	deps     Dependencies
	logger   *slog.Logger
}

// factoryRepo is the narrow interface the Factory needs from the
// notifications.Repo. Used so tests can swap a stub without
// pulling the SQL Repo through. The real *Repo already satisfies
// this surface via the underlying repo.Repository[Notification]
// (which exposes All(ctx) returning the list of rows).
type factoryRepo interface {
	All(ctx context.Context) ([]Notification, error)
}

// factoryStatus is the narrow interface the Factory needs from
// *notificationstatus.Service. Same swap-for-tests rationale as
// factoryRepo. The real Service already exposes GetBlockedProviders.
type factoryStatus interface {
	GetBlockedProviders(ctx context.Context) ([]notificationstatus.Status, error)
}

// NewFactory binds the Factory's deps. logger may be nil; nil
// substitutes [slog.Default]. status may be nil — when nil, no
// blocked-filter is applied (every enabled provider is returned
// regardless of recent failures).
func NewFactory(repo factoryRepo, registry *Registry, status factoryStatus, deps Dependencies, logger *slog.Logger) (*Factory, error) {
	if repo == nil {
		return nil, fmt.Errorf("notifications: NewFactory: nil repo")
	}
	if registry == nil {
		return nil, fmt.Errorf("notifications: NewFactory: nil registry")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Factory{
		repo:     repo,
		registry: registry,
		status:   status,
		deps:     deps,
		logger:   logger,
	}, nil
}

// enabled is the shared accessor: pulls every Notification, keeps
// only those matching `predicate` AND that aren't currently
// blocked (when filterBlocked is true), constructs the Provider
// via [Registry.Construct], and returns the resulting slice. Rows
// whose Constructor returns an error are skipped with a logged
// warning — mirrors C# behaviour where the DI container's
// resolution failure becomes a logged exception at fan-out time.
//
// Mirrors the C# `NotificationFactory.OnXEnabled` accessors at
// `NotificationFactory.cs:45-173` — each accessor delegates to a
// shared `GetAvailableProviders().Where(predicate)` chain via the
// base ProviderFactory class.
func (f *Factory) enabled(ctx context.Context, predicate func(*Notification) bool, filterBlocked bool) ([]Provider, error) {
	rows, err := f.repo.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("notifications: factory: load: %w", err)
	}

	blocked := map[int]bool{}
	if filterBlocked && f.status != nil {
		statuses, err := f.status.GetBlockedProviders(ctx)
		if err != nil {
			return nil, fmt.Errorf("notifications: factory: blocked: %w", err)
		}
		for _, s := range statuses {
			blocked[s.ProviderID] = true
		}
	}

	out := make([]Provider, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		if !predicate(row) {
			continue
		}
		if blocked[row.ID] {
			f.logger.Debug("notifications: skipping blocked provider",
				slog.Int("provider_id", row.ID),
				slog.String("provider_name", row.Name))
			continue
		}
		p, err := f.registry.Construct(row, f.deps)
		if err != nil {
			f.logger.Warn("notifications: factory: construct failed",
				slog.Int("provider_id", row.ID),
				slog.String("provider_name", row.Name),
				slog.String("implementation", row.Implementation),
				slog.Any("err", err))
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// OnGrabEnabled returns providers where Notifications.OnGrab is
// true and (when filterBlocked) not currently in back-off. Mirrors
// C# `OnGrabEnabled` at `NotificationFactory.cs:45-53`.
func (f *Factory) OnGrabEnabled(ctx context.Context, filterBlocked bool) ([]Provider, error) {
	return f.enabled(ctx, func(n *Notification) bool { return n.OnGrab }, filterBlocked)
}

// OnDownloadEnabled mirrors C# `OnDownloadEnabled` at `:55-63`.
func (f *Factory) OnDownloadEnabled(ctx context.Context, filterBlocked bool) ([]Provider, error) {
	return f.enabled(ctx, func(n *Notification) bool { return n.OnDownload }, filterBlocked)
}

// OnUpgradeEnabled mirrors C# `OnUpgradeEnabled` at `:65-73`. The
// Go port reads the nullable *bool with default-false semantics
// (matches C# Where(c => ((NotificationDefinition)c.Definition).OnUpgrade)
// — when the JSON column is null, the C# bool defaults to false).
func (f *Factory) OnUpgradeEnabled(ctx context.Context, filterBlocked bool) ([]Provider, error) {
	return f.enabled(ctx, func(n *Notification) bool { return n.OnUpgrade != nil && *n.OnUpgrade }, filterBlocked)
}

// OnImportCompleteEnabled mirrors C# `OnImportCompleteEnabled` at `:75-83`.
func (f *Factory) OnImportCompleteEnabled(ctx context.Context, filterBlocked bool) ([]Provider, error) {
	return f.enabled(ctx, func(n *Notification) bool { return n.OnImportComplete }, filterBlocked)
}

// OnRenameEnabled mirrors C# `OnRenameEnabled` at `:85-93`.
func (f *Factory) OnRenameEnabled(ctx context.Context, filterBlocked bool) ([]Provider, error) {
	return f.enabled(ctx, func(n *Notification) bool { return n.OnRename }, filterBlocked)
}

// OnSeriesAddEnabled mirrors C# `OnSeriesAddEnabled` at `:95-103`.
func (f *Factory) OnSeriesAddEnabled(ctx context.Context, filterBlocked bool) ([]Provider, error) {
	return f.enabled(ctx, func(n *Notification) bool { return n.OnSeriesAdd }, filterBlocked)
}

// OnSeriesDeleteEnabled mirrors C# `OnSeriesDeleteEnabled` at `:105-113`.
func (f *Factory) OnSeriesDeleteEnabled(ctx context.Context, filterBlocked bool) ([]Provider, error) {
	return f.enabled(ctx, func(n *Notification) bool { return n.OnSeriesDelete }, filterBlocked)
}

// OnEpisodeFileDeleteEnabled mirrors C# `OnEpisodeFileDeleteEnabled` at `:115-123`.
func (f *Factory) OnEpisodeFileDeleteEnabled(ctx context.Context, filterBlocked bool) ([]Provider, error) {
	return f.enabled(ctx, func(n *Notification) bool { return n.OnEpisodeFileDelete }, filterBlocked)
}

// OnEpisodeFileDeleteForUpgradeEnabled mirrors C# at `:125-133`.
func (f *Factory) OnEpisodeFileDeleteForUpgradeEnabled(ctx context.Context, filterBlocked bool) ([]Provider, error) {
	return f.enabled(ctx, func(n *Notification) bool { return n.OnEpisodeFileDeleteForUpgrade }, filterBlocked)
}

// OnHealthIssueEnabled mirrors C# `OnHealthIssueEnabled` at `:135-143`.
func (f *Factory) OnHealthIssueEnabled(ctx context.Context, filterBlocked bool) ([]Provider, error) {
	return f.enabled(ctx, func(n *Notification) bool { return n.OnHealthIssue }, filterBlocked)
}

// OnHealthRestoredEnabled mirrors C# `OnHealthRestoredEnabled` at `:145-153`.
func (f *Factory) OnHealthRestoredEnabled(ctx context.Context, filterBlocked bool) ([]Provider, error) {
	return f.enabled(ctx, func(n *Notification) bool { return n.OnHealthRestored }, filterBlocked)
}

// OnApplicationUpdateEnabled mirrors C# `OnApplicationUpdateEnabled` at `:155-163`.
func (f *Factory) OnApplicationUpdateEnabled(ctx context.Context, filterBlocked bool) ([]Provider, error) {
	return f.enabled(ctx, func(n *Notification) bool { return n.OnApplicationUpdate }, filterBlocked)
}

// OnManualInteractionEnabled mirrors C# `OnManualInteractionEnabled` at `:165-173`.
func (f *Factory) OnManualInteractionEnabled(ctx context.Context, filterBlocked bool) ([]Provider, error) {
	return f.enabled(ctx, func(n *Notification) bool { return n.OnManualInteractionRequired }, filterBlocked)
}

// Available returns every configured provider, regardless of its OnX
// flags. Mirrors C# `ProviderFactory.GetAvailableProviders`
// (`ProviderFactory.cs:89-92` = `Active().Select(GetInstance)`) — the
// source the notification ProcessQueue drain iterates. The Go port has
// no per-provider enable/disable flag, so every configured row is
// "available".
func (f *Factory) Available(ctx context.Context, filterBlocked bool) ([]Provider, error) {
	return f.enabled(ctx, func(*Notification) bool { return true }, filterBlocked)
}

// ShouldHandleSeries ports the C# helper at
// `NotificationService.cs:97-113`. Returns true when the provider
// definition has NO tags configured (no per-series filter), OR
// when the definition's tags intersect the series' tags.
//
// Mirrors the C# behaviour exactly: empty notification tags
// short-circuits to true regardless of the series-tags state
// (including the rare empty-series-tags case which an early-return
// at C# `:99-103` treats as "match anything").
func ShouldHandleSeries(notificationTags, seriesTags []int) bool {
	if len(notificationTags) == 0 {
		return true
	}
	have := make(map[int]struct{}, len(seriesTags))
	for _, t := range seriesTags {
		have[t] = struct{}{}
	}
	for _, t := range notificationTags {
		if _, ok := have[t]; ok {
			return true
		}
	}
	return false
}

// ShouldHandleHealthFailure ports the C# helper at
// `NotificationService.cs:115-128`. Returns true on Error always,
// true on Warning when the provider's IncludeHealthWarnings is on,
// false otherwise (Notice / Ok never fire OnHealthIssue).
//
// Note the C# enum uses `HealthCheckResult.Error` / `Warning` /
// `Notice` / `Ok`; the Go port's healthcheck.Result mirrors the
// same set (ResultError / ResultWarning / ResultNotice / ResultOK).
func ShouldHandleHealthFailure(t healthcheck.Result, includeWarnings bool) bool {
	switch t {
	case healthcheck.ResultError:
		return true
	case healthcheck.ResultWarning:
		return includeWarnings
	default:
		return false
	}
}
