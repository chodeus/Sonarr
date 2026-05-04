package qualityprofiles_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/qualityprofiles"
)

// stubSeriesChecker implements SeriesProfileChecker with a fixed map.
type stubSeriesChecker struct {
	assigned map[int]int // seriesId → profileId
	err      error
}

func (s *stubSeriesChecker) AllSeriesQualityProfiles(context.Context) (map[int]int, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.assigned == nil {
		return map[int]int{}, nil
	}
	out := make(map[int]int, len(s.assigned))
	for k, v := range s.assigned {
		out[k] = v
	}
	return out, nil
}

// stubImportListChecker implements ImportListProfileChecker with a fixed
// slice.
type stubImportListChecker struct {
	ids []int
	err error
}

func (s *stubImportListChecker) AllImportListQualityProfileIDs(context.Context) ([]int, error) {
	if s.err != nil {
		return nil, s.err
	}
	return append([]int(nil), s.ids...), nil
}

func newServiceWithDeleteCheckers(t *testing.T, series *stubSeriesChecker, importList *stubImportListChecker) (*qualityprofiles.Service, *qualityprofiles.Profile) {
	t.Helper()
	svc, _ := newTestService(t)
	svc.SetFormatProvider(&stubFormats{})
	svc.SetSeriesProfileChecker(series)
	svc.SetImportListProfileChecker(importList)

	added, err := svc.Add(t.Context(), sampleProfile("Any"))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	return svc, added
}

func TestDelete_RemovesUnusedProfile(t *testing.T) {
	t.Parallel()
	svc, p := newServiceWithDeleteCheckers(t, &stubSeriesChecker{}, &stubImportListChecker{})

	if err := svc.Delete(t.Context(), p.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := svc.Get(t.Context(), p.ID); !errors.Is(err, qualityprofiles.ErrNotFound) {
		t.Errorf("post-Delete Get: got %v want ErrNotFound", err)
	}
}

func TestDelete_RejectsInUseBySeries(t *testing.T) {
	t.Parallel()
	// Two-phase setup: build the Service+Profile first so we know the
	// assigned profile ID; then attach a series-checker that points at
	// it. Re-creating the fixture would lose the persisted profile.
	seriesCheck := &stubSeriesChecker{}
	svc, p := newServiceWithDeleteCheckers(t, seriesCheck, &stubImportListChecker{})
	seriesCheck.assigned = map[int]int{99: p.ID}

	err := svc.Delete(t.Context(), p.ID)
	if !errors.Is(err, qualityprofiles.ErrQualityProfileInUse) {
		t.Errorf("Delete in-use-by-series: got %v want ErrQualityProfileInUse", err)
	}
	// Profile still present.
	if _, err := svc.Get(t.Context(), p.ID); err != nil {
		t.Errorf("profile should still exist: %v", err)
	}
}

func TestDelete_RejectsInUseByImportList(t *testing.T) {
	t.Parallel()
	importCheck := &stubImportListChecker{}
	svc, p := newServiceWithDeleteCheckers(t, &stubSeriesChecker{}, importCheck)
	importCheck.ids = []int{p.ID}

	err := svc.Delete(t.Context(), p.ID)
	if !errors.Is(err, qualityprofiles.ErrQualityProfileInUse) {
		t.Errorf("Delete in-use-by-import-list: got %v want ErrQualityProfileInUse", err)
	}
}

func TestDelete_RequiresBothCheckers(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	svc.SetFormatProvider(&stubFormats{})

	added, err := svc.Add(t.Context(), sampleProfile("Any"))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	// Neither checker wired.
	if err := svc.Delete(t.Context(), added.ID); err == nil {
		t.Errorf("Delete without checkers should error")
	}
}

func TestDelete_ErrorMessageIncludesProfileName(t *testing.T) {
	t.Parallel()
	seriesCheck := &stubSeriesChecker{}
	svc, p := newServiceWithDeleteCheckers(t, seriesCheck, &stubImportListChecker{})
	seriesCheck.assigned = map[int]int{1: p.ID}

	err := svc.Delete(t.Context(), p.ID)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, qualityprofiles.ErrQualityProfileInUse) {
		t.Errorf("not classified as ErrQualityProfileInUse: %v", err)
	}
	// Should mention the profile name "Any" somewhere.
	if msg := err.Error(); !strings.Contains(msg, "Any") {
		t.Errorf("error message %q should include profile name", msg)
	}
}
