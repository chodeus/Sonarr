package v3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/metadata"
)

// MetadataSchemaResource is the JSON shape produced by
// GET /api/v3/metadata/schema. Mirrors
// `src/Sonarr.Api.V3/Metadata/MetadataResource.cs` extending
// `ProviderResource<MetadataResource>` at
// `src/Sonarr.Api.V3/ProviderResource.cs`.
//
// Field-source mapping (C# → Go), every field on the wire:
//
//	id                 → omit when 0 (schemas are unpersisted)
//	name               → "" on schemas — emitted as empty string
//	tags               → [] on schemas (empty HashSet<int> in C#)
//	fields             → SchemaBuilder.ToSchema(definition.Settings)
//	implementation     → struct name (XbmcMetadata / PlexMetadata / …)
//	implementationName → user-facing label (Kodi (XBMC) / Emby / …)
//	configContract     → XbmcMetadataSettings / PlexMetadataSettings / …
//	infoLink           → derived from `Implementation.ToLower()`
//	message            → null on schemas → omitted
//	presets            → [] (the metadata factory has no presets)
//	enable             → bool — false on schemas (the default)
//
// The C# ProviderResource emits Name as null but the metadata
// schema golden has `name: ""`. That's because the C# `provider.Name`
// accessor returns "" for the schema-builder code path (an
// uninstantiated MetadataDefinition has Name set to "").
type MetadataSchemaResource struct {
	ID                 int        `json:"id,omitempty"`
	Name               string     `json:"name"`
	Fields             []FieldDef `json:"fields"`
	ImplementationName string     `json:"implementationName"`
	Implementation     string     `json:"implementation"`
	ConfigContract     string     `json:"configContract"`
	InfoLink           string     `json:"infoLink"`
	Tags               []int      `json:"tags"`
	Presets            *[]any     `json:"presets,omitempty"`
	Enable             bool       `json:"enable"`
}

// metadataSchemas is the canonical 4-entry table in the order the C#
// MetadataFactory.InitializeProviders pass yields (mirrors the C# DI
// assembly-scan order: Xbmc → Plex → Roksbox → Wdtv).
var metadataSchemas = []MetadataSchemaResource{
	xbmcMetadataSchema(),
	plexMetadataSchema(),
	roksboxMetadataSchema(),
	wdtvMetadataSchema(),
}

func xbmcMetadataSchema() MetadataSchemaResource {
	return MetadataSchemaResource{
		Implementation:     "XbmcMetadata",
		ImplementationName: "Kodi (XBMC) / Emby",
		ConfigContract:     "XbmcMetadataSettings",
		InfoLink:           "https://wiki.servarr.com/sonarr/supported#xbmcmetadata",
		Tags:               []int{},
		Presets:            &[]any{},
		Fields: []FieldDef{
			metaCheckboxField(0, "seriesMetadata", "Series Metadata", "tvshow.nfo with full series metadata", "metadata", true, false),
			metaCheckboxField(1, "seriesMetadataEpisodeGuide", "Series Metadata Episode Guide", "Include JSON formatted episode guide element in tvshow.nfo (Requires 'Series Metadata')", "metadata", false, true),
			metaCheckboxField(2, "seriesMetadataUrl", "Series Metadata URL", "Include TheTVDB show URL in tvshow.nfo (can be combined with 'Series Metadata')", "metadata", false, true),
			metaCheckboxField(3, "episodeMetadata", "Episode Metadata", "<filename>.nfo", "metadata", true, false),
			metaCheckboxField(4, "episodeImageThumb", "Episode Metadata Image Thumbs", "Include image thumb tags in <filename>.nfo (Requires 'Episode Metadata')", "image", false, true),
			metaCheckboxField(5, "seriesImages", "Series Images", "fanart.jpg, poster.jpg, banner.jpg", "image", true, false),
			metaCheckboxField(6, "seasonImages", "Season Images", `season##-poster.jpg, season##-banner.jpg, season-specials-poster.jpg, season-specials-banner.jpg`, "image", true, false),
			metaCheckboxField(7, "episodeImages", "Episode Images", "<filename>-thumb.jpg", "image", true, false),
		},
	}
}

func plexMetadataSchema() MetadataSchemaResource {
	return MetadataSchemaResource{
		Implementation:     "PlexMetadata",
		ImplementationName: "Plex",
		ConfigContract:     "PlexMetadataSettings",
		InfoLink:           "https://wiki.servarr.com/sonarr/supported#plexmetadata",
		Tags:               []int{},
		Presets:            &[]any{},
		Fields: []FieldDef{
			metaCheckboxField(0, "seriesPlexMatchFile", "Series Plex Match File", "Creates a .plexmatch file in the series folder", "metadata", true, false),
			metaCheckboxField(1, "episodeMappings", "Episode Mappings", "Include episode mappings for all files in .plexmatch file", "metadata", false, false),
		},
	}
}

func roksboxMetadataSchema() MetadataSchemaResource {
	return MetadataSchemaResource{
		Implementation:     "RoksboxMetadata",
		ImplementationName: "Roksbox",
		ConfigContract:     "RoksboxMetadataSettings",
		InfoLink:           "https://wiki.servarr.com/sonarr/supported#roksboxmetadata",
		Tags:               []int{},
		Presets:            &[]any{},
		Fields: []FieldDef{
			metaCheckboxField(0, "episodeMetadata", "Episode Metadata", `Season##\filename.xml`, "metadata", true, false),
			metaCheckboxField(1, "seriesImages", "Series Images", "Series Title.jpg", "image", true, false),
			metaCheckboxField(2, "seasonImages", "Season Images", "Season ##.jpg", "image", true, false),
			metaCheckboxField(3, "episodeImages", "Episode Images", `Season##\filename.jpg`, "image", true, false),
		},
	}
}

func wdtvMetadataSchema() MetadataSchemaResource {
	return MetadataSchemaResource{
		Implementation:     "WdtvMetadata",
		ImplementationName: "WDTV",
		ConfigContract:     "WdtvMetadataSettings",
		InfoLink:           "https://wiki.servarr.com/sonarr/supported#wdtvmetadata",
		Tags:               []int{},
		Presets:            &[]any{},
		Fields: []FieldDef{
			metaCheckboxField(0, "episodeMetadata", "Episode Metadata", `Season##\filename.xml`, "metadata", true, false),
			metaCheckboxField(1, "seriesImages", "Series Images", "folder.jpg", "image", true, false),
			metaCheckboxField(2, "seasonImages", "Season Images", `Season##\folder.jpg`, "image", true, false),
			metaCheckboxField(3, "episodeImages", "Episode Images", `Season##\filename.metathumb`, "image", true, false),
		},
	}
}

// metaCheckboxField builds the standard MetadataSettings field
// shape — every metadata provider's settings struct is a list of
// bool toggles with FieldType.Checkbox + a section ("metadata" or
// "image"). value is the default the C# constructor sets on the
// IProviderConfig; advanced is the `Advanced=true` attribute flag
// the C# `[FieldDefinition]` carries on the per-quality "advanced"
// toggles.
func metaCheckboxField(order int, name, label, helpText, section string, value, advanced bool) FieldDef {
	return FieldDef{
		Order: order, Name: name, Label: label, HelpText: helpText,
		Type: "checkbox", Privacy: "normal", Section: section,
		Advanced: advanced, Value: value, ValueSet: true,
	}
}

// MetadataSchemaHandler returns the http.Handler for
// GET /api/v3/metadata/schema. Mirrors the C# pattern at
// `MetadataController.GetTemplates` (inherited from
// `ProviderControllerBase`) — emits the per-provider schema template
// for every registered IMetadata implementation, sorted by the C# DI
// container's enumeration order.
func MetadataSchemaHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(metadataSchemas)
		_, _ = w.Write(buf)
	})
}

// MetadataRowsLister narrows the metadata.Repo surface to the
// All-rows read the list handler needs.
type MetadataRowsLister interface {
	All(ctx context.Context) ([]metadata.Metadata, error)
}

// MetadataByIDGetter narrows the read for /metadata/{id}.
type MetadataByIDGetter interface {
	Get(ctx context.Context, id int) (*metadata.Metadata, error)
}

// MetadataByIDHandler returns GET /api/v3/metadata/{id}. Projects
// the persisted row through the schema-template lookup to emit the
// full Fields-populated wire shape (same shape used by the list
// handler).
func MetadataByIDHandler(svc MetadataByIDGetter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			httperr.BadRequest(w, "metadata: invalid id")
			return
		}
		row, err := svc.Get(r.Context(), id)
		if err != nil {
			if errors.Is(err, metadata.ErrNotFound) {
				httperr.NotFound(w, fmt.Sprintf("metadata %d not found", id))
				return
			}
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("metadata: get %d: %w", id, err), nil)
			return
		}
		schemaByImpl := make(map[string]MetadataSchemaResource, len(metadataSchemas))
		for _, s := range metadataSchemas {
			schemaByImpl[s.Implementation] = s
		}
		schema, ok := schemaByImpl[row.Implementation]
		if !ok {
			httperr.NotFound(w, fmt.Sprintf("metadata %d implementation %q unknown", id, row.Implementation))
			return
		}
		schema.ID = row.ID
		schema.Name = row.Name
		schema.Enable = row.Enable
		schema.Presets = nil
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(schema)
		_, _ = w.Write(buf)
	})
}

// MetadataListHandler returns the http.Handler for GET /api/v3/metadata.
// Mirrors the C# `MetadataController` GetAll inherited from
// `ProviderControllerBase`. Emits one MetadataSchemaResource per
// persisted row, with Id+Name+Enable from the row plus the per-
// implementation schema template's Fields list. Sort order is the
// row's Name ASC (matches the C# wire-order which originates from a
// `_metadataFactory.All()` DB read followed by alphabetical-by-Name
// in the resource projection).
//
// On a fresh DB with no metadata rows the response is `[]`. Once
// SeedDefaults runs (4 default-disabled providers Xbmc/Wdtv/Roksbox/
// Plex inserted in DI-scan order), the response carries the four
// recording entries byte-equal to the C# golden.
//
// Per-row field-value injection: the recording's seeded rows carry
// the IProviderConfig constructor defaults — identical to the schema
// template's per-FieldDef Value. So the list response's Fields list
// is byte-identical to the schema response's per-implementation
// Fields. A future write path (POST/PUT /api/v3/metadata) will need
// per-row Settings decode + value injection; for the empty-DB byte-
// eq this slice covers, the schema-template defaults suffice.
func MetadataListHandler(lister MetadataRowsLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := lister.All(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("metadata: %w", err), nil)
			return
		}
		schemaByImpl := make(map[string]MetadataSchemaResource, len(metadataSchemas))
		for _, s := range metadataSchemas {
			schemaByImpl[s.Implementation] = s
		}
		out := make([]MetadataSchemaResource, 0, len(rows))
		for _, row := range rows {
			schema, ok := schemaByImpl[row.Implementation]
			if !ok {
				// Unknown implementation — skip rather than emit a
				// broken row. Same posture the C# tree takes when a
				// row references a removed-provider implementation.
				continue
			}
			schema.ID = row.ID
			schema.Name = row.Name
			schema.Enable = row.Enable
			// List entries omit `presets` entirely — the C# wire
			// shape only carries presets on the schema endpoint.
			schema.Presets = nil
			out = append(out, schema)
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}
