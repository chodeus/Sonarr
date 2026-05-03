// Package indexers is the Go port of NzbDrone.Core.Indexers.IndexerDefinition
// persistence. The Indexers table is one of the seven polymorphic-doc
// "ThingiProvider" tables: each row is one configured indexer
// instance (a Newznab host, a Torznab feed, etc.), with the per-class
// settings stored as a JSON blob in the Settings TEXT column and the
// class identity stored as Implementation + ConfigContract C# class-
// name strings.
//
// # HEAD shape (12 columns + Id)
//
// Per analysis/schema/{sqlite,postgres}.sql at v230:
//
//	Id, Name UNIQUE, Implementation, Settings (nullable),
//	ConfigContract (nullable), EnableRss (nullable bool),
//	EnableAutomaticSearch (nullable bool),
//	EnableInteractiveSearch (NOT NULL bool),
//	Priority (default 25), Tags (nullable []int JSON),
//	DownloadClientId (default 0),
//	SeasonSearchMaximumSingleEpisodeAge (default 0)
//
// IndexerDefinition.cs additionally exposes Protocol/SupportsRss/
// SupportsSearch/Status — those are computed at runtime from the
// configured class, not persisted in the row, and so don't appear
// here. They will be reattached at the service layer in Phase 4.
//
// # Settings + ConfigContract pass-through
//
// The TP-Q1/Q2 resolution (see internal/provider) keeps Implementation
// and ConfigContract as the C# class-name strings verbatim. This repo
// stores Settings as a raw `*string` so byte-for-byte round-trip with
// existing C# user databases is preserved. Phase 4 service code will
// look up the typed deserialiser via provider.Default().Lookup(
// FamilyIndexer, Key{Impl, Contract}) when it actually needs the
// Settings struct. The repo deliberately does not validate that
// Implementation/ConfigContract is registered — RemoveMissingImplementations
// semantics live one layer up.
package indexers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/repo"
)

// Indexer is one persisted Indexers row.
type Indexer struct {
	ID                                  int     `json:"id"`
	Name                                string  `json:"name"`
	Implementation                      string  `json:"implementation"`
	Settings                            *string `json:"settings,omitempty"`
	ConfigContract                      *string `json:"configContract,omitempty"`
	EnableRss                           *bool   `json:"enableRss,omitempty"`
	EnableAutomaticSearch               *bool   `json:"enableAutomaticSearch,omitempty"`
	EnableInteractiveSearch             bool    `json:"enableInteractiveSearch"`
	Priority                            int     `json:"priority"`
	Tags                                []int   `json:"tags,omitempty"`
	DownloadClientID                    int     `json:"downloadClientId"`
	SeasonSearchMaximumSingleEpisodeAge int     `json:"seasonSearchMaximumSingleEpisodeAge"`
}

// ErrNotFound is returned when no row matches.
var ErrNotFound = errors.New("indexers: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	*repo.Repository[Indexer]
}

var mapper = repo.Mapper[Indexer]{
	Name:  "indexers",
	Table: "Indexers",
	Columns: []string{
		"Name", "Implementation", "Settings", "ConfigContract",
		"EnableRss", "EnableAutomaticSearch", "EnableInteractiveSearch",
		"Priority", "Tags", "DownloadClientId",
		"SeasonSearchMaximumSingleEpisodeAge",
	},
	OrderBy: "Id",
	Bind: func(i *Indexer) []any {
		return []any{
			i.Name,
			i.Implementation,
			nullableString(i.Settings),
			nullableString(i.ConfigContract),
			nullableBool(i.EnableRss),
			nullableBool(i.EnableAutomaticSearch),
			i.EnableInteractiveSearch,
			i.Priority,
			nullableTagsJSON(i.Tags),
			i.DownloadClientID,
			i.SeasonSearchMaximumSingleEpisodeAge,
		}
	},
	Scan: func(i *Indexer, s repo.RowScanner) error {
		var settings, configContract, tagsJSON sql.NullString
		var enableRss, enableAutoSearch sql.NullBool
		if err := s.Scan(
			&i.ID, &i.Name, &i.Implementation,
			&settings, &configContract,
			&enableRss, &enableAutoSearch, &i.EnableInteractiveSearch,
			&i.Priority, &tagsJSON, &i.DownloadClientID,
			&i.SeasonSearchMaximumSingleEpisodeAge,
		); err != nil {
			return err
		}
		i.Settings = unwrapString(settings)
		i.ConfigContract = unwrapString(configContract)
		i.EnableRss = unwrapBool(enableRss)
		i.EnableAutomaticSearch = unwrapBool(enableAutoSearch)
		if tagsJSON.Valid && tagsJSON.String != "" {
			if err := datastore.UnmarshalEmbedded(tagsJSON.String, &i.Tags); err != nil {
				return fmt.Errorf("unmarshal Tags: %w", err)
			}
		}
		return nil
	},
	GetID: func(i *Indexer) int { return i.ID },
	SetID: func(i *Indexer, id int) { i.ID = id },
}

// NewRepo returns a Repo bound to the given DB.
func NewRepo(db *datastore.DB) (*Repo, error) {
	r, err := repo.New(db, mapper, ErrNotFound)
	if err != nil {
		return nil, err
	}
	return &Repo{Repository: r}, nil
}

// FindByName returns the Indexer with the given Name or ErrNotFound.
// Mirrors C# IIndexerRepository.FindByName (`IndexerRepository.cs:21`).
// Name is UNIQUE in the schema.
func (r *Repo) FindByName(ctx context.Context, name string) (*Indexer, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT "Id","Name","Implementation","Settings","ConfigContract",`+
			`"EnableRss","EnableAutomaticSearch","EnableInteractiveSearch",`+
			`"Priority","Tags","DownloadClientId",`+
			`"SeasonSearchMaximumSingleEpisodeAge" FROM "Indexers" WHERE "Name" = %s`,
		datastore.Placeholder(db.Engine, 1))
	var i Indexer
	if err := mapper.Scan(&i, db.SQL.QueryRowContext(ctx, q, name)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("indexers: findByName(%q): %w", name, err)
	}
	return &i, nil
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

// nullableTagsJSON returns NULL for nil Tags (the schema permits NULL)
// and a JSON-encoded "[]" / "[1,2,3]" otherwise. An empty (non-nil)
// slice persists as "[]" so the on-disk representation matches what
// callers see when they iterate.
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
