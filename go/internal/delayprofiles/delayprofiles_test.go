package delayprofiles_test

import (
	"errors"
	"reflect"
	"testing"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/delayprofiles"
)

// runRepoSuite exercises CRUD across both engines, including the three
// new patterns this is the first repo to use: bool round-trip, embedded-
// doc Tags []int, and the reserved-keyword "Order" column.
func runRepoSuite(t *testing.T, db *datastore.DB) {
	t.Helper()
	repo, err := delayprofiles.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}

	// Migration 070 already inserted a default DelayProfile with
	// Order = INT_MAX. All() should see exactly that row at start.
	all, err := repo.All(t.Context())
	if err != nil {
		t.Fatalf("All initial: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("All initial: got %d rows, want 1 (the default from v70)", len(all))
	}
	if all[0].Order != 2147483647 {
		t.Errorf("default profile Order = %d, want INT_MAX", all[0].Order)
	}
	if !all[0].EnableUsenet || !all[0].EnableTorrent {
		t.Errorf("default profile bools = %+v, want both true", all[0])
	}
	if all[0].Tags == nil || len(all[0].Tags) != 0 {
		t.Errorf("default profile Tags = %v, want empty slice", all[0].Tags)
	}

	// Insert a new profile with all fields set, including Tags []int and
	// MinimumCustomFormatScore (*int).
	score := 50
	p := &delayprofiles.Profile{
		EnableUsenet:                   true,
		EnableTorrent:                  false,
		PreferredProtocol:              0, // Usenet
		UsenetDelay:                    30,
		TorrentDelay:                   120,
		Order:                          1,
		Tags:                           []int{42, 7, 9001},
		BypassIfHighestQuality:         true,
		BypassIfAboveCustomFormatScore: false,
		MinimumCustomFormatScore:       &score,
	}
	if _, err := repo.Insert(t.Context(), p); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if p.ID == 0 {
		t.Errorf("Insert: ID not populated")
	}

	// Round-trip via Get.
	got, err := repo.Get(t.Context(), p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.EnableUsenet != true || got.EnableTorrent != false {
		t.Errorf("bool round-trip: %+v", got)
	}
	if !reflect.DeepEqual(got.Tags, []int{42, 7, 9001}) {
		t.Errorf("Tags round-trip: got %v, want [42 7 9001]", got.Tags)
	}
	if got.MinimumCustomFormatScore == nil || *got.MinimumCustomFormatScore != 50 {
		t.Errorf("MinimumCustomFormatScore round-trip: %v", got.MinimumCustomFormatScore)
	}
	if got.Order != 1 || got.UsenetDelay != 30 || got.TorrentDelay != 120 {
		t.Errorf("int columns round-trip: %+v", got)
	}

	// Round-trip with nil MinimumCustomFormatScore (NULL in DB).
	p2 := &delayprofiles.Profile{
		EnableUsenet:      false,
		EnableTorrent:     true,
		PreferredProtocol: 1,
		UsenetDelay:       0,
		TorrentDelay:      0,
		Order:             2,
		Tags:              []int{}, // empty slice, distinct from nil
	}
	if _, err := repo.Insert(t.Context(), p2); err != nil {
		t.Fatalf("Insert p2: %v", err)
	}
	got2, err := repo.Get(t.Context(), p2.ID)
	if err != nil {
		t.Fatalf("Get p2: %v", err)
	}
	if got2.MinimumCustomFormatScore != nil {
		t.Errorf("nil MinimumCustomFormatScore round-trip: got %v, want nil", got2.MinimumCustomFormatScore)
	}
	if got2.Tags == nil {
		t.Errorf("empty Tags round-trip: got nil, want []")
	} else if len(got2.Tags) != 0 {
		t.Errorf("empty Tags round-trip: got %v, want []", got2.Tags)
	}

	// All ordered by Order ascending — see them in seeded-1, p, p2 order.
	all, err = repo.All(t.Context())
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("All: got %d rows, want 3", len(all))
	}
	if all[0].ID != p.ID || all[1].ID != p2.ID {
		t.Errorf("All Order: got [%d, %d, %d], want [%d, %d, default-int-max]",
			all[0].ID, all[1].ID, all[2].ID, p.ID, p2.ID)
	}

	// Update changes everything.
	got.EnableTorrent = true
	got.Tags = []int{1}
	got.MinimumCustomFormatScore = nil
	if err := repo.Update(t.Context(), got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	post, err := repo.Get(t.Context(), got.ID)
	if err != nil {
		t.Fatalf("Get post-Update: %v", err)
	}
	if !post.EnableTorrent || len(post.Tags) != 1 || post.Tags[0] != 1 || post.MinimumCustomFormatScore != nil {
		t.Errorf("post-Update: %+v", post)
	}

	// Update on missing ID returns ErrNotFound.
	if err := repo.Update(t.Context(), &delayprofiles.Profile{ID: 99999}); !errors.Is(err, delayprofiles.ErrNotFound) {
		t.Errorf("Update(missing): %v", err)
	}

	// Delete.
	if err := repo.Delete(t.Context(), p.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := repo.Delete(t.Context(), p.ID); !errors.Is(err, delayprofiles.ErrNotFound) {
		t.Errorf("Delete idempotent: %v", err)
	}

	// Get on missing returns ErrNotFound.
	if _, err := repo.Get(t.Context(), 99999); !errors.Is(err, delayprofiles.ErrNotFound) {
		t.Errorf("Get(missing): %v", err)
	}
}

func TestRepo_SQLite(t *testing.T) {
	t.Parallel()
	runRepoSuite(t, datastoretest.OpenSQLite(t))
}

func TestRepo_Postgres(t *testing.T) {
	runRepoSuite(t, datastoretest.OpenPostgres(t))
}
