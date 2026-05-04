package qualityprofiles

import (
	"context"
	"errors"
	"fmt"
)

// SeriesProfileChecker is the slice of `ISeriesService` that
// QualityProfileService.Delete reaches for at
// `QualityProfileService.cs:67`. The C# version calls
// `_seriesService.GetAllSeries()` and walks every series checking
// `QualityProfileId == id`; the Go port uses the more efficient
// pre-projected map (`AllSeriesQualityProfiles` already exists on
// `series.Repo`/`series.Service`) which `*series.Service` satisfies
// out of the box.
type SeriesProfileChecker interface {
	AllSeriesQualityProfiles(ctx context.Context) (map[int]int, error)
}

// ImportListProfileChecker is the slice of `IImportListFactory`
// QualityProfileService.Delete reaches for at the same `:67`. The C#
// version calls `_importListFactory.All()` and walks every
// ImportListDefinition checking `QualityProfileId == id`. The Go
// port pulls the full ImportList → QualityProfileId projection so
// the checker can be implemented over a thin slice rather than
// dragging in the full ImportList model.
//
// Until ImportListFactory is ported, callers can pass a no-op stub
// (returns empty / nil) — fresh installs have no import lists, so
// this is functionally correct for the bootstrap flow.
type ImportListProfileChecker interface {
	AllQualityProfileIDs(ctx context.Context) ([]int, error)
}

// ErrQualityProfileInUse maps to C# `QualityProfileInUseException`
// thrown at `QualityProfileService.cs:70`. Callers (REST handlers)
// classify via `errors.Is`; the wrapped message carries the profile
// name for friendly user errors.
var ErrQualityProfileInUse = errors.New("qualityprofiles: profile is in use")

// SetSeriesProfileChecker attaches a [SeriesProfileChecker] so
// [Service.Delete] can validate against the series catalogue. Required.
func (s *Service) SetSeriesProfileChecker(c SeriesProfileChecker) {
	s.seriesChecker = c
}

// SetImportListProfileChecker attaches an [ImportListProfileChecker]
// so [Service.Delete] can validate against the import-list factory.
// Required.
func (s *Service) SetImportListProfileChecker(c ImportListProfileChecker) {
	s.importListChecker = c
}

// Delete ports `QualityProfileService.Delete(int id)` at `:65-74`.
// Validates that no series and no import-list reference the profile
// before deleting; throws (returns) [ErrQualityProfileInUse] when the
// check fails.
//
// Both checker interfaces must be wired via [SetSeriesProfileChecker]
// and [SetImportListProfileChecker] before this method is invoked. A
// missing checker is a programmer error — the C# DI graph guarantees
// these dependencies are non-null; the Go port has no equivalent
// at construction time, so a nil checker surfaces as a clear setup
// error rather than panicking at use time.
//
// Order of operations matches C# verbatim: in-use validation runs
// before any persistence side-effect; the profile is loaded before
// the delete so the error message can carry its Name.
func (s *Service) Delete(ctx context.Context, id int) error {
	if s.seriesChecker == nil || s.importListChecker == nil {
		return errors.New("qualityprofiles: Delete requires both SeriesProfileChecker and ImportListProfileChecker — call SetSeriesProfileChecker / SetImportListProfileChecker first")
	}

	seriesAssigned, err := s.seriesChecker.AllSeriesQualityProfiles(ctx)
	if err != nil {
		return fmt.Errorf("qualityprofiles: Delete: load series profiles: %w", err)
	}
	for _, profileID := range seriesAssigned {
		if profileID == id {
			return s.inUseError(ctx, id)
		}
	}

	importListAssigned, err := s.importListChecker.AllQualityProfileIDs(ctx)
	if err != nil {
		return fmt.Errorf("qualityprofiles: Delete: load import-list profiles: %w", err)
	}
	for _, profileID := range importListAssigned {
		if profileID == id {
			return s.inUseError(ctx, id)
		}
	}

	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("qualityprofiles: Delete: %w", err)
	}
	return nil
}

// inUseError builds the in-use error with the profile's Name embedded
// (mirrors the C# `throw new QualityProfileInUseException(profile.Name)`
// at `:70`). On lookup failure (e.g. the profile was concurrently
// deleted) the error falls back to just the ID — the caller still
// gets `ErrQualityProfileInUse` via errors.Is.
func (s *Service) inUseError(ctx context.Context, id int) error {
	profile, err := s.repo.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("%w: id=%d", ErrQualityProfileInUse, id)
	}
	return fmt.Errorf("%w: %q (id=%d)", ErrQualityProfileInUse, profile.Name, id)
}
