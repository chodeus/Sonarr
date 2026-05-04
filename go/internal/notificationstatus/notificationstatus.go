// Package notificationstatus is the Go port of NzbDrone.Core.Notifications
// NotificationStatus persistence (added by migration 194). Each row records
// failure escalation state for a single notification provider so that
// repeatedly-failing providers are temporarily disabled rather than
// hammered.
//
// Schema at HEAD (v230): Id, ProviderId UNIQUE, InitialFailure?,
// MostRecentFailure?, EscalationLevel, DisabledTill?.
//
// The C# class is a thin subclass of ProviderStatusBase with no extra
// fields. DownloadClientStatus has the same shape.
//
// ProviderId references Notifications.Id but no FK is declared in the
// schema; cleanup is application-level.
package notificationstatus

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/repo"
)

// Status is one NotificationStatus row.
type Status struct {
	ID                int        `json:"id"`
	ProviderID        int        `json:"providerId"`
	InitialFailure    *time.Time `json:"initialFailure,omitempty"`
	MostRecentFailure *time.Time `json:"mostRecentFailure,omitempty"`
	EscalationLevel   int        `json:"escalationLevel"`
	DisabledTill      *time.Time `json:"disabledTill,omitempty"`
}

// IsDisabled reports whether the status indicates the provider is currently
// disabled — `DisabledTill` is set and is in the future. The clock is
// injected so callers can pin it for deterministic checks.
func (s *Status) IsDisabled(now time.Time) bool {
	return s.DisabledTill != nil && s.DisabledTill.After(now)
}

// ErrNotFound is returned when no row matches.
var ErrNotFound = errors.New("notificationstatus: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	*repo.Repository[Status]
}

var mapper = repo.Mapper[Status]{
	Name:    "notificationstatus",
	Table:   "NotificationStatus",
	Columns: []string{"ProviderId", "InitialFailure", "MostRecentFailure", "EscalationLevel", "DisabledTill"},
	OrderBy: "Id",
	Bind: func(s *Status) []any {
		return []any{
			s.ProviderID,
			nullableTime(s.InitialFailure),
			nullableTime(s.MostRecentFailure),
			s.EscalationLevel,
			nullableTime(s.DisabledTill),
		}
	},
	Scan: func(s *Status, sc repo.RowScanner) error {
		var initial, recent, disabled sql.NullTime
		if err := sc.Scan(&s.ID, &s.ProviderID, &initial, &recent, &s.EscalationLevel, &disabled); err != nil {
			return err
		}
		s.InitialFailure = unwrapTime(initial)
		s.MostRecentFailure = unwrapTime(recent)
		s.DisabledTill = unwrapTime(disabled)
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
// This is the canonical lookup the runtime uses to check IsDisabled before
// dispatching a notification.
func (r *Repo) GetByProviderID(ctx context.Context, providerID int) (*Status, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT "Id","ProviderId","InitialFailure","MostRecentFailure","EscalationLevel","DisabledTill" FROM "NotificationStatus" WHERE "ProviderId" = %s`,
		datastore.Placeholder(db.Engine, 1))
	var s Status
	var initial, recent, disabled sql.NullTime
	switch err := db.SQL.QueryRowContext(ctx, q, providerID).Scan(
		&s.ID, &s.ProviderID, &initial, &recent, &s.EscalationLevel, &disabled,
	); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("notificationstatus: getByProviderID(%d): %w", providerID, err)
	}
	s.InitialFailure = unwrapTime(initial)
	s.MostRecentFailure = unwrapTime(recent)
	s.DisabledTill = unwrapTime(disabled)
	return &s, nil
}

// Upsert inserts a new row when the Status carries no Id, or updates
// the existing row otherwise. Mirrors C# `BasicRepository.Upsert` at
// the call-sites `ProviderStatusServiceBase.cs:79` and `:136`. If a
// row for the ProviderId already exists but the caller's `s.ID` is
// 0, the existing Id is loaded and the row is updated — this matches
// the C# semantic where `FindByProviderId` is called immediately
// before Upsert and the returned model carries the persisted Id.
//
// Concurrent callers must serialise at the service layer; the C#
// version wraps the FindByProviderId/Upsert pair in a per-service
// lock. This repo helper does NOT serialise on its own.
func (r *Repo) Upsert(ctx context.Context, s *Status) error {
	if s == nil {
		return errors.New("notificationstatus: nil value")
	}
	if s.ID != 0 {
		return r.Update(ctx, s)
	}
	existing, err := r.GetByProviderID(ctx, s.ProviderID)
	switch {
	case errors.Is(err, ErrNotFound):
		_, err := r.Insert(ctx, s)
		return err
	case err != nil:
		return err
	}
	s.ID = existing.ID
	return r.Update(ctx, s)
}

// DeleteByProviderID removes the row matching the given ProviderId.
// No-op (no error) if no row matches, mirroring C#
// `ProviderStatusRepository.DeleteByProviderId` which delegates to
// `BasicRepository.Delete(predicate)` (silent on empty match).
func (r *Repo) DeleteByProviderID(ctx context.Context, providerID int) error {
	db := r.DB()
	q := fmt.Sprintf(
		`DELETE FROM "NotificationStatus" WHERE "ProviderId" = %s`,
		datastore.Placeholder(db.Engine, 1))
	if _, err := db.SQL.ExecContext(ctx, q, providerID); err != nil {
		return fmt.Errorf("notificationstatus: deleteByProviderID(%d): %w", providerID, err)
	}
	return nil
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
