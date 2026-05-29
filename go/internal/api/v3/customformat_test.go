package v3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/customformats"
)

type fakeCustomFormatLister struct {
	rows []*customformats.Format
	err  error
}

func (f *fakeCustomFormatLister) All(context.Context) ([]*customformats.Format, error) {
	return f.rows, f.err
}

func TestCustomFormatHandler_Empty(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	CustomFormatHandler(&fakeCustomFormatLister{rows: nil}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/customformat", nil))
	if got := rr.Body.String(); got != "[]" {
		t.Errorf("empty body: got %q want []", got)
	}
}

func TestCustomFormatHandler_PopulatedShape(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	CustomFormatHandler(&fakeCustomFormatLister{rows: []*customformats.Format{
		{ID: 1, Name: "x264", IncludeCustomFormatWhenRenaming: true},
	}}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/customformat", nil))
	body := rr.Body.String()
	for _, want := range []string{
		`"id":1`,
		`"name":"x264"`,
		`"includeCustomFormatWhenRenaming":true`,
		`"specifications":[]`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %s: %s", want, body)
		}
	}
}

func TestCustomFormatHandler_Error(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	CustomFormatHandler(&fakeCustomFormatLister{err: errors.New("db down")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/customformat", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}

// ---------- 5.B4: CRUD + bulk tests ----------

type fakeCustomFormatWriter struct {
	rows      []*customformats.Format
	getRow    *customformats.Format
	getErr    error
	allErr    error
	insertErr error
	updateErr error
	deleteErr error

	insertCalledWith *customformats.CustomFormat
	updateCalledWith *customformats.CustomFormat
	deleteCalledIDs  []int
}

func (f *fakeCustomFormatWriter) All(_ context.Context) ([]*customformats.Format, error) {
	if f.allErr != nil {
		return nil, f.allErr
	}
	return f.rows, nil
}

func (f *fakeCustomFormatWriter) GetByID(_ context.Context, id int) (*customformats.Format, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.getRow == nil {
		return nil, customformats.ErrNotFound
	}
	return f.getRow, nil
}

func (f *fakeCustomFormatWriter) Insert(_ context.Context, row *customformats.CustomFormat) (*customformats.Format, error) {
	f.insertCalledWith = row
	if f.insertErr != nil {
		return nil, f.insertErr
	}
	return &customformats.Format{ID: 42, Name: row.Name, IncludeCustomFormatWhenRenaming: row.IncludeCustomFormatWhenRenaming}, nil
}

func (f *fakeCustomFormatWriter) Update(_ context.Context, row *customformats.CustomFormat) error {
	f.updateCalledWith = row
	return f.updateErr
}

func (f *fakeCustomFormatWriter) Delete(_ context.Context, id int) error {
	f.deleteCalledIDs = append(f.deleteCalledIDs, id)
	return f.deleteErr
}

func cfBody(name string, hasSpec bool) string {
	specs := "[]"
	if hasSpec {
		specs = `[{"id":0,"name":"x264","implementation":"ReleaseTitleSpecification","implementationName":"Release Title","negate":false,"required":false,"fields":[]}]`
	}
	return fmt.Sprintf(`{"name":%q,"specifications":%s}`, name, specs)
}

// ----- GET /customformat/{id} -----

func TestCustomFormatByIDHandler_Hit(t *testing.T) {
	t.Parallel()
	w := &fakeCustomFormatWriter{getRow: &customformats.Format{ID: 5, Name: "x264"}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/customformat/5", nil)
	req.SetPathValue("id", "5")
	CustomFormatByIDHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"id":5`) || !strings.Contains(rr.Body.String(), `"name":"x264"`) {
		t.Errorf("body: %s", rr.Body.String())
	}
}

func TestCustomFormatByIDHandler_NotFound(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/customformat/99", nil)
	req.SetPathValue("id", "99")
	CustomFormatByIDHandler(&fakeCustomFormatWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d", rr.Code)
	}
}

// ----- POST /customformat -----

func TestCustomFormatCreateHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeCustomFormatWriter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/customformat", strings.NewReader(cfBody("x264", true)))
	CustomFormatCreateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if w.insertCalledWith == nil || w.insertCalledWith.Name != "x264" {
		t.Errorf("svc.Insert called with %+v", w.insertCalledWith)
	}
	if !strings.Contains(w.insertCalledWith.Specifications, "ReleaseTitleSpecification") {
		t.Errorf("Specifications JSON should pass through: %q", w.insertCalledWith.Specifications)
	}
}

func TestCustomFormatCreateHandler_EmptyName400Validation(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/customformat", strings.NewReader(cfBody("", true)))
	CustomFormatCreateHandler(&fakeCustomFormatWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"propertyName":"Name"`) {
		t.Errorf("body should fault Name: %s", rr.Body.String())
	}
}

func TestCustomFormatCreateHandler_EmptySpecs400Validation(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/customformat", strings.NewReader(cfBody("x264", false)))
	CustomFormatCreateHandler(&fakeCustomFormatWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Must contain at least one Condition") {
		t.Errorf("body should require Specifications: %s", rr.Body.String())
	}
}

func TestCustomFormatCreateHandler_DuplicateName400Validation(t *testing.T) {
	t.Parallel()
	w := &fakeCustomFormatWriter{rows: []*customformats.Format{{ID: 10, Name: "x264"}}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v3/customformat", strings.NewReader(cfBody("x264", true)))
	CustomFormatCreateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Must be unique") {
		t.Errorf("body should reject duplicate Name: %s", rr.Body.String())
	}
}

func TestCustomFormatCreateHandler_EmptySpecNameRejected(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	body := `{"name":"x264","specifications":[{"id":0,"name":"","implementation":"X","implementationName":"X","negate":false,"required":false,"fields":[]}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v3/customformat", strings.NewReader(body))
	CustomFormatCreateHandler(&fakeCustomFormatWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Condition name(s) cannot be empty") {
		t.Errorf("body: %s", rr.Body.String())
	}
}

// ----- PUT /customformat/{id} -----

func TestCustomFormatUpdateHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeCustomFormatWriter{getRow: &customformats.Format{ID: 5, Name: "x264 renamed"}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/customformat/5", strings.NewReader(cfBody("x264 renamed", true)))
	req.SetPathValue("id", "5")
	CustomFormatUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if w.updateCalledWith == nil || w.updateCalledWith.ID != 5 {
		t.Errorf("svc.Update called with %+v", w.updateCalledWith)
	}
}

func TestCustomFormatUpdateHandler_UniquenessExcludesSelf(t *testing.T) {
	t.Parallel()
	// Update on id=5 with name "x264" must SUCCEED if the only row
	// with that name IS id=5 — uniqueness excludes self.
	w := &fakeCustomFormatWriter{
		rows:   []*customformats.Format{{ID: 5, Name: "x264"}},
		getRow: &customformats.Format{ID: 5, Name: "x264"},
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/customformat/5", strings.NewReader(cfBody("x264", true)))
	req.SetPathValue("id", "5")
	CustomFormatUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Errorf("status: got %d want 202 (uniqueness must exclude self); body=%s", rr.Code, rr.Body.String())
	}
}

// ----- DELETE /customformat/{id} -----

func TestCustomFormatDeleteHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeCustomFormatWriter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/customformat/5", nil)
	req.SetPathValue("id", "5")
	CustomFormatDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status: %d", rr.Code)
	}
	if len(w.deleteCalledIDs) != 1 || w.deleteCalledIDs[0] != 5 {
		t.Errorf("svc.Delete called with %v", w.deleteCalledIDs)
	}
}

// ----- PUT /customformat/bulk -----

func TestCustomFormatBulkUpdateHandler_PatchesIncludeFlag(t *testing.T) {
	t.Parallel()
	w := &fakeCustomFormatWriter{getRow: &customformats.Format{ID: 5, Name: "x"}}
	rr := httptest.NewRecorder()
	body := `{"ids":[5,7],"includeCustomFormatWhenRenaming":true}`
	req := httptest.NewRequest(http.MethodPut, "/api/v3/customformat/bulk", strings.NewReader(body))
	CustomFormatBulkUpdateHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if w.updateCalledWith == nil || !w.updateCalledWith.IncludeCustomFormatWhenRenaming {
		t.Errorf("Update should have set include=true; got %+v", w.updateCalledWith)
	}
}

func TestCustomFormatBulkUpdateHandler_EmptyIds400(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v3/customformat/bulk", strings.NewReader(`{"ids":[]}`))
	CustomFormatBulkUpdateHandler(&fakeCustomFormatWriter{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "ids must be provided") {
		t.Errorf("body: %s", rr.Body.String())
	}
}

// ----- DELETE /customformat/bulk -----

func TestCustomFormatBulkDeleteHandler_Success(t *testing.T) {
	t.Parallel()
	w := &fakeCustomFormatWriter{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/customformat/bulk", strings.NewReader(`{"ids":[3,5,7]}`))
	CustomFormatBulkDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Body.String() != "{}" {
		t.Errorf("body should be {}; got %s", rr.Body.String())
	}
	if len(w.deleteCalledIDs) != 3 || w.deleteCalledIDs[0] != 3 || w.deleteCalledIDs[2] != 7 {
		t.Errorf("delete called with %v want [3 5 7]", w.deleteCalledIDs)
	}
}

func TestCustomFormatBulkDeleteHandler_AnyMissingIs404(t *testing.T) {
	t.Parallel()
	w := &fakeCustomFormatWriter{deleteErr: customformats.ErrNotFound}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v3/customformat/bulk", strings.NewReader(`{"ids":[99]}`))
	CustomFormatBulkDeleteHandler(w).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d want 404", rr.Code)
	}
}

// ----- J302: Resource projection alignment with calculator-engine output -----
//
// Pins the dual-projection contract of CustomFormatResource:
//   - includeDetails=true (the [toCustomFormatResource] constructor used
//     by GET / by-id / POST / PUT / PUT-bulk): emits all 4 fields,
//     including `"specifications":[]` for an empty list — matches C#
//     `model.Specifications.Select(...).ToList()` behaviour at
//     `CustomFormatResource.cs:30`.
//   - includeDetails=false (the [ToCustomFormatResourceLite]
//     constructor): emits ONLY `id` + `name`, dropping the optional
//     fields via omitempty — matches C# `new CustomFormatResource {
//     Id = model.Id, Name = model.Name }` at
//     `CustomFormatResource.cs:21-25` under STJson's
//     `DefaultIgnoreCondition.WhenWritingNull`.
//
// The lite projection is the wire shape produced by the calculator
// engine chain (`formatCalculator.ParseCustomFormat(model, model.Series)
// .ToResource(false)`) at `EpisodeFileResource.cs:46-66` — when
// populated-row service mappers land post-Wave-3, their per-row
// CustomFormats[] entries will use this constructor.

func TestToCustomFormatResourceLite_EmitsIDAndNameOnly(t *testing.T) {
	t.Parallel()
	f := &customformats.Format{ID: 7, Name: "x265", IncludeCustomFormatWhenRenaming: true}
	res := ToCustomFormatResourceLite(f)
	buf, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(buf)
	// Positive pins.
	if !strings.Contains(body, `"id":7`) {
		t.Errorf("missing id: %s", body)
	}
	if !strings.Contains(body, `"name":"x265"`) {
		t.Errorf("missing name: %s", body)
	}
	// Negative pins — omitempty drops the optional fields.
	if strings.Contains(body, `"specifications"`) {
		t.Errorf("specifications must be omitted (lite projection): %s", body)
	}
	if strings.Contains(body, `"includeCustomFormatWhenRenaming"`) {
		t.Errorf("includeCustomFormatWhenRenaming must be omitted (lite projection): %s", body)
	}
}

func TestToCustomFormatResourceLite_NilFormat_EmitsZeroValues(t *testing.T) {
	t.Parallel()
	res := ToCustomFormatResourceLite(nil)
	buf, _ := json.Marshal(res)
	body := string(buf)
	// Zero-value Format: id=0 (always emitted via JsonIgnore(Never) on C#);
	// name="" (still emitted — non-omitempty); no optional fields.
	if !strings.Contains(body, `"id":0`) {
		t.Errorf("nil Format should still emit id:0; got %s", body)
	}
	if strings.Contains(body, `"specifications"`) {
		t.Errorf("nil Format should not emit specifications: %s", body)
	}
}

func TestToCustomFormatResource_FullProjection_StillEmitsEmptySpecsArray(t *testing.T) {
	t.Parallel()
	// Regression guard: the J302 pointer-to-slice switch must NOT alter
	// the full-projection wire shape — `"specifications":[]` is required
	// for the GET list / by-id / POST / PUT / PUT-bulk responses (the
	// J302 PROGRESS row + the existing TestCustomFormatHandler_OK test
	// at line 44 both depend on this).
	f := &customformats.Format{ID: 1, Name: "x264"}
	res := ToCustomFormatResource(f)
	buf, _ := json.Marshal(res)
	body := string(buf)
	if !strings.Contains(body, `"specifications":[]`) {
		t.Errorf("full projection must emit specifications:[]; got %s", body)
	}
	if !strings.Contains(body, `"includeCustomFormatWhenRenaming"`) {
		t.Errorf("full projection must emit includeCustomFormatWhenRenaming; got %s", body)
	}
}

func TestCustomFormatResource_RoundTrip_NilSpecsDecodesAsNilPointer(t *testing.T) {
	t.Parallel()
	// Validation contract: a POST body missing the `specifications` key
	// OR sending `"specifications": null` should fail with the same
	// "Must contain at least one Condition" error as an empty array.
	// All three cases collapse to nil-or-empty under the pointer-to-
	// slice tag.
	for _, body := range []string{
		`{"name":"x"}`,
		`{"name":"x","specifications":null}`,
		`{"name":"x","specifications":[]}`,
	} {
		var res CustomFormatResource
		if err := json.Unmarshal([]byte(body), &res); err != nil {
			t.Fatalf("unmarshal %s: %v", body, err)
		}
		if res.Specifications != nil && len(*res.Specifications) > 0 {
			t.Errorf("body %s should decode as nil-or-empty Specifications; got %v", body, res.Specifications)
		}
	}
}

// --- G4-followup write-path fix (J431): the persisted Specifications
// column MUST carry the loader's envelope shape `[{"type","body"}]`,
// not the on-wire schema shape. These tests prove the transform's
// output decodes through the SAME customformats.UnmarshalSpecs the
// load path uses (the old schema-shape persistence did NOT decode —
// it produced "unknown discriminator").

func cfField(name string, value any) map[string]any {
	return map[string]any{"name": name, "value": value}
}

func TestCustomFormatSpecsToEnvelopeJSON_EmptyAndNil(t *testing.T) {
	t.Parallel()
	got, err := customFormatSpecsToEnvelopeJSON(nil)
	if err != nil || got != "[]" {
		t.Fatalf("nil: got %q err %v want []", got, err)
	}
	empty := []CustomFormatSpecificationSchema{}
	got, err = customFormatSpecsToEnvelopeJSON(&empty)
	if err != nil || got != "[]" {
		t.Fatalf("empty: got %q err %v want []", got, err)
	}
}

func TestCustomFormatSpecsToEnvelopeJSON_RoundTripsThroughLoader(t *testing.T) {
	t.Parallel()
	specs := []CustomFormatSpecificationSchema{
		{
			Name: "x264", Implementation: "ReleaseTitleSpecification",
			Negate: true, Required: true,
			Fields: []any{cfField("value", `(x|h)\.?264`)},
		},
		{
			Name: "1080p", Implementation: "ResolutionSpecification",
			Fields: []any{cfField("value", float64(1080))},
		},
		{
			Name: "Big", Implementation: "SizeSpecification",
			Fields: []any{cfField("min", float64(5)), cfField("max", float64(20))},
		},
		{
			Name: "NotEnglish", Implementation: "LanguageSpecification",
			Fields: []any{cfField("value", float64(1)), cfField("exceptLanguage", true)},
		},
	}
	out, err := customFormatSpecsToEnvelopeJSON(&specs)
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	// The load path's exact call — this is what failed under the old
	// schema-shape persistence.
	typed, err := customformats.UnmarshalSpecs([]byte(out))
	if err != nil {
		t.Fatalf("UnmarshalSpecs rejected the persisted shape: %v\njson=%s", err, out)
	}
	if len(typed) != 4 {
		t.Fatalf("got %d specs, want 4", len(typed))
	}
	rt, ok := typed[0].(*customformats.ReleaseTitleSpec)
	if !ok {
		t.Fatalf("spec0 type %T", typed[0])
	}
	if rt.Pattern() != `(x|h)\.?264` {
		t.Errorf("regex value = %q", rt.Pattern())
	}
	if !rt.Negate || !rt.Required || rt.Name != "x264" {
		t.Errorf("base fields not preserved: %+v", rt.BaseSpecification)
	}
	if res, ok := typed[1].(*customformats.ResolutionSpec); !ok || res.Value != 1080 {
		t.Errorf("resolution = %+v", typed[1])
	}
	if sz, ok := typed[2].(*customformats.SizeSpec); !ok || sz.Min != 5 || sz.Max != 20 {
		t.Errorf("size = %+v", typed[2])
	}
	if lg, ok := typed[3].(*customformats.LanguageSpec); !ok || lg.Value != 1 || !lg.ExceptLanguage {
		t.Errorf("language = %+v", typed[3])
	}
}

func TestCustomFormatSpecsToEnvelopeJSON_UnknownImplementationErrors(t *testing.T) {
	t.Parallel()
	specs := []CustomFormatSpecificationSchema{
		{Name: "x", Implementation: "NotARealSpecification", Fields: []any{cfField("value", "y")}},
	}
	if _, err := customFormatSpecsToEnvelopeJSON(&specs); err == nil {
		t.Fatal("expected error for unknown implementation discriminator")
	}
}

// TestSpecToSchema_ProjectsTypedSpecWithFieldMetadata is the G4-followup
// read half (J432): GET must project each persisted typed spec into the
// full CustomFormatSpecificationSchema (implementation + implementationName
// + infoLink + base flags + the cloned schema fields with the persisted
// value injected). Mirrors C# ICustomFormatSpecification.ToSchema().
func TestSpecToSchema_ProjectsTypedSpecWithFieldMetadata(t *testing.T) {
	t.Parallel()
	// Build typed specs through the real load codec (envelope → typed).
	env := `[
	  {"type":"ReleaseTitleSpecification","body":{"name":"x264","negate":true,"required":true,"value":"(x|h)264"}},
	  {"type":"ResolutionSpecification","body":{"name":"1080p","negate":false,"required":false,"value":1080}}
	]`
	typed, err := customformats.UnmarshalSpecs([]byte(env))
	if err != nil {
		t.Fatal(err)
	}
	f := &customformats.Format{ID: 7, Name: "HD", IncludeCustomFormatWhenRenaming: true, Specifications: typed}
	res := toCustomFormatResource(f)
	if res.Specifications == nil || len(*res.Specifications) != 2 {
		t.Fatalf("specs = %v", res.Specifications)
	}
	rt := (*res.Specifications)[0]
	if rt.Implementation != "ReleaseTitleSpecification" || rt.ImplementationName != "Release Title" {
		t.Errorf("impl = %q / %q", rt.Implementation, rt.ImplementationName)
	}
	if rt.InfoLink != customFormatInfoLink {
		t.Errorf("infoLink = %q", rt.InfoLink)
	}
	if rt.Name != "x264" || !rt.Negate || !rt.Required {
		t.Errorf("base flags wrong: %+v", rt)
	}
	if len(rt.Fields) != 1 {
		t.Fatalf("fields = %d want 1", len(rt.Fields))
	}
	// The cloned regex field must carry the injected value on the wire.
	body, _ := json.Marshal(res)
	s := string(body)
	for _, want := range []string{
		`"implementation":"ReleaseTitleSpecification"`,
		`"implementationName":"Release Title"`,
		`"infoLink":"` + customFormatInfoLink + `"`,
		`"value":"(x|h)264"`,
		`"name":"value"`, // the field's name
		`"value":1080`,   // resolution int value injected
		`"implementation":"ResolutionSpecification"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("wire missing %s\n%s", want, s)
		}
	}
	// id=0 on each spec must be omitted (C# ToSchema never sets it).
	res2 := toCustomFormatResource(f)
	b2, _ := json.Marshal((*res2.Specifications)[0])
	if strings.Contains(string(b2), `"id"`) {
		t.Errorf("spec must omit id=0: %s", b2)
	}
}
