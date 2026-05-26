package command

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/commands"
)

// fakeService implements Service.
type fakeService struct {
	rows    []commands.Command
	cancels atomic.Int64
	cancelErr error
}

func (f *fakeService) Push(_ context.Context, _, _ string, _ commands.Priority, _ commands.Trigger) (*commands.Command, error) {
	return nil, nil
}

func (f *fakeService) Get(_ context.Context, id int) (*commands.Command, error) {
	for i := range f.rows {
		if f.rows[i].ID == id {
			cp := f.rows[i]
			return &cp, nil
		}
	}
	return nil, commands.ErrNotFound
}

func (f *fakeService) All(_ context.Context) ([]commands.Command, error) {
	out := make([]commands.Command, len(f.rows))
	copy(out, f.rows)
	return out, nil
}

func (f *fakeService) Cancel(_ context.Context, _ int) error {
	f.cancels.Add(1)
	return f.cancelErr
}

// ----- DELETE /{id} — v5 returns 204 (v3 returns 200 + `{}`) -----

func TestDeleteHandler_Rewrites200To204(t *testing.T) {
	t.Parallel()
	svc := &fakeService{rows: []commands.Command{{ID: 42, Name: "TestCommand"}}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/command/42", nil)
	req.SetPathValue("id", "42")
	DeleteHandler(svc).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204; body=%q", rr.Code, rr.Body.String())
	}
	if rr.Body.Len() != 0 {
		t.Errorf("body should be empty for 204; got %q", rr.Body.String())
	}
	if svc.cancels.Load() != 1 {
		t.Errorf("svc.Cancel should be called once; got %d", svc.cancels.Load())
	}
}

func TestDeleteHandler_NotFound_PassesThrough(t *testing.T) {
	t.Parallel()
	// v3 CommandDeleteHandler returns 404 on ErrNotFound (per
	// command.go:233-236); wrapper's 200→204 predicate only matches
	// 200 so 404 passes through unchanged.
	svc := &fakeService{cancelErr: commands.ErrNotFound}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/command/9999", nil)
	req.SetPathValue("id", "9999")
	DeleteHandler(svc).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404 (passthrough); body=%q", rr.Code, rr.Body.String())
	}
}

// ----- alias guards -----

func TestVariableAliases_NonNil(t *testing.T) {
	t.Parallel()
	if ListHandler == nil || ByIDHandler == nil || CreateHandler == nil {
		t.Error("list/byid/create handler aliases must be non-nil")
	}
}

func TestTypeAliasesMatchV3(t *testing.T) {
	t.Parallel()
	var _ Resource = apiv3.CommandResource{}
	var _ apiv3.CommandResource = Resource{}
	var _ Service = (apiv3.CommandService)(nil)
	var _ NameResolver = (apiv3.CommandNameResolver)(nil)
}
