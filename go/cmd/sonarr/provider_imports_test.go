package main

import (
	"testing"

	"sonarr.tv/sonarr/internal/importlists"
	"sonarr.tv/sonarr/internal/notifications"
)

// J184: verify that the blank-imports in provider_imports.go cause
// every concrete provider package's init() to fire — populating
// both global registries with all the expected Constructors before
// cmd/sonarr runs.
//
// The test counts entries via Registry.Names() (sorted). A regression
// that drops a blank-import line (e.g., during a future refactor)
// shows up here as a missing implementation name in the registry.
//
// Why count-based rather than per-name assertions: the per-name
// audit lives in the provider packages' own unit tests (each
// concrete provider's _test.go verifies its registration). The
// cmd/sonarr smoke test guards only the cross-cutting concern —
// that the blank-imports collectively activate the expected total
// count. Adding a new provider then requires both: (1) the
// provider package's MustRegister + own test (well-trodden path);
// (2) bumping this count + the blank-import in provider_imports.go.
const (
	// 8 concrete import-list provider PACKAGES are blank-imported in
	// provider_imports.go; one of them (trakt) registers 3 variants
	// (List / Popular / User) so the total Constructor count is 10.
	// Bumping this number requires also touching provider_imports.go.
	minImportlistsRegistryCount = 10

	// 25 concrete notification provider PACKAGES each call
	// MustRegister at least once. The total Constructor count may
	// exceed 25 if a package registers multiple variants (e.g.
	// Plex's MusicPlayer vs MediaServer variants). Asserted >= so
	// a future multi-variant addition doesn't immediately break
	// this test.
	minNotificationsRegistryCount = 25
)

func TestProviderImports_ImportListsRegistryPopulated(t *testing.T) {
	t.Parallel()
	got := len(importlists.Default().Names())
	if got < minImportlistsRegistryCount {
		t.Errorf("importlists.Default().Names() count = %d, want >= %d (a blank-import in provider_imports.go may be missing)", got, minImportlistsRegistryCount)
	}
}

func TestProviderImports_NotificationsRegistryPopulated(t *testing.T) {
	t.Parallel()
	got := len(notifications.Default().Names())
	if got < minNotificationsRegistryCount {
		t.Errorf("notifications.Default().Names() count = %d, want >= %d (a blank-import in provider_imports.go may be missing)", got, minNotificationsRegistryCount)
	}
}
