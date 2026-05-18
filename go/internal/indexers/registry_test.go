package indexers_test

import (
	"context"
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/indexers"
)

type stubProvider struct{ failures []indexers.TestFailure }

func (s *stubProvider) Test(_ context.Context) ([]indexers.TestFailure, error) {
	return s.failures, nil
}
func (s *stubProvider) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return nil, nil
}

func factory(_ *indexers.Indexer, _ indexers.Dependencies) (indexers.Provider, error) {
	return &stubProvider{}, nil
}

func TestRegistry_RegisterLookupHas(t *testing.T) {
	t.Parallel()
	r := indexers.NewRegistry()
	if err := r.Register("Newznab", "NewznabSettings", factory); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if !r.Has("Newznab", "NewznabSettings") {
		t.Errorf("Has returned false for registered key")
	}
	if _, err := r.Lookup("Newznab", "NewznabSettings"); err != nil {
		t.Errorf("Lookup: %v", err)
	}
	if _, err := r.Lookup("Nope", "Nope"); !errors.Is(err, indexers.ErrNotRegistered) {
		t.Errorf("Lookup miss: %v", err)
	}
}

func TestRegistry_DoubleRegister(t *testing.T) {
	t.Parallel()
	r := indexers.NewRegistry()
	_ = r.Register("A", "ASettings", factory)
	if err := r.Register("A", "ASettings", factory); !errors.Is(err, indexers.ErrAlreadyRegistered) {
		t.Errorf("expected ErrAlreadyRegistered: %v", err)
	}
}

func TestRegistry_EmptyKeysRejected(t *testing.T) {
	t.Parallel()
	r := indexers.NewRegistry()
	if err := r.Register("", "X", factory); err == nil {
		t.Error("empty impl must be rejected")
	}
	if err := r.Register("X", "", factory); err == nil {
		t.Error("empty contract must be rejected")
	}
	if err := r.Register("X", "Y", nil); err == nil {
		t.Error("nil factory must be rejected")
	}
}

func TestRegistry_Construct_Success(t *testing.T) {
	t.Parallel()
	r := indexers.NewRegistry()
	_ = r.Register("Newznab", "NewznabSettings", factory)
	cfg := "NewznabSettings"
	row := &indexers.Indexer{Implementation: "Newznab", ConfigContract: &cfg}
	p, err := r.Construct(row, indexers.Dependencies{})
	if err != nil {
		t.Fatalf("Construct: %v", err)
	}
	if p == nil {
		t.Fatal("Construct returned nil Provider")
	}
}

func TestRegistry_Construct_Missing(t *testing.T) {
	t.Parallel()
	r := indexers.NewRegistry()
	cfg := "NopeSettings"
	row := &indexers.Indexer{Implementation: "Nope", ConfigContract: &cfg}
	_, err := r.Construct(row, indexers.Dependencies{})
	if !errors.Is(err, indexers.ErrNotRegistered) {
		t.Errorf("expected ErrNotRegistered: %v", err)
	}
}

func TestRegistry_Construct_NilRow(t *testing.T) {
	t.Parallel()
	r := indexers.NewRegistry()
	if _, err := r.Construct(nil, indexers.Dependencies{}); err == nil {
		t.Error("nil row must error")
	}
}

func TestDefault_IsSingleton(t *testing.T) {
	t.Parallel()
	a := indexers.Default()
	b := indexers.Default()
	if a != b {
		t.Error("Default() must return the same singleton")
	}
}

func TestRegistry_Names_Sorted(t *testing.T) {
	t.Parallel()
	r := indexers.NewRegistry()
	_ = r.Register("Z", "ZSettings", factory)
	_ = r.Register("A", "ASettings", factory)
	_ = r.Register("M", "MSettings", factory)
	names := r.Names()
	if len(names) != 3 || names[0].Implementation() != "A" || names[2].Implementation() != "Z" {
		t.Errorf("unsorted: %v", names)
	}
}
