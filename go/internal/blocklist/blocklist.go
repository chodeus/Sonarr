// Package blocklist is the Go port of NzbDrone.Core.Blocklisting.Blocklist
// persistence. Each row records a release that the user (or auto-import-
// failure flow) has marked as never-redownload-this. The download decision
// engine consults Blocklist on every release evaluation; cleanup happens
// via the housekeeping job.
//
// Schema at HEAD (v230): Id, SeriesId NOT NULL, EpisodeIds NOT NULL TEXT,
// SourceTitle NOT NULL, Quality NOT NULL TEXT, Date NOT NULL,
// PublishedDate nullable, Size BIGINT nullable, Protocol nullable,
// Indexer nullable, Message nullable, TorrentInfoHash nullable,
// Languages NOT NULL TEXT DEFAULT '[]', IndexerFlags INT NOT NULL
// DEFAULT 0, ReleaseType INT NOT NULL DEFAULT 0, Source nullable.
//
// Three columns hold embedded JSON: EpisodeIds (`[]int`), Quality (the
// QualityModel object), and Languages (`[]int` per the catalogue's
// language-int-form note). EpisodeIds + Languages get marshal/unmarshal
// at the SQL boundary; Quality is stored as opaque raw JSON `string`
// until the QualityModel struct lands in Phase 2.
package blocklist

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/repo"
)

// Entry is one Blocklist row. Quality is opaque JSON — the QualityModel
// struct will replace this when Phase 2 ports the parser/quality model.
type Entry struct {
	ID              int        `json:"id"`
	SeriesID        int        `json:"seriesId"`
	EpisodeIDs      []int      `json:"episodeIds"`
	SourceTitle     string     `json:"sourceTitle"`
	Quality         string     `json:"quality"`
	Date            time.Time  `json:"date"`
	PublishedDate   *time.Time `json:"publishedDate"`
	Size            *int64     `json:"size"`
	Protocol        *int       `json:"protocol"`
	Indexer         string     `json:"indexer"`
	Message         string     `json:"message"`
	TorrentInfoHash string     `json:"torrentInfoHash"`
	Languages       []int      `json:"languages"`
	IndexerFlags    int        `json:"indexerFlags"`
	ReleaseType     int        `json:"releaseType"`
	Source          string     `json:"source"`
}

// ErrNotFound is returned when no row matches.
var ErrNotFound = errors.New("blocklist: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	*repo.Repository[Entry]
}

var mapper = repo.Mapper[Entry]{
	Name:  "blocklist",
	Table: "Blocklist",
	Columns: []string{
		"SeriesId", "EpisodeIds", "SourceTitle", "Quality", "Date",
		"PublishedDate", "Size", "Protocol", "Indexer", "Message",
		"TorrentInfoHash", "Languages", "IndexerFlags", "ReleaseType", "Source",
	},
	OrderBy: "Date",
	Bind: func(e *Entry) []any {
		return []any{
			e.SeriesID,
			marshalInts(e.EpisodeIDs),
			e.SourceTitle,
			defaultJSON(e.Quality, "{}"),
			e.Date.UTC(),
			nullableTime(e.PublishedDate),
			nullableInt64(e.Size),
			nullableInt(e.Protocol),
			nullableStringEmpty(e.Indexer),
			nullableStringEmpty(e.Message),
			nullableStringEmpty(e.TorrentInfoHash),
			marshalInts(e.Languages),
			e.IndexerFlags,
			e.ReleaseType,
			nullableStringEmpty(e.Source),
		}
	},
	Scan: func(e *Entry, s repo.RowScanner) error {
		var episodeIDs, languages string
		var quality string
		var published sql.NullTime
		var size sql.NullInt64
		var protocol sql.NullInt64
		var indexer, message, torrentHash, source sql.NullString
		if err := s.Scan(
			&e.ID, &e.SeriesID, &episodeIDs, &e.SourceTitle, &quality, &e.Date,
			&published, &size, &protocol, &indexer, &message, &torrentHash,
			&languages, &e.IndexerFlags, &e.ReleaseType, &source,
		); err != nil {
			return err
		}
		e.Date = e.Date.UTC()
		e.Quality = quality
		e.PublishedDate = unwrapTime(published)
		if size.Valid {
			v := size.Int64
			e.Size = &v
		}
		if protocol.Valid {
			v := int(protocol.Int64)
			e.Protocol = &v
		}
		e.Indexer = indexer.String
		e.Message = message.String
		e.TorrentInfoHash = torrentHash.String
		e.Source = source.String
		if err := datastore.UnmarshalEmbedded(episodeIDs, &e.EpisodeIDs); err != nil {
			return fmt.Errorf("unmarshal EpisodeIds: %w", err)
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

// AllBySeriesID returns every Blocklist row for the given SeriesId,
// ordered by Date descending (most recent block first). This is the
// shape the decision engine wants when checking "has this series
// blocklisted any recent release that matches?"
func (r *Repo) AllBySeriesID(ctx context.Context, seriesID int) ([]Entry, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT "Id","SeriesId","EpisodeIds","SourceTitle","Quality","Date","PublishedDate","Size","Protocol","Indexer","Message","TorrentInfoHash","Languages","IndexerFlags","ReleaseType","Source" FROM "Blocklist" WHERE "SeriesId" = %s ORDER BY "Date" DESC`,
		datastore.Placeholder(db.Engine, 1))
	rows, err := db.SQL.QueryContext(ctx, q, seriesID)
	if err != nil {
		return nil, fmt.Errorf("blocklist: allBySeriesID(%d): %w", seriesID, err)
	}
	defer func() { _ = rows.Close() }()
	var out []Entry
	for rows.Next() {
		var e Entry
		if err := mapper.Scan(&e, rows); err != nil {
			return nil, fmt.Errorf("blocklist: allBySeriesID scan: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("blocklist: allBySeriesID rows: %w", err)
	}
	return out, nil
}

// DeleteForSeries ports
// `BlocklistRepository.DeleteForSeriesIds(List<int>)`. Bulk DELETE
// every Blocklist row whose SeriesId is in the supplied list.
// Returns rows affected. Empty input is no-op (0, nil).
//
// Production binding: the SeriesDeletedEvent IHandleAsync subscriber
// (slice 3.BB) calls this via Service.DeleteForSeries when a series
// is removed so its blocklist rows don't linger after the parent row
// is gone (the Blocklist.SeriesId column is not declared as a
// foreign key in the schema, so the cleanup is application-side —
// same posture as the slice-3.AY history sibling).
func (r *Repo) DeleteForSeries(ctx context.Context, seriesIDs []int) (int64, error) {
	if len(seriesIDs) == 0 {
		return 0, nil
	}
	db := r.DB()
	placeholders := make([]string, len(seriesIDs))
	args := make([]any, len(seriesIDs))
	for i, id := range seriesIDs {
		placeholders[i] = datastore.Placeholder(db.Engine, i+1)
		args[i] = id
	}
	q := fmt.Sprintf(
		`DELETE FROM "Blocklist" WHERE "SeriesId" IN (%s)`,
		strings.Join(placeholders, ","),
	)
	res, err := db.SQL.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("blocklist: DeleteForSeries: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("blocklist: DeleteForSeries: rowsAffected: %w", err)
	}
	return n, nil
}

// DeleteByIDs ports `BlocklistRepository.DeleteMany(List<int> ids)`.
// Bulk DELETE every Blocklist row whose Id is in the supplied list.
// Returns rows affected. Empty input is no-op (0, nil). Used by the
// /api/v3/blocklist/bulk DELETE handler (slice 5.E3).
func (r *Repo) DeleteByIDs(ctx context.Context, ids []int) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	db := r.DB()
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = datastore.Placeholder(db.Engine, i+1)
		args[i] = id
	}
	q := fmt.Sprintf(
		`DELETE FROM "Blocklist" WHERE "Id" IN (%s)`,
		strings.Join(placeholders, ","),
	)
	res, err := db.SQL.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("blocklist: DeleteByIDs: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("blocklist: DeleteByIDs: rowsAffected: %w", err)
	}
	return n, nil
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

// defaultJSON returns the input string if non-empty, else the fallback.
// Used to satisfy the schema's NOT NULL on Quality when callers leave
// the string empty.
func defaultJSON(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func nullableTime(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t.UTC(), Valid: true}
}

func unwrapTime(n sql.NullTime) *time.Time {
	if !n.Valid {
		return nil
	}
	v := n.Time.UTC()
	return &v
}

func nullableInt(p *int) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*p), Valid: true}
}

func nullableInt64(p *int64) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *p, Valid: true}
}

func nullableStringEmpty(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
