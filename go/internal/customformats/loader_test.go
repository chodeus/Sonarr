package customformats_test

import (
	"context"
	"path/filepath"
	"testing"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/datastore"
)

func newTestRepo(t *testing.T) *customformats.Repo {
	t.Helper()
	dir := t.TempDir()
	db, err := datastore.Open(context.Background(), datastore.Config{
		Engine: datastore.EngineSQLite,
		Path:   filepath.Join(dir, "test.db"),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := datastore.Migrate(context.Background(), db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	r, err := customformats.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	return r
}

func TestLoadFromRow_NilRow(t *testing.T) {
	got, err := customformats.LoadFromRow(nil)
	if err != nil {
		t.Errorf("nil row should not error, got %v", err)
	}
	if got != nil {
		t.Errorf("nil row should produce nil format, got %+v", got)
	}
}

func TestLoadFromRow_DecodesSpecs(t *testing.T) {
	row := &customformats.CustomFormat{
		ID:                              7,
		Name:                            "HDTV-Tag",
		IncludeCustomFormatWhenRenaming: true,
		Specifications:                  `[{"type":"ReleaseTitleSpecification","body":{"name":"hdtv","value":"HDTV"}}]`,
	}
	got, err := customformats.LoadFromRow(row)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 7 || got.Name != "HDTV-Tag" || !got.IncludeCustomFormatWhenRenaming {
		t.Errorf("scalar field mismatch: %+v", got)
	}
	if len(got.Specifications) != 1 {
		t.Fatalf("expected 1 spec, got %d", len(got.Specifications))
	}
	if _, ok := got.Specifications[0].(*customformats.ReleaseTitleSpec); !ok {
		t.Errorf("expected ReleaseTitleSpec, got %T", got.Specifications[0])
	}
}

func TestLoadFromRow_BadJSONErrors(t *testing.T) {
	row := &customformats.CustomFormat{Specifications: `{not an array`}
	_, err := customformats.LoadFromRow(row)
	if err == nil {
		t.Error("expected error for malformed JSON")
	}
}

func TestLoadAll_RoundTrip(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	row := &customformats.CustomFormat{
		Name:           "Test-Format",
		Specifications: `[{"type":"ResolutionSpecification","body":{"name":"1080p","value":1080}}]`,
	}
	if _, err := repo.Insert(ctx, row); err != nil {
		t.Fatal(err)
	}

	loaded, err := customformats.LoadAll(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 format, got %d", len(loaded))
	}
	rs, ok := loaded[0].Specifications[0].(*customformats.ResolutionSpec)
	if !ok || rs.Value != 1080 {
		t.Errorf("loaded spec mismatch: %+v", loaded[0].Specifications[0])
	}
}
