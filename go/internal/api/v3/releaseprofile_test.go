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
