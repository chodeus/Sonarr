package series

import (
	"context"
	"errors"
	"fmt"
)

// ErrPathInUse is returned by [AddValidator.Validate] when another
// row already claims the candidate Path. Mirrors the FluentValidation
// failure produced by `SeriesPathValidator` at
// `src/NzbDrone.Core/Validation/Paths/SeriesPathValidator.cs:18,33`
// (default-message template:
// "Path '{path}' is already configured for another series").
var ErrPathInUse = errors.New("series: path already configured for another series")

// ErrTitleSlugInUse is returned by [AddValidator.Validate] when
// another row already claims the candidate TitleSlug. Mirrors
// `SeriesTitleSlugValidator` at
// `src/NzbDrone.Core/Tv/SeriesTitleSlugValidator.cs:16-17,30-43`
// (default-message template:
// "Title slug '{slug}' is in use by series '{seriesTitle}'.
// Check the FAQ for more information").
var ErrTitleSlugInUse = errors.New("series: title slug already in use")

// AddValidator runs pre-insert duplicate checks against the candidate
// series row. Ports the non-path-shape subset of
// `src/NzbDrone.Core/Tv/AddSeriesValidator.cs:18-26`:
//
//   - SeriesPathValidator (Path collision against an existing row).
//   - SeriesTitleSlugValidator (TitleSlug collision).
//
// Deferred from the C# composite for follow-up slices: IsValidPath
// (path syntax), RootFolderValidator + SeriesAncestorValidator
// (rootfolder-tree containment / ancestor / mapped-network checks).
// Those need the rootfolders.Service + a portable path-shape oracle
// that knows the host OS; both can land separately and chain onto
// this struct's [AddValidator.Validate] call. The two checks here
// are the highest-value duplicate guards: they prevent the
// import-list batch (slice 5.J187) from silently overwriting an
// existing row via INSERT path-collision or producing two rows that
// share the same /api/v3/series/{slug} URL.
//
// Self-row exemption (`s.Key != instanceId` at
// `SeriesPathValidator.cs:33` + `s.Id != instanceId` at
// `SeriesTitleSlugValidator.cs:33`) is preserved: when the candidate
// row has a non-zero ID and the found row's ID matches, the conflict
// is suppressed. This lets [AddValidator.Validate] be re-used by
// future Update flows (PUT /api/v3/series/{id}) without flagging
// the row's own existing Path / TitleSlug as a conflict against
// itself.
type AddValidator struct {
	pathLookup PathLookup
	slugLookup SlugLookup
}

// PathLookup is the FindByPath dependency [AddValidator.Validate]
// reaches for. Implemented by [*Repo]; defined here so test doubles
// can stub without pulling the whole Repo into scope.
type PathLookup interface {
	FindByPath(ctx context.Context, path string) (*Series, error)
}

// SlugLookup is the FindByTitleSlug dependency [AddValidator.Validate]
// reaches for. Implemented by [*Repo].
type SlugLookup interface {
	FindByTitleSlug(ctx context.Context, slug string) (*Series, error)
}

// NewAddValidator wires the two lookups into a validator. Both
// arguments are non-optional — passing nil panics at construction
// rather than risking a silent skip mid-batch.
func NewAddValidator(pathLookup PathLookup, slugLookup SlugLookup) *AddValidator {
	if pathLookup == nil {
		panic("series: NewAddValidator: pathLookup is nil")
	}
	if slugLookup == nil {
		panic("series: NewAddValidator: slugLookup is nil")
	}
	return &AddValidator{pathLookup: pathLookup, slugLookup: slugLookup}
}

// Validate runs the two duplicate checks against `s`. Returns nil
// when both pass. On conflict, returns a wrapped sentinel
// ([ErrPathInUse] or [ErrTitleSlugInUse]) — callers should classify
// via `errors.Is`. Lookup errors other than [ErrNotFound] propagate
// as-is (the C# version would surface them as a 500 via the
// FluentValidation exception bubble).
//
// The Path check is skipped when s.Path is empty: the C# tree's
// `RuleFor(c => c.Path).Cascade(CascadeMode.Stop).IsValidPath()`
// composite returns true for a null PropertyValue at
// `SeriesPathValidator.cs:22-25`, and the PrepareForAdd flow leaves
// Path empty when no folder-name builder is available (tests,
// migrations).
//
// The TitleSlug check is skipped when s.TitleSlug is empty: the C#
// `SeriesTitleSlugValidator.cs:21-24` returns true for null. The
// Go-port import-list flow always derives TitleSlug from the
// upstream provider's response so this is rarely empty in practice
// (defensive only).
func (v *AddValidator) Validate(ctx context.Context, s *Series) error {
	if s == nil {
		return errors.New("series: AddValidator.Validate: nil series")
	}
	if s.Path != "" {
		if err := v.checkPath(ctx, s); err != nil {
			return err
		}
	}
	if s.TitleSlug != "" {
		if err := v.checkSlug(ctx, s); err != nil {
			return err
		}
	}
	return nil
}

func (v *AddValidator) checkPath(ctx context.Context, s *Series) error {
	existing, err := v.pathLookup.FindByPath(ctx, s.Path)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return fmt.Errorf("series: AddValidator: FindByPath: %w", err)
	}
	if existing.ID == s.ID {
		return nil
	}
	return fmt.Errorf("%w: path=%q in use by id=%d", ErrPathInUse, s.Path, existing.ID)
}

func (v *AddValidator) checkSlug(ctx context.Context, s *Series) error {
	existing, err := v.slugLookup.FindByTitleSlug(ctx, s.TitleSlug)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return fmt.Errorf("series: AddValidator: FindByTitleSlug: %w", err)
	}
	if existing.ID == s.ID {
		return nil
	}
	return fmt.Errorf("%w: slug=%q in use by series %q (id=%d)",
		ErrTitleSlugInUse, s.TitleSlug, existing.Title, existing.ID)
}
