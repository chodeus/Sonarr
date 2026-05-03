// Package downloadclients is the Go port of
// NzbDrone.Core.Download.DownloadClientDefinition persistence — the
// second of the seven polymorphic-doc ThingiProvider tables. Each row
// is one configured download-client instance (a Sabnzbd host, a
// Transmission RPC endpoint, etc.) with the per-class settings stored
// as opaque JSON in the Settings column and the class identity stored
// as Implementation + ConfigContract C# class-name strings.
//
// # HEAD shape (10 columns + Id)
//
// Per analysis/schema/{sqlite,postgres}.sql at v230:
//
//	Id, Enable (bool NOT NULL), Name, Implementation,
//	Settings (NOT NULL), ConfigContract (NOT NULL),
//	Priority (default 1), RemoveCompletedDownloads (default true),
//	RemoveFailedDownloads (default true), Tags (nullable []int JSON)
//
// DownloadClientDefinition.cs additionally exposes the runtime-only
// Protocol field, which is computed from the configured class and not
// persisted in the row. It will be reattached at the service layer in
// Phase 4.
//
// See internal/indexers for the rationale behind the Settings/
// ConfigContract pass-through pattern; this repo follows the same
// shape.
package downloadclients

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/repo"
)

// DownloadClient is one persisted DownloadClients row.
type DownloadClient struct {
	ID                       int    `json:"id"`
	Enable                   bool   `json:"enable"`
	Name                     string `json:"name"`
	Implementation           string `json:"implementation"`
	Settings                 string `json:"settings"`
	ConfigContract           string `json:"configContract"`
	Priority                 int    `json:"priority"`
	RemoveCompletedDownloads bool   `json:"removeCompletedDownloads"`
	RemoveFailedDownloads    bool   `json:"removeFailedDownloads"`
	Tags                     []int  `json:"tags,omitempty"`
}

// ErrNotFound is returned when no row matches.
var ErrNotFound = errors.New("downloadclients: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	*repo.Repository[DownloadClient]
}

var mapper = repo.Mapper[DownloadClient]{
	Name:  "downloadclients",
	Table: "DownloadClients",
	Columns: []string{
		"Enable", "Name", "Implementation", "Settings", "ConfigContract",
		"Priority", "RemoveCompletedDownloads", "RemoveFailedDownloads", "Tags",
	},
	OrderBy: "Priority",
	Bind: func(c *DownloadClient) []any {
		return []any{
			c.Enable, c.Name, c.Implementation, c.Settings, c.ConfigContract,
			c.Priority, c.RemoveCompletedDownloads, c.RemoveFailedDownloads,
			nullableTagsJSON(c.Tags),
		}
	},
	Scan: func(c *DownloadClient, s repo.RowScanner) error {
		var tagsJSON sql.NullString
		if err := s.Scan(
			&c.ID, &c.Enable, &c.Name, &c.Implementation, &c.Settings,
			&c.ConfigContract, &c.Priority,
			&c.RemoveCompletedDownloads, &c.RemoveFailedDownloads, &tagsJSON,
		); err != nil {
			return err
		}
		if tagsJSON.Valid && tagsJSON.String != "" {
			if err := datastore.UnmarshalEmbedded(tagsJSON.String, &c.Tags); err != nil {
				return fmt.Errorf("unmarshal Tags: %w", err)
			}
		}
		return nil
	},
	GetID: func(c *DownloadClient) int { return c.ID },
	SetID: func(c *DownloadClient, id int) { c.ID = id },
}

// NewRepo returns a Repo bound to the given DB.
func NewRepo(db *datastore.DB) (*Repo, error) {
	r, err := repo.New(db, mapper, ErrNotFound)
	if err != nil {
		return nil, err
	}
	return &Repo{Repository: r}, nil
}

// FindByName returns the DownloadClient with the given Name or
// ErrNotFound. Mirrors the C# DownloadClientRepository's name lookup
// (Name is not UNIQUE in the schema but service-layer validation
// enforces uniqueness; this method returns the first match by Id).
func (r *Repo) FindByName(ctx context.Context, name string) (*DownloadClient, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT "Id","Enable","Name","Implementation","Settings","ConfigContract",`+
			`"Priority","RemoveCompletedDownloads","RemoveFailedDownloads","Tags" `+
			`FROM "DownloadClients" WHERE "Name" = %s ORDER BY "Id" LIMIT 1`,
		datastore.Placeholder(db.Engine, 1))
	var c DownloadClient
	if err := mapper.Scan(&c, db.SQL.QueryRowContext(ctx, q, name)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("downloadclients: findByName(%q): %w", name, err)
	}
	return &c, nil
}

// nullableTagsJSON: nil → NULL, non-nil → JSON. Same shape as the
// indexers helper of the same name — kept duplicated rather than
// hoisted because the empty-list-vs-NULL semantics differ across the
// seven provider tables and a shared helper would obscure that.
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
