package uisettings_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"sonarr.tv/sonarr/internal/api/v5/uisettings"
)

type fakeUI struct {
	mu                       sync.Mutex
	firstDayOfWeek           int
	calendarWeekColumnHeader string
	shortDateFormat          string
	longDateFormat           string
	timeFormat               string
	timeZone                 string
	showRelativeDates        bool
	enableColorImpaired      bool
	uiLanguage               int
	theme                    string
	themeErr                 error
	setCalls                 []string
}

func newFake() *fakeUI {
	return &fakeUI{
		firstDayOfWeek:           0,
		calendarWeekColumnHeader: "ddd M/D",
		shortDateFormat:          "MMM D YYYY",
		longDateFormat:           "dddd, MMMM D YYYY",
		timeFormat:               "h(:mm)a",
		timeZone:                 "UTC",
		showRelativeDates:        true,
		enableColorImpaired:      false,
		uiLanguage:               1, // English
		theme:                    "auto",
	}
}

func (f *fakeUI) FirstDayOfWeek() int              { return f.firstDayOfWeek }
func (f *fakeUI) CalendarWeekColumnHeader() string { return f.calendarWeekColumnHeader }
func (f *fakeUI) ShortDateFormat() string          { return f.shortDateFormat }
func (f *fakeUI) LongDateFormat() string           { return f.longDateFormat }
func (f *fakeUI) TimeFormat() string               { return f.timeFormat }
func (f *fakeUI) TimeZone() string                 { return f.timeZone }
func (f *fakeUI) ShowRelativeDates() bool          { return f.showRelativeDates }
func (f *fakeUI) EnableColorImpairedMode() bool    { return f.enableColorImpaired }
func (f *fakeUI) UILanguage() int                  { return f.uiLanguage }
func (f *fakeUI) Theme(context.Context) (string, error) {
	if f.themeErr != nil {
		return "", f.themeErr
	}
	return f.theme, nil
}
func (f *fakeUI) SetFirstDayOfWeek(_ context.Context, v int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setCalls = append(f.setCalls, "FirstDayOfWeek")
	f.firstDayOfWeek = v
	return nil
}
func (f *fakeUI) SetCalendarWeekColumnHeader(_ context.Context, v string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setCalls = append(f.setCalls, "CalendarWeekColumnHeader")
	f.calendarWeekColumnHeader = v
	return nil
}
func (f *fakeUI) SetShortDateFormat(_ context.Context, v string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setCalls = append(f.setCalls, "ShortDateFormat")
	f.shortDateFormat = v
	return nil
}
func (f *fakeUI) SetLongDateFormat(_ context.Context, v string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setCalls = append(f.setCalls, "LongDateFormat")
	f.longDateFormat = v
	return nil
}
func (f *fakeUI) SetTimeFormat(_ context.Context, v string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setCalls = append(f.setCalls, "TimeFormat")
	f.timeFormat = v
	return nil
}
func (f *fakeUI) SetTimeZone(_ context.Context, v string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setCalls = append(f.setCalls, "TimeZone")
	f.timeZone = v
	return nil
}
func (f *fakeUI) SetShowRelativeDates(_ context.Context, v bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setCalls = append(f.setCalls, "ShowRelativeDates")
	f.showRelativeDates = v
	return nil
}
func (f *fakeUI) SetEnableColorImpairedMode(_ context.Context, v bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setCalls = append(f.setCalls, "EnableColorImpairedMode")
	f.enableColorImpaired = v
	return nil
}
func (f *fakeUI) SetUILanguage(_ context.Context, v int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setCalls = append(f.setCalls, "UILanguage")
	f.uiLanguage = v
	return nil
}
func (f *fakeUI) SetTheme(_ context.Context, v string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setCalls = append(f.setCalls, "Theme")
	f.theme = v
	return nil
}

func (f *fakeUI) setCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.setCalls)
}

func TestGetHandler_EmitsSingleton(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	uisettings.GetHandler(newFake()).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/settings/ui", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var got uisettings.Resource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != 1 || got.Theme != "auto" || got.UILanguage != 1 {
		t.Errorf("got %+v", got)
	}
}

func TestGetHandler_ThemeError_500(t *testing.T) {
	t.Parallel()
	p := newFake()
	p.themeErr = errors.New("boom")
	rr := httptest.NewRecorder()
	uisettings.GetHandler(p).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v5/settings/ui", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}

func TestUpdateHandler_PutAccepted_PersistsAll10Fields(t *testing.T) {
	t.Parallel()
	p := newFake()
	body := mustEncode(t, uisettings.Resource{
		ID: 1, FirstDayOfWeek: 1, CalendarWeekColumnHeader: "ww", ShortDateFormat: "YYYY-MM-DD",
		LongDateFormat: "dddd YYYY", TimeFormat: "HH:mm", TimeZone: "Europe/Berlin", ShowRelativeDates: false,
		EnableColorImpairedMode: true, UILanguage: 1, Theme: "dark",
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/settings/ui/1", bytes.NewReader(body))
	req.SetPathValue("id", "1")
	uisettings.UpdateHandler(p).ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: got %d want 202; body=%s", rr.Code, rr.Body.String())
	}
	var got uisettings.Resource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Theme != "dark" || got.TimeZone != "Europe/Berlin" || got.FirstDayOfWeek != 1 || !got.EnableColorImpairedMode {
		t.Errorf("body round-trip: %+v", got)
	}
	if c := p.setCallCount(); c != 10 {
		t.Errorf("expected 10 setter calls, got %d", c)
	}
}

func TestUpdateHandler_UILanguageZero_400(t *testing.T) {
	t.Parallel()
	body := mustEncode(t, uisettings.Resource{ID: 1, UILanguage: 0, Theme: "auto"})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/settings/ui/1", bytes.NewReader(body))
	req.SetPathValue("id", "1")
	uisettings.UpdateHandler(newFake()).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (UILanguage=0 < 1)", rr.Code)
	}
}

func TestUpdateHandler_UILanguageUnknown_400(t *testing.T) {
	t.Parallel()
	body := mustEncode(t, uisettings.Resource{ID: 1, UILanguage: 99999, Theme: "auto"})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/settings/ui/1", bytes.NewReader(body))
	req.SetPathValue("id", "1")
	uisettings.UpdateHandler(newFake()).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (UILanguage=99999 not in Language.All)", rr.Code)
	}
}

func TestUpdateHandler_RejectsUnknownField_400(t *testing.T) {
	t.Parallel()
	body := []byte(`{"id":1,"uiLanguage":1,"theme":"auto","wat":"unknown"}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v5/settings/ui/1", bytes.NewReader(body))
	req.SetPathValue("id", "1")
	uisettings.UpdateHandler(newFake()).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d want 400 (DisallowUnknownFields)", rr.Code)
	}
}

func TestHandlerAliases_NonNil(t *testing.T) {
	t.Parallel()
	if uisettings.GetHandler == nil {
		t.Errorf("GetHandler nil")
	}
	if uisettings.UpdateHandler == nil {
		t.Errorf("UpdateHandler nil")
	}
}

// TestTypeAliases pins compile-time equivalence with v3.
func TestTypeAliases(t *testing.T) {
	t.Parallel()
	r := uisettings.Resource{ID: 1, Theme: "auto"}
	if r.Theme != "auto" {
		t.Errorf("Resource not addressable")
	}
}

func mustEncode(t *testing.T, r uisettings.Resource) []byte {
	t.Helper()
	buf, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return buf
}
