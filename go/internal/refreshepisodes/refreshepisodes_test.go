package refreshepisodes_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"

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
	svc, err := refreshepisodes.NewService(store, nil, nil)
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
	svc, err := refreshepisodes.NewService(store, bus, nil)
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
	if _, err := refreshepisodes.NewService(nil, nil, nil); err == nil {
		t.Errorf("nil-store: want error")
	}
}
