package decisionengine_test

import (
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/qualityprofiles"
)

// stubConfig is the minimal ConfigReader implementation used by every
// UpgradableSpecification test. The C# tree's `IConfigService` is huge;
// this slice's spec needs exactly one field.
type stubConfig struct {
	properMode quality.ProperDownloadType
}

func (s stubConfig) DownloadPropersAndRepacks() quality.ProperDownloadType {
	return s.properMode
}

// newSpec builds an UpgradableSpecification with a stub config and a
// discarded logger. Default properMode is PreferAndUpgrade (the C# default).
func newSpec(t *testing.T, mode quality.ProperDownloadType) *decisionengine.UpgradableSpecification {
	t.Helper()
	spec, err := decisionengine.NewUpgradableSpecification(stubConfig{properMode: mode}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewUpgradableSpecification: %v", err)
	}
	return spec
}

// makeView constructs a ProfileView from the supplied raw fields.
// Items are passed as a slice of QualityItems each with Allowed=true.
// FormatItems is set verbatim. Profile carries the cutoff + format-cutoff
// + min-format-score fields the spec reads.
func makeView(t *testing.T, cutoffID int, upgradeAllowed bool, items []qualityprofiles.QualityItem, formatItems []qualityprofiles.FormatItem, cutoffFormatScore, minUpgradeFormatScore int) *decisionengine.ProfileView {
	t.Helper()
	return &decisionengine.ProfileView{
		Profile: &qualityprofiles.Profile{
			Name:                  "Test",
			Cutoff:                cutoffID,
			UpgradeAllowed:        upgradeAllowed,
			CutoffFormatScore:     cutoffFormatScore,
			MinUpgradeFormatScore: minUpgradeFormatScore,
			MinFormatScore:        0,
		},
		Items:       items,
		FormatItems: formatItems,
	}
}

// allowedItem builds a single-quality QualityItem with Allowed=true.
func allowedItem(q quality.Quality) qualityprofiles.QualityItem {
	return qualityprofiles.QualityItem{Quality: q, Allowed: true, Items: []qualityprofiles.QualityItem{}}
}

// disallowedItem builds a single-quality QualityItem with Allowed=false.
func disallowedItem(q quality.Quality) qualityprofiles.QualityItem {
	return qualityprofiles.QualityItem{Quality: q, Allowed: false, Items: []qualityprofiles.QualityItem{}}
}

// model builds a quality.Model with the default revision (v1).
func model(q quality.Quality) quality.Model {
	return quality.NewModel(q)
}

// modelRev builds a quality.Model with an explicit revision version.
func modelRev(q quality.Quality, version int) quality.Model {
	return quality.NewModelWithRevision(q, quality.Revision{Version: version})
}

// TestNewUpgradableSpecification_NilCfgErrors pins the constructor's
// nil-guard.
func TestNewUpgradableSpecification_NilCfgErrors(t *testing.T) {
	t.Parallel()
	if _, err := decisionengine.NewUpgradableSpecification(nil, nil); err == nil {
		t.Errorf("expected nil-cfg error, got nil")
	}
}

// TestNewProfileView_ParsesItems pins the constructor + the parsed view.
func TestNewProfileView_ParsesItems(t *testing.T) {
	t.Parallel()
	p := &qualityprofiles.Profile{
		Items:       `[{"quality":1,"allowed":true,"items":[]}]`,
		FormatItems: `[{"format":42,"score":100}]`,
	}
	view, err := decisionengine.NewProfileView(p)
	if err != nil {
		t.Fatalf("NewProfileView: %v", err)
	}
	if len(view.Items) != 1 || view.Items[0].Quality.ID != 1 {
		t.Errorf("Items mismatch: %+v", view.Items)
	}
	if len(view.FormatItems) != 1 || view.FormatItems[0].FormatID != 42 {
		t.Errorf("FormatItems mismatch: %+v", view.FormatItems)
	}
}

// TestProfileView_FirstAllowedQuality covers the three cases at the
// helper: first quality item allowed; group with allowed member; nothing
// allowed.
func TestProfileView_FirstAllowedQuality(t *testing.T) {
	t.Parallel()
	view := &decisionengine.ProfileView{Items: []qualityprofiles.QualityItem{
		disallowedItem(quality.SDTV),
		allowedItem(quality.HDTV720p),
	}}
	got, ok := view.FirstAllowedQuality()
	if !ok || got.ID != quality.HDTV720p.ID {
		t.Errorf("FirstAllowed: %v %v", got, ok)
	}

	view = &decisionengine.ProfileView{Items: []qualityprofiles.QualityItem{
		disallowedItem(quality.SDTV),
		disallowedItem(quality.HDTV720p),
	}}
	if _, ok := view.FirstAllowedQuality(); ok {
		t.Errorf("expected no-allowed → ok=false")
	}
}

// TestIsUpgradable_NewBetterQuality_AcceptsWhenCutoffNotMet covers the
// first decision branch at C# `:37-41`.
func TestIsUpgradable_NewBetterQuality_AcceptsWhenCutoffNotMet(t *testing.T) {
	t.Parallel()
	view := makeView(t, quality.Bluray2160p.ID, true,
		[]qualityprofiles.QualityItem{
			allowedItem(quality.SDTV),
			allowedItem(quality.HDTV720p),
			allowedItem(quality.Bluray1080p),
			allowedItem(quality.Bluray2160p),
		}, nil, 0, 0)
	current := model(quality.SDTV)
	newQ := model(quality.HDTV720p)
	got := newSpec(t, quality.ProperPreferAndUpgrade).IsUpgradable(view, current, nil, &newQ, nil)
	if got != decisionengine.UpgradeableNone {
		t.Errorf("expected UpgradeableNone, got %v", got)
	}
}

// TestIsUpgradable_ExistingHasBetterQuality covers C# `:43-47`.
func TestIsUpgradable_ExistingHasBetterQuality(t *testing.T) {
	t.Parallel()
	view := makeView(t, quality.Bluray2160p.ID, true,
		[]qualityprofiles.QualityItem{
			allowedItem(quality.SDTV),
			allowedItem(quality.HDTV720p),
		}, nil, 0, 0)
	current := model(quality.HDTV720p)
	newQ := model(quality.SDTV)
	got := newSpec(t, quality.ProperPreferAndUpgrade).IsUpgradable(view, current, nil, &newQ, nil)
	if got != decisionengine.UpgradeableBetterQuality {
		t.Errorf("expected UpgradeableBetterQuality, got %v", got)
	}
}

// TestIsUpgradable_RevisionUpgradePreferred covers C# `:53-58`. Same
// quality + better revision + propers ON → accept.
func TestIsUpgradable_RevisionUpgradePreferred(t *testing.T) {
	t.Parallel()
	view := makeView(t, quality.HDTV720p.ID, true,
		[]qualityprofiles.QualityItem{
			allowedItem(quality.HDTV720p),
		}, nil, 0, 0)
	current := modelRev(quality.HDTV720p, 1)
	newQ := modelRev(quality.HDTV720p, 2)
	got := newSpec(t, quality.ProperPreferAndUpgrade).IsUpgradable(view, current, nil, &newQ, nil)
	if got != decisionengine.UpgradeableNone {
		t.Errorf("expected UpgradeableNone (revision upgrade), got %v", got)
	}
}

// TestIsUpgradable_UpgradesNotAllowed covers C# `:60-65`. Same quality,
// same revision, UpgradeAllowed=false → UpgradesNotAllowed.
func TestIsUpgradable_UpgradesNotAllowed(t *testing.T) {
	t.Parallel()
	view := makeView(t, quality.HDTV720p.ID, false,
		[]qualityprofiles.QualityItem{
			allowedItem(quality.HDTV720p),
		}, nil, 0, 0)
	current := model(quality.HDTV720p)
	newQ := model(quality.HDTV720p)
	got := newSpec(t, quality.ProperPreferAndUpgrade).IsUpgradable(view, current, nil, &newQ, nil)
	if got != decisionengine.UpgradeableUpgradesNotAllowed {
		t.Errorf("expected UpgradeableUpgradesNotAllowed, got %v", got)
	}
}

// TestIsUpgradable_RevisionDowngradeRejected covers C# `:68-73`. Same
// quality, lower revision, propers ON → BetterRevision rejection.
func TestIsUpgradable_RevisionDowngradeRejected(t *testing.T) {
	t.Parallel()
	view := makeView(t, quality.HDTV720p.ID, true,
		[]qualityprofiles.QualityItem{
			allowedItem(quality.HDTV720p),
		}, nil, 0, 0)
	current := modelRev(quality.HDTV720p, 2)
	newQ := modelRev(quality.HDTV720p, 1)
	got := newSpec(t, quality.ProperPreferAndUpgrade).IsUpgradable(view, current, nil, &newQ, nil)
	if got != decisionengine.UpgradeableBetterRevision {
		t.Errorf("expected UpgradeableBetterRevision, got %v", got)
	}
}

// TestIsUpgradable_DoNotPreferIgnoresRevision pins the C# branch with
// `downloadPropersAndRepacks == DoNotPrefer` skipping both revision
// branches. Same quality + better revision + DoNotPrefer → falls through
// to custom-format scoring (and returns CustomFormatScore here since
// no formats).
func TestIsUpgradable_DoNotPreferIgnoresRevision(t *testing.T) {
	t.Parallel()
	view := makeView(t, quality.HDTV720p.ID, true,
		[]qualityprofiles.QualityItem{
			allowedItem(quality.HDTV720p),
		}, nil, 0, 0)
	current := modelRev(quality.HDTV720p, 1)
	newQ := modelRev(quality.HDTV720p, 2)
	got := newSpec(t, quality.ProperDoNotPrefer).IsUpgradable(view, current, nil, &newQ, nil)
	// Revision upgrade is ignored under DoNotPrefer; same quality + same
	// custom-format score → CustomFormatScore rejection (newScore <=
	// currentScore).
	if got != decisionengine.UpgradeableCustomFormatScore {
		t.Errorf("expected UpgradeableCustomFormatScore, got %v", got)
	}
}

// TestIsUpgradable_CustomFormatScoreNotImproving covers C# `:86-94`.
// Same quality, no revision change, new score equals current.
func TestIsUpgradable_CustomFormatScoreNotImproving(t *testing.T) {
	t.Parallel()
	view := makeView(t, quality.HDTV720p.ID, true,
		[]qualityprofiles.QualityItem{
			allowedItem(quality.HDTV720p),
		},
		[]qualityprofiles.FormatItem{
			{FormatID: 1, Score: 100},
		}, 0, 0)
	current := model(quality.HDTV720p)
	newQ := model(quality.HDTV720p)
	cf := []customformats.CustomFormat{{ID: 1}}
	got := newSpec(t, quality.ProperPreferAndUpgrade).IsUpgradable(view, current, cf, &newQ, cf)
	if got != decisionengine.UpgradeableCustomFormatScore {
		t.Errorf("expected UpgradeableCustomFormatScore, got %v", got)
	}
}

// TestIsUpgradable_CustomFormatCutoffMet covers C# `:96-103`.
// New score > current AND current >= cutoff.
func TestIsUpgradable_CustomFormatCutoffMet(t *testing.T) {
	t.Parallel()
	view := makeView(t, quality.HDTV720p.ID, true,
		[]qualityprofiles.QualityItem{
			allowedItem(quality.HDTV720p),
		},
		[]qualityprofiles.FormatItem{
			{FormatID: 1, Score: 100},
			{FormatID: 2, Score: 50},
		}, /*CutoffFormatScore*/ 100, 0)
	current := model(quality.HDTV720p)
	newQ := model(quality.HDTV720p)
	currentCF := []customformats.CustomFormat{{ID: 1}}      // score=100, == cutoff
	newCF := []customformats.CustomFormat{{ID: 1}, {ID: 2}} // score=150, > current
	got := newSpec(t, quality.ProperPreferAndUpgrade).IsUpgradable(view, current, currentCF, &newQ, newCF)
	if got != decisionengine.UpgradeableCustomFormatCutoff {
		t.Errorf("expected UpgradeableCustomFormatCutoff, got %v", got)
	}
}

// TestIsUpgradable_MinIncrementMissing covers C# `:105-114`. New score
// improves over current but doesn't meet MinUpgradeFormatScore.
func TestIsUpgradable_MinIncrementMissing(t *testing.T) {
	t.Parallel()
	view := makeView(t, quality.HDTV720p.ID, true,
		[]qualityprofiles.QualityItem{
			allowedItem(quality.HDTV720p),
		},
		[]qualityprofiles.FormatItem{
			{FormatID: 1, Score: 50},
			{FormatID: 2, Score: 10},
		},
		/*CutoffFormatScore*/ 100,
		/*MinUpgradeFormatScore*/ 25)
	current := model(quality.HDTV720p)
	newQ := model(quality.HDTV720p)
	currentCF := []customformats.CustomFormat{{ID: 1}}      // score=50
	newCF := []customformats.CustomFormat{{ID: 1}, {ID: 2}} // score=60, +10
	got := newSpec(t, quality.ProperPreferAndUpgrade).IsUpgradable(view, current, currentCF, &newQ, newCF)
	if got != decisionengine.UpgradeableMinCustomFormatScore {
		t.Errorf("expected UpgradeableMinCustomFormatScore (need +25, got +10), got %v", got)
	}
}

// TestIsUpgradable_AcceptsWhenScoreImprovesEnough covers the happy-path
// fall-through at C# `:116-121`.
func TestIsUpgradable_AcceptsWhenScoreImprovesEnough(t *testing.T) {
	t.Parallel()
	view := makeView(t, quality.HDTV720p.ID, true,
		[]qualityprofiles.QualityItem{
			allowedItem(quality.HDTV720p),
		},
		[]qualityprofiles.FormatItem{
			{FormatID: 1, Score: 50},
			{FormatID: 2, Score: 100},
		},
		/*CutoffFormatScore*/ 200,
		/*MinUpgradeFormatScore*/ 10)
	current := model(quality.HDTV720p)
	newQ := model(quality.HDTV720p)
	currentCF := []customformats.CustomFormat{{ID: 1}} // score=50
	newCF := []customformats.CustomFormat{{ID: 2}}     // score=100, +50 > increment 10
	got := newSpec(t, quality.ProperPreferAndUpgrade).IsUpgradable(view, current, currentCF, &newQ, newCF)
	if got != decisionengine.UpgradeableNone {
		t.Errorf("expected UpgradeableNone (clean upgrade), got %v", got)
	}
}

// TestQualityCutoffNotMet_BelowCutoff covers C# `:124-131` — current is
// below the profile cutoff.
func TestQualityCutoffNotMet_BelowCutoff(t *testing.T) {
	t.Parallel()
	view := makeView(t /*Cutoff*/, quality.Bluray1080p.ID, true,
		[]qualityprofiles.QualityItem{
			allowedItem(quality.SDTV),
			allowedItem(quality.HDTV720p),
			allowedItem(quality.Bluray1080p),
		}, nil, 0, 0)
	current := model(quality.HDTV720p)
	got := newSpec(t, quality.ProperPreferAndUpgrade).QualityCutoffNotMet(view, current, nil)
	if !got {
		t.Errorf("expected QualityCutoffNotMet=true (current HDTV720p, cutoff Bluray1080p)")
	}
}

// TestQualityCutoffNotMet_AtCutoff_NoNew — current matches cutoff +
// nil newQuality → cutoff IS met.
func TestQualityCutoffNotMet_AtCutoff_NoNew(t *testing.T) {
	t.Parallel()
	view := makeView(t, quality.HDTV720p.ID, true,
		[]qualityprofiles.QualityItem{
			allowedItem(quality.HDTV720p),
		}, nil, 0, 0)
	current := model(quality.HDTV720p)
	got := newSpec(t, quality.ProperPreferAndUpgrade).QualityCutoffNotMet(view, current, nil)
	if got {
		t.Errorf("expected QualityCutoffNotMet=false at cutoff, got true")
	}
}

// TestQualityCutoffNotMet_RevisionUpgradeAtCutoff — current at cutoff but
// new revision is better → notMet=true (revision-upgrade override at
// C# `:134-137`).
func TestQualityCutoffNotMet_RevisionUpgradeAtCutoff(t *testing.T) {
	t.Parallel()
	view := makeView(t, quality.HDTV720p.ID, true,
		[]qualityprofiles.QualityItem{
			allowedItem(quality.HDTV720p),
		}, nil, 0, 0)
	current := modelRev(quality.HDTV720p, 1)
	newQ := modelRev(quality.HDTV720p, 2)
	got := newSpec(t, quality.ProperPreferAndUpgrade).QualityCutoffNotMet(view, current, &newQ)
	if !got {
		t.Errorf("expected QualityCutoffNotMet=true on revision upgrade, got false")
	}
}

// TestCutoffNotMet_QualityFails covers C# `:152-155`.
func TestCutoffNotMet_QualityFails(t *testing.T) {
	t.Parallel()
	view := makeView(t, quality.Bluray1080p.ID, true,
		[]qualityprofiles.QualityItem{
			allowedItem(quality.SDTV),
			allowedItem(quality.HDTV720p),
			allowedItem(quality.Bluray1080p),
		}, nil, 0, 0)
	current := model(quality.HDTV720p)
	got := newSpec(t, quality.ProperPreferAndUpgrade).CutoffNotMet(view, current, nil, nil)
	if !got {
		t.Errorf("expected CutoffNotMet=true (quality below cutoff)")
	}
}

// TestCutoffNotMet_FormatScoreFails covers C# `:157-160`. Quality at
// cutoff but custom-format score below cutoff format score.
func TestCutoffNotMet_FormatScoreFails(t *testing.T) {
	t.Parallel()
	view := makeView(t, quality.HDTV720p.ID, true,
		[]qualityprofiles.QualityItem{
			allowedItem(quality.HDTV720p),
		},
		[]qualityprofiles.FormatItem{
			{FormatID: 1, Score: 50},
		},
		/*CutoffFormatScore*/ 100,
		0)
	current := model(quality.HDTV720p)
	currentCF := []customformats.CustomFormat{{ID: 1}} // score=50, below 100 cutoff
	got := newSpec(t, quality.ProperPreferAndUpgrade).CutoffNotMet(view, current, currentCF, nil)
	if !got {
		t.Errorf("expected CutoffNotMet=true (format score below cutoff)")
	}
}

// TestCutoffNotMet_BothMet covers the happy path — quality + score both met.
func TestCutoffNotMet_BothMet(t *testing.T) {
	t.Parallel()
	view := makeView(t, quality.HDTV720p.ID, true,
		[]qualityprofiles.QualityItem{
			allowedItem(quality.HDTV720p),
		},
		[]qualityprofiles.FormatItem{
			{FormatID: 1, Score: 100},
		},
		/*CutoffFormatScore*/ 100,
		0)
	current := model(quality.HDTV720p)
	currentCF := []customformats.CustomFormat{{ID: 1}}
	got := newSpec(t, quality.ProperPreferAndUpgrade).CutoffNotMet(view, current, currentCF, nil)
	if got {
		t.Errorf("expected CutoffNotMet=false (both met), got true")
	}
}

// TestIsRevisionUpgrade_SameQualityBetterRevision covers C# `:170-179`.
func TestIsRevisionUpgrade_SameQualityBetterRevision(t *testing.T) {
	t.Parallel()
	current := modelRev(quality.HDTV720p, 1)
	newQ := modelRev(quality.HDTV720p, 2)
	if !newSpec(t, quality.ProperPreferAndUpgrade).IsRevisionUpgrade(current, newQ) {
		t.Errorf("expected IsRevisionUpgrade=true")
	}
}

// TestIsRevisionUpgrade_DifferentQualityRejected — different qualities
// is NEVER a revision upgrade per C# `:175-179`.
func TestIsRevisionUpgrade_DifferentQualityRejected(t *testing.T) {
	t.Parallel()
	current := modelRev(quality.WEBDL720p, 1)
	newQ := modelRev(quality.HDTV720p, 5) // higher revision but different quality
	if newSpec(t, quality.ProperPreferAndUpgrade).IsRevisionUpgrade(current, newQ) {
		t.Errorf("expected IsRevisionUpgrade=false for different qualities")
	}
}

// TestIsUpgradeAllowed_RevisionUpgradeAlwaysAllowed covers C# `:189-193`.
func TestIsUpgradeAllowed_RevisionUpgradeAlwaysAllowed(t *testing.T) {
	t.Parallel()
	view := makeView(t, quality.HDTV720p.ID /*UpgradeAllowed*/, false,
		[]qualityprofiles.QualityItem{
			allowedItem(quality.HDTV720p),
		}, nil, 0, 0)
	current := modelRev(quality.HDTV720p, 1)
	newQ := modelRev(quality.HDTV720p, 2)
	if !newSpec(t, quality.ProperPreferAndUpgrade).IsUpgradeAllowed(view, current, nil, newQ, nil) {
		t.Errorf("expected IsUpgradeAllowed=true (revision upgrade overrides UpgradeAllowed=false)")
	}
}

// TestIsUpgradeAllowed_QualityUpgradeBlockedWhenDisallowed covers
// C# `:201-205`.
func TestIsUpgradeAllowed_QualityUpgradeBlockedWhenDisallowed(t *testing.T) {
	t.Parallel()
	view := makeView(t, quality.Bluray1080p.ID /*UpgradeAllowed*/, false,
		[]qualityprofiles.QualityItem{
			allowedItem(quality.HDTV720p),
			allowedItem(quality.Bluray1080p),
		}, nil, 0, 0)
	current := model(quality.HDTV720p)
	newQ := model(quality.Bluray1080p)
	if newSpec(t, quality.ProperPreferAndUpgrade).IsUpgradeAllowed(view, current, nil, newQ, nil) {
		t.Errorf("expected IsUpgradeAllowed=false (UpgradeAllowed=false blocks quality upgrade)")
	}
}

// TestIsUpgradeAllowed_QualityUpgradeAllowedWhenEnabled covers
// C# `:195-199`.
func TestIsUpgradeAllowed_QualityUpgradeAllowedWhenEnabled(t *testing.T) {
	t.Parallel()
	view := makeView(t, quality.Bluray1080p.ID /*UpgradeAllowed*/, true,
		[]qualityprofiles.QualityItem{
			allowedItem(quality.HDTV720p),
			allowedItem(quality.Bluray1080p),
		}, nil, 0, 0)
	current := model(quality.HDTV720p)
	newQ := model(quality.Bluray1080p)
	if !newSpec(t, quality.ProperPreferAndUpgrade).IsUpgradeAllowed(view, current, nil, newQ, nil) {
		t.Errorf("expected IsUpgradeAllowed=true")
	}
}

// TestIsUpgradeAllowed_NoUpgradeReturnsTrueByDefault — when neither
// quality nor custom-format upgrade applies, the C# tree falls through
// to `return true` at `:207`.
func TestIsUpgradeAllowed_NoUpgradeReturnsTrueByDefault(t *testing.T) {
	t.Parallel()
	view := makeView(t, quality.HDTV720p.ID /*UpgradeAllowed*/, false,
		[]qualityprofiles.QualityItem{
			allowedItem(quality.HDTV720p),
		}, nil, 0, 0)
	current := model(quality.HDTV720p)
	newQ := model(quality.HDTV720p)
	if !newSpec(t, quality.ProperPreferAndUpgrade).IsUpgradeAllowed(view, current, nil, newQ, nil) {
		t.Errorf("expected IsUpgradeAllowed=true (no-op upgrade falls through)")
	}
}
