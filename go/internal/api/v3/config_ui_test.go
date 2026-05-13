package v3

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeUIConfig struct {
	first       int
	calHeader   string
	short, long string
	time, tz    string
	rel, color  bool
	lang        int
	theme       string
	themeErr    error
}

func (f *fakeUIConfig) FirstDayOfWeek() int                   { return f.first }
func (f *fakeUIConfig) CalendarWeekColumnHeader() string      { return f.calHeader }
func (f *fakeUIConfig) ShortDateFormat() string               { return f.short }
func (f *fakeUIConfig) LongDateFormat() string                { return f.long }
func (f *fakeUIConfig) TimeFormat() string                    { return f.time }
func (f *fakeUIConfig) TimeZone() string                      { return f.tz }
func (f *fakeUIConfig) ShowRelativeDates() bool               { return f.rel }
func (f *fakeUIConfig) EnableColorImpairedMode() bool         { return f.color }
func (f *fakeUIConfig) UILanguage() int                       { return f.lang }
func (f *fakeUIConfig) Theme(context.Context) (string, error) { return f.theme, f.themeErr }

func TestUIConfigHandler_Defaults(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	UIConfigHandler(&fakeUIConfig{
		first: 1, calHeader: "ddd M/D",
		short: "MMM D YYYY", long: "dddd, MMMM D YYYY",
		time: "h(:mm)a", tz: "",
		rel: true, color: false,
		lang: 1, theme: "auto",
	}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/config/ui", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rr.Code)
	}
	var got UIConfigResource
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := UIConfigResource{
		ID:                       1,
		FirstDayOfWeek:           1,
		CalendarWeekColumnHeader: "ddd M/D",
		ShortDateFormat:          "MMM D YYYY",
		LongDateFormat:           "dddd, MMMM D YYYY",
		TimeFormat:               "h(:mm)a",
		TimeZone:                 "",
		ShowRelativeDates:        true,
		EnableColorImpairedMode:  false,
		UILanguage:               1,
		Theme:                    "auto",
	}
	if got != want {
		t.Errorf("body:\n got %+v\nwant %+v", got, want)
	}
}

func TestUIConfigHandler_ThemeError(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	UIConfigHandler(&fakeUIConfig{themeErr: errors.New("boom")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/config/ui", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}
