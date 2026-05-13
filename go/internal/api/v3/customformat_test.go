package v3

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/customformats"
)

type fakeCustomFormatLister struct {
	rows []*customformats.Format
	err  error
}

func (f *fakeCustomFormatLister) All(context.Context) ([]*customformats.Format, error) {
	return f.rows, f.err
}

func TestCustomFormatHandler_Empty(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	CustomFormatHandler(&fakeCustomFormatLister{rows: nil}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/customformat", nil))
	if got := rr.Body.String(); got != "[]" {
		t.Errorf("empty body: got %q want []", got)
	}
}

func TestCustomFormatHandler_PopulatedShape(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	CustomFormatHandler(&fakeCustomFormatLister{rows: []*customformats.Format{
		{ID: 1, Name: "x264", IncludeCustomFormatWhenRenaming: true},
	}}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/customformat", nil))
	body := rr.Body.String()
	for _, want := range []string{
		`"id":1`,
		`"name":"x264"`,
		`"includeCustomFormatWhenRenaming":true`,
		`"specifications":[]`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %s: %s", want, body)
		}
	}
}

func TestCustomFormatHandler_Error(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	CustomFormatHandler(&fakeCustomFormatLister{err: errors.New("db down")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/customformat", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}
