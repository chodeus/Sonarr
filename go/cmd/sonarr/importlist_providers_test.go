package main

import (
	"testing"

	"sonarr.tv/sonarr/internal/importlists"
)

// J424 (G7): the blank imports in main.go must register the import-list
// providers into importlists.Default() — otherwise ImportListSync runs
// with an empty registry and adds nothing.
func TestImportListProvidersRegistered_J424(t *testing.T) {
	t.Parallel()
	n := len(importlists.Default().Names())
	if n < 8 {
		t.Errorf("importlists.Default() has %d registered providers, want >= 8 (anilist/custom/myanimelist/plex/plexrss/simkl/sonarr/trakt)", n)
	}
}
