package healthcheck_test

import (
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/healthcheck"
)

// TestNew_DerivesWikiFragmentFromMessage mirrors the C# fixture at
// `HealthCheckFixture.cs` — the no-fragment case.
func TestNew_DerivesWikiFragmentFromMessage(t *testing.T) {
	t.Parallel()
	hc := healthcheck.New(
		"FakeCheck",
		healthcheck.ResultWarning,
		healthcheck.ReasonServerNotification,
		"I blew up because of some weird user mistake",
		"",
	)
	want := "https://wiki.servarr.com/sonarr/system#i-blew-up-because-of-some-weird-user-mistake"
	if hc.WikiURL != want {
		t.Errorf("WikiURL = %q, want %q", hc.WikiURL, want)
	}
}

// TestNew_AcceptsAnchorFragment exercises the explicit `#anchor` form.
func TestNew_AcceptsAnchorFragment(t *testing.T) {
	t.Parallel()
	hc := healthcheck.New(
		"FakeCheck",
		healthcheck.ResultWarning,
		healthcheck.ReasonServerNotification,
		"I blew up because of some weird user mistake",
		"#my-health-check",
	)
	want := "https://wiki.servarr.com/sonarr/system#my-health-check"
	if hc.WikiURL != want {
		t.Errorf("WikiURL = %q, want %q", hc.WikiURL, want)
	}
}

// TestNew_AcceptsCustomPageFragment exercises the `page#anchor` form,
// where the fragment carries a different wiki page than `system`.
func TestNew_AcceptsCustomPageFragment(t *testing.T) {
	t.Parallel()
	hc := healthcheck.New(
		"FakeCheck",
		healthcheck.ResultWarning,
		healthcheck.ReasonServerNotification,
		"I blew up because of some weird user mistake",
		"custom_page#my-health-check",
	)
	want := "https://wiki.servarr.com/sonarr/custom_page#my-health-check"
	if hc.WikiURL != want {
		t.Errorf("WikiURL = %q, want %q", hc.WikiURL, want)
	}
}

// TestNew_StripsNonLetterCharsForDerivedFragment pins the cleanup
// regex behaviour: punctuation gone, digits gone, spaces → hyphens.
func TestNew_StripsNonLetterCharsForDerivedFragment(t *testing.T) {
	t.Parallel()
	hc := healthcheck.New(
		"FakeCheck",
		healthcheck.ResultError,
		healthcheck.ReasonRootFolderMissing,
		"Root folder /tmp/foo (id=42) is missing!",
		"",
	)
	if !strings.HasPrefix(hc.WikiURL, "https://wiki.servarr.com/sonarr/system#") {
		t.Fatalf("WikiURL prefix wrong: %q", hc.WikiURL)
	}
	want := "https://wiki.servarr.com/sonarr/system#root-folder-tmpfoo-id-is-missing"
	if hc.WikiURL != want {
		t.Errorf("WikiURL = %q, want %q", hc.WikiURL, want)
	}
}

// TestOK_SkipsWikiURL — passing checks must not carry a wiki URL.
func TestOK_SkipsWikiURL(t *testing.T) {
	t.Parallel()
	hc := healthcheck.OK("X")
	if hc.WikiURL != "" {
		t.Errorf("OK() WikiURL = %q, want empty", hc.WikiURL)
	}
	if hc.Type != healthcheck.ResultOK {
		t.Errorf("OK() Type = %v, want ResultOK", hc.Type)
	}
}

// TestResult_StringRoundtrip pins the lower-case names against the
// v3 JSON wire format.
func TestResult_StringRoundtrip(t *testing.T) {
	t.Parallel()
	cases := []struct {
		r    healthcheck.Result
		want string
	}{
		{healthcheck.ResultOK, "ok"},
		{healthcheck.ResultNotice, "notice"},
		{healthcheck.ResultWarning, "warning"},
		{healthcheck.ResultError, "error"},
		{healthcheck.Result(99), "unknown"},
	}
	for _, tc := range cases {
		if got := tc.r.String(); got != tc.want {
			t.Errorf("Result(%d).String() = %q, want %q", tc.r, got, tc.want)
		}
	}
}

// TestReason_OrderPin guards against accidental re-ordering of the
// Reason enum (which would silently shift every wire value). Pins
// the first entry to 0 and a representative middle entry.
func TestReason_OrderPin(t *testing.T) {
	t.Parallel()
	if int(healthcheck.ReasonAppDataLocation) != 0 {
		t.Errorf("ReasonAppDataLocation = %d, want 0", healthcheck.ReasonAppDataLocation)
	}
	if int(healthcheck.ReasonMinimumApiKeyLength) != 26 {
		t.Errorf("ReasonMinimumApiKeyLength = %d, want 26", healthcheck.ReasonMinimumApiKeyLength)
	}
	if int(healthcheck.ReasonRootFolderMissing) != 53 {
		t.Errorf("ReasonRootFolderMissing = %d, want 53", healthcheck.ReasonRootFolderMissing)
	}
}
