package v3

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/metadata"
)

// fakeMetadataWriter implements MetadataWriter.
type fakeMetadataWriter struct {
	rows   []metadata.Metadata
	nextID int64
	getErr error
}

func (f *fakeMetadataWriter) Get(_ context.Context, id int) (*metadata.Metadata, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	for i := range f.rows {
		if f.rows[i].ID == id {
			cp := f.rows[i]
			return &cp, nil
		}
	}
	return nil, metadata.ErrNotFound
}
func (f *fakeMetadataWriter) All(_ context.Context) ([]metadata.Metadata, error) {
	out := make([]metadata.Metadata, len(f.rows))
	copy(out, f.rows)
	return out, nil
}
func (f *fakeMetadataWriter) Insert(_ context.Context, n *metadata.Metadata) (*metadata.Metadata, error) {
	n.ID = int(atomic.AddInt64(&f.nextID, 1))
	f.rows = append(f.rows, *n)
	cp := *n
	return &cp, nil
}
func (f *fakeMetadataWriter) Update(_ context.Context, n *metadata.Metadata) error {
	for i := range f.rows {
		if f.rows[i].ID == n.ID {
			f.rows[i] = *n
			return nil
		}
	}
	return metadata.ErrNotFound
}
func (f *fakeMetadataWriter) Delete(_ context.Context, id int) error {
	for i := range f.rows {
		if f.rows[i].ID == id {
			f.rows = append(f.rows[:i], f.rows[i+1:]...)
			return nil
		}
	}
	return metadata.ErrNotFound
}

type fakeMetadataEmitter struct {
	created []MetadataResource
	updated []MetadataResource
	deleted []MetadataResource
}

func (f *fakeMetadataEmitter) Created(_ context.Context, r MetadataResource) error {
	f.created = append(f.created, r)
	return nil
}
func (f *fakeMetadataEmitter) Updated(_ context.Context, r MetadataResource) error {
	f.updated = append(f.updated, r)
	return nil
}
func (f *fakeMetadataEmitter) Deleted(_ context.Context, r MetadataResource) error {
	f.deleted = append(f.deleted, r)
	return nil
}

func validMetadataBody() MetadataResource {
	return MetadataResource{
		Name:               "Kodi",
		Implementation:     "XbmcMetadata",
		ImplementationName: "Kodi (XBMC) / Emby",
		ConfigContract:     "XbmcMetadataSettings",
		Tags:               []int{},
		Enable:             true,
		Fields: []ProviderField{
			{Name: "seriesMetadata", Value: true},
			{Name: "episodeMetadata", Value: false},
		},
	}
}

func TestMetadataCreateHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeMetadataWriter{}
	em := &fakeMetadataEmitter{}
	body := validMetadataBody()
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	MetadataCreateHandler(w, em).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/metadata", bytes.NewReader(buf)))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if len(w.rows) != 1 || w.rows[0].Name != "Kodi" || w.rows[0].ID <= 0 {
		t.Errorf("row not inserted: %+v", w.rows)
	}
	if w.rows[0].Settings == "" || w.rows[0].Settings == "{}" {
		t.Errorf("settings should pack fields[]; got %q", w.rows[0].Settings)
	}
	var settings map[string]any
	_ = json.Unmarshal([]byte(w.rows[0].Settings), &settings)
	if settings["seriesMetadata"] != true {
		t.Errorf("settings packing: %+v", settings)
	}
	if !w.rows[0].Enable {
		t.Errorf("Enable not persisted")
	}
	if len(em.created) != 1 || em.created[0].Name != "Kodi" {
		t.Errorf("emitter.Created: %+v", em.created)
	}
}

func TestMetadataCreateHandler_NameMissing_400(t *testing.T) {
	t.Parallel()
	body := validMetadataBody()
	body.Name = ""
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	MetadataCreateHandler(&fakeMetadataWriter{}, &fakeMetadataEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/metadata", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestMetadataCreateHandler_ImplementationMissing_400(t *testing.T) {
	t.Parallel()
	body := validMetadataBody()
	body.Implementation = ""
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	MetadataCreateHandler(&fakeMetadataWriter{}, &fakeMetadataEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/metadata", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestMetadataCreateHandler_DuplicateName_400(t *testing.T) {
	t.Parallel()
	w := &fakeMetadataWriter{rows: []metadata.Metadata{{ID: 9, Name: "kodi"}}}
	body := validMetadataBody()
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	MetadataCreateHandler(w, &fakeMetadataEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/metadata", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestMetadataCreateHandler_MalformedJSON_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	MetadataCreateHandler(&fakeMetadataWriter{}, &fakeMetadataEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/metadata", bytes.NewReader([]byte(`{bad`))))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestMetadataUpdateHandler_Success(t *testing.T) {
	t.Parallel()
	existing := metadata.Metadata{ID: 42, Name: "old", Implementation: "XbmcMetadata", Settings: `{"seriesMetadata":false}`}
	w := &fakeMetadataWriter{rows: []metadata.Metadata{existing}}
	em := &fakeMetadataEmitter{}
	body := validMetadataBody()
	body.ID = 42
	body.Name = "renamed"
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/metadata/42", bytes.NewReader(buf))
	req.SetPathValue("id", "42")
	rr := httptest.NewRecorder()
	MetadataUpdateHandler(w, em).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if w.rows[0].Name != "renamed" {
		t.Errorf("row not renamed: %+v", w.rows[0])
	}
	if len(em.updated) != 1 {
		t.Errorf("emitter.Updated not fired")
	}
}

func TestMetadataUpdateHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	body := validMetadataBody()
	body.ID = 999
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/metadata/999", bytes.NewReader(buf))
	req.SetPathValue("id", "999")
	rr := httptest.NewRecorder()
	MetadataUpdateHandler(&fakeMetadataWriter{}, &fakeMetadataEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestMetadataUpdateHandler_BodyIDFallback(t *testing.T) {
	t.Parallel()
	existing := metadata.Metadata{ID: 5, Name: "old", Implementation: "XbmcMetadata"}
	w := &fakeMetadataWriter{rows: []metadata.Metadata{existing}}
	body := validMetadataBody()
	body.ID = 5
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/metadata/999", bytes.NewReader(buf))
	req.SetPathValue("id", "999")
	rr := httptest.NewRecorder()
	MetadataUpdateHandler(w, &fakeMetadataEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestMetadataUpdateHandler_OmittedFields_KeepsSettings(t *testing.T) {
	t.Parallel()
	existing := metadata.Metadata{ID: 1, Name: "old", Implementation: "XbmcMetadata", Settings: `{"seriesMetadata":true}`}
	w := &fakeMetadataWriter{rows: []metadata.Metadata{existing}}
	body := validMetadataBody()
	body.ID = 1
	body.Fields = nil
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/metadata/1", bytes.NewReader(buf))
	req.SetPathValue("id", "1")
	rr := httptest.NewRecorder()
	MetadataUpdateHandler(w, &fakeMetadataEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d", rr.Code)
	}
	if w.rows[0].Settings != `{"seriesMetadata":true}` {
		t.Errorf("settings should be preserved: %q", w.rows[0].Settings)
	}
}

func TestMetadataDeleteHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeMetadataWriter{rows: []metadata.Metadata{{ID: 7, Name: "victim"}}}
	em := &fakeMetadataEmitter{}
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/metadata/7", nil)
	req.SetPathValue("id", "7")
	rr := httptest.NewRecorder()
	MetadataDeleteHandler(w, em).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != `{}` {
		t.Errorf("body: %q", rr.Body.String())
	}
	if len(em.deleted) != 1 || em.deleted[0].ID != 7 {
		t.Errorf("emitter.Deleted: %+v", em.deleted)
	}
}

func TestMetadataDeleteHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/metadata/999", nil)
	req.SetPathValue("id", "999")
	rr := httptest.NewRecorder()
	MetadataDeleteHandler(&fakeMetadataWriter{}, &fakeMetadataEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestMetadataDeleteHandler_InvalidID_400(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"abc", "0", "-1"} {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodDelete, "/api/v3/metadata/"+raw, nil)
			req.SetPathValue("id", raw)
			rr := httptest.NewRecorder()
			MetadataDeleteHandler(&fakeMetadataWriter{}, &fakeMetadataEmitter{}).ServeHTTP(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("status: %d (id=%q)", rr.Code, raw)
			}
		})
	}
}

// Sanity check: errors.Is wiring for metadata.ErrNotFound — keeps
// the import non-trivial when no tests cover it.
var _ = errors.Is(metadata.ErrNotFound, metadata.ErrNotFound)
