// Package autotagging is the Go port of
// NzbDrone.Core.AutoTagging.AutoTag persistence — the seventh and last
// ThingiProvider table. Like CustomFormats, AutoTagging uses an
// envelope-shaped Specifications column rather than per-row
// Implementation/ConfigContract columns.
//
// # HEAD shape (4 columns + Id)
//
// Per analysis/schema/{sqlite,postgres}.sql at v230:
//
//	Id, Name UNIQUE,
//	Specifications text DEFAULT '[]' NOT NULL,
//	RemoveTagsAutomatically bool DEFAULT false NOT NULL,
//	Tags text DEFAULT '[]' NOT NULL  -- []int JSON
//
// Note that AutoTagging.Tags is NOT NULL with a `[]` default — unlike
// the optional Tags columns on Indexers/DownloadClients/Notifications/
// ImportLists. Nil slice still persists as `[]` to satisfy the
// constraint.
//
// # Specifications envelope
//
// Per analysis/embedded-docs/catalogue.md § AutoTagSpecificationConverter,
// the Specifications column is a JSON array of `{type, body}` envelope
// elements drawn from the 11 known spec types catalogued in
// internal/provider.FamilyAutoTaggingSpec. As with CustomFormats, the
// storage layer treats the payload as opaque text; Phase 4 service
// code typed-deserialises via the registry.
package autotagging

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/repo"
)

// AutoTag is one persisted AutoTagging row.
type AutoTag struct {
	ID                      int    `json:"id"`
	Name                    string `json:"name"`
	Specifications          string `json:"specifications"`
	RemoveTagsAutomatically bool   `json:"removeTagsAutomatically"`
	Tags                    []int  `json:"tags"`
}

// ErrNotFound is returned when no row matches.
var ErrNotFound = errors.New("autotagging: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	*repo.Repository[AutoTag]
}

var mapper = repo.Mapper[AutoTag]{
	Name:    "autotagging",
	Table:   "AutoTagging",
	Columns: []string{"Name", "Specifications", "RemoveTagsAutomatically", "Tags"},
	OrderBy: "Id",
	Bind: func(a *AutoTag) []any {
		spec := a.Specifications
		if spec == "" {
			spec = "[]"
		}
		// Tags is NOT NULL with default '[]'; nil → "[]".
		tags := a.Tags
		if tags == nil {
			tags = []int{}
		}
		tagsJSON, err := datastore.MarshalEmbedded(tags)
		if err != nil {
			tagsJSON = `<marshal-error: ` + err.Error() + `>`
		}
		return []any{a.Name, spec, a.RemoveTagsAutomatically, tagsJSON}
	},
	Scan: func(a *AutoTag, s repo.RowScanner) error {
		var tagsJSON string
		if err := s.Scan(&a.ID, &a.Name, &a.Specifications, &a.RemoveTagsAutomatically, &tagsJSON); err != nil {
			return err
		}
		if err := datastore.UnmarshalEmbedded(tagsJSON, &a.Tags); err != nil {
			return fmt.Errorf("unmarshal Tags: %w", err)
		}
		return nil
	},
	GetID: func(a *AutoTag) int { return a.ID },
	SetID: func(a *AutoTag, id int) { a.ID = id },
}

// NewRepo returns a Repo bound to the given DB.
func NewRepo(db *datastore.DB) (*Repo, error) {
	r, err := repo.New(db, mapper, ErrNotFound)
	if err != nil {
		return nil, err
	}
	return &Repo{Repository: r}, nil
}

// FindByName returns the AutoTag with the given Name or ErrNotFound.
// Name is UNIQUE in the schema.
func (r *Repo) FindByName(ctx context.Context, name string) (*AutoTag, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT "Id","Name","Specifications","RemoveTagsAutomatically","Tags" `+
			`FROM "AutoTagging" WHERE "Name" = %s`,
		datastore.Placeholder(db.Engine, 1))
	var a AutoTag
	if err := mapper.Scan(&a, db.SQL.QueryRowContext(ctx, q, name)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("autotagging: findByName(%q): %w", name, err)
	}
	return &a, nil
}
