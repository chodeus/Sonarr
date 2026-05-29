package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/disk"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
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
