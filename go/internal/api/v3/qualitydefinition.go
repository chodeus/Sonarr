package v3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/qualitydefinitions"
	"sonarr.tv/sonarr/internal/qualityprofiles"
	"sonarr.tv/sonarr/internal/validation"
)

// QualityDefinitionResource is the JSON shape produced by
// GET /api/v3/qualitydefinition. Mirrors
// `src/Sonarr.Api.V3/Qualities/QualityDefinitionResource.cs`.
//
// MinSize/MaxSize/PreferredSize use *float64 with omitempty because
// the C# fields are `double?` and C# emits null-valued doubles as
// "omitted" via STJson.WhenWritingNull. Modern Sonarr does not store
// these values on QualityDefinition anymore — migration 207 moved
// them to QualityProfile.Items — so the empty-DB response never
// includes them. The fields remain on the wire shape for forward
// compatibility with consumers that still read them.
//
// Field-source mapping (C# → Go):
//
//	id            → Decorated.ID         (auto-increment from seed pass)
//	quality       → Decorated.Quality    (full Quality DTO, source as camelCase string)
//	title         → Decorated.Title      (passthrough; defaults to Quality.Name)
//	weight        → Decorated.Weight     (decoration; from quality.DefaultDefinitions)
//	minSize/...   → nil for empty DB     (post-207 these live on QualityProfile)
type QualityDefinitionResource struct {
	ID            int             `json:"id"`
	Quality       qualityResource `json:"quality"`
	Title         string          `json:"title"`
	Weight        int             `json:"weight"`
	MinSize       *float64        `json:"minSize,omitempty"`
	MaxSize       *float64        `json:"maxSize,omitempty"`
	PreferredSize *float64        `json:"preferredSize,omitempty"`
}

// qualityResource is the inline Quality DTO that QualityDefinition
// embeds. Distinct from `quality.Quality` because the wire form emits
// Source as a camelCase string (via C# JsonStringEnumConverter +
// JsonNamingPolicy.CamelCase) whereas `quality.Quality` marshals the
// underlying int for the embedded-doc path. Phase 5 swaps to the
// string form at the REST edge only — the persisted shape stays int.
type qualityResource struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Source     string `json:"source"`
	Resolution int    `json:"resolution"`
}

// QualityDefinitionLister is the read side of qualitydefinitions
// the handler depends on. *qualitydefinitions.Service satisfies it
// in production; the contract test injects a fixture built from
// quality.DefaultDefinitions.
type QualityDefinitionLister interface {
	All(ctx context.Context) ([]qualitydefinitions.Decorated, error)
}

// QualityDefinitionHandler returns the http.Handler for
// GET /api/v3/qualitydefinition. Mirrors
// `QualityDefinitionController.GetAll` at
// `src/Sonarr.Api.V3/Qualities/QualityDefinitionController.cs:55-59`.
// The service already sorts by Weight ASC inside `All()` (matches the
// C# `.OrderBy(d => d.Weight)`), so the handler emits in service order.
func QualityDefinitionHandler(lister QualityDefinitionLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := lister.All(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("qualitydefinitions: %w", err), nil)
			return
		}
		out := make([]QualityDefinitionResource, len(rows))
		for i, row := range rows {
			out[i] = toQualityDefinitionResource(row)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

func toQualityDefinitionResource(d qualitydefinitions.Decorated) QualityDefinitionResource {
	return QualityDefinitionResource{
		ID:      d.ID,
		Quality: toQualityResource(d.Quality),
		Title:   d.Title,
		Weight:  d.Weight,
	}
}

func toQualityResource(q quality.Quality) qualityResource {
	return qualityResource{
		ID:         q.ID,
		Name:       q.Name,
		Source:     QualitySourceName(q.Source),
		Resolution: q.Resolution,
	}
}

// QualitySourceName mirrors C# `JsonStringEnumConverter(JsonNamingPolicy
// .CamelCase)` applied to QualitySource. CamelCase lower-cases the
// leading uppercase run, so "DVD" → "dvd" (not "dVD"), "WebRip" →
// "webRip", "BlurayRaw" → "blurayRaw". Exported so the future
// quality-related controllers can share the mapping without
// duplicating the table.
//
// Unknown enum values fall back to "unknown" to match the C# default
// (System.Text.Json renders unknown enum values as a digit string by
// default, but every value in QualitySource is covered).
func QualitySourceName(s quality.QualitySource) string {
	switch s {
	case quality.SourceUnknown:
		return "unknown"
	case quality.SourceTelevision:
		return "television"
	case quality.SourceTelevisionRaw:
		return "televisionRaw"
	case quality.SourceWeb:
		return "web"
	case quality.SourceWebRip:
		return "webRip"
	case quality.SourceDVD:
		return "dvd"
	case quality.SourceBluray:
		return "bluray"
	case quality.SourceBlurayRaw:
		return "blurayRaw"
	}
	return "unknown"
}

// ---------- 5.B7: PUT/{id} + bulk update + /limits ----------

// QualityDefinitionLimitsResource mirrors C#
// `src/Sonarr.Api.V3/Qualities/QualityDefinitionLimitsResource.cs`:
// `record QualityDefinitionLimitsResource(int Min, int Max)`. C#
// emits `{"min":0,"max":1000}` via STJson.PropertyNamingPolicy.CamelCase.
type QualityDefinitionLimitsResource struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

// qualityDefinitionLimits is the constant pair the C# tree exposes
// at `src/NzbDrone.Core/Qualities/QualityDefinitionLimits.cs:3-7`.
// Used by GET /qualitydefinition/limits. Lifted to a package-level
// var so tests can override if a future slice introduces dynamic
// bounds.
var qualityDefinitionLimits = QualityDefinitionLimitsResource{Min: 0, Max: 1000}

// QualityDefinitionWriter is the write surface for the CRUD handlers.
// *qualitydefinitions.Service satisfies it via GetByID/Update; only
// Update is exposed since QualityDefinition is a seed-defaults table
// (no Add, no Delete — rows are created/destroyed by EnsureDefaults
// and the migration system).
type QualityDefinitionWriter interface {
	GetByID(ctx context.Context, id int) (*qualitydefinitions.Decorated, error)
	Update(ctx context.Context, def *qualitydefinitions.Definition) error
}

// QualityProfileSizeLimitUpdater narrows the qualityprofiles service
// to the UpdateAllSizeLimits side-effect the PUT handlers fire when
// the body carries MinSize/MaxSize/PreferredSize. Post-migration-207
// the size limits live on QualityProfile.Items, not on the
// QualityDefinition row itself; updating a definition propagates the
// new size bounds to every profile.
type QualityProfileSizeLimitUpdater interface {
	UpdateAllSizeLimits(ctx context.Context, sizeLimits ...qualityprofiles.QualityProfileSizeLimit) error
}

// QualityDefinitionByIDHandler returns GET /api/v3/qualitydefinition/{id}.
// Mirrors `QualityDefinitionController.GetResourceById` at
// `src/Sonarr.Api.V3/Qualities/QualityDefinitionController.cs:50-53`.
func QualityDefinitionByIDHandler(svc QualityDefinitionWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseQualityDefinitionID(w, r)
		if !ok {
			return
		}
		d, err := svc.GetByID(r.Context(), id)
		if errors.Is(err, qualitydefinitions.ErrNotFound) {
			httperr.NotFound(w, fmt.Sprintf("qualitydefinition %d not found", id))
			return
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("qualitydefinitions: %w", err), nil)
			return
		}
		writeQualityDefinitionJSON(w, http.StatusOK, *d)
	})
}

// QualityDefinitionUpdateHandler returns PUT /api/v3/qualitydefinition/{id}.
// Mirrors `Update` at `:36-48`. Returns 202 + the persisted definition.
//
// When the body carries any of MinSize/MaxSize/PreferredSize the
// handler also fires QualityProfileSizeLimitUpdater.UpdateAllSizeLimits
// to propagate the change to every QualityProfile.Item — matches the
// C# `if (model.MinSize.HasValue || …) UpdateAllSizeLimits(...)`
// branch at controller `:42-45`.
func QualityDefinitionUpdateHandler(svc QualityDefinitionWriter, profiles QualityProfileSizeLimitUpdater) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseQualityDefinitionID(w, r)
		if !ok {
			return
		}
		body, err := decodeQualityDefinitionBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("qualitydefinition: malformed body: %v", err))
			return
		}
		body.ID = id
		def := &qualitydefinitions.Definition{
			ID:      body.ID,
			Quality: body.Quality.ID,
			Title:   body.Title,
		}
		if errs := validateQualityDefinition(body); !errs.OK() {
			httperr.WriteValidation(w, errs.Failures())
			return
		}
		err = svc.Update(r.Context(), def)
		if errors.Is(err, qualitydefinitions.ErrNotFound) {
			httperr.NotFound(w, fmt.Sprintf("qualitydefinition %d not found", id))
			return
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("qualitydefinitions: %w", err), nil)
			return
		}
		if body.MinSize != nil || body.MaxSize != nil || body.PreferredSize != nil {
			if pErr := profiles.UpdateAllSizeLimits(r.Context(), qualityprofiles.QualityProfileSizeLimit{
				Quality:       qualityFromResource(body.Quality),
				MinSize:       body.MinSize,
				MaxSize:       body.MaxSize,
				PreferredSize: body.PreferredSize,
			}); pErr != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("qualityprofiles: %w", pErr), nil)
				return
			}
		}
		// Re-read the updated definition (the Decorated projection rebuilds Title etc).
		d, err := svc.GetByID(r.Context(), id)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("qualitydefinitions: reload %d: %w", id, err), nil)
			return
		}
		writeQualityDefinitionJSON(w, http.StatusAccepted, *d)
	})
}

// QualityDefinitionUpdateManyHandler returns PUT /api/v3/qualitydefinition/update.
// Mirrors `UpdateMany` at `:61-81`. Body is `[]QualityDefinitionResource`;
// the handler updates each row in order, then collects all rows that
// carry MinSize/MaxSize/PreferredSize into a single UpdateAllSizeLimits
// call. Returns 202 + the final list view (re-read from the service so
// the response carries the canonical Decorated shape).
func QualityDefinitionUpdateManyHandler(svc QualityDefinitionWriter, lister QualityDefinitionLister, profiles QualityProfileSizeLimitUpdater) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []QualityDefinitionResource
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			httperr.BadRequest(w, fmt.Sprintf("qualitydefinition: malformed body: %v", err))
			return
		}
		// Per-row validation up front so partial-write failures don't
		// leave the store in a mixed state.
		errs := validation.New()
		for i, res := range body {
			rowErrs := validateQualityDefinition(&res)
			if !rowErrs.OK() {
				for _, f := range rowErrs.Failures() {
					errs.Add(fmt.Sprintf("[%d].%s", i, f.PropertyName), f.ErrorMessage)
				}
			}
		}
		if !errs.OK() {
			httperr.WriteValidation(w, errs.Failures())
			return
		}
		sizeLimits := make([]qualityprofiles.QualityProfileSizeLimit, 0, len(body))
		for _, res := range body {
			def := &qualitydefinitions.Definition{ID: res.ID, Quality: res.Quality.ID, Title: res.Title}
			if uErr := svc.Update(r.Context(), def); uErr != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("qualitydefinitions: update %d: %w", res.ID, uErr), nil)
				return
			}
			if res.MinSize != nil || res.MaxSize != nil || res.PreferredSize != nil {
				sizeLimits = append(sizeLimits, qualityprofiles.QualityProfileSizeLimit{
					Quality:       qualityFromResource(res.Quality),
					MinSize:       res.MinSize,
					MaxSize:       res.MaxSize,
					PreferredSize: res.PreferredSize,
				})
			}
		}
		if len(sizeLimits) > 0 {
			if pErr := profiles.UpdateAllSizeLimits(r.Context(), sizeLimits...); pErr != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("qualityprofiles: %w", pErr), nil)
				return
			}
		}
		// Re-read everything for the response (matches C#
		// `Accepted(_qualityDefinitionService.All().ToResource())` at :79-80).
		rows, err := lister.All(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("qualitydefinitions: %w", err), nil)
			return
		}
		out := make([]QualityDefinitionResource, len(rows))
		for i, row := range rows {
			out[i] = toQualityDefinitionResource(row)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

// QualityDefinitionLimitsHandler returns GET /api/v3/qualitydefinition/limits.
// Mirrors `GetLimits` at `:83-89`. Emits the constant {min:0, max:1000}
// pair from `src/NzbDrone.Core/Qualities/QualityDefinitionLimits.cs:3-7`.
func QualityDefinitionLimitsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(qualityDefinitionLimits)
		_, _ = w.Write(buf)
	})
}

// ---------- helpers ----------

func parseQualityDefinitionID(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.PathValue("id")
	if raw == "" {
		httperr.BadRequest(w, "qualitydefinition: missing id path parameter")
		return 0, false
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		httperr.BadRequest(w, fmt.Sprintf("qualitydefinition: invalid id %q", raw))
		return 0, false
	}
	return id, true
}

func decodeQualityDefinitionBody(r *http.Request) (*QualityDefinitionResource, error) {
	var t QualityDefinitionResource
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return nil, err
	}
	return &t, nil
}

// validateQualityDefinition ports the cheap subset of the C#
// QualityDefinitionResourceValidator (FluentValidation lives in
// `src/NzbDrone.Core/Qualities/QualityDefinitionResourceValidator.cs`
// but the rules there are mostly null-checks + size-bounds — the Go
// port enforces the same bounds inline):
//
//   - Title not empty (rejects whitespace-only via TrimSpace check)
//   - MinSize, MaxSize, PreferredSize each in [0, 1000] when set
//   - MinSize <= PreferredSize <= MaxSize when all three set
func validateQualityDefinition(res *QualityDefinitionResource) *validation.Errors {
	errs := validation.New()
	errs.Required("Title", res.Title)
	checkSize := func(name string, p *float64) {
		if p == nil {
			return
		}
		if *p < float64(qualityDefinitionLimits.Min) || *p > float64(qualityDefinitionLimits.Max) {
			errs.Add(name, fmt.Sprintf("%s must be between %d and %d", name, qualityDefinitionLimits.Min, qualityDefinitionLimits.Max))
		}
	}
	checkSize("MinSize", res.MinSize)
	checkSize("MaxSize", res.MaxSize)
	checkSize("PreferredSize", res.PreferredSize)
	if res.MinSize != nil && res.PreferredSize != nil && *res.MinSize > *res.PreferredSize {
		errs.Add("PreferredSize", "PreferredSize must be >= MinSize")
	}
	if res.PreferredSize != nil && res.MaxSize != nil && *res.PreferredSize > *res.MaxSize {
		errs.Add("MaxSize", "MaxSize must be >= PreferredSize")
	}
	return errs
}

// qualityFromResource is the inverse of toQualityResource. Looks up
// the full Quality taxonomy by ID so the persisted Quality carries
// canonical Source+Resolution from the registry.
func qualityFromResource(r qualityResource) quality.Quality {
	q, _ := quality.FindByID(r.ID)
	return q
}

func writeQualityDefinitionJSON(w http.ResponseWriter, status int, d qualitydefinitions.Decorated) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	buf, _ := json.Marshal(toQualityDefinitionResource(d))
	_, _ = w.Write(buf)
}
