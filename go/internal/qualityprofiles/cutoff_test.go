package qualityprofiles_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/qualityprofiles"
)

// mkProfile builds a Profile with the supplied items + cutoff +
// upgrade-allowed flag. Items are listed worst→best (profile order).
func mkProfile(t *testing.T, cutoff int, upgradeAllowed bool, items []qualityprofiles.QualityItem) *qualityprofiles.Profile {
	t.Helper()
	p := &qualityprofiles.Profile{Cutoff: cutoff, UpgradeAllowed: upgradeAllowed}
	if err := p.SetItems(items); err != nil {
		t.Fatalf("SetItems: %v", err)
	}
	return p
}

func qi(id int, allowed bool) qualityprofiles.QualityItem {
	return qualityprofiles.QualityItem{Quality: quality.Quality{ID: id}, Allowed: allowed}
}

func sortedEq(t *testing.T, got, want []int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("below-cutoff: got %v want %v", got, want)
	}
	set := make(map[int]bool, len(got))
	for _, n := range got {
		set[n] = true
	}
	for _, w := range want {
		if !set[w] {
			t.Fatalf("below-cutoff: got %v want %v (missing %d)", got, want, w)
		}
	}
}

func TestQualitiesBelowCutoff_UpgradeAllowed(t *testing.T) {
	t.Parallel()
	// Worst→best: 1, 2, 3, 4. Cutoff = 3 → below = {1, 2}.
	p := mkProfile(t, 3, true, []qualityprofiles.QualityItem{
		qi(1, true), qi(2, true), qi(3, true), qi(4, true),
	})
	got, err := qualityprofiles.QualitiesBelowCutoff(p)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	sortedEq(t, got, []int{1, 2})
}

func TestQualitiesBelowCutoff_LowestCutoffIsEmpty(t *testing.T) {
	t.Parallel()
	// Cutoff = 1 (lowest) → nothing ranks below.
	p := mkProfile(t, 1, true, []qualityprofiles.QualityItem{
		qi(1, true), qi(2, true), qi(3, true),
	})
	got, err := qualityprofiles.QualitiesBelowCutoff(p)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("lowest cutoff must yield empty; got %v", got)
	}
}

func TestQualitiesBelowCutoff_UpgradeDisabledUsesFirstAllowed(t *testing.T) {
	t.Parallel()
	// UpgradeAllowed=false → effective cutoff = first ALLOWED quality.
	// Items: 1(disallowed), 2(disallowed), 3(allowed), 4(allowed).
	// First allowed = 3 → below = {1, 2}. (Cutoff field 4 is ignored.)
	p := mkProfile(t, 4, false, []qualityprofiles.QualityItem{
		qi(1, false), qi(2, false), qi(3, true), qi(4, true),
	})
	got, err := qualityprofiles.QualitiesBelowCutoff(p)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	sortedEq(t, got, []int{1, 2})
}

func TestQualitiesBelowCutoff_GroupCutoff(t *testing.T) {
	t.Parallel()
	// A group item ranked above two singles. Cutoff = the group id (100).
	// Below = the two singles {1, 2}.
	group := qualityprofiles.QualityItem{
		ID:      100,
		Name:    "WEB 1080p",
		Allowed: true,
		Items: []qualityprofiles.QualityItem{
			{Quality: quality.Quality{ID: 3}},
			{Quality: quality.Quality{ID: 4}},
		},
	}
	p := mkProfile(t, 100, true, []qualityprofiles.QualityItem{
		qi(1, true), qi(2, true), group,
	})
	got, err := qualityprofiles.QualitiesBelowCutoff(p)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	sortedEq(t, got, []int{1, 2})
}

func TestQualitiesBelowCutoff_BelowGroupFlattens(t *testing.T) {
	t.Parallel()
	// A group ranked BELOW the cutoff flattens to all its member ids.
	group := qualityprofiles.QualityItem{
		ID:      100,
		Allowed: true,
		Items: []qualityprofiles.QualityItem{
			{Quality: quality.Quality{ID: 1}},
			{Quality: quality.Quality{ID: 2}},
		},
	}
	p := mkProfile(t, 5, true, []qualityprofiles.QualityItem{
		group, qi(5, true),
	})
	got, err := qualityprofiles.QualitiesBelowCutoff(p)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	sortedEq(t, got, []int{1, 2}) // group members flattened
}

func TestQualitiesBelowCutoff_CutoffNotFound(t *testing.T) {
	t.Parallel()
	p := mkProfile(t, 999, true, []qualityprofiles.QualityItem{qi(1, true), qi(2, true)})
	got, err := qualityprofiles.QualitiesBelowCutoff(p)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("unknown cutoff must yield empty; got %v", got)
	}
}

func TestQualitiesBelowCutoff_NoAllowedWhenUpgradeDisabled(t *testing.T) {
	t.Parallel()
	// UpgradeAllowed=false but nothing is allowed → empty (no throw).
	p := mkProfile(t, 2, false, []qualityprofiles.QualityItem{qi(1, false), qi(2, false)})
	got, err := qualityprofiles.QualitiesBelowCutoff(p)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("no-allowed must yield empty; got %v", got)
	}
}
