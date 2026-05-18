package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/releaseprofiles"
)

// ReleaseProfileResource mirrors
// `src/Sonarr.Api.V3/Profiles/Release/ReleaseProfileResource.cs`.
//
// C# input accepts Required/Ignored as string, list, or JArray for
// backward compatibility (see ReleaseProfileResource.cs:15-17 +
// RestrictionResourceMapper.ParseArray). Output is always a
// List<string> (line 45-46: `Required ?? new List<string>()`), so the
// Go wire type for output is `[]string`. The POST/PUT polymorphic
// input handling lands in a follow-up slice when those methods port.
//
// Field-source mapping (C# → Go):
//
//	id                  → Profile.ID
//	name                → Profile.Name
//	enabled             → Profile.Enabled
//	required            → Profile.Required (?? [])
//	ignored             → Profile.Ignored  (?? [])
//	airDateRestriction  → Profile.AirDateRestriction
//	airDateGracePeriod  → Profile.AirDateGracePeriod
//	indexerId           → Profile.IndexerIDs.FirstOrDefault(0)
//	tags                → Profile.Tags  (HashSet → []int)
//	excludedTags        → Profile.ExcludedTags  (HashSet → []int)
type ReleaseProfileResource struct {
	ID                 int      `json:"id"`
	Name               string   `json:"name"`
	Enabled            bool     `json:"enabled"`
	Required           []string `json:"required"`
	Ignored            []string `json:"ignored"`
	AirDateRestriction bool     `json:"airDateRestriction"`
	AirDateGracePeriod int      `json:"airDateGracePeriod"`
	IndexerID          int      `json:"indexerId"`
	Tags               []int    `json:"tags"`
	ExcludedTags       []int    `json:"excludedTags"`
}

// ReleaseProfileLister is the read side the handler depends on.
// *releaseprofiles.Service satisfies it via All.
type ReleaseProfileLister interface {
	All(ctx context.Context) ([]releaseprofiles.Profile, error)
}

// ReleaseProfileHandler returns the http.Handler for
// GET /api/v3/releaseprofile. Mirrors
// `ReleaseProfileController.GetAll` at
// `src/Sonarr.Api.V3/Profiles/Release/ReleaseProfileController.cs:82-86`.
//
// Empty DB returns an initialised slice so the response body is `[]`.
func ReleaseProfileHandler(lister ReleaseProfileLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := lister.All(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("releaseprofiles: %w", err), nil)
			return
		}
		out := make([]ReleaseProfileResource, 0, len(rows))
		for _, row := range rows {
			out = append(out, toReleaseProfileResource(row))
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

func toReleaseProfileResource(p releaseprofiles.Profile) ReleaseProfileResource {
	r := ReleaseProfileResource{
		ID:                 p.ID,
		Name:               p.Name,
		Enabled:            p.Enabled,
		Required:           p.Required,
		Ignored:            p.Ignored,
		AirDateRestriction: p.AirDateRestriction,
		AirDateGracePeriod: p.AirDateGracePeriod,
		Tags:               p.Tags,
		ExcludedTags:       p.ExcludedTags,
	}
	// Match the C# `?? new List<string>()` guard — both fields are
	// non-omitempty on the wire and must serialise as `[]`, not
	// `null`, when the row stored a nil slice.
	if r.Required == nil {
		r.Required = []string{}
	}
	if r.Ignored == nil {
		r.Ignored = []string{}
	}
	if r.Tags == nil {
		r.Tags = []int{}
	}
	if r.ExcludedTags == nil {
		r.ExcludedTags = []int{}
	}
	// IndexerId = IndexerIds.FirstOrDefault(0) — collapse the slice
	// to a single int. The Go port stores IndexerIDs as a list for
	// schema future-proofing; today the API only exposes the first.
	if len(p.IndexerIDs) > 0 {
		r.IndexerID = p.IndexerIDs[0]
	}
	return r
}
