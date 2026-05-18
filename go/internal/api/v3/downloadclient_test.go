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

	"sonarr.tv/sonarr/internal/downloadclients"
)

type fakeDownloadClientLister struct {
	rows []downloadclients.DownloadClient
	err  error
}

func (f *fakeDownloadClientLister) All(context.Context) ([]downloadclients.DownloadClient, error) {
	return f.rows, f.err
}

func TestDownloadClientHandler_Empty(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	DownloadClientHandler(&fakeDownloadClientLister{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/downloadclient", nil))
	if got := rr.Body.String(); got != "[]" {
		t.Errorf("empty body: got %q want []", got)
	}
}

func TestDownloadClientHandler_Error(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	DownloadClientHandler(&fakeDownloadClientLister{err: errors.New("boom")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/downloadclient", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}

// fakeDownloadClientWriter implements DownloadClientWriter.
type fakeDownloadClientWriter struct {
	rows   []downloadclients.DownloadClient
	nextID int64
	getErr error
}

func (f *fakeDownloadClientWriter) Get(_ context.Context, id int) (*downloadclients.DownloadClient, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	for i := range f.rows {
		if f.rows[i].ID == id {
			cp := f.rows[i]
			return &cp, nil
		}
	}
	return nil, downloadclients.ErrNotFound
}
func (f *fakeDownloadClientWriter) All(_ context.Context) ([]downloadclients.DownloadClient, error) {
	out := make([]downloadclients.DownloadClient, len(f.rows))
	copy(out, f.rows)
	return out, nil
}
func (f *fakeDownloadClientWriter) Insert(_ context.Context, n *downloadclients.DownloadClient) (*downloadclients.DownloadClient, error) {
	n.ID = int(atomic.AddInt64(&f.nextID, 1))
	f.rows = append(f.rows, *n)
	cp := *n
	return &cp, nil
}
func (f *fakeDownloadClientWriter) Update(_ context.Context, n *downloadclients.DownloadClient) error {
	for i := range f.rows {
		if f.rows[i].ID == n.ID {
			f.rows[i] = *n
			return nil
		}
	}
	return downloadclients.ErrNotFound
}
func (f *fakeDownloadClientWriter) Delete(_ context.Context, id int) error {
	for i := range f.rows {
		if f.rows[i].ID == id {
			f.rows = append(f.rows[:i], f.rows[i+1:]...)
			return nil
		}
	}
	return downloadclients.ErrNotFound
}

type fakeDownloadClientEmitter struct {
	created []DownloadClientResource
	updated []DownloadClientResource
	deleted []DownloadClientResource
}

func (f *fakeDownloadClientEmitter) Created(_ context.Context, r DownloadClientResource) error {
	f.created = append(f.created, r)
	return nil
}
func (f *fakeDownloadClientEmitter) Updated(_ context.Context, r DownloadClientResource) error {
	f.updated = append(f.updated, r)
	return nil
}
func (f *fakeDownloadClientEmitter) Deleted(_ context.Context, r DownloadClientResource) error {
	f.deleted = append(f.deleted, r)
	return nil
}

func validDownloadClientBody() DownloadClientResource {
	return DownloadClientResource{
		providerResourceCore: providerResourceCore{
			Name:               "MySab",
			Implementation:     "Sabnzbd",
			ImplementationName: "Sabnzbd",
			ConfigContract:     "SabnzbdSettings",
			Tags:               []int{},
		},
		Enable:                   true,
		Priority:                 25,
		RemoveCompletedDownloads: true,
		Fields: []ProviderField{
			{Name: "host", Value: "sab.example"},
			{Name: "apiKey", Value: "deadbeef"},
		},
	}
}

func TestDownloadClientCreateHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeDownloadClientWriter{}
	em := &fakeDownloadClientEmitter{}
	body := validDownloadClientBody()
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	DownloadClientCreateHandler(w, em).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/downloadclient", bytes.NewReader(buf)))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if len(w.rows) != 1 || w.rows[0].Name != "MySab" || w.rows[0].ID <= 0 {
		t.Errorf("row not inserted: %+v", w.rows)
	}
	if w.rows[0].Settings == "" || w.rows[0].Settings == "{}" {
		t.Errorf("settings should pack fields[]; got %q", w.rows[0].Settings)
	}
	var settings map[string]any
	_ = json.Unmarshal([]byte(w.rows[0].Settings), &settings)
	if settings["host"] != "sab.example" {
		t.Errorf("settings packing: %+v", settings)
	}
	if len(em.created) != 1 {
		t.Errorf("emitter.Created not fired: %+v", em.created)
	}
}

func TestDownloadClientCreateHandler_NameMissing_400(t *testing.T) {
	t.Parallel()
	body := validDownloadClientBody()
	body.Name = ""
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	DownloadClientCreateHandler(&fakeDownloadClientWriter{}, &fakeDownloadClientEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/downloadclient", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestDownloadClientCreateHandler_DuplicateName_400(t *testing.T) {
	t.Parallel()
	w := &fakeDownloadClientWriter{rows: []downloadclients.DownloadClient{{ID: 9, Name: "mysab"}}}
	body := validDownloadClientBody()
	buf, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	DownloadClientCreateHandler(w, &fakeDownloadClientEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/downloadclient", bytes.NewReader(buf)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestDownloadClientCreateHandler_MalformedJSON_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	DownloadClientCreateHandler(&fakeDownloadClientWriter{}, &fakeDownloadClientEmitter{}).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v3/downloadclient", bytes.NewReader([]byte(`{bad`))))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestDownloadClientUpdateHandler_Success(t *testing.T) {
	t.Parallel()
	existing := downloadclients.DownloadClient{ID: 42, Name: "old", Implementation: "Sabnzbd", Settings: `{"host":"old"}`}
	w := &fakeDownloadClientWriter{rows: []downloadclients.DownloadClient{existing}}
	em := &fakeDownloadClientEmitter{}
	body := validDownloadClientBody()
	body.ID = 42
	body.Name = "renamed"
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/downloadclient/42", bytes.NewReader(buf))
	req.SetPathValue("id", "42")
	rr := httptest.NewRecorder()
	DownloadClientUpdateHandler(w, em).ServeHTTP(rr, req)
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

func TestDownloadClientUpdateHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	body := validDownloadClientBody()
	body.ID = 999
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/downloadclient/999", bytes.NewReader(buf))
	req.SetPathValue("id", "999")
	rr := httptest.NewRecorder()
	DownloadClientUpdateHandler(&fakeDownloadClientWriter{}, &fakeDownloadClientEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestDownloadClientUpdateHandler_BodyIDFallback(t *testing.T) {
	t.Parallel()
	existing := downloadclients.DownloadClient{ID: 5, Name: "old", Implementation: "Sabnzbd"}
	w := &fakeDownloadClientWriter{rows: []downloadclients.DownloadClient{existing}}
	body := validDownloadClientBody()
	body.ID = 5
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/downloadclient/999", bytes.NewReader(buf))
	req.SetPathValue("id", "999")
	rr := httptest.NewRecorder()
	DownloadClientUpdateHandler(w, &fakeDownloadClientEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestDownloadClientUpdateHandler_OmittedFields_KeepsSettings(t *testing.T) {
	t.Parallel()
	existing := downloadclients.DownloadClient{ID: 1, Name: "old", Implementation: "Sabnzbd", Settings: `{"host":"keep"}`}
	w := &fakeDownloadClientWriter{rows: []downloadclients.DownloadClient{existing}}
	body := validDownloadClientBody()
	body.ID = 1
	body.Fields = nil
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v3/downloadclient/1", bytes.NewReader(buf))
	req.SetPathValue("id", "1")
	rr := httptest.NewRecorder()
	DownloadClientUpdateHandler(w, &fakeDownloadClientEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d", rr.Code)
	}
	if w.rows[0].Settings != `{"host":"keep"}` {
		t.Errorf("settings should be preserved: %q", w.rows[0].Settings)
	}
}

func TestDownloadClientDeleteHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeDownloadClientWriter{rows: []downloadclients.DownloadClient{{ID: 7, Name: "victim"}}}
	em := &fakeDownloadClientEmitter{}
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/downloadclient/7", nil)
	req.SetPathValue("id", "7")
	rr := httptest.NewRecorder()
	DownloadClientDeleteHandler(w, em).ServeHTTP(rr, req)
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

func TestDownloadClientDeleteHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/downloadclient/999", nil)
	req.SetPathValue("id", "999")
	rr := httptest.NewRecorder()
	DownloadClientDeleteHandler(&fakeDownloadClientWriter{}, &fakeDownloadClientEmitter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestDownloadClientDeleteHandler_InvalidID_400(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"abc", "0", "-1"} {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodDelete, "/api/v3/downloadclient/"+raw, nil)
			req.SetPathValue("id", raw)
			rr := httptest.NewRecorder()
			DownloadClientDeleteHandler(&fakeDownloadClientWriter{}, &fakeDownloadClientEmitter{}).ServeHTTP(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("status: %d (id=%q)", rr.Code, raw)
			}
		})
	}
}
