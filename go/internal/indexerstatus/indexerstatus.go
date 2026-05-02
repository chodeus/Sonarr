// Package indexerstatus is the Go port of NzbDrone.Core.Indexers
// IndexerStatus persistence (added by migration 091, column added by
// migration 091). Each row records failure escalation state for a
// single indexer provider plus the most recent RSS-sync release
// snapshot used by the per-indexer sync logic to detect "no new
// releases since last fetch".
//
// Schema at HEAD (v230): Id, ProviderId UNIQUE, InitialFailure?,
// MostRecentFailure?, EscalationLevel, DisabledTill?,
// LastRssSyncReleaseInfo TEXT (nullable, embedded JSON).
//
// LastRssSyncReleaseInfo holds an embedded ReleaseInfo document
// (Title, PublishDate, Size, DownloadUrl, ...). Until the ReleaseInfo
// model lands in Phase 2 (parser/indexer port), this package exposes
// the column as a raw JSON string. Callers wanting structured access
// will switch to a typed wrapper at that point — the column type
// itself does not change.
package indexerstatus

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/repo"
)

// Status is one IndexerStatus row.
//
// LastRssSyncReleaseInfo is the raw JSON encoding of the most recent
// RSS-sync release. Empty string represents the SQL NULL state. Phase 2
// will replace this with a ReleaseInfo struct when the parser/indexer
// port supplies one; the on-disk JSON shape stays the same.
type Status struct {
	ID                     int        `json:"id"`
	ProviderID             int        `json:"providerId"`
	InitialFailure         *time.Time `json:"initialFailure,omitempty"`
	MostRecentFailure      *time.Time `json:"mostRecentFailure,omitempty"`
	EscalationLevel        int        `json:"escalationLevel"`
	DisabledTill           *time.Time `json:"disabledTill,omitempty"`
	LastRssSyncReleaseInfo string     `json:"lastRssSyncReleaseInfo,omitempty"`
}

// IsDisabled mirrors ProviderStatusBase.IsDisabled().
func (s *Status) IsDisabled(now time.Time) bool {
	return s.DisabledTill != nil && s.DisabledTill.After(now)
}

// ErrNotFound is returned when no row matches.
var ErrNotFound = errors.New("indexerstatus: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	*repo.Repository[Status]
}

var mapper = repo.Mapper[Status]{
	Name:    "indexerstatus",
	Table:   "IndexerStatus",
	Columns: []string{"ProviderId", "InitialFailure", "MostRecentFailure", "EscalationLevel", "DisabledTill", "LastRssSyncReleaseInfo"},
	OrderBy: "Id",
	Bind: func(s *Status) []any {
		return []any{
			s.ProviderID,
			nullableTime(s.InitialFailure),
			nullableTime(s.MostRecentFailure),
			s.EscalationLevel,
			nullableTime(s.DisabledTill),
			nullableString(s.LastRssSyncReleaseInfo),
		}
	},
	Scan: func(s *Status, sc repo.RowScanner) error {
		var initial, recent, disabled sql.NullTime
		var lastRss sql.NullString
		if err := sc.Scan(&s.ID, &s.ProviderID, &initial, &recent, &s.EscalationLevel, &disabled, &lastRss); err != nil {
			return err
		}
		s.InitialFailure = unwrapTime(initial)
		s.MostRecentFailure = unwrapTime(recent)
		s.DisabledTill = unwrapTime(disabled)
		if lastRss.Valid {
			s.LastRssSyncReleaseInfo = lastRss.String
		}
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
		`SELECT "Id","ProviderId","InitialFailure","MostRecentFailure","EscalationLevel","DisabledTill","LastRssSyncReleaseInfo" FROM "IndexerStatus" WHERE "ProviderId" = %s`,
		datastore.Placeholder(db.Engine, 1))
	var s Status
	var initial, recent, disabled sql.NullTime
	var lastRss sql.NullString
	switch err := db.SQL.QueryRowContext(ctx, q, providerID).Scan(
		&s.ID, &s.ProviderID, &initial, &recent, &s.EscalationLevel, &disabled, &lastRss,
	); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("indexerstatus: getByProviderID(%d): %w", providerID, err)
	}
	s.InitialFailure = unwrapTime(initial)
	s.MostRecentFailure = unwrapTime(recent)
	s.DisabledTill = unwrapTime(disabled)
	if lastRss.Valid {
		s.LastRssSyncReleaseInfo = lastRss.String
	}
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

// nullableString persists "" as NULL. The column is nullable in the
// schema, so an empty Go string round-trips as NULL → empty string. If
// callers ever need to distinguish "" from NULL, switch to *string.
func nullableString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
