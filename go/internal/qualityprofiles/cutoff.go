package qualityprofiles

import "sonarr.tv/sonarr/internal/quality"

// QualitiesBelowCutoff returns the quality IDs that rank BELOW the
// profile's effective cutoff — the set an episode file's quality must
// fall into for the episode to be "cutoff unmet". Mirrors the
// per-profile half of C# `EpisodeCutoffService.EpisodesWhereCutoffUnmet`
// at `src/NzbDrone.Core/Tv/EpisodeCutoffService.cs:26-50`:
//
//	cutoff       = UpgradeAllowed ? Cutoff : FirststAllowedQuality().Id
//	cutoffIndex  = GetIndex(cutoff)
//	belowCutoff  = Items.Take(cutoffIndex.Index)          // items ranked below
//	qualityIds   = belowCutoff.SelectMany(GetQualities).Select(Id)
//
// Profile Items are ordered worst→best, so `Items[:cutoffIndex]` is the
// run of qualities strictly below the cutoff. A group item flattens to
// all its member quality IDs (C# `GetQualities()`).
//
// Returns an empty (nil) slice when nothing ranks below the cutoff
// (cutoff is the lowest allowed quality), when the cutoff quality isn't
// found in the profile, or — for the !UpgradeAllowed branch — when the
// profile has no allowed quality at all. The C# version throws on the
// last case (`Items.First(...)`); the Go port returns empty so a
// malformed profile doesn't abort the whole cutoff sweep.
func QualitiesBelowCutoff(p *Profile) ([]int, error) {
	if p == nil {
		return nil, nil
	}
	items, err := p.ParsedItems()
	if err != nil {
		return nil, err
	}

	cutoffID := p.Cutoff
	if !p.UpgradeAllowed {
		// When upgrades are disabled the effective cutoff is the first
		// allowed quality — anything below it is unmet (C# `:34`).
		q, ok := firstAllowedQuality(items)
		if !ok {
			return nil, nil
		}
		cutoffID = q.ID
	}

	idx, ok := LookupIndexByID(items, cutoffID, false)
	if !ok {
		return nil, nil
	}

	var ids []int
	for _, item := range items[:idx.Index] {
		for _, q := range item.GetQualities() {
			ids = append(ids, q.ID)
		}
	}
	return ids, nil
}

// firstAllowedQuality ports `QualityProfile.FirststAllowedQuality()` at
// `src/NzbDrone.Core/Profiles/Qualities/QualityProfile.cs:25-37` — the
// first Allowed item's quality (or, for an allowed group, its first
// member's quality). Returns ok=false when no item is allowed (the C#
// version would throw on the empty `First`).
func firstAllowedQuality(items []QualityItem) (quality.Quality, bool) {
	for _, item := range items {
		if !item.Allowed {
			continue
		}
		if item.Quality.ID > 0 {
			return item.Quality, true
		}
		if len(item.Items) > 0 {
			return item.Items[0].Quality, true
		}
	}
	return quality.Quality{}, false
}
