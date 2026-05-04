package scheduledtasks

import (
	"context"
	"errors"
	"fmt"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
)

// SetLastExecutionTime ports
// `ScheduledTaskRepository.SetLastExecutionTime(int, DateTime, DateTime)`
// at `src/NzbDrone.Core/Jobs/ScheduledTaskRepository.cs:29-37`.
// Targeted UPDATE on (LastExecution, LastStartTime) only — the C#
// version uses `SetFields(t => t.LastExecution, t => t.LastStartTime)`.
//
// Both timestamps are forced to UTC. Returns ErrNotFound when no row
// matches the given Id.
func (r *Repo) SetLastExecutionTime(ctx context.Context, id int, executionTime, startTime time.Time) error {
	db := r.DB()
	q := fmt.Sprintf(
		`UPDATE "ScheduledTasks" SET "LastExecution" = %s, "LastStartTime" = %s WHERE "Id" = %s`,
		datastore.Placeholder(db.Engine, 1),
		datastore.Placeholder(db.Engine, 2),
		datastore.Placeholder(db.Engine, 3),
	)
	res, err := db.SQL.ExecContext(ctx, q, executionTime.UTC(), startTime.UTC(), id)
	if err != nil {
		return fmt.Errorf("scheduledtasks: SetLastExecutionTime(%d): %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("scheduledtasks: SetLastExecutionTime: rowsAffected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Upsert inserts a fresh row when no row with the given TypeName
// exists, or updates the existing row in-place when one does. Used
// by `Service.RegisterDefaults` to reconcile the host's task list
// against the persisted set on each boot — mirrors the C#
// `BasicRepository.Upsert(scheduledTask)` call from
// `TaskManager.cs:165` (`_scheduledTaskRepository.Upsert(currentDefinition)`).
//
// On insert, the assigned Id is written back to `t.ID`. On update,
// the existing row's Id is preserved and the persisted struct is
// rewritten with `t`'s field values. The C# Upsert is keyed by Id;
// this Go-port helper is keyed by the TypeName UNIQUE constraint
// because the only caller (RegisterDefaults) reconciles incoming
// defaults against the existing rows by name.
func (r *Repo) Upsert(ctx context.Context, t *Task) error {
	if t == nil {
		return errors.New("scheduledtasks: Upsert: nil task")
	}
	if t.TypeName == "" {
		return errors.New("scheduledtasks: Upsert: empty TypeName")
	}
	existing, err := r.GetByTypeName(ctx, t.TypeName)
	switch {
	case errors.Is(err, ErrNotFound):
		if _, err := r.Insert(ctx, t); err != nil {
			return fmt.Errorf("scheduledtasks: Upsert(%q): %w", t.TypeName, err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("scheduledtasks: Upsert(%q): %w", t.TypeName, err)
	}
	t.ID = existing.ID
	if err := r.Update(ctx, t); err != nil {
		return fmt.Errorf("scheduledtasks: Upsert(%q): %w", t.TypeName, err)
	}
	return nil
}
