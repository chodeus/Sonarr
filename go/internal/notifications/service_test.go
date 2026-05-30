package notifications_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
	"sonarr.tv/sonarr/internal/trackeddownloads"
	"sonarr.tv/sonarr/internal/updatehistory"
)

// stubRecorder satisfies notifications.serviceRecorder (via duck-
// typing — the recorder interface lives unexported in
// service.go). Used to verify per-provider RecordSuccess /
// RecordFailure side-effects.
type stubRecorder struct {
	mu        sync.Mutex
	successes []int
	failures  []int
}

func (s *stubRecorder) RecordSuccess(_ context.Context, providerID int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.successes = append(s.successes, providerID)
	return nil
}

func (s *stubRecorder) RecordFailure(_ context.Context, providerID int, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures = append(s.failures, providerID)
	return nil
}

// hookRecorder is a stub Provider that records which OnX hooks
// fired with what payloads. The same type is reused across the
// service tests — each test builds Provider lookup via the
// registry so the factory returns the right hookRecorder.
type hookRecorder struct {
	id                 int
	def                *notifications.Notification
	failOn             map[string]error
	mu                 sync.Mutex
	grabCalls          int
	downloadCalls      int
	seriesAddCalls     int
	seriesDelCalls     int
	renameCalls        int
	renameMsg          notifications.RenameMessage
	episodeDelCalls    int
	healthCalls        int
	healthRestored     int
	processCalls       int
	importCompleteCall int
	importCompleteMsg  notifications.ImportCompleteMessage
	appUpdateCalls     int
	appUpdateMsg       notifications.ApplicationUpdateMessage
	manualCalls        int
	manualMsg          notifications.ManualInteractionRequiredMessage
}

func (h *hookRecorder) Name() string                            { return "Stub" }
func (h *hookRecorder) Link() string                            { return "" }
func (h *hookRecorder) Definition() *notifications.Notification { return h.def }

func (h *hookRecorder) OnGrab(_ context.Context, _ notifications.GrabMessage) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.grabCalls++
	return h.failOn["OnGrab"]
}
func (h *hookRecorder) OnDownload(_ context.Context, _ notifications.DownloadMessage) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.downloadCalls++
	return h.failOn["OnDownload"]
}
func (h *hookRecorder) OnRename(_ context.Context, msg notifications.RenameMessage) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.renameCalls++
	h.renameMsg = msg
	return h.failOn["OnRename"]
}
func (h *hookRecorder) OnImportComplete(_ context.Context, msg notifications.ImportCompleteMessage) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.failOn != nil {
		if err := h.failOn["OnImportComplete"]; err != nil {
			return err
		}
	}
	h.importCompleteCall++
	h.importCompleteMsg = msg
	return nil
}
func (h *hookRecorder) OnEpisodeFileDelete(_ context.Context, _ notifications.EpisodeDeleteMessage) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.episodeDelCalls++
	return h.failOn["OnEpisodeFileDelete"]
}
func (h *hookRecorder) OnSeriesAdd(_ context.Context, _ notifications.SeriesAddMessage) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seriesAddCalls++
	return h.failOn["OnSeriesAdd"]
}
func (h *hookRecorder) OnSeriesDelete(_ context.Context, _ notifications.SeriesDeleteMessage) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seriesDelCalls++
	return h.failOn["OnSeriesDelete"]
}
func (h *hookRecorder) OnHealthIssue(_ context.Context, _ notifications.HealthCheckMessage) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.healthCalls++
	return h.failOn["OnHealthIssue"]
}
func (h *hookRecorder) OnHealthRestored(_ context.Context, _ notifications.HealthCheckMessage) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.healthRestored++
	return h.failOn["OnHealthRestored"]
}
func (h *hookRecorder) OnApplicationUpdate(_ context.Context, msg notifications.ApplicationUpdateMessage) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.appUpdateCalls++
	h.appUpdateMsg = msg
	return h.failOn["OnApplicationUpdate"]
}
func (h *hookRecorder) OnManualInteractionRequired(_ context.Context, msg notifications.ManualInteractionRequiredMessage) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.manualCalls++
	h.manualMsg = msg
	return h.failOn["OnManualInteractionRequired"]
}
func (h *hookRecorder) ProcessQueue(_ context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.processCalls++
	return nil
}
func (h *hookRecorder) Test(_ context.Context) ([]notifications.TestFailure, error) {
	return nil, nil
}
func (h *hookRecorder) SupportsOnGrab() bool                        { return true }
func (h *hookRecorder) SupportsOnDownload() bool                    { return true }
func (h *hookRecorder) SupportsOnUpgrade() bool                     { return true }
func (h *hookRecorder) SupportsOnImportComplete() bool              { return true }
func (h *hookRecorder) SupportsOnRename() bool                      { return true }
func (h *hookRecorder) SupportsOnSeriesAdd() bool                   { return true }
func (h *hookRecorder) SupportsOnSeriesDelete() bool                { return true }
func (h *hookRecorder) SupportsOnEpisodeFileDelete() bool           { return true }
func (h *hookRecorder) SupportsOnEpisodeFileDeleteForUpgrade() bool { return true }
func (h *hookRecorder) SupportsOnHealthIssue() bool                 { return true }
func (h *hookRecorder) SupportsOnHealthRestored() bool              { return true }
func (h *hookRecorder) SupportsOnApplicationUpdate() bool           { return true }
func (h *hookRecorder) SupportsOnManualInteractionRequired() bool   { return true }
func (h *hookRecorder) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return struct{}{}, nil
}

// buildService builds a notifications.Service backed by an
// in-memory stub repo populated with the given rows. The registry
// constructs hookRecorder instances per row; the recorder map is
// returned so tests can inspect side-effects per provider.
func buildService(t *testing.T, rows []notifications.Notification, failPlan map[int]map[string]error) (*notifications.Service, map[int]*hookRecorder, *stubRecorder) {
	t.Helper()
	hooks := make(map[int]*hookRecorder, len(rows))
	registry := notifications.NewRegistry()
	if err := registry.Register("Stub", "StubSettings", func(def *notifications.Notification, _ notifications.Dependencies) (notifications.Provider, error) {
		h := &hookRecorder{id: def.ID, def: def, failOn: failPlan[def.ID]}
		hooks[def.ID] = h
		return h, nil
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	rec := &stubRecorder{}
	f, err := notifications.NewFactory(&stubRepo{rows: rows}, registry, nil, notifications.Dependencies{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewFactory: %v", err)
	}
	svc, err := notifications.NewService(f, rec, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, hooks, rec
}

func TestNewService_RejectsNilFactory(t *testing.T) {
	t.Parallel()
	if _, err := notifications.NewService(nil, nil, nil); err == nil {
		t.Errorf("nil factory: expected error")
	}
}

// TestHandleEpisodeGrabbedEvent_FansOut verifies the dispatcher
// fires OnGrab on every OnGrab-enabled provider and records
// success on the recorder.
func TestHandleEpisodeGrabbedEvent_FansOut(t *testing.T) {
	t.Parallel()
	rows := []notifications.Notification{
		{ID: 1, Name: "A", Implementation: "Stub", ConfigContract: stubContract(), OnGrab: true},
		{ID: 2, Name: "B", Implementation: "Stub", ConfigContract: stubContract(), OnGrab: true},
		{ID: 3, Name: "Skip", Implementation: "Stub", ConfigContract: stubContract(), OnDownload: true}, // OnGrab=false
	}
	svc, hooks, rec := buildService(t, rows, nil)
	s := &series.Series{Title: "Show"}
	ev := decisionengine.EpisodeGrabbedEvent{
		Episode: &decisionengine.Subject{
			Series:            s,
			Episodes:          []episodes.Episode{{SeasonNumber: 1, EpisodeNumber: 1, Title: "Pilot"}},
			ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{},
		},
		DownloadClient:     "Sabnzbd",
		DownloadClientName: "My SAB",
		DownloadID:         "abc",
	}
	if err := svc.HandleEpisodeGrabbedEvent(t.Context(), ev); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if hooks[1].grabCalls != 1 || hooks[2].grabCalls != 1 {
		t.Errorf("OnGrab call counts: A=%d B=%d", hooks[1].grabCalls, hooks[2].grabCalls)
	}
	if _, ok := hooks[3]; ok {
		t.Errorf("Skip provider should not have been constructed")
	}
	if len(rec.successes) != 2 {
		t.Errorf("expected 2 RecordSuccess calls, got %d", len(rec.successes))
	}
}

// TestHandleEpisodeGrabbedEvent_PerProviderFailureIsolated verifies
// that one provider erroring out doesn't abort the fan-out chain,
// and that the failure is recorded on the recorder.
func TestHandleEpisodeGrabbedEvent_PerProviderFailureIsolated(t *testing.T) {
	t.Parallel()
	rows := []notifications.Notification{
		{ID: 1, Name: "broken", Implementation: "Stub", ConfigContract: stubContract(), OnGrab: true},
		{ID: 2, Name: "ok", Implementation: "Stub", ConfigContract: stubContract(), OnGrab: true},
	}
	failPlan := map[int]map[string]error{
		1: {"OnGrab": errors.New("transport down")},
	}
	svc, hooks, rec := buildService(t, rows, failPlan)
	ev := decisionengine.EpisodeGrabbedEvent{
		Episode: &decisionengine.Subject{
			Series:   &series.Series{Title: "Show"},
			Episodes: []episodes.Episode{{SeasonNumber: 1, EpisodeNumber: 1}},
		},
	}
	_ = svc.HandleEpisodeGrabbedEvent(t.Context(), ev)
	if hooks[1].grabCalls != 1 || hooks[2].grabCalls != 1 {
		t.Errorf("both providers should fire: 1=%d 2=%d", hooks[1].grabCalls, hooks[2].grabCalls)
	}
	if len(rec.failures) != 1 || rec.failures[0] != 1 {
		t.Errorf("failures: %v want [1]", rec.failures)
	}
	if len(rec.successes) != 1 || rec.successes[0] != 2 {
		t.Errorf("successes: %v want [2]", rec.successes)
	}
}

// TestHandleEpisodeGrabbedEvent_TagsFilter verifies that
// ShouldHandleSeries gates the per-provider invocation: providers
// with tags that don't intersect the series tags are skipped.
func TestHandleEpisodeGrabbedEvent_TagsFilter(t *testing.T) {
	t.Parallel()
	rows := []notifications.Notification{
		{ID: 1, Name: "no-tags", Implementation: "Stub", ConfigContract: stubContract(), OnGrab: true},
		{ID: 2, Name: "matching", Implementation: "Stub", ConfigContract: stubContract(), OnGrab: true, Tags: []int{42}},
		{ID: 3, Name: "mismatch", Implementation: "Stub", ConfigContract: stubContract(), OnGrab: true, Tags: []int{99}},
	}
	svc, hooks, _ := buildService(t, rows, nil)
	s := &series.Series{Title: "Show", Tags: []int{42}}
	ev := decisionengine.EpisodeGrabbedEvent{
		Episode: &decisionengine.Subject{Series: s, Episodes: []episodes.Episode{{SeasonNumber: 1, EpisodeNumber: 1}}},
	}
	_ = svc.HandleEpisodeGrabbedEvent(t.Context(), ev)
	if hooks[1].grabCalls != 1 {
		t.Errorf("no-tags should fire, got %d", hooks[1].grabCalls)
	}
	if hooks[2].grabCalls != 1 {
		t.Errorf("matching tags should fire, got %d", hooks[2].grabCalls)
	}
	if hooks[3].grabCalls != 0 {
		t.Errorf("mismatching tags should NOT fire, got %d", hooks[3].grabCalls)
	}
}

// TestHandleHealthCheckFailedEvent_IncludeWarningsGate verifies
// that Warning checks only fire on providers with
// IncludeHealthWarnings=true, while Error checks always fire.
func TestHandleHealthCheckFailedEvent_IncludeWarningsGate(t *testing.T) {
	t.Parallel()
	rows := []notifications.Notification{
		{ID: 1, Name: "strict", Implementation: "Stub", ConfigContract: stubContract(), OnHealthIssue: true, IncludeHealthWarnings: false},
		{ID: 2, Name: "lax", Implementation: "Stub", ConfigContract: stubContract(), OnHealthIssue: true, IncludeHealthWarnings: true},
	}

	// Error check fires on both.
	t.Run("error fires both", func(t *testing.T) {
		t.Parallel()
		svc, hooks, _ := buildService(t, rows, nil)
		_ = svc.HandleHealthCheckFailedEvent(t.Context(), healthcheck.FailedEvent{
			HealthCheck: healthcheck.HealthCheck{Type: healthcheck.ResultError, Source: "X"},
		})
		if hooks[1].healthCalls != 1 || hooks[2].healthCalls != 1 {
			t.Errorf("Error: strict=%d lax=%d (want 1,1)", hooks[1].healthCalls, hooks[2].healthCalls)
		}
	})

	// Warning check fires only on lax (IncludeHealthWarnings=true).
	t.Run("warning fires only on lax", func(t *testing.T) {
		t.Parallel()
		svc, hooks, _ := buildService(t, rows, nil)
		_ = svc.HandleHealthCheckFailedEvent(t.Context(), healthcheck.FailedEvent{
			HealthCheck: healthcheck.HealthCheck{Type: healthcheck.ResultWarning, Source: "X"},
		})
		if hooks[1].healthCalls != 0 {
			t.Errorf("strict should suppress warning, got %d", hooks[1].healthCalls)
		}
		if hooks[2].healthCalls != 1 {
			t.Errorf("lax should fire warning, got %d", hooks[2].healthCalls)
		}
	})
}

// TestHandleHealthCheckFailedEvent_StartupGraceSuppresses verifies
// that IsInStartupGracePeriod=true short-circuits the dispatcher.
func TestHandleHealthCheckFailedEvent_StartupGraceSuppresses(t *testing.T) {
	t.Parallel()
	rows := []notifications.Notification{
		{ID: 1, Implementation: "Stub", ConfigContract: stubContract(), OnHealthIssue: true},
	}
	svc, hooks, _ := buildService(t, rows, nil)
	_ = svc.HandleHealthCheckFailedEvent(t.Context(), healthcheck.FailedEvent{
		HealthCheck:            healthcheck.HealthCheck{Type: healthcheck.ResultError},
		IsInStartupGracePeriod: true,
	})
	// Startup-grace short-circuits before the factory loads, so the
	// provider is never constructed. Either nil-hook OR zero-calls
	// satisfies "no fan-out fired".
	if h, ok := hooks[1]; ok && h.healthCalls != 0 {
		t.Errorf("startup-grace should suppress: got %d calls", h.healthCalls)
	}
}

func TestHandleHealthCheckRestoredEvent_FiresOnRestored(t *testing.T) {
	t.Parallel()
	rows := []notifications.Notification{
		{ID: 1, Implementation: "Stub", ConfigContract: stubContract(), OnHealthRestored: true, IncludeHealthWarnings: true},
	}
	svc, hooks, _ := buildService(t, rows, nil)
	_ = svc.HandleHealthCheckRestoredEvent(t.Context(), healthcheck.RestoredEvent{
		PreviousCheck: healthcheck.HealthCheck{Type: healthcheck.ResultWarning},
	})
	if hooks[1].healthRestored != 1 {
		t.Errorf("OnHealthRestored: got %d want 1", hooks[1].healthRestored)
	}
}

func TestHandleSeriesAddedEvent_FiresOnce(t *testing.T) {
	t.Parallel()
	rows := []notifications.Notification{
		{ID: 1, Implementation: "Stub", ConfigContract: stubContract(), OnSeriesAdd: true},
	}
	svc, hooks, _ := buildService(t, rows, nil)
	_ = svc.HandleSeriesAddedEvent(t.Context(), series.SeriesAddedEvent{Series: &series.Series{Title: "X"}})
	if hooks[1].seriesAddCalls != 1 {
		t.Errorf("OnSeriesAdd: got %d want 1", hooks[1].seriesAddCalls)
	}
}

func TestHandleSeriesDeletedEvent_FiresPerSeries(t *testing.T) {
	t.Parallel()
	rows := []notifications.Notification{
		{ID: 1, Implementation: "Stub", ConfigContract: stubContract(), OnSeriesDelete: true},
	}
	svc, hooks, _ := buildService(t, rows, nil)
	_ = svc.HandleSeriesDeletedEvent(t.Context(), series.SeriesDeletedEvent{
		Series:      []series.Series{{Title: "A"}, {Title: "B"}},
		DeleteFiles: true,
	})
	if hooks[1].seriesDelCalls != 2 {
		t.Errorf("OnSeriesDelete: got %d want 2 (one per series in batch)", hooks[1].seriesDelCalls)
	}
}

func TestHandleSeriesRenamedEvent_FiresOnRename(t *testing.T) {
	t.Parallel()
	rows := []notifications.Notification{
		{ID: 1, Implementation: "Stub", ConfigContract: stubContract(), OnRename: true},
		{ID: 2, Implementation: "Stub", ConfigContract: stubContract(), OnRename: false}, // not enabled → never fires
	}
	svc, hooks, _ := buildService(t, rows, nil)
	ev := episodefiles.SeriesRenamedEvent{
		Series: &series.Series{Title: "Renamed Show"},
		RenamedFiles: []episodefiles.RenamedEpisodeFile{
			{EpisodeFile: &episodefiles.File{}, PreviousPath: "/old/a.mkv"},
			{EpisodeFile: &episodefiles.File{}, PreviousPath: "/old/b.mkv"},
		},
	}
	if err := svc.HandleSeriesRenamedEvent(t.Context(), ev); err != nil {
		t.Fatalf("HandleSeriesRenamedEvent: %v", err)
	}
	if hooks[1].renameCalls != 1 {
		t.Errorf("OnRename: got %d want 1", hooks[1].renameCalls)
	}
	if len(hooks[1].renameMsg.RenamedFiles) != 2 {
		t.Errorf("RenamedFiles forwarded: got %d want 2", len(hooks[1].renameMsg.RenamedFiles))
	}
	if h, ok := hooks[2]; ok && h.renameCalls != 0 {
		t.Errorf("OnRename must NOT fire for an OnRename=false provider, got %d", h.renameCalls)
	}
}

func TestHandleSeriesRenamedEvent_NilSeries_NoOp(t *testing.T) {
	t.Parallel()
	rows := []notifications.Notification{
		{ID: 1, Implementation: "Stub", ConfigContract: stubContract(), OnRename: true},
	}
	svc, hooks, _ := buildService(t, rows, nil)
	if err := svc.HandleSeriesRenamedEvent(t.Context(), episodefiles.SeriesRenamedEvent{Series: nil}); err != nil {
		t.Fatalf("err: %v", err)
	}
	if h, ok := hooks[1]; ok && h.renameCalls != 0 {
		t.Errorf("nil series must no-op, got %d", h.renameCalls)
	}
}

func TestHandleSeriesRenamedEvent_TagGate(t *testing.T) {
	t.Parallel()
	rows := []notifications.Notification{
		{ID: 1, Implementation: "Stub", ConfigContract: stubContract(), OnRename: true, Tags: []int{5}},
	}
	svc, hooks, _ := buildService(t, rows, nil)
	// Series tags don't intersect the provider's tag filter → skipped.
	ev := episodefiles.SeriesRenamedEvent{Series: &series.Series{Title: "X", Tags: []int{9}}}
	if err := svc.HandleSeriesRenamedEvent(t.Context(), ev); err != nil {
		t.Fatalf("err: %v", err)
	}
	if hooks[1].renameCalls != 0 {
		t.Errorf("tag-mismatched provider must be skipped, got %d", hooks[1].renameCalls)
	}
}

func TestHandleApplicationUpdate_FiresOnEnabled(t *testing.T) {
	t.Parallel()
	rows := []notifications.Notification{
		{ID: 1, Implementation: "Stub", ConfigContract: stubContract(), OnApplicationUpdate: true},
		{ID: 2, Implementation: "Stub", ConfigContract: stubContract(), OnApplicationUpdate: false}, // not enabled → never fires
	}
	svc, hooks, _ := buildService(t, rows, nil)
	ev := updatehistory.UpdateInstalledEvent{PreviousVersion: "5.0.0.4", NewVersion: "5.0.0.5"}
	if err := svc.HandleApplicationUpdate(t.Context(), ev); err != nil {
		t.Fatalf("HandleApplicationUpdate: %v", err)
	}
	if hooks[1].appUpdateCalls != 1 {
		t.Errorf("OnApplicationUpdate: got %d want 1", hooks[1].appUpdateCalls)
	}
	if got := hooks[1].appUpdateMsg.Message; got != "Sonarr updated from 5.0.0.4 to 5.0.0.5" {
		t.Errorf("message = %q, want %q", got, "Sonarr updated from 5.0.0.4 to 5.0.0.5")
	}
	if hooks[1].appUpdateMsg.PreviousVersion != "5.0.0.4" || hooks[1].appUpdateMsg.NewVersion != "5.0.0.5" {
		t.Errorf("version fields = %+v", hooks[1].appUpdateMsg)
	}
	if h, ok := hooks[2]; ok && h.appUpdateCalls != 0 {
		t.Errorf("OnApplicationUpdate must NOT fire for a disabled provider, got %d", h.appUpdateCalls)
	}
}

func TestHandleApplicationUpdate_NoTagGate(t *testing.T) {
	t.Parallel()
	// A tagged provider must STILL fire — OnApplicationUpdate is a
	// global notification with no series-tag filter (faithful to C#).
	rows := []notifications.Notification{
		{ID: 1, Implementation: "Stub", ConfigContract: stubContract(), OnApplicationUpdate: true, Tags: []int{5}},
	}
	svc, hooks, _ := buildService(t, rows, nil)
	ev := updatehistory.UpdateInstalledEvent{PreviousVersion: "5.0.0.4", NewVersion: "5.0.0.5"}
	if err := svc.HandleApplicationUpdate(t.Context(), ev); err != nil {
		t.Fatalf("HandleApplicationUpdate: %v", err)
	}
	if hooks[1].appUpdateCalls != 1 {
		t.Errorf("tagged provider must still fire (no tag gate), got %d", hooks[1].appUpdateCalls)
	}
}

func TestHandleManualInteractionRequired_FiresWithSeriesMessage_J518(t *testing.T) {
	t.Parallel()
	rows := []notifications.Notification{
		{ID: 1, Implementation: "Stub", ConfigContract: stubContract(), OnManualInteractionRequired: true},
		{ID: 2, Implementation: "Stub", ConfigContract: stubContract(), OnManualInteractionRequired: false}, // disabled
	}
	svc, hooks, _ := buildService(t, rows, nil)
	td := &trackeddownloads.TrackedDownload{
		DownloadID:    "abc",
		DownloadItem:  &downloadclients.Item{DownloadID: "abc", Title: "Show.S01E01"},
		RemoteEpisode: &decisionengine.Subject{Series: &series.Series{Title: "Show"}},
	}
	ev := trackeddownloads.ManualInteractionRequiredEvent{TrackedDownload: td}
	if err := svc.HandleManualInteractionRequiredEvent(t.Context(), ev); err != nil {
		t.Fatalf("HandleManualInteractionRequiredEvent: %v", err)
	}
	if hooks[1].manualCalls != 1 {
		t.Errorf("OnManualInteractionRequired: got %d want 1", hooks[1].manualCalls)
	}
	if hooks[1].manualMsg.Message == "" {
		t.Error("message should be populated from the series")
	}
	if hooks[1].manualMsg.TrackedDownload != td {
		t.Error("message should carry the tracked download")
	}
	if h, ok := hooks[2]; ok && h.manualCalls != 0 {
		t.Errorf("disabled provider must not fire, got %d", h.manualCalls)
	}
}

func TestHandleManualInteractionRequired_FallsBackToDownloadTitle_J518(t *testing.T) {
	t.Parallel()
	rows := []notifications.Notification{
		{ID: 1, Implementation: "Stub", ConfigContract: stubContract(), OnManualInteractionRequired: true},
	}
	svc, hooks, _ := buildService(t, rows, nil)
	// No matched series → message falls back to the download item title.
	td := &trackeddownloads.TrackedDownload{
		DownloadID:   "x",
		DownloadItem: &downloadclients.Item{DownloadID: "x", Title: "Unmatched.Release.720p"},
	}
	ev := trackeddownloads.ManualInteractionRequiredEvent{TrackedDownload: td}
	if err := svc.HandleManualInteractionRequiredEvent(t.Context(), ev); err != nil {
		t.Fatal(err)
	}
	if hooks[1].manualCalls != 1 {
		t.Fatalf("should fire with title fallback, got %d", hooks[1].manualCalls)
	}
	if hooks[1].manualMsg.Message != "Unmatched.Release.720p" {
		t.Errorf("message = %q, want the download title", hooks[1].manualMsg.Message)
	}
}

func TestHandleManualInteractionRequired_EmptyMessage_NoFire_J518(t *testing.T) {
	t.Parallel()
	rows := []notifications.Notification{
		{ID: 1, Implementation: "Stub", ConfigContract: stubContract(), OnManualInteractionRequired: true},
	}
	svc, hooks, _ := buildService(t, rows, nil)
	// No series + no title → empty message → suppressed.
	td := &trackeddownloads.TrackedDownload{DownloadID: "x", DownloadItem: &downloadclients.Item{}}
	ev := trackeddownloads.ManualInteractionRequiredEvent{TrackedDownload: td}
	if err := svc.HandleManualInteractionRequiredEvent(t.Context(), ev); err != nil {
		t.Fatal(err)
	}
	if h, ok := hooks[1]; ok && h.manualCalls != 0 {
		t.Errorf("empty message must suppress the notification, got %d", h.manualCalls)
	}
}

func TestHandleManualInteractionRequired_TagGate_J518(t *testing.T) {
	t.Parallel()
	rows := []notifications.Notification{
		{ID: 1, Implementation: "Stub", ConfigContract: stubContract(), OnManualInteractionRequired: true, Tags: []int{5}},
	}
	svc, hooks, _ := buildService(t, rows, nil)
	td := &trackeddownloads.TrackedDownload{
		DownloadID:    "abc",
		DownloadItem:  &downloadclients.Item{Title: "Show.S01E01"},
		RemoteEpisode: &decisionengine.Subject{Series: &series.Series{Title: "Show", Tags: []int{9}}}, // tag mismatch
	}
	ev := trackeddownloads.ManualInteractionRequiredEvent{TrackedDownload: td}
	if err := svc.HandleManualInteractionRequiredEvent(t.Context(), ev); err != nil {
		t.Fatal(err)
	}
	if hooks[1].manualCalls != 0 {
		t.Errorf("tag-mismatched provider must be skipped, got %d", hooks[1].manualCalls)
	}
}

func TestHandleEpisodeFileDeletedEvent_SkipsEmptyLinkedEpisodes(t *testing.T) {
	t.Parallel()
	rows := []notifications.Notification{
		{ID: 1, Implementation: "Stub", ConfigContract: stubContract(), OnEpisodeFileDelete: true},
	}
	svc, hooks, _ := buildService(t, rows, nil)
	// No linked episodes — should short-circuit. The provider is
	// never constructed (factory.OnEpisodeFileDeleteEnabled is not
	// even called); hooks[1] stays nil. The assertion below
	// allows either case (nil-not-constructed OR constructed-but-
	// zero-calls) — the dispatcher's contract is "no hook fires
	// when LinkedEpisodes is empty".
	_ = svc.HandleEpisodeFileDeletedEvent(t.Context(), episodefiles.EpisodeFileDeletedEvent{
		EpisodeFile:    &episodefiles.File{},
		LinkedEpisodes: nil,
		Series:         &series.Series{Title: "X"},
		Reason:         episodefiles.ReasonManual,
	})
	if h, ok := hooks[1]; ok && h.episodeDelCalls != 0 {
		t.Errorf("empty linked episodes should suppress: got %d", h.episodeDelCalls)
	}
}

func TestHandleEpisodeFileDeletedEvent_FiresWithLinkedEpisodes(t *testing.T) {
	t.Parallel()
	rows := []notifications.Notification{
		{ID: 1, Implementation: "Stub", ConfigContract: stubContract(), OnEpisodeFileDelete: true},
	}
	svc, hooks, _ := buildService(t, rows, nil)
	_ = svc.HandleEpisodeFileDeletedEvent(t.Context(), episodefiles.EpisodeFileDeletedEvent{
		EpisodeFile:    &episodefiles.File{},
		LinkedEpisodes: []episodes.Episode{{SeasonNumber: 1, EpisodeNumber: 1}},
		Series:         &series.Series{Title: "X"},
		Reason:         episodefiles.ReasonManual,
	})
	if hooks[1].episodeDelCalls != 1 {
		t.Errorf("OnEpisodeFileDelete: got %d want 1", hooks[1].episodeDelCalls)
	}
}

// boolPtr returns a *bool pointing at v. Used by the OnUpgrade gate
// tests to disambiguate between nil-OnUpgrade (treated as false) and
// explicit-false vs explicit-true.
func boolPtr(v bool) *bool { return &v }

// TestHandleEpisodeImportedEvent_FiresOnDownload — slice 4.BU (C6):
// successful import emits OnDownload to every OnDownloadEnabled
// provider gated by ShouldHandleSeries.
func TestHandleEpisodeImportedEvent_FiresOnDownload(t *testing.T) {
	t.Parallel()
	rows := []notifications.Notification{
		{ID: 1, Name: "dl-A", Implementation: "Stub", ConfigContract: stubContract(), OnDownload: true},
		{ID: 2, Name: "dl-B", Implementation: "Stub", ConfigContract: stubContract(), OnDownload: true},
		{ID: 3, Name: "skip", Implementation: "Stub", ConfigContract: stubContract(), OnGrab: true}, // OnDownload=false
	}
	svc, hooks, rec := buildService(t, rows, nil)
	ev := episodeimport.EpisodeImportedEvent{
		NewDownload: true,
		LocalEpisode: &episodeimport.LocalEpisode{
			Series:   &series.Series{Title: "Show"},
			Episodes: []episodes.Episode{{SeasonNumber: 1, EpisodeNumber: 1, Title: "Pilot"}},
			Path:     "/data/incoming/foo.mkv",
			Quality:  quality.Model{Quality: quality.SDTV, Revision: quality.NewRevision()},
		},
		EpisodeFile: &episodefiles.File{},
	}
	if err := svc.HandleEpisodeImportedEvent(t.Context(), ev); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if hooks[1].downloadCalls != 1 || hooks[2].downloadCalls != 1 {
		t.Errorf("OnDownload: A=%d B=%d, want 1 each", hooks[1].downloadCalls, hooks[2].downloadCalls)
	}
	if _, ok := hooks[3]; ok {
		t.Errorf("Skip provider should not have been constructed")
	}
	if len(rec.successes) != 2 {
		t.Errorf("recorder successes = %d, want 2", len(rec.successes))
	}
}

// TestHandleEpisodeImportedEvent_NewDownloadFalseSuppresses — slice
// 4.BU (C6): re-emissions from rescans (NewDownload=false) don't fire
// OnDownload. Mirrors C# `NotificationService.cs:165-168`.
func TestHandleEpisodeImportedEvent_NewDownloadFalseSuppresses(t *testing.T) {
	t.Parallel()
	rows := []notifications.Notification{
		{ID: 1, Implementation: "Stub", ConfigContract: stubContract(), OnDownload: true},
	}
	svc, hooks, _ := buildService(t, rows, nil)
	ev := episodeimport.EpisodeImportedEvent{
		NewDownload: false,
		LocalEpisode: &episodeimport.LocalEpisode{
			Series: &series.Series{Title: "X"},
		},
	}
	_ = svc.HandleEpisodeImportedEvent(t.Context(), ev)
	if h, ok := hooks[1]; ok && h.downloadCalls != 0 {
		t.Errorf("expected zero OnDownload calls with NewDownload=false, got %d", h.downloadCalls)
	}
}

// TestHandleEpisodeImportedEvent_OnUpgradeGate — slice 4.BU (C6): when
// OldFiles is non-empty (upgrade path), only providers with
// OnUpgrade=true fire. Mirrors C# `:189`.
func TestHandleEpisodeImportedEvent_OnUpgradeGate(t *testing.T) {
	t.Parallel()
	rows := []notifications.Notification{
		{ID: 1, Name: "upgrade-ok", Implementation: "Stub", ConfigContract: stubContract(), OnDownload: true, OnUpgrade: boolPtr(true)},
		{ID: 2, Name: "no-upgrade", Implementation: "Stub", ConfigContract: stubContract(), OnDownload: true, OnUpgrade: boolPtr(false)},
		{ID: 3, Name: "nil-upgrade", Implementation: "Stub", ConfigContract: stubContract(), OnDownload: true}, // nil → treated as false
	}
	svc, hooks, _ := buildService(t, rows, nil)
	ev := episodeimport.EpisodeImportedEvent{
		NewDownload: true,
		LocalEpisode: &episodeimport.LocalEpisode{
			Series:   &series.Series{Title: "Show"},
			Episodes: []episodes.Episode{{}},
		},
		// Non-empty OldFiles → upgrade path.
		OldFiles: []episodefiles.DeletedFile{{}, {}},
	}
	if err := svc.HandleEpisodeImportedEvent(t.Context(), ev); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if hooks[1].downloadCalls != 1 {
		t.Errorf("OnUpgrade=true provider should fire on upgrade path, got %d", hooks[1].downloadCalls)
	}
	if hooks[2].downloadCalls != 0 {
		t.Errorf("OnUpgrade=false provider should suppress on upgrade path, got %d", hooks[2].downloadCalls)
	}
	if hooks[3].downloadCalls != 0 {
		t.Errorf("OnUpgrade=nil provider should suppress on upgrade path (nil→false), got %d", hooks[3].downloadCalls)
	}
}

// TestHandleEpisodeImportedEvent_FiresOnFreshDownloadEvenWhenOnUpgradeFalse —
// slice 4.BU (C6): empty OldFiles → every OnDownloadEnabled fires
// regardless of OnUpgrade value (the gate only applies on upgrades).
func TestHandleEpisodeImportedEvent_FiresOnFreshDownloadEvenWhenOnUpgradeFalse(t *testing.T) {
	t.Parallel()
	rows := []notifications.Notification{
		{ID: 1, Implementation: "Stub", ConfigContract: stubContract(), OnDownload: true, OnUpgrade: boolPtr(false)},
	}
	svc, hooks, _ := buildService(t, rows, nil)
	ev := episodeimport.EpisodeImportedEvent{
		NewDownload: true,
		LocalEpisode: &episodeimport.LocalEpisode{
			Series:   &series.Series{Title: "Show"},
			Episodes: []episodes.Episode{{}},
		},
	}
	if err := svc.HandleEpisodeImportedEvent(t.Context(), ev); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if hooks[1].downloadCalls != 1 {
		t.Errorf("expected fresh-download to fire regardless of OnUpgrade, got %d", hooks[1].downloadCalls)
	}
}

func TestHandleHealthCheckCompleteEvent_DrainsProcessQueue(t *testing.T) {
	t.Parallel()
	rows := []notifications.Notification{
		{ID: 1, Implementation: "Stub", ConfigContract: stubContract(), OnHealthIssue: true},
		{ID: 2, Implementation: "Stub", ConfigContract: stubContract(), OnHealthIssue: true},
	}
	svc, hooks, _ := buildService(t, rows, nil)
	_ = svc.HandleHealthCheckCompleteEvent(t.Context(), healthcheck.CompleteEvent{})
	if hooks[1].processCalls != 1 || hooks[2].processCalls != 1 {
		t.Errorf("ProcessQueue: 1=%d 2=%d (want 1,1)", hooks[1].processCalls, hooks[2].processCalls)
	}
}

func TestHandleDownloadsProcessedEvent_DrainsProcessQueue_J520(t *testing.T) {
	t.Parallel()
	// Drains ALL configured providers regardless of OnX flags (matches
	// C# GetAvailableProviders) — neither row sets any OnX flag.
	rows := []notifications.Notification{
		{ID: 1, Implementation: "Stub", ConfigContract: stubContract()},
		{ID: 2, Implementation: "Stub", ConfigContract: stubContract()},
	}
	svc, hooks, _ := buildService(t, rows, nil)
	if err := svc.HandleDownloadsProcessedEvent(t.Context(), trackeddownloads.DownloadsProcessedEvent{}); err != nil {
		t.Fatalf("HandleDownloadsProcessedEvent: %v", err)
	}
	if hooks[1].processCalls != 1 || hooks[2].processCalls != 1 {
		t.Errorf("ProcessQueue drain: 1=%d 2=%d (want 1,1)", hooks[1].processCalls, hooks[2].processCalls)
	}
}

func TestHandleRenameCompletedEvent_DrainsProcessQueue_J521(t *testing.T) {
	t.Parallel()
	rows := []notifications.Notification{
		{ID: 1, Implementation: "Stub", ConfigContract: stubContract()},
	}
	svc, hooks, _ := buildService(t, rows, nil)
	if err := svc.HandleRenameCompletedEvent(t.Context(), episodefiles.RenameCompletedEvent{}); err != nil {
		t.Fatalf("HandleRenameCompletedEvent: %v", err)
	}
	if hooks[1].processCalls != 1 {
		t.Errorf("ProcessQueue drain = %d, want 1", hooks[1].processCalls)
	}
}

func TestHandleDeleteCompletedEvent_DrainsProcessQueue_J522(t *testing.T) {
	t.Parallel()
	rows := []notifications.Notification{
		{ID: 1, Implementation: "Stub", ConfigContract: stubContract()},
	}
	svc, hooks, _ := buildService(t, rows, nil)
	if err := svc.HandleDeleteCompletedEvent(t.Context(), episodefiles.DeleteCompletedEvent{}); err != nil {
		t.Fatalf("HandleDeleteCompletedEvent: %v", err)
	}
	if hooks[1].processCalls != 1 {
		t.Errorf("ProcessQueue drain = %d, want 1", hooks[1].processCalls)
	}
}

// TestNilRecorder verifies that nil recorder is tolerated — the
// dispatcher invokes the hook without crashing on the recorder
// call.
func TestNilRecorder(t *testing.T) {
	t.Parallel()
	rows := []notifications.Notification{
		{ID: 1, Implementation: "Stub", ConfigContract: stubContract(), OnGrab: true},
	}
	registry := notifications.NewRegistry()
	hookCount := atomic.Int32{}
	_ = registry.Register("Stub", "StubSettings", func(def *notifications.Notification, _ notifications.Dependencies) (notifications.Provider, error) {
		return &hookRecorder{id: def.ID, def: def}, nil
	})
	f, _ := notifications.NewFactory(&stubRepo{rows: rows}, registry, nil, notifications.Dependencies{}, slog.New(slog.DiscardHandler))
	svc, _ := notifications.NewService(f, nil, slog.New(slog.DiscardHandler))
	_ = svc.HandleEpisodeGrabbedEvent(t.Context(), decisionengine.EpisodeGrabbedEvent{
		Episode: &decisionengine.Subject{Series: &series.Series{Title: "X"}, Episodes: []episodes.Episode{{}}},
	})
	_ = hookCount.Load() // touch
}

// --- J456: OnImportComplete dispatch ------------------------------

func TestHandleDownloadCompletedEvent_FiresOnImportComplete(t *testing.T) {
	t.Parallel()
	rows := []notifications.Notification{
		{ID: 1, Name: "ic-A", Implementation: "Stub", ConfigContract: stubContract(), OnImportComplete: true},
		{ID: 2, Name: "skip", Implementation: "Stub", ConfigContract: stubContract(), OnGrab: true}, // OnImportComplete=false
	}
	svc, hooks, rec := buildService(t, rows, nil)
	ev := trackeddownloads.DownloadCompletedEvent{
		TrackedDownload: &trackeddownloads.TrackedDownload{
			DownloadID:     "dl1",
			DownloadClient: 5,
			DownloadItem:   &downloadclients.Item{DownloadID: "dl1", Title: "Show.S01E01"},
			RemoteEpisode: &decisionengine.Subject{
				Series:            &series.Series{Title: "Show", Path: "/tv/Show"},
				Episodes:          []episodes.Episode{{SeasonNumber: 1, EpisodeNumber: 1, Title: "Pilot"}},
				ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{Quality: quality.Model{Quality: quality.SDTV, Revision: quality.NewRevision()}, ReleaseGroup: "RLSGRP"},
			},
		},
		EpisodeFiles: []episodefiles.File{{ID: 7, RelativePath: "Season 01/ep.mkv"}},
	}
	if err := svc.HandleDownloadCompletedEvent(t.Context(), ev); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if hooks[1].importCompleteCall != 1 {
		t.Fatalf("OnImportComplete calls = %d, want 1", hooks[1].importCompleteCall)
	}
	if _, ok := hooks[2]; ok {
		t.Errorf("OnImportComplete=false provider should not be constructed")
	}
	msg := hooks[1].importCompleteMsg
	if msg.Series == nil || msg.Series.Title != "Show" {
		t.Errorf("msg.Series = %v", msg.Series)
	}
	if len(msg.EpisodeFiles) != 1 || msg.EpisodeFiles[0].ID != 7 {
		t.Errorf("msg.EpisodeFiles = %+v, want [file 7]", msg.EpisodeFiles)
	}
	if msg.ReleaseGroup != "RLSGRP" || msg.DownloadID != "dl1" {
		t.Errorf("msg ReleaseGroup=%q DownloadID=%q", msg.ReleaseGroup, msg.DownloadID)
	}
	if len(rec.successes) != 1 {
		t.Errorf("recorder successes = %d, want 1", len(rec.successes))
	}
}

func TestHandleDownloadCompletedEvent_FullSeasonMessage(t *testing.T) {
	t.Parallel()
	rows := []notifications.Notification{{ID: 1, Implementation: "Stub", ConfigContract: stubContract(), OnImportComplete: true}}
	svc, hooks, _ := buildService(t, rows, nil)
	ev := trackeddownloads.DownloadCompletedEvent{
		TrackedDownload: &trackeddownloads.TrackedDownload{
			DownloadItem: &downloadclients.Item{},
			RemoteEpisode: &decisionengine.Subject{
				Series:            &series.Series{Title: "Show"},
				Episodes:          []episodes.Episode{{SeasonNumber: 3, EpisodeNumber: 1}},
				ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{FullSeason: true, Quality: quality.Model{Quality: quality.SDTV, Revision: quality.NewRevision()}},
			},
		},
	}
	if err := svc.HandleDownloadCompletedEvent(t.Context(), ev); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if got := hooks[1].importCompleteMsg.Message; !contains(got, "Season 3") {
		t.Errorf("full-season message = %q, want it to mention 'Season 3'", got)
	}
}

func TestHandleDownloadCompletedEvent_NilGuards(t *testing.T) {
	t.Parallel()
	rows := []notifications.Notification{{ID: 1, Implementation: "Stub", ConfigContract: stubContract(), OnImportComplete: true}}
	svc, hooks, _ := buildService(t, rows, nil)
	// nil RemoteEpisode → no dispatch.
	_ = svc.HandleDownloadCompletedEvent(t.Context(), trackeddownloads.DownloadCompletedEvent{
		TrackedDownload: &trackeddownloads.TrackedDownload{DownloadItem: &downloadclients.Item{}},
	})
	if h, ok := hooks[1]; ok && h.importCompleteCall != 0 {
		t.Errorf("nil RemoteEpisode should not dispatch, got %d", h.importCompleteCall)
	}
}

func TestHandleUntrackedDownloadCompletedEvent_FiresOnImportComplete(t *testing.T) {
	t.Parallel()
	rows := []notifications.Notification{{ID: 1, Implementation: "Stub", ConfigContract: stubContract(), OnImportComplete: true}}
	svc, hooks, _ := buildService(t, rows, nil)
	ev := trackeddownloads.UntrackedDownloadCompletedEvent{
		Series:            &series.Series{Title: "Show", Path: "/tv/Show"},
		Episodes:          []episodes.Episode{{SeasonNumber: 1, EpisodeNumber: 2, Title: "Two"}},
		EpisodeFiles:      []episodefiles.File{{ID: 9, RelativePath: "Season 01/ep2.mkv"}},
		ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{ReleaseTitle: "Show.S01E02", Quality: quality.Model{Quality: quality.SDTV, Revision: quality.NewRevision()}},
		SourcePath:        "/downloads/Show.S01E02",
	}
	if err := svc.HandleUntrackedDownloadCompletedEvent(t.Context(), ev); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if hooks[1].importCompleteCall != 1 {
		t.Fatalf("OnImportComplete calls = %d, want 1", hooks[1].importCompleteCall)
	}
	msg := hooks[1].importCompleteMsg
	if msg.SourcePath != "/downloads/Show.S01E02" || msg.SourceTitle != "Show.S01E02" {
		t.Errorf("msg SourcePath=%q SourceTitle=%q", msg.SourcePath, msg.SourceTitle)
	}
	if len(msg.EpisodeFiles) != 1 || msg.EpisodeFiles[0].ID != 9 {
		t.Errorf("msg.EpisodeFiles = %+v, want [file 9]", msg.EpisodeFiles)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || (len(sub) > 0 && indexOf(s, sub) >= 0))
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
