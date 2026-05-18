package indexers

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Registry is the per-family typed Constructor map for indexer
// implementations. Concurrent-access safe. Mirrors
// [notifications.Registry] (`internal/notifications/registry.go`)
// in shape — the two would collapse to a generic `provider.Registry[T]`
// once a third provider family demands it.
//
// Expected lifecycle:
//
//   - Each concrete indexer package's init() block calls
//     [Registry.Register] / [Registry.MustRegister] at process start.
//   - The v3 test/test-all/action handlers call [Registry.Construct]
//     at request-time to instantiate the matching Provider per the
//     persisted row's Implementation + ConfigContract columns.
//
// As of Phase 5.C2b: zero concrete impls register. The registry
// exists so the wire boundary is in place; the React UI's test
// button will return 400 "Unknown implementation" for every request
// until the first concrete indexer Provider ports. The Newznab/
// Torznab/TorrentRss directories under internal/indexers/ ship
// Capabilities-parser + protocol helpers today but no full Provider
// implementation.
type Registry struct {
	mu      sync.RWMutex
	entries map[registryKey]Constructor
}

// registryKey is the (Implementation, ConfigContract) pair —
// mirrors the C# IndexerFactory key shape. Most indexers map 1:1
// between Implementation and ConfigContract, but TorznabRequest +
// Newznab share IndexerCapabilitiesSettings so the pair key is
// defensively used.
type registryKey struct {
	implementation string
	configContract string
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{entries: map[registryKey]Constructor{}}
}

// ErrAlreadyRegistered is returned by [Registry.Register] when a key
// is already present.
var ErrAlreadyRegistered = errors.New("indexers: already registered")

// ErrNotRegistered is returned by [Registry.Lookup] / [Registry.Replace]
// when the (Implementation, ConfigContract) pair has no Constructor.
var ErrNotRegistered = errors.New("indexers: not registered")

// Register binds factory to (implementation, configContract).
func (r *Registry) Register(implementation, configContract string, factory Constructor) error {
	if implementation == "" {
		return errors.New("indexers: Register: empty implementation")
	}
	if configContract == "" {
		return errors.New("indexers: Register: empty configContract")
	}
	if factory == nil {
		return errors.New("indexers: Register: nil factory")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	k := registryKey{implementation: implementation, configContract: configContract}
	if _, dup := r.entries[k]; dup {
		return fmt.Errorf("%w: impl=%q contract=%q", ErrAlreadyRegistered, implementation, configContract)
	}
	r.entries[k] = factory
	return nil
}

// MustRegister panics on registration failure — intended for use
// inside init() blocks where failure indicates a programming error.
func (r *Registry) MustRegister(implementation, configContract string, factory Constructor) {
	if err := r.Register(implementation, configContract, factory); err != nil {
		panic(err)
	}
}

// Replace overwrites the factory for an already-registered key.
// Returns [ErrNotRegistered] when the key was never registered.
func (r *Registry) Replace(implementation, configContract string, factory Constructor) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := registryKey{implementation: implementation, configContract: configContract}
	if _, exists := r.entries[k]; !exists {
		return fmt.Errorf("%w: impl=%q contract=%q", ErrNotRegistered, implementation, configContract)
	}
	r.entries[k] = factory
	return nil
}

// Lookup returns the Constructor for the given key or
// [ErrNotRegistered].
func (r *Registry) Lookup(implementation, configContract string) (Constructor, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	k := registryKey{implementation: implementation, configContract: configContract}
	f, ok := r.entries[k]
	if !ok {
		return nil, fmt.Errorf("%w: impl=%q contract=%q", ErrNotRegistered, implementation, configContract)
	}
	return f, nil
}

// Has reports whether the key is registered.
func (r *Registry) Has(implementation, configContract string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.entries[registryKey{implementation: implementation, configContract: configContract}]
	return ok
}

// Names returns every registered (Implementation, ConfigContract)
// pair sorted by Implementation then ConfigContract.
func (r *Registry) Names() []registryKey {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]registryKey, 0, len(r.entries))
	for k := range r.entries {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].implementation != out[j].implementation {
			return out[i].implementation < out[j].implementation
		}
		return out[i].configContract < out[j].configContract
	})
	return out
}

// Construct resolves the row's (Implementation, ConfigContract) to
// a Constructor and invokes it. Nil row → typed error.
func (r *Registry) Construct(row *Indexer, deps Dependencies) (Provider, error) {
	if row == nil {
		return nil, errors.New("indexers: Construct: nil row")
	}
	contract := ""
	if row.ConfigContract != nil {
		contract = *row.ConfigContract
	}
	factory, err := r.Lookup(row.Implementation, contract)
	if err != nil {
		return nil, err
	}
	return factory(row, deps)
}

// Implementation returns the registry key's Implementation column.
func (k registryKey) Implementation() string { return k.implementation }

// ConfigContract returns the registry key's ConfigContract column.
func (k registryKey) ConfigContract() string { return k.configContract }

// String returns "impl/contract".
func (k registryKey) String() string {
	return k.implementation + "/" + k.configContract
}

// defaultRegistry is the package-global Registry instance.
var defaultRegistry = NewRegistry()

// Default returns the package-global Registry. Mirrors C#
// `IIndexerFactory` static facade.
func Default() *Registry { return defaultRegistry }
