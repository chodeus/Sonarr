package episodes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
)

// SetMonitoredFlat ports `EpisodeRepository.SetMonitoredFlat(Episode, bool)`
// at `EpisodeRepository.cs:172-178`. Targeted single-column update — only
// the Monitored column is touched at the SQL layer (matches the C#
// `SetFields(episode, p => p.Monitored)` semantic).
//
// Mutates `e.Monitored` in place to match the new value. Returns
// ErrNotFound if no row was updated.
func (r *Repo) SetMonitoredFlat(ctx context.Context, e *Episode, monitored bool) error {
	if e == nil {
		return errors.New("episodes: SetMonitoredFlat: nil episode")
	}
	db := r.DB()
	q := fmt.Sprintf(
		`UPDATE "Episodes" SET "Monitored" = %s WHERE "Id" = %s`,
		datastore.Placeholder(db.Engine, 1),
		datastore.Placeholder(db.Engine, 2),
	)
	res, err := db.SQL.ExecContext(ctx, q, monitored, e.ID)
	if err != nil {
		return fmt.Errorf("episodes: SetMonitoredFlat(%d): %w", e.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("episodes: SetMonitoredFlat(%d): rowsAffected: %w", e.ID, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	e.Monitored = monitored
	return nil
}

// SetMonitoredBySeason ports
// `EpisodeRepository.SetMonitoredBySeason(int seriesId, int seasonNumber, bool monitored)`
// at `EpisodeRepository.cs:180-187`. Single bulk UPDATE; the WHERE
// includes a guard `"Monitored" != monitored` so the rowsAffected count
// reflects only actual changes — useful for the upstream
// SeriesService.UpdateSeries cascade where the EpisodesChanged flag is
// driven by whether anything actually toggled.
//
// Returns the number of rows affected. Does NOT return ErrNotFound on 0
// rows (the C# version doesn't throw either; "no episodes to flip" is a
// valid no-op).
func (r *Repo) SetMonitoredBySeason(ctx context.Context, seriesID, seasonNumber int, monitored bool) (int64, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`UPDATE "Episodes" SET "Monitored" = %s WHERE "SeriesId" = %s AND "SeasonNumber" = %s AND "Monitored" != %s`,
		datastore.Placeholder(db.Engine, 1),
		datastore.Placeholder(db.Engine, 2),
		datastore.Placeholder(db.Engine, 3),
		datastore.Placeholder(db.Engine, 4),
	)
	res, err := db.SQL.ExecContext(ctx, q, monitored, seriesID, seasonNumber, monitored)
	if err != nil {
		return 0, fmt.Errorf("episodes: SetMonitoredBySeason(series=%d,season=%d): %w", seriesID, seasonNumber, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("episodes: SetMonitoredBySeason: rowsAffected: %w", err)
	}
	return n, nil
}

// SetMonitoredByIDs ports
// `EpisodeRepository.SetMonitored(IEnumerable<int> ids, bool monitored)`
// at `:189-193`. The C# version constructs lightweight Episode rows and
// runs SetFields per-row; the Go port issues a single UPDATE ... WHERE
// "Id" IN (...) which is fewer round-trips and equivalent at the
// behavioural level (only the Monitored column is touched).
//
// Empty input returns 0 rows affected, no error.
func (r *Repo) SetMonitoredByIDs(ctx context.Context, ids []int, monitored bool) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	db := r.DB()
	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids)+1)
	args = append(args, monitored)
	for i, id := range ids {
		placeholders[i] = datastore.Placeholder(db.Engine, i+2)
		args = append(args, id)
	}
	q := fmt.Sprintf(
		`UPDATE "Episodes" SET "Monitored" = %s WHERE "Id" IN (%s)`,
		datastore.Placeholder(db.Engine, 1),
		strings.Join(placeholders, ","),
	)
	res, err := db.SQL.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("episodes: SetMonitoredByIDs: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("episodes: SetMonitoredByIDs: rowsAffected: %w", err)
	}
	return n, nil
}

// SetFileID ports `EpisodeRepository.SetFileId(Episode, int)` at
// `EpisodeRepository.cs:195-202`. Single-column update on EpisodeFileId
// + in-place mutation of the input episode pointer. Mirrors the C#
// flow: `episode.EpisodeFileId = fileId; SetFields(...);`.
func (r *Repo) SetFileID(ctx context.Context, e *Episode, fileID int) error {
	if e == nil {
		return errors.New("episodes: SetFileID: nil episode")
	}
	db := r.DB()
	q := fmt.Sprintf(
		`UPDATE "Episodes" SET "EpisodeFileId" = %s WHERE "Id" = %s`,
		datastore.Placeholder(db.Engine, 1),
		datastore.Placeholder(db.Engine, 2),
	)
	res, err := db.SQL.ExecContext(ctx, q, fileID, e.ID)
	if err != nil {
		return fmt.Errorf("episodes: SetFileID(%d,%d): %w", e.ID, fileID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("episodes: SetFileID: rowsAffected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	id := fileID
	e.EpisodeFileID = &id
	return nil
}

// ClearFileID ports `EpisodeRepository.ClearFileId(Episode, bool unmonitor)`
// at `:204-212`. Detaches the episode from its file (sets EpisodeFileId
// to NULL) and, when `unmonitor` is set, also flips Monitored to false.
// Mirrors the C# behaviour: when unmonitor is true the OR-equals
// pattern `episode.Monitored &= !unmonitor` clears Monitored; otherwise
// the existing Monitored value is preserved.
//
// Note: the C# version sets `episode.EpisodeFileId = 0` and persists
// that as 0 — but the column is nullable in the schema and 0 is a
// reserved "no file" sentinel. The Go port stores NULL instead so
// callers can distinguish "never had a file" (NULL on insert) from
// "explicitly cleared" (was non-zero, now NULL) without an extra column.
// Behavioural impact: `EpisodeFileId IS NULL` and `EpisodeFileId = 0`
// are both consumed identically by every downstream query (none of the
// existing helpers compare against 0; FK lookups all use IS NOT NULL or
// JOIN-based filtering).
func (r *Repo) ClearFileID(ctx context.Context, e *Episode, unmonitor bool) error {
	if e == nil {
		return errors.New("episodes: ClearFileID: nil episode")
	}
	db := r.DB()
	var (
		q    string
		args []any
	)
	if unmonitor {
		q = fmt.Sprintf(
			`UPDATE "Episodes" SET "EpisodeFileId" = NULL, "Monitored" = %s WHERE "Id" = %s`,
			datastore.Placeholder(db.Engine, 1),
			datastore.Placeholder(db.Engine, 2),
		)
		args = []any{false, e.ID}
	} else {
		q = fmt.Sprintf(
			`UPDATE "Episodes" SET "EpisodeFileId" = NULL WHERE "Id" = %s`,
			datastore.Placeholder(db.Engine, 1),
		)
		args = []any{e.ID}
	}
	res, err := db.SQL.ExecContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("episodes: ClearFileID(%d): %w", e.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("episodes: ClearFileID: rowsAffected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	e.EpisodeFileID = nil
	if unmonitor {
		e.Monitored = false
	}
	return nil
}

// UpdateLastSearchTime ports `EpisodeRepository.SetFields(episodes, e => e.LastSearchTime)`
// — the bulk last-search-time persist used by `EpisodeService.UpdateLastSearchTime`
// at `EpisodeService.cs:199-202`. Per-row UPDATE in a loop; not a single
// transaction (the C# `SetFields` batches at the Dapper layer, but the
// observable behaviour is row-by-row updates).
//
// Returns the total number of rows affected. Empty input is a no-op.
func (r *Repo) UpdateLastSearchTime(ctx context.Context, list []Episode) (int64, error) {
	if len(list) == 0 {
		return 0, nil
	}
	db := r.DB()
	q := fmt.Sprintf(
		`UPDATE "Episodes" SET "LastSearchTime" = %s WHERE "Id" = %s`,
		datastore.Placeholder(db.Engine, 1),
		datastore.Placeholder(db.Engine, 2),
	)
	var total int64
	for i := range list {
		var arg any
		if list[i].LastSearchTime != nil {
			arg = list[i].LastSearchTime.UTC()
		} else {
			arg = nil
		}
		res, err := db.SQL.ExecContext(ctx, q, arg, list[i].ID)
		if err != nil {
			return total, fmt.Errorf("episodes: UpdateLastSearchTime(id=%d): %w", list[i].ID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, fmt.Errorf("episodes: UpdateLastSearchTime: rowsAffected: %w", err)
		}
		total += n
	}
	return total, nil
}

// InsertMany ports `EpisodeRepository.InsertMany(...)` invoked by
// `EpisodeService.InsertMany(List<Episode>)` at `EpisodeService.cs:211-214`.
// Per-row Insert in a loop — the generic Repository[T] doesn't expose a
// transactional bulk-insert helper yet (same approximation as series.AddMany).
//
// On error mid-loop the already-inserted rows remain. The C# version
// also lacks transactional rollback for `InsertMany` at this layer.
func (r *Repo) InsertMany(ctx context.Context, list []*Episode) error {
	if len(list) == 0 {
		return nil
	}
	for i, e := range list {
		if e == nil {
			return fmt.Errorf("episodes: InsertMany: nil episode at idx %d", i)
		}
		if _, err := r.Insert(ctx, e); err != nil {
			return fmt.Errorf("episodes: InsertMany(idx=%d): %w", i, err)
		}
	}
	return nil
}

// UpdateMany ports `EpisodeRepository.UpdateMany(...)` invoked by
// `EpisodeService.UpdateMany(List<Episode>)` at `:216-219`. Per-row
// Update in a loop. Idempotent ErrNotFound errors propagate.
func (r *Repo) UpdateMany(ctx context.Context, list []*Episode) error {
	if len(list) == 0 {
		return nil
	}
	for i, e := range list {
		if e == nil {
			return fmt.Errorf("episodes: UpdateMany: nil episode at idx %d", i)
		}
		if err := r.Update(ctx, e); err != nil {
			return fmt.Errorf("episodes: UpdateMany(idx=%d,id=%d): %w", i, e.ID, err)
		}
	}
	return nil
}

// DeleteMany ports `EpisodeRepository.DeleteMany(...)` invoked by
// `EpisodeService.DeleteMany(List<Episode>)` at `:221-224`. Per-row
// Delete in a loop. Tolerates ErrNotFound (idempotent) so the bulk
// path doesn't fail if an upstream listener already removed a row.
func (r *Repo) DeleteMany(ctx context.Context, list []Episode) error {
	if len(list) == 0 {
		return nil
	}
	for i, e := range list {
		if err := r.Delete(ctx, e.ID); err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return fmt.Errorf("episodes: DeleteMany(idx=%d,id=%d): %w", i, e.ID, err)
		}
	}
	return nil
}

// nowUTC returns the current UTC time. Indirected through a package
// var so tests can pin it deterministically — mirrors the
// providerstatus.SetClock pattern used elsewhere in the port. Currently
// unused outside tests (UpdateLastSearchTime takes a time.Time per row
// from the caller) but kept for parity with the wider service layer.
//
//nolint:unused // reserved for time-mocked tests in the service slice
var nowUTC = func() time.Time { return time.Now().UTC() }
