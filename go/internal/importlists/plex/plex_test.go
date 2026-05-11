package plex_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/importlists"
	"sonarr.tv/sonarr/internal/importlists/plex"
)

// Settings.Validate ports the C# `PlexListSettingsValidator` rules
// at `PlexListSettings.cs:7-15` — AccessToken NotEmpty with
// PropertyName overridden to "SignIn".
func TestSettings_Validate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		settings  plex.Settings
		wantProps []string
		wantMsg   string
	}{
		{
			name:     "valid_authenticated",
			settings: plex.Settings{AccessToken: "tok"},
		},
		{
			name:      "missing_access_token",
			settings:  plex.Settings{},
			wantProps: []string{"SignIn"},
			wantMsg:   "Must authenticate with Plex",
		},
		{
			name:      "whitespace_access_token",
			settings:  plex.Settings{AccessToken: "   "},
			wantProps: []string{"SignIn"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.settings.Validate()
			if len(got) != len(tc.wantProps) {
				t.Fatalf("Validate failures: got %d (%v) want %d", len(got), got, len(tc.wantProps))
			}
			for i, want := range tc.wantProps {
				if got[i].PropertyName != want {
					t.Errorf("failure[%d].PropertyName = %q, want %q", i, got[i].PropertyName, want)
				}
			}
			if tc.wantMsg != "" && got[0].ErrorMessage != tc.wantMsg {
				t.Errorf("ErrorMessage = %q, want %q", got[0].ErrorMessage, tc.wantMsg)
			}
		})
	}
}

func TestDefaultSettings(t *testing.T) {
	t.Parallel()
	d := plex.DefaultSettings()
	if d.SignIn != "startOAuth" {
		t.Errorf("SignIn: got %q want startOAuth", d.SignIn)
	}
	if d.AccessToken != "" || d.BaseURL != "" {
		t.Errorf("expected empty AccessToken/BaseURL, got %+v", d)
	}
}

// Settings is a [importlists.Settings] implementer — verify the
// interface satisfaction here so signature drift on the marker
// breaks tests.
func TestSettings_ImplementsMarker(t *testing.T) {
	t.Parallel()
	var _ importlists.Settings = (*plex.Settings)(nil)
}
