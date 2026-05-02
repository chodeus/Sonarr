// Package remotepathmappings is the Go port of NzbDrone.Core.RemotePathMappings'
// RemotePathMapping table layer. The shape mirrors src/NzbDrone.Core/
// RemotePathMappings/RemotePathMapping.cs: { Id, Host, RemotePath, LocalPath }.
//
// The runtime translation logic (longest-prefix match across mappings,
// direction-aware substitution) lives in the service layer, not here.
package remotepathmappings

import (
	"errors"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/repo"
)

// Mapping is one persisted row.
type Mapping struct {
	ID         int    `json:"id"`
	Host       string `json:"host"`
	RemotePath string `json:"remotePath"`
	LocalPath  string `json:"localPath"`
}

// ErrNotFound is returned when no row matches.
var ErrNotFound = errors.New("remotepathmappings: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	*repo.Repository[Mapping]
}

var mapper = repo.Mapper[Mapping]{
	Name:    "remotepathmappings",
	Table:   "RemotePathMappings",
	Columns: []string{"Host", "RemotePath", "LocalPath"},
	OrderBy: "Id",
	Bind:    func(m *Mapping) []any { return []any{m.Host, m.RemotePath, m.LocalPath} },
	Scan: func(m *Mapping, s repo.RowScanner) error {
		return s.Scan(&m.ID, &m.Host, &m.RemotePath, &m.LocalPath)
	},
	GetID: func(m *Mapping) int { return m.ID },
	SetID: func(m *Mapping, id int) { m.ID = id },
}

// NewRepo returns a Repo bound to the given DB.
func NewRepo(db *datastore.DB) (*Repo, error) {
	r, err := repo.New(db, mapper, ErrNotFound)
	if err != nil {
		return nil, err
	}
	return &Repo{Repository: r}, nil
}
