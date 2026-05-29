package downloadprocessing_test

import (
	"context"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadprocessing"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/ospath"
	"sonarr.tv/sonarr/internal/series"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

type fakeStore struct {
	tds []*trackeddownloads.TrackedDownload
}

func (f fakeStore) GetTrackedDownloads() []*trackeddownloads.TrackedDownload { return f.tds }

type fakeImporter struct {
	calls   int
	gotPath string
	gotSer  *series.Series
	gotItem *downloadclients.Item
	results []*episodeimport.ImportResult
}

func (f *fakeImporter) ProcessPath(path string, _ episodeimport.ImportMode, ser *series.Series, dci *downloadclients.Item) []*episodeimport.ImportResult {
	f.calls++
	f.gotPath = path
	f.gotSer = ser
	f.gotItem = dci
	return f.results
}

func completedTD(id string, ser *series.Series, output string) *trackeddownloads.TrackedDownload {
	td := &trackeddownloads.TrackedDownload{
		DownloadID:   id,
		State:        trackeddownloads.StateDownloading,
		DownloadItem: &downloadclients.Item{DownloadID: id, Status: downloadclients.DownloadItemStatusCompleted, OutputPath: ospath.New(output)},
	}
	if ser != nil {
		td.RemoteEpisode = &decisionengine.Subject{Series: ser}
	}
	return td
}

// importedResult returns a result that ResultType()==Imported (empty
// errors + an approved decision).
func importedResult() *episodeimport.ImportResult {
	le := episodeimport.NewLocalEpisode()
	return &episodeimport.ImportResult{Decision: episodeimport.NewImportDecision(le)}
}

func newSvc(t *testing.T, store downloadprocessing.TrackedDownloadStore, imp downloadprocessing.Importer) *downloadprocessing.Service {
	t.Helper()
	s, err := downloadprocessing.NewService(store, imp, slog.Default())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return s
}

// J399: a completed + matched download imports via ProcessPath and is
// marked Imported when all results imported.
func TestProcess_CompletedMatched_Imports_J399(t *testing.T) {
	t.Parallel()
	ser := &series.Series{ID: 42, Path: "/tv/Show"}
	td := completedTD("dl1", ser, "/dl/Show.S01E01")
	imp := &fakeImporter{results: []*episodeimport.ImportResult{importedResult()}}
	n := newSvc(t, fakeStore{tds: []*trackeddownloads.TrackedDownload{td}}, imp).ProcessTrackedDownloads(context.Background())
	if n != 1 || imp.calls != 1 {
		t.Fatalf("n=%d calls=%d, want 1/1", n, imp.calls)
	}
	if imp.gotPath != "/dl/Show.S01E01" || imp.gotSer != ser || imp.gotItem != td.DownloadItem {
		t.Errorf("ProcessPath args: path=%q ser=%v item=%v", imp.gotPath, imp.gotSer, imp.gotItem)
	}
	if td.State != trackeddownloads.StateImported {
		t.Errorf("state = %v, want Imported", td.State)
	}
}

// J399: a download the client still reports Downloading is skipped.
func TestProcess_NotCompleted_Skipped_J399(t *testing.T) {
	t.Parallel()
	td := completedTD("dl1", &series.Series{ID: 1}, "/dl/x")
	td.DownloadItem.Status = downloadclients.DownloadItemStatusDownloading
	imp := &fakeImporter{}
	if n := newSvc(t, fakeStore{tds: []*trackeddownloads.TrackedDownload{td}}, imp).ProcessTrackedDownloads(context.Background()); n != 0 || imp.calls != 0 {
		t.Errorf("n=%d calls=%d, want 0/0", n, imp.calls)
	}
}

// J399: a completed but unmatched download (no series) is skipped (not
// imported) — the import-blocked state is a documented follow-up.
func TestProcess_CompletedUnmatched_Skipped_J399(t *testing.T) {
	t.Parallel()
	td := completedTD("dl1", nil, "/dl/x")
	imp := &fakeImporter{}
	if n := newSvc(t, fakeStore{tds: []*trackeddownloads.TrackedDownload{td}}, imp).ProcessTrackedDownloads(context.Background()); n != 0 || imp.calls != 0 {
		t.Errorf("n=%d calls=%d, want 0/0", n, imp.calls)
	}
}

// J399: a partial import (some results not imported) leaves the state
// Downloading for retry.
func TestProcess_PartialImport_NotMarked_J399(t *testing.T) {
	t.Parallel()
	ser := &series.Series{ID: 1, Path: "/tv/S"}
	td := completedTD("dl1", ser, "/dl/x")
	// One imported + one rejected (rejected decision → not Imported).
	rejected := &episodeimport.ImportResult{Decision: episodeimport.NewImportDecision(episodeimport.NewLocalEpisode(), episodeimport.NewImportRejection(episodeimport.ImportRejectionError, "no")), Errors: []string{"no"}}
	imp := &fakeImporter{results: []*episodeimport.ImportResult{importedResult(), rejected}}
	if n := newSvc(t, fakeStore{tds: []*trackeddownloads.TrackedDownload{td}}, imp).ProcessTrackedDownloads(context.Background()); n != 0 {
		t.Errorf("n=%d, want 0 (partial)", n)
	}
	if td.State != trackeddownloads.StateDownloading {
		t.Errorf("state = %v, want still Downloading", td.State)
	}
}

// J399: an already-Imported download is skipped.
func TestProcess_AlreadyImported_Skipped_J399(t *testing.T) {
	t.Parallel()
	td := completedTD("dl1", &series.Series{ID: 1}, "/dl/x")
	td.State = trackeddownloads.StateImported
	imp := &fakeImporter{}
	if n := newSvc(t, fakeStore{tds: []*trackeddownloads.TrackedDownload{td}}, imp).ProcessTrackedDownloads(context.Background()); n != 0 || imp.calls != 0 {
		t.Errorf("n=%d calls=%d, want 0/0", n, imp.calls)
	}
}
