package healthcheck_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/healthcheck"
)

// stubUpdateCfg is a minimal UpdateCheckConfig.
type stubUpdateCfg struct {
	auto      bool
	mechanism string
	autoErr   error
}

func (s stubUpdateCfg) UpdateAutomatically(_ context.Context) (bool, error) {
	return s.auto, s.autoErr
}

func (s stubUpdateCfg) UpdateMechanism(_ context.Context) (string, error) {
	return s.mechanism, nil
}

// stubUpdateOs is a minimal UpdateCheckOsInfo.
type stubUpdateOs struct {
	docker bool
	darwin bool
}

func (s stubUpdateOs) IsDocker() bool { return s.docker }
func (s stubUpdateOs) IsDarwin() bool { return s.darwin }

// stubUpdateDisk records per-path FolderWritable answers.
type stubUpdateDisk struct {
	writable map[string]bool
}

func (s *stubUpdateDisk) FolderWritable(path string) bool {
	if v, ok := s.writable[path]; ok {
		return v
	}
	return true
}

// stubUpdateLookup returns a fixed AvailableUpdate or an error.
type stubUpdateLookup struct {
	out *healthcheck.AvailableUpdate
	err error
}

func (s stubUpdateLookup) AvailableUpdate(_ context.Context) (*healthcheck.AvailableUpdate, error) {
	return s.out, s.err
}

func TestUpdateCheck_AllPassesPaths(t *testing.T) {
	t.Parallel()
	// Auto=false → step 1-3 gate skipped. No update available → step 4 skipped.
	c, err := healthcheck.NewUpdateCheck(
		&stubUpdateDisk{},
		"/opt/sonarr",
		stubUpdateCfg{auto: false, mechanism: "BuiltIn"},
		stubUpdateOs{},
		stubUpdateLookup{},
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("NewUpdateCheck: %v", err)
	}
	got := c.Check(context.Background())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("Type: got %v, want OK", got.Type)
	}
}

func TestUpdateCheck_TranslocationOnDarwin(t *testing.T) {
	t.Parallel()
	startup := "/private/var/folders/AppTranslocation/abc/d/Sonarr.app/Contents/MacOS"
	c, _ := healthcheck.NewUpdateCheck(
		&stubUpdateDisk{},
		startup,
		stubUpdateCfg{auto: true, mechanism: "BuiltIn"},
		stubUpdateOs{darwin: true},
		stubUpdateLookup{},
		time.Now().UTC(),
	)
	got := c.Check(context.Background())
	if got.Type != healthcheck.ResultError {
		t.Fatalf("Type: got %v, want Error (translocation)", got.Type)
	}
	if got.Reason != healthcheck.ReasonUpdateStartupTranslocation {
		t.Errorf("Reason: got %v, want UpdateStartupTranslocation", got.Reason)
	}
}

func TestUpdateCheck_TranslocationIgnoredOnNonDarwin(t *testing.T) {
	t.Parallel()
	startup := "/var/AppTranslocation/Sonarr"
	c, _ := healthcheck.NewUpdateCheck(
		&stubUpdateDisk{writable: map[string]bool{startup: true, filepath.Join(startup, "UI"): true}},
		startup,
		stubUpdateCfg{auto: true, mechanism: "BuiltIn"},
		stubUpdateOs{darwin: false},
		stubUpdateLookup{},
		time.Now().UTC(),
	)
	got := c.Check(context.Background())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("Type: got %v, want OK (translocation check is Darwin-only)", got.Type)
	}
}

func TestUpdateCheck_StartupNotWritable(t *testing.T) {
	t.Parallel()
	startup := "/opt/sonarr"
	c, _ := healthcheck.NewUpdateCheck(
		&stubUpdateDisk{writable: map[string]bool{startup: false}},
		startup,
		stubUpdateCfg{auto: true, mechanism: "BuiltIn"},
		stubUpdateOs{},
		stubUpdateLookup{},
		time.Now().UTC(),
	)
	got := c.Check(context.Background())
	if got.Type != healthcheck.ResultError {
		t.Fatalf("Type: got %v, want Error", got.Type)
	}
	if got.Reason != healthcheck.ReasonUpdateStartupNotWritable {
		t.Errorf("Reason: got %v, want UpdateStartupNotWritable", got.Reason)
	}
}

func TestUpdateCheck_UiNotWritable(t *testing.T) {
	t.Parallel()
	startup := "/opt/sonarr"
	ui := filepath.Join(startup, "UI")
	c, _ := healthcheck.NewUpdateCheck(
		&stubUpdateDisk{writable: map[string]bool{startup: true, ui: false}},
		startup,
		stubUpdateCfg{auto: true, mechanism: "BuiltIn"},
		stubUpdateOs{},
		stubUpdateLookup{},
		time.Now().UTC(),
	)
	got := c.Check(context.Background())
	if got.Type != healthcheck.ResultError {
		t.Fatalf("Type: got %v, want Error", got.Type)
	}
	if got.Reason != healthcheck.ReasonUpdateUiNotWritable {
		t.Errorf("Reason: got %v, want UpdateUiNotWritable", got.Reason)
	}
}

func TestUpdateCheck_DockerSkipsWritabilityChecks(t *testing.T) {
	t.Parallel()
	startup := "/opt/sonarr"
	c, _ := healthcheck.NewUpdateCheck(
		// Non-writable folders would normally trip step 2.
		&stubUpdateDisk{writable: map[string]bool{startup: false}},
		startup,
		stubUpdateCfg{auto: true, mechanism: "BuiltIn"},
		stubUpdateOs{docker: true},
		stubUpdateLookup{},
		time.Now().UTC(),
	)
	got := c.Check(context.Background())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("Type: got %v, want OK (Docker skips writability gating)", got.Type)
	}
}

func TestUpdateCheck_NonBuiltInSkipsWritabilityChecks(t *testing.T) {
	t.Parallel()
	for _, mech := range []string{"Apt", "Docker", "Script", "External", "apt", "DOCKER"} {
		mech := mech
		t.Run(mech, func(t *testing.T) {
			t.Parallel()
			startup := "/opt/sonarr"
			c, _ := healthcheck.NewUpdateCheck(
				&stubUpdateDisk{writable: map[string]bool{startup: false}},
				startup,
				stubUpdateCfg{auto: true, mechanism: mech},
				stubUpdateOs{},
				stubUpdateLookup{},
				time.Now().UTC(),
			)
			got := c.Check(context.Background())
			if got.Type != healthcheck.ResultOK {
				t.Errorf("Type: got %v, want OK (UpdateMechanism=%q skips writability)", got.Type, mech)
			}
		})
	}
}

func TestUpdateCheck_UpdateAvailableOnOldBuild(t *testing.T) {
	t.Parallel()
	old := time.Now().UTC().AddDate(0, 0, -30)
	c, _ := healthcheck.NewUpdateCheck(
		&stubUpdateDisk{},
		"/opt/sonarr",
		stubUpdateCfg{auto: false, mechanism: "BuiltIn"},
		stubUpdateOs{},
		stubUpdateLookup{out: &healthcheck.AvailableUpdate{Version: "5.0.42"}},
		old,
	)
	got := c.Check(context.Background())
	if got.Type != healthcheck.ResultWarning {
		t.Fatalf("Type: got %v, want Warning", got.Type)
	}
	if got.Reason != healthcheck.ReasonUpdateAvailable {
		t.Errorf("Reason: got %v, want UpdateAvailable", got.Reason)
	}
	if !contains(got.Message, "5.0.42") {
		t.Errorf("Message: got %q, want version", got.Message)
	}
}

func TestUpdateCheck_UpdateAvailableSkippedOnFreshBuild(t *testing.T) {
	t.Parallel()
	fresh := time.Now().UTC().AddDate(0, 0, -3)
	c, _ := healthcheck.NewUpdateCheck(
		&stubUpdateDisk{},
		"/opt/sonarr",
		stubUpdateCfg{auto: false, mechanism: "BuiltIn"},
		stubUpdateOs{},
		stubUpdateLookup{out: &healthcheck.AvailableUpdate{Version: "5.0.42"}},
		fresh,
	)
	got := c.Check(context.Background())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("Type: got %v, want OK (build < 14d old → no nag)", got.Type)
	}
}

func TestUpdateCheck_NilLookupTolerated(t *testing.T) {
	t.Parallel()
	old := time.Now().UTC().AddDate(0, 0, -30)
	c, _ := healthcheck.NewUpdateCheck(
		&stubUpdateDisk{},
		"/opt/sonarr",
		stubUpdateCfg{auto: false, mechanism: "BuiltIn"},
		stubUpdateOs{},
		nil, // no upstream lookup wired yet
		old,
	)
	got := c.Check(context.Background())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("Type: got %v, want OK (nil lookup → no banner)", got.Type)
	}
}

func TestUpdateCheck_LookupErrorTolerated(t *testing.T) {
	t.Parallel()
	old := time.Now().UTC().AddDate(0, 0, -30)
	c, _ := healthcheck.NewUpdateCheck(
		&stubUpdateDisk{},
		"/opt/sonarr",
		stubUpdateCfg{auto: false, mechanism: "BuiltIn"},
		stubUpdateOs{},
		stubUpdateLookup{err: errors.New("network")},
		old,
	)
	got := c.Check(context.Background())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("Type: got %v, want OK (lookup error degrades to silent)", got.Type)
	}
}

func TestNewUpdateCheck_RequiredArgs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		fn   func() (*healthcheck.UpdateCheck, error)
	}{
		{"nil disk", func() (*healthcheck.UpdateCheck, error) {
			return healthcheck.NewUpdateCheck(nil, "/x", stubUpdateCfg{}, stubUpdateOs{}, nil, time.Time{})
		}},
		{"nil cfg", func() (*healthcheck.UpdateCheck, error) {
			return healthcheck.NewUpdateCheck(&stubUpdateDisk{}, "/x", nil, stubUpdateOs{}, nil, time.Time{})
		}},
		{"nil osInfo", func() (*healthcheck.UpdateCheck, error) {
			return healthcheck.NewUpdateCheck(&stubUpdateDisk{}, "/x", stubUpdateCfg{}, nil, nil, time.Time{})
		}},
		{"empty startup", func() (*healthcheck.UpdateCheck, error) {
			return healthcheck.NewUpdateCheck(&stubUpdateDisk{}, "  ", stubUpdateCfg{}, stubUpdateOs{}, nil, time.Time{})
		}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := tc.fn(); err == nil {
				t.Errorf("expected error")
			}
		})
	}
}

// Tiny helper to avoid pulling in `strings` for tests.
func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
