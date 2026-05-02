// Package namingconfig is the Go port of NzbDrone.Core.Organizer.NamingConfig
// persistence. NamingConfig holds the format strings the Organizer uses
// to build episode/season/series filenames, plus a few global toggles.
//
// Schema at HEAD (v230): Id, MultiEpisodeStyle INT NOT NULL,
// RenameEpisodes INT nullable, StandardEpisodeFormat / DailyEpisodeFormat
// / SeasonFolderFormat / SeriesFolderFormat / AnimeEpisodeFormat /
// SpecialsFolderFormat all nullable TEXT, ReplaceIllegalCharacters INT
// NOT NULL DEFAULT 1, ColonReplacementFormat INT NOT NULL DEFAULT 4,
// CustomColonReplacementFormat TEXT NOT NULL DEFAULT empty.
//
// Practical row-count: this is effectively a singleton table — Sonarr
// keeps exactly one row, created by NamingConfigService at startup if
// missing. The Repo exposes the standard CRUD surface anyway because the
// service-layer convenience (`Get current config; if missing, insert
// the default`) belongs above the repo, not inside it.
//
// Type-mapping notes:
//
//   - The C# class declares `bool RenameEpisodes` (not bool?), but the
//     column is nullable. NULL reads as false; we model the field as
//     plain bool and use sql.NullBool at the SQL boundary so writes
//     produce explicit booleans, reads coerce NULL → false.
//   - The five format string columns are nullable TEXT; modeled as
//     plain `string` with NULL coerced to "". The C# Default object
//     populates all five with non-empty strings, so NULL is a legacy /
//     manual-edit case.
package namingconfig

import (
	"database/sql"
	"errors"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/repo"
)

// Config is one NamingConfig row. JSON tags use camelCase; field
// ordering mirrors the schema column order to keep diff-against-the-
// captured-schema obvious.
type Config struct {
	ID                           int    `json:"id"`
	MultiEpisodeStyle            int    `json:"multiEpisodeStyle"`
	RenameEpisodes               bool   `json:"renameEpisodes"`
	StandardEpisodeFormat        string `json:"standardEpisodeFormat,omitempty"`
	DailyEpisodeFormat           string `json:"dailyEpisodeFormat,omitempty"`
	SeasonFolderFormat           string `json:"seasonFolderFormat,omitempty"`
	SeriesFolderFormat           string `json:"seriesFolderFormat,omitempty"`
	AnimeEpisodeFormat           string `json:"animeEpisodeFormat,omitempty"`
	ReplaceIllegalCharacters     bool   `json:"replaceIllegalCharacters"`
	SpecialsFolderFormat         string `json:"specialsFolderFormat,omitempty"`
	ColonReplacementFormat       int    `json:"colonReplacementFormat"`
	CustomColonReplacementFormat string `json:"customColonReplacementFormat"`
}

// ErrNotFound is returned when no row matches.
var ErrNotFound = errors.New("namingconfig: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	*repo.Repository[Config]
}

var mapper = repo.Mapper[Config]{
	Name:  "namingconfig",
	Table: "NamingConfig",
	Columns: []string{
		"MultiEpisodeStyle", "RenameEpisodes", "StandardEpisodeFormat",
		"DailyEpisodeFormat", "SeasonFolderFormat", "SeriesFolderFormat",
		"AnimeEpisodeFormat", "ReplaceIllegalCharacters", "SpecialsFolderFormat",
		"ColonReplacementFormat", "CustomColonReplacementFormat",
	},
	OrderBy: "Id",
	Bind: func(c *Config) []any {
		return []any{
			c.MultiEpisodeStyle, sql.NullBool{Bool: c.RenameEpisodes, Valid: true},
			nullableString(c.StandardEpisodeFormat),
			nullableString(c.DailyEpisodeFormat),
			nullableString(c.SeasonFolderFormat),
			nullableString(c.SeriesFolderFormat),
			nullableString(c.AnimeEpisodeFormat),
			c.ReplaceIllegalCharacters,
			nullableString(c.SpecialsFolderFormat),
			c.ColonReplacementFormat,
			c.CustomColonReplacementFormat,
		}
	},
	Scan: func(c *Config, s repo.RowScanner) error {
		var rename sql.NullBool
		var std, daily, sf, srf, anime, specials sql.NullString
		if err := s.Scan(
			&c.ID, &c.MultiEpisodeStyle, &rename, &std, &daily, &sf, &srf, &anime,
			&c.ReplaceIllegalCharacters, &specials, &c.ColonReplacementFormat,
			&c.CustomColonReplacementFormat,
		); err != nil {
			return err
		}
		c.RenameEpisodes = rename.Valid && rename.Bool
		c.StandardEpisodeFormat = std.String
		c.DailyEpisodeFormat = daily.String
		c.SeasonFolderFormat = sf.String
		c.SeriesFolderFormat = srf.String
		c.AnimeEpisodeFormat = anime.String
		c.SpecialsFolderFormat = specials.String
		return nil
	},
	GetID: func(c *Config) int { return c.ID },
	SetID: func(c *Config, id int) { c.ID = id },
}

// NewRepo returns a Repo bound to the given DB.
func NewRepo(db *datastore.DB) (*Repo, error) {
	r, err := repo.New(db, mapper, ErrNotFound)
	if err != nil {
		return nil, err
	}
	return &Repo{Repository: r}, nil
}

// Default returns the canonical defaults that NamingConfigService inserts
// when no row exists. Mirrors NamingConfig.Default in the C# tree.
//
// ColonReplacementFormat = 4 corresponds to ColonReplacementFormat.Smart;
// MultiEpisodeStyle = 5 corresponds to MultiEpisodeStyle.PrefixedRange.
// The persisted integer values are the wire contract; do not change.
func Default() *Config {
	return &Config{
		MultiEpisodeStyle:            5,
		RenameEpisodes:               false,
		ReplaceIllegalCharacters:     true,
		ColonReplacementFormat:       4,
		CustomColonReplacementFormat: "",
		StandardEpisodeFormat:        `{Series Title} - S{season:00}E{episode:00} - {Episode Title} {Quality Full}`,
		DailyEpisodeFormat:           `{Series Title} - {Air-Date} - {Episode Title} {Quality Full}`,
		AnimeEpisodeFormat:           `{Series Title} - S{season:00}E{episode:00} - {Episode Title} {Quality Full}`,
		SeriesFolderFormat:           `{Series Title}`,
		SeasonFolderFormat:           `Season {season}`,
		SpecialsFolderFormat:         `Specials`,
	}
}

func nullableString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
