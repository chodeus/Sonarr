package v3

import "testing"

// TestProviderImplNameMaps_DivergentNames pins the schema-sourced
// ImplementationName lookups for impls whose display name differs from
// the Implementation key (G4 follow-up, J443).
func TestProviderImplNameMaps_DivergentNames(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		m          map[string]string
		impl, want string
	}{
		{"dlclient nzbget", downloadClientImplNames, "Nzbget", "NZBGet"},
		{"dlclient qbittorrent", downloadClientImplNames, "QBittorrent", "qBittorrent"},
		{"dlclient usenet-ds", downloadClientImplNames, "UsenetDownloadStation", "Download Station"},
		{"metadata xbmc", metadataImplNames, "XbmcMetadata", "Kodi (XBMC) / Emby"},
		{"indexer btn", indexerImplNames, "BroadcastheNet", "BroadcasTheNet"},
	}
	for _, c := range cases {
		if got := implementationNameOr(c.m, c.impl); got != c.want {
			t.Errorf("%s: implementationNameOr(%q) = %q, want %q", c.name, c.impl, got, c.want)
		}
	}
}

// TestImplementationNameOr_FallsBackToImpl: unknown impls return the raw
// Implementation (preserves pre-J443 behaviour for matching names + new
// impls not in the schema table).
func TestImplementationNameOr_FallsBackToImpl(t *testing.T) {
	t.Parallel()
	if got := implementationNameOr(downloadClientImplNames, "NotARealImpl"); got != "NotARealImpl" {
		t.Errorf("fallback = %q, want NotARealImpl", got)
	}
	if got := implementationNameOr(nil, "X"); got != "X" {
		t.Errorf("nil map fallback = %q, want X", got)
	}
}

// TestProviderImplNameMaps_NonEmpty guards against an init ordering /
// empty-schema regression (all 5 maps should be populated).
func TestProviderImplNameMaps_NonEmpty(t *testing.T) {
	t.Parallel()
	for name, m := range map[string]map[string]string{
		"notification": notificationImplNames, "indexer": indexerImplNames,
		"downloadClient": downloadClientImplNames, "importList": importListImplNames,
		"metadata": metadataImplNames,
	} {
		if len(m) == 0 {
			t.Errorf("%s impl-name map is empty", name)
		}
	}
}
