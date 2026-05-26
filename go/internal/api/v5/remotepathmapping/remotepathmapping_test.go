package remotepathmapping

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	rpm "sonarr.tv/sonarr/internal/remotepathmappings"
)

// fakeWriter implements Writer (alias of apiv3.RemotePathMappingWriter).
type fakeWriter struct {
	rows    []rpm.Mapping
	updates atomic.Int64
	deletes atomic.Int64
}

func (f *fakeWriter) All(_ context.Context) ([]rpm.Mapping, error) {
	out := make([]rpm.Mapping, len(f.rows))
	copy(out, f.rows)
	return out, nil
}

func (f *fakeWriter) Get(_ context.Context, id int) (*rpm.Mapping, error) {
	for i := range f.rows {
		if f.rows[i].ID == id {
			cp := f.rows[i]
			return &cp, nil
		}
	}
	return nil, rpm.ErrNotFound
}

func (f *fakeWriter) Add(_ context.Context, m *rpm.Mapping) (*rpm.Mapping, error) {
	m.ID = len(f.rows) + 1
	f.rows = append(f.rows, *m)
	cp := *m
	return &cp, nil
}

func (f *fakeWriter) Update(_ context.Context, m *rpm.Mapping) (*rpm.Mapping, error) {
	f.updates.Add(1)
	for i := range f.rows {
		if f.rows[i].ID == m.ID {
			f.rows[i] = *m
			cp := *m
			return &cp, nil
		}
	}
	return nil, rpm.ErrNotFound
}

func (f *fakeWriter) Remove(_ context.Context, id int) error {
	f.deletes.Add(1)
	for i := range f.rows {
		if f.rows[i].ID == id {
			f.rows = append(f.rows[:i], f.rows[i+1:]...)
			return nil
		}
	}
	return rpm.ErrNotFound
}

// ----- PUT /{id} — v5 returns 200 (v3 returns 202 + body) -----

func TestUpdateHandler_Success_Returns200WithBody(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{rows: []rpm.Mapping{{ID: 1, Host: "x", RemotePath: "/r", LocalPath: "/l"}}}
	body := Resource{ID: 1, Host: "x-new", RemotePath: "/r-new", LocalPath: "/l-new"}
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/remotepathmapping/1", bytes.NewReader(buf))
	req.SetPathValue("id", "1")
	UpdateHandler(w).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200 (v5 demotes v3's 202); body=%q", rr.Code, rr.Body.String())
	}
	// Body must be preserved through the wrapper.
	var got Resource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Host != "x-new" || got.RemotePath != "/r-new" || got.LocalPath != "/l-new" {
		t.Errorf("body should be preserved verbatim; got %+v", got)
	}
	if w.updates.Load() != 1 {
		t.Errorf("svc.Update should be called once; got %d", w.updates.Load())
	}
}

// ----- DELETE /{id} — v5 returns 204 (v3 returns 200 + empty) -----

func TestDeleteHandler_Success_Returns204EmptyBody(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{rows: []rpm.Mapping{{ID: 7, Host: "x"}}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/remotepathmapping/7", nil)
	req.SetPathValue("id", "7")
	DeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204; body=%q", rr.Code, rr.Body.String())
	}
	if rr.Body.Len() != 0 {
		t.Errorf("body should be empty for 204; got %q", rr.Body.String())
	}
	if w.deletes.Load() != 1 {
		t.Errorf("svc.Remove should be called once; got %d", w.deletes.Load())
	}
}

func TestDeleteHandler_MissingID_Returns204Idempotent(t *testing.T) {
	t.Parallel()
	w := &fakeWriter{} // empty
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v5/remotepathmapping/999", nil)
	req.SetPathValue("id", "999")
	DeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204 (idempotent missing-id)", rr.Code)
	}
}

// ----- alias guards -----

func TestVariableAliases_NonNil(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		got  any
	}{
		{"ListHandler", ListHandler},
		{"ByIDHandler", ByIDHandler},
		{"CreateHandler", CreateHandler},
	}
	for _, tc := range cases {
		if tc.got == nil {
			t.Errorf("%s alias is nil — v3 re-export broken", tc.name)
		}
	}
}

func TestTypeAliasesMatchV3(t *testing.T) {
	t.Parallel()
	var _ Resource = apiv3.RemotePathMappingResource{}
	var _ apiv3.RemotePathMappingResource = Resource{}
}
