// Package provider holds the Sonarr "ThingiProvider" identifier registry —
// the (Implementation, ConfigContract) string pairs that the polymorphic
// provider tables (Indexers, DownloadClients, Notifications, ImportLists,
// Metadata) round-trip through their Implementation/ConfigContract TEXT
// columns, plus the polymorphic-spec type strings the CustomFormats and
// AutoTagging tables embed inside their Specifications JSON envelopes.
//
// # TP-Q1/Q2 resolution: opaque string registry
//
// Existing user databases written by Sonarr v3/v4/v5 store
// `Implementation = "Newznab"` and `ConfigContract = "NewznabSettings"`,
// matching the C# class names verbatim. The C# code resolves these on
// load via `Type.Name` reflection (`ProviderRepository.cs:55`,
// `CustomFormatSpecificationConverter.cs`); a class rename silently
// triggers `ProviderFactory.RemoveMissingImplementations`, deleting
// every row with the now-unknown `Implementation`. Six historical C#
// renames have caused exactly this — migrations 046, 049, 082, 090, 104,
// 219 — and each had to be paired with a row-rewriting migration to keep
// data alive.
//
// The Go port uses a side table keyed by the C# strings rather than
// derive identifiers from Go type names. Each provider's constructor (in
// Phase 4 once the actual provider implementations land) registers
// itself with `Register(Key{Impl, Contract}, factory)`. The on-disk
// strings stay frozen at the C# class names; Go-side renames only
// update the constructor map.
//
// # Why a Key pair, not a single string
//
// Two known cases share `ConfigContract` across distinct
// `Implementation`s:
//
//   - `TransmissionSettings` is shared by `Vuze` and `Transmission`
//     (`src/NzbDrone.Core/Download/Clients/Transmission/TransmissionSettings.cs`).
//   - `DownloadStationSettings` is shared by `UsenetDownloadStation` and
//     `TorrentDownloadStation`
//     (`src/NzbDrone.Core/Download/Clients/DownloadStation/DownloadStationSettings.cs`).
//
// A single-string key (just ConfigContract) loses information for these
// rows — the registry can't pick the right implementation. So the
// registry keys on `(impl, contract)` pairs uniformly, even though the
// vast majority of pairs are 1:1.
//
// # What this package does NOT do
//
// This package is a registry of identifier strings only. It does not
// know how to deserialise Settings JSON, build provider instances, run
// `Test()` actions, or render schema. Those concerns live in
// downstream Phase 4 packages once the provider implementations
// themselves land. The Phase 1 storage layer (Indexers/DownloadClients/
// Notifications/ImportLists/Metadata repos) treats Settings as a raw
// `*string` passthrough so byte-identical round-trip is preserved while
// the registry catalog grows incrementally.
package provider

import (
	"fmt"
	"sort"
	"sync"
)

// Family names a polymorphic-row family. Each family has its own
// registry namespace so registrations from one family can't shadow
// another (e.g. a CustomFormat spec named "ReleaseTitleSpecification"
// must not collide with a hypothetical Indexer named the same).
type Family string

// Predefined Family values, one per row family Sonarr ships at HEAD.
// Tests / external packages should reference these constants rather
// than constructing Family literals so a typo surfaces at compile
// time.
const (
	FamilyIndexer          Family = "indexer"
	FamilyDownloadClient   Family = "downloadclient"
	FamilyNotification     Family = "notification"
	FamilyImportList       Family = "importlist"
	FamilyMetadata         Family = "metadata"
	FamilyCustomFormatSpec Family = "customformatspec"
	FamilyAutoTaggingSpec  Family = "autotaggingspec"
)

// Key identifies one registered provider class within a Family. For the
// five row-shaped provider families (Indexer/DownloadClient/Notification/
// ImportList/Metadata), Impl is the row's `Implementation` column and
// Contract is the row's `ConfigContract` column. For the two
// envelope-shaped spec families (CustomFormatSpec/AutoTaggingSpec),
// Impl is the JSON `type` discriminator inside one envelope element and
// Contract is left empty.
type Key struct {
	Impl     string
	Contract string
}

// Constructor returns a freshly-zero-valued Settings/Specification body
// for one registered provider. Phase 4 wires up real constructors once
// the typed Settings structs land; for now most registrations pass nil
// (the catalog's purpose at Phase 1 is identifier validation, not
// instantiation).
type Constructor func() any

// Registry is a Family-namespaced map from Key to Constructor.
// Concurrency-safe — Register, Has, Lookup, and Keys are all goroutine-
// safe under a single RWMutex. The expected lifecycle is: package init()
// blocks call Register once at process start; service code calls Has
// and Lookup at request time.
type Registry struct {
	mu      sync.RWMutex
	entries map[Family]map[Key]Constructor
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{entries: map[Family]map[Key]Constructor{}}
}

// ErrAlreadyRegistered is returned by Register if (family, key) is
// already present. Re-registering the same key with the same factory is
// not allowed because it usually means an init() loop ran twice (e.g.
// package import cycle being broken by a build tag) — the safer thing
// is to surface the duplicate so the caller can fix the import graph.
var ErrAlreadyRegistered = fmt.Errorf("provider: already registered")

// Register binds factory to (family, key). factory may be nil during
// Phase 1 to register an identifier without an instantiation path; Has
// and Lookup distinguish "registered with nil factory" from "not
// registered at all".
func (r *Registry) Register(family Family, key Key, factory Constructor) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	bucket, ok := r.entries[family]
	if !ok {
		bucket = map[Key]Constructor{}
		r.entries[family] = bucket
	}
	if _, dup := bucket[key]; dup {
		return fmt.Errorf("%w: family=%q impl=%q contract=%q", ErrAlreadyRegistered, family, key.Impl, key.Contract)
	}
	bucket[key] = factory
	return nil
}

// MustRegister is the panic-on-error variant for use inside init()
// blocks where a registration failure indicates a programming error.
func (r *Registry) MustRegister(family Family, key Key, factory Constructor) {
	if err := r.Register(family, key, factory); err != nil {
		panic(err)
	}
}

// Replace overwrites the factory for (family, key). Used by Phase 4
// family-port slices to swap the nil placeholder constructor seeded by
// Default() with the real instantiator. Returns nil on success, or
// errors.New("not registered") when the key was never registered (a
// genuine bug — the family-port slice should match an existing catalog
// entry, not invent new ones).
//
// Concurrency-safe; takes the write lock.
func (r *Registry) Replace(family Family, key Key, factory Constructor) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	bucket, ok := r.entries[family]
	if !ok {
		return fmt.Errorf("provider: family %q not registered", family)
	}
	if _, exists := bucket[key]; !exists {
		return fmt.Errorf("provider: %q/%q not registered (use Register for new keys)", key.Impl, key.Contract)
	}
	bucket[key] = factory
	return nil
}

// Has reports whether (family, key) is registered. Returns true even if
// the registered factory is nil — "known identifier" is distinct from
// "instantiable identifier".
func (r *Registry) Has(family Family, key Key) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	bucket, ok := r.entries[family]
	if !ok {
		return false
	}
	_, ok = bucket[key]
	return ok
}

// Lookup returns the factory for (family, key) and whether it is
// registered. A nil factory with ok=true means the identifier is known
// but no instantiation path is wired yet (Phase 1 catalog shape).
func (r *Registry) Lookup(family Family, key Key) (Constructor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	bucket, ok := r.entries[family]
	if !ok {
		return nil, false
	}
	f, ok := bucket[key]
	return f, ok
}

// Keys returns every registered Key in family, sorted by (Impl, Contract).
// Useful for diagnostics and for tests that assert the catalog matches a
// known set.
func (r *Registry) Keys(family Family) []Key {
	r.mu.RLock()
	defer r.mu.RUnlock()
	bucket, ok := r.entries[family]
	if !ok {
		return nil
	}
	out := make([]Key, 0, len(bucket))
	for k := range bucket {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Impl != out[j].Impl {
			return out[i].Impl < out[j].Impl
		}
		return out[i].Contract < out[j].Contract
	})
	return out
}
