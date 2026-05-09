package main

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/namingconfig"
	"sonarr.tv/sonarr/internal/series"
)

// stubNaming feeds a pinned namingconfig.Config + optional error.
type stubNaming struct {
	cfg *namingconfig.Config
	err error
}

func (s stubNaming) GetConfig(_ context.Context) (*namingconfig.Config, error) {
	return s.cfg, s.err
}

// stubRootFolders feeds a pinned best-path + optional error.
type stubRootFolders struct {
	best string
	err  error
}

func (s stubRootFolders) GetBestRootFolderPath(_ context.Context, _ string) (string, error) {
	return s.best, s.err
}

func newAdapter(naming seriesPathBuilderNamingReader, rf seriesPathBuilderRootFolderResolver) seriesPathBuilderAdapter {
	return seriesPathBuilderAdapter{naming: naming, rootFolders: rf, log: slog.New(slog.DiscardHandler)}
}

func TestSeriesPathBuilder_NilSeriesErrors(t *testing.T) {
	t.Parallel()
	a := newAdapter(stubNaming{}, stubRootFolders{})
	if _, err := a.BuildPath(context.Background(), nil, false); err == nil {
		t.Errorf("nil series: want error")
	}
}

func TestSeriesPathBuilder_EmptyRootFolderErrors(t *testing.T) {
	t.Parallel()
	a := newAdapter(stubNaming{}, stubRootFolders{})
	s := &series.Series{Path: "/tv/Show", RootFolderPath: ""}
	if _, err := a.BuildPath(context.Background(), s, true); err == nil {
		t.Errorf("empty RootFolderPath: want error")
	}
}

func TestSeriesPathBuilder_FreshPathFromNamingConfig(t *testing.T) {
	t.Parallel()
	a := newAdapter(
		stubNaming{cfg: &namingconfig.Config{SeriesFolderFormat: "{Series Title}"}},
		stubRootFolders{},
	)
	s := &series.Series{
		Title:          "Show",
		RootFolderPath: "/tv",
	}
	got, err := a.BuildPath(context.Background(), s, false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "/tv/Show" {
		t.Errorf("got %q, want '/tv/Show'", got)
	}
}

func TestSeriesPathBuilder_FreshPathDefaultFormat(t *testing.T) {
	t.Parallel()
	// Empty SeriesFolderFormat → defaults to "{Series Title}".
	a := newAdapter(
		stubNaming{cfg: &namingconfig.Config{SeriesFolderFormat: ""}},
		stubRootFolders{},
	)
	s := &series.Series{Title: "Show", RootFolderPath: "/tv"}
	got, err := a.BuildPath(context.Background(), s, false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "/tv/Show" {
		t.Errorf("got %q, want '/tv/Show' (default fmt)", got)
	}
}

func TestSeriesPathBuilder_NamingConfigErrorPropagates(t *testing.T) {
	t.Parallel()
	cfgErr := errors.New("naming down")
	a := newAdapter(stubNaming{err: cfgErr}, stubRootFolders{})
	s := &series.Series{Title: "Show", RootFolderPath: "/tv"}
	_, err := a.BuildPath(context.Background(), s, false)
	if err == nil {
		t.Fatal("want error")
	}
	if !errors.Is(err, cfgErr) {
		t.Errorf("error chain missing naming err: %v", err)
	}
}

func TestSeriesPathBuilder_UseExistingRelativeFolderResolves(t *testing.T) {
	t.Parallel()
	// best=/tv/old; series.Path=/tv/old/MyShow → rel=MyShow → /tv-new/MyShow.
	a := newAdapter(
		stubNaming{},
		stubRootFolders{best: "/tv/old"},
	)
	s := &series.Series{
		Path:           "/tv/old/MyShow",
		RootFolderPath: "/tv-new",
	}
	got, err := a.BuildPath(context.Background(), s, true)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "/tv-new/MyShow" {
		t.Errorf("got %q, want '/tv-new/MyShow'", got)
	}
}

func TestSeriesPathBuilder_UseExistingRelativeNestedDirs(t *testing.T) {
	t.Parallel()
	a := newAdapter(
		stubNaming{},
		stubRootFolders{best: "/mnt/media"},
	)
	s := &series.Series{
		Path:           "/mnt/media/series/MyShow",
		RootFolderPath: "/tv-new",
	}
	got, err := a.BuildPath(context.Background(), s, true)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "/tv-new/series/MyShow" {
		t.Errorf("got %q, want '/tv-new/series/MyShow'", got)
	}
}

func TestSeriesPathBuilder_UseExistingNoParentMatchFallsBackToBasename(t *testing.T) {
	t.Parallel()
	// best="" or non-parent → fallback uses filepath.Base(series.Path).
	a := newAdapter(
		stubNaming{},
		stubRootFolders{best: "/different/root"},
	)
	s := &series.Series{
		Path:           "/tv/old/MyShow",
		RootFolderPath: "/tv-new",
	}
	got, err := a.BuildPath(context.Background(), s, true)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "/tv-new/MyShow" {
		t.Errorf("got %q, want '/tv-new/MyShow' (basename fallback)", got)
	}
}

func TestSeriesPathBuilder_UseExistingEmptyBestFallsBack(t *testing.T) {
	t.Parallel()
	a := newAdapter(
		stubNaming{},
		stubRootFolders{best: ""}, // no parent root found
	)
	s := &series.Series{
		Path:           "/tv/old/MyShow",
		RootFolderPath: "/tv-new",
	}
	got, err := a.BuildPath(context.Background(), s, true)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "/tv-new/MyShow" {
		t.Errorf("got %q, want '/tv-new/MyShow'", got)
	}
}

func TestSeriesPathBuilder_UseExistingEmptySeriesPathFallsToFreshArm(t *testing.T) {
	t.Parallel()
	// useExistingRelativeFolder=true but series.Path="" → fresh arm
	// (matches C# `useExistingRelativeFolder && series.Path.IsNotNullOrWhiteSpace()`).
	a := newAdapter(
		stubNaming{cfg: &namingconfig.Config{SeriesFolderFormat: "{Series Title}"}},
		stubRootFolders{},
	)
	s := &series.Series{
		Title:          "FreshShow",
		Path:           "", // empty
		RootFolderPath: "/tv",
	}
	got, err := a.BuildPath(context.Background(), s, true)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "/tv/FreshShow" {
		t.Errorf("got %q, want '/tv/FreshShow' (fresh arm)", got)
	}
}

func TestSeriesPathBuilder_UseExistingRootFolderErrorPropagates(t *testing.T) {
	t.Parallel()
	rfErr := errors.New("rf down")
	a := newAdapter(stubNaming{}, stubRootFolders{err: rfErr})
	s := &series.Series{Path: "/tv/old/MyShow", RootFolderPath: "/tv-new"}
	_, err := a.BuildPath(context.Background(), s, true)
	if err == nil {
		t.Fatal("want error")
	}
	if !errors.Is(err, rfErr) {
		t.Errorf("error chain missing rf err: %v", err)
	}
}

func TestIsParentPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		parent, child string
		want          bool
	}{
		{"/tv", "/tv/Show", true},
		{"/tv", "/tv/Show/Season 01", true},
		{"/tv", "/tv", false},     // not own parent
		{"/tv", "/movies", false}, // unrelated
		{"/tv", "/", false},
		{"/", "/tv", true},
		{"/tv//", "/tv/Show", true}, // dirty parent normalised
	}
	for _, c := range cases {
		got := isParentPath(c.parent, c.child)
		if got != c.want {
			t.Errorf("isParentPath(%q, %q) = %v, want %v", c.parent, c.child, got, c.want)
		}
	}
}

func TestDefaultIfEmpty(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, fallback, want string
	}{
		{"value", "fb", "value"},
		{"", "fb", "fb"},
		{"   ", "fb", "fb"},
		{"\t\n", "fb", "fb"},
	}
	for _, c := range cases {
		got := defaultIfEmpty(c.in, c.fallback)
		if got != c.want {
			t.Errorf("defaultIfEmpty(%q, %q) = %q, want %q", c.in, c.fallback, got, c.want)
		}
	}
}

// Sanity check the adapter is exported via the right interface name.
func TestSeriesPathBuilder_InterfaceShape(t *testing.T) {
	t.Parallel()
	a := newAdapter(stubNaming{}, stubRootFolders{})
	var _ series.SeriesPathBuilder = a
	// No assertions — compile-time guarantees the contract. The test
	// just protects against accidental signature drift.
	if !strings.Contains("BuildPath", "Build") {
		t.Fatal("sanity check failed")
	}
}
