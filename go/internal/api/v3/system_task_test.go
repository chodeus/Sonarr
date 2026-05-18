package v3

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/scheduledtasks"
)

type fakeTaskLister struct {
	tasks []scheduledtasks.Task
}

func (f *fakeTaskLister) GetAll(_ context.Context) []scheduledtasks.Task { return f.tasks }

func TestSystemTaskHandler_Empty(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	SystemTaskHandler(&fakeTaskLister{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/system/task", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != `[]` {
		t.Errorf("empty body: got %q want []", rr.Body.String())
	}
}

func TestSystemTaskHandler_NameOrderingAndNextExecution(t *testing.T) {
	t.Parallel()
	last := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	rr := httptest.NewRecorder()
	SystemTaskHandler(&fakeTaskLister{tasks: []scheduledtasks.Task{
		{ID: 1, TypeName: "RssSync", Interval: 30, LastExecution: last},
		{ID: 2, TypeName: "Backup", Interval: 10080, LastExecution: last},
		{ID: 3, TypeName: "RefreshSeries", Interval: 720, LastExecution: last},
	}}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/system/task", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got []TaskResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len: %d", len(got))
	}
	// Name order: "Backup" < "Refresh Series" < "Rss Sync"
	if got[0].Name != "Backup" || got[1].Name != "Refresh Series" || got[2].Name != "Rss Sync" {
		t.Errorf("Name ordering: %q %q %q", got[0].Name, got[1].Name, got[2].Name)
	}
	// TaskName preserves the canonical TypeName ("RefreshSeries").
	if got[1].TaskName != "RefreshSeries" {
		t.Errorf("TaskName: %q", got[1].TaskName)
	}
	// NextExecution = LastExecution + Interval minutes.
	wantNext := last.Add(720 * time.Minute)
	if !got[1].NextExecution.Equal(wantNext) {
		t.Errorf("NextExecution: got %v want %v", got[1].NextExecution, wantNext)
	}
}

func TestSystemTaskByIDHandler_Found(t *testing.T) {
	t.Parallel()
	last := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	svc := &fakeTaskLister{tasks: []scheduledtasks.Task{
		{ID: 7, TypeName: "Backup", Interval: 10080, LastExecution: last},
	}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/system/task/7", nil)
	req.SetPathValue("id", "7")
	SystemTaskByIDHandler(svc).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got TaskResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != 7 || got.TaskName != "Backup" {
		t.Errorf("got %+v", got)
	}
}

func TestSystemTaskByIDHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/system/task/999", nil)
	req.SetPathValue("id", "999")
	SystemTaskByIDHandler(&fakeTaskLister{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestSystemTaskByIDHandler_InvalidID_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/system/task/abc", nil)
	req.SetPathValue("id", "abc")
	SystemTaskByIDHandler(&fakeTaskLister{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}
