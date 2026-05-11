// Package metadatafiles is the Go port of NzbDrone.Core.Extras.Metadata
// .Files.MetadataFile persistence. Each row is a metadata sidecar
// (Kodi/Emby NFO, Plex .info, etc.) the metadata writer produced and
// the scanner tracks for cleanup. Consumer is the C# class name of the
// producing writer.
//
// Schema at HEAD (v230): Id, SeriesId NOT NULL, Consumer NOT NULL,
// Type NOT NULL INT, RelativePath NOT NULL, LastUpdated NOT NULL,
// SeasonNumber nullable, EpisodeFileId nullable, Hash nullable,
// Added nullable, Extension NOT NULL.
//
// Consumer is the metadata-writer class name string (e.g.
// "XbmcMetadata", "RoksboxMetadata"). It uses the same C#-class-name
// registry pattern as the provider tables (TP-Q1/Q2); the Go port will
// keep these as opaque string IDs once the writer registry lands.
package metadatafiles

import (
	"database/sql"
	"errors"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/repo"
)

// MetadataFile is one MetadataFiles row.
type MetadataFile struct {
	ID            int        `json:"id"`
	SeriesID      int        `json:"seriesId"`
	Consumer      string     `json:"consumer"`
	Type          int        `json:"type"`
	RelativePath  string     `json:"relativePath"`
	LastUpdated   time.Time  `json:"lastUpdated"`
	SeasonNumber  *int       `json:"seasonNumber"`
	EpisodeFileID *int       `json:"episodeFileId"`
	Hash          string     `json:"hash"`
	Added         *time.Time `json:"added"`
	Extension     string     `json:"extension"`
}

// ErrNotFound is returned when no row matches.
var ErrNotFound = errors.New("metadatafiles: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	*repo.Repository[MetadataFile]
}

var mapper = repo.Mapper[MetadataFile]{
	Name:  "metadatafiles",
	Table: "MetadataFiles",
	Columns: []string{
		"SeriesId", "Consumer", "Type", "RelativePath", "LastUpdated",
		"SeasonNumber", "EpisodeFileId", "Hash", "Added", "Extension",
	},
	OrderBy: "Id",
	Bind: func(f *MetadataFile) []any {
		return []any{
			f.SeriesID, f.Consumer, f.Type, f.RelativePath, f.LastUpdated.UTC(),
			nullableInt(f.SeasonNumber),
			nullableInt(f.EpisodeFileID),
			nullableStringEmpty(f.Hash),
			nullableTime(f.Added),
			f.Extension,
		}
	},
	Scan: func(f *MetadataFile, s repo.RowScanner) error {
		var seasonNumber, episodeFileID sql.NullInt64
		var hash sql.NullString
		var added sql.NullTime
		if err := s.Scan(
			&f.ID, &f.SeriesID, &f.Consumer, &f.Type, &f.RelativePath, &f.LastUpdated,
			&seasonNumber, &episodeFileID, &hash, &added, &f.Extension,
		); err != nil {
			return err
		}
		f.LastUpdated = f.LastUpdated.UTC()
		if seasonNumber.Valid {
			v := int(seasonNumber.Int64)
			f.SeasonNumber = &v
		}
		if episodeFileID.Valid {
			v := int(episodeFileID.Int64)
			f.EpisodeFileID = &v
		}
		f.Hash = hash.String
		if added.Valid {
			v := added.Time.UTC()
			f.Added = &v
		}
		return nil
	},
	GetID: func(f *MetadataFile) int { return f.ID },
	SetID: func(f *MetadataFile, id int) { f.ID = id },
}

// NewRepo returns a Repo bound to the given DB.
func NewRepo(db *datastore.DB) (*Repo, error) {
	r, err := repo.New(db, mapper, ErrNotFound)
	if err != nil {
		return nil, err
	}
	return &Repo{Repository: r}, nil
}

func nullableInt(p *int) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*p), Valid: true}
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
