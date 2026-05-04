package qualityprofiles

import (
	"context"
	"fmt"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/quality"
)

// QualityProfileSizeLimit ports
// `Profiles/Qualities/QualityProfileSizeLimit.cs`. Carries the per-
// quality size constraints a caller wants pushed across every
// QualityProfile's matching item.
//
// `Quality` identifies which item to update; `MinSize/MaxSize/PreferredSize`
// are nullable (the C# `double?`) so callers can update a subset.
type QualityProfileSizeLimit struct {
	Quality       quality.Quality
	MinSize       *float64
	MaxSize       *float64
	PreferredSize *float64
}

// UpdateAllSizeLimits ports
// `QualityProfileService.UpdateAllSizeLimits(...sizeLimits)` at
// `:262-281`. Walks every QualityProfile, finds the matching
// QualityItem (descending into a group if the top-level entry is a
// group), and overwrites its Min/Max/PreferredSize.
//
// Lookup uses `respectGroupOrder = true` so group-member matches
// produce a `{Index, GroupIndex}` tuple — the inner item is then
// resolved via `Items[Index].Items[GroupIndex]`.
//
// Persistence path: writes go directly through the Repo
// (`s.repo.Update`), bypassing `Service.Update` and therefore NOT
// publishing `QualityProfileUpdatedEvent`. Mirrors the C# semantic
// where `_qualityProfileRepository.UpdateMany(all)` at `:280` skips
// the event flow that the per-profile `Update(QualityProfile)` path
// uses. Size-only changes don't affect quality decisions, so consumers
// don't need to react.
//
// Missing-quality robustness: the C# version uses `GetIndex(...)`
// which silently returns `{0, 0}` on miss — meaning a quality not in
// the profile would corrupt the first slot's sizes. The Go port
// guards against this via [LookupIndexByID]'s explicit `(found bool)`;
// missing qualities are skipped, leaving the profile untouched for
// that size limit.
func (s *Service) UpdateAllSizeLimits(ctx context.Context, sizeLimits ...QualityProfileSizeLimit) error {
	all, err := s.All(ctx)
	if err != nil {
		return fmt.Errorf("qualityprofiles: UpdateAllSizeLimits: list: %w", err)
	}
	for i := range all {
		profile := &all[i]
		items, err := profile.ParsedItems()
		if err != nil {
			return fmt.Errorf("qualityprofiles: UpdateAllSizeLimits: parse profile %d: %w", profile.ID, err)
		}
		mutated := false
		for _, sl := range sizeLimits {
			idx, ok := LookupIndex(items, sl.Quality, true)
			if !ok {
				continue
			}
			top := &items[idx.Index]
			if top.IsGroup() {
				if idx.GroupIndex < 0 || idx.GroupIndex >= len(top.Items) {
					continue
				}
				inner := &top.Items[idx.GroupIndex]
				inner.MinSize = sl.MinSize
				inner.MaxSize = sl.MaxSize
				inner.PreferredSize = sl.PreferredSize
			} else {
				top.MinSize = sl.MinSize
				top.MaxSize = sl.MaxSize
				top.PreferredSize = sl.PreferredSize
			}
			mutated = true
		}
		if !mutated {
			continue
		}
		if err := profile.SetItems(items); err != nil {
			return fmt.Errorf("qualityprofiles: UpdateAllSizeLimits: re-marshal profile %d: %w", profile.ID, err)
		}
		if err := s.repo.Update(ctx, profile); err != nil {
			return fmt.Errorf("qualityprofiles: UpdateAllSizeLimits: persist profile %d: %w", profile.ID, err)
		}
	}
	return nil
}

// OnCustomFormatAdded ports
// `QualityProfileService.Handle(CustomFormatAddedEvent)` at `:159-173`.
// For every QualityProfile, prepends a `FormatItem{FormatID, Score: 0}`
// to FormatItems and persists via [Service.Update] (which publishes
// [QualityProfileUpdatedEvent] per profile, mirroring the C# `Update`
// loop at `:171`).
//
// Bus subscription wiring lives at the host: the handler entry point
// is exposed as a plain method so the host can `Subscribe` it to
// `customformats.CustomFormatAddedEvent`.
func (s *Service) OnCustomFormatAdded(ctx context.Context, format *customformats.Format) error {
	if format == nil {
		return nil
	}
	all, err := s.All(ctx)
	if err != nil {
		return fmt.Errorf("qualityprofiles: OnCustomFormatAdded: list: %w", err)
	}
	for i := range all {
		profile := &all[i]
		items, err := profile.ParsedFormatItems()
		if err != nil {
			return fmt.Errorf("qualityprofiles: OnCustomFormatAdded: parse profile %d: %w", profile.ID, err)
		}
		updated := append([]FormatItem{{FormatID: format.ID, Score: 0}}, items...)
		if err := profile.SetFormatItems(updated); err != nil {
			return fmt.Errorf("qualityprofiles: OnCustomFormatAdded: re-marshal profile %d: %w", profile.ID, err)
		}
		if err := s.Update(ctx, profile); err != nil {
			return fmt.Errorf("qualityprofiles: OnCustomFormatAdded: update profile %d: %w", profile.ID, err)
		}
	}
	return nil
}

// OnCustomFormatDeleted ports
// `QualityProfileService.Handle(CustomFormatDeletedEvent)` at `:175-191`.
// For every QualityProfile, filters the deleted format out of
// FormatItems. When the resulting list is empty, zeroes
// MinFormatScore + CutoffFormatScore and resets MinUpgradeFormatScore
// to 1 (matches the C# guard at `:182-186`).
//
// Each profile is persisted via [Service.Update], which publishes
// [QualityProfileUpdatedEvent] per profile.
func (s *Service) OnCustomFormatDeleted(ctx context.Context, format *customformats.Format) error {
	if format == nil {
		return nil
	}
	all, err := s.All(ctx)
	if err != nil {
		return fmt.Errorf("qualityprofiles: OnCustomFormatDeleted: list: %w", err)
	}
	for i := range all {
		profile := &all[i]
		items, err := profile.ParsedFormatItems()
		if err != nil {
			return fmt.Errorf("qualityprofiles: OnCustomFormatDeleted: parse profile %d: %w", profile.ID, err)
		}
		kept := make([]FormatItem, 0, len(items))
		for _, fi := range items {
			if fi.FormatID == format.ID {
				continue
			}
			kept = append(kept, fi)
		}
		if err := profile.SetFormatItems(kept); err != nil {
			return fmt.Errorf("qualityprofiles: OnCustomFormatDeleted: re-marshal profile %d: %w", profile.ID, err)
		}
		if len(kept) == 0 {
			profile.MinFormatScore = 0
			profile.CutoffFormatScore = 0
			profile.MinUpgradeFormatScore = 1
		}
		if err := s.Update(ctx, profile); err != nil {
			return fmt.Errorf("qualityprofiles: OnCustomFormatDeleted: update profile %d: %w", profile.ID, err)
		}
	}
	return nil
}
