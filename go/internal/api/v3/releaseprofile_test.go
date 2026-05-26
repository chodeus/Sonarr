package v3

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/releaseprofiles"
)

type fakeReleaseProfileLister struct {
	rows []releaseprofiles.Profile
	err  error
}

func (f *fakeReleaseProfileLister) All(context.Context) ([]releaseprofiles.Profile, error) {
	return f.rows, f.err
}

func TestReleaseProfileHandler_Empty(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	ReleaseProfileHandler(&fakeReleaseProfileLister{rows: nil}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/releaseprofile", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	if got := rr.Body.String(); got != "[]" {
		t.Errorf("empty body: got %q want []", got)
	}
}

func TestReleaseProfileHandler_PopulatedFieldShape(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	ReleaseProfileHandler(&fakeReleaseProfileLister{rows: []releaseprofiles.Profile{
		{
			ID: 1, Name: "myProfile", Enabled: true,
			Required:           []string{"x264"},
			Ignored:            nil, // → []
			Tags:               nil, // → []
			ExcludedTags:       []int{5},
			IndexerIDs:         []int{42, 99},
			AirDateRestriction: true,
			AirDateGracePeriod: 7,
		},
	}}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/releaseprofile", nil))
	body := rr.Body.String()
	for _, want := range []string{
		`"id":1`,
		`"name":"myProfile"`,
		`"enabled":true`,
		`"required":["x264"]`,
		`"ignored":[]`, // C# `?? new List<string>()`
		`"airDateRestriction":true`,
		`"airDateGracePeriod":7`,
		`"indexerId":42`, // FirstOrDefault on IndexerIDs
		`"tags":[]`,      // nil → []
		`"excludedTags":[5]`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %s: %s", want, body)
		}
	}
}

func TestReleaseProfileHandler_NoIndexerEmitsZero(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	ReleaseProfileHandler(&fakeReleaseProfileLister{rows: []releaseprofiles.Profile{
		{ID: 1, Name: "noIndexer", IndexerIDs: nil},
	}}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/releaseprofile", nil))
	if !strings.Contains(rr.Body.String(), `"indexerId":0`) {
		t.Errorf("FirstOrDefault should yield 0 on empty IndexerIDs: %s", rr.Body.String())
	}
}

func TestReleaseProfileHandler_Error(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	ReleaseProfileHandler(&fakeReleaseProfileLister{err: errors.New("db down")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/releaseprofile", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}

// ---------- 5.B6: CRUD tests ----------

type fakeReleaseProfileWriter struct {
	row       *releaseprofiles.Profile
	getErr    error
	addErr    error
	updateErr error
	deleteErr error

	addCalledWith    *releaseprofiles.Profile
	updateCalledWith *releaseprofiles.Profile
	deleteCalledID   int
}

func (f *fakeReleaseProfileWriter) Get(_ context.Context, id int) (*releaseprofiles.Profile, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.row == nil {
		return nil, releaseprofiles.ErrNotFound
	}
	return f.row, nil
}

func (f *fakeReleaseProfileWriter) Add(_ context.Context, p *releaseprofiles.Profile) (*releaseprofiles.Profile, error) {
	f.addCalledWith = p
	if f.addErr != nil {
		return nil, f.addErr
	}
	out := *p
	out.ID = 21
	return &out, nil
}

func (f *fakeReleaseProfileWriter) Update(_ context.Context, p *releaseprofiles.Profile) error {
	f.updateCalledWith = p
	return f.updateErr
}

func (f *fakeReleaseProfileWriter) Delete(_ context.Context, id int) error {
	f.deleteCalledID = id
	return f.deleteErr
}

// ----- GET /releaseprofile/{id} -----

func TestReleaseProfileByIDHandler_Hit(t *testing.T) {
	t.Parallel()
	w := &fakeReleaseProfileWriter{row: &releaseprofiles.Profile{ID: 5, Name: "Block junk", Required: []string{"PROPER"}}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/releaseprofile/5", nil)
	req.SetPathValue("id", "5")
	ReleaseProfileByIDHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"id":5`) || !strings.Contains(rr.Body.String(), `"required":["PROPER"]`) {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestReleaseProfileByIDHandler_NotFound(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/releaseprofile/99", nil)
	req.SetPathValue("id", "99")
	ReleaseProfileByIDHandler(&fakeReleaseProfileWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

// ----- POST /releaseprofile -----

func TestReleaseProfileCreateHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeReleaseProfileWriter{}
	rr := httptest.NewRecorder()
	body := `{"id":0,"name":"Block junk","enabled":true,"required":["PROPER"],"ignored":[],"airDateRestriction":false,"airDateGracePeriod":0,"indexerId":3,"tags":[],"excludedTags":[]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v3/releaseprofile", strings.NewReader(body))
	ReleaseProfileCreateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if w.addCalledWith == nil || w.addCalledWith.Name != "Block junk" {
		t.Errorf("svc.Add called with %+v", w.addCalledWith)
	}
	// Single IndexerId from the resource should expand to a 1-element IndexerIDs slice.
	if len(w.addCalledWith.IndexerIDs) != 1 || w.addCalledWith.IndexerIDs[0] != 3 {
		t.Errorf("IndexerIDs: got %v want [3]", w.addCalledWith.IndexerIDs)
	}
	if !strings.Contains(rr.Body.String(), `"id":21`) {
		t.Errorf("response should carry id 21: %s", rr.Body.String())
	}
}

func TestReleaseProfileCreateHandler_AllEmptyIs400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := `{"name":"x","required":[],"ignored":[],"airDateRestriction":false,"airDateGracePeriod":0,"indexerId":0,"tags":[],"excludedTags":[]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v3/releaseprofile", strings.NewReader(body))
	ReleaseProfileCreateHandler(&fakeReleaseProfileWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "'Must contain' or 'Must not contain' is required") {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestReleaseProfileCreateHandler_WhitespaceRequiredIs400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := `{"name":"x","required":["   "],"ignored":[],"airDateRestriction":false,"airDateGracePeriod":0,"tags":[],"excludedTags":[]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v3/releaseprofile", strings.NewReader(body))
	ReleaseProfileCreateHandler(&fakeReleaseProfileWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "'Must contain' should not contain whitespaces") {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestReleaseProfileCreateHandler_TagsExcludedTagsOverlap400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := `{"name":"x","required":["foo"],"ignored":[],"airDateRestriction":false,"airDateGracePeriod":0,"tags":[1,2,3],"excludedTags":[2,3]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v3/releaseprofile", strings.NewReader(body))
	ReleaseProfileCreateHandler(&fakeReleaseProfileWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"propertyName":"ExcludedTags"`) {
		t.Errorf("body must fault ExcludedTags: %s", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "2, 3") {
		t.Errorf("body should list overlapping tag ids: %s", rr.Body.String())
	}
}

// ----- PUT /releaseprofile/{id} -----

func TestReleaseProfileUpdateHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeReleaseProfileWriter{}
	rr := httptest.NewRecorder()
	body := `{"id":5,"name":"Renamed","enabled":true,"required":["PROPER"],"ignored":[],"airDateRestriction":false,"airDateGracePeriod":0,"indexerId":0,"tags":[],"excludedTags":[]}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/releaseprofile/5", strings.NewReader(body))
	req.SetPathValue("id", "5")
	ReleaseProfileUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if w.updateCalledWith.ID != 5 || w.updateCalledWith.Name != "Renamed" {
		t.Errorf("svc.Update called with %+v", w.updateCalledWith)
	}
}

func TestReleaseProfileUpdateHandler_PathIDWinsOverBody(t *testing.T) {
	t.Parallel()
	w := &fakeReleaseProfileWriter{}
	rr := httptest.NewRecorder()
	body := `{"id":999,"name":"x","required":["foo"],"ignored":[],"airDateRestriction":false,"airDateGracePeriod":0,"tags":[],"excludedTags":[]}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/releaseprofile/5", strings.NewReader(body))
	req.SetPathValue("id", "5")
	ReleaseProfileUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d", rr.Code)
	}
	if w.updateCalledWith.ID != 5 {
		t.Errorf("path id must win: got %d", w.updateCalledWith.ID)
	}
}

// ----- DELETE /releaseprofile/{id} -----

func TestReleaseProfileDeleteHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeReleaseProfileWriter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/releaseprofile/5", nil)
	req.SetPathValue("id", "5")
	ReleaseProfileDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status: %d", rr.Code)
	}
	if w.deleteCalledID != 5 {
		t.Errorf("svc.Delete called with %d", w.deleteCalledID)
	}
}

// TestReleaseProfileDeleteHandler_Idempotent — C.1.i-follow-4 (J232). 200 on missing id.
func TestReleaseProfileDeleteHandler_Idempotent(t *testing.T) {
	t.Parallel()
	w := &fakeReleaseProfileWriter{deleteErr: releaseprofiles.ErrNotFound}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/releaseprofile/99", nil)
	req.SetPathValue("id", "99")
	ReleaseProfileDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200 (idempotent)", rr.Code)
	}
}

func TestIntersectInts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a, b, want []int
	}{
		{nil, nil, nil},
		{[]int{1, 2, 3}, nil, nil},
		{nil, []int{1, 2, 3}, nil},
		{[]int{1, 2, 3}, []int{2, 3, 4}, []int{2, 3}},
		{[]int{1, 2, 2, 3}, []int{2, 3}, []int{2, 3}}, // dedupe
		{[]int{1, 2, 3}, []int{4, 5, 6}, nil},
	}
	for i, c := range cases {
		got := intersectInts(c.a, c.b)
		if len(got) != len(c.want) {
			t.Errorf("[%d] len mismatch: got %v want %v", i, got, c.want)
			continue
		}
		for j := range got {
			if got[j] != c.want[j] {
				t.Errorf("[%d] [%d]: got %d want %d", i, j, got[j], c.want[j])
			}
		}
	}
}
