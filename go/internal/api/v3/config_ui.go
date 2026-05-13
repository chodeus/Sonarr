package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// UIConfigResource mirrors
// `src/Sonarr.Api.V3/Config/UiConfigResource.cs`. Ten fields plus the
// singleton Id.
//
// Field-source mapping (C# → Go):
//
//	id                       → constant 1 (singleton convention)
//	firstDayOfWeek           → config.Provider.FirstDayOfWeek
//	calendarWeekColumnHeader → config.Provider.CalendarWeekColumnHeader
//	shortDateFormat          → config.Provider.ShortDateFormat
//	longDateFormat           → config.Provider.LongDateFormat
//	timeFormat               → config.Provider.TimeFormat
//	timeZone                 → config.Provider.TimeZone
//	showRelativeDates        → config.Provider.ShowRelativeDates
//	enableColorImpairedMode  → config.Provider.EnableColorImpairedMode
//	uiLanguage               → config.Provider.UILanguage
//	theme                    → configfile.Provider.Theme (xml-backed)
type UIConfigResource struct {
	ID                       int    `json:"id"`
	FirstDayOfWeek           int    `json:"firstDayOfWeek"`
	CalendarWeekColumnHeader string `json:"calendarWeekColumnHeader"`
	ShortDateFormat          string `json:"shortDateFormat"`
	LongDateFormat           string `json:"longDateFormat"`
	TimeFormat               string `json:"timeFormat"`
	TimeZone                 string `json:"timeZone"`
	ShowRelativeDates        bool   `json:"showRelativeDates"`
	EnableColorImpairedMode  bool   `json:"enableColorImpairedMode"`
	UILanguage               int    `json:"uiLanguage"`
	Theme                    string `json:"theme"`
}

// UIConfigGetter is the read surface the handler depends on. C#
// merges fields from IConfigService + IConfigFileProvider — the Go
// port collapses them into a single getter interface that both
// providers compose into.
type UIConfigGetter interface {
	FirstDayOfWeek() int
	CalendarWeekColumnHeader() string
	ShortDateFormat() string
	LongDateFormat() string
	TimeFormat() string
	TimeZone() string
	ShowRelativeDates() bool
	EnableColorImpairedMode() bool
	UILanguage() int
	Theme(ctx context.Context) (string, error)
}

// UIConfigHandler returns the http.Handler for GET /api/v3/config/ui.
// Mirrors `UiConfigController` — singleton with Id=1.
func UIConfigHandler(getter UIConfigGetter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		theme, err := getter.Theme(r.Context())
		if err != nil {
			http.Error(w, fmt.Sprintf("config/ui: %v", err), http.StatusInternalServerError)
			return
		}
		res := UIConfigResource{
			ID:                       1,
			FirstDayOfWeek:           getter.FirstDayOfWeek(),
			CalendarWeekColumnHeader: getter.CalendarWeekColumnHeader(),
			ShortDateFormat:          getter.ShortDateFormat(),
			LongDateFormat:           getter.LongDateFormat(),
			TimeFormat:               getter.TimeFormat(),
			TimeZone:                 getter.TimeZone(),
			ShowRelativeDates:        getter.ShowRelativeDates(),
			EnableColorImpairedMode:  getter.EnableColorImpairedMode(),
			UILanguage:               getter.UILanguage(),
			Theme:                    theme,
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(res)
		_, _ = w.Write(buf)
	})
}
