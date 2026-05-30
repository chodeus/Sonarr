// Package subtitlefiles is the Go port of NzbDrone.Core.Extras.Subtitles
// SubtitleFile persistence. Subtitles are external files the scanner
// discovers next to (or inside) episode files; they're tracked
// independently so the renamer can move them with their parent.
//
// Schema at HEAD (v230): Id, SeriesId NOT NULL, SeasonNumber NOT NULL,
// EpisodeFileId NOT NULL, RelativePath NOT NULL, Extension NOT NULL,
// Added nullable (the schema allows pre-existing rows without an Added
// timestamp), LastUpdated NOT NULL, Language INT NOT NULL,
// LanguageTags nullable TEXT (JSON []string), Title nullable TEXT,
// Copy INT NOT NULL DEFAULT 0.
//
// LanguageTags is a JSON-encoded `[]string` (e.g. `["forced", "sdh"]`).
// The Mapper marshals/unmarshals at the SQL boundary; nil → "[]" so
// the schema's nullable column gets parseable bytes.
package subtitlefiles

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/repo"
)

// SubtitleFile is one SubtitleFiles row.
type SubtitleFile struct {
	ID            int        `json:"id"`
	SeriesID      int        `json:"seriesId"`
	SeasonNumber  int        `json:"seasonNumber"`
	EpisodeFileID int        `json:"episodeFileId"`
	RelativePath  string     `json:"relativePath"`
	Extension     string     `json:"extension"`
	Added         *time.Time `json:"added"`
	LastUpdated   time.Time  `json:"lastUpdated"`
	Language      int        `json:"language"`
	LanguageTags  []string   `json:"languageTags"`
	Title         string     `json:"title"`
	Copy          int        `json:"copy"`
}

// ErrNotFound is returned when no row matches.
var ErrNotFound = errors.New("subtitlefiles: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	*repo.Repository[SubtitleFile]
}

var mapper = repo.Mapper[SubtitleFile]{
	Name:  "subtitlefiles",
	Table: "SubtitleFiles",
	Columns: []string{
		"SeriesId", "SeasonNumber", "EpisodeFileId", "RelativePath",
		"Extension", "Added", "LastUpdated", "Language", "LanguageTags",
		"Title", "Copy",
	},
	OrderBy: "Id",
	Bind: func(f *SubtitleFile) []any {
		return []any{
			f.SeriesID, f.SeasonNumber, f.EpisodeFileID, f.RelativePath,
			f.Extension, nullableTime(f.Added), f.LastUpdated.UTC(),
			f.Language, marshalStrings(f.LanguageTags),
			nullableStringEmpty(f.Title), f.Copy,
		}
	},
	Scan: func(f *SubtitleFile, s repo.RowScanner) error {
		var added sql.NullTime
		var langTags string
		var title sql.NullString
		if err := s.Scan(
			&f.ID, &f.SeriesID, &f.SeasonNumber, &f.EpisodeFileID,
			&f.RelativePath, &f.Extension, &added, &f.LastUpdated,
			&f.Language, &langTags, &title, &f.Copy,
		); err != nil {
			return err
		}
		f.LastUpdated = f.LastUpdated.UTC()
		if added.Valid {
			v := added.Time.UTC()
			f.Added = &v
		}
		f.Title = title.String
		if err := datastore.UnmarshalEmbedded(langTags, &f.LanguageTags); err != nil {
			return fmt.Errorf("unmarshal LanguageTags: %w", err)
		}
		return nil
	},
	GetID: func(f *SubtitleFile) int { return f.ID },
	SetID: func(f *SubtitleFile, id int) { f.ID = id },
}

// NewRepo returns a Repo bound to the given DB.
func NewRepo(db *datastore.DB) (*Repo, error) {
	r, err := repo.New(db, mapper, ErrNotFound)
	if err != nil {
		return nil, err
	}
	return &Repo{Repository: r}, nil
}

func nullableTime(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t.UTC(), Valid: true}
}

func nullableStringEmpty(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

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

// AllBySeriesID returns every SubtitleFile for the given series, ordered
// by Id. Ports the C# `ISubtitleFileService.GetFilesBySeries` — the source
// the post-rename companion-move pass reads (J480).
func (r *Repo) AllBySeriesID(ctx context.Context, seriesID int) ([]SubtitleFile, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT "Id","SeriesId","SeasonNumber","EpisodeFileId","RelativePath","Extension","Added","LastUpdated","Language","LanguageTags","Title","Copy" FROM "SubtitleFiles" WHERE "SeriesId" = %s ORDER BY "Id"`,
		datastore.Placeholder(db.Engine, 1))
	rows, err := db.SQL.QueryContext(ctx, q, seriesID)
	if err != nil {
		return nil, fmt.Errorf("subtitlefiles: allBySeriesID(%d): %w", seriesID, err)
	}
	defer func() { _ = rows.Close() }()
	var out []SubtitleFile
	for rows.Next() {
		var f SubtitleFile
		if err := mapper.Scan(&f, rows); err != nil {
			return nil, fmt.Errorf("subtitlefiles: allBySeriesID scan: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("subtitlefiles: allBySeriesID rows: %w", err)
	}
	return out, nil
}
