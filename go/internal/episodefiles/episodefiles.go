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
// (`[]int` per the catalogue's language-int-form). Quality + MediaInfo
// are stored as raw `string` until Phase 2 ports the typed wrappers.
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
	"sonarr.tv/sonarr/internal/repo"
)

// File is one EpisodeFiles row.
type File struct {
	ID               int       `json:"id"`
	SeriesID         int       `json:"seriesId"`
	Quality          string    `json:"quality"`
	Size             int64     `json:"size"`
	DateAdded        time.Time `json:"dateAdded"`
	SeasonNumber     int       `json:"seasonNumber"`
	SceneName        string    `json:"sceneName,omitempty"`
	ReleaseGroup     string    `json:"releaseGroup,omitempty"`
	MediaInfo        string    `json:"mediaInfo,omitempty"`
	RelativePath     string    `json:"relativePath,omitempty"`
	OriginalFilePath string    `json:"originalFilePath,omitempty"`
	Languages        []int     `json:"languages"`
	IndexerFlags     int       `json:"indexerFlags"`
	ReleaseType      int       `json:"releaseType"`
	ReleaseHash      string    `json:"releaseHash,omitempty"`
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
		return []any{
			f.SeriesID,
			defaultJSON(f.Quality, "{}"),
			f.Size, f.DateAdded.UTC(), f.SeasonNumber,
			nullableStringEmpty(f.SceneName),
			nullableStringEmpty(f.ReleaseGroup),
			nullableStringEmpty(f.MediaInfo),
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
		f.MediaInfo = mediaInfo.String
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
