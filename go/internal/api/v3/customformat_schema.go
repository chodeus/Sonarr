package v3

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"sonarr.tv/sonarr/internal/language"
)

// CustomFormatSpecificationSchemaResource mirrors
// `src/Sonarr.Api.V3/CustomFormats/CustomFormatSpecificationSchema.cs`.
//
// Field-source mapping (C# → Go), every field present on the wire:
//
//	id                 → SpecificationBase.ID (omit when 0 — schemas are
//	                     unpersisted, so ID is always 0 / omitted)
//	name               → SpecificationBase.Name (string, omitempty)
//	implementation     → reflected struct name ("ReleaseTitleSpecification")
//	implementationName → ICustomFormatSpecification.ImplementationName
//	infoLink           → ICustomFormatSpecification.InfoLink
//	negate             → SpecificationBase.Negate (always emit)
//	required           → SpecificationBase.Required (always emit)
//	fields             → SchemaBuilder.ToSchema(spec) — per-field POCOs
//	presets            → controller-supplied list (only ReleaseTitleSpec
//	                     today; null elsewhere → omitted)
type CustomFormatSpecificationSchemaResource struct {
	ID                 int                                        `json:"id,omitempty"`
	Name               string                                     `json:"name,omitempty"`
	Implementation     string                                     `json:"implementation"`
	ImplementationName string                                     `json:"implementationName"`
	InfoLink           string                                     `json:"infoLink"`
	Negate             bool                                       `json:"negate"`
	Required           bool                                       `json:"required"`
	Fields             []FieldDef                                 `json:"fields"`
	Presets            *[]CustomFormatSpecificationSchemaResource `json:"presets,omitempty"`
}

// customFormatInfoLink is the wiki anchor every CustomFormat
// specification's `InfoLink` returns. Pinned in
// `src/NzbDrone.Core/CustomFormats/CustomFormatSpecificationBase.cs`
// as `public string InfoLink => "https://wiki.servarr.com/sonarr/settings#custom-formats-2"`.
const customFormatInfoLink = "https://wiki.servarr.com/sonarr/settings#custom-formats-2"

// customFormatRegexHelpText is the English-localised string from
// `src/NzbDrone.Core/Localization/Core/en.json` for the
// `CustomFormatsSpecificationRegularExpressionHelpText` key. The Go
// side hard-codes the English string here because the schema
// endpoint emits localized labels — when the per-language overlay
// lands (4.EK follow-up), this constant becomes a lookup against
// the localization service.
const customFormatRegexHelpText = "Custom Format RegEx is Case Insensitive"

// regexValueField is the single `value` field every regex-based
// custom-format spec (ReleaseTitle, ReleaseGroup) emits. Modeled
// once to keep the wire shape identical across both specs.
var regexValueField = FieldDef{
	Order:    0,
	Name:     "value",
	Label:    "Regular Expression",
	HelpText: customFormatRegexHelpText,
	Type:     "textbox",
	Privacy:  "normal",
}

// customFormatSchemas is the canonical list of spec schemas, sorted
// by C# `ICustomFormatSpecification.Order` ascending (matches
// `CustomFormatController.GetTemplates` at
// `src/Sonarr.Api.V3/CustomFormats/CustomFormatController.cs:121`).
//
// Order is FIXED:
//
//	ReleaseTitleSpec (1) → LanguageSpec (3) → IndexerFlagSpec (4) →
//	SourceSpec (5) → ResolutionSpec (6) → SizeSpec (8) →
//	ReleaseGroupSpec (9) → ReleaseTypeSpec (10)
//
// Hard-coded rather than reflection-derived because (a) the C#
// `FieldDefinitionAttribute` reflection has no Go equivalent in the
// port today, and (b) the data is small + static + the maintenance
// cost is bounded by the seven specs. A future schema-builder
// foundation slice may replace this with a reflection-driven
// emitter.
var customFormatSchemas = func() []CustomFormatSpecificationSchemaResource {
	out := []CustomFormatSpecificationSchemaResource{
		releaseTitleSchema(),
		languageSchema(),
		indexerFlagSchema(),
		sourceSchema(),
		resolutionSchema(),
		sizeSchema(),
		releaseGroupSchema(),
		releaseTypeSchema(),
	}
	// Top-level schemas always emit `"presets":[…]` on the wire (the
	// C# `.ToList()` on the filtered preset enumeration yields a
	// non-null `List<T>` even when the filter matches nothing). Inner
	// preset entries, however, omit the `presets` field entirely (the
	// nested ToSchema() call never sets it). Modeling Presets as
	// `*[]…` lets a nil pointer omit + a non-nil pointer (even to an
	// empty slice) emit `[]` — both shapes required for byte-eq.
	for i := range out {
		if out[i].Presets == nil {
			empty := []CustomFormatSpecificationSchemaResource{}
			out[i].Presets = &empty
		}
	}
	return out
}()

func releaseTitleSchema() CustomFormatSpecificationSchemaResource {
	presets := releaseTitlePresets()
	return CustomFormatSpecificationSchemaResource{
		Implementation:     "ReleaseTitleSpecification",
		ImplementationName: "Release Title",
		InfoLink:           customFormatInfoLink,
		Fields:             []FieldDef{regexValueField},
		Presets:            &presets,
	}
}

// releaseTitlePresets ports the C#
// `CustomFormatController.GetPresets` hard-coded list at
// `src/Sonarr.Api.V3/CustomFormats/CustomFormatController.cs:151-188`.
// Six ReleaseTitleSpecification entries with regex Value defaults.
// Wiki and field shape match the parent ReleaseTitle schema; only
// `name` + the field's `value` differ per preset.
func releaseTitlePresets() []CustomFormatSpecificationSchemaResource {
	specs := []struct {
		name, regex string
	}{
		{"x264", `(x|h)\.?264`},
		{"x265", `(((x|h)\.?265)|(HEVC))`},
		{"Simple Hardcoded Subs", `subs?`},
		{"Hardcoded Subs", `\b(?<hcsub>(\w+SUBS?)\b)|(?<hc>(HC|SUBBED))\b`},
		{"Surround Sound", `DTS.?(HD|ES|X(?!\D))|TRUEHD|ATMOS|DD(\+|P).?([5-9])|EAC3.?([5-9])`},
		{"Preferred Words", `\b(SPARKS|Framestor)\b`},
	}
	out := make([]CustomFormatSpecificationSchemaResource, 0, len(specs))
	for _, s := range specs {
		out = append(out, CustomFormatSpecificationSchemaResource{
			Name:               s.name,
			Implementation:     "ReleaseTitleSpecification",
			ImplementationName: "Release Title",
			InfoLink:           customFormatInfoLink,
			Fields:             []FieldDef{regexValueField.WithValue(s.regex)},
		})
	}
	return out
}

func languageSchema() CustomFormatSpecificationSchemaResource {
	return CustomFormatSpecificationSchemaResource{
		Implementation:     "LanguageSpecification",
		ImplementationName: "Language",
		InfoLink:           customFormatInfoLink,
		Fields: []FieldDef{
			{
				Order: 0, Name: "value", Label: "Language", Type: "select",
				Privacy: "normal", SelectOptions: languageSelectOptions(),
				Value: 0, ValueSet: true,
			},
			{
				Order: 1, Name: "exceptLanguage", Label: "Except Language",
				HelpText: "Matches if any language other than the selected language is present",
				Type:     "checkbox", Privacy: "normal",
				Value: false, ValueSet: true,
			},
		},
	}
}

// languageSelectOptions emits the language list the C#
// `LanguageSpecification` field's `[FieldDefinition]` reflection
// produces via `Language.AllLookup`. Order is C# `OrderBy(l => l.Id
// > 0).ThenBy(l => l.Name)` — non-positive Ids (Original=-2,
// Unknown=0) come first, then positives alphabetically by name.
// Identical sort to `/api/v3/language` (slice 4.O) — reuses
// `LanguageIdentifier`-adjacent helpers via the existing
// language registry.
func languageSelectOptions() []SelectOption {
	langs := language.All()
	out := make([]SelectOption, 0, len(langs))
	for _, l := range langs {
		out = append(out, SelectOption{Value: l.ID, Name: l.Name})
	}
	sort.SliceStable(out, func(i, j int) bool {
		ag, bg := langBucket(out[i].Value), langBucket(out[j].Value)
		if ag != bg {
			return ag < bg
		}
		return strings.Compare(out[i].Name, out[j].Name) < 0
	})
	return out
}

func langBucket(id int) int {
	if id > 0 {
		return 1
	}
	return 0
}

func indexerFlagSchema() CustomFormatSpecificationSchemaResource {
	return CustomFormatSpecificationSchemaResource{
		Implementation:     "IndexerFlagSpecification",
		ImplementationName: "Indexer Flag",
		InfoLink:           customFormatInfoLink,
		Fields: []FieldDef{
			{
				Order: 0, Name: "value", Label: "Flag", Type: "select",
				Privacy: "normal", SelectOptions: indexerFlagOptions(),
				Value: 0, ValueSet: true,
			},
		},
	}
}

// indexerFlagOptions emits the IndexerFlags enum entries the C#
// `IndexerFlags` enum exposes. Each flag is a power-of-two bit; the
// `Order` field is the bit value itself (matching C# `Flag` typed
// enum value carrying both Value + Order). Source: `src/NzbDrone.
// Core/Indexers/IndexerFlags.cs`.
func indexerFlagOptions() []SelectOption {
	return []SelectOption{
		{Value: 1, Name: "Freeleech", Order: 1},
		{Value: 2, Name: "Halfleech", Order: 2},
		{Value: 4, Name: "DoubleUpload", Order: 4},
		{Value: 8, Name: "Internal", Order: 8},
		{Value: 16, Name: "Scene", Order: 16},
		{Value: 32, Name: "Freeleech75", Order: 32},
		{Value: 64, Name: "Freeleech25", Order: 64},
		{Value: 128, Name: "Nuked", Order: 128},
		{Value: 256, Name: "Subtitles", Order: 256},
	}
}

func sourceSchema() CustomFormatSpecificationSchemaResource {
	return CustomFormatSpecificationSchemaResource{
		Implementation:     "SourceSpecification",
		ImplementationName: "Source",
		InfoLink:           customFormatInfoLink,
		Fields: []FieldDef{
			{
				Order: 0, Name: "value", Label: "Source", Type: "select",
				Privacy: "normal", SelectOptions: sourceOptions(),
				Value: 0, ValueSet: true,
			},
		},
	}
}

// sourceOptions mirrors the C# `QualitySource` enum entries.
// Source: `src/NzbDrone.Core/Qualities/QualitySource.cs:3-13`.
func sourceOptions() []SelectOption {
	return []SelectOption{
		{Value: 0, Name: "Unknown", Order: 0},
		{Value: 1, Name: "Television", Order: 1},
		{Value: 2, Name: "TelevisionRaw", Order: 2},
		{Value: 3, Name: "Web", Order: 3},
		{Value: 4, Name: "WebRip", Order: 4},
		{Value: 5, Name: "DVD", Order: 5},
		{Value: 6, Name: "Bluray", Order: 6},
		{Value: 7, Name: "BlurayRaw", Order: 7},
	}
}

func resolutionSchema() CustomFormatSpecificationSchemaResource {
	return CustomFormatSpecificationSchemaResource{
		Implementation:     "ResolutionSpecification",
		ImplementationName: "Resolution",
		InfoLink:           customFormatInfoLink,
		Fields: []FieldDef{
			{
				Order: 0, Name: "value", Label: "Resolution", Type: "select",
				Privacy: "normal", SelectOptions: resolutionOptions(),
				Value: 0, ValueSet: true,
			},
		},
	}
}

// resolutionOptions mirrors `src/NzbDrone.Core/CustomFormats/
// Specifications/ResolutionSpecification.cs` — Value=int pixel
// height, Name="R<height>p". Order matches Value.
func resolutionOptions() []SelectOption {
	return []SelectOption{
		{Value: 0, Name: "Unknown", Order: 0},
		{Value: 360, Name: "R360p", Order: 360},
		{Value: 480, Name: "R480p", Order: 480},
		{Value: 540, Name: "R540p", Order: 540},
		{Value: 576, Name: "R576p", Order: 576},
		{Value: 720, Name: "R720p", Order: 720},
		{Value: 1080, Name: "R1080p", Order: 1080},
		{Value: 2160, Name: "R2160p", Order: 2160},
	}
}

func sizeSchema() CustomFormatSpecificationSchemaResource {
	return CustomFormatSpecificationSchemaResource{
		Implementation:     "SizeSpecification",
		ImplementationName: "Size",
		InfoLink:           customFormatInfoLink,
		Fields: []FieldDef{
			{
				Order: 0, Name: "min", Label: "Minimum Size", Unit: "GB",
				HelpText: "Release must be greater than this size",
				Type:     "number", Privacy: "normal", IsFloat: true,
				Value: float64(0), ValueSet: true,
			},
			{
				Order: 1, Name: "max", Label: "Maximum Size", Unit: "GB",
				HelpText: "Release must be less than or equal to this size",
				Type:     "number", Privacy: "normal", IsFloat: true,
				Value: float64(0), ValueSet: true,
			},
		},
	}
}

func releaseGroupSchema() CustomFormatSpecificationSchemaResource {
	return CustomFormatSpecificationSchemaResource{
		Implementation:     "ReleaseGroupSpecification",
		ImplementationName: "Release Group",
		InfoLink:           customFormatInfoLink,
		Fields:             []FieldDef{regexValueField},
	}
}

func releaseTypeSchema() CustomFormatSpecificationSchemaResource {
	return CustomFormatSpecificationSchemaResource{
		Implementation:     "ReleaseTypeSpecification",
		ImplementationName: "Release Type",
		InfoLink:           customFormatInfoLink,
		Fields: []FieldDef{
			{
				Order: 0, Name: "value", Label: "Release Type", Type: "select",
				Privacy: "normal", SelectOptions: releaseTypeOptions(),
				Value: 0, ValueSet: true,
			},
		},
	}
}

// releaseTypeOptions mirrors `src/NzbDrone.Core/Parser/Model/
// ReleaseType.cs`. The "(N)" hint reflects the Episode count
// implication for each release type.
func releaseTypeOptions() []SelectOption {
	return []SelectOption{
		{Value: 0, Name: "Unknown", Order: 0},
		{Value: 1, Name: "Single Episode", Order: 9, Hint: "(1)"},
		{Value: 2, Name: "Multi-Episode", Order: 12, Hint: "(2)"},
		{Value: 3, Name: "Season Pack", Order: 15, Hint: "(3)"},
	}
}

// CustomFormatSchemaHandler returns the http.Handler for
// GET /api/v3/customformat/schema. Mirrors
// `CustomFormatController.GetTemplates` at
// `src/Sonarr.Api.V3/CustomFormats/CustomFormatController.cs:118-131`.
//
// The C# side composes the schema from runtime reflection over each
// IProvideCustomFormatSpecification implementation + a hard-coded
// preset list. The Go port substitutes the canonical
// customFormatSchemas table built at package init — same wire shape,
// no reflection. User-defined preset rows (the C# tree merges in
// every persisted CustomFormat row's specifications under a
// "<Name>: <preset>" prefix) are DEFERRED until the format-import
// flow has a story; the recording corpus uses an empty DB so the
// merge yields no extra entries today.
func CustomFormatSchemaHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(customFormatSchemas)
		_, _ = w.Write(buf)
	})
}
