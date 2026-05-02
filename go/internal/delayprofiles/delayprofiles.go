// Package delayprofiles is the Go port of NzbDrone.Core.Profiles.Delay.
// DelayProfiles control how long Sonarr waits for a preferred-protocol
// release before falling back to the alternative. The schema mirrors
// what migrations 070, 097, 156, 182 produce: { Id, EnableUsenet,
// EnableTorrent, PreferredProtocol, UsenetDelay, TorrentDelay, Order,
// Tags, BypassIfHighestQuality, BypassIfAboveCustomFormatScore,
// MinimumCustomFormatScore (nullable) }.
//
// This is the first repository in the port that exercises:
//   - Boolean columns whose engine-level types differ (SQLite INTEGER
//     0/1 vs Postgres boolean true/false). Both engines accept Go
//     bool through database/sql when the column type is a recognised
//     bool variant; modernc/sqlite handles INTEGER columns by Scan-
//     ing into bool transparently because they're declared as the
//     boolean affinity type by FluentMigrator and we mirror that.
//   - An embedded-doc field (Tags []int stored as a JSON array in the
//     TEXT column). Round-trip via datastore.MarshalEmbedded /
//     UnmarshalEmbedded; explicit per-field marshal in queries.
//   - A nullable INT column (MinimumCustomFormatScore) modeled as
//     *int to match C# int? — distinct from the C# bool? case which
//     would also be a Go pointer.
//   - The reserved SQL keyword "Order" as a column name. Quoted in
//     every reference to keep both engines happy.
package delayprofiles

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"sonarr.tv/sonarr/internal/datastore"
)

// Profile is one persisted DelayProfile row.
//
// Tags is the JSON int-array stored in the TEXT column "Tags". We expose
// it as []int on the Go side; the repository serializes/deserializes it
// at the SQL boundary using datastore.MarshalEmbedded / Unmarshal.
//
// MinimumCustomFormatScore mirrors C# int? — non-nil means "filter on";
// nil means "no minimum".
type Profile struct {
	ID                             int   `json:"id"`
	EnableUsenet                   bool  `json:"enableUsenet"`
	EnableTorrent                  bool  `json:"enableTorrent"`
	PreferredProtocol              int   `json:"preferredProtocol"`
	UsenetDelay                    int   `json:"usenetDelay"`
	TorrentDelay                   int   `json:"torrentDelay"`
	Order                          int   `json:"order"`
	Tags                           []int `json:"tags"`
	BypassIfHighestQuality         bool  `json:"bypassIfHighestQuality"`
	BypassIfAboveCustomFormatScore bool  `json:"bypassIfAboveCustomFormatScore"`
	MinimumCustomFormatScore       *int  `json:"minimumCustomFormatScore,omitempty"`
}

// ErrNotFound is returned by Get when no row matches.
var ErrNotFound = errors.New("delayprofiles: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	db *datastore.DB
}

// NewRepo returns a Repo bound to the given DB.
func NewRepo(db *datastore.DB) (*Repo, error) {
	if db == nil || db.SQL == nil {
		return nil, errors.New("delayprofiles: nil DB")
	}
	if db.Role != datastore.RoleMain {
		return nil, fmt.Errorf("delayprofiles: expected RoleMain, got %q", db.Role)
	}
	return &Repo{db: db}, nil
}

// columnList is the canonical column ordering used by SELECT and INSERT.
// Defining it once avoids drift between query strings.
const columnList = `"EnableUsenet","EnableTorrent","PreferredProtocol",` +
	`"UsenetDelay","TorrentDelay","Order","Tags",` +
	`"BypassIfHighestQuality","BypassIfAboveCustomFormatScore",` +
	`"MinimumCustomFormatScore"`

// Insert persists a new Profile and populates p.ID. p.Tags is serialized
// to JSON via datastore.MarshalEmbedded; nil Tags becomes "null", caller
// should pass an empty slice for the empty-array storage shape.
func (r *Repo) Insert(ctx context.Context, p *Profile) (*Profile, error) {
	if p == nil {
		return nil, errors.New("delayprofiles: nil Profile")
	}
	tagsJSON, err := datastore.MarshalEmbedded(p.Tags)
	if err != nil {
		return nil, fmt.Errorf("delayprofiles: marshal tags: %w", err)
	}
	q := fmt.Sprintf(
		`INSERT INTO "DelayProfiles" (%s) VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s,%s) RETURNING "Id"`,
		columnList,
		datastore.Placeholder(r.db.Engine, 1),
		datastore.Placeholder(r.db.Engine, 2),
		datastore.Placeholder(r.db.Engine, 3),
		datastore.Placeholder(r.db.Engine, 4),
		datastore.Placeholder(r.db.Engine, 5),
		datastore.Placeholder(r.db.Engine, 6),
		datastore.Placeholder(r.db.Engine, 7),
		datastore.Placeholder(r.db.Engine, 8),
		datastore.Placeholder(r.db.Engine, 9),
		datastore.Placeholder(r.db.Engine, 10),
	)
	if err := r.db.SQL.QueryRowContext(ctx, q,
		p.EnableUsenet, p.EnableTorrent, p.PreferredProtocol,
		p.UsenetDelay, p.TorrentDelay, p.Order, tagsJSON,
		p.BypassIfHighestQuality, p.BypassIfAboveCustomFormatScore,
		nullableInt(p.MinimumCustomFormatScore),
	).Scan(&p.ID); err != nil {
		return nil, fmt.Errorf("delayprofiles: insert: %w", err)
	}
	return p, nil
}

// Get returns the Profile with the given ID or ErrNotFound.
func (r *Repo) Get(ctx context.Context, id int) (*Profile, error) {
	q := fmt.Sprintf(
		`SELECT "Id",%s FROM "DelayProfiles" WHERE "Id" = %s`,
		columnList, datastore.Placeholder(r.db.Engine, 1))
	var p Profile
	var tagsJSON string
	var minScore sql.NullInt64
	switch err := r.db.SQL.QueryRowContext(ctx, q, id).Scan(
		&p.ID, &p.EnableUsenet, &p.EnableTorrent, &p.PreferredProtocol,
		&p.UsenetDelay, &p.TorrentDelay, &p.Order, &tagsJSON,
		&p.BypassIfHighestQuality, &p.BypassIfAboveCustomFormatScore,
		&minScore,
	); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("delayprofiles: get(%d): %w", id, err)
	}
	if minScore.Valid {
		v := int(minScore.Int64)
		p.MinimumCustomFormatScore = &v
	}
	if err := datastore.UnmarshalEmbedded(tagsJSON, &p.Tags); err != nil {
		return nil, fmt.Errorf("delayprofiles: unmarshal tags(%d): %w", id, err)
	}
	return &p, nil
}

// All returns every Profile, ordered by Order ascending (matching the
// runtime evaluation order Sonarr applies).
func (r *Repo) All(ctx context.Context) ([]Profile, error) {
	q := fmt.Sprintf(`SELECT "Id",%s FROM "DelayProfiles" ORDER BY "Order"`, columnList)
	rows, err := r.db.SQL.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("delayprofiles: all: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Profile
	for rows.Next() {
		var p Profile
		var tagsJSON string
		var minScore sql.NullInt64
		if err := rows.Scan(
			&p.ID, &p.EnableUsenet, &p.EnableTorrent, &p.PreferredProtocol,
			&p.UsenetDelay, &p.TorrentDelay, &p.Order, &tagsJSON,
			&p.BypassIfHighestQuality, &p.BypassIfAboveCustomFormatScore,
			&minScore,
		); err != nil {
			return nil, fmt.Errorf("delayprofiles: all scan: %w", err)
		}
		if minScore.Valid {
			v := int(minScore.Int64)
			p.MinimumCustomFormatScore = &v
		}
		if err := datastore.UnmarshalEmbedded(tagsJSON, &p.Tags); err != nil {
			return nil, fmt.Errorf("delayprofiles: all unmarshal tags(%d): %w", p.ID, err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("delayprofiles: all rows: %w", err)
	}
	return out, nil
}

// Update writes new field values for an existing Profile. ID must be set.
func (r *Repo) Update(ctx context.Context, p *Profile) error {
	if p == nil {
		return errors.New("delayprofiles: nil Profile")
	}
	tagsJSON, err := datastore.MarshalEmbedded(p.Tags)
	if err != nil {
		return fmt.Errorf("delayprofiles: marshal tags: %w", err)
	}
	q := fmt.Sprintf(
		`UPDATE "DelayProfiles" SET
			"EnableUsenet" = %s, "EnableTorrent" = %s, "PreferredProtocol" = %s,
			"UsenetDelay" = %s, "TorrentDelay" = %s, "Order" = %s, "Tags" = %s,
			"BypassIfHighestQuality" = %s, "BypassIfAboveCustomFormatScore" = %s,
			"MinimumCustomFormatScore" = %s
		WHERE "Id" = %s`,
		datastore.Placeholder(r.db.Engine, 1),
		datastore.Placeholder(r.db.Engine, 2),
		datastore.Placeholder(r.db.Engine, 3),
		datastore.Placeholder(r.db.Engine, 4),
		datastore.Placeholder(r.db.Engine, 5),
		datastore.Placeholder(r.db.Engine, 6),
		datastore.Placeholder(r.db.Engine, 7),
		datastore.Placeholder(r.db.Engine, 8),
		datastore.Placeholder(r.db.Engine, 9),
		datastore.Placeholder(r.db.Engine, 10),
		datastore.Placeholder(r.db.Engine, 11),
	)
	res, err := r.db.SQL.ExecContext(ctx, q,
		p.EnableUsenet, p.EnableTorrent, p.PreferredProtocol,
		p.UsenetDelay, p.TorrentDelay, p.Order, tagsJSON,
		p.BypassIfHighestQuality, p.BypassIfAboveCustomFormatScore,
		nullableInt(p.MinimumCustomFormatScore),
		p.ID,
	)
	if err != nil {
		return fmt.Errorf("delayprofiles: update(%d): %w", p.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delayprofiles: update(%d) rowsAffected: %w", p.ID, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes the Profile with the given ID.
func (r *Repo) Delete(ctx context.Context, id int) error {
	q := fmt.Sprintf(`DELETE FROM "DelayProfiles" WHERE "Id" = %s`, datastore.Placeholder(r.db.Engine, 1))
	res, err := r.db.SQL.ExecContext(ctx, q, id)
	if err != nil {
		return fmt.Errorf("delayprofiles: delete(%d): %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delayprofiles: delete(%d) rowsAffected: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// nullableInt converts a *int into a value the database/sql layer
// understands as either NULL or the underlying int. Pgx and modernc/sqlite
// both accept a typed sql.NullInt64 here without further conversion.
func nullableInt(p *int) any {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*p), Valid: true}
}
