// Package downloadhistory is the Go port of NzbDrone.Core.Download.History
// DownloadHistory persistence (added by migration 145, see ledger row 145).
// Each row records a download-pipeline event for a release tracked by
// DownloadId — Grabbed, Imported, Failed, Ignored, FileImported. The
// download-monitoring job consults this table to drive the per-download
// status panel and to detect failures that need user intervention.
//
// Schema at HEAD (v230): Id, EventType NOT NULL, SeriesId NOT NULL,
// DownloadId NOT NULL, SourceTitle NOT NULL, Date NOT NULL,
// Protocol nullable, IndexerId nullable, DownloadClientId nullable,
// Release nullable TEXT, Data nullable TEXT.
//
// Release holds an embedded ReleaseInfo JSON (provider-specific release
// metadata captured at grab time). Data holds a Dictionary<string,string>
// JSON of arbitrary attribution fields. Both are stored as opaque strings
// here — Phase 2 may replace Release with a typed wrapper once the
// ReleaseInfo struct lands.
package downloadhistory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/repo"
)

// Entry is one DownloadHistory row.
type Entry struct {
	ID          int       `json:"id"`
	EventType   int       `json:"eventType"`
	SeriesID    int       `json:"seriesId"`
	DownloadID  string    `json:"downloadId"`
	SourceTitle string    `json:"sourceTitle"`
	Date        time.Time `json:"date"`
	// Protocol / IndexerID / DownloadClientID mirror C# non-nullable
	// int properties per go/JSON_CONVENTIONS.md Option A. Schema
	// columns are nullable; Scan coerces NULL → 0 to match C#
	// property semantics.
	Protocol         int    `json:"protocol"`
	IndexerID        int    `json:"indexerId"`
	DownloadClientID int    `json:"downloadClientId"`
	Release          string `json:"release"`
	Data             string `json:"data"`
}

// ErrNotFound is returned when no row matches.
var ErrNotFound = errors.New("downloadhistory: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	*repo.Repository[Entry]
}

var mapper = repo.Mapper[Entry]{
	Name:  "downloadhistory",
	Table: "DownloadHistory",
	Columns: []string{
		"EventType", "SeriesId", "DownloadId", "SourceTitle", "Date",
		"Protocol", "IndexerId", "DownloadClientId", "Release", "Data",
	},
	OrderBy: "Date",
	Bind: func(e *Entry) []any {
		return []any{
			e.EventType, e.SeriesID, e.DownloadID, e.SourceTitle, e.Date.UTC(),
			e.Protocol,
			e.IndexerID,
			e.DownloadClientID,
			nullableStringEmpty(e.Release),
			nullableStringEmpty(e.Data),
		}
	},
	Scan: func(e *Entry, s repo.RowScanner) error {
		var protocol, indexerID, downloadClientID sql.NullInt64
		var release, data sql.NullString
		if err := s.Scan(
			&e.ID, &e.EventType, &e.SeriesID, &e.DownloadID, &e.SourceTitle, &e.Date,
			&protocol, &indexerID, &downloadClientID, &release, &data,
		); err != nil {
			return err
		}
		e.Date = e.Date.UTC()
		if protocol.Valid {
			e.Protocol = int(protocol.Int64)
		}
		if indexerID.Valid {
			e.IndexerID = int(indexerID.Int64)
		}
		if downloadClientID.Valid {
			e.DownloadClientID = int(downloadClientID.Int64)
		}
		e.Release = release.String
		e.Data = data.String
		return nil
	},
	GetID: func(e *Entry) int { return e.ID },
	SetID: func(e *Entry, id int) { e.ID = id },
}

// NewRepo returns a Repo bound to the given DB.
func NewRepo(db *datastore.DB) (*Repo, error) {
	r, err := repo.New(db, mapper, ErrNotFound)
	if err != nil {
		return nil, err
	}
	return &Repo{Repository: r}, nil
}

// AllByDownloadID returns every Entry for a download lifecycle, ordered
// by Date descending. Most callers want the latest event first.
func (r *Repo) AllByDownloadID(ctx context.Context, downloadID string) ([]Entry, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT "Id","EventType","SeriesId","DownloadId","SourceTitle","Date","Protocol","IndexerId","DownloadClientId","Release","Data" FROM "DownloadHistory" WHERE "DownloadId" = %s ORDER BY "Date" DESC`,
		datastore.Placeholder(db.Engine, 1))
	return r.scanQuery(ctx, q, downloadID, "allByDownloadID")
}

// AllBySeriesID returns every Entry for the given Series, ordered by
// Date descending.
func (r *Repo) AllBySeriesID(ctx context.Context, seriesID int) ([]Entry, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT "Id","EventType","SeriesId","DownloadId","SourceTitle","Date","Protocol","IndexerId","DownloadClientId","Release","Data" FROM "DownloadHistory" WHERE "SeriesId" = %s ORDER BY "Date" DESC`,
		datastore.Placeholder(db.Engine, 1))
	return r.scanQuery(ctx, q, seriesID, "allBySeriesID")
}

func (r *Repo) scanQuery(ctx context.Context, query string, arg any, op string) ([]Entry, error) {
	db := r.DB()
	rows, err := db.SQL.QueryContext(ctx, query, arg)
	if err != nil {
		return nil, fmt.Errorf("downloadhistory: %s: %w", op, err)
	}
	defer func() { _ = rows.Close() }()
	var out []Entry
	for rows.Next() {
		var e Entry
		if err := mapper.Scan(&e, rows); err != nil {
			return nil, fmt.Errorf("downloadhistory: %s scan: %w", op, err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("downloadhistory: %s rows: %w", op, err)
	}
	return out, nil
}

func nullableStringEmpty(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
