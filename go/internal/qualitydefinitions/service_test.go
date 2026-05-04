package qualitydefinitions_test

import (
	"errors"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/qualitydefinitions"
)

func newService(t *testing.T) (*qualitydefinitions.Service, *qualitydefinitions.Repo) {
	t.Helper()
	db := datastoretest.OpenSQLite(t)
	r, err := qualitydefinitions.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	return qualitydefinitions.NewServiceWithTTL(r, 0), r
}

func TestService_EnsureDefaults_SeedsAllQualities(t *testing.T) {
	t.Parallel()
	svc, repo := newService(t)

	// Empty initially.
	rows, err := repo.All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("pre-state: got %d rows, want 0", len(rows))
	}

	if err := svc.EnsureDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}

	rows, err = repo.All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(quality.DefaultDefinitions()) {
		t.Errorf("post-Seed: got %d, want %d", len(rows), len(quality.DefaultDefinitions()))
	}

	// Idempotent: a second call doesn't duplicate rows.
	if err := svc.EnsureDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows2, err := repo.All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows2) != len(rows) {
		t.Errorf("EnsureDefaults not idempotent: %d → %d", len(rows), len(rows2))
	}
}

func TestService_EnsureDefaults_DeletesOrphans(t *testing.T) {
	t.Parallel()
	svc, repo := newService(t)

	// Insert a row with a Quality that's not in the default table
	// (mimics a retired-quality leftover).
	orphan := &qualitydefinitions.Definition{Quality: 999, Title: "Retired"}
	if _, err := repo.Insert(t.Context(), orphan); err != nil {
		t.Fatal(err)
	}

	if err := svc.EnsureDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}

	if _, err := repo.Get(t.Context(), orphan.ID); !errors.Is(err, qualitydefinitions.ErrNotFound) {
		t.Errorf("orphan should be deleted, Get = %v", err)
	}
}

func TestService_All_OrderedByWeight(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	if err := svc.EnsureDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}

	all, err := svc.All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(all); i++ {
		if all[i].Weight < all[i-1].Weight {
			t.Errorf("All not sorted by Weight: %d (%s) < %d (%s) at index %d",
				all[i].Weight, all[i].Quality.Name, all[i-1].Weight, all[i-1].Quality.Name, i)
			break
		}
	}
}

func TestService_All_DecoratesWithWeightAndGroupName(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	if err := svc.EnsureDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}

	all, err := svc.All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var seenWebDL1080p bool
	for _, d := range all {
		if d.Quality.ID == quality.WEBDL1080p.ID {
			seenWebDL1080p = true
			if d.Weight != 12 {
				t.Errorf("WEBDL1080p Weight = %d, want 12", d.Weight)
			}
			if d.GroupName != "WEB 1080p" {
				t.Errorf("WEBDL1080p GroupName = %q, want \"WEB 1080p\"", d.GroupName)
			}
		}
	}
	if !seenWebDL1080p {
		t.Error("WEBDL1080p not in All()")
	}
}

func TestService_GetByID_AndGet(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	if err := svc.EnsureDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}

	all, err := svc.All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	first := all[0]

	got, err := svc.GetByID(t.Context(), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Quality.ID != first.Quality.ID {
		t.Errorf("GetByID: got quality.ID=%d, want %d", got.Quality.ID, first.Quality.ID)
	}

	got2, err := svc.Get(t.Context(), first.Quality)
	if err != nil {
		t.Fatal(err)
	}
	if got2.ID != first.ID {
		t.Errorf("Get(quality): got ID=%d, want %d", got2.ID, first.ID)
	}

	if _, err := svc.GetByID(t.Context(), 99999); !errors.Is(err, qualitydefinitions.ErrNotFound) {
		t.Errorf("GetByID(missing) = %v, want ErrNotFound", err)
	}
	if _, err := svc.Get(t.Context(), quality.Quality{ID: 999}); !errors.Is(err, qualitydefinitions.ErrNotFound) {
		t.Errorf("Get(unknown quality) = %v, want ErrNotFound", err)
	}
}

func TestService_Update_PersistsTitleAndInvalidates(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	if err := svc.EnsureDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}

	got, err := svc.Get(t.Context(), quality.HDTV1080p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "HDTV-1080p" {
		t.Errorf("default Title = %q, want \"HDTV-1080p\"", got.Title)
	}

	row := &qualitydefinitions.Definition{
		ID: got.ID, Quality: got.Quality.ID, Title: "MyHD",
	}
	if err := svc.Update(t.Context(), row); err != nil {
		t.Fatal(err)
	}

	got, err = svc.Get(t.Context(), quality.HDTV1080p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "MyHD" {
		t.Errorf("post-Update Title = %q, want \"MyHD\"", got.Title)
	}
}

func TestService_Update_NilErrors(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	if err := svc.Update(t.Context(), nil); err == nil {
		t.Error("Update(nil) should error")
	}
}

func TestService_ResetTitles_RestoresDefaults(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	if err := svc.EnsureDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}

	got, err := svc.Get(t.Context(), quality.HDTV720p)
	if err != nil {
		t.Fatal(err)
	}
	row := &qualitydefinitions.Definition{ID: got.ID, Quality: got.Quality.ID, Title: "Custom HD"}
	if err := svc.Update(t.Context(), row); err != nil {
		t.Fatal(err)
	}

	if err := svc.ResetTitles(t.Context(), true); err != nil {
		t.Fatal(err)
	}

	got, err = svc.Get(t.Context(), quality.HDTV720p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "HDTV-720p" {
		t.Errorf("ResetTitles(true): Title = %q, want \"HDTV-720p\"", got.Title)
	}
}

func TestService_ResetTitles_FalseIsNoOp(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	if err := svc.EnsureDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}

	got, _ := svc.Get(t.Context(), quality.HDTV720p)
	row := &qualitydefinitions.Definition{ID: got.ID, Quality: got.Quality.ID, Title: "Keep Me"}
	if err := svc.Update(t.Context(), row); err != nil {
		t.Fatal(err)
	}

	if err := svc.ResetTitles(t.Context(), false); err != nil {
		t.Fatal(err)
	}

	got, _ = svc.Get(t.Context(), quality.HDTV720p)
	if got.Title != "Keep Me" {
		t.Errorf("ResetTitles(false): Title = %q, want \"Keep Me\" (false should be no-op)", got.Title)
	}
}

func TestService_All_Caches(t *testing.T) {
	t.Parallel()
	// Use a non-zero TTL to verify caching works.
	db := datastoretest.OpenSQLite(t)
	repo, err := qualitydefinitions.NewRepo(db)
	if err != nil {
		t.Fatal(err)
	}
	svc := qualitydefinitions.NewServiceWithTTL(repo, 5*time.Second)

	if err := svc.EnsureDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}

	first, err := svc.All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	originalCount := len(first)

	// Direct-Repo insert bypasses cache invalidation.
	if _, err := repo.Insert(t.Context(), &qualitydefinitions.Definition{
		Quality: 9999, Title: "Bypass",
	}); err != nil {
		t.Fatal(err)
	}

	cached, err := svc.All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(cached) != originalCount {
		t.Errorf("cache should still show %d (direct insert bypassed invalidation), got %d",
			originalCount, len(cached))
	}
}
