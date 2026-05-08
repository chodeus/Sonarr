package namingconfig_test

import (
	"errors"
	"sync"
	"testing"

	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/namingconfig"
)

func newServiceSQLite(t *testing.T) (*namingconfig.Service, *namingconfig.Repo) {
	t.Helper()
	db := datastoretest.OpenSQLite(t)
	r, err := namingconfig.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	return namingconfig.NewService(r, nil), r
}

func TestService_GetConfig_SeedsDefault(t *testing.T) {
	t.Parallel()
	svc, repo := newServiceSQLite(t)

	// Pre-state: empty table.
	all, err := repo.All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("pre-state: got %d rows, want 0", len(all))
	}

	cfg, err := svc.GetConfig(t.Context())
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if cfg.ID == 0 {
		t.Errorf("GetConfig: seeded row has ID=0")
	}
	// Sanity-check Default-shaped fields.
	if cfg.MultiEpisodeStyle != 5 || cfg.ColonReplacementFormat != 4 {
		t.Errorf("seeded values don't match Default(): %+v", cfg)
	}
	if cfg.StandardEpisodeFormat == "" {
		t.Errorf("StandardEpisodeFormat empty after seed")
	}

	// Post-state: exactly one row.
	all, err = repo.All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Errorf("post-state: got %d rows, want 1", len(all))
	}
}

func TestService_GetConfig_Idempotent(t *testing.T) {
	t.Parallel()
	svc, repo := newServiceSQLite(t)

	first, err := svc.GetConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.GetConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Errorf("GetConfig not idempotent: ids %d vs %d", first.ID, second.ID)
	}

	all, err := repo.All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Errorf("after two GetConfig calls: got %d rows, want 1 (no duplicate seeding)", len(all))
	}
}

func TestService_GetConfig_ConcurrentFirstCall(t *testing.T) {
	t.Parallel()
	svc, repo := newServiceSQLite(t)

	const N = 8
	var wg sync.WaitGroup
	ids := make([]int, N)
	errs := make([]error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cfg, err := svc.GetConfig(t.Context())
			if err != nil {
				errs[i] = err
				return
			}
			ids[i] = cfg.ID
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: %v", i, err)
		}
	}
	// Exactly one row should exist; all callers should see the same ID.
	all, err := repo.All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("after %d concurrent GetConfig calls: got %d rows, want 1", N, len(all))
	}
	for i := 1; i < N; i++ {
		if ids[i] != ids[0] {
			t.Errorf("goroutine %d saw id=%d, goroutine 0 saw id=%d", i, ids[i], ids[0])
		}
	}
}

func TestService_Save_InsertWhenIDZero(t *testing.T) {
	t.Parallel()
	svc, repo := newServiceSQLite(t)

	cfg := namingconfig.Default()
	cfg.RenameEpisodes = true
	cfg.SeriesFolderFormat = "{Series Title} ({tvdbid})"

	if err := svc.Save(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.ID == 0 {
		t.Errorf("Save: ID not populated after insert path")
	}

	got, err := repo.Get(t.Context(), cfg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.RenameEpisodes || got.SeriesFolderFormat != "{Series Title} ({tvdbid})" {
		t.Errorf("Save round-trip: %+v", got)
	}
}

func TestService_Save_UpdateWhenIDSet(t *testing.T) {
	t.Parallel()
	svc, repo := newServiceSQLite(t)

	cfg, err := svc.GetConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	cfg.RenameEpisodes = true
	cfg.SeriesFolderFormat = "Custom"
	if err := svc.Save(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}

	got, err := repo.Get(t.Context(), cfg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.RenameEpisodes || got.SeriesFolderFormat != "Custom" {
		t.Errorf("Save update path: %+v", got)
	}

	all, err := repo.All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Errorf("Save with ID set should NOT insert a new row: got %d rows", len(all))
	}
}

func TestService_Save_NilErrors(t *testing.T) {
	t.Parallel()
	svc, _ := newServiceSQLite(t)
	if err := svc.Save(t.Context(), nil); err == nil {
		t.Error("Save(nil) should error")
	}
}

func TestService_Save_UpdateMissingIDPropagatesNotFound(t *testing.T) {
	t.Parallel()
	svc, _ := newServiceSQLite(t)

	cfg := namingconfig.Default()
	cfg.ID = 9999
	if err := svc.Save(t.Context(), cfg); !errors.Is(err, namingconfig.ErrNotFound) {
		t.Errorf("Save(non-existent ID) = %v, want ErrNotFound", err)
	}
}
