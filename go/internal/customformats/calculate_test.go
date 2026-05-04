package customformats

import (
	"reflect"
	"testing"

	"sonarr.tv/sonarr/internal/parser"
)

func makeFormat(name string, specs ...Specification) *Format {
	return &Format{Name: name, Specifications: specs}
}

func makeReleaseTitleSpec(t *testing.T, pattern string) *ReleaseTitleSpec {
	t.Helper()
	s := &ReleaseTitleSpec{}
	if err := s.SetPattern(pattern); err != nil {
		t.Fatalf("SetPattern(%q) error: %v", pattern, err)
	}
	return s
}

func TestCalculate_SingleSpecMatchesAndPicks(t *testing.T) {
	formats := []*Format{
		makeFormat("HDTV-Tag", makeReleaseTitleSpec(t, "HDTV")),
		makeFormat("WEB-DL-Tag", makeReleaseTitleSpec(t, "WEB-DL")),
	}
	input := CustomFormatInput{
		EpisodeInfo: &parser.ParsedEpisodeInfo{ReleaseTitle: "Series.S01E01.HDTV.x264-LOL"},
	}
	matched := Calculate(input, formats)
	if len(matched) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matched))
	}
	if matched[0].Name != "HDTV-Tag" {
		t.Errorf("matched[0].Name = %q, want HDTV-Tag", matched[0].Name)
	}
}

func TestCalculate_SortsByName(t *testing.T) {
	hdtv := makeReleaseTitleSpec(t, "HDTV")
	formats := []*Format{
		makeFormat("Z-Tag", hdtv),
		makeFormat("A-Tag", hdtv),
		makeFormat("M-Tag", hdtv),
	}
	input := CustomFormatInput{
		EpisodeInfo: &parser.ParsedEpisodeInfo{ReleaseTitle: "S01E01.HDTV.x264-LOL"},
	}
	matched := Calculate(input, formats)
	names := []string{}
	for _, m := range matched {
		names = append(names, m.Name)
	}
	if !reflect.DeepEqual(names, []string{"A-Tag", "M-Tag", "Z-Tag"}) {
		t.Errorf("names = %v, want sorted A,M,Z", names)
	}
}

func TestCalculate_GroupsBySpecType(t *testing.T) {
	// A format with TWO ReleaseTitleSpecs of different patterns:
	// inside the SAME type-group, only ONE needs to match.
	bothSpec := makeFormat(
		"either-or",
		makeReleaseTitleSpec(t, "HDTV"),
		makeReleaseTitleSpec(t, "WEB-DL"),
	)
	input := CustomFormatInput{
		EpisodeInfo: &parser.ParsedEpisodeInfo{ReleaseTitle: "Series.S01E01.HDTV.x264-LOL"},
	}
	matched := Calculate(input, []*Format{bothSpec})
	if len(matched) != 1 {
		t.Errorf("expected the format to match (HDTV satisfies one of the two specs in the type-group)")
	}
}

func TestCalculate_RequiredSpecFailureKillsMatch(t *testing.T) {
	// Required spec MUST be satisfied; otherwise the group fails
	// even if a non-required sibling spec is satisfied.
	requiredSpec := &ReleaseTitleSpec{
		BaseSpecification: BaseSpecification{Required: true},
	}
	if err := requiredSpec.SetPattern("PROPER"); err != nil {
		t.Fatal(err)
	}
	siblingSpec := makeReleaseTitleSpec(t, "HDTV")

	f := makeFormat("requires-PROPER", requiredSpec, siblingSpec)
	input := CustomFormatInput{
		EpisodeInfo: &parser.ParsedEpisodeInfo{ReleaseTitle: "Series.S01E01.HDTV.x264-LOL"},
	}
	matched := Calculate(input, []*Format{f})
	if len(matched) != 0 {
		t.Errorf("required spec missed the match → format should not match")
	}

	// Now PROPER is in the title; both specs satisfied → match.
	input.EpisodeInfo.ReleaseTitle = "Series.S01E01.PROPER.HDTV.x264-LOL"
	matched = Calculate(input, []*Format{f})
	if len(matched) != 1 {
		t.Errorf("required spec matched + sibling matched → expected match")
	}
}

func TestCalculate_AllFailedKillsGroup(t *testing.T) {
	// A format with one spec that doesn't match → all-failed,
	// group fails → format doesn't match.
	f := makeFormat("never-matches", makeReleaseTitleSpec(t, "DOES-NOT-APPEAR"))
	input := CustomFormatInput{
		EpisodeInfo: &parser.ParsedEpisodeInfo{ReleaseTitle: "Series.S01E01.HDTV.x264-LOL"},
	}
	matched := Calculate(input, []*Format{f})
	if len(matched) != 0 {
		t.Errorf("expected no match")
	}
}

func TestCalculate_MultipleGroupsAllMustMatch(t *testing.T) {
	// One ReleaseTitleSpec + one ReleaseGroupSpec — TWO groups.
	// Both must match for the format to match.
	titleOk := makeReleaseTitleSpec(t, "HDTV")
	groupOk := &ReleaseGroupSpec{}
	if err := groupOk.SetPattern("LOL"); err != nil {
		t.Fatal(err)
	}
	f := makeFormat("HDTV+LOL", titleOk, groupOk)

	t.Run("both groups match → format matches", func(t *testing.T) {
		input := CustomFormatInput{
			EpisodeInfo: &parser.ParsedEpisodeInfo{
				ReleaseTitle: "Series.S01E01.HDTV.x264-LOL",
				ReleaseGroup: "LOL",
			},
		}
		matched := Calculate(input, []*Format{f})
		if len(matched) != 1 {
			t.Errorf("expected match")
		}
	})

	t.Run("first group fails → format fails", func(t *testing.T) {
		input := CustomFormatInput{
			EpisodeInfo: &parser.ParsedEpisodeInfo{
				ReleaseTitle: "Series.S01E01.WEB-DL.x264-LOL",
				ReleaseGroup: "LOL",
			},
		}
		matched := Calculate(input, []*Format{f})
		if len(matched) != 0 {
			t.Errorf("first group fails → no match expected")
		}
	})

	t.Run("second group fails → format fails", func(t *testing.T) {
		input := CustomFormatInput{
			EpisodeInfo: &parser.ParsedEpisodeInfo{
				ReleaseTitle: "Series.S01E01.HDTV.x264-DIMENSION",
				ReleaseGroup: "DIMENSION",
			},
		}
		matched := Calculate(input, []*Format{f})
		if len(matched) != 0 {
			t.Errorf("second group fails → no match expected")
		}
	})
}

func TestCalculate_EmptySpecificationsNoMatch(t *testing.T) {
	// A format with no specs → no groups → not matched (mirrors
	// C# vacuous-truth quirk: All(empty) is true, but DidMatch
	// inverts to false).
	f := makeFormat("empty")
	matched := Calculate(CustomFormatInput{}, []*Format{f})
	if len(matched) != 0 {
		t.Errorf("empty Specifications → expected no match")
	}
}

func TestCalculate_NilFormatSkipped(t *testing.T) {
	// Defensive: nil entries in the formats slice are skipped, not
	// dereferenced.
	matched := Calculate(CustomFormatInput{}, []*Format{nil, nil})
	if len(matched) != 0 {
		t.Errorf("nil entries should be skipped, not match")
	}
}

func TestSpecificationGroup_DidMatch_AllFailed(t *testing.T) {
	g := SpecificationGroup{Matches: []SpecificationMatch{
		{Spec: &ReleaseTitleSpec{}, Satisfied: false},
		{Spec: &ReleaseTitleSpec{}, Satisfied: false},
	}}
	if g.DidMatch() {
		t.Errorf("all-failed group should NOT match")
	}
}

func TestSpecificationGroup_DidMatch_MixedNoneRequired(t *testing.T) {
	g := SpecificationGroup{Matches: []SpecificationMatch{
		{Spec: &ReleaseTitleSpec{}, Satisfied: false},
		{Spec: &ReleaseTitleSpec{}, Satisfied: true},
	}}
	if !g.DidMatch() {
		t.Errorf("at-least-one-satisfied with no required → should match")
	}
}

func TestSpecificationGroup_DidMatch_RequiredFailed(t *testing.T) {
	required := &ReleaseTitleSpec{BaseSpecification: BaseSpecification{Required: true}}
	other := &ReleaseTitleSpec{}
	g := SpecificationGroup{Matches: []SpecificationMatch{
		{Spec: required, Satisfied: false},
		{Spec: other, Satisfied: true},
	}}
	if g.DidMatch() {
		t.Errorf("required-failed group should NOT match (even with sibling satisfied)")
	}
}
