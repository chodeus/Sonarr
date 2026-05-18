package v3

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/qualitydefinitions"
	"sonarr.tv/sonarr/internal/qualityprofiles"
)

func TestQualitySourceName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   quality.QualitySource
		want string
	}{
		{quality.SourceUnknown, "unknown"},
		{quality.SourceTelevision, "television"},
		{quality.SourceTelevisionRaw, "televisionRaw"},
		{quality.SourceWeb, "web"},
		{quality.SourceWebRip, "webRip"},
		{quality.SourceDVD, "dvd"},
		{quality.SourceBluray, "bluray"},
		{quality.SourceBlurayRaw, "blurayRaw"},
		{quality.QualitySource(99), "unknown"}, // unknown enum → fallback
	}
	for _, tc := range cases {
		if got := QualitySourceName(tc.in); got != tc.want {
			t.Errorf("QualitySourceName(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// fakeLister returns canned rows or an error. Used in handler tests.
type fakeLister struct {
	rows []qualitydefinitions.Decorated
	err  error
}

func (f *fakeLister) All(context.Context) ([]qualitydefinitions.Decorated, error) {
	return f.rows, f.err
}

func TestQualityDefinitionHandler_OK(t *testing.T) {
	t.Parallel()
	lister := &fakeLister{rows: []qualitydefinitions.Decorated{
		{ID: 1, Quality: quality.Unknown, Title: "Unknown", Weight: 1, GroupName: ""},
		{ID: 2, Quality: quality.SDTV, Title: "SDTV", Weight: 2, GroupName: ""},
	}}
	rr := httptest.NewRecorder()
	QualityDefinitionHandler(lister).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/qualitydefinition", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type: got %q", ct)
	}

	var got []QualityDefinitionResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(got), got)
	}
	if got[0].Quality.Source != "unknown" {
		t.Errorf("first entry source: got %q want unknown", got[0].Quality.Source)
	}
	if got[1].Quality.Source != "television" {
		t.Errorf("second entry source: got %q want television", got[1].Quality.Source)
	}
	// GroupName must not be emitted on the wire — the resource lacks
	// the field, so a successful round-trip into QualityDefinitionResource
	// already proves this. Belt-and-braces: confirm the raw body has
	// no "groupName" key.
	if strings.Contains(rr.Body.String(), "groupName") {
		t.Errorf("body unexpectedly contains groupName key: %s", rr.Body.String())
	}
}

func TestQualityDefinitionHandler_Error(t *testing.T) {
	t.Parallel()
	lister := &fakeLister{err: errors.New("db down")}
	rr := httptest.NewRecorder()
	QualityDefinitionHandler(lister).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/qualitydefinition", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}

func TestQualityDefinitionHandler_OmitsNilSizeFields(t *testing.T) {
	t.Parallel()
	lister := &fakeLister{rows: []qualitydefinitions.Decorated{
		{ID: 1, Quality: quality.Unknown, Title: "Unknown", Weight: 1},
	}}
	rr := httptest.NewRecorder()
	QualityDefinitionHandler(lister).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/qualitydefinition", nil))
	body := rr.Body.String()
	for _, key := range []string{"minSize", "maxSize", "preferredSize"} {
		if strings.Contains(body, key) {
			t.Errorf("body unexpectedly contains %q: %s", key, body)
		}
	}
}

// ---------- 5.B7: PUT + bulk + /limits tests ----------

type fakeQualityDefinitionWriter struct {
	row    *qualitydefinitions.Decorated
	rows   []qualitydefinitions.Decorated
	getErr error
	upErr  error

	updateCalledWith []qualitydefinitions.Definition
}

func (f *fakeQualityDefinitionWriter) All(_ context.Context) ([]qualitydefinitions.Decorated, error) {
	return f.rows, nil
}

func (f *fakeQualityDefinitionWriter) GetByID(_ context.Context, id int) (*qualitydefinitions.Decorated, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.row == nil {
		return nil, qualitydefinitions.ErrNotFound
	}
	c := *f.row
	c.ID = id
	return &c, nil
}

func (f *fakeQualityDefinitionWriter) Update(_ context.Context, d *qualitydefinitions.Definition) error {
	f.updateCalledWith = append(f.updateCalledWith, *d)
	return f.upErr
}

type recordingSizeLimitUpdater struct {
	calledWith []qualityprofiles.QualityProfileSizeLimit
	err        error
}

func (r *recordingSizeLimitUpdater) UpdateAllSizeLimits(_ context.Context, sl ...qualityprofiles.QualityProfileSizeLimit) error {
	r.calledWith = append(r.calledWith, sl...)
	return r.err
}

// ----- GET /qualitydefinition/{id} -----

func TestQualityDefinitionByIDHandler_Hit(t *testing.T) {
	t.Parallel()
	w := &fakeQualityDefinitionWriter{row: &qualitydefinitions.Decorated{Title: "SDTV", Quality: quality.SDTV}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/qualitydefinition/1", nil)
	req.SetPathValue("id", "1")
	QualityDefinitionByIDHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"id":1`) || !strings.Contains(rr.Body.String(), `"title":"SDTV"`) {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestQualityDefinitionByIDHandler_NotFound(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/qualitydefinition/99", nil)
	req.SetPathValue("id", "99")
	QualityDefinitionByIDHandler(&fakeQualityDefinitionWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

// ----- GET /qualitydefinition/limits -----

func TestQualityDefinitionLimitsHandler_Constants(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/qualitydefinition/limits", nil)
	QualityDefinitionLimitsHandler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != `{"min":0,"max":1000}` {
		t.Errorf("body: got %q want %q", rr.Body.String(), `{"min":0,"max":1000}`)
	}
}

// ----- PUT /qualitydefinition/{id} -----

func TestQualityDefinitionUpdateHandler_TitleOnly(t *testing.T) {
	t.Parallel()
	w := &fakeQualityDefinitionWriter{row: &qualitydefinitions.Decorated{Title: "Renamed", Quality: quality.SDTV}}
	updater := &recordingSizeLimitUpdater{}
	rr := httptest.NewRecorder()
	body := `{"id":1,"title":"Renamed","weight":1,"quality":{"id":1,"name":"SDTV","source":"television","resolution":480}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/qualitydefinition/1", strings.NewReader(body))
	req.SetPathValue("id", "1")
	QualityDefinitionUpdateHandler(w, updater).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if len(w.updateCalledWith) != 1 || w.updateCalledWith[0].Title != "Renamed" {
		t.Errorf("svc.Update called with %v", w.updateCalledWith)
	}
	if len(updater.calledWith) != 0 {
		t.Errorf("UpdateAllSizeLimits must NOT fire when no sizes set; got %v", updater.calledWith)
	}
}

func TestQualityDefinitionUpdateHandler_WithSizesPropagatesToProfiles(t *testing.T) {
	t.Parallel()
	w := &fakeQualityDefinitionWriter{row: &qualitydefinitions.Decorated{Title: "SDTV", Quality: quality.SDTV}}
	updater := &recordingSizeLimitUpdater{}
	rr := httptest.NewRecorder()
	body := `{"id":1,"title":"SDTV","weight":1,"quality":{"id":1,"name":"SDTV","source":"television","resolution":480},"minSize":2.5,"maxSize":100,"preferredSize":50}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/qualitydefinition/1", strings.NewReader(body))
	req.SetPathValue("id", "1")
	QualityDefinitionUpdateHandler(w, updater).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if len(updater.calledWith) != 1 {
		t.Fatalf("UpdateAllSizeLimits should have been called once; got %d", len(updater.calledWith))
	}
	sl := updater.calledWith[0]
	if sl.MinSize == nil || *sl.MinSize != 2.5 {
		t.Errorf("min: %v", sl.MinSize)
	}
	if sl.MaxSize == nil || *sl.MaxSize != 100 {
		t.Errorf("max: %v", sl.MaxSize)
	}
}

func TestQualityDefinitionUpdateHandler_EmptyTitleIs400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := `{"id":1,"title":"","weight":1,"quality":{"id":1,"name":"SDTV","source":"television","resolution":480}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/qualitydefinition/1", strings.NewReader(body))
	req.SetPathValue("id", "1")
	QualityDefinitionUpdateHandler(&fakeQualityDefinitionWriter{row: &qualitydefinitions.Decorated{}}, &recordingSizeLimitUpdater{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"propertyName":"Title"`) {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestQualityDefinitionUpdateHandler_SizeOutOfRangeIs400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := `{"id":1,"title":"SDTV","weight":1,"quality":{"id":1,"name":"SDTV","source":"television","resolution":480},"minSize":2000}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/qualitydefinition/1", strings.NewReader(body))
	req.SetPathValue("id", "1")
	QualityDefinitionUpdateHandler(&fakeQualityDefinitionWriter{row: &qualitydefinitions.Decorated{}}, &recordingSizeLimitUpdater{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"propertyName":"MinSize"`) {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestQualityDefinitionUpdateHandler_MinAbovePreferredIs400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := `{"id":1,"title":"SDTV","weight":1,"quality":{"id":1,"name":"SDTV","source":"television","resolution":480},"minSize":50,"preferredSize":20}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/qualitydefinition/1", strings.NewReader(body))
	req.SetPathValue("id", "1")
	QualityDefinitionUpdateHandler(&fakeQualityDefinitionWriter{row: &qualitydefinitions.Decorated{}}, &recordingSizeLimitUpdater{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"propertyName":"PreferredSize"`) {
		t.Errorf("body: %s", rr.Body.String())
	}
}

// ----- PUT /qualitydefinition/update (bulk) -----

func TestQualityDefinitionUpdateManyHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeQualityDefinitionWriter{
		rows: []qualitydefinitions.Decorated{
			{ID: 1, Title: "SDTV", Quality: quality.SDTV},
			{ID: 2, Title: "HDTV-720p", Quality: quality.HDTV720p},
		},
	}
	updater := &recordingSizeLimitUpdater{}
	rr := httptest.NewRecorder()
	body := `[
		{"id":1,"title":"Renamed-1","weight":1,"quality":{"id":1,"name":"SDTV","source":"television","resolution":480}},
		{"id":2,"title":"Renamed-2","weight":2,"quality":{"id":4,"name":"HDTV-720p","source":"television","resolution":720},"minSize":10}
	]`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/qualitydefinition/update", strings.NewReader(body))
	QualityDefinitionUpdateManyHandler(w, w, updater).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if len(w.updateCalledWith) != 2 {
		t.Errorf("expected 2 Updates, got %d", len(w.updateCalledWith))
	}
	if len(updater.calledWith) != 1 {
		t.Errorf("UpdateAllSizeLimits should fire once (only row 2 has sizes); got %d", len(updater.calledWith))
	}
}

func TestQualityDefinitionUpdateManyHandler_PartialValidationFailureRejectsBatch(t *testing.T) {
	t.Parallel()
	w := &fakeQualityDefinitionWriter{}
	rr := httptest.NewRecorder()
	body := `[
		{"id":1,"title":"OK","weight":1,"quality":{"id":1,"name":"SDTV","source":"television","resolution":480}},
		{"id":2,"title":"","weight":2,"quality":{"id":4,"name":"HDTV","source":"television","resolution":720}}
	]`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/qualitydefinition/update", strings.NewReader(body))
	QualityDefinitionUpdateManyHandler(w, w, &recordingSizeLimitUpdater{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"propertyName":"[1].Title"`) {
		t.Errorf("validation should attribute the failure to row [1].Title: %s", rr.Body.String())
	}
	if len(w.updateCalledWith) != 0 {
		t.Errorf("validation must reject before any Update fires; got %d updates", len(w.updateCalledWith))
	}
}
