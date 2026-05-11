// Package pendingreleases is the Go port of NzbDrone.Core.Download.Pending
// PendingRelease persistence. Each row is a release the decision engine
// queued for later evaluation — typically because the indexer's delay
// profile blocked an immediate grab. The DownloadDecisionMaker reads
// this table on each pass and flushes rows whose time has come.
//
// Schema at HEAD (v230): Id, SeriesId NOT NULL, Title NOT NULL,
// Added NOT NULL DATETIME, ParsedEpisodeInfo NOT NULL TEXT,
// Release NOT NULL TEXT, Reason NOT NULL INT DEFAULT 0,
// AdditionalInfo nullable TEXT.
//
// Three columns hold opaque JSON: ParsedEpisodeInfo (the parser-produced
// metadata), Release (the indexer's ReleaseInfo blob), and
// AdditionalInfo (a small {SeriesMatchType, ReleaseSource} envelope).
// Each is stored as a string until its typed wrapper lands in Phase 2.
package pendingreleases

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/repo"
)

// Release is one PendingReleases row.
type Release struct {
	ID                int       `json:"id"`
	SeriesID          int       `json:"seriesId"`
	Title             string    `json:"title"`
	Added             time.Time `json:"added"`
	ParsedEpisodeInfo string    `json:"parsedEpisodeInfo"`
	ReleaseInfo       string    `json:"release"`
	Reason            int       `json:"reason"`
	AdditionalInfo    string    `json:"additionalInfo"`
}

// ErrNotFound is returned when no row matches.
var ErrNotFound = errors.New("pendingreleases: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	*repo.Repository[Release]
}

var mapper = repo.Mapper[Release]{
	Name:  "pendingreleases",
	Table: "PendingReleases",
	Columns: []string{
		"SeriesId", "Title", "Added", "ParsedEpisodeInfo", "Release",
		"Reason", "AdditionalInfo",
	},
	OrderBy: "Added",
	Bind: func(r *Release) []any {
		return []any{
			r.SeriesID, r.Title, r.Added.UTC(),
			defaultJSON(r.ParsedEpisodeInfo, "{}"),
			defaultJSON(r.ReleaseInfo, "{}"),
			r.Reason,
			nullableStringEmpty(r.AdditionalInfo),
		}
	},
	Scan: func(r *Release, s repo.RowScanner) error {
		var additional sql.NullString
		if err := s.Scan(
			&r.ID, &r.SeriesID, &r.Title, &r.Added,
			&r.ParsedEpisodeInfo, &r.ReleaseInfo, &r.Reason, &additional,
		); err != nil {
			return err
		}
		r.Added = r.Added.UTC()
		r.AdditionalInfo = additional.String
		return nil
	},
	GetID: func(r *Release) int { return r.ID },
	SetID: func(r *Release, id int) { r.ID = id },
}

// NewRepo returns a Repo bound to the given DB.
func NewRepo(db *datastore.DB) (*Repo, error) {
	r, err := repo.New(db, mapper, ErrNotFound)
	if err != nil {
		return nil, err
	}
	return &Repo{Repository: r}, nil
}

// AllBySeriesID returns every queued release for the given Series,
// ordered by Added ascending. Used when the user views the queue panel
// for a specific series.
func (r *Repo) AllBySeriesID(ctx context.Context, seriesID int) ([]Release, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT "Id","SeriesId","Title","Added","ParsedEpisodeInfo","Release","Reason","AdditionalInfo" FROM "PendingReleases" WHERE "SeriesId" = %s ORDER BY "Added"`,
		datastore.Placeholder(db.Engine, 1))
	rows, err := db.SQL.QueryContext(ctx, q, seriesID)
	if err != nil {
		return nil, fmt.Errorf("pendingreleases: allBySeriesID(%d): %w", seriesID, err)
	}
	defer func() { _ = rows.Close() }()
	var out []Release
	for rows.Next() {
		var rel Release
		if err := mapper.Scan(&rel, rows); err != nil {
			return nil, fmt.Errorf("pendingreleases: allBySeriesID scan: %w", err)
		}
		out = append(out, rel)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pendingreleases: allBySeriesID rows: %w", err)
	}
	return out, nil
}

func defaultJSON(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func nullableStringEmpty(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
