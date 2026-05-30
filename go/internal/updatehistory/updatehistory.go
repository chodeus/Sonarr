// Package updatehistory ports the C# `NzbDrone.Core.Update.History`
// subtree — the persisted record of which Sonarr versions have been
// installed. It backs two behaviours:
//
//   - The `prevVersion` query param on the recent-updates feed
//     (`UpdatePackageProvider.GetRecentUpdates`) so the server can
//     tailor the changelog list to what the user previously ran.
//   - The post-install `UpdateInstalledEvent` that drives the
//     `OnApplicationUpdate` notification (wired in a later slice).
//
// Ports `Update/History/{UpdateHistory,UpdateHistoryEventType,
// UpdateHistoryRepository,UpdateHistoryService}.cs`.
//
// The UpdateHistory table lives in the RoleLog database (C#
// `IUpdateHistoryRepository : BasicRepository<UpdateHistory>` is
// constructed with `ILogDatabase`). Like internal/logs this package
// therefore issues its queries directly against the RoleLog
// datastore rather than going through the main-DB-only
// internal/repo helper. The table + IX_UpdateHistory_Date index are
// created by log migration 141 (`00141_add_update_history.sql`).
package updatehistory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
)

// EventType mirrors C# `UpdateHistoryEventType`
// (`Update/History/UpdateHistoryEventType.cs`). Stored as the integer
// EventType column.
type EventType int

const (
	// EventTypeUnknown is the zero/default tier.
	EventTypeUnknown EventType = 0
	// EventTypeInitiated marks an update that was started.
	EventTypeInitiated EventType = 1
	// EventTypeInstalled marks an update that completed and is now
	// running. Only Installed rows are queried by the service.
	EventTypeInstalled EventType = 2
)

// History mirrors C# `UpdateHistory` (`Update/History/UpdateHistory.cs`).
// Version is the C# `System.Version.ToString()` value stored verbatim
// in the TEXT Version column.
type History struct {
	ID        int
	Date      time.Time
	Version   string
	EventType EventType
}

// ErrNilDB is returned by NewRepo for a nil database.
var ErrNilDB = errors.New("updatehistory: nil DB")

// Repo is the update-history repository over the RoleLog DB.
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

// Insert persists an update-history row and populates its ID. Ports the
// `BasicRepository.Insert` used by `UpdateHistoryService.Handle`.
func (r *Repo) Insert(ctx context.Context, h *History) error {
	if h == nil {
		return errors.New("updatehistory: nil value")
	}
	ph := func(n int) string { return datastore.Placeholder(r.db.Engine, n) }
	q := fmt.Sprintf(
		`INSERT INTO "UpdateHistory" ("Date","Version","EventType") VALUES (%s,%s,%s) RETURNING "Id"`,
		ph(1), ph(2), ph(3),
	)
	var id int
	if err := r.db.SQL.QueryRowContext(ctx, q, h.Date.UTC(), h.Version, int(h.EventType)).Scan(&id); err != nil {
		return fmt.Errorf("updatehistory: insert: %w", err)
	}
	h.ID = id
	return nil
}

// LastInstalled ports `UpdateHistoryRepository.LastInstalled` — the most
// recent Installed row (Date DESC), or nil when none. ErrNoRows maps to
// (nil, nil).
func (r *Repo) LastInstalled(ctx context.Context) (*History, error) {
	return r.queryOne(ctx, 0)
}

// PreviouslyInstalled ports `UpdateHistoryRepository.PreviouslyInstalled`
// — the second-most-recent Installed row (Date DESC, skip 1), i.e. the
// version that ran before the current one. nil when there is no prior
// install.
func (r *Repo) PreviouslyInstalled(ctx context.Context) (*History, error) {
	return r.queryOne(ctx, 1)
}

// queryOne fetches a single Installed row ordered Date DESC with the
// given OFFSET. offset 0 → LastInstalled, offset 1 → PreviouslyInstalled.
func (r *Repo) queryOne(ctx context.Context, offset int) (*History, error) {
	q := fmt.Sprintf(
		`SELECT "Id","Date","Version","EventType" FROM "UpdateHistory" WHERE "EventType" = %s `+
			`ORDER BY "Date" DESC LIMIT 1 OFFSET %s`,
		datastore.Placeholder(r.db.Engine, 1), datastore.Placeholder(r.db.Engine, 2),
	)
	var h History
	var et int
	err := r.db.SQL.QueryRowContext(ctx, q, int(EventTypeInstalled), offset).
		Scan(&h.ID, &h.Date, &h.Version, &et)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("updatehistory: query: %w", err)
	}
	h.EventType = EventType(et)
	return &h, nil
}

// InstalledSince ports `UpdateHistoryRepository.InstalledSince` — all
// Installed rows on/after dateTime, ordered Date ASC.
func (r *Repo) InstalledSince(ctx context.Context, dateTime time.Time) ([]History, error) {
	q := fmt.Sprintf(
		`SELECT "Id","Date","Version","EventType" FROM "UpdateHistory" `+
			`WHERE "EventType" = %s AND "Date" >= %s ORDER BY "Date" ASC`,
		datastore.Placeholder(r.db.Engine, 1), datastore.Placeholder(r.db.Engine, 2),
	)
	rows, err := r.db.SQL.QueryContext(ctx, q, int(EventTypeInstalled), dateTime.UTC())
	if err != nil {
		return nil, fmt.Errorf("updatehistory: installed-since: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []History
	for rows.Next() {
		var h History
		var et int
		if err := rows.Scan(&h.ID, &h.Date, &h.Version, &et); err != nil {
			return nil, fmt.Errorf("updatehistory: scan: %w", err)
		}
		h.EventType = EventType(et)
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("updatehistory: rows: %w", err)
	}
	return out, nil
}

// Purge deletes every update-history row. Ports the `BasicRepository.Purge`
// the service calls when it detects a corrupted history.
func (r *Repo) Purge(ctx context.Context) error {
	if _, err := r.db.SQL.ExecContext(ctx, `DELETE FROM "UpdateHistory"`); err != nil {
		return fmt.Errorf("updatehistory: purge: %w", err)
	}
	return nil
}
