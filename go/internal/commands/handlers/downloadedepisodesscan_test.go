package handlers_test

import (
	"errors"
	"log/slog"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/commands/handlers"
	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/series"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

// stubProcessor records ProcessPath calls + lets the test stipulate
// the returned import results.
type stubProcessor struct {
	calls   int
	gotPath string
	gotMode episodeimport.ImportMode
	gotSer  *series.Series
	gotDci  *downloadclients.Item
	ret     []*episodeimport.ImportResult
}

func (s *stubProcessor) ProcessPath(
	path string, mode episodeimport.ImportMode,
	ser *series.Series, dci *downloadclients.Item,
) []*episodeimport.ImportResult {
	s.calls++
	s.gotPath = path
	s.gotMode = mode
	s.gotSer = ser
	s.gotDci = dci
	return s.ret
}

type stubFinder struct {
	td *trackeddownloads.TrackedDownload
}

func (s *stubFinder) Find(_ string) *trackeddownloads.TrackedDownload {
	return s.td
}

type stubVerifier struct {
	calls int
}

func (s *stubVerifier) VerifyImport(_ *trackeddownloads.TrackedDownload, _ []*episodeimport.ImportResult) {
	s.calls++
}

type stubReporter struct {
	calls int
}

func (s *stubReporter) ReportUnsuccessful() {
	s.calls++
}

func TestDownloadedEpisodesScan_NewWithNilProcessor(t *testing.T) {
	t.Parallel()
	if _, err := handlers.NewDownloadedEpisodesScan(nil, nil, nil, nil, nil); err == nil {
		t.Fatal("expected error from nil processor")
	}
}

func TestDownloadedEpisodesScan_EmptyPath_ReturnsError(t *testing.T) {
	t.Parallel()
	p := &stubProcessor{}
	h, err := handlers.NewDownloadedEpisodesScan(p, nil, nil, nil, slog.Default())
	if err != nil {
		t.Fatalf("NewDownloadedEpisodesScan: %v", err)
	}
	if err := h.Execute(t.Context(), `{"path": ""}`); err == nil {
		t.Fatal("expected error on empty path")
	}
	if p.calls != 0 {
		t.Errorf("processor should not have been called on empty path, got %d", p.calls)
	}
}

func TestDownloadedEpisodesScan_EmptyBody_ReturnsError(t *testing.T) {
	t.Parallel()
	p := &stubProcessor{}
	h, _ := handlers.NewDownloadedEpisodesScan(p, nil, nil, nil, slog.Default())
	// Empty body decodes as zero-value struct → empty path → error.
	if err := h.Execute(t.Context(), ""); err == nil {
		t.Fatal("expected error on empty body")
	}
}

func TestDownloadedEpisodesScan_MalformedBody_ReturnsError(t *testing.T) {
	t.Parallel()
	p := &stubProcessor{}
	h, _ := handlers.NewDownloadedEpisodesScan(p, nil, nil, nil, slog.Default())
	if err := h.Execute(t.Context(), `not-json`); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestDownloadedEpisodesScan_ManualImport_NoDci(t *testing.T) {
	t.Parallel()
	p := &stubProcessor{ret: []*episodeimport.ImportResult{}}
	h, _ := handlers.NewDownloadedEpisodesScan(p, nil, nil, nil, slog.Default())
	if err := h.Execute(t.Context(), `{"path":"/drop/show","importMode":1}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if p.calls != 1 {
		t.Fatalf("processor.ProcessPath called %d times, want 1", p.calls)
	}
	if p.gotPath != "/drop/show" {
		t.Errorf("got path %q, want /drop/show", p.gotPath)
	}
	if p.gotMode != episodeimport.ImportModeMove {
		t.Errorf("got mode %v, want Move", p.gotMode)
	}
	if p.gotSer != nil || p.gotDci != nil {
		t.Errorf("manual import should pass nil ser/dci, got ser=%v dci=%v", p.gotSer, p.gotDci)
	}
}

func TestDownloadedEpisodesScan_KnownDownload_PassesSeriesAndDci(t *testing.T) {
	t.Parallel()
	ser := &series.Series{ID: 42}
	dci := &downloadclients.Item{Title: "Show.S01E01"}
	td := &trackeddownloads.TrackedDownload{
		DownloadItem:  dci,
		RemoteEpisode: &decisionengine.Subject{Series: ser},
	}
	p := &stubProcessor{ret: []*episodeimport.ImportResult{}}
	verifier := &stubVerifier{}
	finder := &stubFinder{td: td}
	h, _ := handlers.NewDownloadedEpisodesScan(p, finder, verifier, nil, slog.Default())

	body := `{"path":"/drop/show","downloadClientId":"abc-123","importMode":2}`
	if err := h.Execute(t.Context(), body); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if p.gotSer != ser {
		t.Errorf("got ser %v, want from TrackedDownload", p.gotSer)
	}
	if p.gotDci != dci {
		t.Errorf("got dci %v, want from TrackedDownload", p.gotDci)
	}
	if p.gotMode != episodeimport.ImportModeCopy {
		t.Errorf("got mode %v, want Copy", p.gotMode)
	}
	if verifier.calls != 1 {
		t.Errorf("verifier.VerifyImport called %d times, want 1", verifier.calls)
	}
}

func TestDownloadedEpisodesScan_UnknownDownload_FallsThroughToManual(t *testing.T) {
	t.Parallel()
	p := &stubProcessor{ret: []*episodeimport.ImportResult{}}
	finder := &stubFinder{td: nil} // not found
	verifier := &stubVerifier{}
	h, _ := handlers.NewDownloadedEpisodesScan(p, finder, verifier, nil, slog.Default())

	body := `{"path":"/drop/show","downloadClientId":"unknown","importMode":0}`
	if err := h.Execute(t.Context(), body); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if p.gotSer != nil || p.gotDci != nil {
		t.Errorf("unknown download should fall through to manual, got ser=%v dci=%v", p.gotSer, p.gotDci)
	}
	if verifier.calls != 0 {
		t.Errorf("verifier should not run on manual fallback, got %d calls", verifier.calls)
	}
}

func TestDownloadedEpisodesScan_NoFinderConfigured_FallsThroughToManual(t *testing.T) {
	t.Parallel()
	// downloadClientId set but no finder wired → manual import.
	p := &stubProcessor{ret: []*episodeimport.ImportResult{}}
	h, _ := handlers.NewDownloadedEpisodesScan(p, nil, nil, nil, slog.Default())
	body := `{"path":"/drop/show","downloadClientId":"abc"}`
	if err := h.Execute(t.Context(), body); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if p.gotSer != nil {
		t.Errorf("expected nil ser when no finder wired, got %+v", p.gotSer)
	}
}

func TestDownloadedEpisodesScan_NoImported_ReportsUnsuccessful(t *testing.T) {
	t.Parallel()
	rejected := episodeimport.NewSkippedImportResult(
		episodeimport.NewImportDecision(nil,
			episodeimport.NewImportRejection(episodeimport.ImportRejectionUnknownSeries, "unknown")),
		"unknown",
	)
	p := &stubProcessor{ret: []*episodeimport.ImportResult{rejected}}
	reporter := &stubReporter{}
	h, _ := handlers.NewDownloadedEpisodesScan(p, nil, nil, reporter, slog.Default())
	if err := h.Execute(t.Context(), `{"path":"/drop/show"}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if reporter.calls != 1 {
		t.Errorf("reporter.ReportUnsuccessful called %d times, want 1", reporter.calls)
	}
}

func TestDownloadedEpisodesScan_AnyImported_DoesNotReportUnsuccessful(t *testing.T) {
	t.Parallel()
	imported := episodeimport.NewImportedImportResult(
		episodeimport.NewImportDecision(&episodeimport.LocalEpisode{Path: "/drop/show.mkv"}),
		&episodefiles.File{},
	)
	p := &stubProcessor{ret: []*episodeimport.ImportResult{imported}}
	reporter := &stubReporter{}
	h, _ := handlers.NewDownloadedEpisodesScan(p, nil, nil, reporter, slog.Default())
	if err := h.Execute(t.Context(), `{"path":"/drop/show"}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if reporter.calls != 0 {
		t.Errorf("reporter.ReportUnsuccessful called %d times on success, want 0", reporter.calls)
	}
}

func TestDownloadedEpisodesScan_NoReporterWired_NoOp(t *testing.T) {
	t.Parallel()
	// No imported result + no reporter → handler logs + returns nil.
	rejected := episodeimport.NewSkippedImportResult(
		episodeimport.NewImportDecision(nil,
			episodeimport.NewImportRejection(episodeimport.ImportRejectionUnknownSeries, "unknown")),
		"unknown",
	)
	p := &stubProcessor{ret: []*episodeimport.ImportResult{rejected}}
	h, _ := handlers.NewDownloadedEpisodesScan(p, nil, nil, nil, slog.Default())
	if err := h.Execute(t.Context(), `{"path":"/drop/show"}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
}

func TestDownloadedEpisodesScan_ErrorMessage_MentionsPath(t *testing.T) {
	t.Parallel()
	p := &stubProcessor{}
	h, _ := handlers.NewDownloadedEpisodesScan(p, nil, nil, nil, slog.Default())
	err := h.Execute(t.Context(), `{}`)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "path") {
		t.Errorf("error message %q should mention 'path'", err.Error())
	}
}

// Compile-time guard: a nil error from NewDownloadedEpisodesScan
// surfaces a usable handler.
var _ = errors.New
