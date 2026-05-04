package importlists

import (
	"context"
	"errors"
	"fmt"
)

// Service is the read-only slice of an eventual ImportListService port.
// At this stage it exposes only the methods needed to satisfy the
// `qualityprofiles.ImportListProfileChecker` bridge — Phase 4 will
// extend this with the full Provider lifecycle (Create/Update/Delete +
// per-Implementation runner wiring).
//
// The narrow surface here matches the established Phase 2 pattern:
// ship the integration point that downstream services need, defer the
// rest until the full surface is in scope.
type Service struct {
	repo *Repo
}

// NewService binds a Repo into a Service. The Repo is required — there
// is no useful "no-op" mode for the read methods.
func NewService(r *Repo) (*Service, error) {
	if r == nil {
		return nil, errors.New("importlists: nil repo")
	}
	return &Service{repo: r}, nil
}

// All ports `IImportListFactory.All()` — returns every persisted
// ImportListDefinition row. Phase 4 will wrap this with typed
// Settings/ConfigContract resolution; for now it's a straight
// passthrough.
func (s *Service) All(ctx context.Context) ([]ImportList, error) {
	rows, err := s.repo.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("importlists: All: %w", err)
	}
	return rows, nil
}

// AllQualityProfileIDs ports the C# slice
// `_importListFactory.All().Select(c => c.QualityProfileId)`. Returns
// the projection without inflating the typed
// ImportListDefinition view.
//
// Satisfies `qualityprofiles.ImportListProfileChecker` — pinned at
// compile time via a `var _` in `service_test.go`.
func (s *Service) AllQualityProfileIDs(ctx context.Context) ([]int, error) {
	return s.repo.AllQualityProfileIDs(ctx)
}
