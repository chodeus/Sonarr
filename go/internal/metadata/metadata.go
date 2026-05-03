// Package metadata is the Go port of
// NzbDrone.Core.Extras.Metadata.MetadataDefinition persistence — the
// fifth polymorphic-doc ThingiProvider table. Each row configures one
// metadata writer (Kodi/Emby/Jellyfin XBMC, Plex, Roksbox, WDTV) that
// emits sidecar files alongside imported episodes.
//
// # HEAD shape (5 columns + Id)
//
// Per analysis/schema/{sqlite,postgres}.sql at v230:
//
//	Id, Enable (bool NOT NULL), Name, Implementation,
//	Settings (NOT NULL), ConfigContract (NOT NULL)
//
// The smallest of the seven provider tables — there is no per-row Tags
// column, no event toggles. MetadataDefinition.cs is itself empty
// (just inherits ProviderDefinition).
//
// See internal/indexers for the rationale behind the Settings/
// ConfigContract pass-through pattern; this repo follows the same shape.
package metadata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/repo"
)

// Metadata is one persisted Metadata row.
type Metadata struct {
	ID             int    `json:"id"`
	Enable         bool   `json:"enable"`
	Name           string `json:"name"`
	Implementation string `json:"implementation"`
	Settings       string `json:"settings"`
	ConfigContract string `json:"configContract"`
}

// ErrNotFound is returned when no row matches.
var ErrNotFound = errors.New("metadata: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	*repo.Repository[Metadata]
}

var mapper = repo.Mapper[Metadata]{
	Name:    "metadata",
	Table:   "Metadata",
	Columns: []string{"Enable", "Name", "Implementation", "Settings", "ConfigContract"},
	OrderBy: "Id",
	Bind: func(m *Metadata) []any {
		return []any{m.Enable, m.Name, m.Implementation, m.Settings, m.ConfigContract}
	},
	Scan: func(m *Metadata, s repo.RowScanner) error {
		return s.Scan(&m.ID, &m.Enable, &m.Name, &m.Implementation, &m.Settings, &m.ConfigContract)
	},
	GetID: func(m *Metadata) int { return m.ID },
	SetID: func(m *Metadata, id int) { m.ID = id },
}

// NewRepo returns a Repo bound to the given DB.
func NewRepo(db *datastore.DB) (*Repo, error) {
	r, err := repo.New(db, mapper, ErrNotFound)
	if err != nil {
		return nil, err
	}
	return &Repo{Repository: r}, nil
}

// FindByName returns the Metadata row with the given Name or
// ErrNotFound. Name is not UNIQUE in the schema.
func (r *Repo) FindByName(ctx context.Context, name string) (*Metadata, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT "Id","Enable","Name","Implementation","Settings","ConfigContract" `+
			`FROM "Metadata" WHERE "Name" = %s ORDER BY "Id" LIMIT 1`,
		datastore.Placeholder(db.Engine, 1))
	var m Metadata
	if err := mapper.Scan(&m, db.SQL.QueryRowContext(ctx, q, name)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("metadata: findByName(%q): %w", name, err)
	}
	return &m, nil
}
