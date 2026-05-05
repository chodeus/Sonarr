package housekeeping_test

import (
	"log/slog"
	"testing"

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
)

func ptrString(v string) *string { return &v }

// Each orphan-status housekeeper tests the same shape: seed two
// status rows (one with a valid ProviderID, one with a dangling ID),
// run cleanup, assert dangling row removed and valid row survives.

func TestCleanupOrphanedIndexerStatus_Sqlite(t *testing.T) {
	t.Parallel()
	runIndexerStatusOrphanSuite(t, datastoretest.OpenSQLite(t))
}

func TestCleanupOrphanedIndexerStatus_Postgres(t *testing.T) {
	runIndexerStatusOrphanSuite(t, datastoretest.OpenPostgres(t))
}

func runIndexerStatusOrphanSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	idxRepo, err := indexers.NewRepo(db)
	if err != nil {
		t.Fatalf("indexers.NewRepo: %v", err)
	}
	keep := &indexers.Indexer{Name: "Live", Implementation: "Newznab", EnableInteractiveSearch: true, Priority: 25, Tags: []int{}}
	if _, err := idxRepo.Insert(t.Context(), keep); err != nil {
		t.Fatalf("indexers Insert: %v", err)
	}

	statusRepo, err := indexerstatus.NewRepo(db)
	if err != nil {
		t.Fatalf("indexerstatus.NewRepo: %v", err)
	}
	survivor := &indexerstatus.Status{ProviderID: keep.ID, EscalationLevel: 1}
	orphan := &indexerstatus.Status{ProviderID: 9999, EscalationLevel: 1}
	if _, err := statusRepo.Insert(t.Context(), survivor); err != nil {
		t.Fatalf("indexerstatus Insert survivor: %v", err)
	}
	if _, err := statusRepo.Insert(t.Context(), orphan); err != nil {
		t.Fatalf("indexerstatus Insert orphan: %v", err)
	}

	task, err := housekeeping.NewCleanupOrphanedIndexerStatus(db, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}

	if _, err := statusRepo.Get(t.Context(), orphan.ID); err == nil {
		t.Errorf("orphan status survived")
	}
	if _, err := statusRepo.Get(t.Context(), survivor.ID); err != nil {
		t.Errorf("survivor status dropped: %v", err)
	}
}

func TestCleanupOrphanedDownloadClientStatus_Sqlite(t *testing.T) {
	t.Parallel()
	runDownloadClientStatusOrphanSuite(t, datastoretest.OpenSQLite(t))
}

func TestCleanupOrphanedDownloadClientStatus_Postgres(t *testing.T) {
	runDownloadClientStatusOrphanSuite(t, datastoretest.OpenPostgres(t))
}

func runDownloadClientStatusOrphanSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	dcRepo, err := downloadclients.NewRepo(db)
	if err != nil {
		t.Fatalf("downloadclients.NewRepo: %v", err)
	}
	keep := &downloadclients.DownloadClient{Name: "Live", Implementation: "QBittorrent", Priority: 1, Tags: []int{}}
	if _, err := dcRepo.Insert(t.Context(), keep); err != nil {
		t.Fatalf("downloadclients Insert: %v", err)
	}

	statusRepo, err := downloadclientstatus.NewRepo(db)
	if err != nil {
		t.Fatalf("downloadclientstatus.NewRepo: %v", err)
	}
	survivor := &downloadclientstatus.Status{ProviderID: keep.ID, EscalationLevel: 1}
	orphan := &downloadclientstatus.Status{ProviderID: 9999, EscalationLevel: 1}
	if _, err := statusRepo.Insert(t.Context(), survivor); err != nil {
		t.Fatalf("downloadclientstatus Insert survivor: %v", err)
	}
	if _, err := statusRepo.Insert(t.Context(), orphan); err != nil {
		t.Fatalf("downloadclientstatus Insert orphan: %v", err)
	}

	task, err := housekeeping.NewCleanupOrphanedDownloadClientStatus(db, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}

	if _, err := statusRepo.Get(t.Context(), orphan.ID); err == nil {
		t.Errorf("orphan status survived")
	}
	if _, err := statusRepo.Get(t.Context(), survivor.ID); err != nil {
		t.Errorf("survivor status dropped: %v", err)
	}
}

func TestCleanupOrphanedImportListStatus_Sqlite(t *testing.T) {
	t.Parallel()
	runImportListStatusOrphanSuite(t, datastoretest.OpenSQLite(t))
}

func TestCleanupOrphanedImportListStatus_Postgres(t *testing.T) {
	runImportListStatusOrphanSuite(t, datastoretest.OpenPostgres(t))
}

func runImportListStatusOrphanSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	listsRepo, err := importlists.NewRepo(db)
	if err != nil {
		t.Fatalf("importlists.NewRepo: %v", err)
	}
	keep := &importlists.ImportList{
		Name:             "Live",
		Implementation:   "RSS",
		ConfigContract:   ptrString("RssListSettings"),
		Settings:         ptrString("{}"),
		RootFolderPath:   "/tv",
		ShouldMonitor:    1,
		Tags:             []int{},
		QualityProfileID: 1,
	}
	if _, err := listsRepo.Insert(t.Context(), keep); err != nil {
		t.Fatalf("importlists Insert: %v", err)
	}

	statusRepo, err := importliststatus.NewRepo(db)
	if err != nil {
		t.Fatalf("importliststatus.NewRepo: %v", err)
	}
	survivor := &importliststatus.Status{ProviderID: keep.ID, EscalationLevel: 1}
	orphan := &importliststatus.Status{ProviderID: 9999, EscalationLevel: 1}
	if _, err := statusRepo.Insert(t.Context(), survivor); err != nil {
		t.Fatalf("importliststatus Insert survivor: %v", err)
	}
	if _, err := statusRepo.Insert(t.Context(), orphan); err != nil {
		t.Fatalf("importliststatus Insert orphan: %v", err)
	}

	task, err := housekeeping.NewCleanupOrphanedImportListStatus(db, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}

	if _, err := statusRepo.Get(t.Context(), orphan.ID); err == nil {
		t.Errorf("orphan status survived")
	}
	if _, err := statusRepo.Get(t.Context(), survivor.ID); err != nil {
		t.Errorf("survivor status dropped: %v", err)
	}
}

func TestCleanupOrphanedNotificationStatus_Sqlite(t *testing.T) {
	t.Parallel()
	runNotificationStatusOrphanSuite(t, datastoretest.OpenSQLite(t))
}

func TestCleanupOrphanedNotificationStatus_Postgres(t *testing.T) {
	runNotificationStatusOrphanSuite(t, datastoretest.OpenPostgres(t))
}

func runNotificationStatusOrphanSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	notifRepo, err := notifications.NewRepo(db)
	if err != nil {
		t.Fatalf("notifications.NewRepo: %v", err)
	}
	cc := "DiscordSettings"
	keep := &notifications.Notification{
		Name:           "Live",
		Implementation: "Discord",
		ConfigContract: &cc,
		Settings:       "{}",
		Tags:           []int{},
	}
	if _, err := notifRepo.Insert(t.Context(), keep); err != nil {
		t.Fatalf("notifications Insert: %v", err)
	}

	statusRepo, err := notificationstatus.NewRepo(db)
	if err != nil {
		t.Fatalf("notificationstatus.NewRepo: %v", err)
	}
	survivor := &notificationstatus.Status{ProviderID: keep.ID, EscalationLevel: 1}
	orphan := &notificationstatus.Status{ProviderID: 9999, EscalationLevel: 1}
	if _, err := statusRepo.Insert(t.Context(), survivor); err != nil {
		t.Fatalf("notificationstatus Insert survivor: %v", err)
	}
	if _, err := statusRepo.Insert(t.Context(), orphan); err != nil {
		t.Fatalf("notificationstatus Insert orphan: %v", err)
	}

	task, err := housekeeping.NewCleanupOrphanedNotificationStatus(db, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := task.Clean(t.Context()); err != nil {
		t.Fatalf("Clean: %v", err)
	}

	if _, err := statusRepo.Get(t.Context(), orphan.ID); err == nil {
		t.Errorf("orphan status survived")
	}
	if _, err := statusRepo.Get(t.Context(), survivor.ID); err != nil {
		t.Errorf("survivor status dropped: %v", err)
	}
}

func TestNewCleanupOrphanedStatus_NilDB(t *testing.T) {
	t.Parallel()
	if _, err := housekeeping.NewCleanupOrphanedIndexerStatus(nil, nil); err == nil {
		t.Errorf("IndexerStatus(nil): want error")
	}
	if _, err := housekeeping.NewCleanupOrphanedDownloadClientStatus(nil, nil); err == nil {
		t.Errorf("DownloadClientStatus(nil): want error")
	}
	if _, err := housekeeping.NewCleanupOrphanedImportListStatus(nil, nil); err == nil {
		t.Errorf("ImportListStatus(nil): want error")
	}
	if _, err := housekeeping.NewCleanupOrphanedNotificationStatus(nil, nil); err == nil {
		t.Errorf("NotificationStatus(nil): want error")
	}
}
