// Package commands is the Go port of NzbDrone.Core.Messaging.Commands
// CommandModel persistence. Each row is one queued/in-flight/completed
// command — the unit the scheduler dispatches and the API exposes via
// /api/v3/command. The C# CommandQueueManager treats this table as the
// authoritative queue; the Go scheduler will follow the same pattern.
//
// Schema at HEAD (v230): Id, Name NOT NULL, Body NOT NULL TEXT
// (JSON-encoded Command subclass), Priority NOT NULL INT,
// Status NOT NULL INT, QueuedAt NOT NULL DATETIME, StartedAt nullable,
// EndedAt nullable, Duration nullable TEXT (TimeSpan format),
// Exception nullable TEXT, Trigger NOT NULL INT, Result INT NOT NULL DEFAULT 1.
//
// Notes:
//
//   - Body is opaque JSON. The Go scheduler will deserialise it through
//     a registered command-type table; the repo treats it as a string.
//   - Duration is stored as a TimeSpan string (`hh:mm:ss[.fffffff]`).
//     The Mapper uses datastore.FormatDuration / ParseDuration at the
//     SQL boundary; callers see a regular `*time.Duration`.
//   - Priority/Status/Trigger/Result are persisted enums; the Go port
//     stores them as plain int (the on-disk wire contract).
//   - The C# CommandModel has a `Message` field that is NOT persisted;
//     omitted from this struct. Callers use Result + Exception to
//     surface success/failure detail.
package commands

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/repo"
)

// Command is one Commands row.
type Command struct {
	ID        int            `json:"id"`
	Name      string         `json:"name"`
	Body      string         `json:"body"`
	Priority  int            `json:"priority"`
	Status    int            `json:"status"`
	QueuedAt  time.Time      `json:"queuedAt"`
	StartedAt *time.Time     `json:"startedAt"`
	EndedAt   *time.Time     `json:"endedAt"`
	Duration  *time.Duration `json:"duration"`
	Exception string         `json:"exception"`
	Trigger   int            `json:"trigger"`
	Result    int            `json:"result"`
}

// ErrNotFound is returned when no row matches.
var ErrNotFound = errors.New("commands: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	*repo.Repository[Command]
}

var mapper = repo.Mapper[Command]{
	Name:  "commands",
	Table: "Commands",
	Columns: []string{
		"Name", "Body", "Priority", "Status", "QueuedAt",
		"StartedAt", "EndedAt", "Duration", "Exception", "Trigger", "Result",
	},
	OrderBy: "QueuedAt",
	Bind: func(c *Command) []any {
		return []any{
			c.Name, c.Body, c.Priority, c.Status, c.QueuedAt.UTC(),
			nullableTime(c.StartedAt),
			nullableTime(c.EndedAt),
			nullableDuration(c.Duration),
			nullableStringEmpty(c.Exception),
			c.Trigger, c.Result,
		}
	},
	Scan: func(c *Command, s repo.RowScanner) error {
		var started, ended sql.NullTime
		var duration, exception sql.NullString
		if err := s.Scan(
			&c.ID, &c.Name, &c.Body, &c.Priority, &c.Status, &c.QueuedAt,
			&started, &ended, &duration, &exception, &c.Trigger, &c.Result,
		); err != nil {
			return err
		}
		c.QueuedAt = c.QueuedAt.UTC()
		if started.Valid {
			v := started.Time.UTC()
			c.StartedAt = &v
		}
		if ended.Valid {
			v := ended.Time.UTC()
			c.EndedAt = &v
		}
		if duration.Valid {
			parsed, err := datastore.ParseDuration(duration.String)
			if err != nil {
				return fmt.Errorf("parse Duration: %w", err)
			}
			c.Duration = &parsed
		}
		c.Exception = exception.String
		return nil
	},
	GetID: func(c *Command) int { return c.ID },
	SetID: func(c *Command, id int) { c.ID = id },
}

// NewRepo returns a Repo bound to the given DB.
func NewRepo(db *datastore.DB) (*Repo, error) {
	r, err := repo.New(db, mapper, ErrNotFound)
	if err != nil {
		return nil, err
	}
	return &Repo{Repository: r}, nil
}

// AllByStatus returns every Command with the given Status, ordered by
// QueuedAt ascending. The scheduler dispatch loop calls this with
// Status=Queued (1) to find work to dequeue.
func (r *Repo) AllByStatus(ctx context.Context, status int) ([]Command, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT "Id","Name","Body","Priority","Status","QueuedAt","StartedAt","EndedAt","Duration","Exception","Trigger","Result" FROM "Commands" WHERE "Status" = %s ORDER BY "QueuedAt"`,
		datastore.Placeholder(db.Engine, 1))
	rows, err := db.SQL.QueryContext(ctx, q, status)
	if err != nil {
		return nil, fmt.Errorf("commands: allByStatus(%d): %w", status, err)
	}
	defer func() { _ = rows.Close() }()
	var out []Command
	for rows.Next() {
		var c Command
		if err := mapper.Scan(&c, rows); err != nil {
			return nil, fmt.Errorf("commands: allByStatus scan: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("commands: allByStatus rows: %w", err)
	}
	return out, nil
}

func nullableTime(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t.UTC(), Valid: true}
}

func nullableDuration(d *time.Duration) sql.NullString {
	if d == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: datastore.FormatDuration(*d), Valid: true}
}

func nullableStringEmpty(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
