package handlers_test

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/commands"
	"sonarr.tv/sonarr/internal/commands/handlers"
	"sonarr.tv/sonarr/internal/healthcheck"
)

var checkHealthFixedTime = time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)

// trackingCheck is the test fixture for verifying that
// CheckHealth.Execute reaches a registered Check via the orchestrator.
// Records the most-recent ctx, lets us flip whether it participates
// in the schedule batch, and counts invocations atomically.
type trackingCheck struct {
	name     string
	startup  bool
	schedule bool
	calls    atomic.Int64
}

func (c *trackingCheck) Name() string          { return c.name }
func (c *trackingCheck) CheckOnStartup() bool  { return c.startup }
func (c *trackingCheck) CheckOnSchedule() bool { return c.schedule }
func (c *trackingCheck) Run(_ context.Context) healthcheck.HealthCheck {
	c.calls.Add(1)
	return healthcheck.OK(c.name)
}

func newHealthSvcWithChecks(t *testing.T, checks []healthcheck.Check) *healthcheck.Service {
	t.Helper()
	svc, err := healthcheck.NewService(checks, nil, slog.New(slog.DiscardHandler),
		healthcheck.WithDebounce(0))
	if err != nil {
		t.Fatalf("healthcheck.NewService: %v", err)
	}
	return svc
}

// TestCheckHealth_NewWithNilService_Errors pins the constructor's
// nil-guard. The handler is meaningless without a service, and a
// panic at first dispatch would surface as a goroutine crash.
func TestCheckHealth_NewWithNilService_Errors(t *testing.T) {
	t.Parallel()
	if _, err := handlers.NewCheckHealth(nil); err == nil {
		t.Errorf("NewCheckHealth(nil): expected error, got nil")
	}
}

// TestCheckHealth_DefaultBodyRunsScheduledChecks pins the default
// trigger interpretation: empty / "{}" body → TriggerScheduled.
// Mirrors the C# `:139` `Trigger == Manual ? all : scheduled`
// branch with Unspecified falling onto scheduled.
func TestCheckHealth_DefaultBodyRunsScheduledChecks(t *testing.T) {
	t.Parallel()
	scheduledOnly := &trackingCheck{name: "S", schedule: true}
	startupOnly := &trackingCheck{name: "U", startup: true}
	svc := newHealthSvcWithChecks(t, []healthcheck.Check{scheduledOnly, startupOnly})
	h, err := handlers.NewCheckHealth(svc)
	if err != nil {
		t.Fatalf("NewCheckHealth: %v", err)
	}
	if err := h.Execute(t.Context(), "{}"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if scheduledOnly.calls.Load() != 1 {
		t.Errorf("scheduled call count = %d, want 1", scheduledOnly.calls.Load())
	}
	if startupOnly.calls.Load() != 0 {
		t.Errorf("startup-only call count = %d, want 0 (default trigger is Scheduled)", startupOnly.calls.Load())
	}
}

// TestCheckHealth_ManualBodyRunsAllChecks pins the explicit
// {"trigger":"manual"} form — runs every registered check.
func TestCheckHealth_ManualBodyRunsAllChecks(t *testing.T) {
	t.Parallel()
	scheduledOnly := &trackingCheck{name: "S", schedule: true}
	startupOnly := &trackingCheck{name: "U", startup: true}
	svc := newHealthSvcWithChecks(t, []healthcheck.Check{scheduledOnly, startupOnly})
	h, err := handlers.NewCheckHealth(svc)
	if err != nil {
		t.Fatalf("NewCheckHealth: %v", err)
	}
	if err := h.Execute(t.Context(), `{"trigger":"manual"}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if scheduledOnly.calls.Load() != 1 {
		t.Errorf("scheduled call count = %d, want 1", scheduledOnly.calls.Load())
	}
	if startupOnly.calls.Load() != 1 {
		t.Errorf("startup-only call count = %d, want 1 (manual trigger runs all)", startupOnly.calls.Load())
	}
}

// TestCheckHealth_GarbageBodyDefaultsToScheduled — defensive default
// against bad input. Mirrors C# CommandTrigger.Unspecified →
// scheduled-only.
func TestCheckHealth_GarbageBodyDefaultsToScheduled(t *testing.T) {
	t.Parallel()
	scheduledOnly := &trackingCheck{name: "S", schedule: true}
	startupOnly := &trackingCheck{name: "U", startup: true}
	svc := newHealthSvcWithChecks(t, []healthcheck.Check{scheduledOnly, startupOnly})
	h, _ := handlers.NewCheckHealth(svc)
	if err := h.Execute(t.Context(), "not json"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if scheduledOnly.calls.Load() != 1 {
		t.Errorf("scheduled call count = %d, want 1", scheduledOnly.calls.Load())
	}
	if startupOnly.calls.Load() != 0 {
		t.Errorf("startup-only call count = %d, want 0", startupOnly.calls.Load())
	}
}

// TestCheckHealth_RegisteredInRegistry verifies RegisterAll wires the
// handler under the canonical Commands.Name. Catches a regression in
// the registry-key convention (CheckHealth, not CheckHealthCommand).
func TestCheckHealth_RegisteredInRegistry(t *testing.T) {
	t.Parallel()
	registry := commands.NewRegistry()
	cmdSvc := newCleanupHarness(t, checkHealthFixedTime).svc
	deps := handlers.Deps{
		Commands:     cmdSvc,
		Housekeeping: emptyHouseSvc(t),
		HealthCheck:  emptyHealthSvc(t),
		RescanSeries: noopRescanScanner{},
		Log:          slog.Default(),
	}
	if err := handlers.RegisterAll(registry, deps); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	if _, ok := registry.Lookup(handlers.CheckHealthName); !ok {
		t.Errorf("CheckHealth not registered under name %q", handlers.CheckHealthName)
	}
}

// TestCheckHealth_DefaultScheduledTasks_Has6HourInterval pins the
// scheduled-task interval against TaskManager.cs:96-99 (6h).
func TestCheckHealth_DefaultScheduledTasks_Has6HourInterval(t *testing.T) {
	t.Parallel()
	tasks := handlers.DefaultScheduledTasks()
	for _, task := range tasks {
		if task.TypeName == handlers.CheckHealthName {
			if task.Interval != 6*60 {
				t.Errorf("CheckHealth Interval: %d minutes, want %d (6h)", task.Interval, 6*60)
			}
			return
		}
	}
	t.Errorf("CheckHealth not in DefaultScheduledTasks")
}
