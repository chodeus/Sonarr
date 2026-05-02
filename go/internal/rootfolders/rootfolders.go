// Package rootfolders is the Go port of NzbDrone.Core.RootFolders' RootFolder
// table layer. The shape mirrors src/NzbDrone.Core/RootFolders/RootFolder.cs:
// { Id, Path } with Path UNIQUE.
//
// The C# RootFolder type also has a non-persisted Accessible bool and
// FreeSpace long that the service computes at read time from the live
// filesystem. Those live in the service layer (yet to be ported).
package rootfolders

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/repo"
)

// RootFolder is one persisted row.
type RootFolder struct {
	ID   int    `json:"id"`
	Path string `json:"path"`
}

// ErrNotFound is returned when no row matches.
var ErrNotFound = errors.New("rootfolders: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	*repo.Repository[RootFolder]
}

var mapper = repo.Mapper[RootFolder]{
	Name:    "rootfolders",
	Table:   "RootFolders",
	Columns: []string{"Path"},
	OrderBy: "Id",
	Bind:    func(rf *RootFolder) []any { return []any{rf.Path} },
	Scan:    func(rf *RootFolder, s repo.RowScanner) error { return s.Scan(&rf.ID, &rf.Path) },
	GetID:   func(rf *RootFolder) int { return rf.ID },
	SetID:   func(rf *RootFolder, id int) { rf.ID = id },
}

// NewRepo returns a Repo bound to the given DB.
func NewRepo(db *datastore.DB) (*Repo, error) {
	r, err := repo.New(db, mapper, ErrNotFound)
	if err != nil {
		return nil, err
	}
	return &Repo{Repository: r}, nil
}

// GetByPath returns the RootFolder whose Path matches exactly, or
// ErrNotFound. Path is UNIQUE so at most one row matches.
func (r *Repo) GetByPath(ctx context.Context, path string) (*RootFolder, error) {
	db := r.DB()
	q := fmt.Sprintf(`SELECT "Id","Path" FROM "RootFolders" WHERE "Path" = %s`,
		datastore.Placeholder(db.Engine, 1))
	var rf RootFolder
	switch err := db.SQL.QueryRowContext(ctx, q, path).Scan(&rf.ID, &rf.Path); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("rootfolders: getByPath(%q): %w", path, err)
	}
	return &rf, nil
}
