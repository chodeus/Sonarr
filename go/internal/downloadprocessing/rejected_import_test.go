package downloadprocessing_test

import (
	"context"
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadprocessing"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

type stubSettings struct {
	settings *indexers.CachedIndexerSettings
	err      error
}

func (s stubSettings) GetSettings(context.Context, int) (*indexers.CachedIndexerSettings, error) {
	return s.settings, s.err
}

func rejectedResult(reason episodeimport.ImportRejectionReason) *episodeimport.ImportResult {
	return &episodeimport.ImportResult{
		Decision: episodeimport.NewImportDecision(nil, episodeimport.NewImportRejection(reason, "bad file")),
		Errors:   []string{"bad file"},
	}
}

func trackedWithRelease(indexerID int) *trackeddownloads.TrackedDownload {
	return &trackeddownloads.TrackedDownload{
		DownloadID:    "abc",
		DownloadItem:  &downloadclients.Item{Title: "Some.Release"},
		RemoteEpisode: &decisionengine.Subject{Release: &parser.ReleaseInfo{IndexerID: indexerID}},
		State:         trackeddownloads.StateImportPending,
	}
}

func TestRejectedImport_NotRejected_ReturnsFalse(t *testing.T) {
	t.Parallel()
	svc := downloadprocessing.NewRejectedImportService(stubSettings{}, nil)
	// An imported (no-error) result is not a rejection.
	imported := &episodeimport.ImportResult{Decision: episodeimport.NewImportDecision(nil), Errors: nil}
	if svc.Process(context.Background(), trackedWithRelease(1), imported) {
		t.Error("non-rejected result should return false")
	}
}

func TestRejectedImport_NoRelease_ReturnsFalse(t *testing.T) {
	t.Parallel()
	svc := downloadprocessing.NewRejectedImportService(stubSettings{}, nil)
	td := &trackeddownloads.TrackedDownload{DownloadID: "x", DownloadItem: &downloadclients.Item{}}
	if svc.Process(context.Background(), td, rejectedResult(episodeimport.ImportRejectionDangerousFile)) {
		t.Error("no release should return false")
	}
}

func TestRejectedImport_DangerousFile_FailsWhenIndexerConfigured(t *testing.T) {
	t.Parallel()
	svc := downloadprocessing.NewRejectedImportService(
		stubSettings{settings: &indexers.CachedIndexerSettings{FailDownloads: []indexers.FailDownloads{indexers.FailDownloadsPotentiallyDangerous}}}, nil)
	td := trackedWithRelease(7)
	if !svc.Process(context.Background(), td, rejectedResult(episodeimport.ImportRejectionDangerousFile)) {
		t.Fatal("should handle the rejection")
	}
	if td.Status != trackeddownloads.StatusError || td.State != trackeddownloads.StateFailedPending {
		t.Errorf("dangerous file with configured indexer should Fail; got status=%v state=%v", td.Status, td.State)
	}
}

func TestRejectedImport_ExecutableFile_FailsWhenConfigured(t *testing.T) {
	t.Parallel()
	svc := downloadprocessing.NewRejectedImportService(
		stubSettings{settings: &indexers.CachedIndexerSettings{FailDownloads: []indexers.FailDownloads{indexers.FailDownloadsExecutables}}}, nil)
	td := trackedWithRelease(7)
	svc.Process(context.Background(), td, rejectedResult(episodeimport.ImportRejectionExecutableFile))
	if td.State != trackeddownloads.StateFailedPending {
		t.Errorf("executable file with configured indexer should Fail; state=%v", td.State)
	}
}

func TestRejectedImport_DangerousFile_WarnsWhenIndexerNotConfigured(t *testing.T) {
	t.Parallel()
	// Indexer settings present but FailDownloads does NOT include the flag.
	svc := downloadprocessing.NewRejectedImportService(
		stubSettings{settings: &indexers.CachedIndexerSettings{FailDownloads: nil}}, nil)
	td := trackedWithRelease(7)
	if !svc.Process(context.Background(), td, rejectedResult(episodeimport.ImportRejectionDangerousFile)) {
		t.Fatal("should handle the rejection")
	}
	if td.Status != trackeddownloads.StatusWarning || td.State == trackeddownloads.StateFailedPending {
		t.Errorf("unconfigured indexer should Warn (not Fail); status=%v state=%v", td.Status, td.State)
	}
	if len(td.StatusMessages) == 0 {
		t.Error("warn should set status messages from the result errors")
	}
}

func TestRejectedImport_NilSettings_Warns(t *testing.T) {
	t.Parallel()
	// GetSettings returns nil (unknown indexer) → warn, not fail.
	svc := downloadprocessing.NewRejectedImportService(stubSettings{settings: nil}, nil)
	td := trackedWithRelease(7)
	svc.Process(context.Background(), td, rejectedResult(episodeimport.ImportRejectionDangerousFile))
	if td.Status != trackeddownloads.StatusWarning {
		t.Errorf("nil settings should Warn; status=%v", td.Status)
	}
}

func TestRejectedImport_SettingsError_Warns(t *testing.T) {
	t.Parallel()
	svc := downloadprocessing.NewRejectedImportService(stubSettings{err: errors.New("boom")}, nil)
	td := trackedWithRelease(7)
	svc.Process(context.Background(), td, rejectedResult(episodeimport.ImportRejectionExecutableFile))
	if td.Status != trackeddownloads.StatusWarning || td.State == trackeddownloads.StateFailedPending {
		t.Errorf("settings error should degrade to Warn; status=%v state=%v", td.Status, td.State)
	}
}

func TestRejectedImport_OtherReason_Warns(t *testing.T) {
	t.Parallel()
	// A rejection reason not in the fail-set → warn even when the indexer
	// fails dangerous/executable downloads.
	svc := downloadprocessing.NewRejectedImportService(
		stubSettings{settings: &indexers.CachedIndexerSettings{FailDownloads: []indexers.FailDownloads{indexers.FailDownloadsExecutables}}}, nil)
	td := trackedWithRelease(7)
	svc.Process(context.Background(), td, rejectedResult(episodeimport.ImportRejectionMultiSeason))
	if td.Status != trackeddownloads.StatusWarning || td.State == trackeddownloads.StateFailedPending {
		t.Errorf("non-fail reason should Warn; status=%v state=%v", td.Status, td.State)
	}
}
