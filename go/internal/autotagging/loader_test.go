package autotagging_test

import (
	"context"
	"path/filepath"
	"testing"

	"sonarr.tv/sonarr/internal/autotagging"
	"sonarr.tv/sonarr/internal/datastore"
)

func newTestRepo(t *testing.T) *autotagging.Repo {
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
	r, err := autotagging.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	return r
}

func TestLoadFromRow_NilRow(t *testing.T) {
	got, err := autotagging.LoadFromRow(nil)
	if err != nil {
		t.Errorf("nil row should not error, got %v", err)
	}
	if got != nil {
		t.Errorf("nil row should produce nil tag, got %+v", got)
	}
}

func TestLoadFromRow_DecodesSpecs(t *testing.T) {
	row := &autotagging.AutoTag{
		ID:                      3,
		Name:                    "anime-tagger",
		RemoveTagsAutomatically: true,
		Tags:                    []int{10, 20},
		Specifications:          `[{"type":"SeriesTypeSpecification","body":{"name":"anime","value":2}}]`,
	}
	got, err := autotagging.LoadFromRow(row)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 3 || got.Name != "anime-tagger" || !got.RemoveTagsAutomatically {
		t.Errorf("scalar mismatch: %+v", got)
	}
	if len(got.Tags) != 2 || got.Tags[0] != 10 {
		t.Errorf("tags mismatch: %+v", got.Tags)
	}
	if len(got.Specifications) != 1 {
		t.Fatalf("expected 1 spec, got %d", len(got.Specifications))
	}
	if st, ok := got.Specifications[0].(*autotagging.SeriesTypeSpec); !ok || st.Value != 2 {
		t.Errorf("expected SeriesTypeSpec(value=2), got %+v", got.Specifications[0])
	}
}

func TestLoadFromRow_BadJSONErrors(t *testing.T) {
	row := &autotagging.AutoTag{Specifications: `not json`}
	_, err := autotagging.LoadFromRow(row)
	if err == nil {
		t.Error("expected error for malformed JSON")
	}
}

func TestLoadAll_RoundTrip(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	row := &autotagging.AutoTag{
		Name:           "test-autotag",
		Tags:           []int{1},
		Specifications: `[{"type":"MonitoredSpecification","body":{"name":"m"}}]`,
	}
	if _, err := repo.Insert(ctx, row); err != nil {
		t.Fatal(err)
	}

	loaded, err := autotagging.LoadAll(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 tag, got %d", len(loaded))
	}
	if _, ok := loaded[0].Specifications[0].(*autotagging.MonitoredSpec); !ok {
		t.Errorf("expected MonitoredSpec, got %T", loaded[0].Specifications[0])
	}
}
