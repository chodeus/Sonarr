package v3

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/delayprofiles"
	"sonarr.tv/sonarr/internal/indexers"
)

type fakeDelayProfileLister struct {
	rows []delayprofiles.Profile
	err  error
}

func (f *fakeDelayProfileLister) All(context.Context) ([]delayprofiles.Profile, error) {
	return f.rows, f.err
}

func TestDownloadProtocolName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   indexers.DownloadProtocol
		want string
	}{
		{indexers.DownloadProtocolUnknown, "unknown"},
		{indexers.DownloadProtocolUsenet, "usenet"},
		{indexers.DownloadProtocolTorrent, "torrent"},
		{indexers.DownloadProtocol(99), "unknown"},
	}
	for _, tc := range cases {
		if got := DownloadProtocolName(tc.in); got != tc.want {
			t.Errorf("DownloadProtocolName(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDelayProfileHandler_Empty(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	DelayProfileHandler(&fakeDelayProfileLister{rows: nil}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/delayprofile", nil))
	if got := rr.Body.String(); got != "[]" {
		t.Errorf("empty body: got %q want []", got)
	}
}

func TestDelayProfileHandler_DefaultProfile(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	DelayProfileHandler(&fakeDelayProfileLister{rows: []delayprofiles.Profile{
		{
			ID: 1, EnableUsenet: true, EnableTorrent: true,
			PreferredProtocol:      int(indexers.DownloadProtocolUsenet),
			Order:                  2147483647,
			BypassIfHighestQuality: true,
			// MinimumCustomFormatScore = nil → must emit 0
			Tags: nil, // → []
		},
	}}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/delayprofile", nil))
	body := rr.Body.String()
	for _, want := range []string{
		`"id":1`,
		`"enableUsenet":true`,
		`"enableTorrent":true`,
		`"preferredProtocol":"usenet"`,
		`"usenetDelay":0`,
		`"torrentDelay":0`,
		`"bypassIfHighestQuality":true`,
		`"bypassIfAboveCustomFormatScore":false`,
		`"minimumCustomFormatScore":0`,
		`"order":2147483647`,
		`"tags":[]`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %s: %s", want, body)
		}
	}
}

func TestDelayProfileHandler_NonNilMinScore(t *testing.T) {
	t.Parallel()
	score := 75
	rr := httptest.NewRecorder()
	DelayProfileHandler(&fakeDelayProfileLister{rows: []delayprofiles.Profile{
		{ID: 2, PreferredProtocol: int(indexers.DownloadProtocolTorrent), MinimumCustomFormatScore: &score},
	}}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/delayprofile", nil))
	body := rr.Body.String()
	if !strings.Contains(body, `"minimumCustomFormatScore":75`) {
		t.Errorf("expected score 75: %s", body)
	}
	if !strings.Contains(body, `"preferredProtocol":"torrent"`) {
		t.Errorf("expected torrent protocol: %s", body)
	}
}

func TestDelayProfileHandler_Error(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	DelayProfileHandler(&fakeDelayProfileLister{err: errors.New("db down")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/delayprofile", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}

// ---------- 5.B8: CRUD + reorder tests ----------

type fakeDelayProfileWriter struct {
	row        *delayprofiles.Profile
	rows       []delayprofiles.Profile
	reordered  []delayprofiles.Profile
	getErr     error
	addErr     error
	updateErr  error
	deleteErr  error
	reorderErr error

	addCalledWith      *delayprofiles.Profile
	updateCalledWith   *delayprofiles.Profile
	deleteCalledID     int
	reorderCalledID    int
	reorderCalledAfter *int
}

func (f *fakeDelayProfileWriter) All(_ context.Context) ([]delayprofiles.Profile, error) {
	return f.rows, nil
}

func (f *fakeDelayProfileWriter) Get(_ context.Context, id int) (*delayprofiles.Profile, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.row == nil {
		return nil, delayprofiles.ErrNotFound
	}
	return f.row, nil
}

func (f *fakeDelayProfileWriter) Add(_ context.Context, p *delayprofiles.Profile) (*delayprofiles.Profile, error) {
	f.addCalledWith = p
	if f.addErr != nil {
		return nil, f.addErr
	}
	out := *p
	out.ID = 7
	return &out, nil
}

func (f *fakeDelayProfileWriter) Update(_ context.Context, p *delayprofiles.Profile) error {
	f.updateCalledWith = p
	return f.updateErr
}

func (f *fakeDelayProfileWriter) Delete(_ context.Context, id int) error {
	f.deleteCalledID = id
	return f.deleteErr
}

func (f *fakeDelayProfileWriter) Reorder(_ context.Context, id int, afterID *int) ([]delayprofiles.Profile, error) {
	f.reorderCalledID = id
	f.reorderCalledAfter = afterID
	if f.reorderErr != nil {
		return nil, f.reorderErr
	}
	return f.reordered, nil
}

// ----- GET /delayprofile/{id} -----

func TestDelayProfileByIDHandler_Hit(t *testing.T) {
	t.Parallel()
	w := &fakeDelayProfileWriter{row: &delayprofiles.Profile{ID: 7, EnableUsenet: true, Tags: []int{1, 2}}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/delayprofile/7", nil)
	req.SetPathValue("id", "7")
	DelayProfileByIDHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"id":7`) || !strings.Contains(rr.Body.String(), `"tags":[1,2]`) {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestDelayProfileByIDHandler_NotFound(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/delayprofile/99", nil)
	req.SetPathValue("id", "99")
	DelayProfileByIDHandler(&fakeDelayProfileWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

// ----- POST /delayprofile -----

func TestDelayProfileCreateHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeDelayProfileWriter{}
	rr := httptest.NewRecorder()
	body := `{"id":0,"enableUsenet":true,"enableTorrent":false,"preferredProtocol":"usenet","usenetDelay":30,"torrentDelay":0,"bypassIfHighestQuality":false,"bypassIfAboveCustomFormatScore":false,"minimumCustomFormatScore":0,"order":1,"tags":[1,2]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v3/delayprofile", strings.NewReader(body))
	DelayProfileCreateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if w.addCalledWith == nil || !w.addCalledWith.EnableUsenet || w.addCalledWith.UsenetDelay != 30 {
		t.Errorf("svc.Add called with %+v", w.addCalledWith)
	}
	if !strings.Contains(rr.Body.String(), `"id":7`) {
		t.Errorf("response should carry id 7: %s", rr.Body.String())
	}
}

func TestDelayProfileCreateHandler_EmptyTagsIs400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := `{"enableUsenet":true,"enableTorrent":false,"preferredProtocol":"usenet","usenetDelay":0,"torrentDelay":0,"tags":[]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v3/delayprofile", strings.NewReader(body))
	DelayProfileCreateHandler(&fakeDelayProfileWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"propertyName":"Tags"`) {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestDelayProfileCreateHandler_NeitherProtocolEnabled400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := `{"enableUsenet":false,"enableTorrent":false,"preferredProtocol":"usenet","usenetDelay":0,"torrentDelay":0,"tags":[1]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v3/delayprofile", strings.NewReader(body))
	DelayProfileCreateHandler(&fakeDelayProfileWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Either Usenet or Torrent should be enabled") {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestDelayProfileCreateHandler_NegativeDelay400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := `{"enableUsenet":true,"enableTorrent":false,"preferredProtocol":"usenet","usenetDelay":-5,"torrentDelay":0,"tags":[1]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v3/delayprofile", strings.NewReader(body))
	DelayProfileCreateHandler(&fakeDelayProfileWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"propertyName":"UsenetDelay"`) {
		t.Errorf("body: %s", rr.Body.String())
	}
}

// ----- PUT /delayprofile/{id} -----

func TestDelayProfileUpdateHandler_GlobalRequiresEmptyTags(t *testing.T) {
	t.Parallel()
	// id=1 + non-empty Tags is the canonical rule violation.
	rr := httptest.NewRecorder()
	body := `{"id":1,"enableUsenet":true,"enableTorrent":false,"preferredProtocol":"usenet","usenetDelay":0,"torrentDelay":0,"tags":[1,2]}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/delayprofile/1", strings.NewReader(body))
	req.SetPathValue("id", "1")
	DelayProfileUpdateHandler(&fakeDelayProfileWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Global delay profile must not have tags") {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestDelayProfileUpdateHandler_GlobalEmptyTagsSucceeds(t *testing.T) {
	t.Parallel()
	w := &fakeDelayProfileWriter{}
	rr := httptest.NewRecorder()
	body := `{"id":1,"enableUsenet":true,"enableTorrent":false,"preferredProtocol":"usenet","usenetDelay":0,"torrentDelay":0,"tags":[]}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/delayprofile/1", strings.NewReader(body))
	req.SetPathValue("id", "1")
	DelayProfileUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Errorf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
}

func TestDelayProfileUpdateHandler_PathIDWinsOverBody(t *testing.T) {
	t.Parallel()
	w := &fakeDelayProfileWriter{}
	rr := httptest.NewRecorder()
	body := `{"id":999,"enableUsenet":true,"enableTorrent":false,"preferredProtocol":"usenet","usenetDelay":0,"torrentDelay":0,"tags":[1]}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/delayprofile/5", strings.NewReader(body))
	req.SetPathValue("id", "5")
	DelayProfileUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d", rr.Code)
	}
	if w.updateCalledWith.ID != 5 {
		t.Errorf("path id wins: got %d want 5", w.updateCalledWith.ID)
	}
}

// ----- DELETE /delayprofile/{id} -----

func TestDelayProfileDeleteHandler_GlobalIs405(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/delayprofile/1", nil)
	req.SetPathValue("id", "1")
	DelayProfileDeleteHandler(&fakeDelayProfileWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d want 405", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Cannot delete global delay profile") {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestDelayProfileDeleteHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeDelayProfileWriter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/delayprofile/5", nil)
	req.SetPathValue("id", "5")
	DelayProfileDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status: %d", rr.Code)
	}
	if w.deleteCalledID != 5 {
		t.Errorf("Delete called with %d", w.deleteCalledID)
	}
}

// TestDelayProfileDeleteHandler_Idempotent — C.1.i-follow-4 (J232). 200 on missing id.
func TestDelayProfileDeleteHandler_Idempotent(t *testing.T) {
	t.Parallel()
	w := &fakeDelayProfileWriter{deleteErr: delayprofiles.ErrNotFound}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/delayprofile/99", nil)
	req.SetPathValue("id", "99")
	DelayProfileDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200 (idempotent)", rr.Code)
	}
}

// ----- PUT /delayprofile/reorder/{id} -----

func TestDelayProfileReorderHandler_WithAfter(t *testing.T) {
	t.Parallel()
	w := &fakeDelayProfileWriter{reordered: []delayprofiles.Profile{
		{ID: 5, Order: 1, EnableUsenet: true},
		{ID: 3, Order: 2, EnableUsenet: true},
	}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/delayprofile/reorder/3?after=5", nil)
	req.SetPathValue("id", "3")
	DelayProfileReorderHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if w.reorderCalledID != 3 || w.reorderCalledAfter == nil || *w.reorderCalledAfter != 5 {
		t.Errorf("Reorder called with id=%d after=%v", w.reorderCalledID, w.reorderCalledAfter)
	}
	if !strings.Contains(rr.Body.String(), `"id":5`) || !strings.Contains(rr.Body.String(), `"id":3`) {
		t.Errorf("body should list both profiles: %s", rr.Body.String())
	}
}

func TestDelayProfileReorderHandler_NoAfter(t *testing.T) {
	t.Parallel()
	// Omitting after= moves the profile to top — passes nil to the service.
	w := &fakeDelayProfileWriter{reordered: []delayprofiles.Profile{{ID: 3, EnableUsenet: true}}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/delayprofile/reorder/3", nil)
	req.SetPathValue("id", "3")
	DelayProfileReorderHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if w.reorderCalledAfter != nil {
		t.Errorf("Reorder should be called with nil afterID; got %v", w.reorderCalledAfter)
	}
}

func TestDelayProfileReorderHandler_InvalidAfter400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/delayprofile/reorder/3?after=abc", nil)
	req.SetPathValue("id", "3")
	DelayProfileReorderHandler(&fakeDelayProfileWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestDelayProfileReorderHandler_NotFound404(t *testing.T) {
	t.Parallel()
	w := &fakeDelayProfileWriter{reorderErr: delayprofiles.ErrNotFound}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/delayprofile/reorder/99", nil)
	req.SetPathValue("id", "99")
	DelayProfileReorderHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}
