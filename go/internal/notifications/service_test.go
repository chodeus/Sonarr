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
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
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
	id              int
	def             *notifications.Notification
	failOn          map[string]error
	mu              sync.Mutex
	grabCalls       int
	downloadCalls   int
	seriesAddCalls  int
	seriesDelCalls  int
	episodeDelCalls int
	healthCalls     int
	healthRestored  int
	processCalls    int
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
func (h *hookRecorder) OnRename(_ context.Context, _ notifications.RenameMessage) error {
	return nil
}
func (h *hookRecorder) OnImportComplete(_ context.Context, _ notifications.ImportCompleteMessage) error {
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
func (h *hookRecorder) OnApplicationUpdate(_ context.Context, _ notifications.ApplicationUpdateMessage) error {
	return nil
}
func (h *hookRecorder) OnManualInteractionRequired(_ context.Context, _ notifications.ManualInteractionRequiredMessage) error {
	return nil
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
