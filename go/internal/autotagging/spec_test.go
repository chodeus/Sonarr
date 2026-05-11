package autotagging

import (
	"testing"

	"sonarr.tv/sonarr/internal/series"
)

func ptr[T any](v T) *T { return &v }

func mkInput(s *series.Series) Input {
	return Input{Series: s}
}

func TestMonitoredSpec(t *testing.T) {
	spec := &MonitoredSpec{}
	if !spec.IsSatisfiedBy(mkInput(&series.Series{Monitored: true})) {
		t.Errorf("expected match for monitored=true")
	}
	if spec.IsSatisfiedBy(mkInput(&series.Series{Monitored: false})) {
		t.Errorf("did not expect match for monitored=false")
	}
	if spec.IsSatisfiedBy(Input{}) {
		t.Errorf("nil Series should not match")
	}
}

func TestQualityProfileSpec(t *testing.T) {
	spec := &QualityProfileSpec{Value: 7}
	if !spec.IsSatisfiedBy(mkInput(&series.Series{QualityProfileID: 7})) {
		t.Errorf("expected match")
	}
	if spec.IsSatisfiedBy(mkInput(&series.Series{QualityProfileID: 8})) {
		t.Errorf("did not expect match for differing profile")
	}
	// Per Option A: QualityProfileID is non-nullable int (C# parity).
	// Schema is NOT NULL. The "0 means unset" sentinel matches C#
	// default-int behaviour.
	if spec.IsSatisfiedBy(mkInput(&series.Series{QualityProfileID: 0})) {
		t.Errorf("zero profile id (unset sentinel) should not match")
	}
}

func TestSeriesTypeSpec(t *testing.T) {
	spec := &SeriesTypeSpec{Value: series.SeriesTypeAnime}
	if !spec.IsSatisfiedBy(mkInput(&series.Series{SeriesType: series.SeriesTypeAnime})) {
		t.Errorf("expected match for Anime")
	}
	if spec.IsSatisfiedBy(mkInput(&series.Series{SeriesType: series.SeriesTypeStandard})) {
		t.Errorf("did not expect match for Standard")
	}
}

func TestStatusSpec(t *testing.T) {
	spec := &StatusSpec{Status: series.SeriesStatusContinuing}
	if !spec.IsSatisfiedBy(mkInput(&series.Series{Status: series.SeriesStatusContinuing})) {
		t.Errorf("expected match")
	}
	if spec.IsSatisfiedBy(mkInput(&series.Series{Status: series.SeriesStatusEnded})) {
		t.Errorf("did not expect match for Ended")
	}
}

func TestTagSpec(t *testing.T) {
	spec := &TagSpec{Value: 3}
	if !spec.IsSatisfiedBy(mkInput(&series.Series{Tags: []int{1, 2, 3}})) {
		t.Errorf("expected match (Tags contains 3)")
	}
	if spec.IsSatisfiedBy(mkInput(&series.Series{Tags: []int{1, 2}})) {
		t.Errorf("did not expect match (Tags doesn't contain 3)")
	}
}

func TestYearSpec(t *testing.T) {
	spec := &YearSpec{Min: 2010, Max: 2020}
	if !spec.IsSatisfiedBy(mkInput(&series.Series{Year: 2015})) {
		t.Errorf("expected match for 2015 in [2010, 2020]")
	}
	if !spec.IsSatisfiedBy(mkInput(&series.Series{Year: 2010})) {
		t.Errorf("expected match for boundary Min")
	}
	if !spec.IsSatisfiedBy(mkInput(&series.Series{Year: 2020})) {
		t.Errorf("expected match for boundary Max")
	}
	if spec.IsSatisfiedBy(mkInput(&series.Series{Year: 2009})) {
		t.Errorf("did not expect match for below Min")
	}
	// Per Option A: Year is non-nullable int (C# parity).
	// Year == 0 is the "unset" sentinel (NULL coerces to 0 in Scan).
	if spec.IsSatisfiedBy(mkInput(&series.Series{Year: 0})) {
		t.Errorf("zero Year (unset sentinel) should not match")
	}
}

func TestGenreSpec(t *testing.T) {
	spec := &GenreSpec{Value: []string{"Comedy", "Drama"}}
	if !spec.IsSatisfiedBy(mkInput(&series.Series{Genres: `["Comedy"]`})) {
		t.Errorf("expected match for Comedy")
	}
	if !spec.IsSatisfiedBy(mkInput(&series.Series{Genres: `["drama","action"]`})) {
		t.Errorf("expected case-insensitive match for drama")
	}
	if spec.IsSatisfiedBy(mkInput(&series.Series{Genres: `["Action"]`})) {
		t.Errorf("did not expect match for Action")
	}
	if spec.IsSatisfiedBy(mkInput(&series.Series{Genres: ""})) {
		t.Errorf("empty Genres should not match")
	}
	if spec.IsSatisfiedBy(mkInput(&series.Series{Genres: `not-json`})) {
		t.Errorf("invalid JSON Genres should not match")
	}
}

func TestNetworkSpec(t *testing.T) {
	spec := &NetworkSpec{Value: []string{"NBC", "CBS"}}
	if !spec.IsSatisfiedBy(mkInput(&series.Series{Network: "nbc"})) {
		t.Errorf("expected case-insensitive match")
	}
	if spec.IsSatisfiedBy(mkInput(&series.Series{Network: "FOX"})) {
		t.Errorf("did not expect match for FOX")
	}
}

func TestOriginalCountrySpec(t *testing.T) {
	spec := &OriginalCountrySpec{Value: []string{"USA", "GBR"}}
	if !spec.IsSatisfiedBy(mkInput(&series.Series{OriginalCountry: "usa"})) {
		t.Errorf("expected case-insensitive match")
	}
	if spec.IsSatisfiedBy(mkInput(&series.Series{OriginalCountry: "FRA"})) {
		t.Errorf("did not expect match for FRA")
	}
}

func TestOriginalLanguageSpec(t *testing.T) {
	spec := &OriginalLanguageSpec{Value: 1}
	if !spec.IsSatisfiedBy(mkInput(&series.Series{OriginalLanguage: 1})) {
		t.Errorf("expected match")
	}
	if spec.IsSatisfiedBy(mkInput(&series.Series{OriginalLanguage: 2})) {
		t.Errorf("did not expect match for differing OriginalLanguage")
	}
}

func TestRootFolderSpec(t *testing.T) {
	spec := &RootFolderSpec{Value: "/tv"}
	if !spec.IsSatisfiedBy(Input{RootFolderPath: "/tv"}) {
		t.Errorf("expected match for exact path")
	}
	if !spec.IsSatisfiedBy(Input{RootFolderPath: "/TV"}) {
		t.Errorf("expected case-insensitive match")
	}
	if !spec.IsSatisfiedBy(Input{RootFolderPath: "/tv/"}) {
		t.Errorf("expected match with trailing separator")
	}
	if spec.IsSatisfiedBy(Input{RootFolderPath: "/movies"}) {
		t.Errorf("did not expect match for different path")
	}
}

func TestNegateInversesResult(t *testing.T) {
	spec := &MonitoredSpec{BaseSpecification: BaseSpecification{Negate: true}}
	if spec.IsSatisfiedBy(mkInput(&series.Series{Monitored: true})) {
		t.Errorf("Negate should invert: monitored=true → no match")
	}
	if !spec.IsSatisfiedBy(mkInput(&series.Series{Monitored: false})) {
		t.Errorf("Negate should invert: monitored=false → match")
	}
}

func TestSpecificationInterfaceConformance(t *testing.T) {
	var _ Specification = &MonitoredSpec{}
	var _ Specification = &QualityProfileSpec{}
	var _ Specification = &SeriesTypeSpec{}
	var _ Specification = &StatusSpec{}
	var _ Specification = &TagSpec{}
	var _ Specification = &YearSpec{}
	var _ Specification = &GenreSpec{}
	var _ Specification = &NetworkSpec{}
	var _ Specification = &OriginalCountrySpec{}
	var _ Specification = &OriginalLanguageSpec{}
	var _ Specification = &RootFolderSpec{}
}

func TestCalculate_EndToEnd(t *testing.T) {
	tag := &Tag{
		Name: "anime",
		Specifications: []Specification{
			&SeriesTypeSpec{Value: series.SeriesTypeAnime},
			&MonitoredSpec{},
		},
	}
	tags := []*Tag{tag}

	t.Run("both specs match → tag matches", func(t *testing.T) {
		input := mkInput(&series.Series{
			SeriesType: series.SeriesTypeAnime,
			Monitored:  true,
		})
		matched := Calculate(input, tags)
		if len(matched) != 1 {
			t.Errorf("expected 1 tag, got %d", len(matched))
		}
	})

	t.Run("one spec fails → tag fails", func(t *testing.T) {
		input := mkInput(&series.Series{
			SeriesType: series.SeriesTypeStandard,
			Monitored:  true,
		})
		matched := Calculate(input, tags)
		if len(matched) != 0 {
			t.Errorf("expected 0 tags, got %d", len(matched))
		}
	})
}

func TestCalculate_GroupsByType(t *testing.T) {
	// Two GenreSpecs of different Value → SAME type-group →
	// only one needs to match.
	tag := &Tag{
		Name: "any-of",
		Specifications: []Specification{
			&GenreSpec{Value: []string{"Comedy"}},
			&GenreSpec{Value: []string{"Drama"}},
		},
	}
	input := mkInput(&series.Series{Genres: `["Drama"]`})
	matched := Calculate(input, []*Tag{tag})
	if len(matched) != 1 {
		t.Errorf("either-of group should match when one of two specs is satisfied")
	}
}

func TestCalculate_RequiredFails(t *testing.T) {
	required := &MonitoredSpec{BaseSpecification: BaseSpecification{Required: true}}
	other := &TagSpec{Value: 1}
	tag := &Tag{Name: "needs-monitored", Specifications: []Specification{required, other}}

	// Other matches, required doesn't → group fails (different
	// types so two groups, but same logic per group).
	input := mkInput(&series.Series{Monitored: false, Tags: []int{1}})
	matched := Calculate(input, []*Tag{tag})
	if len(matched) != 0 {
		t.Errorf("required-failed → tag should not match")
	}
}

func TestCalculate_SortsByName(t *testing.T) {
	monitor := &MonitoredSpec{}
	tags := []*Tag{
		{Name: "z-tag", Specifications: []Specification{monitor}},
		{Name: "a-tag", Specifications: []Specification{monitor}},
		{Name: "m-tag", Specifications: []Specification{monitor}},
	}
	input := mkInput(&series.Series{Monitored: true})
	matched := Calculate(input, tags)
	if len(matched) != 3 {
		t.Fatalf("expected 3 matches, got %d", len(matched))
	}
	if matched[0].Name != "a-tag" || matched[1].Name != "m-tag" || matched[2].Name != "z-tag" {
		t.Errorf("not sorted: %s, %s, %s", matched[0].Name, matched[1].Name, matched[2].Name)
	}
}
