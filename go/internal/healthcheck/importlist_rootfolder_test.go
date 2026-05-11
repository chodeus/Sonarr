package healthcheck_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/healthcheck"
)

type stubAllImportListLister struct {
	all []healthcheck.EnabledImportList
	err error
}

func (s stubAllImportListLister) AllImportLists(_ context.Context) ([]healthcheck.EnabledImportList, error) {
	return s.all, s.err
}

type stubRootFolderPathLister struct {
	paths []string
	err   error
}

func (s stubRootFolderPathLister) AllRootFolderPaths(_ context.Context) ([]string, error) {
	return s.paths, s.err
}

type stubFolderProbe struct {
	exists map[string]bool
}

func (s stubFolderProbe) FolderExists(p string) bool {
	if s.exists == nil {
		return true
	}
	return s.exists[p]
}

func makeImportListRootFolderCheck(t *testing.T, lists []healthcheck.EnabledImportList, paths []string, exists map[string]bool) *healthcheck.ImportListRootFolderCheck {
	t.Helper()
	c, err := healthcheck.NewImportListRootFolderCheck(
		stubAllImportListLister{all: lists},
		stubRootFolderPathLister{paths: paths},
		stubFolderProbe{exists: exists},
	)
	if err != nil {
		t.Fatalf("NewImportListRootFolderCheck: %v", err)
	}
	return c
}

func TestImportListRootFolderCheck_NewRejectsNil(t *testing.T) {
	t.Parallel()
	if _, err := healthcheck.NewImportListRootFolderCheck(nil, stubRootFolderPathLister{}, stubFolderProbe{}); err == nil {
		t.Errorf("nil AllImportListLister: expected error")
	}
	if _, err := healthcheck.NewImportListRootFolderCheck(stubAllImportListLister{}, nil, stubFolderProbe{}); err == nil {
		t.Errorf("nil ImportListRootFolderPathLister: expected error")
	}
	if _, err := healthcheck.NewImportListRootFolderCheck(stubAllImportListLister{}, stubRootFolderPathLister{}, nil); err == nil {
		t.Errorf("nil ImportListFolderProbe: expected error")
	}
}

func TestImportListRootFolderCheck_NoImportLists_OK(t *testing.T) {
	t.Parallel()
	c := makeImportListRootFolderCheck(t, nil, nil, nil)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("expected OK on no import lists, got %v: %s", got.Type, got.Message)
	}
}

func TestImportListRootFolderCheck_AllRootsValid_OK(t *testing.T) {
	t.Parallel()
	lists := []healthcheck.EnabledImportList{
		{ID: 1, Name: "Sonarr Peer", RootFolderPath: "/mnt/tv"},
		{ID: 2, Name: "Plex Watchlist", RootFolderPath: "/mnt/anime"},
	}
	paths := []string{"/mnt/tv", "/mnt/anime"}
	exists := map[string]bool{"/mnt/tv": true, "/mnt/anime": true}
	c := makeImportListRootFolderCheck(t, lists, paths, exists)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("expected OK on all-valid, got %v: %s", got.Type, got.Message)
	}
}

// SingleMissing → Error / ReasonImportListRootFolderMissing.
func TestImportListRootFolderCheck_SingleMissing_Error(t *testing.T) {
	t.Parallel()
	lists := []healthcheck.EnabledImportList{
		{ID: 1, Name: "PeerA", RootFolderPath: "/mnt/tv"},
	}
	// path is missing — not in configured root folders.
	c := makeImportListRootFolderCheck(t, lists, []string{"/mnt/movies"}, map[string]bool{"/mnt/movies": true})
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultError {
		t.Fatalf("expected Error, got %v: %s", got.Type, got.Message)
	}
	if got.Reason != healthcheck.ReasonImportListRootFolderMissing {
		t.Errorf("Reason: got %v want ReasonImportListRootFolderMissing", got.Reason)
	}
	if !strings.Contains(got.Message, "/mnt/tv") || !strings.Contains(got.Message, "PeerA") {
		t.Errorf("Message should contain path + list name: %q", got.Message)
	}
}

// MultipleMissingDistinctRoots → Error /
// ReasonImportListRootFolderMultipleMissing.
func TestImportListRootFolderCheck_MultipleMissingDistinctRoots(t *testing.T) {
	t.Parallel()
	lists := []healthcheck.EnabledImportList{
		{ID: 1, Name: "PeerA", RootFolderPath: "/mnt/tv"},
		{ID: 2, Name: "PeerB", RootFolderPath: "/mnt/anime"},
	}
	c := makeImportListRootFolderCheck(t, lists, nil, nil)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultError {
		t.Fatalf("expected Error, got %v: %s", got.Type, got.Message)
	}
	if got.Reason != healthcheck.ReasonImportListRootFolderMultipleMissing {
		t.Errorf("Reason: got %v want ReasonImportListRootFolderMultipleMissing", got.Reason)
	}
	// Both paths present in message.
	if !strings.Contains(got.Message, "/mnt/tv") || !strings.Contains(got.Message, "/mnt/anime") {
		t.Errorf("Message should contain both paths: %q", got.Message)
	}
	// Joined with " | ".
	if !strings.Contains(got.Message, " | ") {
		t.Errorf("Message should join missing roots with ' | ': %q", got.Message)
	}
}

// Multiple lists share the same missing root → grouped under one
// path with comma-separated names. Mirrors C# `:47-51`.
func TestImportListRootFolderCheck_MultipleListsShareMissingRoot(t *testing.T) {
	t.Parallel()
	lists := []healthcheck.EnabledImportList{
		{ID: 1, Name: "Zebra", RootFolderPath: "/mnt/missing"},
		{ID: 2, Name: "Alpha", RootFolderPath: "/mnt/missing"},
	}
	c := makeImportListRootFolderCheck(t, lists, nil, nil)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultError {
		t.Fatalf("expected Error, got %v: %s", got.Type, got.Message)
	}
	if got.Reason != healthcheck.ReasonImportListRootFolderMissing {
		t.Errorf("Reason: got %v want ReasonImportListRootFolderMissing (single)", got.Reason)
	}
	// Both list names rendered + case-insensitive sorted within the
	// per-path group → "/mnt/missing (Alpha, Zebra)".
	if !strings.Contains(got.Message, "/mnt/missing (Alpha, Zebra)") {
		t.Errorf("Message should group names case-insensitively: %q", got.Message)
	}
}

// Empty path → missing.
func TestImportListRootFolderCheck_EmptyPath_Missing(t *testing.T) {
	t.Parallel()
	lists := []healthcheck.EnabledImportList{
		{ID: 1, Name: "Empty", RootFolderPath: ""},
	}
	c := makeImportListRootFolderCheck(t, lists, []string{"/mnt/tv"}, map[string]bool{"/mnt/tv": true})
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultError {
		t.Errorf("empty path should be Error, got %v: %s", got.Type, got.Message)
	}
}

// Whitespace-only path → missing.
func TestImportListRootFolderCheck_WhitespacePath_Missing(t *testing.T) {
	t.Parallel()
	lists := []healthcheck.EnabledImportList{
		{ID: 1, Name: "WS", RootFolderPath: "   "},
	}
	c := makeImportListRootFolderCheck(t, lists, []string{"/mnt/tv"}, map[string]bool{"/mnt/tv": true})
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultError {
		t.Errorf("whitespace path should be Error, got %v: %s", got.Type, got.Message)
	}
}

// Path in configured roots but disk FolderExists=false → missing.
func TestImportListRootFolderCheck_ConfiguredButGone_Missing(t *testing.T) {
	t.Parallel()
	lists := []healthcheck.EnabledImportList{
		{ID: 1, Name: "Gone", RootFolderPath: "/mnt/usb"},
	}
	c := makeImportListRootFolderCheck(t, lists, []string{"/mnt/usb"}, map[string]bool{"/mnt/usb": false})
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultError {
		t.Errorf("disk-missing path should be Error, got %v: %s", got.Type, got.Message)
	}
}

// Case-insensitive PathEquals: configured "/Mnt/Tv" matches list
// "/mnt/tv".
func TestImportListRootFolderCheck_CaseInsensitiveMatch(t *testing.T) {
	t.Parallel()
	lists := []healthcheck.EnabledImportList{
		{ID: 1, Name: "X", RootFolderPath: "/mnt/tv"},
	}
	// Configured root in mixed case; disk exists at the list's path.
	c := makeImportListRootFolderCheck(t, lists, []string{"/Mnt/Tv"}, map[string]bool{"/mnt/tv": true})
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("case-insensitive match should pass, got %v: %s", got.Type, got.Message)
	}
}

// Trailing-slash on configured root matches a list path without
// the slash.
func TestImportListRootFolderCheck_TrailingSlashMatch(t *testing.T) {
	t.Parallel()
	lists := []healthcheck.EnabledImportList{
		{ID: 1, Name: "X", RootFolderPath: "/mnt/tv"},
	}
	c := makeImportListRootFolderCheck(t, lists, []string{"/mnt/tv/"}, map[string]bool{"/mnt/tv": true})
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("trailing-slash should normalise, got %v: %s", got.Type, got.Message)
	}
}

func TestImportListRootFolderCheck_ListListerError(t *testing.T) {
	t.Parallel()
	c, _ := healthcheck.NewImportListRootFolderCheck(
		stubAllImportListLister{err: errors.New("db down")},
		stubRootFolderPathLister{},
		stubFolderProbe{},
	)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Errorf("list lister error: want Warning, got %v: %s", got.Type, got.Message)
	}
}

func TestImportListRootFolderCheck_RootPathListerError(t *testing.T) {
	t.Parallel()
	c, _ := healthcheck.NewImportListRootFolderCheck(
		stubAllImportListLister{all: []healthcheck.EnabledImportList{{ID: 1, Name: "X", RootFolderPath: "/mnt/tv"}}},
		stubRootFolderPathLister{err: errors.New("rf gone")},
		stubFolderProbe{},
	)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Errorf("rootpath lister error: want Warning, got %v: %s", got.Type, got.Message)
	}
}

func TestImportListRootFolderCheck_Metadata(t *testing.T) {
	t.Parallel()
	c, _ := healthcheck.NewImportListRootFolderCheck(stubAllImportListLister{}, stubRootFolderPathLister{}, stubFolderProbe{})
	if c.Name() != healthcheck.ImportListRootFolderCheckName {
		t.Errorf("Name: got %q", c.Name())
	}
	if !c.CheckOnStartup() {
		t.Errorf("CheckOnStartup: want true")
	}
	if !c.CheckOnSchedule() {
		t.Errorf("CheckOnSchedule: want true")
	}
	if c.EventTriggers() != nil {
		t.Errorf("EventTriggers: want nil (event-driven bindings deferred), got %v", c.EventTriggers())
	}
}
