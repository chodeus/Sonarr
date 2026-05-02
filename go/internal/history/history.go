// Package history is the Go port of NzbDrone.Core.History.EpisodeHistory
// persistence. Each row records an event in an episode's lifecycle —
// grabbed, imported, failed, deleted — with the release metadata as it
// was at the time.
//
// Schema at HEAD (v230): Id, EpisodeId NOT NULL, SeriesId NOT NULL,
// SourceTitle NOT NULL, Date NOT NULL, Quality NOT NULL TEXT,
// Data NOT NULL TEXT, EventType nullable, DownloadId nullable,
// Languages NOT NULL TEXT DEFAULT '[]'.
//
// Three columns hold embedded JSON: Quality (the QualityModel object,
// stored as opaque raw JSON until Phase 2 ports the struct), Data (the
// Dictionary<string,string> of arbitrary key-value pairs the importer
// adds — releaseGroup, indexer, downloadClient, etc.), and Languages
// (`[]int` per the catalogue's language-int-form note).
//
// EventType is nullable in the schema but the C# enum starts at 0
// (Unknown) — old rows that predate the EventType column read as NULL.
// Modeled as *int so callers can distinguish "unknown / no event type"
// from explicit Unknown=0.
package history

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/repo"
)

// Entry is one History row.
type Entry struct {
	ID          int               `json:"id"`
	EpisodeID   int               `json:"episodeId"`
	SeriesID    int               `json:"seriesId"`
	SourceTitle string            `json:"sourceTitle"`
	Date        time.Time         `json:"date"`
	Quality     string            `json:"quality"`
	Data        map[string]string `json:"data"`
	EventType   *int              `json:"eventType,omitempty"`
	DownloadID  string            `json:"downloadId,omitempty"`
	Languages   []int             `json:"languages"`
}

// ErrNotFound is returned when no row matches.
var ErrNotFound = errors.New("history: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	*repo.Repository[Entry]
}

var mapper = repo.Mapper[Entry]{
	Name:  "history",
	Table: "History",
	Columns: []string{
		"EpisodeId", "SeriesId", "SourceTitle", "Date", "Quality",
		"Data", "EventType", "DownloadId", "Languages",
	},
	OrderBy: "Date",
	Bind: func(e *Entry) []any {
		return []any{
			e.EpisodeID, e.SeriesID, e.SourceTitle, e.Date.UTC(),
			defaultJSON(e.Quality, "{}"),
			marshalDict(e.Data),
			nullableInt(e.EventType),
			nullableStringEmpty(e.DownloadID),
			marshalInts(e.Languages),
		}
	},
	Scan: func(e *Entry, s repo.RowScanner) error {
		var quality, data, languages string
		var eventType sql.NullInt64
		var downloadID sql.NullString
		if err := s.Scan(
			&e.ID, &e.EpisodeID, &e.SeriesID, &e.SourceTitle, &e.Date,
			&quality, &data, &eventType, &downloadID, &languages,
		); err != nil {
			return err
		}
		e.Date = e.Date.UTC()
		e.Quality = quality
		e.DownloadID = downloadID.String
		if eventType.Valid {
			v := int(eventType.Int64)
			e.EventType = &v
		}
		if err := datastore.UnmarshalEmbedded(data, &e.Data); err != nil {
			return fmt.Errorf("unmarshal Data: %w", err)
		}
		if err := datastore.UnmarshalEmbedded(languages, &e.Languages); err != nil {
			return fmt.Errorf("unmarshal Languages: %w", err)
		}
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

// AllByEpisodeID returns every History row for the given EpisodeId,
// ordered by Date descending.
func (r *Repo) AllByEpisodeID(ctx context.Context, episodeID int) ([]Entry, error) {
	return r.queryByInt(ctx, "EpisodeId", episodeID)
}

// AllBySeriesID returns every History row for the given SeriesId,
// ordered by Date descending.
func (r *Repo) AllBySeriesID(ctx context.Context, seriesID int) ([]Entry, error) {
	return r.queryByInt(ctx, "SeriesId", seriesID)
}

// AllByDownloadID returns every History row for the given DownloadId.
// DownloadId tracks releases through their grab/import lifecycle, so
// callers wanting "every event for this grab" use this lookup.
func (r *Repo) AllByDownloadID(ctx context.Context, downloadID string) ([]Entry, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT "Id","EpisodeId","SeriesId","SourceTitle","Date","Quality","Data","EventType","DownloadId","Languages" FROM "History" WHERE "DownloadId" = %s ORDER BY "Date" DESC`,
		datastore.Placeholder(db.Engine, 1))
	return r.scanQuery(ctx, q, downloadID, "allByDownloadID")
}

func (r *Repo) queryByInt(ctx context.Context, col string, value int) ([]Entry, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT "Id","EpisodeId","SeriesId","SourceTitle","Date","Quality","Data","EventType","DownloadId","Languages" FROM "History" WHERE "%s" = %s ORDER BY "Date" DESC`,
		col, datastore.Placeholder(db.Engine, 1))
	return r.scanQuery(ctx, q, value, "queryBy"+col)
}

func (r *Repo) scanQuery(ctx context.Context, query string, arg any, op string) ([]Entry, error) {
	db := r.DB()
	rows, err := db.SQL.QueryContext(ctx, query, arg)
	if err != nil {
		return nil, fmt.Errorf("history: %s: %w", op, err)
	}
	defer func() { _ = rows.Close() }()
	var out []Entry
	for rows.Next() {
		var e Entry
		if err := mapper.Scan(&e, rows); err != nil {
			return nil, fmt.Errorf("history: %s scan: %w", op, err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("history: %s rows: %w", op, err)
	}
	return out, nil
}

func defaultJSON(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// marshalDict serialises a string→string dictionary to JSON. nil → "{}".
func marshalDict(d map[string]string) string {
	if d == nil {
		d = map[string]string{}
	}
	out, err := datastore.MarshalEmbedded(d)
	if err != nil {
		return `<marshal-error: ` + err.Error() + `>`
	}
	return out
}

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

func nullableInt(p *int) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*p), Valid: true}
}

func nullableStringEmpty(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
