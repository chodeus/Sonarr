// Package importlists is the Go port of
// NzbDrone.Core.ImportLists.ImportListDefinition persistence — the
// fourth polymorphic-doc ThingiProvider table. Each row is one
// configured import list (a Plex watchlist, a Trakt user list, etc.)
// with the per-class settings stored as opaque JSON in the Settings
// column and the class identity stored as Implementation +
// ConfigContract C# class-name strings.
//
// # HEAD shape (13 columns + Id)
//
// Per analysis/schema/{sqlite,postgres}.sql at v230:
//
//	Id, Name UNIQUE, Implementation,
//	Settings (nullable), ConfigContract (nullable),
//	EnableAutomaticAdd (nullable bool), RootFolderPath,
//	ShouldMonitor (int — MonitorTypes enum),
//	QualityProfileId, Tags (nullable []int JSON),
//	SeriesType (int default 0), SeasonFolder (default true),
//	SearchForMissingEpisodes (default true),
//	MonitorNewItems (int default 0)
//
// ImportListDefinition.cs additionally exposes runtime-only
// Status / ListType / MinRefreshInterval — reattached at the service
// layer in Phase 4.
//
// See internal/indexers for the rationale behind the Settings/
// ConfigContract pass-through pattern; this repo follows the same shape.
package importlists

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/repo"
)

// ImportList is one persisted ImportLists row.
type ImportList struct {
	ID                       int     `json:"id"`
	Name                     string  `json:"name"`
	Implementation           string  `json:"implementation"`
	Settings                 *string `json:"settings,omitempty"`
	ConfigContract           *string `json:"configContract,omitempty"`
	EnableAutomaticAdd       *bool   `json:"enableAutomaticAdd,omitempty"`
	RootFolderPath           string  `json:"rootFolderPath"`
	ShouldMonitor            int     `json:"shouldMonitor"`
	QualityProfileID         int     `json:"qualityProfileId"`
	Tags                     []int   `json:"tags,omitempty"`
	SeriesType               int     `json:"seriesType"`
	SeasonFolder             bool    `json:"seasonFolder"`
	SearchForMissingEpisodes bool    `json:"searchForMissingEpisodes"`
	MonitorNewItems          int     `json:"monitorNewItems"`
}

// ErrNotFound is returned when no row matches.
var ErrNotFound = errors.New("importlists: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	*repo.Repository[ImportList]
}

var mapper = repo.Mapper[ImportList]{
	Name:  "importlists",
	Table: "ImportLists",
	Columns: []string{
		"Name", "Implementation", "Settings", "ConfigContract",
		"EnableAutomaticAdd", "RootFolderPath", "ShouldMonitor",
		"QualityProfileId", "Tags", "SeriesType", "SeasonFolder",
		"SearchForMissingEpisodes", "MonitorNewItems",
	},
	OrderBy: "Id",
	Bind: func(l *ImportList) []any {
		return []any{
			l.Name, l.Implementation,
			nullableString(l.Settings), nullableString(l.ConfigContract),
			nullableBool(l.EnableAutomaticAdd),
			l.RootFolderPath, l.ShouldMonitor, l.QualityProfileID,
			nullableTagsJSON(l.Tags), l.SeriesType, l.SeasonFolder,
			l.SearchForMissingEpisodes, l.MonitorNewItems,
		}
	},
	Scan: func(l *ImportList, s repo.RowScanner) error {
		var settings, configContract, tagsJSON sql.NullString
		var enableAuto sql.NullBool
		if err := s.Scan(
			&l.ID, &l.Name, &l.Implementation,
			&settings, &configContract, &enableAuto,
			&l.RootFolderPath, &l.ShouldMonitor, &l.QualityProfileID,
			&tagsJSON, &l.SeriesType, &l.SeasonFolder,
			&l.SearchForMissingEpisodes, &l.MonitorNewItems,
		); err != nil {
			return err
		}
		l.Settings = unwrapString(settings)
		l.ConfigContract = unwrapString(configContract)
		l.EnableAutomaticAdd = unwrapBool(enableAuto)
		if tagsJSON.Valid && tagsJSON.String != "" {
			if err := datastore.UnmarshalEmbedded(tagsJSON.String, &l.Tags); err != nil {
				return fmt.Errorf("unmarshal Tags: %w", err)
			}
		}
		return nil
	},
	GetID: func(l *ImportList) int { return l.ID },
	SetID: func(l *ImportList, id int) { l.ID = id },
}

// NewRepo returns a Repo bound to the given DB.
func NewRepo(db *datastore.DB) (*Repo, error) {
	r, err := repo.New(db, mapper, ErrNotFound)
	if err != nil {
		return nil, err
	}
	return &Repo{Repository: r}, nil
}

// FindByName returns the ImportList with the given Name or ErrNotFound.
// Name is UNIQUE in the schema.
func (r *Repo) FindByName(ctx context.Context, name string) (*ImportList, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT "Id","Name","Implementation","Settings","ConfigContract",`+
			`"EnableAutomaticAdd","RootFolderPath","ShouldMonitor",`+
			`"QualityProfileId","Tags","SeriesType","SeasonFolder",`+
			`"SearchForMissingEpisodes","MonitorNewItems" `+
			`FROM "ImportLists" WHERE "Name" = %s`,
		datastore.Placeholder(db.Engine, 1))
	var l ImportList
	if err := mapper.Scan(&l, db.SQL.QueryRowContext(ctx, q, name)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("importlists: findByName(%q): %w", name, err)
	}
	return &l, nil
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
