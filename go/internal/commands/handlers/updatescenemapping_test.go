package handlers_test

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/commands/handlers"
)

// stubSceneMappingUpdater records calls + returns the configured err.
type stubSceneMappingUpdater struct {
	calls atomic.Int32
	err   error
}

func (s *stubSceneMappingUpdater) UpdateMappings(_ context.Context) error {
	s.calls.Add(1)
	return s.err
}

// TestNewUpdateSceneMapping_RejectsNil covers the constructor guard.
func TestNewUpdateSceneMapping_RejectsNil(t *testing.T) {
	if _, err := handlers.NewUpdateSceneMapping(nil, slog.Default()); err == nil {
		t.Errorf("expected error on nil SceneMappingUpdater")
	}
}

// TestUpdateSceneMapping_Execute_HappyPath verifies the handler
// delegates exactly once and returns nil when the updater succeeds.
func TestUpdateSceneMapping_Execute_HappyPath(t *testing.T) {
	updater := &stubSceneMappingUpdater{}
	h, err := handlers.NewUpdateSceneMapping(updater, slog.Default())
	if err != nil {
		t.Fatalf("NewUpdateSceneMapping: %v", err)
	}
	if err := h.Execute(context.Background(), ""); err != nil {
		t.Errorf("Execute err = %v, want nil", err)
	}
	if got := updater.calls.Load(); got != 1 {
		t.Errorf("updater.calls = %d, want 1", got)
	}
}

// TestUpdateSceneMapping_Execute_PropagatesError verifies updater
// errors flow back to the dispatcher so the command persists as
// failed.
func TestUpdateSceneMapping_Execute_PropagatesError(t *testing.T) {
	want := errors.New("cloud unavailable")
	updater := &stubSceneMappingUpdater{err: want}
	h, err := handlers.NewUpdateSceneMapping(updater, slog.Default())
	if err != nil {
		t.Fatalf("NewUpdateSceneMapping: %v", err)
	}
	got := h.Execute(context.Background(), "")
	if !errors.Is(got, want) {
		t.Errorf("Execute err = %v, want %v", got, want)
	}
}

// TestUpdateSceneMapping_Execute_IgnoresBody covers the unused-body
// contract — C# `UpdateSceneMappingCommand` has no payload.
func TestUpdateSceneMapping_Execute_IgnoresBody(t *testing.T) {
	updater := &stubSceneMappingUpdater{}
	h, err := handlers.NewUpdateSceneMapping(updater, slog.Default())
	if err != nil {
		t.Fatalf("NewUpdateSceneMapping: %v", err)
	}
	if err := h.Execute(context.Background(), `{"some":"garbage"}`); err != nil {
		t.Errorf("Execute should ignore body content, got err = %v", err)
	}
	if got := updater.calls.Load(); got != 1 {
		t.Errorf("updater still called once regardless of body, got %d", got)
	}
}
