package sonarr_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/importlists/sonarr"
)

// Settings.Validate ports the C# `SonarrSettingsValidator` rules
// at `SonarrSettings.cs:9-16` — BaseUrl ValidRootUrl + ApiKey
// NotEmpty.
func TestSettings_Validate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		settings  sonarr.Settings
		wantProps []string
	}{
		{
			name:     "valid",
			settings: sonarr.Settings{BaseURL: "http://peer:8989", APIKey: "abc"},
		},
		{
			name:      "missing_baseurl",
			settings:  sonarr.Settings{APIKey: "abc"},
			wantProps: []string{"BaseUrl"},
		},
		{
			name:      "missing_apikey",
			settings:  sonarr.Settings{BaseURL: "http://peer:8989"},
			wantProps: []string{"ApiKey"},
		},
		{
			name:      "missing_both",
			settings:  sonarr.Settings{},
			wantProps: []string{"BaseUrl", "ApiKey"},
		},
		{
			name:      "invalid_scheme",
			settings:  sonarr.Settings{BaseURL: "ftp://peer:8989", APIKey: "abc"},
			wantProps: []string{"BaseUrl"},
		},
		{
			name:      "no_host",
			settings:  sonarr.Settings{BaseURL: "http://", APIKey: "abc"},
			wantProps: []string{"BaseUrl"},
		},
		{
			name:     "https_ok",
			settings: sonarr.Settings{BaseURL: "https://peer.example/sonarr", APIKey: "k"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.settings.Validate()
			if len(got) != len(tc.wantProps) {
				t.Fatalf("Validate failures: got %d (%v) want %d (%v)", len(got), got, len(tc.wantProps), tc.wantProps)
			}
			gotProps := make([]string, len(got))
			for i, f := range got {
				gotProps[i] = f.PropertyName
			}
			for i, want := range tc.wantProps {
				if gotProps[i] != want {
					t.Errorf("failure[%d].PropertyName = %q, want %q", i, gotProps[i], want)
				}
			}
		})
	}
}

func TestDefaultSettings(t *testing.T) {
	t.Parallel()
	d := sonarr.DefaultSettings()
	if d.APIKey != "" {
		t.Errorf("APIKey: got %q want empty", d.APIKey)
	}
	if d.SyncSeasonMonitoring {
		t.Errorf("SyncSeasonMonitoring: got true want false")
	}
	if d.ProfileIDs == nil || d.LanguageProfileIDs == nil || d.TagIDs == nil || d.RootFolderPaths == nil {
		t.Errorf("filter slices: expected non-nil empty, got nils %v", d)
	}
}
