// TrimLogDatabase housekeeper. Ports the C# task at
// `src/NzbDrone.Core/Housekeeping/Housekeepers/TrimLogDatabase.cs`.
//
// Deletes Logs rows older than 7 days — the retention policy C#
// `LogRepository.Trim()` enforces (`Delete(c => c.Time <= now-7d)` at
// `Instrumentation/LogRepository.cs:19-24`). Without this housekeeper the
// log database grows unbounded on long-running instances.
//
// # LogDbEnabled gate
//
// The C# task gates on `_configFileProvider.LogDbEnabled` and no-ops when
// the log DB is disabled. In the Go port the log repo is nil exactly when
// the log DB is disabled (main.go builds `logRepo` only inside the
// log-DB-enabled branch), so the host registers this housekeeper ONLY
// when the log DB is enabled — the gate lives at the registration site,
// which is behaviourally identical (no trim runs when disabled).
//
// # Vacuum
//
// C# `LogRepository.Trim()` also calls `Vacuum()` to reclaim the freed
// pages. The Go logs repo exposes no Vacuum surface yet; the row deletion
// (the user-visible retention behaviour) is ported here, and a Vacuum can
// be added if/when the logs repo grows that method. Not a correctness gap
// — only on-disk space reclamation.

package housekeeping

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// LogTrimmer narrows the logs repo to the one method TrimLogDatabase
// reaches for. *logs.Repo satisfies it via Trim. Pulled out so tests can
// inject a stub without standing up the log datastore.
type LogTrimmer interface {
	Trim(ctx context.Context, now time.Time) (int64, error)
}

// NewTrimLogDatabase ports `Housekeepers/TrimLogDatabase.cs:1-25`.
// trimmer is required (nil → error). Production binding: the host's
// *logs.Repo (only when the log DB is enabled). log defaults to
// slog.Default.
func NewTrimLogDatabase(trimmer LogTrimmer, log *slog.Logger) (Task, error) {
	if trimmer == nil {
		return nil, errors.New("housekeeping: NewTrimLogDatabase: nil trimmer")
	}
	if log == nil {
		log = slog.Default()
	}
	return &trimLogDatabase{trimmer: trimmer, log: log, now: time.Now}, nil
}

type trimLogDatabase struct {
	trimmer LogTrimmer
	log     *slog.Logger
	// now is the clock for the cutoff; the trimmer computes now-7d. Wired
	// to time.Now; tests pin it.
	now func() time.Time
}

// Name implements Task.
func (t *trimLogDatabase) Name() string { return "TrimLogDatabase" }

// Clean implements Task. Single call into the trimmer (which deletes
// rows older than 7 days from `now`).
func (t *trimLogDatabase) Clean(ctx context.Context) error {
	n, err := t.trimmer.Trim(ctx, t.now().UTC())
	if err != nil {
		return fmt.Errorf("housekeeping: TrimLogDatabase: %w", err)
	}
	if n > 0 {
		t.log.LogAttrs(ctx, slog.LevelInfo,
			"housekeeping: trimmed old Logs rows",
			slog.Int64("rows", n))
	}
	return nil
}
