package series_test

import (
	"context"
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/series"
)

// stubLookup is a tiny test double satisfying both
// [series.PathLookup] and [series.SlugLookup]. Each lookup returns
// either a synthetic row, [series.ErrNotFound], or a forced error.
type stubLookup struct {
	pathResult *series.Series
	pathErr    error
	slugResult *series.Series
	slugErr    error
}

func (s *stubLookup) FindByPath(_ context.Context, _ string) (*series.Series, error) {
	return s.pathResult, s.pathErr
}

func (s *stubLookup) FindByTitleSlug(_ context.Context, _ string) (*series.Series, error) {
	return s.slugResult, s.slugErr
}

func TestAddValidator_BothChecksPass(t *testing.T) {
	t.Parallel()
	stub := &stubLookup{pathErr: series.ErrNotFound, slugErr: series.ErrNotFound}
	v := series.NewAddValidator(stub, stub)
	if err := v.Validate(t.Context(), &series.Series{Path: "/tv/Show", TitleSlug: "show"}); err != nil {
		t.Fatalf("Validate: want nil, got %v", err)
	}
}

func TestAddValidator_PathCollision(t *testing.T) {
	t.Parallel()
	stub := &stubLookup{
		pathResult: &series.Series{ID: 42, Path: "/tv/Show"},
		slugErr:    series.ErrNotFound,
	}
	v := series.NewAddValidator(stub, stub)
	err := v.Validate(t.Context(), &series.Series{Path: "/tv/Show", TitleSlug: "show"})
	if !errors.Is(err, series.ErrPathInUse) {
		t.Fatalf("Validate: want ErrPathInUse, got %v", err)
	}
}

func TestAddValidator_PathCollision_SelfRowExempt(t *testing.T) {
	t.Parallel()
	stub := &stubLookup{
		pathResult: &series.Series{ID: 7, Path: "/tv/Show"},
		slugErr:    series.ErrNotFound,
	}
	v := series.NewAddValidator(stub, stub)
	// Candidate ID matches the found row's ID — Update flow shouldn't
	// flag self as a conflict.
	if err := v.Validate(t.Context(), &series.Series{ID: 7, Path: "/tv/Show", TitleSlug: "show"}); err != nil {
		t.Fatalf("Validate: self-row exempt, want nil, got %v", err)
	}
}

func TestAddValidator_SlugCollision(t *testing.T) {
	t.Parallel()
	stub := &stubLookup{
		pathErr:    series.ErrNotFound,
		slugResult: &series.Series{ID: 99, Title: "The Show", TitleSlug: "show"},
	}
	v := series.NewAddValidator(stub, stub)
	err := v.Validate(t.Context(), &series.Series{Path: "/tv/Show", TitleSlug: "show"})
	if !errors.Is(err, series.ErrTitleSlugInUse) {
		t.Fatalf("Validate: want ErrTitleSlugInUse, got %v", err)
	}
}

func TestAddValidator_SlugCollision_SelfRowExempt(t *testing.T) {
	t.Parallel()
	stub := &stubLookup{
		pathErr:    series.ErrNotFound,
		slugResult: &series.Series{ID: 7, TitleSlug: "show"},
	}
	v := series.NewAddValidator(stub, stub)
	if err := v.Validate(t.Context(), &series.Series{ID: 7, Path: "/tv/Show", TitleSlug: "show"}); err != nil {
		t.Fatalf("Validate: self-row exempt, want nil, got %v", err)
	}
}

func TestAddValidator_EmptyPath_SkipsPathCheck(t *testing.T) {
	t.Parallel()
	// pathLookup would return a conflict if called — but Path is
	// empty so the check is skipped per the C# null-PropertyValue
	// guard.
	stub := &stubLookup{
		pathResult: &series.Series{ID: 42, Path: "/anywhere"},
		slugErr:    series.ErrNotFound,
	}
	v := series.NewAddValidator(stub, stub)
	if err := v.Validate(t.Context(), &series.Series{Path: "", TitleSlug: "show"}); err != nil {
		t.Fatalf("Validate: empty path, want nil, got %v", err)
	}
}

func TestAddValidator_EmptySlug_SkipsSlugCheck(t *testing.T) {
	t.Parallel()
	stub := &stubLookup{
		pathErr:    series.ErrNotFound,
		slugResult: &series.Series{ID: 99, TitleSlug: "anything"},
	}
	v := series.NewAddValidator(stub, stub)
	if err := v.Validate(t.Context(), &series.Series{Path: "/tv/Show", TitleSlug: ""}); err != nil {
		t.Fatalf("Validate: empty slug, want nil, got %v", err)
	}
}

func TestAddValidator_PathLookupError_Propagates(t *testing.T) {
	t.Parallel()
	boom := errors.New("db down")
	stub := &stubLookup{pathErr: boom, slugErr: series.ErrNotFound}
	v := series.NewAddValidator(stub, stub)
	err := v.Validate(t.Context(), &series.Series{Path: "/tv/Show", TitleSlug: "show"})
	if !errors.Is(err, boom) {
		t.Fatalf("Validate: want wrapped boom, got %v", err)
	}
}

func TestAddValidator_SlugLookupError_Propagates(t *testing.T) {
	t.Parallel()
	boom := errors.New("db down")
	stub := &stubLookup{pathErr: series.ErrNotFound, slugErr: boom}
	v := series.NewAddValidator(stub, stub)
	err := v.Validate(t.Context(), &series.Series{Path: "/tv/Show", TitleSlug: "show"})
	if !errors.Is(err, boom) {
		t.Fatalf("Validate: want wrapped boom, got %v", err)
	}
}

func TestAddValidator_NilSeries(t *testing.T) {
	t.Parallel()
	stub := &stubLookup{}
	v := series.NewAddValidator(stub, stub)
	if err := v.Validate(t.Context(), nil); err == nil {
		t.Fatal("Validate(nil): want error, got nil")
	}
}

func TestNewAddValidator_NilLookupPanics(t *testing.T) {
	t.Parallel()
	t.Run("nil path lookup", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("want panic")
			}
		}()
		series.NewAddValidator(nil, &stubLookup{})
	})
	t.Run("nil slug lookup", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("want panic")
			}
		}()
		series.NewAddValidator(&stubLookup{}, nil)
	})
}
