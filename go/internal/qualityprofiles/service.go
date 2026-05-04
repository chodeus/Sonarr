package qualityprofiles

import (
	"context"
	"errors"
	"fmt"

	"sonarr.tv/sonarr/internal/messaging"
)

// QualityProfileUpdatedEvent ports
// `Profiles/Qualities/QualityProfileUpdatedEvent.cs:1-7`. Carries the
// updated profile's ID so listeners (notably PendingReleaseService) can
// re-evaluate cached release decisions against the new profile shape.
//
// The C# class is `QualityProfileUpdatedEvent(int id)`; the Go port
// preserves the single-Id payload — callers don't need the full Profile
// to react.
type QualityProfileUpdatedEvent struct {
	ID int
}

// Service ports `QualityProfileService.cs:27-289`. Surface assembled
// across multiple slices:
//
//   - Add / Update / Get / All / Exists — straight CRUD passthroughs
//     to the Repo. Update publishes [QualityProfileUpdatedEvent].
//     (Phase 2.44.)
//   - GetDefaultProfile / SeedDefaults — typed-Items profile builder
//     and the 6-profile seed for fresh installs. Both depend on a
//     [FormatProvider] (set via [Service.SetFormatProvider]).
//     (Phase 2.46.)
//
// Still deferred:
//
//   - Delete with `QualityProfileInUseException` — depends on
//     ISeriesService.GetAllSeries and IImportListFactory.All; both
//     services unported.
//   - UpdateAllSizeLimits + CustomFormatAdded/Deleted handlers —
//     planned for the next slice; the typed wrappers from 2.45 are
//     the foundation.
//
// Cache strategy: none. The C# class doesn't cache the `All()` list
// either; callers go through to the DB on every call.
type Service struct {
	repo    *Repo
	bus     *messaging.Bus
	formats FormatProvider
}

// NewService binds a Repo + Bus into a QualityProfile Service. Pass
// `bus = nil` to skip event publishing (used by migrations and tests
// that don't care about the wire-up).
func NewService(r *Repo, bus *messaging.Bus) (*Service, error) {
	if r == nil {
		return nil, errors.New("qualityprofiles: nil repo")
	}
	return &Service{repo: r, bus: bus}, nil
}

// Add ports `QualityProfileService.Add(QualityProfile)` at `:54-57`.
// Straight Insert; no event published (the C# version doesn't publish
// here either — only Update does).
func (s *Service) Add(ctx context.Context, p *Profile) (*Profile, error) {
	if p == nil {
		return nil, errors.New("qualityprofiles: cannot add nil profile")
	}
	inserted, err := s.repo.Insert(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("qualityprofiles: insert: %w", err)
	}
	return inserted, nil
}

// Update ports `QualityProfileService.Update(QualityProfile)` at
// `:59-63`. Persists the row, then publishes
// [QualityProfileUpdatedEvent].
func (s *Service) Update(ctx context.Context, p *Profile) error {
	if p == nil {
		return errors.New("qualityprofiles: cannot update nil profile")
	}
	if err := s.repo.Update(ctx, p); err != nil {
		return fmt.Errorf("qualityprofiles: update: %w", err)
	}
	if s.bus != nil {
		_ = messaging.Publish(ctx, s.bus, QualityProfileUpdatedEvent{ID: p.ID})
	}
	return nil
}

// All ports `QualityProfileService.All()` at `:76-79`.
func (s *Service) All(ctx context.Context) ([]Profile, error) {
	rows, err := s.repo.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("qualityprofiles: All: %w", err)
	}
	return rows, nil
}

// Get ports `QualityProfileService.Get(int)` at `:81-84`.
func (s *Service) Get(ctx context.Context, id int) (*Profile, error) {
	return s.repo.Get(ctx, id)
}

// Exists ports `QualityProfileService.Exists(int)` at `:86-89`.
// Returns (true, nil) on hit, (false, nil) on missing-row, (false, err)
// on lower-level errors.
//
// The C# call to `_qualityProfileRepository.Exists(id)` goes through
// the base `BasicRepository.Exists`. The Go port doesn't have a
// generic Exists on `*repo.Repository` yet, so this surfaces via a Get
// + ErrNotFound classification — the row data is dropped on the floor
// once we've confirmed the row exists.
func (s *Service) Exists(ctx context.Context, id int) (bool, error) {
	_, err := s.repo.Get(ctx, id)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, ErrNotFound):
		return false, nil
	default:
		return false, fmt.Errorf("qualityprofiles: exists(%d): %w", id, err)
	}
}
