package config_test

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/config"
	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/messaging"
)

// providerFixture builds a config.Provider over a fresh DB + bus,
// with Load already called so getters return cached values without
// further wiring. Returns the provider, the underlying repo (handy
// for out-of-band writes that simulate external mutators), and the
// bus.
func providerFixture(t *testing.T, db *datastore.DB) (*config.Provider, *config.Repo, *messaging.Bus) {
	t.Helper()
	repo, err := config.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	bus := messaging.New(slog.Default())
	prov, err := config.NewProvider(repo, bus)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if err := prov.Load(t.Context()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return prov, repo, bus
}

func TestProvider_DefaultsWhenMissing(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	prov, _, _ := providerFixture(t, db)

	if got := prov.CreateEmptySeriesFolders(); got != false {
		t.Errorf("CreateEmptySeriesFolders default = %v, want false", got)
	}
	if got := prov.DeleteEmptyFolders(); got != false {
		t.Errorf("DeleteEmptyFolders default = %v, want false", got)
	}
	if got := prov.SetPermissionsLinux(); got != false {
		t.Errorf("SetPermissionsLinux default = %v, want false", got)
	}
	if got := prov.ChmodFolder(); got != "755" {
		t.Errorf("ChmodFolder default = %q, want 755", got)
	}
	if got := prov.ChownGroup(); got != "" {
		t.Errorf("ChownGroup default = %q, want \"\"", got)
	}
}

func TestProvider_SetThenGet(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	prov, _, _ := providerFixture(t, db)

	ctx := t.Context()
	if err := prov.SetCreateEmptySeriesFolders(ctx, true); err != nil {
		t.Fatalf("SetCreateEmptySeriesFolders: %v", err)
	}
	if err := prov.SetDeleteEmptyFolders(ctx, true); err != nil {
		t.Fatalf("SetDeleteEmptyFolders: %v", err)
	}
	if err := prov.SetSetPermissionsLinux(ctx, true); err != nil {
		t.Fatalf("SetSetPermissionsLinux: %v", err)
	}
	if err := prov.SetChmodFolder(ctx, "775"); err != nil {
		t.Fatalf("SetChmodFolder: %v", err)
	}
	if err := prov.SetChownGroup(ctx, "media"); err != nil {
		t.Fatalf("SetChownGroup: %v", err)
	}

	if got := prov.CreateEmptySeriesFolders(); !got {
		t.Errorf("CreateEmptySeriesFolders after Set = %v, want true", got)
	}
	if got := prov.DeleteEmptyFolders(); !got {
		t.Errorf("DeleteEmptyFolders after Set = %v, want true", got)
	}
	if got := prov.SetPermissionsLinux(); !got {
		t.Errorf("SetPermissionsLinux after Set = %v, want true", got)
	}
	if got := prov.ChmodFolder(); got != "775" {
		t.Errorf("ChmodFolder after Set = %q, want 775", got)
	}
	if got := prov.ChownGroup(); got != "media" {
		t.Errorf("ChownGroup after Set = %q, want media", got)
	}
}

func TestProvider_SetIsCaseInsensitive(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	prov, repo, _ := providerFixture(t, db)

	if err := prov.SetChmodFolder(t.Context(), "700"); err != nil {
		t.Fatalf("SetChmodFolder: %v", err)
	}
	// Stored under a lowercase key so subsequent reads from any
	// casing variation return the same row. Mirrors C#
	// `key.ToLowerInvariant()` at `ConfigService.cs:498`.
	got, err := repo.GetByKey(t.Context(), "chmodfolder")
	if err != nil {
		t.Fatalf("GetByKey(chmodfolder): %v", err)
	}
	if got.Value != "700" {
		t.Errorf("Repo row Value = %q, want 700", got.Value)
	}
}

func TestProvider_BoolParsing(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	prov, repo, _ := providerFixture(t, db)

	// Out-of-band Repo writes simulate a hand-edited DB with the
	// permissive "yes" / "no" / "1" / "0" forms found in older
	// Sonarr installs.
	for _, c := range []struct {
		raw  string
		want bool
	}{
		{"true", true},
		{"True", true},
		{"yes", true},
		{"YES", true},
		{"1", true},
		{"false", false},
		{"no", false},
		{"0", false},
		{"garbage", false}, // unparseable falls through to default (false here)
		{"", false},        // empty is the same as missing
	} {
		if _, err := repo.Set(t.Context(), "createemptyseriesfolders", c.raw); err != nil {
			t.Fatalf("Repo.Set(%q): %v", c.raw, err)
		}
		if err := prov.Load(t.Context()); err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got := prov.CreateEmptySeriesFolders(); got != c.want {
			t.Errorf("CreateEmptySeriesFolders(%q) = %v, want %v", c.raw, got, c.want)
		}
	}
}

func TestProvider_SetIdempotentSkipsPublish(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	prov, _, bus := providerFixture(t, db)

	var publishes int32
	messaging.Subscribe(bus, "test.count", func(_ context.Context, _ config.ConfigSavedEvent) error {
		atomic.AddInt32(&publishes, 1)
		return nil
	})

	ctx := t.Context()
	// First Set actually changes the row → publish fires.
	if err := prov.SetChmodFolder(ctx, "700"); err != nil {
		t.Fatalf("Set #1: %v", err)
	}
	// Second Set is a no-op (cache already says "700") → no publish.
	if err := prov.SetChmodFolder(ctx, "700"); err != nil {
		t.Fatalf("Set #2: %v", err)
	}
	if got := atomic.LoadInt32(&publishes); got != 1 {
		t.Errorf("publish count = %d, want 1 (idempotent set must not re-publish)", got)
	}
}

func TestProvider_SubscribeReloadsOnEvent(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	prov, repo, bus := providerFixture(t, db)
	prov.Subscribe(bus)

	// External writer mutates the row directly through the Repo.
	if _, err := repo.Set(t.Context(), "chmodfolder", "777"); err != nil {
		t.Fatalf("external Repo.Set: %v", err)
	}
	// The cache still says default ("755") until the event fires.
	if got := prov.ChmodFolder(); got != "755" {
		t.Errorf("pre-event ChmodFolder = %q, want stale default 755", got)
	}
	// Publishing the event must trigger the subscribed handler which
	// reloads the cache from disk; sync handlers run on the publishing
	// goroutine so the assertion is race-free.
	if err := messaging.Publish(t.Context(), bus, config.ConfigSavedEvent{}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if got := prov.ChmodFolder(); got != "777" {
		t.Errorf("post-event ChmodFolder = %q, want 777 (cache reloaded)", got)
	}
}

func TestProvider_NilBusOK(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	repo, err := config.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	prov, err := config.NewProvider(repo, nil)
	if err != nil {
		t.Fatalf("NewProvider(nil bus): %v", err)
	}
	if err := prov.Load(t.Context()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Set works without a bus — publish step is silently skipped.
	if err := prov.SetChmodFolder(t.Context(), "700"); err != nil {
		t.Fatalf("SetChmodFolder: %v", err)
	}
	if got := prov.ChmodFolder(); got != "700" {
		t.Errorf("ChmodFolder = %q, want 700", got)
	}
	// Subscribe is a no-op with nil bus — must not panic.
	prov.Subscribe(nil)
}

func TestProvider_RejectsNilRepo(t *testing.T) {
	t.Parallel()
	if _, err := config.NewProvider(nil, nil); err == nil {
		t.Error("NewProvider accepted nil repo; want error")
	}
}

// TestProvider_ConcurrentReads exercises the RWMutex path under -race.
// One goroutine spins on SetChmodFolder while many spin on the
// getters; no torn reads, no data races.
func TestProvider_ConcurrentReads(t *testing.T) {
	t.Parallel()
	db := datastoretest.OpenSQLite(t)
	prov, _, _ := providerFixture(t, db)

	const readers = 8
	const iters = 200
	var wg sync.WaitGroup
	wg.Add(readers + 1)

	stop := make(chan struct{})
	for i := 0; i < readers; i++ {
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = prov.ChmodFolder()
					_ = prov.ChownGroup()
					_ = prov.CreateEmptySeriesFolders()
					_ = prov.DeleteEmptyFolders()
					_ = prov.SetPermissionsLinux()
				}
			}
		}()
	}

	go func() {
		defer wg.Done()
		ctx := t.Context()
		for i := 0; i < iters; i++ {
			v := "755"
			if i%2 == 0 {
				v = "775"
			}
			if err := prov.SetChmodFolder(ctx, v); err != nil {
				t.Errorf("SetChmodFolder: %v", err)
				return
			}
		}
		close(stop)
	}()

	wg.Wait()
}

// TestProvider_PostgresSmoke is the postgres twin of the SQLite
// suite — runs the per-getter default + Set/Get round-trip against
// the shared Postgres test fixture so we catch any engine-specific
// upsert / case-folding skew.
func TestProvider_PostgresSmoke(t *testing.T) {
	db := datastoretest.OpenPostgres(t)
	prov, _, _ := providerFixture(t, db)

	if got := prov.ChmodFolder(); got != "755" {
		t.Errorf("ChmodFolder default (pg) = %q, want 755", got)
	}
	if err := prov.SetChmodFolder(t.Context(), "770"); err != nil {
		t.Fatalf("SetChmodFolder: %v", err)
	}
	if got := prov.ChmodFolder(); got != "770" {
		t.Errorf("ChmodFolder after Set (pg) = %q, want 770", got)
	}
}
