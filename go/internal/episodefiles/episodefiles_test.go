package episodefiles_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/mediainfo"
)

func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := episodefiles.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}

	all, err := repo.All(t.Context())
	if err != nil {
		t.Fatalf("All initial: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("All initial: got %d, want 0", len(all))
	}

	now := time.Date(2026, 5, 2, 14, 30, 0, 0, time.UTC)

	mediaInfo := &mediainfo.Model{
		ContainerFormat: "Matroska",
		VideoFormat:     "AVC",
		VideoCodecID:    "V_MPEG4/ISO/AVC",
		VideoBitrate:    8_000_000,
		Width:           1920,
		Height:          1080,
		AudioStreams: []mediainfo.AudioStream{
			{Format: "AAC", Channels: 2, Language: "eng"},
		},
	}
	full := &episodefiles.File{
		SeriesID:         50,
		Quality:          `{"quality":{"id":7,"name":"WEBDL-1080p"},"revision":{"version":1}}`,
		Size:             3_000_000_000,
		DateAdded:        now,
		SeasonNumber:     1,
		SceneName:        "Foo.S01E01.1080p.WEB-DL.RARBG",
		ReleaseGroup:     "RARBG",
		MediaInfo:        mediaInfo,
		RelativePath:     "Season 1/Foo.S01E01.mkv",
		OriginalFilePath: "Foo.S01E01.1080p.WEB-DL.x264-RARBG.mkv",
		Languages:        []int{1, 2},
		IndexerFlags:     1,
		ReleaseType:      0,
		ReleaseHash:      "abc123def",
	}
	if _, err := repo.Insert(t.Context(), full); err != nil {
		t.Fatalf("Insert full: %v", err)
	}

	sparse := &episodefiles.File{
		SeriesID:     50,
		Size:         1_000_000_000,
		DateAdded:    now,
		SeasonNumber: 1,
		Languages:    []int{},
	}
	if _, err := repo.Insert(t.Context(), sparse); err != nil {
		t.Fatalf("Insert sparse: %v", err)
	}

	other := &episodefiles.File{
		SeriesID:     99,
		Size:         500_000_000,
		DateAdded:    now,
		SeasonNumber: 1,
		Languages:    []int{},
	}
	if _, err := repo.Insert(t.Context(), other); err != nil {
		t.Fatalf("Insert other: %v", err)
	}

	got, err := repo.Get(t.Context(), full.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.SceneName != full.SceneName {
		t.Errorf("SceneName: %q", got.SceneName)
	}
	if got.MediaInfo == nil {
		t.Fatalf("MediaInfo: nil, want round-tripped struct")
	}
	if !reflect.DeepEqual(got.MediaInfo, mediaInfo) {
		t.Errorf("MediaInfo: %+v, want %+v", got.MediaInfo, mediaInfo)
	}
	if !reflect.DeepEqual(got.Languages, []int{1, 2}) {
		t.Errorf("Languages: %v", got.Languages)
	}
	if got.Size != 3_000_000_000 {
		t.Errorf("Size (bigint test): %d", got.Size)
	}

	gotSparse, err := repo.Get(t.Context(), sparse.ID)
	if err != nil {
		t.Fatalf("Get sparse: %v", err)
	}
	if gotSparse.SceneName != "" || gotSparse.MediaInfo != nil || gotSparse.RelativePath != "" {
		t.Errorf("sparse nullables: %+v", gotSparse)
	}
	if gotSparse.Quality != "{}" {
		t.Errorf("sparse Quality: %q, want default '{}'", gotSparse.Quality)
	}

	bySeries50, err := repo.AllBySeriesID(t.Context(), 50)
	if err != nil {
		t.Fatalf("AllBySeriesID(50): %v", err)
	}
	if len(bySeries50) != 2 {
		t.Errorf("series 50: got %d, want 2", len(bySeries50))
	}

	if err := repo.Update(t.Context(), &episodefiles.File{ID: 99999, Languages: []int{}, DateAdded: now}); !errors.Is(err, episodefiles.ErrNotFound) {
		t.Errorf("Update(missing): %v", err)
	}
	if err := repo.Delete(t.Context(), full.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestRepo_SQLite(t *testing.T) {
	t.Parallel()
	runRepoSuite(t, datastoretest.OpenSQLite(t))
}

func TestRepo_Postgres(t *testing.T) {
	runRepoSuite(t, datastoretest.OpenPostgres(t))
}
