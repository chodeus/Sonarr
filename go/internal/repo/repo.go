// Package repo provides a small generic CRUD helper that captures the
// shared shape across Sonarr's per-table repositories. Each per-domain
// package (tags, config, delayprofiles, scheduledtasks, ...) defines a
// model struct and a Mapper[T] describing how that struct maps to its
// SQL table; this package then provides Insert / Get / All / Update /
// Delete primitives implemented once and parameterised by Mapper.
//
// The design keeps the per-domain SQL implicit: callers describe the
// table name, column list, ordering, and the Bind/Scan/SetID/GetID funcs,
// and this package builds the standard CRUD SQL using those parts plus
// datastore.Placeholder for engine-aware parameter syntax. Per-domain
// helpers like GetByX / Set (upserts) / domain-specific queries are NOT
// generated here — the caller writes those alongside its Mapper using
// the same datastore.Placeholder primitive.
//
// What this helper deliberately does NOT do:
//
//   - Reflect over struct tags. The Mapper's Bind/Scan are explicit
//     functions; column ordering and types are owned by the caller.
//     Reflection would save a few lines per package at the cost of
//     opaque errors and harder migrations when columns change.
//   - Manage embedded-doc JSON serialization. Domains that need it
//     (delayprofiles.Tags, indexerstatus.LastRssSyncReleaseInfo) handle
//     marshal/unmarshal inside their Bind/Scan funcs using
//     datastore.MarshalEmbedded / UnmarshalEmbedded.
//   - Cache prepared statements. The hot path here is ms-level table
//     reads; the savings from prepared-statement caching are not worth
//     the lifecycle complexity until profiling proves otherwise.
package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"sonarr.tv/sonarr/internal/datastore"
)

// RowScanner is the subset of *sql.Row / *sql.Rows that Mapper.Scan needs.
// Both QueryRow and rows.Scan satisfy it.
type RowScanner interface {
	Scan(dest ...any) error
}

// Mapper describes how a row of T's table maps to T's struct. All fields
// are required.
//
// Conventions:
//
//   - Name is used in returned error messages (e.g. "tags: insert: ...").
//     It should match the per-domain package name.
//   - Table is the schema name without quotes; this package wraps it in
//     double quotes when building SQL so callers don't have to.
//   - Columns lists every persisted column EXCEPT "Id" — Id is always
//     SELECTed and is the WHERE-key for Get/Update/Delete. Order in this
//     slice is canonical: Bind must return values in this order, Scan
//     must read in this order (preceded by Id), and Update emits SETs in
//     this order.
//   - OrderBy is the column name used for All's ORDER BY. Pass "Id" if
//     no domain ordering applies. Wrapped in quotes by this package.
//   - Bind returns the persisted-column values for one row, in Columns
//     order. It must NOT include Id.
//   - Scan reads (Id, Columns...) into t in that exact order.
//   - GetID / SetID expose the Id field on T for Update WHERE / Insert
//     RETURNING. Callers write trivial closures: `func(t *Tag) int
//     { return t.ID }`.
type Mapper[T any] struct {
	Name    string
	Table   string
	Columns []string
	OrderBy string
	Bind    func(*T) []any
	Scan    func(*T, RowScanner) error
	GetID   func(*T) int
	SetID   func(*T, int)
}

// Repository is the generic CRUD wrapper. Construct one per domain via
// New(). Concurrency-safe to share across goroutines (database/sql is
// concurrency-safe; this struct is read-only).
type Repository[T any] struct {
	db       *datastore.DB
	mapper   Mapper[T]
	notFound error

	// Pre-computed quoted column lists so we build SQL strings once per
	// repository, not per call.
	quotedColumns string // `"Col1","Col2",...`
	quotedOrderBy string // `"OrderCol"`
}

// New returns a Repository[T] bound to db. notFound is the sentinel error
// the caller wants returned from Get/Update/Delete on a no-row outcome —
// each domain package keeps its own ErrNotFound and threads it through.
func New[T any](db *datastore.DB, mapper Mapper[T], notFound error) (*Repository[T], error) {
	if db == nil || db.SQL == nil {
		return nil, fmt.Errorf("%s: nil DB", nameOr(mapper.Name))
	}
	if db.Role != datastore.RoleMain {
		return nil, fmt.Errorf("%s: expected RoleMain, got %q", nameOr(mapper.Name), db.Role)
	}
	if notFound == nil {
		return nil, fmt.Errorf("%s: nil notFound sentinel", nameOr(mapper.Name))
	}
	if err := validateMapper(mapper); err != nil {
		return nil, err
	}
	return &Repository[T]{
		db:            db,
		mapper:        mapper,
		notFound:      notFound,
		quotedColumns: quoteAll(mapper.Columns),
		quotedOrderBy: `"` + mapper.OrderBy + `"`,
	}, nil
}

func validateMapper[T any](m Mapper[T]) error {
	switch {
	case m.Name == "":
		return errors.New("repo: Mapper.Name required")
	case m.Table == "":
		return fmt.Errorf("%s: Mapper.Table required", m.Name)
	case len(m.Columns) == 0:
		return fmt.Errorf("%s: Mapper.Columns must be non-empty", m.Name)
	case m.OrderBy == "":
		return fmt.Errorf("%s: Mapper.OrderBy required", m.Name)
	case m.Bind == nil || m.Scan == nil || m.GetID == nil || m.SetID == nil:
		return fmt.Errorf("%s: Mapper.Bind/Scan/GetID/SetID all required", m.Name)
	}
	return nil
}

func nameOr(s string) string {
	if s == "" {
		return "repo"
	}
	return s
}

// Insert persists a new row and populates t's Id field via SetID.
// Returns the same pointer for chaining.
func (r *Repository[T]) Insert(ctx context.Context, t *T) (*T, error) {
	if t == nil {
		return nil, fmt.Errorf("%s: nil value", r.mapper.Name)
	}
	q := fmt.Sprintf(
		`INSERT INTO "%s" (%s) VALUES (%s) RETURNING "Id"`,
		r.mapper.Table, r.quotedColumns, r.placeholders(len(r.mapper.Columns)),
	)
	var id int
	if err := r.db.SQL.QueryRowContext(ctx, q, r.mapper.Bind(t)...).Scan(&id); err != nil {
		return nil, fmt.Errorf("%s: insert: %w", r.mapper.Name, err)
	}
	r.mapper.SetID(t, id)
	return t, nil
}

// Get returns the row with the given Id, or the configured notFound
// sentinel if no row matches.
func (r *Repository[T]) Get(ctx context.Context, id int) (*T, error) {
	q := fmt.Sprintf(
		`SELECT "Id",%s FROM "%s" WHERE "Id" = %s`,
		r.quotedColumns, r.mapper.Table, datastore.Placeholder(r.db.Engine, 1),
	)
	var v T
	switch err := r.mapper.Scan(&v, r.db.SQL.QueryRowContext(ctx, q, id)); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, r.notFound
	case err != nil:
		return nil, fmt.Errorf("%s: get(%d): %w", r.mapper.Name, id, err)
	}
	return &v, nil
}

// All returns every row, ordered by the Mapper's OrderBy column ASC.
func (r *Repository[T]) All(ctx context.Context) ([]T, error) {
	q := fmt.Sprintf(
		`SELECT "Id",%s FROM "%s" ORDER BY %s`,
		r.quotedColumns, r.mapper.Table, r.quotedOrderBy,
	)
	rows, err := r.db.SQL.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("%s: all: %w", r.mapper.Name, err)
	}
	defer func() { _ = rows.Close() }()
	var out []T
	for rows.Next() {
		var v T
		if err := r.mapper.Scan(&v, rows); err != nil {
			return nil, fmt.Errorf("%s: all scan: %w", r.mapper.Name, err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: all rows: %w", r.mapper.Name, err)
	}
	return out, nil
}

// Update writes new column values for the row whose Id matches t's Id.
// Returns the configured notFound sentinel if no row matched.
func (r *Repository[T]) Update(ctx context.Context, t *T) error {
	if t == nil {
		return fmt.Errorf("%s: nil value", r.mapper.Name)
	}
	id := r.mapper.GetID(t)
	q := fmt.Sprintf(
		`UPDATE "%s" SET %s WHERE "Id" = %s`,
		r.mapper.Table,
		r.updateAssignments(),
		datastore.Placeholder(r.db.Engine, len(r.mapper.Columns)+1),
	)
	args := append(r.mapper.Bind(t), id) //nolint:gocritic // append-result intentional
	res, err := r.db.SQL.ExecContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("%s: update(%d): %w", r.mapper.Name, id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s: update(%d) rowsAffected: %w", r.mapper.Name, id, err)
	}
	if n == 0 {
		return r.notFound
	}
	return nil
}

// Delete removes the row with the given Id. Returns the configured
// notFound sentinel if no row matched.
func (r *Repository[T]) Delete(ctx context.Context, id int) error {
	q := fmt.Sprintf(
		`DELETE FROM "%s" WHERE "Id" = %s`,
		r.mapper.Table, datastore.Placeholder(r.db.Engine, 1),
	)
	res, err := r.db.SQL.ExecContext(ctx, q, id)
	if err != nil {
		return fmt.Errorf("%s: delete(%d): %w", r.mapper.Name, id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s: delete(%d) rowsAffected: %w", r.mapper.Name, id, err)
	}
	if n == 0 {
		return r.notFound
	}
	return nil
}

// DB returns the underlying datastore handle. Per-domain repos use it for
// their domain-specific queries (GetByX, Set/upsert) without re-importing
// datastore.
func (r *Repository[T]) DB() *datastore.DB { return r.db }

// placeholders builds "?,?,?" for SQLite or "$1,$2,$3" for Postgres.
func (r *Repository[T]) placeholders(n int) string {
	parts := make([]string, n)
	for i := 0; i < n; i++ {
		parts[i] = datastore.Placeholder(r.db.Engine, i+1)
	}
	return strings.Join(parts, ",")
}

// updateAssignments builds `"Col1" = $1, "Col2" = $2, ...` (or `?` for SQLite).
func (r *Repository[T]) updateAssignments() string {
	parts := make([]string, len(r.mapper.Columns))
	for i, c := range r.mapper.Columns {
		parts[i] = `"` + c + `" = ` + datastore.Placeholder(r.db.Engine, i+1)
	}
	return strings.Join(parts, ", ")
}

// quoteAll quotes a column list as `"A","B","C"`.
func quoteAll(cols []string) string {
	parts := make([]string, len(cols))
	for i, c := range cols {
		parts[i] = `"` + c + `"`
	}
	return strings.Join(parts, ",")
}
