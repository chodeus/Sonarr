package customformats

import (
	"testing"

	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
)

func TestResolutionSpec(t *testing.T) {
	spec := &ResolutionSpec{Value: int(quality.Resolution1080p)}

	t.Run("matches when Quality.Resolution equals Value", func(t *testing.T) {
		input := CustomFormatInput{
			EpisodeInfo: &parser.ParsedEpisodeInfo{
				Quality: quality.Model{Quality: quality.WEBDL1080p},
			},
		}
		if !spec.IsSatisfiedBy(input) {
			t.Errorf("expected match for 1080p")
		}
	})

	t.Run("no match when Quality.Resolution differs", func(t *testing.T) {
		input := CustomFormatInput{
			EpisodeInfo: &parser.ParsedEpisodeInfo{
				Quality: quality.Model{Quality: quality.HDTV720p},
			},
		}
		if spec.IsSatisfiedBy(input) {
			t.Errorf("did not expect match")
		}
	})

	t.Run("nil EpisodeInfo defaults to ResolutionUnknown", func(t *testing.T) {
		unknownSpec := &ResolutionSpec{Value: int(quality.ResolutionUnknown)}
		if !unknownSpec.IsSatisfiedBy(CustomFormatInput{}) {
			t.Errorf("expected nil EpisodeInfo to match ResolutionUnknown")
		}
	})

	if spec.Order() != 6 {
		t.Errorf("Order should be 6")
	}
	if spec.ImplementationName() != "Resolution" {
		t.Errorf("ImplementationName mismatch")
	}
}

func TestSourceSpec(t *testing.T) {
	spec := &SourceSpec{Value: int(quality.SourceBluray)}

	t.Run("matches when Quality.Source equals Value", func(t *testing.T) {
		input := CustomFormatInput{
			EpisodeInfo: &parser.ParsedEpisodeInfo{
				Quality: quality.Model{Quality: quality.Bluray1080p},
			},
		}
		if !spec.IsSatisfiedBy(input) {
			t.Errorf("expected match for Bluray source")
		}
	})

	t.Run("no match for different source", func(t *testing.T) {
		input := CustomFormatInput{
			EpisodeInfo: &parser.ParsedEpisodeInfo{
				Quality: quality.Model{Quality: quality.HDTV720p},
			},
		}
		if spec.IsSatisfiedBy(input) {
			t.Errorf("did not expect match")
		}
	})

	if spec.Order() != 5 {
		t.Errorf("Order should be 5")
	}
}

func TestReleaseTypeSpec(t *testing.T) {
	spec := &ReleaseTypeSpec{Value: int(parser.ReleaseTypeMultiEpisode)}

	t.Run("matches when ReleaseType equals Value", func(t *testing.T) {
		input := CustomFormatInput{ReleaseType: parser.ReleaseTypeMultiEpisode}
		if !spec.IsSatisfiedBy(input) {
			t.Errorf("expected match")
		}
	})

	t.Run("no match for different ReleaseType", func(t *testing.T) {
		input := CustomFormatInput{ReleaseType: parser.ReleaseTypeSingleEpisode}
		if spec.IsSatisfiedBy(input) {
			t.Errorf("did not expect match")
		}
	})

	if spec.Order() != 10 {
		t.Errorf("Order should be 10")
	}
}

func TestSizeSpec(t *testing.T) {
	// Min=1.0 GB, Max=5.0 GB → matches strictly > 1GB and <= 5GB.
	spec := &SizeSpec{Min: 1.0, Max: 5.0}

	cases := []struct {
		name      string
		size      int64
		wantMatch bool
	}{
		{"below min", bytesPerGB / 2, false},
		{"exactly min — does NOT match (asymmetric > vs <=)", bytesPerGB, false},
		{"above min, below max", bytesPerGB * 3, true},
		{"exactly max — DOES match", bytesPerGB * 5, true},
		{"above max", bytesPerGB * 10, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := CustomFormatInput{Size: tc.size}
			if got := spec.IsSatisfiedBy(input); got != tc.wantMatch {
				t.Errorf("IsSatisfiedBy(size=%d) = %v, want %v", tc.size, got, tc.wantMatch)
			}
		})
	}

	if spec.Order() != 8 {
		t.Errorf("Order should be 8")
	}
}

func TestIndexerFlagSpec(t *testing.T) {
	spec := &IndexerFlagSpec{Value: int(parser.IndexerFlagFreeleech)}

	t.Run("matches when flag is set", func(t *testing.T) {
		input := CustomFormatInput{IndexerFlags: parser.IndexerFlagFreeleech | parser.IndexerFlagInternal}
		if !spec.IsSatisfiedBy(input) {
			t.Errorf("expected match for Freeleech bit")
		}
	})

	t.Run("no match when flag is not set", func(t *testing.T) {
		input := CustomFormatInput{IndexerFlags: parser.IndexerFlagInternal}
		if spec.IsSatisfiedBy(input) {
			t.Errorf("did not expect match")
		}
	})

	t.Run("no match on empty flags", func(t *testing.T) {
		if spec.IsSatisfiedBy(CustomFormatInput{}) {
			t.Errorf("did not expect match on zero IndexerFlags")
		}
	})

	if spec.Order() != 4 {
		t.Errorf("Order should be 4")
	}
}

func TestLanguageSpec(t *testing.T) {
	t.Run("matches when language is in input", func(t *testing.T) {
		spec := &LanguageSpec{Value: language.English.ID}
		input := CustomFormatInput{Languages: []language.Language{language.English}}
		if !spec.IsSatisfiedBy(input) {
			t.Errorf("expected match")
		}
	})

	t.Run("no match when language not in input", func(t *testing.T) {
		spec := &LanguageSpec{Value: language.Spanish.ID}
		input := CustomFormatInput{Languages: []language.Language{language.English}}
		if spec.IsSatisfiedBy(input) {
			t.Errorf("did not expect match")
		}
	})

	t.Run("nil Languages always returns false", func(t *testing.T) {
		spec := &LanguageSpec{Value: language.English.ID}
		if spec.IsSatisfiedBy(CustomFormatInput{}) {
			t.Errorf("nil Languages should not match")
		}
		// Even with Negate, nil should still return false (C# quirk).
		spec.Negate = true
		if spec.IsSatisfiedBy(CustomFormatInput{}) {
			t.Errorf("nil Languages should NOT match even with Negate=true (C# quirk preserved)")
		}
	})

	t.Run("ExceptLanguage matches when there is at least one other language", func(t *testing.T) {
		spec := &LanguageSpec{Value: language.English.ID, ExceptLanguage: true}
		input := CustomFormatInput{Languages: []language.Language{language.English, language.Spanish}}
		if !spec.IsSatisfiedBy(input) {
			t.Errorf("expected match: list contains Spanish (≠ English)")
		}
	})

	t.Run("ExceptLanguage no match when only target language present", func(t *testing.T) {
		spec := &LanguageSpec{Value: language.English.ID, ExceptLanguage: true}
		input := CustomFormatInput{Languages: []language.Language{language.English}}
		if spec.IsSatisfiedBy(input) {
			t.Errorf("did not expect match: list contains only English")
		}
	})

	t.Run("Negate flips contains result", func(t *testing.T) {
		spec := &LanguageSpec{
			BaseSpecification: BaseSpecification{Negate: true},
			Value:             language.English.ID,
		}
		input := CustomFormatInput{Languages: []language.Language{language.Spanish}}
		if !spec.IsSatisfiedBy(input) {
			t.Errorf("Negate should match when language is NOT in input")
		}
	})

	t.Run("Original-language fallback uses Series.OriginalLanguage", func(t *testing.T) {
		// Value = Original.ID; Series.OriginalLanguage = Spanish.
		// → comparison should be against Spanish, not Original.
		spec := &LanguageSpec{Value: language.Original.ID}
		input := CustomFormatInput{
			EpisodeInfo: &parser.ParsedEpisodeInfo{},
			Series:      &series.Series{OriginalLanguage: language.Spanish.ID},
			Languages:   []language.Language{language.Spanish},
		}
		if !spec.IsSatisfiedBy(input) {
			t.Errorf("expected match: Original-language fallback should swap to Spanish")
		}
	})

	t.Run("Original-language fallback skipped when Series.OriginalLanguage is Unknown", func(t *testing.T) {
		spec := &LanguageSpec{Value: language.Original.ID}
		input := CustomFormatInput{
			EpisodeInfo: &parser.ParsedEpisodeInfo{},
			Series:      &series.Series{OriginalLanguage: language.Unknown.ID},
			Languages:   []language.Language{language.Original},
		}
		if !spec.IsSatisfiedBy(input) {
			t.Errorf("expected match: should compare directly against Original.ID when Series.OriginalLanguage is Unknown")
		}
	})

	if (&LanguageSpec{}).Order() != 3 {
		t.Errorf("Order should be 3")
	}
}

func TestAllSpecsConformInterface(t *testing.T) {
	// Compile-time guard: all six new specs satisfy Specification.
	var _ Specification = &ResolutionSpec{}
	var _ Specification = &SourceSpec{}
	var _ Specification = &ReleaseTypeSpec{}
	var _ Specification = &SizeSpec{}
	var _ Specification = &IndexerFlagSpec{}
	var _ Specification = &LanguageSpec{}
}
