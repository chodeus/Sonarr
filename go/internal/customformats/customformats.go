// Package customformats is the Go port of
// NzbDrone.Core.CustomFormats.CustomFormat persistence — the sixth of
// the seven ThingiProvider tables. Unlike the five row-shaped
// provider tables (Indexers/DownloadClients/Notifications/ImportLists/
// Metadata), CustomFormats has no Implementation/ConfigContract
// columns: each row carries a `Specifications` JSON envelope that
// itself contains a polymorphic list of spec discriminator/body pairs.
//
// # HEAD shape (3 columns + Id)
//
// Per analysis/schema/{sqlite,postgres}.sql at v230:
//
//	Id, Name UNIQUE,
//	Specifications text DEFAULT '[]' NOT NULL,
//	IncludeCustomFormatWhenRenaming bool DEFAULT false NOT NULL
//
// # Specifications envelope
//
// Per analysis/embedded-docs/catalogue.md § CustomFormatSpecificationConverter,
// the Specifications column is a JSON array of `{type, body}` envelope
// elements. The discriminator strings are catalogued in
// internal/provider.FamilyCustomFormatSpec (8 entries at HEAD —
// SeasonPackSpecification was renamed to ReleaseTypeSpecification in
// migration 205). Service-layer code in Phase 4 will use that
// catalogue to typed-deserialise; the storage layer treats the
// payload as opaque text so byte-for-byte round-trip with existing
// user databases is preserved.
package customformats

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/repo"
)

// CustomFormat is one persisted CustomFormats row.
type CustomFormat struct {
	ID                              int    `json:"id"`
	Name                            string `json:"name"`
	Specifications                  string `json:"specifications"`
	IncludeCustomFormatWhenRenaming bool   `json:"includeCustomFormatWhenRenaming"`
}

// ErrNotFound is returned when no row matches.
var ErrNotFound = errors.New("customformats: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	*repo.Repository[CustomFormat]
}

var mapper = repo.Mapper[CustomFormat]{
	Name:    "customformats",
	Table:   "CustomFormats",
	Columns: []string{"Name", "Specifications", "IncludeCustomFormatWhenRenaming"},
	OrderBy: "Id",
	Bind: func(c *CustomFormat) []any {
		spec := c.Specifications
		if spec == "" {
			spec = "[]" // schema default; persist [] so reads see [] not "".
		}
		return []any{c.Name, spec, c.IncludeCustomFormatWhenRenaming}
	},
	Scan: func(c *CustomFormat, s repo.RowScanner) error {
		return s.Scan(&c.ID, &c.Name, &c.Specifications, &c.IncludeCustomFormatWhenRenaming)
	},
	GetID: func(c *CustomFormat) int { return c.ID },
	SetID: func(c *CustomFormat, id int) { c.ID = id },
}

// NewRepo returns a Repo bound to the given DB.
func NewRepo(db *datastore.DB) (*Repo, error) {
	r, err := repo.New(db, mapper, ErrNotFound)
	if err != nil {
		return nil, err
	}
	return &Repo{Repository: r}, nil
}

// FindByName returns the CustomFormat with the given Name or ErrNotFound.
// Name is UNIQUE in the schema.
func (r *Repo) FindByName(ctx context.Context, name string) (*CustomFormat, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT "Id","Name","Specifications","IncludeCustomFormatWhenRenaming" `+
			`FROM "CustomFormats" WHERE "Name" = %s`,
		datastore.Placeholder(db.Engine, 1))
	var c CustomFormat
	if err := mapper.Scan(&c, db.SQL.QueryRowContext(ctx, q, name)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("customformats: findByName(%q): %w", name, err)
	}
	return &c, nil
}
