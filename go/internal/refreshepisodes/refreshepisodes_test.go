package refreshepisodes_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/refreshepisodes"
	"sonarr.tv/sonarr/internal/series"
)

// stubStore captures the InsertMany / UpdateMany / DeleteMany calls so
// tests can assert what the reconcile loop decided to persist.
type stubStore struct {
	existing  []episodes.Episode
	getErr    error
	inserted  []episodes.Episode
	updated   []episodes.Episode
	deleted   []episodes.Episode
	insertErr error
	updateErr error
	deleteErr error
}

func (s *stubStore) GetEpisodesBySeries(_ context.Context, _ int) ([]episodes.Episode, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	out := make([]episodes.Episode, len(s.existing))
	copy(out, s.existing)
	return out, nil
}

func (s *stubStore) InsertMany(_ context.Context, list []*episodes.Episode) error {
	if s.insertErr != nil {
		return s.insertErr
	}
	for _, e := range list {
		s.inserted = append(s.inserted, *e)
	}
	return nil
}

func (s *stubStore) UpdateMany(_ context.Context, list []*episodes.Episode) error {
	if s.updateErr != nil {
		return s.updateErr
	}
	for _, e := range list {
		s.updated = append(s.updated, *e)
	}
	return nil
}

func (s *stubStore) DeleteMany(_ context.Context, list []episodes.Episode) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	s.deleted = append(s.deleted, list...)
	return nil
}

func mustNewSvc(t *testing.T, store *stubStore) *refreshepisodes.Service {
	t.Helper()
	svc, err := refreshepisodes.NewService(store, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

func TestRefreshEpisodeInfo_InsertsNewEpisode_DefaultsToSeasonMonitored(t *testing.T) {
	t.Parallel()
	store := &stubStore{} // empty existing
	svc := mustNewSvc(t, store)

	ser := &series.Series{
		ID:      42,
		Seasons: `[{"seasonNumber":1,"monitored":true}]`,
	}
	remote := []episodes.Episode{
		{SeasonNumber: 1, EpisodeNumber: 1, Title: "Pilot"},
		{SeasonNumber: 1, EpisodeNumber: 2, Title: "Second"},
	}

	if err := svc.RefreshEpisodeInfo(t.Context(), ser, remote); err != nil {
		t.Fatalf("RefreshEpisodeInfo: %v", err)
	}
	if len(store.inserted) != 2 {
		t.Fatalf("inserted: got %d, want 2", len(store.inserted))
	}
	if len(store.updated) != 0 || len(store.deleted) != 0 {
		t.Errorf("updated=%d deleted=%d, both should be empty", len(store.updated), len(store.deleted))
	}
	for _, ep := range store.inserted {
		if ep.SeriesID != 42 {
			t.Errorf("SeriesID: got %d, want 42", ep.SeriesID)
		}
		if !ep.Monitored {
			t.Errorf("S%dE%d should be monitored (season monitored=true)", ep.SeasonNumber, ep.EpisodeNumber)
		}
	}
}

func TestRefreshEpisodeInfo_UpdatesExisting_PreservesIDsAndFlags(t *testing.T) {
	t.Parallel()
	existingID := 99
	fileID := 7
	store := &stubStore{
		existing: []episodes.Episode{
			{ID: existingID, SeriesID: 42, SeasonNumber: 1, EpisodeNumber: 1, Title: "Old", Monitored: false, EpisodeFileID: &fileID},
		},
	}
	svc := mustNewSvc(t, store)

	ser := &series.Series{ID: 42, Seasons: `[{"seasonNumber":1,"monitored":true}]`}
	remote := []episodes.Episode{
		{SeasonNumber: 1, EpisodeNumber: 1, Title: "Refreshed Title", Overview: "Refreshed overview"},
	}
	if err := svc.RefreshEpisodeInfo(t.Context(), ser, remote); err != nil {
		t.Fatalf("RefreshEpisodeInfo: %v", err)
	}
	if len(store.updated) != 1 {
		t.Fatalf("updated: got %d, want 1", len(store.updated))
	}
	got := store.updated[0]
	if got.ID != existingID {
		t.Errorf("ID: got %d, want %d (existing should be preserved)", got.ID, existingID)
	}
	if got.Title != "Refreshed Title" {
		t.Errorf("Title: got %q, want %q", got.Title, "Refreshed Title")
	}
	if got.Overview != "Refreshed overview" {
		t.Errorf("Overview: got %q", got.Overview)
	}
	// Monitored flag on the existing row carried false; reconcile must not
	// stomp the user's per-episode choice on update path.
	if got.Monitored {
		t.Errorf("Monitored: got true, want false (existing user choice preserved)")
	}
	// EpisodeFileID was attached to the existing row; reconcile must not
	// strip it (the C# port copies only the SkyHook-authoritative fields).
	if got.EpisodeFileID == nil || *got.EpisodeFileID != fileID {
		t.Errorf("EpisodeFileID stripped on update path")
	}
}

func TestRefreshEpisodeInfo_DeletesRemovedEpisode(t *testing.T) {
	t.Parallel()
	store := &stubStore{
		existing: []episodes.Episode{
			{ID: 1, SeasonNumber: 1, EpisodeNumber: 1},
			{ID: 2, SeasonNumber: 1, EpisodeNumber: 2}, // not in remote → delete
		},
	}
	svc := mustNewSvc(t, store)
	ser := &series.Series{ID: 42, Seasons: `[]`}
	remote := []episodes.Episode{
		{SeasonNumber: 1, EpisodeNumber: 1},
	}
	if err := svc.RefreshEpisodeInfo(t.Context(), ser, remote); err != nil {
		t.Fatalf("RefreshEpisodeInfo: %v", err)
	}
	if len(store.deleted) != 1 || store.deleted[0].ID != 2 {
		t.Errorf("deleted: got %+v, want [{ID:2}]", store.deleted)
	}
}

func TestRefreshEpisodeInfo_DedupesByKey(t *testing.T) {
	t.Parallel()
	store := &stubStore{}
	svc := mustNewSvc(t, store)
	ser := &series.Series{ID: 42, Seasons: `[]`}
	remote := []episodes.Episode{
		{SeasonNumber: 1, EpisodeNumber: 1, Title: "first"},
		{SeasonNumber: 1, EpisodeNumber: 1, Title: "duplicate — should be dropped"},
		{SeasonNumber: 1, EpisodeNumber: 2, Title: "second"},
	}
	if err := svc.RefreshEpisodeInfo(t.Context(), ser, remote); err != nil {
		t.Fatalf("RefreshEpisodeInfo: %v", err)
	}
	if len(store.inserted) != 2 {
		t.Fatalf("inserted: got %d, want 2 (duplicate must be dropped)", len(store.inserted))
	}
	if store.inserted[0].Title != "first" {
		t.Errorf("first-occurrence-wins: got %q, want %q", store.inserted[0].Title, "first")
	}
}

func TestRefreshEpisodeInfo_DefaultsTitleToTBA(t *testing.T) {
	t.Parallel()
	store := &stubStore{}
	svc := mustNewSvc(t, store)
	ser := &series.Series{ID: 42, Seasons: `[]`}
	remote := []episodes.Episode{{SeasonNumber: 1, EpisodeNumber: 1, Title: ""}}
	if err := svc.RefreshEpisodeInfo(t.Context(), ser, remote); err != nil {
		t.Fatalf("RefreshEpisodeInfo: %v", err)
	}
	if store.inserted[0].Title != "TBA" {
		t.Errorf("Title: got %q, want TBA", store.inserted[0].Title)
	}
}

func TestGetMonitoredStatus_SeasonZeroSpecialCase(t *testing.T) {
	t.Parallel()
	store := &stubStore{}
	svc := mustNewSvc(t, store)
	ser := &series.Series{
		ID: 42,
		// Season 2 monitored, but episode 0 in season 2 is the "specials placeholder" → must be false.
		Seasons: `[{"seasonNumber":1,"monitored":true},{"seasonNumber":2,"monitored":true}]`,
	}
	remote := []episodes.Episode{
		{SeasonNumber: 1, EpisodeNumber: 0, Title: "S1E0 pilot premiere"}, // S1E0 stays monitored from season
		{SeasonNumber: 2, EpisodeNumber: 0, Title: "S2E0 specials placeholder"},
	}
	if err := svc.RefreshEpisodeInfo(t.Context(), ser, remote); err != nil {
		t.Fatalf("RefreshEpisodeInfo: %v", err)
	}
	var s1e0, s2e0 *episodes.Episode
	for i := range store.inserted {
		switch {
		case store.inserted[i].SeasonNumber == 1 && store.inserted[i].EpisodeNumber == 0:
			s1e0 = &store.inserted[i]
		case store.inserted[i].SeasonNumber == 2 && store.inserted[i].EpisodeNumber == 0:
			s2e0 = &store.inserted[i]
		}
	}
	if s1e0 == nil || s2e0 == nil {
		t.Fatalf("both episodes should have been inserted; got %+v", store.inserted)
	}
	if !s1e0.Monitored {
		t.Errorf("S1E0 (pilot/series-premiere special case): want monitored=true, got false")
	}
	if s2e0.Monitored {
		t.Errorf("S2E0 (specials-placeholder special case): want monitored=false, got true")
	}
}

func TestRefreshEpisodeInfo_PublishesEvent(t *testing.T) {
	t.Parallel()
	bus := messaging.New(slog.Default())
	defer bus.BeginShutdown()
	var mu sync.Mutex
	var got []episodes.EpisodeInfoRefreshedEvent
	messaging.Subscribe(bus, "test.J200Reconcile", func(_ context.Context, ev episodes.EpisodeInfoRefreshedEvent) error {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
		return nil
	})

	store := &stubStore{
		existing: []episodes.Episode{
			{ID: 1, SeasonNumber: 1, EpisodeNumber: 1, Title: "old"},
			{ID: 2, SeasonNumber: 1, EpisodeNumber: 99, Title: "removed upstream"},
		},
	}
	svc, err := refreshepisodes.NewService(store, bus, nil, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	ser := &series.Series{ID: 42, Seasons: `[]`}
	remote := []episodes.Episode{
		{SeasonNumber: 1, EpisodeNumber: 1, Title: "refreshed"},
		{SeasonNumber: 1, EpisodeNumber: 2, Title: "newly added"},
	}
	if err := svc.RefreshEpisodeInfo(t.Context(), ser, remote); err != nil {
		t.Fatalf("RefreshEpisodeInfo: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("expected one event; got %d", len(got))
	}
	ev := got[0]
	if ev.SeriesID != 42 {
		t.Errorf("SeriesID: got %d, want 42", ev.SeriesID)
	}
	if len(ev.Added) != 1 || ev.Added[0].EpisodeNumber != 2 {
		t.Errorf("Added: got %+v, want one ep at E2", ev.Added)
	}
	if len(ev.Updated) != 1 || ev.Updated[0].ID != 1 {
		t.Errorf("Updated: got %+v, want one ep ID=1", ev.Updated)
	}
	if len(ev.Removed) != 1 || ev.Removed[0].ID != 2 {
		t.Errorf("Removed: got %+v, want one ep ID=2", ev.Removed)
	}
}

func TestRefreshEpisodeInfo_NilService(t *testing.T) {
	t.Parallel()
	var svc *refreshepisodes.Service
	if err := svc.RefreshEpisodeInfo(t.Context(), &series.Series{}, nil); err == nil {
		t.Errorf("nil-receiver: want error, got nil")
	}
}

func TestRefreshEpisodeInfo_NilSeries(t *testing.T) {
	t.Parallel()
	svc := mustNewSvc(t, &stubStore{})
	if err := svc.RefreshEpisodeInfo(t.Context(), nil, nil); err == nil {
		t.Errorf("nil-series: want error, got nil")
	}
}

func TestRefreshEpisodeInfo_PropagatesGetError(t *testing.T) {
	t.Parallel()
	store := &stubStore{getErr: errors.New("get blew up")}
	svc := mustNewSvc(t, store)
	if err := svc.RefreshEpisodeInfo(t.Context(), &series.Series{ID: 42}, nil); err == nil {
		t.Errorf("want propagated GetEpisodesBySeries error")
	}
}

func TestNewService_NilStore(t *testing.T) {
	t.Parallel()
	if _, err := refreshepisodes.NewService(nil, nil, nil, nil); err == nil {
		t.Errorf("nil-store: want error")
	}
}

// ---- J201 — finale-detection heuristic tests ----

func j201Service(t *testing.T, store *stubStore, now time.Time) *refreshepisodes.Service {
	t.Helper()
	svc, err := refreshepisodes.NewService(store, nil, func() time.Time { return now }, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

func TestFinaleDetection_EndedSeriesLastEpisodeRecent_StampsSeriesFinale(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	aired := now.AddDate(0, 0, -3) // 3 days ago — within the 14-day cutoff

	store := &stubStore{}
	svc := j201Service(t, store, now)
	ser := &series.Series{
		ID:      42,
		Status:  series.SeriesStatusEnded,
		Seasons: `[{"seasonNumber":1,"monitored":true},{"seasonNumber":2,"monitored":true}]`,
	}
	remote := []episodes.Episode{
		{SeasonNumber: 2, EpisodeNumber: 1, Title: "S2E1", AirDateUtc: &aired},
		{SeasonNumber: 2, EpisodeNumber: 2, Title: "S2E2 — last episode of last season", AirDateUtc: &aired},
	}
	if err := svc.RefreshEpisodeInfo(t.Context(), ser, remote); err != nil {
		t.Fatalf("RefreshEpisodeInfo: %v", err)
	}
	var s2e2 *episodes.Episode
	for i := range store.inserted {
		if store.inserted[i].SeasonNumber == 2 && store.inserted[i].EpisodeNumber == 2 {
			s2e2 = &store.inserted[i]
		}
	}
	if s2e2 == nil {
		t.Fatal("S2E2 not inserted")
	}
	if s2e2.FinaleType != "series" {
		t.Errorf("FinaleType: got %q, want %q", s2e2.FinaleType, "series")
	}
}

func TestFinaleDetection_ContinuingSeries_NoStamp(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	aired := now.AddDate(0, 0, -3)
	store := &stubStore{}
	svc := j201Service(t, store, now)
	ser := &series.Series{
		ID:      42,
		Status:  series.SeriesStatusContinuing, // not ended → skip
		Seasons: `[{"seasonNumber":1,"monitored":true}]`,
	}
	remote := []episodes.Episode{
		{SeasonNumber: 1, EpisodeNumber: 1, AirDateUtc: &aired},
		{SeasonNumber: 1, EpisodeNumber: 2, AirDateUtc: &aired},
	}
	if err := svc.RefreshEpisodeInfo(t.Context(), ser, remote); err != nil {
		t.Fatalf("RefreshEpisodeInfo: %v", err)
	}
	for _, e := range store.inserted {
		if e.FinaleType != "" {
			t.Errorf("S%dE%d: FinaleType=%q, want empty (series Continuing)", e.SeasonNumber, e.EpisodeNumber, e.FinaleType)
		}
	}
}

func TestFinaleDetection_OldAirDate_NoStamp(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -20) // 20 days ago — outside the 14-day cutoff
	store := &stubStore{}
	svc := j201Service(t, store, now)
	ser := &series.Series{
		ID:      42,
		Status:  series.SeriesStatusEnded,
		Seasons: `[{"seasonNumber":1,"monitored":true}]`,
	}
	remote := []episodes.Episode{
		{SeasonNumber: 1, EpisodeNumber: 1, AirDateUtc: &old},
		{SeasonNumber: 1, EpisodeNumber: 2, AirDateUtc: &old},
	}
	if err := svc.RefreshEpisodeInfo(t.Context(), ser, remote); err != nil {
		t.Fatalf("RefreshEpisodeInfo: %v", err)
	}
	for _, e := range store.inserted {
		if e.FinaleType != "" {
			t.Errorf("S%dE%d: FinaleType=%q, want empty (old air date)", e.SeasonNumber, e.EpisodeNumber, e.FinaleType)
		}
	}
}

func TestFinaleDetection_ExistingFinaleInSeason_NoStamp(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	aired := now.AddDate(0, 0, -3)
	store := &stubStore{}
	svc := j201Service(t, store, now)
	ser := &series.Series{
		ID:      42,
		Status:  series.SeriesStatusEnded,
		Seasons: `[{"seasonNumber":1,"monitored":true}]`,
	}
	// Remote already flags S1E1 as a season finale — the heuristic
	// must NOT also stamp S1E3 as a series finale (one finale per
	// season is the contract).
	remote := []episodes.Episode{
		{SeasonNumber: 1, EpisodeNumber: 1, AirDateUtc: &aired, FinaleType: "season"},
		{SeasonNumber: 1, EpisodeNumber: 2, AirDateUtc: &aired},
		{SeasonNumber: 1, EpisodeNumber: 3, AirDateUtc: &aired},
	}
	if err := svc.RefreshEpisodeInfo(t.Context(), ser, remote); err != nil {
		t.Fatalf("RefreshEpisodeInfo: %v", err)
	}
	for _, e := range store.inserted {
		if e.EpisodeNumber == 1 && e.FinaleType != "season" {
			t.Errorf("S1E1: FinaleType=%q, want season (from remote)", e.FinaleType)
		}
		if e.EpisodeNumber != 1 && e.FinaleType != "" {
			t.Errorf("S%dE%d: FinaleType=%q, want empty (existing finale guards the season)", e.SeasonNumber, e.EpisodeNumber, e.FinaleType)
		}
	}
}

func TestFinaleDetection_NotLastSeason_NoStamp(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	aired := now.AddDate(0, 0, -3)
	store := &stubStore{}
	svc := j201Service(t, store, now)
	ser := &series.Series{
		ID:     42,
		Status: series.SeriesStatusEnded,
		// Latest season is 2; S1E2 must NOT be stamped even though
		// it's the last episode of season 1.
		Seasons: `[{"seasonNumber":1,"monitored":true},{"seasonNumber":2,"monitored":true}]`,
	}
	remote := []episodes.Episode{
		{SeasonNumber: 1, EpisodeNumber: 1, AirDateUtc: &aired},
		{SeasonNumber: 1, EpisodeNumber: 2, AirDateUtc: &aired}, // last of S1 but not last season
		{SeasonNumber: 2, EpisodeNumber: 1, AirDateUtc: &aired},
	}
	if err := svc.RefreshEpisodeInfo(t.Context(), ser, remote); err != nil {
		t.Fatalf("RefreshEpisodeInfo: %v", err)
	}
	for _, e := range store.inserted {
		// Only check S1E2 — S2E1 has EpisodeNumber=1 which also fails
		// the heuristic's >1 guard.
		if e.SeasonNumber == 1 && e.EpisodeNumber == 2 && e.FinaleType != "" {
			t.Errorf("S1E2: FinaleType=%q, want empty (S2 is the last season)", e.FinaleType)
		}
	}
}

func TestFinaleDetection_FirstEpisodeOfLastSeason_NoStamp(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	aired := now.AddDate(0, 0, -3)
	store := &stubStore{}
	svc := j201Service(t, store, now)
	ser := &series.Series{
		ID:      42,
		Status:  series.SeriesStatusEnded,
		Seasons: `[{"seasonNumber":1,"monitored":true}]`,
	}
	// Single-episode last season → EpisodeNumber=1=count, but the >1
	// guard means a pilot-only-episode is NOT auto-stamped.
	remote := []episodes.Episode{
		{SeasonNumber: 1, EpisodeNumber: 1, AirDateUtc: &aired},
	}
	if err := svc.RefreshEpisodeInfo(t.Context(), ser, remote); err != nil {
		t.Fatalf("RefreshEpisodeInfo: %v", err)
	}
	if store.inserted[0].FinaleType != "" {
		t.Errorf("S1E1 alone: FinaleType=%q, want empty (>1 guard)", store.inserted[0].FinaleType)
	}
}

// ---- J202 — UnmonitorReaddedEpisodes tests ----

func TestUnmonitorReadded_AddOptionsSet_Skips(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -30) // 30 days ago — would normally trigger
	store := &stubStore{}         // no existing → cold-add path
	svc := j201Service(t, store, now)

	ser := &series.Series{
		ID: 42,
		// Non-empty AddOptions → AddSeriesService monitor override
		// is active; UnmonitorReaddedEpisodes must not interfere.
		AddOptions: `{"ignoreEpisodesWithFiles":true}`,
		Seasons:    `[{"seasonNumber":1,"monitored":true}]`,
	}
	remote := []episodes.Episode{
		{SeasonNumber: 1, EpisodeNumber: 1, AirDateUtc: &old},
	}
	if err := svc.RefreshEpisodeInfo(t.Context(), ser, remote); err != nil {
		t.Fatalf("RefreshEpisodeInfo: %v", err)
	}
	if !store.inserted[0].Monitored {
		t.Errorf("AddOptions present: Monitored should NOT be flipped to false (got false)")
	}
}

func TestUnmonitorReadded_ColdAdd_OldEpisodes_Unmonitors(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -30) // 30 days ago — past both thresholds
	yesterday := now.AddDate(0, 0, -2)
	recent := now.AddDate(0, 0, 1) // future

	store := &stubStore{} // no existing → cold-add path
	svc := j201Service(t, store, now)
	ser := &series.Series{
		ID:      42,
		Seasons: `[{"seasonNumber":1,"monitored":true}]`,
		// AddOptions empty → reads as nil decoded; the heuristic runs.
	}
	remote := []episodes.Episode{
		{SeasonNumber: 1, EpisodeNumber: 1, AirDateUtc: &old},       // old → unmonitor
		{SeasonNumber: 1, EpisodeNumber: 2, AirDateUtc: &yesterday}, // older than 1d → unmonitor
		{SeasonNumber: 1, EpisodeNumber: 3, AirDateUtc: &recent},    // future → keep monitored
	}
	if err := svc.RefreshEpisodeInfo(t.Context(), ser, remote); err != nil {
		t.Fatalf("RefreshEpisodeInfo: %v", err)
	}
	got := map[int]bool{}
	for _, ep := range store.inserted {
		got[ep.EpisodeNumber] = ep.Monitored
	}
	if got[1] {
		t.Errorf("E1 (old): Monitored=true, want false")
	}
	if got[2] {
		t.Errorf("E2 (yesterday): Monitored=true, want false")
	}
	if !got[3] {
		t.Errorf("E3 (future): Monitored=false, want true (preserved)")
	}
}

func TestUnmonitorReadded_HasExisting_OnlyWarns_NoUnmonitor(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -30) // way past detection threshold
	store := &stubStore{
		existing: []episodes.Episode{
			{ID: 99, SeasonNumber: 1, EpisodeNumber: 1, Title: "already-known-episode"},
		},
	}
	svc := j201Service(t, store, now)
	ser := &series.Series{
		ID:      42,
		Seasons: `[{"seasonNumber":1,"monitored":true}]`,
	}
	// E2 is brand new + old-aired. With existing E1 in the persistence,
	// the heuristic must NOT auto-unmonitor — log only.
	remote := []episodes.Episode{
		{SeasonNumber: 1, EpisodeNumber: 1, Title: "refreshed-E1"},
		{SeasonNumber: 1, EpisodeNumber: 2, Title: "newly-back-filled-old-E2", AirDateUtc: &old},
	}
	if err := svc.RefreshEpisodeInfo(t.Context(), ser, remote); err != nil {
		t.Fatalf("RefreshEpisodeInfo: %v", err)
	}
	var e2 *episodes.Episode
	for i := range store.inserted {
		if store.inserted[i].EpisodeNumber == 2 {
			e2 = &store.inserted[i]
		}
	}
	if e2 == nil {
		t.Fatal("E2 not inserted")
	}
	if !e2.Monitored {
		t.Errorf("hasExisting + old-back-fill: E2.Monitored=false, want true (warn-only path)")
	}
}

func TestUnmonitorReadded_NoOldEpisodes_NoOp(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	recent := now.AddDate(0, 0, -3) // within detection threshold but recent
	store := &stubStore{}
	svc := j201Service(t, store, now)
	ser := &series.Series{ID: 42, Seasons: `[{"seasonNumber":1,"monitored":true}]`}
	remote := []episodes.Episode{
		{SeasonNumber: 1, EpisodeNumber: 1, AirDateUtc: &recent},
	}
	if err := svc.RefreshEpisodeInfo(t.Context(), ser, remote); err != nil {
		t.Fatalf("RefreshEpisodeInfo: %v", err)
	}
	if !store.inserted[0].Monitored {
		t.Errorf("recent AirDateUtc: Monitored=false, want true (no old episodes → no-op)")
	}
}

func TestUnmonitorReadded_ColdAdd_DetectionOnly_NoUnmonitor(t *testing.T) {
	t.Parallel()
	// Episode is older than 1d (unmonitor threshold) but newer than 14d
	// (detection threshold) — the heuristic should NOT fire at all since
	// detection requires AirDateUtc < now - 14d.
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	between := now.AddDate(0, 0, -5) // 5 days ago: past 1d, well within 14d
	store := &stubStore{}
	svc := j201Service(t, store, now)
	ser := &series.Series{ID: 42, Seasons: `[{"seasonNumber":1,"monitored":true}]`}
	remote := []episodes.Episode{
		{SeasonNumber: 1, EpisodeNumber: 1, AirDateUtc: &between},
	}
	if err := svc.RefreshEpisodeInfo(t.Context(), ser, remote); err != nil {
		t.Fatalf("RefreshEpisodeInfo: %v", err)
	}
	if !store.inserted[0].Monitored {
		t.Errorf("5d-old AirDateUtc: Monitored=false, want true (detection threshold not reached)")
	}
}
