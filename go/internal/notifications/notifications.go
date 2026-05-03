// Package notifications is the Go port of
// NzbDrone.Core.Notifications.NotificationDefinition persistence — the
// third polymorphic-doc ThingiProvider table. Each row is one
// configured notification target (a Discord webhook, an Apprise
// instance, etc.), with the per-class settings stored as opaque JSON
// in the Settings column and the class identity stored as
// Implementation + ConfigContract C# class-name strings.
//
// # HEAD shape (20 columns + Id)
//
// Per analysis/schema/{sqlite,postgres}.sql at v230. Notable shape:
// 13 OnX bool flags (one per notification trigger), of which OnUpgrade
// is the lone nullable bool (legacy — every other OnX defaults to
// false NOT NULL). ConfigContract is also nullable per the original v1
// migration; it became non-null at insert time only after later
// migrations added the column.
//
// NotificationDefinition.cs additionally exposes 13 SupportsOnX
// runtime-only fields populated from the configured class —
// reattached at the service layer in Phase 4.
//
// See internal/indexers for the rationale behind the Settings/
// ConfigContract pass-through pattern; this repo follows the same shape.
package notifications

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/repo"
)

// Notification is one persisted Notifications row.
type Notification struct {
	ID                            int     `json:"id"`
	Name                          string  `json:"name"`
	OnGrab                        bool    `json:"onGrab"`
	OnDownload                    bool    `json:"onDownload"`
	Settings                      string  `json:"settings"`
	Implementation                string  `json:"implementation"`
	ConfigContract                *string `json:"configContract,omitempty"`
	OnUpgrade                     *bool   `json:"onUpgrade,omitempty"`
	Tags                          []int   `json:"tags,omitempty"`
	OnRename                      bool    `json:"onRename"`
	OnHealthIssue                 bool    `json:"onHealthIssue"`
	IncludeHealthWarnings         bool    `json:"includeHealthWarnings"`
	OnSeriesDelete                bool    `json:"onSeriesDelete"`
	OnEpisodeFileDelete           bool    `json:"onEpisodeFileDelete"`
	OnEpisodeFileDeleteForUpgrade bool    `json:"onEpisodeFileDeleteForUpgrade"`
	OnApplicationUpdate           bool    `json:"onApplicationUpdate"`
	OnManualInteractionRequired   bool    `json:"onManualInteractionRequired"`
	OnSeriesAdd                   bool    `json:"onSeriesAdd"`
	OnHealthRestored              bool    `json:"onHealthRestored"`
	OnImportComplete              bool    `json:"onImportComplete"`
}

// ErrNotFound is returned when no row matches.
var ErrNotFound = errors.New("notifications: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	*repo.Repository[Notification]
}

var mapper = repo.Mapper[Notification]{
	Name:  "notifications",
	Table: "Notifications",
	Columns: []string{
		"Name", "OnGrab", "OnDownload", "Settings", "Implementation",
		"ConfigContract", "OnUpgrade", "Tags", "OnRename",
		"OnHealthIssue", "IncludeHealthWarnings",
		"OnSeriesDelete", "OnEpisodeFileDelete",
		"OnEpisodeFileDeleteForUpgrade", "OnApplicationUpdate",
		"OnManualInteractionRequired", "OnSeriesAdd",
		"OnHealthRestored", "OnImportComplete",
	},
	OrderBy: "Id",
	Bind: func(n *Notification) []any {
		return []any{
			n.Name, n.OnGrab, n.OnDownload, n.Settings, n.Implementation,
			nullableString(n.ConfigContract),
			nullableBool(n.OnUpgrade),
			nullableTagsJSON(n.Tags),
			n.OnRename,
			n.OnHealthIssue, n.IncludeHealthWarnings,
			n.OnSeriesDelete, n.OnEpisodeFileDelete,
			n.OnEpisodeFileDeleteForUpgrade, n.OnApplicationUpdate,
			n.OnManualInteractionRequired, n.OnSeriesAdd,
			n.OnHealthRestored, n.OnImportComplete,
		}
	},
	Scan: func(n *Notification, s repo.RowScanner) error {
		var configContract, tagsJSON sql.NullString
		var onUpgrade sql.NullBool
		if err := s.Scan(
			&n.ID, &n.Name, &n.OnGrab, &n.OnDownload, &n.Settings,
			&n.Implementation, &configContract, &onUpgrade, &tagsJSON,
			&n.OnRename, &n.OnHealthIssue, &n.IncludeHealthWarnings,
			&n.OnSeriesDelete, &n.OnEpisodeFileDelete,
			&n.OnEpisodeFileDeleteForUpgrade, &n.OnApplicationUpdate,
			&n.OnManualInteractionRequired, &n.OnSeriesAdd,
			&n.OnHealthRestored, &n.OnImportComplete,
		); err != nil {
			return err
		}
		n.ConfigContract = unwrapString(configContract)
		n.OnUpgrade = unwrapBool(onUpgrade)
		if tagsJSON.Valid && tagsJSON.String != "" {
			if err := datastore.UnmarshalEmbedded(tagsJSON.String, &n.Tags); err != nil {
				return fmt.Errorf("unmarshal Tags: %w", err)
			}
		}
		return nil
	},
	GetID: func(n *Notification) int { return n.ID },
	SetID: func(n *Notification, id int) { n.ID = id },
}

// NewRepo returns a Repo bound to the given DB.
func NewRepo(db *datastore.DB) (*Repo, error) {
	r, err := repo.New(db, mapper, ErrNotFound)
	if err != nil {
		return nil, err
	}
	return &Repo{Repository: r}, nil
}

// FindByName returns the Notification with the given Name or
// ErrNotFound. Name is not UNIQUE in the schema; this returns the first
// match by Id.
func (r *Repo) FindByName(ctx context.Context, name string) (*Notification, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT "Id","Name","OnGrab","OnDownload","Settings","Implementation",`+
			`"ConfigContract","OnUpgrade","Tags","OnRename",`+
			`"OnHealthIssue","IncludeHealthWarnings",`+
			`"OnSeriesDelete","OnEpisodeFileDelete",`+
			`"OnEpisodeFileDeleteForUpgrade","OnApplicationUpdate",`+
			`"OnManualInteractionRequired","OnSeriesAdd",`+
			`"OnHealthRestored","OnImportComplete" `+
			`FROM "Notifications" WHERE "Name" = %s ORDER BY "Id" LIMIT 1`,
		datastore.Placeholder(db.Engine, 1))
	var n Notification
	if err := mapper.Scan(&n, db.SQL.QueryRowContext(ctx, q, name)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("notifications: findByName(%q): %w", name, err)
	}
	return &n, nil
}

func nullableString(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}

func unwrapString(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	v := n.String
	return &v
}

func nullableBool(b *bool) sql.NullBool {
	if b == nil {
		return sql.NullBool{}
	}
	return sql.NullBool{Bool: *b, Valid: true}
}

func unwrapBool(n sql.NullBool) *bool {
	if !n.Valid {
		return nil
	}
	v := n.Bool
	return &v
}

func nullableTagsJSON(xs []int) sql.NullString {
	if xs == nil {
		return sql.NullString{}
	}
	out, err := datastore.MarshalEmbedded(xs)
	if err != nil {
		return sql.NullString{String: `<marshal-error: ` + err.Error() + `>`, Valid: true}
	}
	return sql.NullString{String: out, Valid: true}
}
