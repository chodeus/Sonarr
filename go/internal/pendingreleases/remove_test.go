package pendingreleases_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/pendingreleases"
	"sonarr.tv/sonarr/internal/quality"
)

// preseedParsedInfo builds the camelCase JSON blob the remove paths
// expect in ParsedEpisodeInfo column. Mirrors the same wire-shape
// AddMany serializers wrote in slice 4.BQ tests.
func preseedParsedInfo(season int, episodes []int, q quality.Model) string {
	payload := map[string]any{
		"seasonNumber":   season,
		"episodeNumbers": episodes,
		"quality":        q,
	}
	b, _ := json.Marshal(payload)
	return string(b)
}

// preseedReleaseInfo builds the camelCase JSON blob the remove paths
// (and AddMany dedup) read for (title, publishDate, indexer).
func preseedReleaseInfo(title string, pubDate time.Time, indexer string) string {
	payload := map[string]any{
		"title":       title,
		"publishDate": pubDate,
		"indexer":     indexer,
	}
	b, _ := json.Marshal(payload)
	return string(b)
}

func makeServiceForRemove(t *testing.T, db *datastore.DB, bus *messaging.Bus) (*pendingreleases.Service, *pendingreleases.Repo) {
	t.Helper()
	repo, err := pendingreleases.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	svc, err := pendingreleases.NewService(repo, bus, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, repo
}

func TestQueueIDFor_Stable(t *testing.T) {
	t.Parallel()
	a := pendingreleases.QueueIDFor(42)
	b := pendingreleases.QueueIDFor(42)
	if a != b {
		t.Errorf("QueueIDFor not deterministic: %d vs %d", a, b)
	}
	if a == 0 {
		t.Error("QueueIDFor(42) returned zero, want non-zero")
	}
	if pendingreleases.QueueIDFor(42) == pendingreleases.QueueIDFor(43) {
		t.Error("QueueIDFor collisions for sequential IDs — hash is broken")
	}
}

func TestDeleteBySeriesIDs_DeletesAllRowsForListedSeries(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	svc, repo := makeServiceForRemove(t, db, nil)
	pub := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	for _, sid := range []int{1, 1, 2, 3} {
		_, _ = repo.Insert(t.Context(), &pendingreleases.Release{SeriesID: sid, Title: "X", Added: pub})
	}
	if err := svc.DeleteBySeriesIDs(t.Context(), []int{1, 3}); err != nil {
		t.Fatalf("DeleteBySeriesIDs: %v", err)
	}
	all, _ := repo.All(t.Context())
	if len(all) != 1 || all[0].SeriesID != 2 {
		t.Errorf("after DeleteBySeriesIDs(1,3): got %+v, want only seriesID=2", all)
	}
}

func TestDeleteBySeriesIDs_EmptyInputIsNoop(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	svc, _ := makeServiceForRemove(t, db, nil)
	if err := svc.DeleteBySeriesIDs(t.Context(), nil); err != nil {
		t.Fatalf("DeleteBySeriesIDs(nil): %v", err)
	}
}

func TestRemoveByQueueID_DeletesAllRowsWithMatchingSeasonAndEpisodes(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	svc, repo := makeServiceForRemove(t, db, nil)
	pub := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	q := quality.Model{Quality: quality.HDTV720p, Revision: quality.NewRevision()}

	// 3 rows: two with same (season=1, episodes=[5]) — different titles
	// (different release-info) so the match key for THIS test is the
	// season+episodes pair, not the (title,pubdate,indexer) tuple.
	r1, _ := repo.Insert(t.Context(), &pendingreleases.Release{
		SeriesID:          7,
		Title:             "Show.S01E05.720p",
		Added:             pub,
		ParsedEpisodeInfo: preseedParsedInfo(1, []int{5}, q),
		Reason:            int(pendingreleases.ReasonDelay),
	})
	_, _ = repo.Insert(t.Context(), &pendingreleases.Release{
		SeriesID:          7,
		Title:             "Show.S01E05.1080p",
		Added:             pub.Add(time.Hour),
		ParsedEpisodeInfo: preseedParsedInfo(1, []int{5}, q),
		Reason:            int(pendingreleases.ReasonDelay),
	})
	// Different episode set — should survive.
	_, _ = repo.Insert(t.Context(), &pendingreleases.Release{
		SeriesID:          7,
		Title:             "Show.S01E06.720p",
		Added:             pub.Add(2 * time.Hour),
		ParsedEpisodeInfo: preseedParsedInfo(1, []int{6}, q),
		Reason:            int(pendingreleases.ReasonDelay),
	})

	queueID := pendingreleases.QueueIDFor(r1.ID)
	if err := svc.RemoveByQueueID(t.Context(), queueID); err != nil {
		t.Fatalf("RemoveByQueueID: %v", err)
	}
	rows, _ := repo.AllBySeriesID(t.Context(), 7)
	if len(rows) != 1 || rows[0].Title != "Show.S01E06.720p" {
		t.Errorf("after RemoveByQueueID: got %+v, want only the S01E06 row", rows)
	}
}

func TestRemoveByQueueID_UnknownQueueIDIsNoop(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	svc, repo := makeServiceForRemove(t, db, nil)
	_, _ = repo.Insert(t.Context(), &pendingreleases.Release{SeriesID: 7, Title: "X", Added: time.Now()})
	if err := svc.RemoveByQueueID(t.Context(), 12345); err != nil {
		t.Fatalf("RemoveByQueueID(unknown): %v", err)
	}
	rows, _ := repo.All(t.Context())
	if len(rows) != 1 {
		t.Errorf("unknown queueID should be a no-op, rows = %d", len(rows))
	}
}

func TestRemoveGrabbed_DeletesEqualOrLowerQualityIntersectingRows(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	svc, repo := makeServiceForRemove(t, db, nil)
	pub := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	sd := quality.Model{Quality: quality.SDTV, Revision: quality.NewRevision()}
	hd := quality.Model{Quality: quality.HDTV720p, Revision: quality.NewRevision()}
	bluray := quality.Model{Quality: quality.Bluray1080p, Revision: quality.NewRevision()}

	// Three pending rows for series 7, season 1, episode 5 at different
	// qualities.
	_, _ = repo.Insert(t.Context(), &pendingreleases.Release{
		SeriesID:          7,
		Title:             "SD",
		Added:             pub,
		ParsedEpisodeInfo: preseedParsedInfo(1, []int{5}, sd),
		Reason:            int(pendingreleases.ReasonDelay),
	})
	_, _ = repo.Insert(t.Context(), &pendingreleases.Release{
		SeriesID:          7,
		Title:             "HD",
		Added:             pub.Add(time.Hour),
		ParsedEpisodeInfo: preseedParsedInfo(1, []int{5}, hd),
		Reason:            int(pendingreleases.ReasonDelay),
	})
	_, _ = repo.Insert(t.Context(), &pendingreleases.Release{
		SeriesID:          7,
		Title:             "BluRay",
		Added:             pub.Add(2 * time.Hour),
		ParsedEpisodeInfo: preseedParsedInfo(1, []int{5}, bluray),
		Reason:            int(pendingreleases.ReasonDelay),
	})

	// Grabbed at HD: SD + HD should be deleted (≤ grabbed); BluRay survives.
	in := pendingreleases.GrabbedInput{
		SeriesID:       7,
		SeasonNumber:   1,
		EpisodeNumbers: []int{5},
		Quality:        hd,
	}
	if err := svc.RemoveGrabbed(t.Context(), in); err != nil {
		t.Fatalf("RemoveGrabbed: %v", err)
	}
	rows, _ := repo.AllBySeriesID(t.Context(), 7)
	if len(rows) != 1 || rows[0].Title != "BluRay" {
		t.Errorf("after RemoveGrabbed(HD): got %+v, want only BluRay row", rows)
	}
}

func TestRemoveGrabbed_DoesNotTouchDifferentSeason(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	svc, repo := makeServiceForRemove(t, db, nil)
	q := quality.Model{Quality: quality.HDTV720p, Revision: quality.NewRevision()}
	// Same series, same episode number, DIFFERENT season.
	_, _ = repo.Insert(t.Context(), &pendingreleases.Release{
		SeriesID:          7,
		Title:             "S01",
		Added:             time.Now(),
		ParsedEpisodeInfo: preseedParsedInfo(1, []int{5}, q),
	})
	_, _ = repo.Insert(t.Context(), &pendingreleases.Release{
		SeriesID:          7,
		Title:             "S02",
		Added:             time.Now(),
		ParsedEpisodeInfo: preseedParsedInfo(2, []int{5}, q),
	})
	in := pendingreleases.GrabbedInput{SeriesID: 7, SeasonNumber: 1, EpisodeNumbers: []int{5}, Quality: q}
	if err := svc.RemoveGrabbed(t.Context(), in); err != nil {
		t.Fatalf("RemoveGrabbed: %v", err)
	}
	rows, _ := repo.AllBySeriesID(t.Context(), 7)
	if len(rows) != 1 || rows[0].Title != "S02" {
		t.Errorf("season filter failed; got %+v", rows)
	}
}

func TestRemoveRejected_DeletesByTitlePubDateIndexerTuple(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	svc, repo := makeServiceForRemove(t, db, nil)
	pub := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	_, _ = repo.Insert(t.Context(), &pendingreleases.Release{
		SeriesID:    7,
		Title:       "Show.S01E01",
		Added:       pub,
		ReleaseInfo: preseedReleaseInfo("Show.S01E01", pub, "IdxA"),
	})
	_, _ = repo.Insert(t.Context(), &pendingreleases.Release{
		SeriesID:    7,
		Title:       "Show.S01E01",
		Added:       pub,
		ReleaseInfo: preseedReleaseInfo("Show.S01E01", pub, "IdxB"), // different indexer
	})
	rejected := []pendingreleases.RejectedInput{{
		SeriesID:     7,
		MatchTitle:   "Show.S01E01",
		MatchPubDate: pub,
		MatchIndexer: "IdxA",
	}}
	if err := svc.RemoveRejected(t.Context(), rejected); err != nil {
		t.Fatalf("RemoveRejected: %v", err)
	}
	rows, _ := repo.AllBySeriesID(t.Context(), 7)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1 (IdxB survives)", len(rows))
	}
	var info map[string]any
	_ = json.Unmarshal([]byte(rows[0].ReleaseInfo), &info)
	if info["indexer"] != "IdxB" {
		t.Errorf("surviving row indexer = %v, want IdxB", info["indexer"])
	}
}

func TestRemoveRejected_EmptyInputIsNoop(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	svc, _ := makeServiceForRemove(t, db, nil)
	if err := svc.RemoveRejected(t.Context(), nil); err != nil {
		t.Fatalf("RemoveRejected(nil): %v", err)
	}
}

func TestOldestPending_ReturnsOldestRowWithIntersectingEpisodes(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	svc, repo := makeServiceForRemove(t, db, nil)
	q := quality.Model{Quality: quality.SDTV, Revision: quality.NewRevision()}
	t0 := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)

	_, _ = repo.Insert(t.Context(), &pendingreleases.Release{
		SeriesID:          7,
		Title:             "Older",
		Added:             t0,
		ParsedEpisodeInfo: preseedParsedInfo(1, []int{5}, q),
	})
	_, _ = repo.Insert(t.Context(), &pendingreleases.Release{
		SeriesID:          7,
		Title:             "Newer",
		Added:             t0.Add(2 * time.Hour),
		ParsedEpisodeInfo: preseedParsedInfo(1, []int{5}, q),
	})
	// Non-matching episode set; should be skipped.
	_, _ = repo.Insert(t.Context(), &pendingreleases.Release{
		SeriesID:          7,
		Title:             "Distractor",
		Added:             t0.Add(-time.Hour),
		ParsedEpisodeInfo: preseedParsedInfo(1, []int{6}, q),
	})

	got, err := svc.OldestPending(t.Context(), 7, 1, []int{5})
	if err != nil {
		t.Fatalf("OldestPending: %v", err)
	}
	if got == nil || got.Title != "Older" {
		t.Errorf("got %+v, want the 'Older' row", got)
	}
}

func TestOldestPending_NoMatchReturnsNil(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	svc, repo := makeServiceForRemove(t, db, nil)
	q := quality.Model{Quality: quality.SDTV, Revision: quality.NewRevision()}
	_, _ = repo.Insert(t.Context(), &pendingreleases.Release{
		SeriesID:          7,
		Title:             "X",
		Added:             time.Now(),
		ParsedEpisodeInfo: preseedParsedInfo(1, []int{5}, q),
	})
	got, err := svc.OldestPending(t.Context(), 7, 1, []int{99})
	if err != nil {
		t.Fatalf("OldestPending: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for no-match, got %+v", got)
	}
}

func TestRemovePathsPublishUpdatedEventWhenAnyRowDeleted(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	bus := messaging.New(nil)
	svc, repo := makeServiceForRemove(t, db, bus)

	q := quality.Model{Quality: quality.SDTV, Revision: quality.NewRevision()}
	_, _ = repo.Insert(t.Context(), &pendingreleases.Release{
		SeriesID:          7,
		Title:             "X",
		Added:             time.Now(),
		ParsedEpisodeInfo: preseedParsedInfo(1, []int{5}, q),
	})

	got := make(chan pendingreleases.PendingReleasesUpdatedEvent, 4)
	messaging.Subscribe(bus, "test-pending-updated", func(_ context.Context, e pendingreleases.PendingReleasesUpdatedEvent) error {
		got <- e
		return nil
	})

	if err := svc.RemoveGrabbed(t.Context(), pendingreleases.GrabbedInput{
		SeriesID:       7,
		SeasonNumber:   1,
		EpisodeNumbers: []int{5},
		Quality:        q,
	}); err != nil {
		t.Fatalf("RemoveGrabbed: %v", err)
	}
	select {
	case <-got:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected PendingReleasesUpdatedEvent after RemoveGrabbed deleted a row")
	}
}
