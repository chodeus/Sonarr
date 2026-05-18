package v3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/namingconfig"
	"sonarr.tv/sonarr/internal/validation"
)

// NamingConfigResource mirrors
// `src/Sonarr.Api.V3/Config/NamingConfigResource.cs`. Twelve-field
// passthrough — the persisted namingconfig.Config carries the same
// shape with camelCase JSON tags ready for wire emission. The
// resource type exists so future controller-only fields (e.g.
// computed sample-output strings) can land without touching the
// persistence struct.
//
// Field-source mapping (C# → Go): every field is a direct
// passthrough from namingconfig.Config.{ID,MultiEpisodeStyle,...}.
type NamingConfigResource struct {
	ID                           int    `json:"id"`
	MultiEpisodeStyle            int    `json:"multiEpisodeStyle"`
	RenameEpisodes               bool   `json:"renameEpisodes"`
	StandardEpisodeFormat        string `json:"standardEpisodeFormat"`
	DailyEpisodeFormat           string `json:"dailyEpisodeFormat"`
	SeasonFolderFormat           string `json:"seasonFolderFormat"`
	SeriesFolderFormat           string `json:"seriesFolderFormat"`
	AnimeEpisodeFormat           string `json:"animeEpisodeFormat"`
	ReplaceIllegalCharacters     bool   `json:"replaceIllegalCharacters"`
	SpecialsFolderFormat         string `json:"specialsFolderFormat"`
	ColonReplacementFormat       int    `json:"colonReplacementFormat"`
	CustomColonReplacementFormat string `json:"customColonReplacementFormat"`
}

// NamingConfigGetter is the read side the handler depends on.
// *namingconfig.Service.GetConfig satisfies it.
type NamingConfigGetter interface {
	GetConfig(ctx context.Context) (*namingconfig.Config, error)
}

// NamingConfigHandler returns the http.Handler for
// GET /api/v3/config/naming. Mirrors
// `NamingConfigController.GetNamingConfig` —
// the C# RestController<NamingConfigResource> base resolves the
// singleton row via `GetResourceById(1)`, which forwards to the
// service's Get. The Go port collapses the GetResourceById layer
// since this is the only call site.
func NamingConfigHandler(getter NamingConfigGetter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg, err := getter.GetConfig(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("namingconfig: %w", err), nil)
			return
		}
		res := NamingConfigResource{
			ID:                           cfg.ID,
			MultiEpisodeStyle:            cfg.MultiEpisodeStyle,
			RenameEpisodes:               cfg.RenameEpisodes,
			StandardEpisodeFormat:        cfg.StandardEpisodeFormat,
			DailyEpisodeFormat:           cfg.DailyEpisodeFormat,
			SeasonFolderFormat:           cfg.SeasonFolderFormat,
			SeriesFolderFormat:           cfg.SeriesFolderFormat,
			AnimeEpisodeFormat:           cfg.AnimeEpisodeFormat,
			ReplaceIllegalCharacters:     cfg.ReplaceIllegalCharacters,
			SpecialsFolderFormat:         cfg.SpecialsFolderFormat,
			ColonReplacementFormat:       cfg.ColonReplacementFormat,
			CustomColonReplacementFormat: cfg.CustomColonReplacementFormat,
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(res)
		_, _ = w.Write(buf)
	})
}

// ---------- 5.B12: PUT + /examples ----------

// NamingExampleResource is the JSON shape produced by
// GET /api/v3/config/naming/examples. Mirrors C# at
// `src/Sonarr.Api.V3/Config/NamingExampleResource.cs`. Every field
// is a string OR null on the wire (null when the corresponding
// format fails validation or is empty); modelled here as plain
// strings with `omitempty` so empty values drop the key the same
// way STJson.WhenWritingNull does.
type NamingExampleResource struct {
	SingleEpisodeExample     string `json:"singleEpisodeExample,omitempty"`
	MultiEpisodeExample      string `json:"multiEpisodeExample,omitempty"`
	DailyEpisodeExample      string `json:"dailyEpisodeExample,omitempty"`
	AnimeEpisodeExample      string `json:"animeEpisodeExample,omitempty"`
	AnimeMultiEpisodeExample string `json:"animeMultiEpisodeExample,omitempty"`
	SeriesFolderExample      string `json:"seriesFolderExample,omitempty"`
	SeasonFolderExample      string `json:"seasonFolderExample,omitempty"`
	SpecialsFolderExample    string `json:"specialsFolderExample,omitempty"`
}

// NamingConfigWriter is the write surface — Save is the singleton
// upsert pattern (the namingconfig table holds one row). *namingconfig.
// Service satisfies it via GetConfig + Save.
type NamingConfigWriter interface {
	NamingConfigGetter
	Save(ctx context.Context, config *namingconfig.Config) error
}

// NamingConfigUpdateHandler returns PUT /api/v3/config/naming/{id}.
// Mirrors `NamingConfigController.UpdateNamingConfig` at
// `src/Sonarr.Api.V3/Config/NamingConfigController.cs:56-65`. The
// path id is ignored (the table has one row); the body's Id wins —
// matches C# `Save(resource.ToModel())` which doesn't consult the
// URL id either. Returns 202 + the persisted body.
//
// Validation chain (subset of C# SharedValidator at :32-39):
//   - MultiEpisodeStyle in [0, 5]
//   - ColonReplacementFormat in [0, 4] (the enum range; 4 = Custom)
//   - When ColonReplacementFormat == 4 (Custom), CustomColonReplacementFormat
//     must be non-empty
//
// DEFERRED: the per-format token validators (ValidEpisodeFormat,
// ValidDailyEpisodeFormat, ValidAnimeEpisodeFormat,
// ValidSeriesFolderFormat, ValidSeasonFolderFormat,
// ValidSpecialsFolderFormat). Each runs the C# filename builder
// against sample data and checks for required token presence + no
// invalid path chars. Porting those needs the IFilenameSampleService
// equivalent which the organizer package hasn't built; follow-up
// slice. Service.Save will still accept invalid formats today; the
// gap surfaces only when the format actually fails to render.
func NamingConfigUpdateHandler(svc NamingConfigWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeNamingConfigBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("namingconfig: malformed body: %v", err))
			return
		}
		if errs := validateNamingConfig(body); !errs.OK() {
			httperr.WriteValidation(w, errs.Failures())
			return
		}
		model := &namingconfig.Config{
			ID:                           body.ID,
			MultiEpisodeStyle:            body.MultiEpisodeStyle,
			RenameEpisodes:               body.RenameEpisodes,
			StandardEpisodeFormat:        body.StandardEpisodeFormat,
			DailyEpisodeFormat:           body.DailyEpisodeFormat,
			SeasonFolderFormat:           body.SeasonFolderFormat,
			SeriesFolderFormat:           body.SeriesFolderFormat,
			AnimeEpisodeFormat:           body.AnimeEpisodeFormat,
			ReplaceIllegalCharacters:     body.ReplaceIllegalCharacters,
			SpecialsFolderFormat:         body.SpecialsFolderFormat,
			ColonReplacementFormat:       body.ColonReplacementFormat,
			CustomColonReplacementFormat: body.CustomColonReplacementFormat,
		}
		if err := svc.Save(r.Context(), model); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("namingconfig: %w", err), nil)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		buf, _ := json.Marshal(toNamingConfigResource(model))
		_, _ = w.Write(buf)
	})
}

// NamingConfigExamplesHandler returns GET /api/v3/config/naming/examples.
// Mirrors `NamingConfigController.GetExamples` at `:67-117`. C# accepts
// the format strings as query parameters (?standardEpisodeFormat=...
// etc) and renders sample filenames using IFilenameSampleService.
//
// Body shape: NamingExampleResource with one string-or-null field per
// example category. C# returns null when the format fails validation;
// the Go port returns nil/omitted for the same case.
//
// DEFERRED: the actual rendering. The Go organizer package has the
// underlying BuildFileName/BuildFilePath primitives, but the C#
// IFilenameSampleService pre-builds reference Episode/Series/File
// fixtures (with hand-picked titles, qualities, languages, etc.)
// against which to render the format strings. Porting those
// fixtures + the per-format validator chain is significant work
// and is gated on the per-format validator slice. Until then, this
// handler returns an empty NamingExampleResource (all fields
// omitted) which matches the C# null-on-validation-failure shape —
// the React UI degrades gracefully to "no examples available"
// rather than showing stale rendered strings.
func NamingConfigExamplesHandler(svc NamingConfigGetter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(NamingExampleResource{})
		_, _ = w.Write(buf)
	})
}

// ---------- helpers ----------

func decodeNamingConfigBody(r *http.Request) (*NamingConfigResource, error) {
	var t NamingConfigResource
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return nil, err
	}
	return &t, nil
}

// validateNamingConfig ports the cheap subset of the C#
// SharedValidator chain (range + conditional checks); the per-format
// token validators are deferred (see NamingConfigUpdateHandler).
func validateNamingConfig(res *NamingConfigResource) *validation.Errors {
	errs := validation.New()
	errs.Range("MultiEpisodeStyle", res.MultiEpisodeStyle, 0, 5)
	// ColonReplacementFormat enum values (per C# ColonReplacementFormat):
	// 0 = Delete, 1 = Dash, 2 = SpaceDash, 3 = SpaceDashSpace, 4 = Custom.
	// Anything else is invalid.
	errs.Range("ColonReplacementFormat", res.ColonReplacementFormat, 0, 4)
	if res.ColonReplacementFormat == 4 && res.CustomColonReplacementFormat == "" {
		errs.Add("CustomColonReplacementFormat", "Custom Colon Replacement Format must be provided when ColonReplacementFormat is Custom")
	}
	return errs
}

func toNamingConfigResource(c *namingconfig.Config) NamingConfigResource {
	return NamingConfigResource{
		ID:                           c.ID,
		MultiEpisodeStyle:            c.MultiEpisodeStyle,
		RenameEpisodes:               c.RenameEpisodes,
		StandardEpisodeFormat:        c.StandardEpisodeFormat,
		DailyEpisodeFormat:           c.DailyEpisodeFormat,
		SeasonFolderFormat:           c.SeasonFolderFormat,
		SeriesFolderFormat:           c.SeriesFolderFormat,
		AnimeEpisodeFormat:           c.AnimeEpisodeFormat,
		ReplaceIllegalCharacters:     c.ReplaceIllegalCharacters,
		SpecialsFolderFormat:         c.SpecialsFolderFormat,
		ColonReplacementFormat:       c.ColonReplacementFormat,
		CustomColonReplacementFormat: c.CustomColonReplacementFormat,
	}
}

// errorsNamingConfig — unused right now but reserved; the
// per-format validator slice may want to wrap the underlying
// ValidationException via this helper.
var _ = errors.New
