package v3

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/series"
)

type fakeSeriesLister struct {
	rows []series.Series
	err  error
}

func (f *fakeSeriesLister) GetAllSeries(context.Context) ([]series.Series, error) {
	return f.rows, f.err
}

func TestSeriesListHandler_EmptyList(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	SeriesListHandler(&fakeSeriesLister{}).ServeHTTP(rr,
		httptest.NewRequest(http.MethodGet, "/api/v3/series", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	if rr.Body.String() != "[]" {
		t.Errorf("body should be []: %q", rr.Body.String())
	}
}

func TestSeriesListHandler_ScalarProjection(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	SeriesListHandler(&fakeSeriesLister{rows: []series.Series{
		{ID: 1, Title: "Demo", SortTitle: "demo", Status: 1, Path: "/tv/Demo", Monitored: true, SeriesType: 2, MonitorNewItems: 1, Runtime: 30, Tags: []int{1, 2}},
	}}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/series", nil))

	var got []SeriesResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("count: %+v", got)
	}
	r := got[0]
	if r.Title != "Demo" || r.Path != "/tv/Demo" || !r.Monitored || r.Runtime != 30 {
		t.Errorf("scalar projection: %+v", r)
	}
	if r.Status != "ended" || r.SeriesType != "anime" || r.MonitorNewItems != "none" {
		t.Errorf("enum projection: status=%q seriesType=%q monitorNewItems=%q", r.Status, r.SeriesType, r.MonitorNewItems)
	}
	if !r.Ended {
		t.Errorf("ended computed flag: false, want true (status=1)")
	}
	if r.LanguageProfileID != 1 {
		t.Errorf("languageProfileId hard-coded: %d, want 1", r.LanguageProfileID)
	}
	if len(r.Tags) != 2 || r.Tags[0] != 1 {
		t.Errorf("tags: %v", r.Tags)
	}
}

func TestSeriesListHandler_NilTagsEmitsEmptyArray(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	SeriesListHandler(&fakeSeriesLister{rows: []series.Series{
		{ID: 1, Title: "NoTags", Tags: nil},
	}}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/series", nil))
	body := rr.Body.String()
	if !strings.Contains(body, `"tags":[]`) {
		t.Errorf("nil Tags should emit []: %s", body)
	}
}

func TestSeriesListHandler_ListerError(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	SeriesListHandler(&fakeSeriesLister{err: errors.New("boom")}).
		ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/series", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}

func TestSeriesEnumNames(t *testing.T) {
	t.Parallel()
	if seriesStatusName(0) != "continuing" || seriesStatusName(1) != "ended" {
		t.Errorf("seriesStatusName mapping wrong")
	}
	if seriesTypeName(0) != "standard" || seriesTypeName(2) != "anime" {
		t.Errorf("seriesTypeName mapping wrong")
	}
	if monitorNewItemsName(0) != "all" || monitorNewItemsName(1) != "none" {
		t.Errorf("monitorNewItemsName mapping wrong")
	}
	if seriesStatusName(99) != "continuing" {
		t.Errorf("fallback for unknown status")
	}
}
