package v3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/qualityprofiles"
	"sonarr.tv/sonarr/internal/validation"
)

// QualityProfileResource is the JSON shape produced by
// GET /api/v3/qualityprofile. Mirrors
// `src/Sonarr.Api.V3/Profiles/Quality/QualityProfileResource.cs` —
// the wire-shape contract is the C# RestResource base + the explicit
// properties on the resource class. The contract test pins this
// against the recorded `v3.qualityprofile` golden (6 seeded
// profiles).
//
// Field-source mapping (C# → Go):
//
//	id                    → Profile.ID         (RestResource base; omitempty)
//	name                  → Profile.Name
//	upgradeAllowed        → Profile.UpgradeAllowed
//	cutoff                → Profile.Cutoff
//	items                 → Profile.Items      (parsed via qualityprofiles.ParseItems)
//	minFormatScore        → Profile.MinFormatScore
//	cutoffFormatScore     → Profile.CutoffFormatScore
//	minUpgradeFormatScore → Profile.MinUpgradeFormatScore
//	formatItems           → Profile.FormatItems + customformats.Service.All (joined for Name)
//
// JSON ordering is alphabetized by the contract-test normalizer
// (runner.Run → normalize.JSON round-trips through map[string]any),
// so Go struct field order doesn't matter for byte-eq. The C# tree
// declares fields in property order and System.Text.Json preserves
// that; the normalizer hides the difference.
type QualityProfileResource struct {
	ID                    int                          `json:"id,omitempty"`
	Name                  string                       `json:"name"`
	UpgradeAllowed        bool                         `json:"upgradeAllowed"`
	Cutoff                int                          `json:"cutoff"`
	Items                 []QualityProfileItemResource `json:"items"`
	MinFormatScore        int                          `json:"minFormatScore"`
	CutoffFormatScore     int                          `json:"cutoffFormatScore"`
	MinUpgradeFormatScore int                          `json:"minUpgradeFormatScore"`
	FormatItems           []ProfileFormatItemResource  `json:"formatItems"`
}

// QualityProfileItemResource is the per-entry view inside
// `QualityProfileResource.Items`. Mirrors
// `QualityProfileQualityItemResource` in the same C# file.
//
// Two shapes share the type, matching the C# polymorphism via the
// JsonIgnore(WhenWritingDefault/Null) defaults configured on STJson:
//
//   - Quality entry: `Quality` is non-nil (full taxonomy object on
//     the wire), `Id`/`Name` are zero/empty (omitted), MinSize +
//     MaxSize + PreferredSize carry the per-quality size limits.
//   - Group entry: `Quality` is nil (omitted), `Id` is the synthetic
//     group id (≥1000, omitted-when-zero per the RestResource Id
//     attribute), `Name` is the group label, `Items` lists the
//     nested member qualities, MinSize/MaxSize/PreferredSize are nil
//     (omitted — groups don't store per-quality size limits).
//
// Pointer types for MinSize/MaxSize/PreferredSize so omitempty
// distinguishes "0 size" from "no size set" (the C# `double?`
// semantic). `Quality *qualityResource` pointer for the same reason.
type QualityProfileItemResource struct {
	ID            int                          `json:"id,omitempty"`
	Name          string                       `json:"name,omitempty"`
	Quality       *qualityResource             `json:"quality,omitempty"`
	Items         []QualityProfileItemResource `json:"items"`
	Allowed       bool                         `json:"allowed"`
	MinSize       *float64                     `json:"minSize,omitempty"`
	MaxSize       *float64                     `json:"maxSize,omitempty"`
	PreferredSize *float64                     `json:"preferredSize,omitempty"`
}

// ProfileFormatItemResource is the per-CustomFormat entry inside
// `QualityProfileResource.FormatItems`. Mirrors
// `ProfileFormatItemResource` in the same C# file. Carries the
// CustomFormat's Id + Name (joined from `customformats.Service.All`)
// plus the user-assigned score.
type ProfileFormatItemResource struct {
	Format int    `json:"format"`
	Name   string `json:"name"`
	Score  int    `json:"score"`
}

// QualityProfileLister narrows the qualityprofiles service surface
// to the read path the handler needs. *qualityprofiles.Service
// satisfies it via the bound-method on its All(ctx).
type QualityProfileLister interface {
	All(ctx context.Context) ([]qualityprofiles.Profile, error)
}

// QualityProfileSchemaLister narrows the qualityprofiles service to
// the GetDefaultProfile call /api/v3/qualityprofile/schema needs.
// *qualityprofiles.Service satisfies it via GetDefaultProfile —
// signature takes (name, *Quality, ...Quality). The schema handler
// always passes ("", nil) so the variadic tail is empty.
type QualityProfileSchemaLister interface {
	GetDefaultProfile(ctx context.Context, name string, cutoff *quality.Quality, allowed ...quality.Quality) (*qualityprofiles.Profile, error)
}

// CustomFormatNameLister narrows the customformats service to the
// FormatItems Name-join surface. *customformats.Service satisfies
// it via All(ctx) ([]*customformats.Format, error).
type CustomFormatNameLister interface {
	All(ctx context.Context) ([]*customformats.Format, error)
}

// QualityProfileListHandler returns the http.Handler for
// GET /api/v3/qualityprofile. Mirrors
// `QualityProfileController.GetAll` at
// `src/Sonarr.Api.V3/Profiles/Quality/QualityProfileController.cs:79-83`.
//
// The handler reads every Profile, parses Items + FormatItems from
// their embedded-JSON columns, joins FormatItem Names from the
// CustomFormat lookup table, and emits the alphabetized resource
// list. The C# call site relies on `Service.All` returning rows in
// insertion order (seed-defaults order: Any, SD, HD-720p, HD-1080p,
// Ultra-HD, HD - 720p/1080p) — the Go port preserves that via the
// repo's `OrderBy: "Id"` setting plus the autoincrement insert.
func QualityProfileListHandler(profiles QualityProfileLister, formats CustomFormatNameLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := profiles.All(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("qualityprofiles: %w", err), nil)
			return
		}
		formatRows, err := formats.All(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("customformats: %w", err), nil)
			return
		}
		formatNames := make(map[int]string, len(formatRows))
		for _, f := range formatRows {
			if f != nil {
				formatNames[f.ID] = f.Name
			}
		}
		out := make([]QualityProfileResource, 0, len(rows))
		for _, p := range rows {
			res, err := toQualityProfileResource(p, formatNames)
			if err != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("qualityprofiles: parse %d: %w", p.ID, err), nil)
				return
			}
			out = append(out, res)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

func toQualityProfileResource(p qualityprofiles.Profile, formatNames map[int]string) (QualityProfileResource, error) {
	items, err := p.ParsedItems()
	if err != nil {
		return QualityProfileResource{}, err
	}
	formatItems, err := p.ParsedFormatItems()
	if err != nil {
		return QualityProfileResource{}, err
	}
	resItems := make([]QualityProfileItemResource, 0, len(items))
	for _, it := range items {
		resItems = append(resItems, toQualityProfileItemResource(it))
	}
	resFormats := make([]ProfileFormatItemResource, 0, len(formatItems))
	for _, fi := range formatItems {
		resFormats = append(resFormats, ProfileFormatItemResource{
			Format: fi.FormatID,
			Name:   formatNames[fi.FormatID],
			Score:  fi.Score,
		})
	}
	return QualityProfileResource{
		ID:                    p.ID,
		Name:                  p.Name,
		UpgradeAllowed:        p.UpgradeAllowed,
		Cutoff:                p.Cutoff,
		Items:                 resItems,
		MinFormatScore:        p.MinFormatScore,
		CutoffFormatScore:     p.CutoffFormatScore,
		MinUpgradeFormatScore: p.MinUpgradeFormatScore,
		FormatItems:           resFormats,
	}, nil
}

// QualityProfileSchemaHandler returns the http.Handler for
// GET /api/v3/qualityprofile/schema. Mirrors
// `QualityProfileSchemaController.GetSchema` at
// `src/Sonarr.Api.V3/Profiles/Quality/QualityProfileSchemaController.cs:18-22`
// — emits a single template QualityProfileResource produced by
// `Service.GetDefaultProfile("", nil)` (no cutoff, no allowed
// qualities). The result is what the UI uses to build a brand-new
// profile against; allowed defaults to false everywhere, MinSize/
// MaxSize/PreferredSize default to the runtime size table.
//
// The handler shares the FormatProvider plumbing with the list
// handler — FormatItems on the schema response are seeded with
// Score=0 for every known CustomFormat (the Name-join read happens
// here too so a populated CustomFormat row's Name flows through).
//
// Output is a SINGLE QualityProfileResource (object, not list) — the
// C# controller returns `QualityProfileResource`, not `List<...>`.
func QualityProfileSchemaHandler(schema QualityProfileSchemaLister, formats CustomFormatNameLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		profile, err := schema.GetDefaultProfile(r.Context(), "", nil)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("qualityprofile/schema: %w", err), nil)
			return
		}
		formatRows, err := formats.All(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("customformats: %w", err), nil)
			return
		}
		formatNames := make(map[int]string, len(formatRows))
		for _, f := range formatRows {
			if f != nil {
				formatNames[f.ID] = f.Name
			}
		}
		res, err := toQualityProfileResource(*profile, formatNames)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("qualityprofile/schema: project: %w", err), nil)
			return
		}
		// The schema response carries no Id (the profile is unpersisted);
		// the omitempty tag on ID handles that automatically. Likewise
		// UpgradeAllowed defaults to false (matches the C# value).
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(res)
		_, _ = w.Write(buf)
	})
}

// toQualityProfileItemResource projects one parsed QualityItem into
// the wire shape. Group vs quality discrimination uses the same
// `IsGroup()` check as the parsed type (Quality.ID == 0 + len(Items)
// > 0). Group entries elide Quality + size pointers; quality entries
// elide Id + Name (left at their zero values so omitempty drops
// them).
func toQualityProfileItemResource(it qualityprofiles.QualityItem) QualityProfileItemResource {
	children := make([]QualityProfileItemResource, 0, len(it.Items))
	for _, c := range it.Items {
		children = append(children, toQualityProfileItemResource(c))
	}
	out := QualityProfileItemResource{
		ID:      it.ID,
		Name:    it.Name,
		Items:   children,
		Allowed: it.Allowed,
	}
	if !it.IsGroup() {
		q := toQualityResource(it.Quality)
		out.Quality = &q
		out.MinSize = it.MinSize
		out.MaxSize = it.MaxSize
		out.PreferredSize = it.PreferredSize
	}
	return out
}

// ---------- 5.B5: CRUD ----------

// QualityProfileWriter is the write surface for the CRUD handlers.
// *qualityprofiles.Service satisfies it via All/Get/Add/Update/Delete.
// QualityProfileController in C# extends RestController<T> (NOT
// RestControllerWithSignalR), so no SignalR fan-out is wired.
type QualityProfileWriter interface {
	All(ctx context.Context) ([]qualityprofiles.Profile, error)
	Get(ctx context.Context, id int) (*qualityprofiles.Profile, error)
	Add(ctx context.Context, p *qualityprofiles.Profile) (*qualityprofiles.Profile, error)
	Update(ctx context.Context, p *qualityprofiles.Profile) error
	Delete(ctx context.Context, id int) error
}

// QualityProfileByIDHandler returns GET /api/v3/qualityprofile/{id}.
// Mirrors `QualityProfileController.GetResourceById` at
// `src/Sonarr.Api.V3/Profiles/Quality/QualityProfileController.cs:75-78`.
func QualityProfileByIDHandler(profiles QualityProfileWriter, formats CustomFormatNameLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseQualityProfileID(w, r)
		if !ok {
			return
		}
		p, err := profiles.Get(r.Context(), id)
		if errors.Is(err, qualityprofiles.ErrNotFound) {
			httperr.NotFound(w, fmt.Sprintf("qualityprofile %d not found", id))
			return
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("qualityprofiles: %w", err), nil)
			return
		}
		writeQualityProfileJSON(w, r, http.StatusOK, *p, formats)
	})
}

// QualityProfileCreateHandler returns POST /api/v3/qualityprofile.
// Mirrors `Create` at `:49-56`. Returns 201 + the persisted profile
// (with Id populated by the service).
//
// Validation ports the cheap subset of the C# SharedValidator chain:
//   - Required(Name)
//   - Items NotEmpty
//   - MinUpgradeFormatScore >= 1 (GreaterThanOrEqualTo(1))
//   - Cutoff resolves to one of the items (ValidCutoff)
//
// The deeper rules (FormatItems-vs-All match, MinFormatScore
// satisfiability, the per-item ValidItems internal check) are
// DEFERRED — they require richer cross-service validators that the
// Go port hasn't built yet. The service-side Add still runs whatever
// invariant checks live there, so the wire-shape rejection surface
// stays correct for the cases this slice covers.
func QualityProfileCreateHandler(profiles QualityProfileWriter, formats CustomFormatNameLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeQualityProfileBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("qualityprofile: malformed body: %v", err))
			return
		}
		if errs := validateQualityProfile(body); !errs.OK() {
			httperr.WriteValidation(w, errs.Failures())
			return
		}
		model, err := fromQualityProfileResource(body)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("qualityprofiles: serialise: %w", err), nil)
			return
		}
		inserted, err := profiles.Add(r.Context(), &model)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("qualityprofiles: %w", err), nil)
			return
		}
		writeQualityProfileJSON(w, r, http.StatusCreated, *inserted, formats)
	})
}

// QualityProfileUpdateHandler returns PUT /api/v3/qualityprofile/{id}.
// Mirrors `Update` at `:64-73`. Returns 202 + the persisted profile.
// Path id wins over body id per RestPutById.
func QualityProfileUpdateHandler(profiles QualityProfileWriter, formats CustomFormatNameLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseQualityProfileID(w, r)
		if !ok {
			return
		}
		body, err := decodeQualityProfileBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("qualityprofile: malformed body: %v", err))
			return
		}
		if errs := validateQualityProfile(body); !errs.OK() {
			httperr.WriteValidation(w, errs.Failures())
			return
		}
		body.ID = id
		model, err := fromQualityProfileResource(body)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("qualityprofiles: serialise: %w", err), nil)
			return
		}
		err = profiles.Update(r.Context(), &model)
		if errors.Is(err, qualityprofiles.ErrNotFound) {
			httperr.NotFound(w, fmt.Sprintf("qualityprofile %d not found", id))
			return
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("qualityprofiles: %w", err), nil)
			return
		}
		writeQualityProfileJSON(w, r, http.StatusAccepted, model, formats)
	})
}

// QualityProfileDeleteHandler returns DELETE /api/v3/qualityprofile/{id}.
// Mirrors `DeleteProfile` at `:58-62`. Returns 200 empty body on
// success; 409 envelope on ErrQualityProfileInUse (the C# pipeline
// surfaces QualityProfileInUseException as HTTP 409 via
// `Sonarr.Http.Exceptions.QualityProfileInUseException` → 409 status).
func QualityProfileDeleteHandler(profiles QualityProfileWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseQualityProfileID(w, r)
		if !ok {
			return
		}
		// Idempotent DELETE — C.1.i-follow-4 alignment (J232). C# returns 200 on missing id.
		// ErrQualityProfileInUse remains 409 (deletion not allowed when referenced).
		err := profiles.Delete(r.Context(), id)
		switch {
		case errors.Is(err, qualityprofiles.ErrNotFound):
			// Swallow — idempotent posture.
		case errors.Is(err, qualityprofiles.ErrQualityProfileInUse):
			httperr.Conflict(w, err.Error())
			return
		case err != nil:
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("qualityprofiles: %w", err), nil)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}

// ---------- helpers ----------

func parseQualityProfileID(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.PathValue("id")
	if raw == "" {
		httperr.BadRequest(w, "qualityprofile: missing id path parameter")
		return 0, false
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		httperr.BadRequest(w, fmt.Sprintf("qualityprofile: invalid id %q", raw))
		return 0, false
	}
	return id, true
}

func decodeQualityProfileBody(r *http.Request) (*QualityProfileResource, error) {
	var t QualityProfileResource
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return nil, err
	}
	return &t, nil
}

// validateQualityProfile ports the cheap subset of the C# SharedValidator
// chain. Defers the complex set-comparison rules to a future slice that
// can model the C# QualityProfileResourceValidator semantics.
func validateQualityProfile(res *QualityProfileResource) *validation.Errors {
	errs := validation.New()
	if !errs.Required("Name", res.Name) {
		return errs
	}
	if len(res.Items) == 0 {
		errs.Add("Items", "must contain at least one item")
		return errs
	}
	errs.Range("MinUpgradeFormatScore", res.MinUpgradeFormatScore, 1, int(^uint(0)>>1))
	// ValidCutoff: the Cutoff value must match either a leaf Quality.ID
	// or a group ID present in Items.
	if !cutoffResolves(res.Items, res.Cutoff) {
		errs.Add("Cutoff", "Cutoff must match one of the included items")
	}
	return errs
}

func cutoffResolves(items []QualityProfileItemResource, cutoff int) bool {
	for _, it := range items {
		if it.ID != 0 && it.ID == cutoff {
			return true
		}
		if it.Quality != nil && it.Quality.ID == cutoff {
			return true
		}
		if cutoffResolves(it.Items, cutoff) {
			return true
		}
	}
	return false
}

// fromQualityProfileResource serialises the wire-shape resource into
// the persisted Profile, including the Items+FormatItems JSON columns
// via the qualityprofiles SetItems/SetFormatItems helpers.
func fromQualityProfileResource(res *QualityProfileResource) (qualityprofiles.Profile, error) {
	items := make([]qualityprofiles.QualityItem, 0, len(res.Items))
	for _, it := range res.Items {
		items = append(items, fromQualityProfileItemResource(it))
	}
	formatItems := make([]qualityprofiles.FormatItem, 0, len(res.FormatItems))
	for _, fi := range res.FormatItems {
		formatItems = append(formatItems, qualityprofiles.FormatItem{
			FormatID: fi.Format,
			Score:    fi.Score,
		})
	}
	p := qualityprofiles.Profile{
		ID:                    res.ID,
		Name:                  res.Name,
		UpgradeAllowed:        res.UpgradeAllowed,
		Cutoff:                res.Cutoff,
		MinFormatScore:        res.MinFormatScore,
		CutoffFormatScore:     res.CutoffFormatScore,
		MinUpgradeFormatScore: res.MinUpgradeFormatScore,
	}
	if err := p.SetItems(items); err != nil {
		return qualityprofiles.Profile{}, err
	}
	if err := p.SetFormatItems(formatItems); err != nil {
		return qualityprofiles.Profile{}, err
	}
	return p, nil
}

// fromQualityProfileItemResource reverses toQualityProfileItemResource.
// Group vs quality discrimination uses the wire-shape signal: if the
// resource carries a non-nil Quality, it's a leaf entry; otherwise
// (Quality is nil and Items is non-empty) it's a group.
func fromQualityProfileItemResource(res QualityProfileItemResource) qualityprofiles.QualityItem {
	if res.Quality != nil {
		// Leaf quality entry. Look up the full Quality taxonomy by ID
		// so the persisted JSON carries the canonical Quality shape
		// (Source + Resolution come from the registry, not the wire).
		q, _ := quality.FindByID(res.Quality.ID)
		return qualityprofiles.QualityItem{
			Quality:       q,
			Items:         []qualityprofiles.QualityItem{},
			Allowed:       res.Allowed,
			MinSize:       res.MinSize,
			MaxSize:       res.MaxSize,
			PreferredSize: res.PreferredSize,
		}
	}
	// Group entry. Recurse on nested Items.
	children := make([]qualityprofiles.QualityItem, 0, len(res.Items))
	for _, c := range res.Items {
		children = append(children, fromQualityProfileItemResource(c))
	}
	return qualityprofiles.QualityItem{
		ID:      res.ID,
		Name:    res.Name,
		Items:   children,
		Allowed: res.Allowed,
	}
}

// writeQualityProfileJSON emits a single Profile projected to
// QualityProfileResource. Loads the format-name map fresh from the
// CustomFormat service so the FormatItems Name-join is current.
// Mirrors the C# `Service.Get(id).ToResource()` shape.
func writeQualityProfileJSON(w http.ResponseWriter, r *http.Request, status int, p qualityprofiles.Profile, formats CustomFormatNameLister) {
	formatRows, err := formats.All(r.Context())
	if err != nil {
		httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("customformats: %w", err), nil)
		return
	}
	formatNames := make(map[int]string, len(formatRows))
	for _, f := range formatRows {
		if f != nil {
			formatNames[f.ID] = f.Name
		}
	}
	res, err := toQualityProfileResource(p, formatNames)
	if err != nil {
		httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("qualityprofiles: project: %w", err), nil)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	buf, _ := json.Marshal(res)
	_, _ = w.Write(buf)
}
