package housekeeping_test

import (
	"log/slog"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/extrafiles"
	"sonarr.tv/sonarr/internal/housekeeping"
	"sonarr.tv/sonarr/internal/metadatafiles"
	"sonarr.tv/sonarr/internal/pendingreleases"
	"sonarr.tv/sonarr/internal/series"
	"sonarr.tv/sonarr/internal/subtitlefiles"
)

func seedSeriesAndFile(t *testing.T, db *datastore.DB) (keep, dropped *series.Series, keepFile *episodefiles.File) {
	t.Helper()
	now := time.Now().UTC()
	seriesRepo, err := series.NewRepo(db)
	if err != nil {
		t.Fatalf("series.NewRepo: %v", err)
	}
	added := now
	keep = &series.Series{TvdbID: 300, Title: "Live", CleanTitle: "live", TitleSlug: "live", Path: "/tv/live", Added: &added, Status: 1, Tags: []int{}, MalIDs: []int{}, Images: "[]", Seasons: "[]"}
	dropped = &series.Series{TvdbID: 301, Title: "Gone", CleanTitle: "gone", TitleSlug: "gone", Path: "/tv/gone", Added: &added, Status: 1, Tags: []int{}, MalIDs: []int{}, Images: "[]", Seasons: "[]"}
	if _, err := seriesRepo.Insert(t.Context(), keep); err != nil {
		t.Fatalf("series Insert keep: %v", err)
	}
	if _, err := seriesRepo.Insert(t.Context(), dropped); err != nil {
		t.Fatalf("series Insert dropped: %v", err)
	}
	filesRepo, err := episodefiles.NewRepo(db)
	if err != nil {
		t.Fatalf("episodefiles.NewRepo: %v", err)
	}
	keepFile = &episodefiles.File{
		SeriesID:     keep.ID,
		SeasonNumber: 1,
		RelativePath: "k.mkv",
		Quality:      "{}",
		Size:         1,
		DateAdded:    now,
		Languages:    []int{},
	}
	if _, err := filesRepo.Insert(t.Context(), keepFile); err != nil {
		t.Fatalf("episodefiles Insert keep: %v", err)
	}
	if err := seriesRepo.Delete(t.Context(), dropped.ID); err != nil {
		t.Fatalf("series Delete dropped: %v", err)
	}
	return keep, dropped, keepFile
}

func TestCleanupOrphanedPendingReleases_Sqlite(t *testing.T) {
	t.Parallel()
	runPendingReleasesSuite(t, datastoretest.OpenSQLite(t))
}

func TestCleanupOrphanedPendingReleases_Postgres(t *testing.T) {
	runPendingReleasesSuite(t, datastoretest.OpenPostgres(t))
}

func runPendingReleasesSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	keep, dropped, _ := seedSeriesAndFile(t, db)

	prRepo, err := pendingreleases.NewRepo(db)
	if err != nil {
		t.Fatalf("pendingreleases.NewRepo: %v", err)
	}
	now := time.Now().UTC()
	survivor := &pendingreleases.Release{SeriesID: keep.ID, Title: "s", Added: now, ParsedEpisodeInfo: "{}", ReleaseInfo: "{}"}
	orphan := &pendingreleases.Release{SeriesID: dropped.ID, Title: "o", Added: now, ParsedEpisodeInfo: "{}", ReleaseInfo: "{}"}
	if _, err := prRepo.Insert(t.Context(), survivor); err != nil {
		t.Fatalf("pendingreleases Insert survivor: %v", err)
	}
	if _, err := prRepo.Insert(t.Context(), orphan); err != nil {
		t.Fatalf("pendingreleases Insert orphan: %v", err)
	}

	task, err := housekeeping.NewCleanupOrphanedPendingReleases(db, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}

	if _, err := prRepo.Get(t.Context(), orphan.ID); err == nil {
		t.Errorf("orphan PendingRelease survived")
	}
	if _, err := prRepo.Get(t.Context(), survivor.ID); err != nil {
		t.Errorf("survivor PendingRelease dropped: %v", err)
	}
}

func TestCleanupOrphanedExtraFiles_Sqlite(t *testing.T) {
	t.Parallel()
	runExtraFilesSuite(t, datastoretest.OpenSQLite(t))
}

func TestCleanupOrphanedExtraFiles_Postgres(t *testing.T) {
	runExtraFilesSuite(t, datastoretest.OpenPostgres(t))
}

func runExtraFilesSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	keep, dropped, keepFile := seedSeriesAndFile(t, db)
	now := time.Now().UTC()

	exRepo, err := extrafiles.NewRepo(db)
	if err != nil {
		t.Fatalf("extrafiles.NewRepo: %v", err)
	}
	survivor := &extrafiles.ExtraFile{SeriesID: keep.ID, SeasonNumber: 1, EpisodeFileID: keepFile.ID, RelativePath: "s.png", Extension: ".png", Added: now, LastUpdated: now}
	seriesOrphan := &extrafiles.ExtraFile{SeriesID: dropped.ID, SeasonNumber: 1, EpisodeFileID: keepFile.ID, RelativePath: "so.png", Extension: ".png", Added: now, LastUpdated: now}
	fileOrphan := &extrafiles.ExtraFile{SeriesID: keep.ID, SeasonNumber: 1, EpisodeFileID: 9999, RelativePath: "fo.png", Extension: ".png", Added: now, LastUpdated: now}
	unlinked := &extrafiles.ExtraFile{SeriesID: keep.ID, SeasonNumber: 1, EpisodeFileID: 0, RelativePath: "u.png", Extension: ".png", Added: now, LastUpdated: now}
	for i, row := range []*extrafiles.ExtraFile{survivor, seriesOrphan, fileOrphan, unlinked} {
		if _, err := exRepo.Insert(t.Context(), row); err != nil {
			t.Fatalf("extrafiles Insert idx=%d: %v", i, err)
		}
	}

	task, err := housekeeping.NewCleanupOrphanedExtraFiles(db, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}

	for _, gone := range []int{seriesOrphan.ID, fileOrphan.ID, unlinked.ID} {
		if _, err := exRepo.Get(t.Context(), gone); err == nil {
			t.Errorf("ExtraFile id=%d survived but should be gone", gone)
		}
	}
	if _, err := exRepo.Get(t.Context(), survivor.ID); err != nil {
		t.Errorf("survivor ExtraFile dropped: %v", err)
	}
}

func TestCleanupOrphanedSubtitleFiles_Sqlite(t *testing.T) {
	t.Parallel()
	runSubtitleFilesSuite(t, datastoretest.OpenSQLite(t))
}

func TestCleanupOrphanedSubtitleFiles_Postgres(t *testing.T) {
	runSubtitleFilesSuite(t, datastoretest.OpenPostgres(t))
}

func runSubtitleFilesSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	keep, dropped, keepFile := seedSeriesAndFile(t, db)
	now := time.Now().UTC()

	sfRepo, err := subtitlefiles.NewRepo(db)
	if err != nil {
		t.Fatalf("subtitlefiles.NewRepo: %v", err)
	}
	mk := func(seriesID, fileID int, name string) *subtitlefiles.SubtitleFile {
		return &subtitlefiles.SubtitleFile{
			SeriesID: seriesID, SeasonNumber: 1, EpisodeFileID: fileID,
			RelativePath: name, Extension: ".srt", LastUpdated: now,
			Language: 1, LanguageTags: []string{},
		}
	}
	survivor := mk(keep.ID, keepFile.ID, "s.srt")
	seriesOrphan := mk(dropped.ID, keepFile.ID, "so.srt")
	fileOrphan := mk(keep.ID, 9999, "fo.srt")
	unlinked := mk(keep.ID, 0, "u.srt")
	for i, row := range []*subtitlefiles.SubtitleFile{survivor, seriesOrphan, fileOrphan, unlinked} {
		if _, err := sfRepo.Insert(t.Context(), row); err != nil {
			t.Fatalf("subtitlefiles Insert idx=%d: %v", i, err)
		}
	}

	task, err := housekeeping.NewCleanupOrphanedSubtitleFiles(db, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}

	for _, gone := range []int{seriesOrphan.ID, fileOrphan.ID, unlinked.ID} {
		if _, err := sfRepo.Get(t.Context(), gone); err == nil {
			t.Errorf("SubtitleFile id=%d survived but should be gone", gone)
		}
	}
	if _, err := sfRepo.Get(t.Context(), survivor.ID); err != nil {
		t.Errorf("survivor SubtitleFile dropped: %v", err)
	}
}

func TestCleanupOrphanedMetadataFiles_Sqlite(t *testing.T) {
	t.Parallel()
	runMetadataFilesSuite(t, datastoretest.OpenSQLite(t))
}

func TestCleanupOrphanedMetadataFiles_Postgres(t *testing.T) {
	runMetadataFilesSuite(t, datastoretest.OpenPostgres(t))
}

func runMetadataFilesSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	keep, dropped, keepFile := seedSeriesAndFile(t, db)
	now := time.Now().UTC()

	mfRepo, err := metadatafiles.NewRepo(db)
	if err != nil {
		t.Fatalf("metadatafiles.NewRepo: %v", err)
	}
	mk := func(seriesID, fileID, mtype int, name string) *metadatafiles.MetadataFile {
		f := &metadatafiles.MetadataFile{
			SeriesID:     seriesID,
			Consumer:     "Kodi",
			Type:         mtype,
			RelativePath: name,
			LastUpdated:  now,
			Extension:    ".nfo",
		}
		if fileID > 0 {
			id := fileID
			f.EpisodeFileID = &id
		} else {
			zero := 0
			f.EpisodeFileID = &zero
		}
		return f
	}
	// Type 5 = EpisodeMetadata (episode-scoped). Survivor = good
	// link. seriesOrphan = dangling SeriesId. fileOrphan = good
	// SeriesId, dangling EpisodeFileId. unlinkedEpisode = Type 5 +
	// EpisodeFileId=0 (gets swept). unlinkedSeries = Type 1 + EpisodeFileId=0
	// (legitimate, survives).
	survivor := mk(keep.ID, keepFile.ID, 5, "s.nfo")
	seriesOrphan := mk(dropped.ID, keepFile.ID, 5, "so.nfo")
	fileOrphan := mk(keep.ID, 9999, 5, "fo.nfo")
	unlinkedEpisode := mk(keep.ID, 0, 5, "ue.nfo")
	unlinkedSeries := mk(keep.ID, 0, 1, "us.nfo")
	for i, row := range []*metadatafiles.MetadataFile{survivor, seriesOrphan, fileOrphan, unlinkedEpisode, unlinkedSeries} {
		if _, err := mfRepo.Insert(t.Context(), row); err != nil {
			t.Fatalf("metadatafiles Insert idx=%d: %v", i, err)
		}
	}

	task, err := housekeeping.NewCleanupOrphanedMetadataFiles(db, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}

	for _, gone := range []int{seriesOrphan.ID, fileOrphan.ID, unlinkedEpisode.ID} {
		if _, err := mfRepo.Get(t.Context(), gone); err == nil {
			t.Errorf("MetadataFile id=%d survived but should be gone", gone)
		}
	}
	for _, alive := range []int{survivor.ID, unlinkedSeries.ID} {
		if _, err := mfRepo.Get(t.Context(), alive); err != nil {
			t.Errorf("MetadataFile id=%d dropped but should survive: %v", alive, err)
		}
	}
}

func TestNewFileRefHousekeepers_NilDB(t *testing.T) {
	t.Parallel()
	if _, err := housekeeping.NewCleanupOrphanedExtraFiles(nil, nil); err == nil {
		t.Errorf("ExtraFiles(nil): want error")
	}
	if _, err := housekeeping.NewCleanupOrphanedSubtitleFiles(nil, nil); err == nil {
		t.Errorf("SubtitleFiles(nil): want error")
	}
	if _, err := housekeeping.NewCleanupOrphanedMetadataFiles(nil, nil); err == nil {
		t.Errorf("MetadataFiles(nil): want error")
	}
	if _, err := housekeeping.NewCleanupOrphanedPendingReleases(nil, nil); err == nil {
		t.Errorf("PendingReleases(nil): want error")
	}
}
