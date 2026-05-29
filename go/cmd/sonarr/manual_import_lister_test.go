package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/disk"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

// J387a: mapDecision projects an ImportDecision → ManualImportResource.
// Uses a REAL file on disk so GetFileSize returns the true size (the
// round-trip disk assertion), and asserts every mapped field including
// the single-season Episodes embed + Series projection + rejection.
func TestManualImportMapDecision_J387a(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "Show.S01E01.WEBDL-1080p.mkv")
	content := []byte("0123456789") // 10 bytes
	if err := os.WriteFile(file, content, 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	q, _ := quality.FindByID(1) // SDTV
	eng, _ := language.FindByID(1)
	le := episodeimport.NewLocalEpisode()
	le.Path = file
	le.Series = &series.Series{ID: 42, Path: dir, Title: "Show"}
	le.Episodes = []episodes.Episode{
		{ID: 100, SeriesID: 42, SeasonNumber: 1, EpisodeNumber: 1, Title: "Pilot"},
		{ID: 101, SeriesID: 42, SeasonNumber: 1, EpisodeNumber: 2, Title: "Two"},
	}
	le.Quality = quality.NewModel(q)
	le.Languages = []language.Language{eng}
	le.ReleaseGroup = "SONARR"
	le.IndexerFlags = parser.IndexerFlags(2)
	le.ReleaseType = parser.ReleaseTypeSingleEpisode

	decision := episodeimport.NewImportDecision(le,
		episodeimport.NewImportRejection(episodeimport.ImportRejectionError, "boom"))

	a := manualImportDecisionAdapter{disk: disk.NewProvider(slog.Default()), log: slog.Default()}
	res := a.mapDecision(decision, dir, "dl1", "DownloadFolder")

	if res.ID != hashInt31(file) {
		t.Errorf("ID = %d, want hashInt31(path) = %d", res.ID, hashInt31(file))
	}
	if res.Path != file {
		t.Errorf("Path = %q", res.Path)
	}
	if res.RelativePath != "Show.S01E01.WEBDL-1080p.mkv" {
		t.Errorf("RelativePath = %q", res.RelativePath)
	}
	if res.Name != "Show.S01E01.WEBDL-1080p" {
		t.Errorf("Name = %q", res.Name)
	}
	if res.FolderName != "DownloadFolder" {
		t.Errorf("FolderName = %q", res.FolderName)
	}
	if res.DownloadID != "dl1" {
		t.Errorf("DownloadID = %q", res.DownloadID)
	}
	if res.Size != 10 {
		t.Errorf("Size = %d, want 10 (real file size)", res.Size)
	}
	if res.SeasonNumber == nil || *res.SeasonNumber != 1 {
		t.Errorf("SeasonNumber = %v, want 1", res.SeasonNumber)
	}
	if len(res.Episodes) != 2 {
		t.Errorf("Episodes len = %d, want 2", len(res.Episodes))
	}
	if res.Series == nil || res.Series.Title != "Show" {
		t.Errorf("Series = %+v, want title Show", res.Series)
	}
	if res.ReleaseGroup != "SONARR" {
		t.Errorf("ReleaseGroup = %q", res.ReleaseGroup)
	}
	if res.IndexerFlags != 2 {
		t.Errorf("IndexerFlags = %d", res.IndexerFlags)
	}
	if res.ReleaseType != apiv3.CamelCaseEnumName(parser.ReleaseTypeSingleEpisode.String()) {
		t.Errorf("ReleaseType = %q", res.ReleaseType)
	}
	if res.Quality == nil || res.Quality.Quality.ID != 1 {
		t.Errorf("Quality = %+v, want id 1", res.Quality)
	}
	if len(res.Languages) != 1 || res.Languages[0].ID != 1 {
		t.Errorf("Languages = %v, want [id:1]", res.Languages)
	}
	if len(res.Rejections) != 1 || res.Rejections[0].Reason != "boom" || res.Rejections[0].Type != "permanent" {
		t.Errorf("Rejections = %+v", res.Rejections)
	}
}

// Multi-season episodes must NOT collapse to a single SeasonNumber +
// Episodes embed (C# `:424` distinct-season guard).
func TestManualImportMapDecision_MultiSeasonNoEmbed_J387a(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "pack.mkv")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	le := episodeimport.NewLocalEpisode()
	le.Path = file
	le.Series = &series.Series{ID: 1, Path: dir, Title: "S"}
	le.Episodes = []episodes.Episode{
		{ID: 1, SeasonNumber: 1, EpisodeNumber: 1},
		{ID: 2, SeasonNumber: 2, EpisodeNumber: 1},
	}
	a := manualImportDecisionAdapter{disk: disk.NewProvider(slog.Default()), log: slog.Default()}
	res := a.mapDecision(episodeimport.NewImportDecision(le), dir, "", "")
	if res.SeasonNumber != nil {
		t.Errorf("SeasonNumber = %v, want nil for multi-season", res.SeasonNumber)
	}
	if len(res.Episodes) != 0 {
		t.Errorf("Episodes len = %d, want 0 for multi-season", len(res.Episodes))
	}
}

// GetMediaFiles guards: nil idm/disk → empty (non-nil) list; the
// library + downloadId modes are deferred to J387b and return empty.
func TestManualImportGetMediaFiles_Guards_J387a(t *testing.T) {
	t.Parallel()
	// nil idm → empty.
	a := manualImportDecisionAdapter{log: slog.Default()}
	got, err := a.GetMediaFiles(t.Context(), apiv3.ManualImportQuery{Folder: "/dl"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("nil-idm GetMediaFiles = %v, want non-nil empty", got)
	}
}

// J387b: mapEpisodeFile projects an existing episode-file row (library
// mode). Real temp file for size; asserts the persisted-quality decode,
// language-id projection, EpisodeFileId, single-season, and the Episodes
// embed filtered to those linked to this file.
func TestManualImportMapEpisodeFile_J387b(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rel := "Season 01/Show.S01E01.mkv"
	if err := os.MkdirAll(filepath.Join(dir, "Season 01"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, rel), []byte("12345"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	fileID := 77
	f := episodefiles.File{
		ID: fileID, SeriesID: 42, SeasonNumber: 1, RelativePath: rel,
		Quality:      `{"quality":1,"revision":{"version":1,"real":0,"isRepack":false}}`,
		ReleaseGroup: "GRP", Languages: []int{1}, IndexerFlags: 4, ReleaseType: int(parser.ReleaseTypeSingleEpisode),
	}
	other := 99
	allEps := []episodes.Episode{
		{ID: 1, SeasonNumber: 1, EpisodeNumber: 1, EpisodeFileID: &fileID},
		{ID: 2, SeasonNumber: 1, EpisodeNumber: 2, EpisodeFileID: &other}, // different file → excluded
		{ID: 3, SeasonNumber: 1, EpisodeNumber: 3},                        // no file → excluded
	}
	a := manualImportDecisionAdapter{disk: disk.NewProvider(slog.Default()), log: slog.Default()}
	res := a.mapEpisodeFile(f, &series.Series{ID: 42, Path: dir, Title: "Show"}, "ShowFolder", allEps)

	if res.RelativePath != rel {
		t.Errorf("RelativePath = %q", res.RelativePath)
	}
	if res.EpisodeFileID == nil || *res.EpisodeFileID != fileID {
		t.Errorf("EpisodeFileID = %v, want %d", res.EpisodeFileID, fileID)
	}
	if res.SeasonNumber == nil || *res.SeasonNumber != 1 {
		t.Errorf("SeasonNumber = %v, want 1", res.SeasonNumber)
	}
	if res.Size != 5 {
		t.Errorf("Size = %d, want 5", res.Size)
	}
	if res.Quality == nil || res.Quality.Quality.ID != 1 {
		t.Errorf("Quality = %+v, want id 1 (decoded from persisted column)", res.Quality)
	}
	if len(res.Languages) != 1 || res.Languages[0].ID != 1 {
		t.Errorf("Languages = %v, want [id:1]", res.Languages)
	}
	if len(res.Episodes) != 1 || res.Episodes[0].ID != 1 {
		t.Errorf("Episodes = %+v, want only the file-linked episode id 1", res.Episodes)
	}
	if res.ReleaseGroup != "GRP" || res.IndexerFlags != 4 {
		t.Errorf("ReleaseGroup/IndexerFlags = %q/%d", res.ReleaseGroup, res.IndexerFlags)
	}
	if res.Series == nil || res.Series.Title != "Show" {
		t.Errorf("Series = %+v", res.Series)
	}
}

// J387b: mapUnmappedFile projects an on-disk file with no episode-file
// row — Unknown quality + language, no season, no EpisodeFileId.
func TestManualImportMapUnmappedFile_J387b(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "stray.mkv")
	if err := os.WriteFile(file, []byte("ab"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	a := manualImportDecisionAdapter{disk: disk.NewProvider(slog.Default()), log: slog.Default()}
	res := a.mapUnmappedFile(file, &series.Series{ID: 1, Path: dir, Title: "S"}, "S")

	if res.EpisodeFileID != nil {
		t.Errorf("EpisodeFileID = %v, want nil for unmapped", res.EpisodeFileID)
	}
	if res.SeasonNumber != nil {
		t.Errorf("SeasonNumber = %v, want nil for unmapped", res.SeasonNumber)
	}
	if res.Quality == nil || res.Quality.Quality.ID != 0 {
		t.Errorf("Quality = %+v, want Unknown (id 0)", res.Quality)
	}
	if len(res.Languages) != 1 || res.Languages[0].ID != 0 {
		t.Errorf("Languages = %v, want [Unknown]", res.Languages)
	}
	if res.Size != 2 {
		t.Errorf("Size = %d, want 2", res.Size)
	}
	if res.Series == nil {
		t.Error("Series should be set on unmapped item")
	}
}

// J387b: library-mode round-trip on a real SQLite DB + real disk. Seeds a
// series, one mapped episode file + its episode, and writes both the
// mapped file and an unmapped file under the series folder. GetMediaFiles
// (seriesId, no season) must return the mapped row (with its episode embed)
// plus the unmapped stray as an Unknown-quality candidate.
func TestManualImportLibraryMode_J387b(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	bus := messaging.New(slog.Default())
	seriesRepo, err := series.NewRepo(db)
	if err != nil {
		t.Fatalf("series.NewRepo: %v", err)
	}
	seriesSvc, err := series.NewService(seriesRepo, bus)
	if err != nil {
		t.Fatalf("series.NewService: %v", err)
	}
	episodesRepo, err := episodes.NewRepo(db)
	if err != nil {
		t.Fatalf("episodes.NewRepo: %v", err)
	}
	episodesSvc, err := episodes.NewService(episodesRepo, bus)
	if err != nil {
		t.Fatalf("episodes.NewService: %v", err)
	}
	efRepo, err := episodefiles.NewRepo(db)
	if err != nil {
		t.Fatalf("episodefiles.NewRepo: %v", err)
	}
	efSvc, err := episodefiles.NewService(efRepo, bus)
	if err != nil {
		t.Fatalf("episodefiles.NewService: %v", err)
	}
	ctx := t.Context()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Show.S01E01.mkv"), []byte("mapped"), 0o644); err != nil {
		t.Fatalf("write mapped: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Show.S01E02.mkv"), []byte("stray"), 0o644); err != nil {
		t.Fatalf("write stray: %v", err)
	}

	ser := &series.Series{TvdbID: 5, Title: "Show", CleanTitle: "show", Path: dir, Monitored: true, Runtime: 30}
	if _, err := seriesRepo.Insert(ctx, ser); err != nil {
		t.Fatalf("insert series: %v", err)
	}
	stored, err := efSvc.Add(ctx, &episodefiles.File{
		SeriesID: ser.ID, SeasonNumber: 1, RelativePath: "Show.S01E01.mkv",
		Quality: `{"quality":1,"revision":{"version":1}}`, Languages: []int{1},
	})
	if err != nil {
		t.Fatalf("add episodefile: %v", err)
	}
	if _, err := episodesRepo.Insert(ctx, &episodes.Episode{
		SeriesID: ser.ID, SeasonNumber: 1, EpisodeNumber: 1, Title: "Pilot", EpisodeFileID: &stored.ID,
	}); err != nil {
		t.Fatalf("insert episode: %v", err)
	}

	a := manualImportDecisionAdapter{
		series: seriesSvc, episodes: episodesSvc, episodeFiles: efSvc,
		disk: disk.NewProvider(slog.Default()), log: slog.Default(),
	}
	res, err := a.GetMediaFiles(ctx, apiv3.ManualImportQuery{SeriesID: &ser.ID})
	if err != nil {
		t.Fatalf("GetMediaFiles: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("got %d rows, want 2 (1 mapped + 1 unmapped); rows=%+v", len(res), res)
	}

	var mapped, unmapped *apiv3.ManualImportResource
	for i := range res {
		if res[i].EpisodeFileID != nil {
			mapped = &res[i]
		} else {
			unmapped = &res[i]
		}
	}
	if mapped == nil || mapped.RelativePath != "Show.S01E01.mkv" {
		t.Fatalf("mapped row missing/wrong: %+v", mapped)
	}
	if len(mapped.Episodes) != 1 || mapped.Episodes[0].ID == 0 {
		t.Errorf("mapped row Episodes = %+v, want the linked episode", mapped.Episodes)
	}
	if mapped.Quality == nil || mapped.Quality.Quality.ID != 1 {
		t.Errorf("mapped Quality = %+v, want id 1", mapped.Quality)
	}
	if unmapped == nil || filepath.Base(unmapped.Path) != "Show.S01E02.mkv" {
		t.Fatalf("unmapped row missing/wrong: %+v", unmapped)
	}
	if unmapped.Quality == nil || unmapped.Quality.Quality.ID != 0 {
		t.Errorf("unmapped Quality = %+v, want Unknown", unmapped.Quality)
	}
}

// J387b: downloadId mode with an empty tracked-download cache → Find
// returns nil → empty list (the production-reachable path until Phase 4
// download-client wiring populates the cache).
func TestManualImportDownloadIdMode_NilTracked_J387b(t *testing.T) {
	t.Parallel()
	a := manualImportDecisionAdapter{
		idm:     &episodeimport.ImportDecisionMaker{},
		disk:    disk.NewProvider(slog.Default()),
		tracked: trackeddownloads.NewService(slog.Default()),
		log:     slog.Default(),
	}
	got, err := a.GetMediaFiles(t.Context(), apiv3.ManualImportQuery{DownloadID: "unknown"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("downloadId+empty-cache = %v, want non-nil empty", got)
	}
}

func TestManualImportHelpers_J387a(t *testing.T) {
	t.Parallel()
	if hashInt31("a") != hashInt31("a") {
		t.Error("hashInt31 not deterministic")
	}
	if hashInt31("a") < 0 {
		t.Error("hashInt31 must be non-negative (high bit cleared)")
	}
	if hashInt31("a") == hashInt31("b") {
		t.Error("hashInt31 collision on distinct inputs")
	}
	if got := nameWithoutExt("/a/b/Show.S01E01.mkv"); got != "Show.S01E01" {
		t.Errorf("nameWithoutExt = %q", got)
	}
	if s, ok := singleSeason([]episodes.Episode{{SeasonNumber: 3}, {SeasonNumber: 3}}); !ok || s != 3 {
		t.Errorf("singleSeason = (%d,%v), want (3,true)", s, ok)
	}
	if _, ok := singleSeason(nil); ok {
		t.Error("singleSeason(nil) should be (_, false)")
	}
}
