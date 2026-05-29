package history

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/history"
)

// fakeMarker implements FailedDownloadMarker.
type fakeMarker struct{ calls atomic.Int64 }

func (f *fakeMarker) MarkAsFailed(_ context.Context, _ int) error {
	f.calls.Add(1)
	return nil
}

// ----- POST /failed/{id} — v5 returns 204 (v3 returns 200 + `{}`) -----

func TestMarkAsFailedHandler_Rewrites200To204(t *testing.T) {
	t.Parallel()
	svc := &fakeMarker{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v5/history/failed/42", nil)
	req.SetPathValue("id", "42")
	MarkAsFailedHandler(svc).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("status: got %d want 204; body=%q", rr.Code, rr.Body.String())
	}
	if rr.Body.Len() != 0 {
		t.Errorf("body should be empty for 204; got %q", rr.Body.String())
	}
	if svc.calls.Load() != 1 {
		t.Errorf("svc.MarkAsFailed should be called once; got %d", svc.calls.Load())
	}
}

// ----- GET /season — v5-specific stub -----

// fakeSeasonReader satisfies apiv3.HistorySeasonReader.
type fakeSeasonReader struct{ rows []history.Entry }

func (f fakeSeasonReader) GetBySeason(_ context.Context, _, _ int, _ *history.EventType) ([]history.Entry, error) {
	return f.rows, nil
}

func TestSeasonHandler_ValidQuery_ReturnsEmptyArray(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/history/season?seriesId=1&seasonNumber=2", nil)
	SeasonHandler(fakeSeasonReader{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%q", rr.Code, rr.Body.String())
	}
	if body := rr.Body.String(); body != "[]" {
		t.Errorf("body: got %q want `[]`", body)
	}
}

func TestSeasonHandler_MissingSeriesID_Returns400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/history/season?seasonNumber=2", nil)
	SeasonHandler(fakeSeasonReader{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}

func TestSeasonHandler_MissingSeasonNumber_Returns400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/history/season?seriesId=1", nil)
	SeasonHandler(fakeSeasonReader{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}

// ----- GET /episode (J427: wired to a real reader) -----

// fakeEpisodeReader satisfies apiv3.HistoryEpisodeReader.
type fakeEpisodeReader struct{ rows []history.Entry }

func (f fakeEpisodeReader) GetByEpisode(_ context.Context, _ int, _ *history.EventType) ([]history.Entry, error) {
	return f.rows, nil
}

func TestEpisodeHandler_ValidQuery_ReturnsEmptyArray(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/history/episode?episodeId=42", nil)
	EpisodeHandler(fakeEpisodeReader{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%q", rr.Code, rr.Body.String())
	}
	if body := rr.Body.String(); body != "[]" {
		t.Errorf("body: got %q want `[]`", body)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("content-type: got %q want application/json", ct)
	}
}

func TestEpisodeHandler_MissingEpisodeID_Returns400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/history/episode", nil)
	EpisodeHandler(fakeEpisodeReader{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}

// J427: a populated reader's rows project into the response.
func TestEpisodeHandler_ReturnsRows_J427(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/history/episode?episodeId=42", nil)
	EpisodeHandler(fakeEpisodeReader{rows: []history.Entry{{ID: 1, EpisodeID: 42}, {ID: 2, EpisodeID: 42}}}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if body := rr.Body.String(); !strings.Contains(body, `"id":1`) || !strings.Contains(body, `"id":2`) {
		t.Errorf("body missing rows: %s", body)
	}
}

// ----- alias guards -----

func TestVariableAliases_NonNil(t *testing.T) {
	t.Parallel()
	if SinceHandler == nil || SeriesHandler == nil {
		t.Error("path-alias handler vars must be non-nil")
	}
	// ListHandler is now a func (J418 includeSubresources translation) —
	// assert it constructs a non-nil handler.
	if ListHandler(nil) == nil {
		t.Error("ListHandler must construct a handler")
	}
}

func TestTypeAliasesMatchV3(t *testing.T) {
	t.Parallel()
	var _ Resource = apiv3.HistoryResource{}
	var _ apiv3.HistoryResource = Resource{}
	// Lister/Range interfaces are check via the alias keyword
	// resolving to the v3 type.
	var _ PagedLister = (apiv3.HistoryPagedLister)(nil)
	var _ RangeReader = (apiv3.HistoryRangeReader)(nil)
	var _ FailedDownloadMarker = (apiv3.FailedDownloadMarker)(nil)
}

// Compile-time pin: history.EventType is reachable from this package
// so empty-corpus tests can construct stub readers without a sentinel.
var _ history.EventType
