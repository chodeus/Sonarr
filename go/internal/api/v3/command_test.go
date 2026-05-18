package v3

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/commands"
)

// fakeCommandService stubs CommandService. Calls are recorded so the
// POST test can assert the canonical name + body propagation; All/Get
// return preconfigured rows for the read tests.
type fakeCommandService struct {
	pushed  []pushArgs
	all     []commands.Command
	getByID map[int]*commands.Command
}

type pushArgs struct {
	name     string
	body     string
	priority commands.Priority
	trigger  commands.Trigger
}

func (f *fakeCommandService) Push(_ context.Context, name, body string, p commands.Priority, t commands.Trigger) (*commands.Command, error) {
	f.pushed = append(f.pushed, pushArgs{name, body, p, t})
	return &commands.Command{
		ID:       len(f.pushed),
		Name:     name,
		Body:     body,
		Priority: int(p),
		Status:   int(commands.StatusQueued),
		QueuedAt: time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC),
		Trigger:  int(t),
		Result:   int(commands.ResultUnknown),
	}, nil
}

func (f *fakeCommandService) Get(_ context.Context, id int) (*commands.Command, error) {
	if c, ok := f.getByID[id]; ok {
		return c, nil
	}
	return nil, commands.ErrNotFound
}

func (f *fakeCommandService) All(_ context.Context) ([]commands.Command, error) {
	return f.all, nil
}

func (f *fakeCommandService) Cancel(_ context.Context, id int) error {
	if _, ok := f.getByID[id]; !ok {
		return commands.ErrNotFound
	}
	return nil
}

type fakeNameResolver struct{ names map[string]string }

func (f fakeNameResolver) CanonicalName(name string) (string, bool) {
	for k := range f.names {
		if strings.EqualFold(k, name) {
			return k, true
		}
	}
	return "", false
}

func defaultResolver() fakeNameResolver {
	return fakeNameResolver{names: map[string]string{
		"RssSync":       "",
		"RefreshSeries": "",
		"ManualImport":  "",
	}}
}

func TestCommandCreateHandler_Success(t *testing.T) {
	t.Parallel()
	svc := &fakeCommandService{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/command", strings.NewReader(`{"name":"RssSync"}`))
	CommandCreateHandler(svc, defaultResolver()).ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if len(svc.pushed) != 1 {
		t.Fatalf("expected 1 push; got %d", len(svc.pushed))
	}
	if svc.pushed[0].name != "RssSync" {
		t.Errorf("canonical name: got %q", svc.pushed[0].name)
	}
	if svc.pushed[0].priority != commands.PriorityNormal {
		t.Errorf("priority: got %v", svc.pushed[0].priority)
	}
	if svc.pushed[0].trigger != commands.TriggerManual {
		t.Errorf("trigger: got %v", svc.pushed[0].trigger)
	}
	var got CommandResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Name != "RssSync" || got.CommandName != "Rss Sync" {
		t.Errorf("response Name/CommandName: %q / %q", got.Name, got.CommandName)
	}
	if got.Status != "queued" || got.Priority != "normal" || got.Trigger != "manual" {
		t.Errorf("response enums: status=%q priority=%q trigger=%q", got.Status, got.Priority, got.Trigger)
	}
}

func TestCommandCreateHandler_CaseInsensitiveName(t *testing.T) {
	t.Parallel()
	svc := &fakeCommandService{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/command", strings.NewReader(`{"name":"rsssync"}`))
	CommandCreateHandler(svc, defaultResolver()).ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if svc.pushed[0].name != "RssSync" {
		t.Errorf("expected canonical PascalCase; got %q", svc.pushed[0].name)
	}
}

func TestCommandCreateHandler_ManualImportHighPriority(t *testing.T) {
	t.Parallel()
	svc := &fakeCommandService{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/command", strings.NewReader(`{"name":"manualImport"}`))
	CommandCreateHandler(svc, defaultResolver()).ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if svc.pushed[0].priority != commands.PriorityHigh {
		t.Errorf("ManualImport should be High; got %v", svc.pushed[0].priority)
	}
}

func TestCommandCreateHandler_BodyPassedThrough(t *testing.T) {
	t.Parallel()
	svc := &fakeCommandService{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/command", strings.NewReader(`{"name":"refreshSeries","seriesIds":[1,2,3]}`))
	CommandCreateHandler(svc, defaultResolver()).ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(svc.pushed[0].body, `"seriesIds":[1,2,3]`) {
		t.Errorf("body should include seriesIds; got %s", svc.pushed[0].body)
	}
	if !strings.Contains(svc.pushed[0].body, `"name":"RefreshSeries"`) {
		t.Errorf("body should have canonical name; got %s", svc.pushed[0].body)
	}
}

func TestCommandCreateHandler_BlankName_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/command", strings.NewReader(`{"name":"   "}`))
	CommandCreateHandler(&fakeCommandService{}, defaultResolver()).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestCommandCreateHandler_UnknownCommand_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/command", strings.NewReader(`{"name":"FrobnicateAll"}`))
	CommandCreateHandler(&fakeCommandService{}, defaultResolver()).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Unknown command") {
		t.Errorf("expected unknown-command message; body=%s", rr.Body.String())
	}
}

func TestCommandCreateHandler_MalformedBody_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/command", strings.NewReader(`not json`))
	CommandCreateHandler(&fakeCommandService{}, defaultResolver()).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestCommandListHandler_OrderingStartedThenQueued(t *testing.T) {
	t.Parallel()
	queued := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	svc := &fakeCommandService{
		all: []commands.Command{
			{ID: 1, Name: "Completed", Status: int(commands.StatusCompleted), Priority: 0, QueuedAt: queued},
			{ID: 2, Name: "Queued", Status: int(commands.StatusQueued), Priority: 0, QueuedAt: queued},
			{ID: 3, Name: "Started", Status: int(commands.StatusStarted), Priority: 0, QueuedAt: queued},
		},
	}
	rr := httptest.NewRecorder()
	CommandListHandler(svc).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/command", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got []CommandResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len: %d", len(got))
	}
	if got[0].Name != "Started" || got[1].Name != "Queued" || got[2].Name != "Completed" {
		t.Errorf("ordering: got %q %q %q", got[0].Name, got[1].Name, got[2].Name)
	}
}

func TestCommandByIDHandler_Found(t *testing.T) {
	t.Parallel()
	queued := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	svc := &fakeCommandService{getByID: map[int]*commands.Command{
		42: {ID: 42, Name: "RssSync", Status: int(commands.StatusQueued), QueuedAt: queued},
	}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/command/42", nil)
	req.SetPathValue("id", "42")
	CommandByIDHandler(svc).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got CommandResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != 42 {
		t.Errorf("id: %d", got.ID)
	}
}

func TestCommandByIDHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/command/999", nil)
	req.SetPathValue("id", "999")
	CommandByIDHandler(&fakeCommandService{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestCommandDeleteHandler_Success(t *testing.T) {
	t.Parallel()
	svc := &fakeCommandService{getByID: map[int]*commands.Command{
		7: {ID: 7, Name: "RssSync"},
	}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/command/7", nil)
	req.SetPathValue("id", "7")
	CommandDeleteHandler(svc).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
}

func TestCommandDeleteHandler_NotFound_404(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/command/999", nil)
	req.SetPathValue("id", "999")
	CommandDeleteHandler(&fakeCommandService{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

func TestSplitCamelCase(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"RssSync", "Rss Sync"},
		{"CheckForFinishedDownload", "Check For Finished Download"},
		{"Backup", "Backup"},
		{"", ""},
		{"ABC", "ABC"},
	}
	for _, tc := range cases {
		if got := splitCamelCase(tc.in); got != tc.want {
			t.Errorf("splitCamelCase(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, "00:00:00"},
		{time.Second, "00:00:01"},
		{61 * time.Second, "00:01:01"},
		{3661 * time.Second, "01:01:01"},
		{500 * time.Millisecond, "00:00:00.5"},
		{123 * time.Microsecond, "00:00:00.000123"},
	}
	for _, tc := range cases {
		if got := formatDuration(tc.in); got != tc.want {
			t.Errorf("formatDuration(%v) = %q; want %q", tc.in, got, tc.want)
		}
	}
}
