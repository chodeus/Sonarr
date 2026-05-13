package v3

import (
	"encoding/json"
	"net/http"
)

// AutoTaggingSpecificationSchemaResource mirrors
// `src/Sonarr.Api.V3/AutoTagging/AutoTaggingSpecificationSchema.cs`.
// Same shape as CustomFormatSpecificationSchemaResource minus the
// `infoLink` (AutoTagSpecificationBase has no InfoLink property) and
// minus the `presets` (the autotagging schema endpoint emits no
// presets — the C# controller's GetTemplates just emits the
// per-spec schema with no preset list).
//
// Field-source mapping (C# → Go):
//
//	id                 → SpecificationBase.ID (omit when 0)
//	name               → SpecificationBase.Name (omitempty)
//	implementation     → reflected struct name
//	implementationName → ImplementationName accessor
//	negate             → SpecificationBase.Negate (always emit)
//	required           → SpecificationBase.Required (always emit)
//	fields             → SchemaBuilder.ToSchema(spec)
type AutoTaggingSpecificationSchemaResource struct {
	ID                 int        `json:"id,omitempty"`
	Name               string     `json:"name,omitempty"`
	Implementation     string     `json:"implementation"`
	ImplementationName string     `json:"implementationName"`
	Negate             bool       `json:"negate"`
	Required           bool       `json:"required"`
	Fields             []FieldDef `json:"fields"`
}

// autoTaggingSchemas is the canonical 11-entry table, sorted by C#
// `IAutoTaggingSpecification.Order` ASC then by implementation name
// ASC (LINQ OrderBy is stable + the C# DI assembly-scan returns
// specs in alphabetical type-name order within each Order bucket).
//
// Order=1: Genre, Monitored, Network, OriginalCountry,
// OriginalLanguage, QualityProfile, RootFolder, Status, Tag, Year.
// Order=2: SeriesType.
//
// MonitoredSpecification emits an empty fields slice (`fields: []`)
// — it has no Value field, just the on/off toggle inherent in the
// implementation. The wire shape pins `fields: []` (non-null empty),
// modeled by the non-omitempty `Fields []FieldDef` tag plus the
// per-schema constructor initialising the slice to `[]FieldDef{}`.
var autoTaggingSchemas = []AutoTaggingSpecificationSchemaResource{
	genreSchema(),
	monitoredSchema(),
	networkSchema(),
	originalCountrySchema(),
	originalLanguageSchema(),
	qualityProfileSchema(),
	rootFolderSchema(),
	statusSchema(),
	tagSchema(),
	yearSchema(),
	seriesTypeSchema(),
}

func genreSchema() AutoTaggingSpecificationSchemaResource {
	return AutoTaggingSpecificationSchemaResource{
		Implementation:     "GenreSpecification",
		ImplementationName: "Genre",
		Fields: []FieldDef{{
			Order: 0, Name: "value", Label: "Genre(s)",
			Type: "tag", Privacy: "normal",
		}},
	}
}

func monitoredSchema() AutoTaggingSpecificationSchemaResource {
	// MonitoredSpecification carries no fields — the spec's only
	// state is on/off + the inherent Negate flag. Empty `fields: []`
	// on the wire.
	return AutoTaggingSpecificationSchemaResource{
		Implementation:     "MonitoredSpecification",
		ImplementationName: "Monitored",
		Fields:             []FieldDef{},
	}
}

func networkSchema() AutoTaggingSpecificationSchemaResource {
	return AutoTaggingSpecificationSchemaResource{
		Implementation:     "NetworkSpecification",
		ImplementationName: "Network",
		Fields: []FieldDef{{
			Order: 0, Name: "value", Label: "Network(s)",
			Type: "tag", Privacy: "normal",
		}},
	}
}

func originalCountrySchema() AutoTaggingSpecificationSchemaResource {
	return AutoTaggingSpecificationSchemaResource{
		Implementation:     "OriginalCountrySpecification",
		ImplementationName: "Original Country",
		Fields: []FieldDef{{
			Order: 0, Name: "value", Label: "Country",
			Type: "tag", Privacy: "normal",
		}},
	}
}

func originalLanguageSchema() AutoTaggingSpecificationSchemaResource {
	// OriginalLanguageSpec emits the same language select-options
	// list as CustomFormat LanguageSpec EXCEPT Original (-2) is
	// excluded (a series can't have "Original" as its source
	// language — the value only makes sense as a "use series
	// language" sentinel on QualityProfile audio).
	opts := languageSelectOptions()
	filtered := make([]SelectOption, 0, len(opts)-1)
	for _, o := range opts {
		if o.Value == -2 {
			continue
		}
		filtered = append(filtered, o)
	}
	return AutoTaggingSpecificationSchemaResource{
		Implementation:     "OriginalLanguageSpecification",
		ImplementationName: "Original Language",
		Fields: []FieldDef{{
			Order: 0, Name: "value", Label: "Language", Type: "select",
			Privacy: "normal", SelectOptions: filtered,
			Value: 0, ValueSet: true,
		}},
	}
}

func qualityProfileSchema() AutoTaggingSpecificationSchemaResource {
	return AutoTaggingSpecificationSchemaResource{
		Implementation:     "QualityProfileSpecification",
		ImplementationName: "Quality Profile",
		Fields: []FieldDef{{
			Order: 0, Name: "value", Label: "Quality Profile",
			Type: "qualityProfile", Privacy: "normal",
			Value: 0, ValueSet: true,
		}},
	}
}

func rootFolderSchema() AutoTaggingSpecificationSchemaResource {
	return AutoTaggingSpecificationSchemaResource{
		Implementation:     "RootFolderSpecification",
		ImplementationName: "Root Folder",
		Fields: []FieldDef{{
			Order: 0, Name: "value", Label: "Root Folder",
			Type: "rootFolder", Privacy: "normal",
		}},
	}
}

func statusSchema() AutoTaggingSpecificationSchemaResource {
	return AutoTaggingSpecificationSchemaResource{
		Implementation:     "StatusSpecification",
		ImplementationName: "Status",
		Fields: []FieldDef{{
			Order: 0, Name: "status", Label: "Status", Type: "select",
			Privacy: "normal",
			SelectOptions: []SelectOption{
				{Value: -1, Name: "Deleted", Order: -1},
				{Value: 0, Name: "Continuing", Order: 0},
				{Value: 1, Name: "Ended", Order: 1},
				{Value: 2, Name: "Upcoming", Order: 2},
			},
			Value: 0, ValueSet: true,
		}},
	}
}

func tagSchema() AutoTaggingSpecificationSchemaResource {
	return AutoTaggingSpecificationSchemaResource{
		Implementation:     "TagSpecification",
		ImplementationName: "Tag",
		Fields: []FieldDef{{
			Order: 0, Name: "value", Label: "Tag",
			Type: "seriesTag", Privacy: "normal",
			Value: 0, ValueSet: true,
		}},
	}
}

func yearSchema() AutoTaggingSpecificationSchemaResource {
	return AutoTaggingSpecificationSchemaResource{
		Implementation:     "YearSpecification",
		ImplementationName: "Year",
		Fields: []FieldDef{
			{
				Order: 0, Name: "min", Label: "Minimum Year",
				Type: "number", Privacy: "normal",
				Value: 0, ValueSet: true,
			},
			{
				Order: 1, Name: "max", Label: "Maximum Year",
				Type: "number", Privacy: "normal",
				Value: 0, ValueSet: true,
			},
		},
	}
}

func seriesTypeSchema() AutoTaggingSpecificationSchemaResource {
	return AutoTaggingSpecificationSchemaResource{
		Implementation:     "SeriesTypeSpecification",
		ImplementationName: "Series Type",
		Fields: []FieldDef{{
			Order: 0, Name: "value", Label: "Series Type", Type: "select",
			Privacy: "normal",
			SelectOptions: []SelectOption{
				{Value: 0, Name: "Standard", Order: 0},
				{Value: 1, Name: "Daily", Order: 1},
				{Value: 2, Name: "Anime", Order: 2},
			},
			Value: 0, ValueSet: true,
		}},
	}
}

// AutoTaggingSchemaHandler returns the http.Handler for
// GET /api/v3/autotagging/schema. Mirrors
// `AutoTaggingController.GetTemplates` at
// `src/Sonarr.Api.V3/AutoTagging/AutoTaggingController.cs:92-97`.
// Emits the canonical 11-spec table; no presets (the C# controller
// returns the schema list verbatim, no preset merging).
func AutoTaggingSchemaHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(autoTaggingSchemas)
		_, _ = w.Write(buf)
	})
}
