package provider_test

import (
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/provider"
)

func TestRegistry_RegisterAndLookup(t *testing.T) {
	t.Parallel()
	r := provider.NewRegistry()
	k := provider.Key{Impl: "Newznab", Contract: "NewznabSettings"}
	if err := r.Register(provider.FamilyIndexer, k, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if !r.Has(provider.FamilyIndexer, k) {
		t.Fatalf("Has(%v) = false, want true", k)
	}
	if _, ok := r.Lookup(provider.FamilyIndexer, k); !ok {
		t.Fatalf("Lookup(%v) ok=false, want true", k)
	}
	other := provider.Key{Impl: "Torznab", Contract: "TorznabSettings"}
	if r.Has(provider.FamilyIndexer, other) {
		t.Fatalf("Has(%v) = true, want false", other)
	}
}

func TestRegistry_DuplicateRegisterRejected(t *testing.T) {
	t.Parallel()
	r := provider.NewRegistry()
	k := provider.Key{Impl: "Sabnzbd", Contract: "SabnzbdSettings"}
	if err := r.Register(provider.FamilyDownloadClient, k, nil); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := r.Register(provider.FamilyDownloadClient, k, nil); !errors.Is(err, provider.ErrAlreadyRegistered) {
		t.Fatalf("second Register: %v, want ErrAlreadyRegistered", err)
	}
}

func TestRegistry_FamilyNamespaceIsolated(t *testing.T) {
	t.Parallel()
	r := provider.NewRegistry()
	k := provider.Key{Impl: "Webhook", Contract: "WebhookSettings"}
	// Same key, different families — must not collide.
	if err := r.Register(provider.FamilyNotification, k, nil); err != nil {
		t.Fatalf("notification Register: %v", err)
	}
	if err := r.Register(provider.FamilyMetadata, k, nil); err != nil {
		t.Fatalf("metadata Register: %v", err)
	}
	if !r.Has(provider.FamilyNotification, k) || !r.Has(provider.FamilyMetadata, k) {
		t.Fatalf("family namespaces not isolated")
	}
}

func TestRegistry_LookupReturnsConstructor(t *testing.T) {
	t.Parallel()
	r := provider.NewRegistry()
	type fakeSettings struct{ X int }
	factory := func() any { return &fakeSettings{X: 42} }
	k := provider.Key{Impl: "Custom", Contract: "CustomSettings"}
	if err := r.Register(provider.FamilyImportList, k, factory); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, ok := r.Lookup(provider.FamilyImportList, k)
	if !ok || got == nil {
		t.Fatalf("Lookup ok=%v factory=%v", ok, got)
	}
	v, _ := got().(*fakeSettings)
	if v == nil || v.X != 42 {
		t.Fatalf("factory output: %#v", got())
	}
}

// TestDefault_HasExpectedCounts asserts the Phase-1 catalog matches the
// counts the deep-dive's analysis/providers/*.md docs locked in. If a
// future C# tree update adds or removes a provider class, this test
// fails loudly instead of silently shipping a stale catalog.
func TestDefault_HasExpectedCounts(t *testing.T) {
	t.Parallel()
	r := provider.Default()
	cases := []struct {
		family provider.Family
		want   int
	}{
		{provider.FamilyIndexer, 10},
		{provider.FamilyDownloadClient, 20},
		{provider.FamilyNotification, 25},
		{provider.FamilyImportList, 10},
		{provider.FamilyMetadata, 4},
		{provider.FamilyCustomFormatSpec, 8},
		{provider.FamilyAutoTaggingSpec, 11},
	}
	for _, c := range cases {
		if got := len(r.Keys(c.family)); got != c.want {
			t.Errorf("Default Keys(%q): got %d, want %d", c.family, got, c.want)
		}
	}
}

// TestDefault_SharedContractPairs verifies the two known
// contract-shared pairs registered as distinct entries: pair lookup
// disambiguates Vuze vs Transmission and the two DownloadStation
// halves. If pair-keying ever regressed to single-string keying, one of
// each pair would silently overwrite the other.
func TestDefault_SharedContractPairs(t *testing.T) {
	t.Parallel()
	r := provider.Default()
	pairs := []provider.Key{
		{Impl: "Transmission", Contract: "TransmissionSettings"},
		{Impl: "Vuze", Contract: "TransmissionSettings"},
		{Impl: "UsenetDownloadStation", Contract: "DownloadStationSettings"},
		{Impl: "TorrentDownloadStation", Contract: "DownloadStationSettings"},
	}
	for _, p := range pairs {
		if !r.Has(provider.FamilyDownloadClient, p) {
			t.Errorf("Default missing pair %v", p)
		}
	}
}
