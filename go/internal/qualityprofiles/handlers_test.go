package qualityprofiles_test

import (
	"context"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/qualityprofiles"
)

// floatPtr returns &v for nullable size-limit fields.
func floatPtr(v float64) *float64 { return &v }

// UpdateAllSizeLimits walks every profile and updates Min/Max/Preferred
// for matching qualities. Singleton-quality items get the values
// directly; group-member qualities get the values on the inner
// nested item, not the parent group.
func TestUpdateAllSizeLimits_SingletonQuality(t *testing.T) {
	t.Parallel()
	svc := newServiceWithFormats(t, nil)
	if err := svc.SeedDefaults(t.Context()); err != nil {
		t.Fatalf("SeedDefaults: %v", err)
	}
	limits := []qualityprofiles.QualityProfileSizeLimit{
		{Quality: quality.SDTV, MinSize: floatPtr(0.5), MaxSize: floatPtr(99.9), PreferredSize: floatPtr(50.0)},
	}
	if err := svc.UpdateAllSizeLimits(t.Context(), limits...); err != nil {
		t.Fatalf("UpdateAllSizeLimits: %v", err)
	}
	all, _ := svc.All(t.Context())
	for _, p := range all {
		items, err := p.ParsedItems()
		if err != nil {
			t.Fatalf("ParsedItems(%q): %v", p.Name, err)
		}
		var sdtv *qualityprofiles.QualityItem
		for i := range items {
			if items[i].Quality.ID == quality.SDTV.ID {
				sdtv = &items[i]
				break
			}
		}
		if sdtv == nil {
			continue // some defaults don't include SDTV directly (e.g. "HD-720p")
		}
		if sdtv.MinSize == nil || *sdtv.MinSize != 0.5 {
			t.Errorf("%s: MinSize=%v want 0.5", p.Name, sdtv.MinSize)
		}
		if sdtv.MaxSize == nil || *sdtv.MaxSize != 99.9 {
			t.Errorf("%s: MaxSize=%v want 99.9", p.Name, sdtv.MaxSize)
		}
		if sdtv.PreferredSize == nil || *sdtv.PreferredSize != 50.0 {
			t.Errorf("%s: PreferredSize=%v want 50.0", p.Name, sdtv.PreferredSize)
		}
	}
}

// Group-member quality: WEBDL720p belongs to the WEB 720p group. Updating
// it should write to the nested inner item, not the group's parent
// fields.
func TestUpdateAllSizeLimits_GroupMember(t *testing.T) {
	t.Parallel()
	svc := newServiceWithFormats(t, nil)
	if err := svc.SeedDefaults(t.Context()); err != nil {
		t.Fatalf("SeedDefaults: %v", err)
	}
	limits := []qualityprofiles.QualityProfileSizeLimit{
		{Quality: quality.WEBDL720p, MinSize: floatPtr(1.0), MaxSize: floatPtr(2.0), PreferredSize: floatPtr(1.5)},
	}
	if err := svc.UpdateAllSizeLimits(t.Context(), limits...); err != nil {
		t.Fatalf("UpdateAllSizeLimits: %v", err)
	}
	all, _ := svc.All(t.Context())
	for _, p := range all {
		items, _ := p.ParsedItems()
		var group *qualityprofiles.QualityItem
		for i := range items {
			if items[i].Name == "WEB 720p" {
				group = &items[i]
				break
			}
		}
		if group == nil {
			continue
		}
		// Group's parent fields should be untouched.
		if group.MinSize != nil || group.MaxSize != nil || group.PreferredSize != nil {
			t.Errorf("%s: group parent should retain nil sizes, got %+v", p.Name, group)
		}
		// Inner WEBDL720p item should have the new sizes.
		var webdl *qualityprofiles.QualityItem
		for i := range group.Items {
			if group.Items[i].Quality.ID == quality.WEBDL720p.ID {
				webdl = &group.Items[i]
				break
			}
		}
		if webdl == nil {
			t.Fatalf("%s: WEBDL720p missing from WEB 720p group", p.Name)
		}
		if webdl.MinSize == nil || *webdl.MinSize != 1.0 {
			t.Errorf("%s: WEBDL720p MinSize=%v want 1.0", p.Name, webdl.MinSize)
		}
	}
}

// UpdateAllSizeLimits does NOT publish QualityProfileUpdatedEvent —
// mirrors the C# UpdateMany path at :280 (bypasses Service.Update).
func TestUpdateAllSizeLimits_DoesNotPublishEvent(t *testing.T) {
	t.Parallel()
	svc, bus := newTestService(t)
	svc.SetFormatProvider(&stubFormats{})
	if err := svc.SeedDefaults(t.Context()); err != nil {
		t.Fatalf("SeedDefaults: %v", err)
	}

	var events atomic.Int32
	messaging.Subscribe(bus, "qp.size-update.subscribe",
		func(_ context.Context, _ qualityprofiles.QualityProfileUpdatedEvent) error {
			events.Add(1)
			return nil
		})

	limits := []qualityprofiles.QualityProfileSizeLimit{
		{Quality: quality.SDTV, MinSize: floatPtr(0.0)},
	}
	if err := svc.UpdateAllSizeLimits(t.Context(), limits...); err != nil {
		t.Fatalf("UpdateAllSizeLimits: %v", err)
	}
	if events.Load() != 0 {
		t.Errorf("UpdateAllSizeLimits should not publish events; got %d", events.Load())
	}
}

// Missing quality is skipped silently — does NOT corrupt the first
// item's sizes (guards against the C# zero-value bug).
func TestUpdateAllSizeLimits_MissingQualitySkipped(t *testing.T) {
	t.Parallel()
	svc := newServiceWithFormats(t, nil)

	// Build a "HD-720p" profile that does NOT include SDTV.
	cutoff := quality.HDTV720p
	hd, err := svc.GetDefaultProfile(t.Context(), "HD-720p", &cutoff,
		quality.HDTV720p, quality.WEBRip720p, quality.WEBDL720p, quality.Bluray720p)
	if err != nil {
		t.Fatalf("GetDefaultProfile: %v", err)
	}
	if _, err := svc.Add(t.Context(), hd); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Snapshot the first item's pre-update sizes.
	all, _ := svc.All(t.Context())
	preItems, _ := all[0].ParsedItems()
	preFirst := preItems[0]

	// Try updating SDTV's sizes — SDTV ISN'T in this profile.
	limits := []qualityprofiles.QualityProfileSizeLimit{
		{Quality: quality.SDTV, MinSize: floatPtr(99.0)},
	}
	if err := svc.UpdateAllSizeLimits(t.Context(), limits...); err != nil {
		t.Fatalf("UpdateAllSizeLimits: %v", err)
	}

	all2, _ := svc.All(t.Context())
	postItems, _ := all2[0].ParsedItems()
	postFirst := postItems[0]
	if !sameSizes(preFirst, postFirst) {
		t.Errorf("first item's sizes corrupted by missing-quality update: pre=%+v post=%+v", preFirst, postFirst)
	}
}

// OnCustomFormatAdded prepends a zero-score FormatItem to every
// profile's FormatItems and publishes QualityProfileUpdatedEvent per
// profile.
func TestOnCustomFormatAdded_PrependsAndPublishes(t *testing.T) {
	t.Parallel()
	svc, bus := newTestService(t)
	svc.SetFormatProvider(&stubFormats{})

	if err := svc.SeedDefaults(t.Context()); err != nil {
		t.Fatalf("SeedDefaults: %v", err)
	}
	allBefore, _ := svc.All(t.Context())
	profileCount := len(allBefore)
	if profileCount == 0 {
		t.Fatalf("expected ≥ 1 profile from seed")
	}

	var events atomic.Int32
	messaging.Subscribe(bus, "qp.fmt-add.subscribe",
		func(_ context.Context, _ qualityprofiles.QualityProfileUpdatedEvent) error {
			events.Add(1)
			return nil
		})

	if err := svc.OnCustomFormatAdded(t.Context(), &customformats.Format{ID: 42, Name: "F42"}); err != nil {
		t.Fatalf("OnCustomFormatAdded: %v", err)
	}

	if int(events.Load()) != profileCount {
		t.Errorf("expected %d events (one per profile), got %d", profileCount, events.Load())
	}

	all, _ := svc.All(t.Context())
	for _, p := range all {
		fi, _ := p.ParsedFormatItems()
		if len(fi) == 0 || fi[0].FormatID != 42 {
			t.Errorf("%s: expected FormatItems[0].FormatID=42, got %+v", p.Name, fi)
		}
	}
}

// OnCustomFormatDeleted removes the matching FormatItem; when the list
// goes empty, zeroes MinFormatScore + CutoffFormatScore and resets
// MinUpgradeFormatScore to 1.
func TestOnCustomFormatDeleted_FiltersAndResetsScoresWhenEmpty(t *testing.T) {
	t.Parallel()
	svc := newServiceWithFormats(t, []*customformats.Format{
		{ID: 7, Name: "Solo"},
	})
	if err := svc.SeedDefaults(t.Context()); err != nil {
		t.Fatalf("SeedDefaults: %v", err)
	}
	// Set non-default score thresholds on every profile so we can
	// observe the reset.
	all, _ := svc.All(t.Context())
	for i := range all {
		p := &all[i]
		p.MinFormatScore = 5
		p.CutoffFormatScore = 5
		p.MinUpgradeFormatScore = 5
		if err := svc.Update(t.Context(), p); err != nil {
			t.Fatalf("Update: %v", err)
		}
	}

	// Delete the only format. FormatItems should empty out → score
	// thresholds get reset.
	if err := svc.OnCustomFormatDeleted(t.Context(), &customformats.Format{ID: 7}); err != nil {
		t.Fatalf("OnCustomFormatDeleted: %v", err)
	}

	all2, _ := svc.All(t.Context())
	for _, p := range all2 {
		fi, _ := p.ParsedFormatItems()
		if len(fi) != 0 {
			t.Errorf("%s: expected empty FormatItems, got %+v", p.Name, fi)
		}
		if p.MinFormatScore != 0 {
			t.Errorf("%s: MinFormatScore=%d want 0", p.Name, p.MinFormatScore)
		}
		if p.CutoffFormatScore != 0 {
			t.Errorf("%s: CutoffFormatScore=%d want 0", p.Name, p.CutoffFormatScore)
		}
		if p.MinUpgradeFormatScore != 1 {
			t.Errorf("%s: MinUpgradeFormatScore=%d want 1", p.Name, p.MinUpgradeFormatScore)
		}
	}
}

// Multi-format profile: deleting one leaves the others; score
// thresholds are NOT reset because the list is still non-empty.
func TestOnCustomFormatDeleted_PreservesOthersAndKeepsThresholds(t *testing.T) {
	t.Parallel()
	formats := []*customformats.Format{{ID: 1}, {ID: 2}, {ID: 3}}
	svc := newServiceWithFormats(t, formats)
	if err := svc.SeedDefaults(t.Context()); err != nil {
		t.Fatalf("SeedDefaults: %v", err)
	}
	all, _ := svc.All(t.Context())
	for i := range all {
		p := &all[i]
		p.MinFormatScore = 10
		p.CutoffFormatScore = 20
		p.MinUpgradeFormatScore = 5
		if err := svc.Update(t.Context(), p); err != nil {
			t.Fatalf("Update: %v", err)
		}
	}

	if err := svc.OnCustomFormatDeleted(t.Context(), &customformats.Format{ID: 2}); err != nil {
		t.Fatalf("OnCustomFormatDeleted: %v", err)
	}

	all2, _ := svc.All(t.Context())
	for _, p := range all2 {
		fi, _ := p.ParsedFormatItems()
		if len(fi) != 2 {
			t.Errorf("%s: expected 2 remaining FormatItems, got %d", p.Name, len(fi))
		}
		for _, item := range fi {
			if item.FormatID == 2 {
				t.Errorf("%s: deleted FormatID=2 still present: %+v", p.Name, item)
			}
		}
		// Thresholds preserved (FormatItems wasn't empty).
		if p.MinFormatScore != 10 || p.CutoffFormatScore != 20 || p.MinUpgradeFormatScore != 5 {
			t.Errorf("%s: thresholds reset unexpectedly: %+v", p.Name, p)
		}
	}
}

// nil format is a no-op; mirrors C# `message.CustomFormat` always
// non-null but defensive on the Go side.
func TestOnCustomFormat_NilFormatIsNoOp(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	svc.SetFormatProvider(&stubFormats{})
	if err := svc.SeedDefaults(t.Context()); err != nil {
		t.Fatalf("SeedDefaults: %v", err)
	}
	if err := svc.OnCustomFormatAdded(t.Context(), nil); err != nil {
		t.Errorf("OnCustomFormatAdded(nil): %v", err)
	}
	if err := svc.OnCustomFormatDeleted(t.Context(), nil); err != nil {
		t.Errorf("OnCustomFormatDeleted(nil): %v", err)
	}
}

// sameSizes reports whether two QualityItems have identical Min/Max/
// PreferredSize values (treating nil-vs-nil as equal).
func sameSizes(a, b qualityprofiles.QualityItem) bool {
	return ptrEqualF(a.MinSize, b.MinSize) &&
		ptrEqualF(a.MaxSize, b.MaxSize) &&
		ptrEqualF(a.PreferredSize, b.PreferredSize)
}

func ptrEqualF(a, b *float64) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return *a == *b
	}
}
