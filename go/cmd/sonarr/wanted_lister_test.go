package main

import (
	"testing"
	"time"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/episodes"
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
	rows, total, err := lister.PagedWanted(ctx, apiv3.PagingRequest{Page: 1, PageSize: 50}, apiv3.WantedMissing, false, false)
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
	rows, _, err = lister.PagedWanted(ctx, apiv3.PagingRequest{Page: 1, PageSize: 50}, apiv3.WantedMissing, true, true)
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
