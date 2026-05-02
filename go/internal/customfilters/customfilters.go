// Package customfilters is the Go port of NzbDrone.Core.CustomFilters
// CustomFilter persistence. Each row is one user-saved filter that the
// frontend stores against a section ("Type") with a human label ("Label")
// and a JSON-encoded filter blob ("Filters") interpreted entirely by the
// React app.
//
// Schema at HEAD (v230): Id, Type TEXT NOT NULL, Label TEXT NOT NULL,
// Filters TEXT NOT NULL.
//
// The Filters column carries opaque JSON; the repo treats it as a
// pass-through string. Validation lives at the API/UI boundary.
package customfilters

import (
	"errors"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/repo"
)

// Filter is one CustomFilters row.
type Filter struct {
	ID      int    `json:"id"`
	Type    string `json:"type"`
	Label   string `json:"label"`
	Filters string `json:"filters"`
}

// ErrNotFound is returned when no row matches.
var ErrNotFound = errors.New("customfilters: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	*repo.Repository[Filter]
}

var mapper = repo.Mapper[Filter]{
	Name:    "customfilters",
	Table:   "CustomFilters",
	Columns: []string{"Type", "Label", "Filters"},
	OrderBy: "Id",
	Bind:    func(f *Filter) []any { return []any{f.Type, f.Label, f.Filters} },
	Scan: func(f *Filter, s repo.RowScanner) error {
		return s.Scan(&f.ID, &f.Type, &f.Label, &f.Filters)
	},
	GetID: func(f *Filter) int { return f.ID },
	SetID: func(f *Filter, id int) { f.ID = id },
}

// NewRepo returns a Repo bound to the given DB.
func NewRepo(db *datastore.DB) (*Repo, error) {
	r, err := repo.New(db, mapper, ErrNotFound)
	if err != nil {
		return nil, err
	}
	return &Repo{Repository: r}, nil
}
