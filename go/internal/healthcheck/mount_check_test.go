package healthcheck_test

import (
	"context"
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/disk"
	"sonarr.tv/sonarr/internal/healthcheck"
)

type stubMountSeriesPaths struct {
	out map[int]string
	err error
}

func (s stubMountSeriesPaths) AllSeriesPaths(_ context.Context) (map[int]string, error) {
	return s.out, s.err
}

type stubMountResolver struct {
	byPath map[string]*disk.Mount
	errFor map[string]error
}

func (s stubMountResolver) GetMount(path string) (*disk.Mount, error) {
	if e, ok := s.errFor[path]; ok {
		return nil, e
	}
	if m, ok := s.byPath[path]; ok {
		return m, nil
	}
	return nil, errors.New("not found")
}

func TestMountCheck_AllWritablePasses(t *testing.T) {
	t.Parallel()
	c, _ := healthcheck.NewMountCheck(
		stubMountSeriesPaths{out: map[int]string{1: "/data/show-a", 2: "/data/show-b"}},
		stubMountResolver{byPath: map[string]*disk.Mount{
			"/data/show-a": {Name: "/data", RootDirectory: "/data", IsReadOnly: false},
			"/data/show-b": {Name: "/data", RootDirectory: "/data", IsReadOnly: false},
		}},
	)
	got := c.Check(context.Background())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("Type: got %v, want OK", got.Type)
	}
}

func TestMountCheck_ReadOnlyMountFiresError(t *testing.T) {
	t.Parallel()
	c, _ := healthcheck.NewMountCheck(
		stubMountSeriesPaths{out: map[int]string{1: "/ro/show-x"}},
		stubMountResolver{byPath: map[string]*disk.Mount{
			"/ro/show-x": {Name: "/ro", RootDirectory: "/ro", IsReadOnly: true},
		}},
	)
	got := c.Check(context.Background())
	if got.Type != healthcheck.ResultError {
		t.Fatalf("Type: got %v, want Error", got.Type)
	}
	if got.Reason != healthcheck.ReasonMountSeries {
		t.Errorf("Reason: got %v, want MountSeries", got.Reason)
	}
	if !contains(got.Message, "/ro") || !contains(got.Message, "/ro/show-x") {
		t.Errorf("Message: got %q, want mount name + path", got.Message)
	}
}

func TestMountCheck_DeduplicatesByRoot(t *testing.T) {
	t.Parallel()
	// Three series share a single read-only mount root → message
	// should only mention the root once.
	c, _ := healthcheck.NewMountCheck(
		stubMountSeriesPaths{out: map[int]string{1: "/ro/a", 2: "/ro/b", 3: "/ro/c"}},
		stubMountResolver{byPath: map[string]*disk.Mount{
			"/ro/a": {Name: "/ro", RootDirectory: "/ro", IsReadOnly: true},
			"/ro/b": {Name: "/ro", RootDirectory: "/ro", IsReadOnly: true},
			"/ro/c": {Name: "/ro", RootDirectory: "/ro", IsReadOnly: true},
		}},
	)
	got := c.Check(context.Background())
	if got.Type != healthcheck.ResultError {
		t.Fatalf("Type: got %v, want Error", got.Type)
	}
	// First-seen path wins by series-ID order: /ro/a (ID=1).
	if !contains(got.Message, "/ro/a") {
		t.Errorf("Message: got %q, want /ro/a (first by series ID)", got.Message)
	}
	if contains(got.Message, "/ro/b") || contains(got.Message, "/ro/c") {
		t.Errorf("Message: got %q, want b/c de-duplicated", got.Message)
	}
}

func TestMountCheck_MixedReadOnlyAndWritable(t *testing.T) {
	t.Parallel()
	c, _ := healthcheck.NewMountCheck(
		stubMountSeriesPaths{out: map[int]string{
			1: "/rw/show-a",
			2: "/ro/show-x",
			3: "/rw/show-b",
		}},
		stubMountResolver{byPath: map[string]*disk.Mount{
			"/rw/show-a": {Name: "/rw", RootDirectory: "/rw", IsReadOnly: false},
			"/ro/show-x": {Name: "/ro", RootDirectory: "/ro", IsReadOnly: true},
			"/rw/show-b": {Name: "/rw", RootDirectory: "/rw", IsReadOnly: false},
		}},
	)
	got := c.Check(context.Background())
	if got.Type != healthcheck.ResultError {
		t.Fatalf("Type: got %v, want Error", got.Type)
	}
	if !contains(got.Message, "/ro") {
		t.Errorf("Message: got %q, want /ro", got.Message)
	}
	if contains(got.Message, "/rw") {
		t.Errorf("Message: got %q, want /rw filtered", got.Message)
	}
}

func TestMountCheck_LookupErrorSkipsPath(t *testing.T) {
	t.Parallel()
	c, _ := healthcheck.NewMountCheck(
		stubMountSeriesPaths{out: map[int]string{1: "/data/a", 2: "/missing/b"}},
		stubMountResolver{
			byPath: map[string]*disk.Mount{
				"/data/a": {Name: "/data", RootDirectory: "/data", IsReadOnly: false},
			},
			errFor: map[string]error{"/missing/b": errors.New("ENOENT")},
		},
	)
	got := c.Check(context.Background())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("Type: got %v, want OK (lookup-failed paths are skipped silently)", got.Type)
	}
}

func TestMountCheck_EmptySeriesPathSkipped(t *testing.T) {
	t.Parallel()
	c, _ := healthcheck.NewMountCheck(
		stubMountSeriesPaths{out: map[int]string{1: ""}},
		stubMountResolver{},
	)
	got := c.Check(context.Background())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("Type: got %v, want OK (empty paths are skipped)", got.Type)
	}
}

func TestMountCheck_SeriesLookupErrorPasses(t *testing.T) {
	t.Parallel()
	c, _ := healthcheck.NewMountCheck(
		stubMountSeriesPaths{err: errors.New("DB unavailable")},
		stubMountResolver{},
	)
	got := c.Check(context.Background())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("Type: got %v, want OK (series lookup failure → silent pass)", got.Type)
	}
}

func TestNewMountCheck_RequiredArgs(t *testing.T) {
	t.Parallel()
	if _, err := healthcheck.NewMountCheck(nil, stubMountResolver{}); err == nil {
		t.Errorf("nil series: want error")
	}
	if _, err := healthcheck.NewMountCheck(stubMountSeriesPaths{}, nil); err == nil {
		t.Errorf("nil resolver: want error")
	}
}
