package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/validation"
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
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("config/ui: %w", err), nil)
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

// ---------- 5.B13b: PUT ----------

// UIConfigWriter is the write surface — extends UIConfigGetter with
// per-field setters. The Go *config.Provider satisfies the DB-backed
// setters; the *configfile.Provider satisfies SetTheme (XML-backed).
// The composite uiConfigGetter adapter in host_wiring.go wraps both.
type UIConfigWriter interface {
	UIConfigGetter
	SetFirstDayOfWeek(ctx context.Context, v int) error
	SetCalendarWeekColumnHeader(ctx context.Context, v string) error
	SetShortDateFormat(ctx context.Context, v string) error
	SetLongDateFormat(ctx context.Context, v string) error
	SetTimeFormat(ctx context.Context, v string) error
	SetTimeZone(ctx context.Context, v string) error
	SetShowRelativeDates(ctx context.Context, v bool) error
	SetEnableColorImpairedMode(ctx context.Context, v bool) error
	SetUILanguage(ctx context.Context, v int) error
	SetTheme(ctx context.Context, v string) error
}

// UIConfigUpdateHandler returns PUT /api/v3/config/ui/{id}.
// Mirrors `UiConfigController.SaveConfig` at
// `src/Sonarr.Api.V3/Config/UiConfigController.cs:34-45` — the C#
// override saves the dictionary to BOTH ConfigFileProvider (XML, for
// Theme) AND ConfigService (DB, for everything else). The Go port
// composes both via the UIConfigWriter interface.
//
// Validation chain (C# SharedValidator at :21-31):
//   - UILanguage >= 1
//   - UILanguage must exist in Language.All
func UIConfigUpdateHandler(svc UIConfigWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body UIConfigResource
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			httperr.BadRequest(w, fmt.Sprintf("config/ui: malformed body: %v", err))
			return
		}
		if errs := validateUIConfig(&body); !errs.OK() {
			httperr.WriteValidation(w, errs.Failures())
			return
		}
		ctx := r.Context()
		type setter struct {
			name string
			fn   func() error
		}
		// Per-field application. The C# pipeline uses reflection over
		// the resource's properties; Go uses explicit setters. The
		// order doesn't affect correctness (no inter-field dependencies)
		// but matches the resource declaration order for readability.
		setters := []setter{
			{"FirstDayOfWeek", func() error { return svc.SetFirstDayOfWeek(ctx, body.FirstDayOfWeek) }},
			{"CalendarWeekColumnHeader", func() error { return svc.SetCalendarWeekColumnHeader(ctx, body.CalendarWeekColumnHeader) }},
			{"ShortDateFormat", func() error { return svc.SetShortDateFormat(ctx, body.ShortDateFormat) }},
			{"LongDateFormat", func() error { return svc.SetLongDateFormat(ctx, body.LongDateFormat) }},
			{"TimeFormat", func() error { return svc.SetTimeFormat(ctx, body.TimeFormat) }},
			{"TimeZone", func() error { return svc.SetTimeZone(ctx, body.TimeZone) }},
			{"ShowRelativeDates", func() error { return svc.SetShowRelativeDates(ctx, body.ShowRelativeDates) }},
			{"EnableColorImpairedMode", func() error { return svc.SetEnableColorImpairedMode(ctx, body.EnableColorImpairedMode) }},
			{"UILanguage", func() error { return svc.SetUILanguage(ctx, body.UILanguage) }},
			{"Theme", func() error { return svc.SetTheme(ctx, body.Theme) }},
		}
		for _, s := range setters {
			if err := s.fn(); err != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("config/ui: set %s: %w", s.name, err), nil)
				return
			}
		}
		theme, err := svc.Theme(ctx)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("config/ui: reload theme: %w", err), nil)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		buf, _ := json.Marshal(UIConfigResource{
			ID:                       1,
			FirstDayOfWeek:           svc.FirstDayOfWeek(),
			CalendarWeekColumnHeader: svc.CalendarWeekColumnHeader(),
			ShortDateFormat:          svc.ShortDateFormat(),
			LongDateFormat:           svc.LongDateFormat(),
			TimeFormat:               svc.TimeFormat(),
			TimeZone:                 svc.TimeZone(),
			ShowRelativeDates:        svc.ShowRelativeDates(),
			EnableColorImpairedMode:  svc.EnableColorImpairedMode(),
			UILanguage:               svc.UILanguage(),
			Theme:                    theme,
		})
		_, _ = w.Write(buf)
	})
}

func validateUIConfig(res *UIConfigResource) *validation.Errors {
	errs := validation.New()
	if res.UILanguage < 1 {
		errs.Add("UILanguage", "The UI Language value cannot be less than 1")
		return errs
	}
	if _, ok := language.FindByID(res.UILanguage); !ok {
		errs.Add("UILanguage", "Invalid UI Language value")
	}
	return errs
}
