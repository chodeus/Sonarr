// Package importliststatus is the Go port of NzbDrone.Core.ImportLists
// ImportListStatus persistence. Each row records failure escalation state
// for a single import-list provider, plus two import-list-specific fields
// not on the shared ProviderStatusBase: LastInfoSync (nullable timestamp
// of the most recent successful sync) and HasRemovedItemSinceLastClean
// (a flag the cleanup job consults to know whether to scan).
//
// Schema at HEAD (v230): Id, ProviderId UNIQUE, InitialFailure?,
// MostRecentFailure?, EscalationLevel, DisabledTill?, LastInfoSync?,
// HasRemovedItemSinceLastClean BOOL NOT NULL DEFAULT 0.
package importliststatus

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/repo"
)

// Status is one ImportListStatus row.
type Status struct {
	ID                           int        `json:"id"`
	ProviderID                   int        `json:"providerId"`
	InitialFailure               *time.Time `json:"initialFailure,omitempty"`
	MostRecentFailure            *time.Time `json:"mostRecentFailure,omitempty"`
	EscalationLevel              int        `json:"escalationLevel"`
	DisabledTill                 *time.Time `json:"disabledTill,omitempty"`
	LastInfoSync                 *time.Time `json:"lastInfoSync,omitempty"`
	HasRemovedItemSinceLastClean bool       `json:"hasRemovedItemSinceLastClean"`
}

// IsDisabled mirrors ProviderStatusBase.IsDisabled().
func (s *Status) IsDisabled(now time.Time) bool {
	return s.DisabledTill != nil && s.DisabledTill.After(now)
}

// ErrNotFound is returned when no row matches.
var ErrNotFound = errors.New("importliststatus: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	*repo.Repository[Status]
}

var mapper = repo.Mapper[Status]{
	Name:    "importliststatus",
	Table:   "ImportListStatus",
	Columns: []string{"ProviderId", "InitialFailure", "MostRecentFailure", "EscalationLevel", "DisabledTill", "LastInfoSync", "HasRemovedItemSinceLastClean"},
	OrderBy: "Id",
	Bind: func(s *Status) []any {
		return []any{
			s.ProviderID,
			nullableTime(s.InitialFailure),
			nullableTime(s.MostRecentFailure),
			s.EscalationLevel,
			nullableTime(s.DisabledTill),
			nullableTime(s.LastInfoSync),
			s.HasRemovedItemSinceLastClean,
		}
	},
	Scan: func(s *Status, sc repo.RowScanner) error {
		var initial, recent, disabled, lastInfo sql.NullTime
		if err := sc.Scan(&s.ID, &s.ProviderID, &initial, &recent, &s.EscalationLevel, &disabled, &lastInfo, &s.HasRemovedItemSinceLastClean); err != nil {
			return err
		}
		s.InitialFailure = unwrapTime(initial)
		s.MostRecentFailure = unwrapTime(recent)
		s.DisabledTill = unwrapTime(disabled)
		s.LastInfoSync = unwrapTime(lastInfo)
		return nil
	},
	GetID: func(s *Status) int { return s.ID },
	SetID: func(s *Status, id int) { s.ID = id },
}

// NewRepo returns a Repo bound to the given DB.
func NewRepo(db *datastore.DB) (*Repo, error) {
	r, err := repo.New(db, mapper, ErrNotFound)
	if err != nil {
		return nil, err
	}
	return &Repo{Repository: r}, nil
}

// GetByProviderID returns the Status for the given ProviderId or ErrNotFound.
func (r *Repo) GetByProviderID(ctx context.Context, providerID int) (*Status, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT "Id","ProviderId","InitialFailure","MostRecentFailure","EscalationLevel","DisabledTill","LastInfoSync","HasRemovedItemSinceLastClean" FROM "ImportListStatus" WHERE "ProviderId" = %s`,
		datastore.Placeholder(db.Engine, 1))
	var s Status
	var initial, recent, disabled, lastInfo sql.NullTime
	switch err := db.SQL.QueryRowContext(ctx, q, providerID).Scan(
		&s.ID, &s.ProviderID, &initial, &recent, &s.EscalationLevel, &disabled, &lastInfo, &s.HasRemovedItemSinceLastClean,
	); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("importliststatus: getByProviderID(%d): %w", providerID, err)
	}
	s.InitialFailure = unwrapTime(initial)
	s.MostRecentFailure = unwrapTime(recent)
	s.DisabledTill = unwrapTime(disabled)
	s.LastInfoSync = unwrapTime(lastInfo)
	return &s, nil
}

func nullableTime(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t.UTC(), Valid: true}
}

func unwrapTime(n sql.NullTime) *time.Time {
	if !n.Valid {
		return nil
	}
	v := n.Time.UTC()
	return &v
}
