package v3

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/tags"
)

type fakeTagLister struct {
	rows []tags.Tag
	err  error
}

func (f *fakeTagLister) All(context.Context) ([]tags.Tag, error) {
	return f.rows, f.err
}

func TestTagHandler_Empty(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	TagHandler(&fakeTagLister{rows: nil}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/tag", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	if got := rr.Body.String(); got != "[]" {
		t.Errorf("empty body: got %q want []", got)
	}
}

func TestTagHandler_Populated(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	TagHandler(&fakeTagLister{rows: []tags.Tag{
		{ID: 1, Label: "alpha"},
		{ID: 2, Label: "beta"},
	}}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/tag", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	want := `[{"id":1,"label":"alpha"},{"id":2,"label":"beta"}]`
	if got := rr.Body.String(); got != want {
		t.Errorf("body: got %q want %q", got, want)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type: %q", ct)
	}
}

func TestTagHandler_Error(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	TagHandler(&fakeTagLister{err: errors.New("db down")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/tag", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}
