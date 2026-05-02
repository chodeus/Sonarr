// Package extrafiles is the Go port of NzbDrone.Core.Extras.Files.ExtraFile
// persistence (the base abstract class — concrete subclasses live in
// SubtitleFile + MetadataFile + Other extras tables; this package owns
// the ExtraFiles table proper, used for catch-all "other" extras the
// scanner discovers next to episode files).
//
// Schema at HEAD (v230): Id, SeriesId NOT NULL, SeasonNumber NOT NULL,
// EpisodeFileId NOT NULL, RelativePath NOT NULL, Extension NOT NULL,
// Added NOT NULL, LastUpdated NOT NULL.
//
// The schema makes SeasonNumber and EpisodeFileId NOT NULL even though
// the C# base class declares them as `int?`. Dapper tolerates the
// over-specification; the persistence shape is the authoritative one,
// and this package treats them as plain int.
package extrafiles

import (
	"errors"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/repo"
)

// ExtraFile is one ExtraFiles row.
type ExtraFile struct {
	ID            int       `json:"id"`
	SeriesID      int       `json:"seriesId"`
	SeasonNumber  int       `json:"seasonNumber"`
	EpisodeFileID int       `json:"episodeFileId"`
	RelativePath  string    `json:"relativePath"`
	Extension     string    `json:"extension"`
	Added         time.Time `json:"added"`
	LastUpdated   time.Time `json:"lastUpdated"`
}

// ErrNotFound is returned when no row matches.
var ErrNotFound = errors.New("extrafiles: not found")

// Repo is the CRUD wrapper.
type Repo struct {
	*repo.Repository[ExtraFile]
}

var mapper = repo.Mapper[ExtraFile]{
	Name:    "extrafiles",
	Table:   "ExtraFiles",
	Columns: []string{"SeriesId", "SeasonNumber", "EpisodeFileId", "RelativePath", "Extension", "Added", "LastUpdated"},
	OrderBy: "Id",
	Bind: func(f *ExtraFile) []any {
		return []any{
			f.SeriesID, f.SeasonNumber, f.EpisodeFileID, f.RelativePath,
			f.Extension, f.Added.UTC(), f.LastUpdated.UTC(),
		}
	},
	Scan: func(f *ExtraFile, s repo.RowScanner) error {
		if err := s.Scan(
			&f.ID, &f.SeriesID, &f.SeasonNumber, &f.EpisodeFileID,
			&f.RelativePath, &f.Extension, &f.Added, &f.LastUpdated,
		); err != nil {
			return err
		}
		f.Added = f.Added.UTC()
		f.LastUpdated = f.LastUpdated.UTC()
		return nil
	},
	GetID: func(f *ExtraFile) int { return f.ID },
	SetID: func(f *ExtraFile, id int) { f.ID = id },
}

// NewRepo returns a Repo bound to the given DB.
func NewRepo(db *datastore.DB) (*Repo, error) {
	r, err := repo.New(db, mapper, ErrNotFound)
	if err != nil {
		return nil, err
	}
	return &Repo{Repository: r}, nil
}
