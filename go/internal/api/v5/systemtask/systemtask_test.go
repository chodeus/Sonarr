package systemtask_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	systemtaskv5 "sonarr.tv/sonarr/internal/api/v5/systemtask"
	"sonarr.tv/sonarr/internal/scheduledtasks"
)

type fakeLister struct {
	tasks []scheduledtasks.Task
}

func (f *fakeLister) GetAll(_ context.Context) []scheduledtasks.Task { return f.tasks }

func TestListHandler_EmptyReturnsArray(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	systemtaskv5.ListHandler(&fakeLister{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/system/task", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if got := rr.Body.String(); got != "[]" {
		t.Errorf("body: %q want []", got)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("content-type: %q", ct)
	}
}

func TestListHandler_PopulatedEmitsLastDuration(t *testing.T) {
	t.Parallel()
	exec := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	start := exec.Add(-5 * time.Minute) // duration = 5 min
	rr := httptest.NewRecorder()
	systemtaskv5.ListHandler(&fakeLister{tasks: []scheduledtasks.Task{
		{ID: 1, TypeName: "RssSync", Interval: 30, LastExecution: exec, LastStartTime: &start},
		{ID: 2, TypeName: "Backup", Interval: 10080, LastExecution: exec, LastStartTime: &start},
	}}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/system/task", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got []systemtaskv5.Resource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len: %d", len(got))
	}
	// Alphabetic ordering: Backup before "Rss Sync"
	if got[0].Name != "Backup" || got[1].Name != "Rss Sync" {
		t.Errorf("ordering: %q %q", got[0].Name, got[1].Name)
	}
	for _, r := range got {
		if r.LastDuration != "00:05:00" {
			t.Errorf("LastDuration: got %q want 00:05:00", r.LastDuration)
		}
		if r.TaskName == "" {
			t.Errorf("TaskName empty for %+v", r)
		}
	}
	if !strings.Contains(rr.Body.String(), `"lastDuration":"00:05:00"`) {
		t.Errorf("expected lastDuration field present in raw body: %s", rr.Body.String())
	}
}

func TestListHandler_LastStartTimeAlwaysEmitted(t *testing.T) {
	t.Parallel()
	exec := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	rr := httptest.NewRecorder()
	// LastStartTime=nil → wire MUST still surface "lastStartTime" as
	// "0001-01-01T00:00:00Z" (.NET default(DateTime) parity). v3's
	// omitempty behaviour does NOT carry through.
	systemtaskv5.ListHandler(&fakeLister{tasks: []scheduledtasks.Task{
		{ID: 1, TypeName: "Backup", Interval: 60, LastExecution: exec, LastStartTime: nil},
	}}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/system/task", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"lastStartTime":"0001-01-01T00:00:00Z"`) {
		t.Errorf("v5 must emit lastStartTime unconditionally; body=%s", body)
	}
}

func TestByIDHandler_Found(t *testing.T) {
	t.Parallel()
	exec := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	start := exec.Add(-time.Second)
	svc := &fakeLister{tasks: []scheduledtasks.Task{
		{ID: 7, TypeName: "Backup", Interval: 10080, LastExecution: exec, LastStartTime: &start},
	}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/system/task/7", nil)
	req.SetPathValue("id", "7")
	systemtaskv5.ByIDHandler(svc).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got systemtaskv5.Resource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != 7 || got.Name != "Backup" || got.LastDuration != "00:00:01" {
		t.Errorf("got %+v", got)
	}
}

func TestByIDHandler_NotFound(t *testing.T) {
	t.Parallel()
	svc := &fakeLister{tasks: []scheduledtasks.Task{
		{ID: 1, TypeName: "Backup", Interval: 60, LastExecution: time.Now()},
	}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/system/task/999", nil)
	req.SetPathValue("id", "999")
	systemtaskv5.ByIDHandler(svc).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
}

func TestByIDHandler_InvalidID(t *testing.T) {
	t.Parallel()
	svc := &fakeLister{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v5/system/task/abc", nil)
	req.SetPathValue("id", "abc")
	systemtaskv5.ByIDHandler(svc).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400", rr.Code)
	}
}

// TestNextExecution pins LastExecution + Interval-minutes contract.
func TestNextExecution(t *testing.T) {
	t.Parallel()
	exec := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	rr := httptest.NewRecorder()
	systemtaskv5.ListHandler(&fakeLister{tasks: []scheduledtasks.Task{
		{ID: 1, TypeName: "RssSync", Interval: 30, LastExecution: exec},
	}}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/system/task", nil))
	var got []systemtaskv5.Resource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := exec.Add(30 * time.Minute)
	if !got[0].NextExecution.Equal(want) {
		t.Errorf("NextExecution: got %v want %v", got[0].NextExecution, want)
	}
}

// TestFormatTimeSpan_Variants spot-checks the LastDuration formatter
// for negative durations and sub-second precision (.NET ticks).
func TestFormatTimeSpan_Variants(t *testing.T) {
	t.Parallel()
	exec := time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC)
	start := exec.Add(-1500 * time.Millisecond) // 1.5s before exec
	rr := httptest.NewRecorder()
	systemtaskv5.ListHandler(&fakeLister{tasks: []scheduledtasks.Task{
		{ID: 1, TypeName: "X", Interval: 1, LastExecution: exec, LastStartTime: &start},
	}}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/system/task", nil))
	var got []systemtaskv5.Resource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got[0].LastDuration != "00:00:01.5000000" {
		t.Errorf("ticks: got %q want 00:00:01.5000000", got[0].LastDuration)
	}
}
