package main

import (
	"testing"
	"time"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/qualityprofiles"
	"sonarr.tv/sonarr/internal/series"
)

// J383: wantedRepoLister hydrates the BASE Series embed + the Images
// projection only when the includeSeries / includeImages flags are set,
// mirroring C# MapToResource(v, includeSeries, _, includeImages). Uses a
// real SQLite repo seeded with one missing episode of a monitored series.
func TestPagedWanted_IncludeSeriesImages_J383(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	seriesRepo, err := series.NewRepo(db)
	if err != nil {
		t.Fatalf("series.NewRepo: %v", err)
	}
	episodesRepo, err := episodes.NewRepo(db)
	if err != nil {
		t.Fatalf("episodes.NewRepo: %v", err)
	}
	ctx := t.Context()

	ser := &series.Series{TvdbID: 2001, Title: "Show", CleanTitle: "show", Monitored: true, Runtime: 30}
	if _, err := seriesRepo.Insert(ctx, ser); err != nil {
		t.Fatalf("insert series: %v", err)
	}
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	past := now.Add(-24 * time.Hour)
	ep := &episodes.Episode{
		SeriesID: ser.ID, SeasonNumber: 1, EpisodeNumber: 1,
		Title: "Want", Monitored: true, AirDateUtc: &past, Runtime: 30,
	}
	if _, err := episodesRepo.Insert(ctx, ep); err != nil {
		t.Fatalf("insert episode: %v", err)
	}

	lister := wantedRepoLister{
		episodes:  episodesRepo,
		seriesSvc: &fakeHistorySeriesLookup{byID: map[int]*series.Series{ser.ID: {ID: ser.ID, Title: "Show"}}},
		now:       func() time.Time { return now },
	}

	// Neither flag → BASE projection only (no Series, no Images).
	rows, total, err := lister.PagedWanted(ctx, apiv3.PagingRequest{Page: 1, PageSize: 50}, apiv3.WantedMissing, false, false, false)
	if err != nil {
		t.Fatalf("PagedWanted(neither): %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Fatalf("total/len = %d/%d, want 1/1", total, len(rows))
	}
	if rows[0].Series != nil {
		t.Errorf("Series present without includeSeries; got %+v", rows[0].Series)
	}
	if rows[0].Images != nil {
		t.Errorf("Images present without includeImages; got %+v", rows[0].Images)
	}

	// Both flags → Series embed + Images projection present.
	rows, _, err = lister.PagedWanted(ctx, apiv3.PagingRequest{Page: 1, PageSize: 50}, apiv3.WantedMissing, true, false, true)
	if err != nil {
		t.Fatalf("PagedWanted(both): %v", err)
	}
	if rows[0].Series == nil || rows[0].Series.Title != "Show" {
		t.Errorf("Series embed missing/wrong: %+v", rows[0].Series)
	}
	if rows[0].Images == nil {
		t.Errorf("Images projection nil with includeImages=true")
	}
}

// J384: wantedRepoLister hydrates the EpisodeFile subresource on the
// cutoff branch when includeEpisodeFile is set. Seeds a real below-cutoff
// file + profile so WantedCutoffUnmet returns the row, then asserts the
// EpisodeFile embed appears only with the flag.
func TestPagedWanted_CutoffIncludeEpisodeFile_J384(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	seriesRepo, err := series.NewRepo(db)
	if err != nil {
		t.Fatalf("series.NewRepo: %v", err)
	}
	episodesRepo, err := episodes.NewRepo(db)
	if err != nil {
		t.Fatalf("episodes.NewRepo: %v", err)
	}
	filesRepo, err := episodefiles.NewRepo(db)
	if err != nil {
		t.Fatalf("episodefiles.NewRepo: %v", err)
	}
	filesSvc, err := episodefiles.NewService(filesRepo, nil)
	if err != nil {
		t.Fatalf("episodefiles.NewService: %v", err)
	}
	profilesRepo, err := qualityprofiles.NewRepo(db)
	if err != nil {
		t.Fatalf("qualityprofiles.NewRepo: %v", err)
	}
	profilesSvc, err := qualityprofiles.NewService(profilesRepo, nil)
	if err != nil {
		t.Fatalf("qualityprofiles.NewService: %v", err)
	}
	ctx := t.Context()

	// Profile with quality id 2 below the cutoff (id 5) → below set [2].
	prof, err := profilesSvc.Add(ctx, &qualityprofiles.Profile{
		Name:           "HD",
		Cutoff:         5,
		UpgradeAllowed: true,
		Items:          `[{"quality":2,"allowed":true,"items":[]},{"quality":5,"allowed":true,"items":[]}]`,
		FormatItems:    `[]`,
	})
	if err != nil {
		t.Fatalf("add profile: %v", err)
	}

	ser := &series.Series{TvdbID: 3001, Title: "Show", CleanTitle: "show", Monitored: true, Runtime: 30, QualityProfileID: prof.ID}
	if _, err := seriesRepo.Insert(ctx, ser); err != nil {
		t.Fatalf("insert series: %v", err)
	}
	file, err := filesRepo.Insert(ctx, &episodefiles.File{
		SeriesID: ser.ID, SeasonNumber: 1, Size: 100, DateAdded: time.Now().UTC(),
		Quality: `{"quality":2,"revision":{"version":1,"real":0,"isRepack":false}}`,
	})
	if err != nil {
		t.Fatalf("insert file: %v", err)
	}
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	past := now.Add(-24 * time.Hour)
	ep := &episodes.Episode{
		SeriesID: ser.ID, SeasonNumber: 1, EpisodeNumber: 1, Title: "Cutoff",
		Monitored: true, AirDateUtc: &past, Runtime: 30, EpisodeFileID: &file.ID,
	}
	if _, err := episodesRepo.Insert(ctx, ep); err != nil {
		t.Fatalf("insert episode: %v", err)
	}

	lister := wantedRepoLister{
		episodes: episodesRepo,
		profiles: profilesSvc,
		epFiles:  &episodeFileServiceWithPopulator{Service: filesSvc},
		now:      func() time.Time { return now },
	}

	// Without the flag → no EpisodeFile embed.
	rows, total, err := lister.PagedWanted(ctx, apiv3.PagingRequest{Page: 1, PageSize: 50}, apiv3.WantedCutoff, false, false, false)
	if err != nil {
		t.Fatalf("PagedWanted(cutoff, no flag): %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Fatalf("total/len = %d/%d, want 1/1 (cutoff seed)", total, len(rows))
	}
	if rows[0].EpisodeFile != nil {
		t.Errorf("EpisodeFile present without includeEpisodeFile; got %+v", rows[0].EpisodeFile)
	}

	// With the flag → EpisodeFile embed populated.
	rows, _, err = lister.PagedWanted(ctx, apiv3.PagingRequest{Page: 1, PageSize: 50}, apiv3.WantedCutoff, false, true, false)
	if err != nil {
		t.Fatalf("PagedWanted(cutoff, flag): %v", err)
	}
	if rows[0].EpisodeFile == nil {
		t.Fatalf("EpisodeFile nil with includeEpisodeFile=true")
	}
	if rows[0].EpisodeFile.ID != file.ID {
		t.Errorf("EpisodeFile.ID = %d, want %d", rows[0].EpisodeFile.ID, file.ID)
	}
}
