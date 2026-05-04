package qualityprofiles_test

import (
	"context"
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/qualityprofiles"
)

// stubFormats implements [qualityprofiles.FormatProvider] with a
// canned slice. Tests inject specific format counts to exercise the
// FormatItems scaffolding.
type stubFormats struct {
	formats []*customformats.Format
}

func (s *stubFormats) All(context.Context) ([]*customformats.Format, error) {
	return s.formats, nil
}

func newServiceWithFormats(t *testing.T, formats []*customformats.Format) *qualityprofiles.Service {
	t.Helper()
	svc, _ := newTestService(t)
	svc.SetFormatProvider(&stubFormats{formats: formats})
	return svc
}

func TestGetDefaultProfile_RequiresFormatProvider(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	if _, err := svc.GetDefaultProfile(t.Context(), "Any", &quality.SDTV); err == nil {
		t.Errorf("expected error when FormatProvider is unset")
	}
}

func TestGetDefaultProfile_AnyShape(t *testing.T) {
	t.Parallel()
	svc := newServiceWithFormats(t, nil)
	cutoff := quality.SDTV
	p, err := svc.GetDefaultProfile(t.Context(), "Any", &cutoff,
		quality.SDTV, quality.WEBRip480p, quality.WEBDL480p,
		quality.DVD, quality.Bluray480p, quality.Bluray576p,
		quality.HDTV720p, quality.HDTV1080p,
		quality.WEBRip720p, quality.WEBDL720p,
		quality.WEBRip1080p, quality.WEBDL1080p,
		quality.Bluray720p, quality.Bluray1080p,
	)
	if err != nil {
		t.Fatalf("GetDefaultProfile: %v", err)
	}
	if p.Name != "Any" {
		t.Errorf("Name=%q", p.Name)
	}

	items, err := p.ParsedItems()
	if err != nil {
		t.Fatalf("ParsedItems: %v", err)
	}
	if len(items) == 0 {
		t.Fatalf("expected non-empty Items")
	}

	// Cutoff is SDTV (singleton group, weight=2) → keeps SDTV.ID, no
	// group-id substitution.
	if p.Cutoff != quality.SDTV.ID {
		t.Errorf("Cutoff=%d want SDTV.ID=%d", p.Cutoff, quality.SDTV.ID)
	}

	// Spot-check: the WEB 480p group (Weight=3) holds WEBRip480p +
	// WEBDL480p, both allowed.
	var webGroup *qualityprofiles.QualityItem
	for i := range items {
		if items[i].Name == "WEB 480p" {
			webGroup = &items[i]
			break
		}
	}
	if webGroup == nil {
		t.Fatalf("WEB 480p group missing from Items")
	}
	if !webGroup.IsGroup() {
		t.Errorf("WEB 480p should IsGroup()")
	}
	if webGroup.ID < 1000 {
		t.Errorf("group Id should be ≥ 1000, got %d", webGroup.ID)
	}
	if !webGroup.Allowed {
		t.Errorf("WEB 480p should be Allowed=true (members in allowed list)")
	}

	// Cross-check: a non-allowed singleton (RawHD, weight=9) is in
	// Items but not Allowed.
	var rawhd *qualityprofiles.QualityItem
	for i := range items {
		if items[i].Quality.ID == quality.RawHD.ID {
			rawhd = &items[i]
			break
		}
	}
	if rawhd == nil {
		t.Fatalf("RawHD missing from Items")
	}
	if rawhd.Allowed {
		t.Errorf("RawHD should not be Allowed (not in passed allowed list)")
	}
}

// Cutoff resolution: when the cutoff Quality belongs to a group, the
// returned Profile's Cutoff is replaced with the synthetic group Id.
func TestGetDefaultProfile_CutoffInGroupReplacedWithGroupID(t *testing.T) {
	t.Parallel()
	svc := newServiceWithFormats(t, nil)
	cutoff := quality.WEBDL720p // Weight=10, in "WEB 720p" group with WEBRip720p.
	p, err := svc.GetDefaultProfile(t.Context(), "HD-720p", &cutoff,
		quality.HDTV720p, quality.WEBRip720p, quality.WEBDL720p, quality.Bluray720p)
	if err != nil {
		t.Fatalf("GetDefaultProfile: %v", err)
	}

	items, _ := p.ParsedItems()
	var webGroup *qualityprofiles.QualityItem
	for i := range items {
		if items[i].Name == "WEB 720p" {
			webGroup = &items[i]
			break
		}
	}
	if webGroup == nil {
		t.Fatalf("WEB 720p group missing")
	}
	if p.Cutoff != webGroup.ID {
		t.Errorf("Cutoff=%d should match WEB 720p group Id=%d", p.Cutoff, webGroup.ID)
	}
}

func TestGetDefaultProfile_NilCutoffYieldsUnknown(t *testing.T) {
	t.Parallel()
	svc := newServiceWithFormats(t, nil)
	p, err := svc.GetDefaultProfile(t.Context(), "Any", nil)
	if err != nil {
		t.Fatalf("GetDefaultProfile: %v", err)
	}
	if p.Cutoff != quality.Unknown.ID {
		t.Errorf("nil cutoff: got %d want Unknown.ID=%d", p.Cutoff, quality.Unknown.ID)
	}
}

// Every loaded CustomFormat surfaces as a zero-score FormatItem.
func TestGetDefaultProfile_FormatItemsScaffold(t *testing.T) {
	t.Parallel()
	formats := []*customformats.Format{
		{ID: 1, Name: "F1"},
		{ID: 2, Name: "F2"},
	}
	svc := newServiceWithFormats(t, formats)

	p, err := svc.GetDefaultProfile(t.Context(), "Any", nil)
	if err != nil {
		t.Fatalf("GetDefaultProfile: %v", err)
	}
	fi, err := p.ParsedFormatItems()
	if err != nil {
		t.Fatalf("ParsedFormatItems: %v", err)
	}
	if len(fi) != 2 {
		t.Fatalf("FormatItems: len=%d want 2", len(fi))
	}
	for _, f := range fi {
		if f.Score != 0 {
			t.Errorf("FormatItems should default to Score=0, got %+v", f)
		}
	}
}

// Defaults are inserted iff no profile rows exist; when any profile
// is present, SeedDefaults is a no-op.
func TestSeedDefaults_InsertsSixOnEmpty(t *testing.T) {
	t.Parallel()
	svc := newServiceWithFormats(t, nil)
	if err := svc.SeedDefaults(t.Context()); err != nil {
		t.Fatalf("SeedDefaults: %v", err)
	}
	all, err := svc.All(t.Context())
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 6 {
		t.Errorf("expected 6 default profiles, got %d", len(all))
	}
	want := []string{"Any", "SD", "HD-720p", "HD-1080p", "Ultra-HD", "HD - 720p/1080p"}
	have := map[string]bool{}
	for _, p := range all {
		have[p.Name] = true
	}
	for _, n := range want {
		if !have[n] {
			t.Errorf("missing default profile %q (have=%v)", n, have)
		}
	}
}

func TestSeedDefaults_NoOpWhenAnyProfileExists(t *testing.T) {
	t.Parallel()
	svc := newServiceWithFormats(t, nil)
	if _, err := svc.Add(t.Context(), sampleProfile("Existing")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := svc.SeedDefaults(t.Context()); err != nil {
		t.Fatalf("SeedDefaults: %v", err)
	}
	all, err := svc.All(t.Context())
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("expected SeedDefaults to skip; got %d profiles", len(all))
	}
}

func TestSeedDefaults_RequiresFormatProvider(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	err := svc.SeedDefaults(t.Context())
	if err == nil {
		t.Errorf("SeedDefaults without FormatProvider should error")
	}
	if errors.Is(err, qualityprofiles.ErrNotFound) {
		t.Errorf("unexpected ErrNotFound — should be a clear setup error")
	}
}
