package v3

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

// ---------- 5.B13b: PUT tests ----------

type fakeUIConfigWriter struct {
	*fakeUIConfig
	setFirst, setLang                               int
	setCalHdr, setShort, setLongStr, setTime, setTz string
	setRel, setColor                                bool
	setThemeStr                                     string
	calls                                           []string
}

func (f *fakeUIConfigWriter) SetFirstDayOfWeek(_ context.Context, v int) error {
	f.setFirst = v
	f.first = v
	f.calls = append(f.calls, "first")
	return nil
}
func (f *fakeUIConfigWriter) SetCalendarWeekColumnHeader(_ context.Context, v string) error {
	f.setCalHdr = v
	f.calHeader = v
	f.calls = append(f.calls, "cal")
	return nil
}
func (f *fakeUIConfigWriter) SetShortDateFormat(_ context.Context, v string) error {
	f.setShort = v
	f.short = v
	f.calls = append(f.calls, "short")
	return nil
}
func (f *fakeUIConfigWriter) SetLongDateFormat(_ context.Context, v string) error {
	f.setLongStr = v
	f.long = v
	f.calls = append(f.calls, "long")
	return nil
}
func (f *fakeUIConfigWriter) SetTimeFormat(_ context.Context, v string) error {
	f.setTime = v
	f.time = v
	f.calls = append(f.calls, "time")
	return nil
}
func (f *fakeUIConfigWriter) SetTimeZone(_ context.Context, v string) error {
	f.setTz = v
	f.tz = v
	f.calls = append(f.calls, "tz")
	return nil
}
func (f *fakeUIConfigWriter) SetShowRelativeDates(_ context.Context, v bool) error {
	f.setRel = v
	f.rel = v
	f.calls = append(f.calls, "rel")
	return nil
}
func (f *fakeUIConfigWriter) SetEnableColorImpairedMode(_ context.Context, v bool) error {
	f.setColor = v
	f.color = v
	f.calls = append(f.calls, "color")
	return nil
}
func (f *fakeUIConfigWriter) SetUILanguage(_ context.Context, v int) error {
	f.setLang = v
	f.lang = v
	f.calls = append(f.calls, "lang")
	return nil
}
func (f *fakeUIConfigWriter) SetTheme(_ context.Context, v string) error {
	f.setThemeStr = v
	f.theme = v
	f.calls = append(f.calls, "theme")
	return nil
}

func uiBody(uiLanguage int) string {
	return `{"id":1,"firstDayOfWeek":1,"calendarWeekColumnHeader":"ddd M/D","shortDateFormat":"MMM D YYYY","longDateFormat":"dddd, MMMM D YYYY","timeFormat":"h(:mm)a","timeZone":"UTC","showRelativeDates":true,"enableColorImpairedMode":false,"uiLanguage":` + strconvItoa(uiLanguage) + `,"theme":"dark"}`
}

func strconvItoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	pos := len(b)
	for n > 0 {
		pos--
		b[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}

func TestUIConfigUpdateHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeUIConfigWriter{fakeUIConfig: &fakeUIConfig{theme: "dark"}}
	rr := httptest.NewRecorder()
	req := strings.NewReader(uiBody(1))
	httpReq := httptest.NewRequest(http.MethodPut, "/api/v3/config/ui/1", req)
	httpReq.SetPathValue("id", "1")
	UIConfigUpdateHandler(w).ServeHTTP(rr, httpReq)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	wantCalls := []string{"first", "cal", "short", "long", "time", "tz", "rel", "color", "lang", "theme"}
	if len(w.calls) != len(wantCalls) {
		t.Fatalf("setter calls: got %v want %v", w.calls, wantCalls)
	}
	for i, c := range w.calls {
		if c != wantCalls[i] {
			t.Errorf("calls[%d]: got %q want %q", i, c, wantCalls[i])
		}
	}
	if w.setThemeStr != "dark" {
		t.Errorf("SetTheme not called with body value; got %q", w.setThemeStr)
	}
}

func TestUIConfigUpdateHandler_UILanguageBelow1_400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	httpReq := httptest.NewRequest(http.MethodPut, "/api/v3/config/ui/1", strings.NewReader(uiBody(0)))
	httpReq.SetPathValue("id", "1")
	UIConfigUpdateHandler(&fakeUIConfigWriter{fakeUIConfig: &fakeUIConfig{}}).ServeHTTP(rr, httpReq)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "UI Language value cannot be less than 1") {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestUIConfigUpdateHandler_InvalidUILanguage_400(t *testing.T) {
	t.Parallel()
	// 9999 is not a valid Language.ID — must reject.
	rr := httptest.NewRecorder()
	httpReq := httptest.NewRequest(http.MethodPut, "/api/v3/config/ui/1", strings.NewReader(uiBody(9999)))
	httpReq.SetPathValue("id", "1")
	UIConfigUpdateHandler(&fakeUIConfigWriter{fakeUIConfig: &fakeUIConfig{}}).ServeHTTP(rr, httpReq)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Invalid UI Language value") {
		t.Errorf("body: %s", rr.Body.String())
	}
}
