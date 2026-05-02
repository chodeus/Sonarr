// Package qualityprofiles is the Go port of NzbDrone.Core.Profiles.Qualities
// QualityProfile persistence. Each row is a named profile holding the
// ordered Items list (allowed qualities + groups) plus FormatItems
// (per-CustomFormat scores) that the decision engine consults on every
// release evaluation.
//
// Schema at HEAD (v230): Id, Name UNIQUE NOT NULL, Cutoff INT NOT NULL,
// Items TEXT NOT NULL, UpgradeAllowed BOOL nullable,
// FormatItems TEXT NOT NULL DEFAULT '[]', MinFormatScore INT NOT NULL
// DEFAULT 0, CutoffFormatScore INT NOT NULL DEFAULT 0,
// MinUpgradeFormatScore INT NOT NULL DEFAULT 1.
//
// Items + FormatItems are embedded JSON (`List<QualityProfileQualityItem>`
// and `List<ProfileFormatItem>` respectively). Both are stored as opaque
// `string` here — the typed wrappers will land in Phase 2 once the
// Quality + CustomFormat models are ported. The on-disk JSON shape is
// the wire contract; switching to typed wrappers later is non-breaking.
//
// UpgradeAllowed is nullable in the schema but the C# class declares
// `bool UpgradeAllowed`. Modeling as plain `bool` with NULL coerced to
// false matches the C# default and what the decision engine sees.
package qualityprofiles

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/repo"
)

// Profile is one QualityProfiles row.
type Profile struct {
	ID                    int    `json:"id"`
	Name                  string `json:"name"`
	Cutoff                int    `json:"cutoff"`
	Items                 string `json:"items"`
	UpgradeAllowed        bool   `json:"upgradeAllowed"`
	FormatItems           string `json:"formatItems"`
	MinFormatScore        int    `json:"minFormatScore"`
	CutoffFormatScore     int    `json:"cutoffFormatScore"`
	MinUpgradeFormatScore int    `json:"minUpgradeFormatScore"`
}

// ErrNotFound is returned when no row matches.
var ErrNotFound = errors.New("qualityprofiles: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	*repo.Repository[Profile]
}

var mapper = repo.Mapper[Profile]{
	Name:  "qualityprofiles",
	Table: "QualityProfiles",
	Columns: []string{
		"Name", "Cutoff", "Items", "UpgradeAllowed", "FormatItems",
		"MinFormatScore", "CutoffFormatScore", "MinUpgradeFormatScore",
	},
	OrderBy: "Id",
	Bind: func(p *Profile) []any {
		return []any{
			p.Name, p.Cutoff,
			defaultJSON(p.Items, "[]"),
			sql.NullBool{Bool: p.UpgradeAllowed, Valid: true},
			defaultJSON(p.FormatItems, "[]"),
			p.MinFormatScore, p.CutoffFormatScore, p.MinUpgradeFormatScore,
		}
	},
	Scan: func(p *Profile, s repo.RowScanner) error {
		var upgradeAllowed sql.NullBool
		if err := s.Scan(
			&p.ID, &p.Name, &p.Cutoff, &p.Items, &upgradeAllowed,
			&p.FormatItems, &p.MinFormatScore, &p.CutoffFormatScore,
			&p.MinUpgradeFormatScore,
		); err != nil {
			return err
		}
		p.UpgradeAllowed = upgradeAllowed.Valid && upgradeAllowed.Bool
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

// GetByName returns the Profile with the given Name or ErrNotFound.
// Name is UNIQUE so at most one row matches.
func (r *Repo) GetByName(ctx context.Context, name string) (*Profile, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT "Id","Name","Cutoff","Items","UpgradeAllowed","FormatItems","MinFormatScore","CutoffFormatScore","MinUpgradeFormatScore" FROM "QualityProfiles" WHERE "Name" = %s`,
		datastore.Placeholder(db.Engine, 1))
	var p Profile
	var upgradeAllowed sql.NullBool
	switch err := db.SQL.QueryRowContext(ctx, q, name).Scan(
		&p.ID, &p.Name, &p.Cutoff, &p.Items, &upgradeAllowed,
		&p.FormatItems, &p.MinFormatScore, &p.CutoffFormatScore,
		&p.MinUpgradeFormatScore,
	); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("qualityprofiles: getByName(%q): %w", name, err)
	}
	p.UpgradeAllowed = upgradeAllowed.Valid && upgradeAllowed.Bool
	return &p, nil
}

func defaultJSON(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
