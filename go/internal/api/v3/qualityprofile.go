package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/qualityprofiles"
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
			http.Error(w, fmt.Sprintf("qualityprofiles: %v", err), http.StatusInternalServerError)
			return
		}
		formatRows, err := formats.All(r.Context())
		if err != nil {
			http.Error(w, fmt.Sprintf("customformats: %v", err), http.StatusInternalServerError)
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
				http.Error(w, fmt.Sprintf("qualityprofiles: parse %d: %v", p.ID, err), http.StatusInternalServerError)
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
