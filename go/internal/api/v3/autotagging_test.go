package v3

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/autotagging"
)

type fakeAutoTaggingLister struct {
	rows []*autotagging.Tag
	err  error
}

func (f *fakeAutoTaggingLister) All(context.Context) ([]*autotagging.Tag, error) {
	return f.rows, f.err
}

func TestAutoTaggingHandler_Empty(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	AutoTaggingHandler(&fakeAutoTaggingLister{rows: nil}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/autotagging", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	if got := rr.Body.String(); got != "[]" {
		t.Errorf("empty body: got %q want []", got)
	}
}

func TestAutoTaggingHandler_PopulatedNilTagsNormalised(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	AutoTaggingHandler(&fakeAutoTaggingLister{rows: []*autotagging.Tag{
		{ID: 1, Name: "alpha", RemoveTagsAutomatically: false, Tags: nil},
	}}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/autotagging", nil))
	body := rr.Body.String()
	// Nil tags must serialise as [] not null — C# HashSet<int>
	// renders as [] when empty.
	if !strings.Contains(body, `"tags":[]`) {
		t.Errorf("tags should be []: %s", body)
	}
	if !strings.Contains(body, `"specifications":[]`) {
		t.Errorf("specifications should be [] (deferred port): %s", body)
	}
}

func TestAutoTaggingHandler_Error(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	AutoTaggingHandler(&fakeAutoTaggingLister{err: errors.New("db down")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/autotagging", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}
