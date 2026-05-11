package importlists

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"sonarr.tv/sonarr/internal/importliststatus"
)

// Factory ports the C# `ImportListFactory` at
// `src/NzbDrone.Core/ImportLists/ImportListFactory.cs:16-94`.
//
// It bridges the persisted [ImportList] rows (loaded via the
// [Repo]), the [Registry] of concrete provider constructors, and
// the [importliststatus.Service] (for back-off filtering) into
// the per-poll "list of providers I should fan out to" the
// sync orchestrator (queue [I4] sub-arc I4b) consumes.
//
// Slice 4.AO (sub-arc I4a of queue [I4]) — pattern-mirror of
// notifications.Factory at `internal/notifications/factory.go`.
//
// Concurrency: Factory itself is stateless; each per-event accessor
// reads through the underlying Repo + StatusService. Safe for
// concurrent use by multiple sync workers — the orchestrator's
// per-list worker pool may call AutomaticAddEnabled concurrently
// with Test against the same Factory instance.
//
// The C# Factory derives from `ProviderFactory<IImportList,
// ImportListDefinition>` which adds caching + Sync() machinery
// (provider definitions reload on config change). The Go port
// defers caching — each scheduled poll reads through the Repo.
// That's acceptable because the ImportLists table is tiny (~10s
// of rows max) and the orchestrator's MinRefreshInterval gates
// each fan-out at 5min+; the cache layer is follow-up work that
// can land if profiling shows it's needed. Same rationale as
// notifications.Factory.
type Factory struct {
	repo     factoryRepo
	registry *Registry
	status   factoryStatus
	deps     Dependencies
	logger   *slog.Logger
	now      func() time.Time
}

// factoryRepo is the narrow interface the Factory needs from the
// importlists.Repo. Used so tests can swap a stub without pulling
// the SQL Repo through. The real *Repo already satisfies this
// surface via the underlying repo.Repository[ImportList] (which
// exposes All(ctx) returning the list of rows).
type factoryRepo interface {
	All(ctx context.Context) ([]ImportList, error)
}

// factoryStatus is the narrow interface the Factory needs from
// *importliststatus.Service. Same swap-for-tests rationale as
// factoryRepo. The real Service already exposes
// GetBlockedProviders + RecordSuccess + RecordFailure.
type factoryStatus interface {
	GetBlockedProviders(ctx context.Context) ([]importliststatus.Status, error)
	RecordSuccess(ctx context.Context, providerID int) error
	RecordFailure(ctx context.Context, providerID int, minimumBackOff time.Duration) error
}

// NewFactory binds the Factory's deps. logger may be nil; nil
// substitutes [slog.Default]. status may be nil — when nil, no
// blocked-filter is applied (every enabled provider is returned
// regardless of recent failures) AND Test silently skips the
// per-row RecordSuccess / RecordFailure call.
func NewFactory(repo factoryRepo, registry *Registry, status factoryStatus, deps Dependencies, logger *slog.Logger) (*Factory, error) {
	if repo == nil {
		return nil, errors.New("importlists: NewFactory: nil repo")
	}
	if registry == nil {
		return nil, errors.New("importlists: NewFactory: nil registry")
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
		now:      func() time.Time { return time.Now().UTC() },
	}, nil
}

// SetClock overrides the now-function for deterministic tests.
// Used by tests that need to assert log-line content involving
// the per-blocked DisabledTill timestamp.
func (f *Factory) SetClock(now func() time.Time) { f.now = now }

// AutomaticAddEnabled ports C# `AutomaticAddEnabled` at
// `ImportListFactory.cs:46-56`. Returns the constructed Provider
// list for every persisted row where EnableAutomaticAdd is true
// AND (when filterBlocked is true) the per-row provider isn't
// currently inside a status-service back-off window.
//
// Predicate semantics for EnableAutomaticAdd: the column is
// nullable in the schema (`*bool`). Nil = false (legacy v1-
// migration rows where the column didn't exist default to
// disabled). The Go port mirrors the C# default-false semantics
// for null EnableAutomaticAdd at `ImportListDefinition.cs:12`.
//
// Rows whose Constructor returns an error are skipped with a
// logged warning — mirrors the C# `IServiceProvider` resolution
// behaviour where a DI-resolution failure becomes a logged
// exception at fan-out time rather than aborting the loop.
func (f *Factory) AutomaticAddEnabled(ctx context.Context, filterBlocked bool) ([]Provider, error) {
	return f.enabled(ctx, func(row *ImportList) bool {
		return row.EnableAutomaticAdd != nil && *row.EnableAutomaticAdd
	}, filterBlocked)
}

// All returns the constructed Provider list for every persisted
// row regardless of EnableAutomaticAdd. Useful for v3 API endpoints
// that surface the catalog (list every configured import-list, not
// just the auto-add-enabled subset).
//
// Mirrors the C# `ProviderFactory<IImportList>.GetAvailableProviders`
// + `All()` accessors the base class exposes.
func (f *Factory) All(ctx context.Context) ([]Provider, error) {
	return f.enabled(ctx, func(*ImportList) bool { return true }, false)
}

// Test ports C# `ImportListFactory.Test` at
// `ImportListFactory.cs:74-93`. Constructs the Provider for the
// given row, invokes the provider's Test, and records the outcome
// to the status service when Id != 0:
//
//   - Id == 0 (creating a NEW row not yet persisted) → just runs
//     Test and returns the failures; does NOT record.
//   - Id != 0 + no failures → RecordSuccess.
//   - Id != 0 + failures → RecordFailure.
//
// The Id-zero gate matches the C# `if (definition.Id == 0)` early-
// return at `:78-81` — when the React UI is testing an unsaved
// row, there's no Id to record against.
//
// When status is nil, the record step is silently skipped (same
// posture as AutomaticAddEnabled). Failure recording uses zero
// minimumBackOff (matches C# `RecordFailure(id)` overload that
// defaults TimeSpan to zero).
func (f *Factory) Test(ctx context.Context, row *ImportList) ([]TestFailure, error) {
	if row == nil {
		return nil, errors.New("importlists: Test: nil row")
	}
	provider, err := f.registry.Construct(row, f.deps)
	if err != nil {
		return nil, fmt.Errorf("importlists: Test: construct: %w", err)
	}
	failures, err := provider.Test(ctx)
	if err != nil {
		return nil, fmt.Errorf("importlists: Test: provider: %w", err)
	}
	if row.ID != 0 && f.status != nil {
		if len(failures) == 0 {
			_ = f.status.RecordSuccess(ctx, row.ID)
		} else {
			_ = f.status.RecordFailure(ctx, row.ID, 0)
		}
	}
	return failures, nil
}

// enabled is the shared accessor: pulls every ImportList, keeps
// only those matching `predicate` AND (when filterBlocked is true)
// that aren't currently blocked, constructs the Provider via
// [Registry.Construct], and returns the resulting slice.
//
// Rows whose Constructor returns an error are skipped with a
// logged warning.
//
// Mirrors the C# `ProviderFactory.GetAvailableProviders`
// .Where(predicate) chain via the base ProviderFactory class.
func (f *Factory) enabled(ctx context.Context, predicate func(*ImportList) bool, filterBlocked bool) ([]Provider, error) {
	rows, err := f.repo.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("importlists: factory: load: %w", err)
	}

	blocked := map[int]*importliststatus.Status{}
	if filterBlocked && f.status != nil {
		statuses, err := f.status.GetBlockedProviders(ctx)
		if err != nil {
			return nil, fmt.Errorf("importlists: factory: blocked: %w", err)
		}
		for i := range statuses {
			s := statuses[i]
			blocked[s.ProviderID] = &s
		}
	}

	out := make([]Provider, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		if !predicate(row) {
			continue
		}
		if st, isBlocked := blocked[row.ID]; isBlocked {
			disabledTill := time.Time{}
			if st.DisabledTill != nil {
				disabledTill = st.DisabledTill.Local()
			}
			f.logger.Debug("importlists: temporarily ignoring blocked provider",
				slog.Int("provider_id", row.ID),
				slog.String("provider_name", row.Name),
				slog.Time("disabled_till", disabledTill))
			continue
		}
		p, err := f.registry.Construct(row, f.deps)
		if err != nil {
			f.logger.Warn("importlists: factory: construct failed",
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

// ShouldHandleByTags reports whether a sync run should dispatch the
// given series to this import-list's downstream side-effects (tag
// application, root-folder selection). Returns true when the
// import-list has NO tags configured (no per-series filter), OR
// when the list's tags intersect the series' tags.
//
// Mirrors the C# helper pattern at
// `notifications.ShouldHandleSeries` (notifications uses the same
// shape via NotificationDefinition.Tags). Lifted to a free helper
// rather than a Factory method since the orchestrator calls it
// per-series — no Factory state is needed.
//
// Note: at the time this helper lands (slice 4.AO), the import-
// list orchestrator (sub-arc I4b) is not yet wired. The helper
// is shipped here so sub-arc I4b can consume it without further
// refactor; the predicate matches the same C# call-site at
// `ImportListSyncService.cs` that gates per-list cleanup.
func ShouldHandleByTags(listTags, seriesTags []int) bool {
	if len(listTags) == 0 {
		return true
	}
	have := make(map[int]struct{}, len(seriesTags))
	for _, t := range seriesTags {
		have[t] = struct{}{}
	}
	for _, t := range listTags {
		if _, ok := have[t]; ok {
			return true
		}
	}
	return false
}
