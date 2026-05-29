package v3

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakePreviewer is a RenamePreviewer stub for handler tests.
type fakePreviewer struct {
	series []RenamePreview
	season []RenamePreview
	bulk   []RenamePreview
	err    error
}

func (f fakePreviewer) GetBySeries(context.Context, int) ([]RenamePreview, error) {
	return f.series, f.err
}
func (f fakePreviewer) GetBySeason(context.Context, int, int) ([]RenamePreview, error) {
	return f.season, f.err
}
func (f fakePreviewer) GetBulk(context.Context, []int) ([]RenamePreview, error) {
	return f.bulk, f.err
}

func TestRenameHandler_ProjectsPreviews(t *testing.T) {
	t.Parallel()
	p := fakePreviewer{series: []RenamePreview{
		{SeriesID: 1, SeasonNumber: 2, EpisodeNumbers: []int{3}, EpisodeFileID: 99, ExistingPath: "old.mkv", NewPath: "new.mkv"},
	}}
	rr := httptest.NewRecorder()
	RenameHandler(p).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/rename?seriesId=1", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got []RenameEpisodeResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d resources, want 1", len(got))
	}
	// C# maps Id = EpisodeFileId.
	if got[0].ID != 99 || got[0].EpisodeFileID != 99 {
		t.Errorf("id/episodeFileId = %d/%d, want 99/99", got[0].ID, got[0].EpisodeFileID)
	}
	if got[0].NewPath != "new.mkv" || got[0].ExistingPath != "old.mkv" {
		t.Errorf("paths: %q -> %q", got[0].ExistingPath, got[0].NewPath)
	}
}

func TestRenameHandler_SeasonBranch(t *testing.T) {
	t.Parallel()
	p := fakePreviewer{season: []RenamePreview{{SeriesID: 1, SeasonNumber: 2, EpisodeFileID: 5}}}
	rr := httptest.NewRecorder()
	RenameHandler(p).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/rename?seriesId=1&seasonNumber=2", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got []RenameEpisodeResource
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if len(got) != 1 || got[0].EpisodeFileID != 5 {
		t.Errorf("season branch projection wrong: %+v", got)
	}
}

func TestRenameHandler_InvalidSeasonNumber_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	RenameHandler(fakePreviewer{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/rename?seriesId=1&seasonNumber=x", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}

func TestRenameHandler_PreviewerError_500(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	RenameHandler(fakePreviewer{err: errors.New("boom")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/rename?seriesId=1", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}

func TestRenameBulkHandler_ProjectsPreviews(t *testing.T) {
	t.Parallel()
	p := fakePreviewer{bulk: []RenamePreview{{SeriesID: 2, EpisodeFileID: 7}}}
	rr := httptest.NewRecorder()
	RenameBulkHandler(p).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/rename/bulk?seriesIds=2&seriesIds=3", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got []RenameEpisodeResource
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if len(got) != 1 || got[0].EpisodeFileID != 7 {
		t.Errorf("bulk projection wrong: %+v", got)
	}
}

func TestRenameHandler_EmptyArray(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	RenameHandler(nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/rename?seriesId=42", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != "[]" {
		t.Errorf("body: %q", rr.Body.String())
	}
}

func TestRenameHandler_MissingSeriesID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	RenameHandler(nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/rename", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestRenameHandler_InvalidSeriesID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	RenameHandler(nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/rename?seriesId=abc", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestRenameHandler_WithSeasonNumber(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	RenameHandler(nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/rename?seriesId=42&seasonNumber=1", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestRenameBulkHandler_EmptyArray(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	RenameBulkHandler(nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/rename/bulk?seriesIds=1&seriesIds=2", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != "[]" {
		t.Errorf("body: %q", rr.Body.String())
	}
}

func TestRenameBulkHandler_MissingSeriesIDs_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	RenameBulkHandler(nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/rename/bulk", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestRenameBulkHandler_NegativeSeriesID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	RenameBulkHandler(nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/rename/bulk?seriesIds=0", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}
