package qualityprofiles

import (
	"context"
	"errors"
	"fmt"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/quality"
)

// FormatProvider is the slice of `customformats.Service` (or any
// equivalent) that QualityProfileService needs. Mirrors the C#
// `ICustomFormatService.All()` reach at
// `QualityProfileService.cs:242` — only the `All` shape is exercised.
//
// Tests inject stubs returning a fixed list; the production wiring
// passes `*customformats.Service`, which already satisfies this
// interface (its `All(ctx) ([]*customformats.Format, error)` matches
// signature exactly).
type FormatProvider interface {
	All(ctx context.Context) ([]*customformats.Format, error)
}

// SetFormatProvider attaches a [FormatProvider] to the Service.
// Required by [Service.GetDefaultProfile] and [Service.SeedDefaults] —
// both call into customformats to populate per-profile FormatItems
// scaffolding.
//
// The default constructor leaves this nil; the host wires it up after
// constructing both services. Tests inject a stub.
func (s *Service) SetFormatProvider(fp FormatProvider) {
	s.formats = fp
}

// GetDefaultProfile ports
// `QualityProfileService.GetDefaultProfile(name, cutoff, allowed)` at
// `QualityProfileService.cs:193-260`.
//
// Builds a Profile by walking `quality.DefaultDefinitions`, grouped by
// Weight:
//
//   - Singleton groups (one Quality at a Weight) become a flat
//     [QualityItem] with the Quality's `Allowed` flag set to whether
//     it appears in `allowed`.
//   - Multi-Quality groups become a parent [QualityItem] with `Id`
//     starting at 1000, `Name` set to the group's GroupName, and
//     nested children. The group's `Allowed` is true iff ANY member
//     appears in `allowed` — matches the C# `groupAllowed` at `:217`.
//
// `cutoff` resolution: passing nil yields `Quality.Unknown.ID` per
// the C# null-coalesce at `:198`. When the cutoff Quality belongs to
// a group, the returned Profile's `Cutoff` is replaced with the
// group's synthetic Id (matches `:234-237`).
//
// FormatItems are populated from the FormatProvider — every known
// CustomFormat gets a [FormatItem] with `Score=0`, mirroring `:242-246`.
//
// The returned Profile is unsaved (no Id assigned yet); pass it to
// [Service.Add] to persist.
func (s *Service) GetDefaultProfile(ctx context.Context, name string, cutoff *quality.Quality, allowed ...quality.Quality) (*Profile, error) {
	if s.formats == nil {
		return nil, errors.New("qualityprofiles: GetDefaultProfile requires a FormatProvider; call SetFormatProvider first")
	}

	profileCutoffID := quality.Unknown.ID
	if cutoff != nil {
		profileCutoffID = cutoff.ID
	}
	allowedSet := make(map[int]bool, len(allowed))
	for _, q := range allowed {
		allowedSet[q.ID] = true
	}

	groups := groupByWeight(quality.DefaultDefinitions())
	items := make([]QualityItem, 0, len(groups))
	groupID := 1000

	for _, g := range groups {
		if len(g) == 1 {
			d := g[0]
			items = append(items, QualityItem{
				Quality:       d.Quality,
				Allowed:       allowedSet[d.Quality.ID],
				MinSize:       d.MinSize,
				MaxSize:       d.MaxSize,
				PreferredSize: d.PreferredSize,
			})
			continue
		}

		// Multi-Quality group.
		groupAllowed := false
		for _, d := range g {
			if allowedSet[d.Quality.ID] {
				groupAllowed = true
				break
			}
		}

		nested := make([]QualityItem, 0, len(g))
		for _, d := range g {
			nested = append(nested, QualityItem{
				Quality:       d.Quality,
				Allowed:       groupAllowed,
				MinSize:       d.MinSize,
				MaxSize:       d.MaxSize,
				PreferredSize: d.PreferredSize,
			})
		}

		items = append(items, QualityItem{
			ID:      groupID,
			Name:    g[0].GroupName,
			Items:   nested,
			Allowed: groupAllowed,
		})

		// If the user-passed cutoff is in this group, swap to the
		// synthetic group Id. Mirrors `:234-237`.
		for _, d := range g {
			if d.Quality.ID == profileCutoffID {
				profileCutoffID = groupID
				break
			}
		}
		groupID++
	}

	formats, err := s.formats.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("qualityprofiles: GetDefaultProfile: load formats: %w", err)
	}
	formatItems := make([]FormatItem, 0, len(formats))
	for _, f := range formats {
		formatItems = append(formatItems, FormatItem{FormatID: f.ID, Score: 0})
	}

	p := &Profile{
		Name:                  name,
		Cutoff:                profileCutoffID,
		MinFormatScore:        0,
		CutoffFormatScore:     0,
		MinUpgradeFormatScore: 1,
	}
	if err := p.SetItems(items); err != nil {
		return nil, err
	}
	if err := p.SetFormatItems(formatItems); err != nil {
		return nil, err
	}
	return p, nil
}

// SeedDefaults ports the C# `Handle(ApplicationStartedEvent)` at
// `QualityProfileService.cs:91-157`. If any QualityProfile rows exist,
// the seed is a no-op (mirrors `if (All().Any()) return;`). Otherwise,
// inserts the 6 default profiles in C# order:
//
//   - Any (cutoff SDTV)
//   - SD (cutoff SDTV)
//   - HD-720p (cutoff HDTV720p)
//   - HD-1080p (cutoff HDTV1080p)
//   - Ultra-HD (cutoff HDTV2160p)
//   - HD - 720p/1080p (cutoff HDTV720p)
//
// The C# version's `Logger.Info("Setting up default quality profiles")`
// is omitted; the Go host can log at the call site.
//
// Each profile is built via [Service.GetDefaultProfile] then persisted
// via [Service.Add]. The C# `AddDefaultProfile` private helper at
// `:283-288` is inlined here.
func (s *Service) SeedDefaults(ctx context.Context) error {
	if s.formats == nil {
		return errors.New("qualityprofiles: SeedDefaults requires a FormatProvider; call SetFormatProvider first")
	}
	all, err := s.All(ctx)
	if err != nil {
		return fmt.Errorf("qualityprofiles: SeedDefaults: list: %w", err)
	}
	if len(all) > 0 {
		return nil
	}

	cutoffSDTV := quality.SDTV
	cutoff720 := quality.HDTV720p
	cutoff1080 := quality.HDTV1080p
	cutoff2160 := quality.HDTV2160p

	defaults := []struct {
		name    string
		cutoff  *quality.Quality
		allowed []quality.Quality
	}{
		{
			"Any", &cutoffSDTV,
			[]quality.Quality{
				quality.SDTV, quality.WEBRip480p, quality.WEBDL480p,
				quality.DVD, quality.Bluray480p, quality.Bluray576p,
				quality.HDTV720p, quality.HDTV1080p,
				quality.WEBRip720p, quality.WEBDL720p,
				quality.WEBRip1080p, quality.WEBDL1080p,
				quality.Bluray720p, quality.Bluray1080p,
			},
		},
		{
			"SD", &cutoffSDTV,
			[]quality.Quality{
				quality.SDTV, quality.WEBRip480p, quality.WEBDL480p,
				quality.DVD, quality.Bluray480p, quality.Bluray576p,
			},
		},
		{
			"HD-720p", &cutoff720,
			[]quality.Quality{
				quality.HDTV720p, quality.WEBRip720p, quality.WEBDL720p,
				quality.Bluray720p,
			},
		},
		{
			"HD-1080p", &cutoff1080,
			[]quality.Quality{
				quality.HDTV1080p, quality.WEBRip1080p, quality.WEBDL1080p,
				quality.Bluray1080p,
			},
		},
		{
			"Ultra-HD", &cutoff2160,
			[]quality.Quality{
				quality.HDTV2160p, quality.WEBRip2160p, quality.WEBDL2160p,
				quality.Bluray2160p,
			},
		},
		{
			"HD - 720p/1080p", &cutoff720,
			[]quality.Quality{
				quality.HDTV720p, quality.HDTV1080p,
				quality.WEBRip720p, quality.WEBDL720p,
				quality.WEBRip1080p, quality.WEBDL1080p,
				quality.Bluray720p, quality.Bluray1080p,
			},
		},
	}

	for _, d := range defaults {
		p, err := s.GetDefaultProfile(ctx, d.name, d.cutoff, d.allowed...)
		if err != nil {
			return fmt.Errorf("qualityprofiles: SeedDefaults: build %q: %w", d.name, err)
		}
		if _, err := s.Add(ctx, p); err != nil {
			return fmt.Errorf("qualityprofiles: SeedDefaults: insert %q: %w", d.name, err)
		}
	}
	return nil
}

// groupByWeight buckets a DefaultDefinition slice by Weight, preserving
// first-occurrence order. Mirrors the C# `groupedQualites = ...
// .GroupBy(q => q.Weight)` at `:195` — the result iteration order is
// the order in which each Weight first appears.
func groupByWeight(defs []quality.DefaultDefinition) [][]quality.DefaultDefinition {
	indexByWeight := make(map[int]int)
	out := make([][]quality.DefaultDefinition, 0, len(defs))
	for _, d := range defs {
		idx, ok := indexByWeight[d.Weight]
		if !ok {
			indexByWeight[d.Weight] = len(out)
			out = append(out, []quality.DefaultDefinition{d})
			continue
		}
		out[idx] = append(out[idx], d)
	}
	return out
}
