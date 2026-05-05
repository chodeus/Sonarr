package housekeeping_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclientstatus"
	"sonarr.tv/sonarr/internal/housekeeping"
	"sonarr.tv/sonarr/internal/importlists"
	"sonarr.tv/sonarr/internal/importliststatus"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/indexerstatus"
	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/notificationstatus"
	"sonarr.tv/sonarr/internal/providerstatus"
)

// The four FixFuture*StatusTimes housekeepers share the same shape:
// seed three rows per status table — one with future-clock columns, one
// already past, one with EscalationLevel that puts a tighter cap on
// DisabledTill — then assert the housekeeper clamps the future row,
// the cap row, and leaves the past row alone.

func TestNewFixFutureStatusTimes_NilDB(t *testing.T) {
	t.Parallel()
	if _, err := housekeeping.NewFixFutureIndexerStatusTimes(nil, nil); !errors.Is(err, housekeeping.ErrNilDB) {
		t.Errorf("Indexer(nil): %v, want ErrNilDB", err)
	}
	if _, err := housekeeping.NewFixFutureDownloadClientStatusTimes(nil, nil); !errors.Is(err, housekeeping.ErrNilDB) {
		t.Errorf("DownloadClient(nil): %v, want ErrNilDB", err)
	}
	if _, err := housekeeping.NewFixFutureImportListStatusTimes(nil, nil); !errors.Is(err, housekeeping.ErrNilDB) {
		t.Errorf("ImportList(nil): %v, want ErrNilDB", err)
	}
	if _, err := housekeeping.NewFixFutureNotificationStatusTimes(nil, nil); !errors.Is(err, housekeeping.ErrNilDB) {
		t.Errorf("Notification(nil): %v, want ErrNilDB", err)
	}
}

func TestFixFutureStatusTimes_TaskNames(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	cases := []struct {
		make func(*datastore.DB, *slog.Logger) (*housekeeping.FixFutureProviderStatusTimes, error)
		want string
	}{
		{housekeeping.NewFixFutureIndexerStatusTimes, "FixFutureIndexerStatusTimes"},
		{housekeeping.NewFixFutureDownloadClientStatusTimes, "FixFutureDownloadClientStatusTimes"},
		{housekeeping.NewFixFutureImportListStatusTimes, "FixFutureImportListStatusTimes"},
		{housekeeping.NewFixFutureNotificationStatusTimes, "FixFutureNotificationStatusTimes"},
	}
	for _, c := range cases {
		task, err := c.make(db, slog.Default())
		if err != nil {
			t.Fatalf("New %s: %v", c.want, err)
		}
		if task.Name() != c.want {
			t.Errorf("Name: got %q, want %q", task.Name(), c.want)
		}
	}
}

func TestFixFutureIndexerStatusTimes_Sqlite(t *testing.T) {
	t.Parallel()
	runIndexerFixSuite(t, datastoretest.OpenSQLite(t))
}

func TestFixFutureIndexerStatusTimes_Postgres(t *testing.T) {
	runIndexerFixSuite(t, datastoretest.OpenPostgres(t))
}

func runIndexerFixSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)

	// Seed two indexers (one for each status row that needs a valid
	// ProviderId).
	idxRepo, err := indexers.NewRepo(db)
	if err != nil {
		t.Fatalf("indexers.NewRepo: %v", err)
	}
	mkIdx := func(name string) int {
		row := &indexers.Indexer{Name: name, Implementation: "Newznab", EnableInteractiveSearch: true, Priority: 25, Tags: []int{}}
		if _, err := idxRepo.Insert(t.Context(), row); err != nil {
			t.Fatalf("indexers Insert %s: %v", name, err)
		}
		return row.ID
	}
	futID := mkIdx("Future")
	pastID := mkIdx("Past")
	capID := mkIdx("CappedByEscalation")

	statusRepo, err := indexerstatus.NewRepo(db)
	if err != nil {
		t.Fatalf("indexerstatus.NewRepo: %v", err)
	}

	wayFuture := now.Add(48 * time.Hour)
	wayPast := now.Add(-48 * time.Hour)

	futureRow := &indexerstatus.Status{
		ProviderID:        futID,
		EscalationLevel:   1, // Periods[1] = 60s, so DisabledTill cap = now+60s
		DisabledTill:      &wayFuture,
		InitialFailure:    &wayFuture,
		MostRecentFailure: &wayFuture,
	}
	pastRow := &indexerstatus.Status{
		ProviderID:        pastID,
		EscalationLevel:   2, // Periods[2] = 5min
		DisabledTill:      &wayPast,
		InitialFailure:    &wayPast,
		MostRecentFailure: &wayPast,
	}
	// Capped row: DisabledTill is already in the future but BELOW
	// `now + Periods[level]`, so no clamp on DisabledTill should happen.
	// InitialFailure here is also future so we still expect ONE clamp.
	withinCap := now.Add(2 * time.Minute) // < Periods[3]=15min
	capRow := &indexerstatus.Status{
		ProviderID:        capID,
		EscalationLevel:   3,
		DisabledTill:      &withinCap, // stays put
		InitialFailure:    &wayFuture, // gets clamped to `now`
		MostRecentFailure: nil,        // NULL stays NULL
	}
	for _, r := range []*indexerstatus.Status{futureRow, pastRow, capRow} {
		if _, err := statusRepo.Insert(t.Context(), r); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	task, err := housekeeping.NewFixFutureIndexerStatusTimes(db, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	task.SetClock(func() time.Time { return now })

	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}

	// future row: every column clamped.
	got, err := statusRepo.Get(t.Context(), futureRow.ID)
	if err != nil {
		t.Fatalf("Get future: %v", err)
	}
	wantDisabled := now.Add(time.Duration(providerstatus.BackOffPeriodSeconds[1]) * time.Second)
	if got.DisabledTill == nil || !got.DisabledTill.Equal(wantDisabled) {
		t.Errorf("future DisabledTill: got %v, want %v", got.DisabledTill, wantDisabled)
	}
	if got.InitialFailure == nil || !got.InitialFailure.Equal(now) {
		t.Errorf("future InitialFailure: got %v, want %v", got.InitialFailure, now)
	}
	if got.MostRecentFailure == nil || !got.MostRecentFailure.Equal(now) {
		t.Errorf("future MostRecentFailure: got %v, want %v", got.MostRecentFailure, now)
	}

	// past row: untouched.
	got, err = statusRepo.Get(t.Context(), pastRow.ID)
	if err != nil {
		t.Fatalf("Get past: %v", err)
	}
	if got.DisabledTill == nil || !got.DisabledTill.Equal(wayPast) {
		t.Errorf("past DisabledTill drifted: got %v, want %v", got.DisabledTill, wayPast)
	}
	if got.InitialFailure == nil || !got.InitialFailure.Equal(wayPast) {
		t.Errorf("past InitialFailure drifted: got %v, want %v", got.InitialFailure, wayPast)
	}
	if got.MostRecentFailure == nil || !got.MostRecentFailure.Equal(wayPast) {
		t.Errorf("past MostRecentFailure drifted: got %v, want %v", got.MostRecentFailure, wayPast)
	}

	// cap row: DisabledTill stays put, InitialFailure clamped, NULL
	// MostRecentFailure stays NULL.
	got, err = statusRepo.Get(t.Context(), capRow.ID)
	if err != nil {
		t.Fatalf("Get cap: %v", err)
	}
	if got.DisabledTill == nil || !got.DisabledTill.Equal(withinCap) {
		t.Errorf("cap DisabledTill drifted: got %v, want %v", got.DisabledTill, withinCap)
	}
	if got.InitialFailure == nil || !got.InitialFailure.Equal(now) {
		t.Errorf("cap InitialFailure: got %v, want %v", got.InitialFailure, now)
	}
	if got.MostRecentFailure != nil {
		t.Errorf("cap MostRecentFailure: got %v, want nil", got.MostRecentFailure)
	}
}

func TestFixFutureDownloadClientStatusTimes_Sqlite(t *testing.T) {
	t.Parallel()
	runDownloadClientFixSuite(t, datastoretest.OpenSQLite(t))
}

func TestFixFutureDownloadClientStatusTimes_Postgres(t *testing.T) {
	runDownloadClientFixSuite(t, datastoretest.OpenPostgres(t))
}

func runDownloadClientFixSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	dcRepo, err := downloadclients.NewRepo(db)
	if err != nil {
		t.Fatalf("downloadclients.NewRepo: %v", err)
	}
	dc := &downloadclients.DownloadClient{Name: "DC", Implementation: "QBittorrent", Priority: 1, Tags: []int{}}
	if _, err := dcRepo.Insert(t.Context(), dc); err != nil {
		t.Fatalf("downloadclients Insert: %v", err)
	}

	statusRepo, err := downloadclientstatus.NewRepo(db)
	if err != nil {
		t.Fatalf("downloadclientstatus.NewRepo: %v", err)
	}
	wayFuture := now.Add(72 * time.Hour)
	row := &downloadclientstatus.Status{
		ProviderID:        dc.ID,
		EscalationLevel:   2,
		DisabledTill:      &wayFuture,
		InitialFailure:    &wayFuture,
		MostRecentFailure: &wayFuture,
	}
	if _, err := statusRepo.Insert(t.Context(), row); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	task, err := housekeeping.NewFixFutureDownloadClientStatusTimes(db, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	task.SetClock(func() time.Time { return now })
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}

	got, err := statusRepo.Get(t.Context(), row.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	wantDisabled := now.Add(time.Duration(providerstatus.BackOffPeriodSeconds[2]) * time.Second)
	if got.DisabledTill == nil || !got.DisabledTill.Equal(wantDisabled) {
		t.Errorf("DisabledTill: got %v, want %v", got.DisabledTill, wantDisabled)
	}
	if got.InitialFailure == nil || !got.InitialFailure.Equal(now) {
		t.Errorf("InitialFailure: got %v, want %v", got.InitialFailure, now)
	}
	if got.MostRecentFailure == nil || !got.MostRecentFailure.Equal(now) {
		t.Errorf("MostRecentFailure: got %v, want %v", got.MostRecentFailure, now)
	}
}

func TestFixFutureImportListStatusTimes_Sqlite(t *testing.T) {
	t.Parallel()
	runImportListFixSuite(t, datastoretest.OpenSQLite(t))
}

func TestFixFutureImportListStatusTimes_Postgres(t *testing.T) {
	runImportListFixSuite(t, datastoretest.OpenPostgres(t))
}

func runImportListFixSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	listsRepo, err := importlists.NewRepo(db)
	if err != nil {
		t.Fatalf("importlists.NewRepo: %v", err)
	}
	ilist := &importlists.ImportList{
		Name:             "Live",
		Implementation:   "RSS",
		ConfigContract:   ptrString("RssListSettings"),
		Settings:         ptrString("{}"),
		RootFolderPath:   "/tv",
		ShouldMonitor:    1,
		Tags:             []int{},
		QualityProfileID: 1,
	}
	if _, err := listsRepo.Insert(t.Context(), ilist); err != nil {
		t.Fatalf("importlists Insert: %v", err)
	}

	statusRepo, err := importliststatus.NewRepo(db)
	if err != nil {
		t.Fatalf("importliststatus.NewRepo: %v", err)
	}
	wayFuture := now.Add(72 * time.Hour)
	row := &importliststatus.Status{
		ProviderID:        ilist.ID,
		EscalationLevel:   4,
		DisabledTill:      &wayFuture,
		InitialFailure:    &wayFuture,
		MostRecentFailure: &wayFuture,
	}
	if _, err := statusRepo.Insert(t.Context(), row); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	task, err := housekeeping.NewFixFutureImportListStatusTimes(db, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	task.SetClock(func() time.Time { return now })
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}

	got, err := statusRepo.Get(t.Context(), row.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	wantDisabled := now.Add(time.Duration(providerstatus.BackOffPeriodSeconds[4]) * time.Second)
	if got.DisabledTill == nil || !got.DisabledTill.Equal(wantDisabled) {
		t.Errorf("DisabledTill: got %v, want %v", got.DisabledTill, wantDisabled)
	}
	if got.InitialFailure == nil || !got.InitialFailure.Equal(now) {
		t.Errorf("InitialFailure: got %v, want %v", got.InitialFailure, now)
	}
	if got.MostRecentFailure == nil || !got.MostRecentFailure.Equal(now) {
		t.Errorf("MostRecentFailure: got %v, want %v", got.MostRecentFailure, now)
	}
}

func TestFixFutureNotificationStatusTimes_Sqlite(t *testing.T) {
	t.Parallel()
	runNotificationFixSuite(t, datastoretest.OpenSQLite(t))
}

func TestFixFutureNotificationStatusTimes_Postgres(t *testing.T) {
	runNotificationFixSuite(t, datastoretest.OpenPostgres(t))
}

func runNotificationFixSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	notifRepo, err := notifications.NewRepo(db)
	if err != nil {
		t.Fatalf("notifications.NewRepo: %v", err)
	}
	cc := "DiscordSettings"
	notif := &notifications.Notification{
		Name:           "Live",
		Implementation: "Discord",
		ConfigContract: &cc,
		Settings:       "{}",
		Tags:           []int{},
	}
	if _, err := notifRepo.Insert(t.Context(), notif); err != nil {
		t.Fatalf("notifications Insert: %v", err)
	}

	statusRepo, err := notificationstatus.NewRepo(db)
	if err != nil {
		t.Fatalf("notificationstatus.NewRepo: %v", err)
	}
	wayFuture := now.Add(72 * time.Hour)
	row := &notificationstatus.Status{
		ProviderID:        notif.ID,
		EscalationLevel:   5,
		DisabledTill:      &wayFuture,
		InitialFailure:    &wayFuture,
		MostRecentFailure: &wayFuture,
	}
	if _, err := statusRepo.Insert(t.Context(), row); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	task, err := housekeeping.NewFixFutureNotificationStatusTimes(db, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	task.SetClock(func() time.Time { return now })
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}

	got, err := statusRepo.Get(t.Context(), row.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	wantDisabled := now.Add(time.Duration(providerstatus.BackOffPeriodSeconds[5]) * time.Second)
	if got.DisabledTill == nil || !got.DisabledTill.Equal(wantDisabled) {
		t.Errorf("DisabledTill: got %v, want %v", got.DisabledTill, wantDisabled)
	}
	if got.InitialFailure == nil || !got.InitialFailure.Equal(now) {
		t.Errorf("InitialFailure: got %v, want %v", got.InitialFailure, now)
	}
	if got.MostRecentFailure == nil || !got.MostRecentFailure.Equal(now) {
		t.Errorf("MostRecentFailure: got %v, want %v", got.MostRecentFailure, now)
	}
}

func TestFixFutureProviderStatusTimes_Noop_NoFutureRows(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)

	idxRepo, err := indexers.NewRepo(db)
	if err != nil {
		t.Fatalf("indexers.NewRepo: %v", err)
	}
	idx := &indexers.Indexer{Name: "OnlyPast", Implementation: "Newznab", EnableInteractiveSearch: true, Priority: 25, Tags: []int{}}
	if _, err := idxRepo.Insert(t.Context(), idx); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	statusRepo, err := indexerstatus.NewRepo(db)
	if err != nil {
		t.Fatalf("indexerstatus.NewRepo: %v", err)
	}
	wayPast := now.Add(-48 * time.Hour)
	row := &indexerstatus.Status{
		ProviderID:        idx.ID,
		EscalationLevel:   1,
		DisabledTill:      &wayPast,
		InitialFailure:    &wayPast,
		MostRecentFailure: &wayPast,
	}
	if _, err := statusRepo.Insert(t.Context(), row); err != nil {
		t.Fatalf("Insert status: %v", err)
	}

	task, err := housekeeping.NewFixFutureIndexerStatusTimes(db, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	task.SetClock(func() time.Time { return now })
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}

	got, err := statusRepo.Get(t.Context(), row.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.DisabledTill == nil || !got.DisabledTill.Equal(wayPast) {
		t.Errorf("DisabledTill drifted: got %v, want %v", got.DisabledTill, wayPast)
	}
	if got.InitialFailure == nil || !got.InitialFailure.Equal(wayPast) {
		t.Errorf("InitialFailure drifted: got %v, want %v", got.InitialFailure, wayPast)
	}
	if got.MostRecentFailure == nil || !got.MostRecentFailure.Equal(wayPast) {
		t.Errorf("MostRecentFailure drifted: got %v, want %v", got.MostRecentFailure, wayPast)
	}
}

func TestFixFutureProviderStatusTimes_ContextCancel(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	task, err := housekeeping.NewFixFutureIndexerStatusTimes(db, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := task.Clean(ctx); err == nil {
		t.Errorf("Clean(cancelled): want error from QueryContext")
	}
}
