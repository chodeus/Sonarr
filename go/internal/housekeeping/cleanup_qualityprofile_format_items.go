// CleanupQualityProfileFormatItems. Ports
// `src/NzbDrone.Core/Housekeeping/Housekeepers/CleanupQualityProfileFormatItems.cs:1-77`.
//
// Reconciles each QualityProfile's `FormatItems` JSON list against the
// live CustomFormats catalogue. The decision engine assumes the per-
// profile FormatItems list mirrors every CustomFormat one-to-one
// (with a Score field per spec); profiles drift out of sync when
// CustomFormats are added or deleted between sessions, and the C#
// layer that should keep them in sync isn't always reached
// (custom-format admin UI vs raw DB import). This task does the
// reconciliation:
//
//   - Drop FormatItems whose FormatID no longer matches a live
//     CustomFormat.
//   - Prepend a Score=0 FormatItem for any CustomFormat the profile
//     is currently missing.
//   - If the resulting list ends up empty, reset MinFormatScore=0,
//     CutoffFormatScore=0, MinUpgradeFormatScore=1 (matches C# `:65-69`).
//
// The C# port iterates `customFormats` as a Dictionary; .NET dicts
// preserve insertion order which mirrors `_customFormatRepository.All()`
// (sorted by Id ASC). The Go port uses the same order — `Insert(0, ...)`
// in C# is preserved as a slice prepend, so missing CFs end up at the
// FRONT of FormatItems in DESCENDING Id order (each prepend pushes
// earlier prepends down). This is preserved verbatim because the
// decision engine doesn't depend on FormatItems order — but matching
// the C# wire shape minimises diff churn against existing user
// databases.

package housekeeping

import (
	"context"
	"fmt"
	"log/slog"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/qualityprofiles"
)

// CleanupQualityProfileFormatItems is the housekeeper that reconciles
// QualityProfile.FormatItems against the live CustomFormats set.
type CleanupQualityProfileFormatItems struct {
	qpRepo *qualityprofiles.Repo
	cfRepo *customformats.Repo
	log    *slog.Logger
}

// NewCleanupQualityProfileFormatItems builds the housekeeper. Both
// repos must be non-nil; the C# DI graph wires both unconditionally
// and there's no useful fallback without one of them. log defaults to
// slog.Default.
func NewCleanupQualityProfileFormatItems(
	qpRepo *qualityprofiles.Repo,
	cfRepo *customformats.Repo,
	log *slog.Logger,
) (*CleanupQualityProfileFormatItems, error) {
	if qpRepo == nil {
		return nil, fmt.Errorf("housekeeping: CleanupQualityProfileFormatItems: nil qualityprofiles.Repo")
	}
	if cfRepo == nil {
		return nil, fmt.Errorf("housekeeping: CleanupQualityProfileFormatItems: nil customformats.Repo")
	}
	if log == nil {
		log = slog.Default()
	}
	return &CleanupQualityProfileFormatItems{qpRepo: qpRepo, cfRepo: cfRepo, log: log}, nil
}

// Name implements Task.
func (t *CleanupQualityProfileFormatItems) Name() string {
	return "CleanupQualityProfileFormatItems"
}

// Clean implements Task. Loads every CustomFormat + every
// QualityProfile, reconciles each profile's FormatItems list, and
// updates only the rows that actually drifted.
func (t *CleanupQualityProfileFormatItems) Clean(ctx context.Context) error {
	cfs, err := t.cfRepo.All(ctx)
	if err != nil {
		return fmt.Errorf("housekeeping: CleanupQualityProfileFormatItems: load custom formats: %w", err)
	}
	cfIDs := make(map[int]bool, len(cfs))
	for _, cf := range cfs {
		cfIDs[cf.ID] = true
	}

	profiles, err := t.qpRepo.All(ctx)
	if err != nil {
		return fmt.Errorf("housekeeping: CleanupQualityProfileFormatItems: load profiles: %w", err)
	}

	updated := 0
	for i := range profiles {
		profile := &profiles[i]
		previousItems, err := qualityprofiles.ParseFormatItems(profile.FormatItems)
		if err != nil {
			return fmt.Errorf("housekeeping: CleanupQualityProfileFormatItems: parse profile %d: %w", profile.ID, err)
		}

		// Filter: keep only items whose FormatID is in the live set.
		// FormatID == 0 is treated as corruption (CustomFormat IDs are
		// auto-incremented from 1) and dropped — matches C# `p.Format != null`.
		kept := make([]qualityprofiles.FormatItem, 0, len(previousItems))
		keptIDs := make(map[int]bool, len(previousItems))
		for _, item := range previousItems {
			if item.FormatID == 0 || !cfIDs[item.FormatID] {
				continue
			}
			// Defensive: skip duplicate FormatIDs in case of corruption
			// (the C# version doesn't dedup, but it would write back the
			// same dup; matching C# byte-for-byte isn't worth preserving
			// the bug here).
			if keptIDs[item.FormatID] {
				continue
			}
			kept = append(kept, item)
			keptIDs[item.FormatID] = true
		}

		// Prepend a Score=0 FormatItem for each CustomFormat the
		// profile is currently missing. Mirrors C# `:42-50`
		// (`Insert(0, new ProfileFormatItem { Format = customFormat.Value, Score = 0 })`).
		// `cfRepo.All` returns by Id ASC; iterating that order with
		// prepend gives final list with missing IDs in DESCENDING order
		// at the front, matching .NET Dictionary iteration semantics.
		for _, cf := range cfs {
			if keptIDs[cf.ID] {
				continue
			}
			kept = append([]qualityprofiles.FormatItem{{FormatID: cf.ID, Score: 0}}, kept...)
			keptIDs[cf.ID] = true
		}

		if !formatItemSetsDiffer(previousItems, kept) {
			continue
		}

		newJSON, err := qualityprofiles.MarshalFormatItems(kept)
		if err != nil {
			return fmt.Errorf("housekeeping: CleanupQualityProfileFormatItems: marshal profile %d: %w", profile.ID, err)
		}
		profile.FormatItems = newJSON

		// If the new list is empty, reset the score knobs to the
		// FormatItems-empty defaults. Mirrors C# `:65-69`.
		if len(kept) == 0 {
			profile.MinFormatScore = 0
			profile.CutoffFormatScore = 0
			profile.MinUpgradeFormatScore = 1
		}

		if err := t.qpRepo.Update(ctx, profile); err != nil {
			return fmt.Errorf("housekeeping: CleanupQualityProfileFormatItems: update profile %d: %w", profile.ID, err)
		}
		updated++
	}

	if updated > 0 {
		t.log.LogAttrs(ctx, slog.LevelInfo,
			"housekeeping: reconciled QualityProfile.FormatItems",
			slog.Int("profiles", updated),
			slog.Int("customFormats", len(cfs)))
	}
	return nil
}

// formatItemSetsDiffer reports whether the set of FormatIDs in `prev`
// and `next` is different. Mirrors C# `:53-58`: profile gets updated
// iff `ids.Except(previousIds).Any() || previousIds.Except(ids).Any()`.
// Score changes alone do NOT trigger an update — those are user-edited
// state the housekeeper must not stomp.
func formatItemSetsDiffer(prev, next []qualityprofiles.FormatItem) bool {
	if len(prev) != len(next) {
		return true
	}
	prevIDs := make(map[int]bool, len(prev))
	for _, p := range prev {
		prevIDs[p.FormatID] = true
	}
	for _, n := range next {
		if !prevIDs[n.FormatID] {
			return true
		}
	}
	return false
}
