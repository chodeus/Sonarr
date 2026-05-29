// Package logs is the read/write repository for the application log
// database (the separate RoleLog datastore — see internal/datastore).
// It ports the C# `Instrumentation/Log` model + `LogRepository`
// (`src/NzbDrone.Core/Instrumentation/{Log,LogRepository}.cs`) and the
// purge arm `LogService.Execute(ClearLogCommand)` →
// `_logRepository.Purge(vacuum: true)`.
//
// The Logs table schema (Id, Message, Time, Logger, Exception,
// ExceptionType, Level — the Method column was dropped by log-DB
// migration 064) already exists in the embedded log migrations; this
// package opens a repo over it. The async slog→DB sink that POPULATES
// the table (the C# NLog DatabaseTarget equivalent) lands as its own
// slice; this slice ships the repo + read/purge primitives + the
// boot-time DB open.
//
// Unlike the main-DB repositories this package does NOT use the generic
// internal/repo helper — that helper hard-rejects any non-RoleMain DB
// (repo.New: "expected RoleMain"). The Logs table lives in the RoleLog
// database, so the handful of queries are issued directly here.
package logs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
)

// ErrNilDB is returned by NewRepo for a nil database.
var ErrNilDB = errors.New("logs: nil DB")

// Log mirrors C# `Instrumentation/Log.cs`. The Method column was
// removed at log-DB migration 064, so it is absent here.
type Log struct {
	ID            int
	Message       string
	Time          time.Time
	Logger        string
	Exception     string
	ExceptionType string
	Level         string
}

// Repo is the log-database repository.
type Repo struct {
	db *datastore.DB
}

// NewRepo binds a Repo to the (RoleLog) DB.
func NewRepo(db *datastore.DB) (*Repo, error) {
	if db == nil || db.SQL == nil {
		return nil, ErrNilDB
	}
	return &Repo{db: db}, nil
}

// Insert persists a log row and populates its ID. Used by the slog→DB
// sink (DatabaseTarget equivalent). RETURNING "Id" works on both SQLite
// (>=3.35) and Postgres, mirroring internal/repo's insert.
func (r *Repo) Insert(ctx context.Context, l *Log) (*Log, error) {
	if l == nil {
		return nil, errors.New("logs: nil value")
	}
	const q = `INSERT INTO "Logs" ("Message","Time","Logger","Exception","ExceptionType","Level") VALUES ` +
		`(%s,%s,%s,%s,%s,%s) RETURNING "Id"`
	ph := func(n int) string { return datastore.Placeholder(r.db.Engine, n) }
	query := fmt.Sprintf(q, ph(1), ph(2), ph(3), ph(4), ph(5), ph(6))
	var id int
	if err := r.db.SQL.QueryRowContext(ctx, query,
		l.Message, l.Time.UTC(), l.Logger, nullStr(l.Exception), nullStr(l.ExceptionType), l.Level,
	).Scan(&id); err != nil {
		return nil, fmt.Errorf("logs: insert: %w", err)
	}
	l.ID = id
	return l, nil
}

// sortColumns whitelists the columns the paged read may sort on, mapping
// the API-facing key to the physical column. Anything else falls back to
// Time.
var sortColumns = map[string]string{
	"id":      "Id",
	"time":    "Time",
	"level":   "Level",
	"logger":  "Logger",
	"message": "Message",
}

// Page is one page of log rows plus the unpaged total.
type Page struct {
	Records      []Log
	TotalRecords int
}

// PageQuery describes a paged log read. Levels, when non-empty, filters
// to rows whose Level is in the set (the cumulative level filter the C#
// LogController builds from the `level` query param). page is 1-indexed.
type PageQuery struct {
	Page     int
	PageSize int
	SortKey  string
	Desc     bool
	Levels   []string
}

// Paged returns one page of log rows ordered by SortKey (whitelisted;
// defaults to Time) in the requested direction, optionally filtered to
// the given Levels, plus the (filtered) total row count. PageSize <= 0
// falls back to 50. Mirrors the shape the C# `LogController.GetLogs`
// PagingSpec consumes.
func (r *Repo) Paged(ctx context.Context, qy PageQuery) (Page, error) {
	page, pageSize := qy.Page, qy.PageSize
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 50
	}
	col, ok := sortColumns[qy.SortKey]
	if !ok {
		col = "Time"
	}
	dir := "ASC"
	if qy.Desc {
		dir = "DESC"
	}

	// Optional level filter — a `Level IN (...)` clause shared by the
	// COUNT and the page SELECT. Placeholders 1..len(Levels) bind the
	// level values; LIMIT/OFFSET take the next two.
	where := ""
	args := make([]any, 0, len(qy.Levels)+2)
	if len(qy.Levels) > 0 {
		phs := make([]string, len(qy.Levels))
		for i, lv := range qy.Levels {
			phs[i] = datastore.Placeholder(r.db.Engine, i+1)
			args = append(args, lv)
		}
		where = ` WHERE "Level" IN (` + strings.Join(phs, ",") + `)`
	}

	var total int
	if err := r.db.SQL.QueryRowContext(ctx, `SELECT COUNT(*) FROM "Logs"`+where, args...).Scan(&total); err != nil {
		return Page{}, fmt.Errorf("logs: count: %w", err)
	}

	limitPH := datastore.Placeholder(r.db.Engine, len(qy.Levels)+1)
	offsetPH := datastore.Placeholder(r.db.Engine, len(qy.Levels)+2)
	q := fmt.Sprintf(
		`SELECT "Id","Message","Time","Logger","Exception","ExceptionType","Level" FROM "Logs"%s ORDER BY "%s" %s, "Id" %s LIMIT %s OFFSET %s`,
		where, col, dir, dir, limitPH, offsetPH,
	)
	rows, err := r.db.SQL.QueryContext(ctx, q, append(args, pageSize, (page-1)*pageSize)...)
	if err != nil {
		return Page{}, fmt.Errorf("logs: paged query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]Log, 0, pageSize)
	for rows.Next() {
		l, err := scanLog(rows)
		if err != nil {
			return Page{}, fmt.Errorf("logs: scan: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("logs: rows: %w", err)
	}
	return Page{Records: out, TotalRecords: total}, nil
}

// Purge deletes every Logs row. Ports `LogService.Execute(ClearLogCommand)`
// → `LogRepository.Purge(vacuum: true)`. Returns rows removed.
func (r *Repo) Purge(ctx context.Context) (int64, error) {
	res, err := r.db.SQL.ExecContext(ctx, `DELETE FROM "Logs"`)
	if err != nil {
		return 0, fmt.Errorf("logs: Purge: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("logs: Purge: rowsAffected: %w", err)
	}
	return n, nil
}

// Trim deletes rows older than 7 days. Ports `LogRepository.Trim()`
// (`Delete(c => c.Time <= now-7d)`). now is injected so callers/tests
// control the cutoff. Returns rows removed.
func (r *Repo) Trim(ctx context.Context, now time.Time) (int64, error) {
	cutoff := now.UTC().AddDate(0, 0, -7)
	q := fmt.Sprintf(`DELETE FROM "Logs" WHERE "Time" <= %s`, datastore.Placeholder(r.db.Engine, 1))
	res, err := r.db.SQL.ExecContext(ctx, q, cutoff)
	if err != nil {
		return 0, fmt.Errorf("logs: Trim: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("logs: Trim: rowsAffected: %w", err)
	}
	return n, nil
}

// rowScanner is the minimal interface *sql.Row / *sql.Rows satisfy.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanLog reads one Logs row, mapping the nullable Exception /
// ExceptionType columns back to empty strings.
func scanLog(s rowScanner) (Log, error) {
	var l Log
	var exception, exceptionType sql.NullString
	if err := s.Scan(&l.ID, &l.Message, &l.Time, &l.Logger, &exception, &exceptionType, &l.Level); err != nil {
		return Log{}, err
	}
	l.Time = l.Time.UTC()
	l.Exception = exception.String
	l.ExceptionType = exceptionType.String
	return l, nil
}

// nullStr maps an empty string to a SQL NULL (matching the C# nullable
// Exception/ExceptionType columns), else the string itself.
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
