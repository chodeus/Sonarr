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

// Service ports `QualityProfileService.cs:27-289` minus the parts that
// require typed Items / FormatItems wrappers and other still-unported
// dependencies. What ships in this slice:
//
//   - Add / Update / Get / All / Exists — straight CRUD passthroughs to
//     the Repo. The `_qualityProfileRepository` calls in the C# class
//     translate one-to-one onto the existing `*Repo` from
//     `qualityprofiles.go`.
//   - QualityProfileUpdatedEvent published on every successful Update.
//     Mirrors `:62`.
//
// What's deferred to a follow-up slice (and noted in STATUS.md):
//
//   - Delete with `QualityProfileInUseException` — depends on
//     ISeriesService.GetAllSeries and IImportListFactory.All; both
//     services unported (see Phase 2 RemainingAreas).
//   - GetDefaultProfile / UpdateAllSizeLimits / CustomFormatAdded /
//     CustomFormatDeleted handlers — all mutate the typed
//     `Items` / `FormatItems` slices; the on-disk shape ships as opaque
//     JSON strings until the typed wrappers land in their own slice.
//   - ApplicationStartedEvent seed of the 6 default profiles
//     (Any/SD/HD-720p/HD-1080p/Ultra-HD/HD - 720p/1080p) — calls
//     GetDefaultProfile, so deferred with it.
//
// Cache strategy: none in this slice. The C# class doesn't cache the
// `All()` list either; callers go through to the DB on every call. When
// the typed-Items follow-up lands it can introduce a parsed-views cache
// if profiling justifies it.
type Service struct {
	repo *Repo
	bus  *messaging.Bus
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
