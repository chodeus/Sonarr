package healthcheck_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/series"
)

// stubSeriesPaths returns a fixed map.
type stubSeriesPaths struct {
	paths map[int]string
	err   error
}

func (s stubSeriesPaths) AllSeriesPaths(_ context.Context) (map[int]string, error) {
	return s.paths, s.err
}

// stubResolver maps each series path to a root via the prefix table.
// Falls back to filepath.Dir-equivalent when no prefix matches.
type stubResolver struct {
	mapping map[string]string
	err     error
}

func (s stubResolver) GetBestRootFolderPath(_ context.Context, p string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	if r, ok := s.mapping[p]; ok {
		return r, nil
	}
	return "", nil
}

// stubDisk reports per-path existence/emptiness from maps.
type stubDisk struct {
	exists map[string]bool
	empty  map[string]bool
}

func (s stubDisk) FolderExists(p string) bool { return s.exists[p] }
func (s stubDisk) FolderEmpty(p string) bool  { return s.empty[p] }

func newRootFolderTestCheck(seriesPaths map[int]string, mapping map[string]string, exists, empty map[string]bool) *healthcheck.RootFolderCheck {
	return healthcheck.NewRootFolderCheck(
		stubSeriesPaths{paths: seriesPaths},
		stubResolver{mapping: mapping},
		stubDisk{exists: exists, empty: empty},
	)
}

func TestRootFolderCheck_NewRequiresAllDeps(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic on nil deps")
		}
	}()
	healthcheck.NewRootFolderCheck(nil, nil, nil)
}

func TestRootFolderCheck_PassesWhenAllRootsHealthy(t *testing.T) {
	t.Parallel()
	c := newRootFolderTestCheck(
		map[int]string{1: "/mnt/tv/Show1", 2: "/mnt/tv/Show2"},
		map[string]string{"/mnt/tv/Show1": "/mnt/tv", "/mnt/tv/Show2": "/mnt/tv"},
		map[string]bool{"/mnt/tv": true},
		map[string]bool{},
	)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("expected OK, got %v: %s", got.Type, got.Message)
	}
}

func TestRootFolderCheck_SingleMissingRootFolderErrors(t *testing.T) {
	t.Parallel()
	c := newRootFolderTestCheck(
		map[int]string{1: "/mnt/tv/Show1"},
		map[string]string{"/mnt/tv/Show1": "/mnt/tv"},
		map[string]bool{"/mnt/tv": false}, // missing
		map[string]bool{},
	)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultError {
		t.Errorf("expected Error, got %v", got.Type)
	}
	if got.Reason != healthcheck.ReasonRootFolderMissing {
		t.Errorf("Reason = %v, want ReasonRootFolderMissing", got.Reason)
	}
	if !strings.Contains(got.Message, "/mnt/tv") {
		t.Errorf("Message %q should mention path", got.Message)
	}
}

func TestRootFolderCheck_MultipleMissingRootFoldersErrors(t *testing.T) {
	t.Parallel()
	c := newRootFolderTestCheck(
		map[int]string{1: "/mnt/tv/Show1", 2: "/mnt/anime/Show2"},
		map[string]string{"/mnt/tv/Show1": "/mnt/tv", "/mnt/anime/Show2": "/mnt/anime"},
		map[string]bool{"/mnt/tv": false, "/mnt/anime": false},
		map[string]bool{},
	)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultError {
		t.Errorf("expected Error, got %v", got.Type)
	}
	if got.Reason != healthcheck.ReasonRootFolderMultipleMissing {
		t.Errorf("Reason = %v, want ReasonRootFolderMultipleMissing", got.Reason)
	}
	// Output uses ` | ` separator (matches C#).
	if !strings.Contains(got.Message, " | ") {
		t.Errorf("Message %q should contain ` | ` separator", got.Message)
	}
	if !strings.Contains(got.Message, "/mnt/tv") || !strings.Contains(got.Message, "/mnt/anime") {
		t.Errorf("Message %q missing one of the paths", got.Message)
	}
}

func TestRootFolderCheck_SingleEmptyRootFolderWarns(t *testing.T) {
	t.Parallel()
	c := newRootFolderTestCheck(
		map[int]string{1: "/mnt/tv/Show1"},
		map[string]string{"/mnt/tv/Show1": "/mnt/tv"},
		map[string]bool{"/mnt/tv": true},
		map[string]bool{"/mnt/tv": true},
	)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Errorf("expected Warning, got %v", got.Type)
	}
	if got.Reason != healthcheck.ReasonRootFolderEmpty {
		t.Errorf("Reason = %v, want ReasonRootFolderEmpty", got.Reason)
	}
}

func TestRootFolderCheck_MultipleEmptyRootFoldersWarn(t *testing.T) {
	t.Parallel()
	c := newRootFolderTestCheck(
		map[int]string{1: "/mnt/tv/Show1", 2: "/mnt/anime/Show2"},
		map[string]string{"/mnt/tv/Show1": "/mnt/tv", "/mnt/anime/Show2": "/mnt/anime"},
		map[string]bool{"/mnt/tv": true, "/mnt/anime": true},
		map[string]bool{"/mnt/tv": true, "/mnt/anime": true},
	)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Errorf("expected Warning, got %v", got.Type)
	}
	// C# uses single `RootFolderEmpty` reason for both — the multi-empty
	// case keeps the same reason but a different localized message.
	if got.Reason != healthcheck.ReasonRootFolderEmpty {
		t.Errorf("Reason = %v, want ReasonRootFolderEmpty", got.Reason)
	}
	if !strings.Contains(got.Message, " | ") {
		t.Errorf("Message %q should contain ` | ` separator", got.Message)
	}
}

func TestRootFolderCheck_MissingTakesPriorityOverEmpty(t *testing.T) {
	t.Parallel()
	// One root missing + one root empty → MissingMultiple is NOT
	// triggered (only one missing); but the missing root takes
	// precedence over the empty warning.
	c := newRootFolderTestCheck(
		map[int]string{1: "/mnt/tv/Show1", 2: "/mnt/anime/Show2"},
		map[string]string{"/mnt/tv/Show1": "/mnt/tv", "/mnt/anime/Show2": "/mnt/anime"},
		map[string]bool{"/mnt/tv": true, "/mnt/anime": false},
		map[string]bool{"/mnt/tv": true},
	)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultError {
		t.Errorf("expected Error (missing takes priority), got %v", got.Type)
	}
}

func TestRootFolderCheck_DistinctRootsDeduplicated(t *testing.T) {
	t.Parallel()
	// Three series sharing one root → only one missing-root reported.
	c := newRootFolderTestCheck(
		map[int]string{1: "/mnt/tv/A", 2: "/mnt/tv/B", 3: "/mnt/tv/C"},
		map[string]string{"/mnt/tv/A": "/mnt/tv", "/mnt/tv/B": "/mnt/tv", "/mnt/tv/C": "/mnt/tv"},
		map[string]bool{"/mnt/tv": false},
		map[string]bool{},
	)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultError {
		t.Errorf("expected Error, got %v", got.Type)
	}
	if got.Reason != healthcheck.ReasonRootFolderMissing {
		t.Errorf("expected single-missing reason, got %v", got.Reason)
	}
}

func TestRootFolderCheck_SeriesPathsErrorWarns(t *testing.T) {
	t.Parallel()
	c := healthcheck.NewRootFolderCheck(
		stubSeriesPaths{err: errors.New("db down")},
		stubResolver{},
		stubDisk{},
	)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Errorf("expected Warning, got %v", got.Type)
	}
	if !strings.Contains(got.Message, "db down") {
		t.Errorf("Message %q should mention error", got.Message)
	}
}

func TestRootFolderCheck_NonAbsolutePathFlaggedAsMissing(t *testing.T) {
	t.Parallel()
	c := newRootFolderTestCheck(
		map[int]string{1: "/mnt/tv/Show"},
		map[string]string{"/mnt/tv/Show": "relative/path"}, // non-absolute → invalid
		map[string]bool{},
		map[string]bool{},
	)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultError {
		t.Errorf("expected Error on relative path, got %v", got.Type)
	}
}

func TestRootFolderCheck_CheckOnStartupAndScheduleDefaults(t *testing.T) {
	t.Parallel()
	c := newRootFolderTestCheck(map[int]string{}, map[string]string{}, map[string]bool{}, map[string]bool{})
	if !c.CheckOnStartup() {
		t.Errorf("CheckOnStartup should default true")
	}
	if !c.CheckOnSchedule() {
		t.Errorf("CheckOnSchedule should default true")
	}
}

func TestRootFolderCheck_EventTriggersIncludeSeriesDeleted(t *testing.T) {
	t.Parallel()
	c := newRootFolderTestCheck(map[int]string{}, map[string]string{}, map[string]bool{}, map[string]bool{})
	triggers := c.EventTriggers()
	if len(triggers) == 0 {
		t.Fatalf("expected at least one EventTrigger")
	}
	want := reflect.TypeOf(series.SeriesDeletedEvent{})
	found := false
	for _, tr := range triggers {
		if tr.EventType == want {
			found = true
		}
	}
	if !found {
		t.Errorf("expected SeriesDeletedEvent trigger, got %+v", triggers)
	}
}

func TestRootFolderCheck_NameAndInterfaceShape(t *testing.T) {
	t.Parallel()
	c := newRootFolderTestCheck(map[int]string{}, map[string]string{}, map[string]bool{}, map[string]bool{})
	if c.Name() != healthcheck.RootFolderCheckName {
		t.Errorf("Name = %q", c.Name())
	}
	var iface healthcheck.Check = c
	_ = iface
	var iface2 healthcheck.EventDrivenCheck = c
	_ = iface2
}
