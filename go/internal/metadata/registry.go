package metadata

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Registry is the per-family typed Constructor map. Concurrent-access
// safe — Register/Replace are write-locked, Lookup/Has/Names are
// read-locked. The expected lifecycle mirrors
// [importlists.Registry] / [notifications.Registry]:
//
//   - package init() blocks in each concrete metadata-writer package
//     call [Registry.Register] / [Registry.MustRegister] once at
//     process start;
//   - the consumer-facing service (queue [W7] — MetadataService)
//     calls [Registry.Construct] at dispatch time to instantiate the
//     matching Provider per the persisted row's Implementation plus
//     ConfigContract columns.
//
// A separate [Default] global mirrors the C# `IMetadataFactory` static
// registry; tests construct private Registries when isolation matters.
//
// Slice 4.BA (queue [W1] of focused-subset 6 — METADATA-WRITERS
// foundation): pattern-mirror of importlists.Registry at
// `internal/importlists/registry.go`.
type Registry struct {
	mu      sync.RWMutex
	entries map[registryKey]Constructor
}

// registryKey is the (Implementation, ConfigContract) pair. Every
// concrete metadata-writer has a distinct ConfigContract; the pair-key
// is used for parity with the importlists.Registry /
// notifications.Registry / downloadclients.Registry precedents and to
// leave room for a hypothetical future case where two implementations
// share a contract.
type registryKey struct {
	implementation string
	configContract string
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{entries: map[registryKey]Constructor{}}
}

// ErrAlreadyRegistered is returned by [Registry.Register] when a key
// is already present. Re-registering with the same factory usually
// means an init() loop ran twice — surface the duplicate rather than
// silently winning.
var ErrAlreadyRegistered = errors.New("metadata: already registered")

// ErrNotRegistered is returned by [Registry.Lookup] / [Registry.Replace]
// when the (Implementation, ConfigContract) pair has no Constructor
// registered. Distinguishes a missing registration from a registration
// whose factory returns an error at construct time.
var ErrNotRegistered = errors.New("metadata: not registered")

// Register binds factory to (implementation, configContract). Both
// keys must be non-empty (an empty key surfaces a programmer error —
// every C# concrete metadata-writer class has both a Type.Name and a
// ConfigContract).
//
// Returns [ErrAlreadyRegistered] when the key is already present.
func (r *Registry) Register(implementation, configContract string, factory Constructor) error {
	if implementation == "" {
		return errors.New("metadata: Register: empty implementation")
	}
	if configContract == "" {
		return errors.New("metadata: Register: empty configContract")
	}
	if factory == nil {
		return errors.New("metadata: Register: nil factory")
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

// MustRegister is the panic-on-error variant for use inside init()
// blocks where a registration failure indicates a programming error.
func (r *Registry) MustRegister(implementation, configContract string, factory Constructor) {
	if err := r.Register(implementation, configContract, factory); err != nil {
		panic(err)
	}
}

// Replace overwrites the factory for (implementation, configContract).
// Returns [ErrNotRegistered] when the key was never registered. Used
// by tests that want to swap a real factory for a stub.
func (r *Registry) Replace(implementation, configContract string, factory Constructor) error {
	if factory == nil {
		return errors.New("metadata: Replace: nil factory")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	k := registryKey{implementation: implementation, configContract: configContract}
	if _, exists := r.entries[k]; !exists {
		return fmt.Errorf("%w: impl=%q contract=%q", ErrNotRegistered, implementation, configContract)
	}
	r.entries[k] = factory
	return nil
}

// Lookup returns the Constructor for (implementation, configContract)
// or [ErrNotRegistered] when the key has no factory. Concurrent
// callers may invoke Lookup freely.
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

// Has reports whether (implementation, configContract) is registered.
func (r *Registry) Has(implementation, configContract string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.entries[registryKey{implementation: implementation, configContract: configContract}]
	return ok
}

// Names returns every registered (Implementation, ConfigContract) pair
// sorted by Implementation then ConfigContract. Useful for the v3
// API's "schema" endpoint (which lists every available metadata-writer
// type) and for tests that assert the catalog matches a known set.
func (r *Registry) Names() []RegistryKey {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]RegistryKey, 0, len(r.entries))
	for k := range r.entries {
		out = append(out, RegistryKey{Implementation: k.implementation, ConfigContract: k.configContract})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Implementation != out[j].Implementation {
			return out[i].Implementation < out[j].Implementation
		}
		return out[i].ConfigContract < out[j].ConfigContract
	})
	return out
}

// RegistryKey is the exported (Implementation, ConfigContract) tuple
// [Registry.Names] returns. Exported variant of the package-private
// `registryKey` so callers iterating the catalog can read the fields
// without re-deriving the shape.
type RegistryKey struct {
	Implementation string
	ConfigContract string
}

// String returns "impl/contract" — convenient for log lines and test
// assertions.
func (k RegistryKey) String() string {
	return k.Implementation + "/" + k.ConfigContract
}

// Construct is the convenience wrapper that resolves the row's
// (Implementation, ConfigContract) pair to a Constructor and invokes
// it. Returns the constructed Provider or the propagated error.
//
// nil row → typed error (defensive guard for the orchestrator's per-
// row loop).
func (r *Registry) Construct(row *Metadata, deps Dependencies) (Provider, error) {
	if row == nil {
		return nil, errors.New("metadata: Construct: nil row")
	}
	factory, err := r.Lookup(row.Implementation, row.ConfigContract)
	if err != nil {
		return nil, err
	}
	return factory(row, deps)
}

// defaultRegistry is the package-global Registry instance. Concrete
// provider packages register here at init() time.
var defaultRegistry = NewRegistry()

// Default returns the package-global Registry. Mirrors the C#
// `IMetadataFactory` static facade.
func Default() *Registry { return defaultRegistry }
