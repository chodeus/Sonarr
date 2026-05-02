// Package scheduledtasks is the Go port of NzbDrone.Core.Jobs.ScheduledTask
// persistence. The ScheduledTasks table records when each periodically-run
// command last fired and (since migration 081) when its current execution
// started. The C# scheduler reads these rows on startup to decide whether
// each task is due and writes them on every dispatch.
//
// Schema at HEAD (v230): Id, TypeName UNIQUE, Interval, LastExecution NOT
// NULL, LastStartTime nullable.
//
// Notes:
//
//   - Priority is on the C# ScheduledTask class but is NOT persisted — it
//     defaults to CommandPriority.Low in the constructor and is recomputed
//     per-dispatch.
//   - Time columns are scalar DATETIME (SQLite) / timestamp with time zone
//     (Postgres) and round-trip through database/sql as time.Time directly.
//     LastStartTime is *time.Time for the nullable column; the Mapper
//     converts to/from sql.NullTime at the boundary and normalises
//     everything to UTC on both write and read.
package scheduledtasks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/repo"
)

// Task is one ScheduledTasks row.
type Task struct {
	ID            int        `json:"id"`
	TypeName      string     `json:"typeName"`
	Interval      int        `json:"interval"`
	LastExecution time.Time  `json:"lastExecution"`
	LastStartTime *time.Time `json:"lastStartTime,omitempty"`
}

// ErrNotFound is returned when no row matches.
var ErrNotFound = errors.New("scheduledtasks: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	*repo.Repository[Task]
}

var mapper = repo.Mapper[Task]{
	Name:    "scheduledtasks",
	Table:   "ScheduledTasks",
	Columns: []string{"TypeName", "Interval", "LastExecution", "LastStartTime"},
	OrderBy: "Id",
	Bind: func(t *Task) []any {
		return []any{t.TypeName, t.Interval, t.LastExecution.UTC(), nullableTime(t.LastStartTime)}
	},
	Scan: func(t *Task, s repo.RowScanner) error {
		var lastStart sql.NullTime
		if err := s.Scan(&t.ID, &t.TypeName, &t.Interval, &t.LastExecution, &lastStart); err != nil {
			return err
		}
		t.LastExecution = t.LastExecution.UTC()
		if lastStart.Valid {
			v := lastStart.Time.UTC()
			t.LastStartTime = &v
		}
		return nil
	},
	GetID: func(t *Task) int { return t.ID },
	SetID: func(t *Task, id int) { t.ID = id },
}

// NewRepo returns a Repo bound to the given DB.
func NewRepo(db *datastore.DB) (*Repo, error) {
	r, err := repo.New(db, mapper, ErrNotFound)
	if err != nil {
		return nil, err
	}
	return &Repo{Repository: r}, nil
}

// GetByTypeName returns the Task with the given TypeName or ErrNotFound.
// TypeName is the unique scheduler key (the C# Type.FullName of the
// ScheduledTask subclass, persisted as a string).
func (r *Repo) GetByTypeName(ctx context.Context, typeName string) (*Task, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT "Id","TypeName","Interval","LastExecution","LastStartTime" FROM "ScheduledTasks" WHERE "TypeName" = %s`,
		datastore.Placeholder(db.Engine, 1))
	var t Task
	var lastStart sql.NullTime
	switch err := db.SQL.QueryRowContext(ctx, q, typeName).Scan(
		&t.ID, &t.TypeName, &t.Interval, &t.LastExecution, &lastStart,
	); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("scheduledtasks: getByTypeName(%q): %w", typeName, err)
	}
	t.LastExecution = t.LastExecution.UTC()
	if lastStart.Valid {
		v := lastStart.Time.UTC()
		t.LastStartTime = &v
	}
	return &t, nil
}

func nullableTime(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t.UTC(), Valid: true}
}
