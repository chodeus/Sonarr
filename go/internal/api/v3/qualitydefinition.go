package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/qualitydefinitions"
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
			http.Error(w, fmt.Sprintf("qualitydefinitions: %v", err), http.StatusInternalServerError)
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
