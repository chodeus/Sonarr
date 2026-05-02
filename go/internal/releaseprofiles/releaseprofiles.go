// Package releaseprofiles is the Go port of NzbDrone.Core.Profiles.Releases
// ReleaseProfile persistence. A ReleaseProfile defines required/ignored
// term lists and indexer/tag scoping that the decision engine consults
// when evaluating releases.
//
// Schema at HEAD (v230): Id, Required nullable, Ignored nullable,
// Tags NOT NULL, Enabled BOOL NOT NULL DEFAULT true, Name nullable,
// ExcludedTags NOT NULL DEFAULT '[]', IndexerIds NOT NULL DEFAULT '[]',
// AirDateRestriction BOOL NOT NULL DEFAULT false,
// AirDateGracePeriod INT NOT NULL DEFAULT 0.
//
// Five columns hold JSON-encoded collections: Required + Ignored carry
// `[]string`, Tags + ExcludedTags + IndexerIds carry `[]int`. The Bind/
// Scan funcs marshal/unmarshal at the SQL boundary via
// datastore.MarshalEmbedded / UnmarshalEmbedded.
//
// Required and Ignored are nullable in the schema but the C# class
// initialises them to empty `List<string>` in the constructor. The Go
// port mirrors that: nil slice → "[]" on persist (NOT "null") so
// readers always get a parseable JSON document.
package releaseprofiles

import (
	"errors"
	"fmt"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/repo"
)

// Profile is one ReleaseProfiles row.
type Profile struct {
	ID                 int      `json:"id"`
	Name               string   `json:"name,omitempty"`
	Enabled            bool     `json:"enabled"`
	Required           []string `json:"required"`
	Ignored            []string `json:"ignored"`
	Tags               []int    `json:"tags"`
	ExcludedTags       []int    `json:"excludedTags"`
	IndexerIDs         []int    `json:"indexerIds"`
	AirDateRestriction bool     `json:"airDateRestriction"`
	AirDateGracePeriod int      `json:"airDateGracePeriod"`
}

// ErrNotFound is returned when no row matches.
var ErrNotFound = errors.New("releaseprofiles: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	*repo.Repository[Profile]
}

var mapper = repo.Mapper[Profile]{
	Name:  "releaseprofiles",
	Table: "ReleaseProfiles",
	Columns: []string{
		"Required", "Ignored", "Tags", "Enabled", "Name", "ExcludedTags",
		"IndexerIds", "AirDateRestriction", "AirDateGracePeriod",
	},
	OrderBy: "Id",
	Bind: func(p *Profile) []any {
		return []any{
			marshalStrings(p.Required),
			marshalStrings(p.Ignored),
			marshalInts(p.Tags),
			p.Enabled,
			nullableName(p.Name),
			marshalInts(p.ExcludedTags),
			marshalInts(p.IndexerIDs),
			p.AirDateRestriction,
			p.AirDateGracePeriod,
		}
	},
	Scan: func(p *Profile, s repo.RowScanner) error {
		var required, ignored, tags, excludedTags, indexerIDs string
		var name *string // *string sentinel; database/sql handles NULL via *string scan target.
		if err := s.Scan(
			&p.ID, &required, &ignored, &tags, &p.Enabled, &name,
			&excludedTags, &indexerIDs, &p.AirDateRestriction, &p.AirDateGracePeriod,
		); err != nil {
			return err
		}
		if name != nil {
			p.Name = *name
		}
		if err := datastore.UnmarshalEmbedded(required, &p.Required); err != nil {
			return fmt.Errorf("unmarshal Required: %w", err)
		}
		if err := datastore.UnmarshalEmbedded(ignored, &p.Ignored); err != nil {
			return fmt.Errorf("unmarshal Ignored: %w", err)
		}
		if err := datastore.UnmarshalEmbedded(tags, &p.Tags); err != nil {
			return fmt.Errorf("unmarshal Tags: %w", err)
		}
		if err := datastore.UnmarshalEmbedded(excludedTags, &p.ExcludedTags); err != nil {
			return fmt.Errorf("unmarshal ExcludedTags: %w", err)
		}
		if err := datastore.UnmarshalEmbedded(indexerIDs, &p.IndexerIDs); err != nil {
			return fmt.Errorf("unmarshal IndexerIds: %w", err)
		}
		return nil
	},
	GetID: func(p *Profile) int { return p.ID },
	SetID: func(p *Profile, id int) { p.ID = id },
}

// NewRepo returns a Repo bound to the given DB.
func NewRepo(db *datastore.DB) (*Repo, error) {
	r, err := repo.New(db, mapper, ErrNotFound)
	if err != nil {
		return nil, err
	}
	return &Repo{Repository: r}, nil
}

// marshalStrings serializes a []string as JSON. nil → "[]" so the schema's
// NOT NULL columns get a parseable document, matching the C# default of
// `new List<string>()`.
func marshalStrings(xs []string) string {
	if xs == nil {
		xs = []string{}
	}
	out, err := datastore.MarshalEmbedded(xs)
	if err != nil {
		return `<marshal-error: ` + err.Error() + `>`
	}
	return out
}

// marshalInts serializes a []int as JSON. nil → "[]".
func marshalInts(xs []int) string {
	if xs == nil {
		xs = []int{}
	}
	out, err := datastore.MarshalEmbedded(xs)
	if err != nil {
		return `<marshal-error: ` + err.Error() + `>`
	}
	return out
}

// nullableName persists "" as NULL on the nullable Name column.
func nullableName(s string) any {
	if s == "" {
		return nil
	}
	return s
}
