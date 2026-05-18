// Package episodefiles is the Go port of NzbDrone.Core.MediaFiles.EpisodeFile
// persistence. Each row tracks one on-disk media file: the actual mkv/mp4
// the user has downloaded for a specific Series+Season+EpisodeFile.
//
// Schema at HEAD (v230): Id, SeriesId NOT NULL, Quality NOT NULL TEXT,
// Size BIGINT NOT NULL, DateAdded NOT NULL, SeasonNumber NOT NULL,
// SceneName nullable, ReleaseGroup nullable, MediaInfo nullable TEXT,
// RelativePath nullable, OriginalFilePath nullable,
// Languages NOT NULL TEXT DEFAULT '[]', IndexerFlags INT NOT NULL DEFAULT 0,
// ReleaseType INT NOT NULL DEFAULT 0, ReleaseHash nullable.
//
// Three columns hold opaque embedded JSON: Quality (QualityModel),
// MediaInfo (MediaInfoModel — the rich ffprobe-like blob), and Languages
// (`[]int` per the catalogue's language-int-form). Quality is still
// stored as raw `string` until Phase 4 ports the typed wrapper;
// MediaInfo became typed in Phase 3.14 — `*mediainfo.Model` round-trips
// through the embedded-doc helpers at the SQL boundary.
//
// The C# class also has Path (computed from RelativePath + Series.Path)
// and LazyLoaded<List<Episode>> + LazyLoaded<Series> for navigation —
// none of these are persisted; the service layer wires them on read.
package episodefiles

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/mediainfo"
	"sonarr.tv/sonarr/internal/repo"
)

// File is one EpisodeFiles row.
//
// MediaInfo is `*mediainfo.Model` — a non-nil pointer carries a
// recorded ffprobe pass; nil means "no MediaInfo recorded yet". The
// Mapper round-trips nil ↔ NULL through the SQL boundary so
// `AllWithoutMediaInfo` (which filters on `MediaInfo IS NULL`)
// continues to match the rows the C# tree calls "missing MediaInfo".
type File struct {
	ID               int              `json:"id"`
	SeriesID         int              `json:"seriesId"`
	Quality          string           `json:"quality"`
	Size             int64            `json:"size"`
	DateAdded        time.Time        `json:"dateAdded"`
	SeasonNumber     int              `json:"seasonNumber"`
	SceneName        string           `json:"sceneName"`
	ReleaseGroup     string           `json:"releaseGroup"`
	MediaInfo        *mediainfo.Model `json:"mediaInfo"`
	RelativePath     string           `json:"relativePath"`
	OriginalFilePath string           `json:"originalFilePath"`
	Languages        []int            `json:"languages"`
	IndexerFlags     int              `json:"indexerFlags"`
	ReleaseType      int              `json:"releaseType"`
	ReleaseHash      string           `json:"releaseHash"`
}

// ErrNotFound is returned when no row matches.
var ErrNotFound = errors.New("episodefiles: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	*repo.Repository[File]
}

var mapper = repo.Mapper[File]{
	Name:  "episodefiles",
	Table: "EpisodeFiles",
	Columns: []string{
		"SeriesId", "Quality", "Size", "DateAdded", "SeasonNumber",
		"SceneName", "ReleaseGroup", "MediaInfo", "RelativePath",
		"OriginalFilePath", "Languages", "IndexerFlags", "ReleaseType",
		"ReleaseHash",
	},
	OrderBy: "Id",
	Bind: func(f *File) []any {
		mediaInfo, err := marshalMediaInfo(f.MediaInfo)
		if err != nil {
			// Bind has no error return; surface as a sentinel string the
			// repo will fail on. This is unreachable in practice (the
			// MediaInfo struct is closed and well-formed), but a panic
			// would be worse.
			mediaInfo = sql.NullString{String: `<marshal-error: ` + err.Error() + `>`, Valid: true}
		}
		return []any{
			f.SeriesID,
			defaultJSON(f.Quality, "{}"),
			f.Size, f.DateAdded.UTC(), f.SeasonNumber,
			nullableStringEmpty(f.SceneName),
			nullableStringEmpty(f.ReleaseGroup),
			mediaInfo,
			nullableStringEmpty(f.RelativePath),
			nullableStringEmpty(f.OriginalFilePath),
			marshalInts(f.Languages),
			f.IndexerFlags, f.ReleaseType,
			nullableStringEmpty(f.ReleaseHash),
		}
	},
	Scan: func(f *File, s repo.RowScanner) error {
		var sceneName, releaseGroup, mediaInfo, relativePath, originalFilePath, releaseHash sql.NullString
		var languages string
		if err := s.Scan(
			&f.ID, &f.SeriesID, &f.Quality, &f.Size, &f.DateAdded, &f.SeasonNumber,
			&sceneName, &releaseGroup, &mediaInfo, &relativePath, &originalFilePath,
			&languages, &f.IndexerFlags, &f.ReleaseType, &releaseHash,
		); err != nil {
			return err
		}
		f.DateAdded = f.DateAdded.UTC()
		f.SceneName = sceneName.String
		f.ReleaseGroup = releaseGroup.String
		parsedMediaInfo, err := unmarshalMediaInfo(mediaInfo)
		if err != nil {
			return err
		}
		f.MediaInfo = parsedMediaInfo
		f.RelativePath = relativePath.String
		f.OriginalFilePath = originalFilePath.String
		f.ReleaseHash = releaseHash.String
		if err := datastore.UnmarshalEmbedded(languages, &f.Languages); err != nil {
			return fmt.Errorf("unmarshal Languages: %w", err)
		}
		return nil
	},
	GetID: func(f *File) int { return f.ID },
	SetID: func(f *File, id int) { f.ID = id },
}

// NewRepo returns a Repo bound to the given DB.
func NewRepo(db *datastore.DB) (*Repo, error) {
	r, err := repo.New(db, mapper, ErrNotFound)
	if err != nil {
		return nil, err
	}
	return &Repo{Repository: r}, nil
}

// AllBySeriesID returns every File for the given Series, ordered by Id.
func (r *Repo) AllBySeriesID(ctx context.Context, seriesID int) ([]File, error) {
	db := r.DB()
	q := fmt.Sprintf(
		`SELECT "Id","SeriesId","Quality","Size","DateAdded","SeasonNumber","SceneName","ReleaseGroup","MediaInfo","RelativePath","OriginalFilePath","Languages","IndexerFlags","ReleaseType","ReleaseHash" FROM "EpisodeFiles" WHERE "SeriesId" = %s ORDER BY "Id"`,
		datastore.Placeholder(db.Engine, 1))
	rows, err := db.SQL.QueryContext(ctx, q, seriesID)
	if err != nil {
		return nil, fmt.Errorf("episodefiles: allBySeriesID(%d): %w", seriesID, err)
	}
	defer func() { _ = rows.Close() }()
	var out []File
	for rows.Next() {
		var f File
		if err := mapper.Scan(&f, rows); err != nil {
			return nil, fmt.Errorf("episodefiles: allBySeriesID scan: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("episodefiles: allBySeriesID rows: %w", err)
	}
	return out, nil
}

// DeleteBySeriesID issues a single `DELETE FROM "EpisodeFiles" WHERE
// "SeriesId" = ?` and returns the number of rows affected. Mirrors
// the C# cascade behaviour: when a series is deleted, every
// EpisodeFile row for that series is dropped. The C# tree does this
// via the per-event handler `MediaFileService` listens for
// `SeriesDeletedEvent` and walks the file list.
//
// IMPORTANT: this is the DB-row cascade ONLY. The C# tree also
// deletes the on-disk file via `DiskProvider.DeleteFile` when
// `deleteFiles=true` is supplied to the delete endpoint. That
// physical-disk cascade is DEFERRED — the API-layer ?deleteFiles=true
// param still parses-and-ignores the physical side; only the DB
// rows are cleaned up. Cleanup of the actual .mkv/.mp4 files is a
// follow-up slice that needs the DiskProvider wired through.
//
// Slice 5.D4 port pre-work for the eventual /series/{id}?deleteFiles=true
// cascade wired in slice 5.D5.
func (r *Repo) DeleteBySeriesID(ctx context.Context, seriesID int) (int64, error) {
	db := r.DB()
	q := fmt.Sprintf(`DELETE FROM "EpisodeFiles" WHERE "SeriesId" = %s`, datastore.Placeholder(db.Engine, 1))
	res, err := db.SQL.ExecContext(ctx, q, seriesID)
	if err != nil {
		return 0, fmt.Errorf("episodefiles: deleteBySeriesID(%d): %w", seriesID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("episodefiles: deleteBySeriesID(%d): rowsAffected: %w", seriesID, err)
	}
	return affected, nil
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

// marshalMediaInfo serialises *mediainfo.Model for the MediaInfo
// column. Nil pointer → SQL NULL (matches the C# `MediaInfo == null`
// semantic that AllWithoutMediaInfo uses to find rows needing a
// ffprobe pass).
func marshalMediaInfo(m *mediainfo.Model) (sql.NullString, error) {
	if m == nil {
		return sql.NullString{}, nil
	}
	s, err := datastore.MarshalEmbedded(m)
	if err != nil {
		return sql.NullString{}, fmt.Errorf("marshal MediaInfo: %w", err)
	}
	return sql.NullString{String: s, Valid: true}, nil
}

// unmarshalMediaInfo parses the MediaInfo column. NULL → nil pointer;
// non-NULL → fresh *mediainfo.Model. Empty string is treated as nil
// to match how legacy rows that stored "" instead of NULL upgrade
// (the C# bridge handles both as default(MediaInfoModel)).
func unmarshalMediaInfo(s sql.NullString) (*mediainfo.Model, error) {
	if !s.Valid || s.String == "" {
		return nil, nil
	}
	var m mediainfo.Model
	if err := datastore.UnmarshalEmbedded(s.String, &m); err != nil {
		return nil, fmt.Errorf("unmarshal MediaInfo: %w", err)
	}
	return &m, nil
}
