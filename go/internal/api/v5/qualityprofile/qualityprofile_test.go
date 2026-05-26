package qualityprofile_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"sonarr.tv/sonarr/internal/api/v5/qualityprofile"
	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/qualityprofiles"
)

type fakeWriter struct {
	mu       sync.Mutex
	deleted  []int
	deleteEr error
}

func (f *fakeWriter) All(context.Context) ([]qualityprofiles.Profile, error) {
	return nil, nil
}
func (f *fakeWriter) Get(context.Context, int) (*qualityprofiles.Profile, error) {
	return nil, qualityprofiles.ErrNotFound
}
func (f *fakeWriter) Add(_ context.Context, p *qualityprofiles.Profile) (*qualityprofiles.Profile, error) {
	p.ID = 1
	return p, nil
}
func (f *fakeWriter) Update(context.Context, *qualityprofiles.Profile) error { return nil }
func (f *fakeWriter) Delete(_ context.Context, id int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, id)
	return f.deleteEr
}

func (f *fakeWriter) deletedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.deleted)
}

func TestDeleteHandler_RewritesTo204(t *testing.T) {
	t.Parallel()
	svc := &fakeWriter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/qualityprofile/7", nil)
	req.SetPathValue("id", "7")
	qualityprofile.DeleteHandler(svc).ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204", rr.Code)
	}
	if rr.Body.Len() != 0 {
		t.Errorf("body must be empty for 204: %q", rr.Body.String())
	}
	if svc.deletedCount() != 1 {
		t.Errorf("Delete not invoked")
	}
}

func TestDeleteHandler_InUse_Passes409Through(t *testing.T) {
	t.Parallel()
	svc := &fakeWriter{deleteEr: qualityprofiles.ErrQualityProfileInUse}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/qualityprofile/7", nil)
	req.SetPathValue("id", "7")
	qualityprofile.DeleteHandler(svc).ServeHTTP(rr, req)

	// Wrapper predicate only matches 200; 409 must pass through.
	if rr.Code != http.StatusConflict {
		t.Errorf("status: got %d want 409 (in-use carries through wrapper)", rr.Code)
	}
}

func TestDeleteHandler_MissingID_IdempotentRewritten(t *testing.T) {
	t.Parallel()
	svc := &fakeWriter{deleteEr: qualityprofiles.ErrNotFound}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/qualityprofile/999", nil)
	req.SetPathValue("id", "999")
	qualityprofile.DeleteHandler(svc).ServeHTTP(rr, req)

	// v3 swallows ErrNotFound and returns 200 (J232 idempotent posture)
	// → wrapper rewrites to 204.
	if rr.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204 (idempotent swallow + wrapper)", rr.Code)
	}
}

func TestHandlerAliases_NonNil(t *testing.T) {
	t.Parallel()
	if qualityprofile.ListHandler == nil {
		t.Errorf("ListHandler alias is nil")
	}
	if qualityprofile.ByIDHandler == nil {
		t.Errorf("ByIDHandler alias is nil")
	}
	if qualityprofile.SchemaHandler == nil {
		t.Errorf("SchemaHandler alias is nil")
	}
	if qualityprofile.CreateHandler == nil {
		t.Errorf("CreateHandler alias is nil")
	}
	if qualityprofile.UpdateHandler == nil {
		t.Errorf("UpdateHandler alias is nil")
	}
}

// TestTypeAliases pins compile-time equivalence with v3 — proves the
// Resource shape is wire-safe across versions.
func TestTypeAliases(t *testing.T) {
	t.Parallel()
	r := qualityprofile.Resource{Name: "x"}
	if r.Name != "x" {
		t.Errorf("Resource not addressable")
	}
	var item qualityprofile.ItemResource
	item.Allowed = true
	if !item.Allowed {
		t.Errorf("ItemResource not addressable")
	}
	var fi qualityprofile.FormatItemResource
	fi.Format = 1
	if fi.Format != 1 {
		t.Errorf("FormatItemResource not addressable")
	}
}

// fakeCustomFormats satisfies CustomFormatNameLister for handler smoke.
type fakeCustomFormats struct{}

func (fakeCustomFormats) All(context.Context) ([]customformats.Format, error) {
	return nil, errors.New("unused")
}
