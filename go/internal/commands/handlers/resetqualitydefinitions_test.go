package handlers_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/commands/handlers"
)

type recordingQualityDefinitionResetter struct {
	calls          int
	gotResetTitles bool
	err            error
}

func (r *recordingQualityDefinitionResetter) ResetTitles(_ context.Context, resetTitles bool) error {
	r.calls++
	r.gotResetTitles = resetTitles
	return r.err
}

func TestResetQualityDefinitions_NilResetter(t *testing.T) {
	t.Parallel()
	if _, err := handlers.NewResetQualityDefinitions(nil, nil); err == nil {
		t.Fatal("expected error from nil resetter")
	}
}

// Empty body → resetTitles defaults to false.
func TestResetQualityDefinitions_EmptyBody_DefaultsFalse(t *testing.T) {
	t.Parallel()
	r := &recordingQualityDefinitionResetter{}
	h, _ := handlers.NewResetQualityDefinitions(r, slog.Default())
	if err := h.Execute(t.Context(), ""); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.calls != 1 || r.gotResetTitles {
		t.Errorf("calls=%d resetTitles=%v, want 1/false", r.calls, r.gotResetTitles)
	}
}

// resetTitles:true in the body is parsed and forwarded.
func TestResetQualityDefinitions_ParsesResetTitles(t *testing.T) {
	t.Parallel()
	r := &recordingQualityDefinitionResetter{}
	h, _ := handlers.NewResetQualityDefinitions(r, slog.Default())
	if err := h.Execute(t.Context(), `{"resetTitles":true}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.calls != 1 || !r.gotResetTitles {
		t.Errorf("calls=%d resetTitles=%v, want 1/true", r.calls, r.gotResetTitles)
	}
}

// Malformed body falls back to the default (false) and still runs.
func TestResetQualityDefinitions_MalformedBody_UsesDefault(t *testing.T) {
	t.Parallel()
	r := &recordingQualityDefinitionResetter{}
	h, _ := handlers.NewResetQualityDefinitions(r, slog.Default())
	if err := h.Execute(t.Context(), `{not json`); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.calls != 1 || r.gotResetTitles {
		t.Errorf("calls=%d resetTitles=%v, want 1/false", r.calls, r.gotResetTitles)
	}
}

func TestResetQualityDefinitions_PropagatesError(t *testing.T) {
	t.Parallel()
	r := &recordingQualityDefinitionResetter{err: errors.New("boom")}
	h, _ := handlers.NewResetQualityDefinitions(r, slog.Default())
	if err := h.Execute(t.Context(), ""); err == nil {
		t.Fatal("expected error to propagate")
	}
}
