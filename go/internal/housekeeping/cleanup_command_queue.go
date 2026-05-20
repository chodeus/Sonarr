// CleanupCommandQueue housekeeper. Ports the C# task at
// `src/NzbDrone.Core/Housekeeping/Housekeepers/CleanupCommandQueue.cs`.
//
// Trims the Commands table by removing rows whose EndedAt timestamp
// is older than 1 day — same retention policy as C#
// `CommandRepository.Trim()` at `:25-30`. The C# housekeeper
// delegates to `IManageCommandQueue.CleanCommands()` which itself
// wraps the repo's Trim + a 5-minute in-memory queue sweep; the Go
// port reaches directly through [commands.Service.CleanCommands]
// (which delegates to the repo's Trim) and passes the 1-day cutoff
// explicitly.
//
// # 5-minute in-memory queue sweep
//
// The C# CleanCommands at `CommandQueueManager.cs:235-246` ALSO
// purges in-memory queue entries with EndedAt > 5 minutes old. The
// Go port's commands.Service doesn't yet expose an in-memory queue
// surface (the Phase 3.10 dispatcher arc landed the persisted half
// only). When the in-memory dispatcher ports its purge surface,
// this housekeeper can route through it. For now: persistent
// retention only, matching the audit-log-side behaviour the C#
// repo's Trim handles independently of the in-memory cache.

package housekeeping

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// CommandQueueCleaner narrows [*commands.Service] to the one method
// the housekeeper reaches for (CleanCommands). Pulled out so tests
// can inject a stub without standing up the full service stack.
type CommandQueueCleaner interface {
	CleanCommands(ctx context.Context, cutoff time.Time) (int64, error)
}

// CleanupCommandQueueTTL is the 1-day cutoff pinned against C#
// `CommandRepository.cs:27` (`DateTime.UtcNow.AddDays(-1)`).
const CleanupCommandQueueTTL = 24 * time.Hour

// NewCleanupCommandQueue ports
// `Housekeepers/CleanupCommandQueue.cs:1-20`. Trims Commands rows
// older than 24h.
//
// cleaner is required (nil → error). Production binding:
// [*commands.Service] from the host's wiring. log defaults to
// slog.Default.
func NewCleanupCommandQueue(cleaner CommandQueueCleaner, log *slog.Logger) (Task, error) {
	if cleaner == nil {
		return nil, errors.New("housekeeping: NewCleanupCommandQueue: nil cleaner")
	}
	if log == nil {
		log = slog.Default()
	}
	return &cleanupCommandQueue{cleaner: cleaner, log: log, now: time.Now}, nil
}

type cleanupCommandQueue struct {
	cleaner CommandQueueCleaner
	log     *slog.Logger
	// now is the clock for cutoff computation. Wired by the
	// constructor to time.Now; tests step over the boundary by
	// pinning the value directly.
	now func() time.Time
}

// Name implements Task.
func (t *cleanupCommandQueue) Name() string {
	return "CleanupCommandQueue"
}

// Clean implements Task. Single call into the cleaner with the
// `now - 24h` cutoff.
func (t *cleanupCommandQueue) Clean(ctx context.Context) error {
	cutoff := t.now().UTC().Add(-CleanupCommandQueueTTL)
	n, err := t.cleaner.CleanCommands(ctx, cutoff)
	if err != nil {
		return fmt.Errorf("housekeeping: CleanupCommandQueue: %w", err)
	}
	if n > 0 {
		t.log.LogAttrs(ctx, slog.LevelInfo,
			"housekeeping: trimmed stale Commands rows",
			slog.Int64("rows", n))
	}
	return nil
}
