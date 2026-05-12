package xbmc_test

import (
	"encoding/json"
	"testing"

	"sonarr.tv/sonarr/internal/metadata/xbmc"
)

func TestDefaultSettings(t *testing.T) {
	t.Parallel()
	s := xbmc.DefaultSettings()
	// Pin the C# constructor defaults at XbmcMetadataSettings.cs:16-25.
	if !s.SeriesMetadata || !s.EpisodeMetadata || !s.SeriesImages || !s.SeasonImages || !s.EpisodeImages {
		t.Errorf("default true-fields not all true: %+v", s)
	}
	if s.SeriesMetadataEpisodeGuide || s.SeriesMetadataUrl || s.EpisodeImageThumb {
		t.Errorf("default false-fields not all false: %+v", s)
	}
}

func TestSettings_Validate_AlwaysSucceeds(t *testing.T) {
	t.Parallel()
	// C# XbmcSettingsValidator has no rules; verify Validate() returns
	// nil regardless of field state.
	for _, s := range []xbmc.Settings{
		{},
		xbmc.DefaultSettings(),
		{SeriesMetadata: true, EpisodeMetadata: true, EpisodeImageThumb: true},
	} {
		if failures := s.Validate(); failures != nil {
			t.Errorf("Validate() = %+v, want nil", failures)
		}
	}
}

func TestSettings_JSON_Roundtrip(t *testing.T) {
	t.Parallel()
	want := xbmc.DefaultSettings()
	body, err := json.Marshal(&want)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got xbmc.Settings
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got != want {
		t.Errorf("roundtrip drift:\n got: %+v\nwant: %+v", got, want)
	}
}

func TestSettings_JSON_FieldNames(t *testing.T) {
	t.Parallel()
	// Option A: every field emitted (no omitempty). Field names match
	// the lowerCamelCase C# property names.
	body, err := json.Marshal(&xbmc.Settings{})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	expectedKeys := []string{
		"seriesMetadata", "seriesMetadataEpisodeGuide", "seriesMetadataUrl",
		"episodeMetadata", "episodeImageThumb",
		"seriesImages", "seasonImages", "episodeImages",
	}
	for _, k := range expectedKeys {
		v, ok := raw[k]
		if !ok {
			t.Errorf("missing JSON key %q", k)
			continue
		}
		// Zero-value Settings → every bool is false. Pin that the
		// field is emitted (Option A) rather than omitted.
		if b, isBool := v.(bool); !isBool || b != false {
			t.Errorf("key %q = %v (%T), want false", k, v, v)
		}
	}
	if len(raw) != len(expectedKeys) {
		t.Errorf("got %d keys (%v), want %d", len(raw), raw, len(expectedKeys))
	}
}

func TestImplementationNames(t *testing.T) {
	t.Parallel()
	// These are persisted in the Metadata table's Implementation +
	// ConfigContract columns — renaming them would orphan existing
	// rows. Pin the strings.
	if xbmc.ImplementationName != "XbmcMetadata" {
		t.Errorf("ImplementationName = %q, want %q", xbmc.ImplementationName, "XbmcMetadata")
	}
	if xbmc.ConfigContractName != "XbmcMetadataSettings" {
		t.Errorf("ConfigContractName = %q, want %q", xbmc.ConfigContractName, "XbmcMetadataSettings")
	}
}
