package downloadclients

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Constructor builds a [Provider] instance from a persisted
// [DownloadClient] row + the cross-provider [Dependencies] bundle.
// Concrete client packages (Sabnzbd, Transmission, etc. — queue [14]
// and follow-ups) register one Constructor per (Implementation,
// ConfigContract) pair at init() time.
//
// Returning an error covers two failure modes the C# tree handles
// via exceptions:
//
//   - The row's Settings JSON couldn't be deserialised into the
//     expected per-client Settings struct.
//   - A required dependency in [Dependencies] is nil and the
//     concrete client refuses to construct without it (e.g. a
//     usenet client refusing to start without IHttpClient).
//
// The first case is the common one — the registry tolerates a row
// whose ConfigContract column has drifted from the registered
// Constructor's expected shape by returning a typed error the
// orchestrator can fall back on (mark the row as "broken
// implementation" without losing the persisted Settings JSON).
type Constructor func(definition *DownloadClient, deps Dependencies) (Provider, error)

// Dependencies bundles the cross-provider deps every concrete
// download-client constructor receives. Mirrors the parameter pack
// shared across `DownloadClientBase`, `UsenetClientBase`, and
// `TorrentClientBase` constructors in the C# tree (see
// `src/NzbDrone.Core/Download/{DownloadClientBase,UsenetClientBase,TorrentClientBase}.cs`).
//
// Fields are typed as `any` so the actual cross-package dep types
// (httpclient.Client, config.Provider, disk.Provider, etc.) don't
// drag this package's import graph through every consumer. Concrete
// client packages cast to the typed shape they need at construction
// time. Nil values are explicitly tolerated when the per-client
// constructor doesn't reach for that dep — e.g. a usenet client
// constructor that doesn't talk to disk leaves DiskProvider nil.
//
// The named struct here is the contract; we evolve it additively
// as concrete clients land in queue [14] and follow-ups.
type Dependencies struct {
	// HTTPClient is the HTTP transport every client uses to reach
	// its RPC endpoint. Bound to `*httpclient.Client` in production.
	// Mirrors C# `IHttpClient` injected into every download-client
	// constructor.
	HTTPClient any

	// ConfigProvider is the application-config getter (download-
	// directory, remote-path mappings, retention timers, etc.).
	// Bound to `*config.Provider` in production.
	ConfigProvider any

	// DiskProvider gives concrete clients filesystem access for
	// the post-import cleanup branch (DeleteFolder / DeleteFile)
	// and the local-folder Test path. Bound to `*disk.Provider`
	// in production.
	DiskProvider any

	// Logger is the slog.Logger handle the concrete client emits
	// against. Bound to the host-supplied logger in production.
	Logger any

	// RemotePathMappingService translates the on-client absolute
	// paths to Sonarr-side paths (used when the client + Sonarr
	// run on separate hosts). Bound to the future
	// `remotepathmappings.Service` when that package ports. nil
	// when no remappings are configured.
	RemotePathMappingService any

	// StatusService is the [*downloadclientstatus.Service] for
	// back-off escalation. Bound by main.go.
	StatusService any
}

// Registry is the per-family typed Constructor map. Concurrent-
// access safe — Register/Replace are write-locked, Lookup/Has/Names
// are read-locked. The expected lifecycle:
//
//   - package init() blocks in each concrete client package call
//     Register once at process start;
//   - the orchestrator (queue [15] — ProcessDownloadDecisions)
//     calls Lookup at grab time to instantiate the matching
//     Provider per the persisted row's Implementation + Config-
//     Contract columns.
//
// A separate [Default] global mirrors the C# `IDownloadClientFactory`
// static registry; tests construct private Registries when isolation
// matters.
type Registry struct {
	mu      sync.RWMutex
	entries map[registryKey]Constructor
}

// registryKey is the (Implementation, ConfigContract) pair. Two
// known download-client implementations share a ConfigContract:
//
//   - "Transmission" + "Vuze" both use `TransmissionSettings`.
//   - "UsenetDownloadStation" + "TorrentDownloadStation" share
//     `DownloadStationSettings`.
//
// So the key must include both columns to disambiguate. Matches the
// reasoning in `internal/provider.Key` (the global identifier
// catalog).
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
// means an init() loop ran twice — surface the duplicate rather
// than silently winning.
var ErrAlreadyRegistered = errors.New("downloadclients: already registered")

// ErrNotRegistered is returned by [Registry.Lookup] /
// [Registry.Replace] when the (Implementation, ConfigContract) pair
// has no Constructor registered. Distinguishes a missing
// registration from a registration whose factory returns an error
// at construct time.
var ErrNotRegistered = errors.New("downloadclients: not registered")

// ErrMarkAsImportedUnsupported is the typed-error return for
// [Provider.MarkItemAsImported] implementations that don't support
// the operation. Mirrors C# `NotSupportedException` thrown by
// `DownloadClientBase.MarkItemAsImported` at
// `Download/DownloadClientBase.cs:189-192` for the default impl.
var ErrMarkAsImportedUnsupported = errors.New("downloadclients: MarkItemAsImported not supported by this client")

// Register binds factory to (implementation, configContract). Both
// keys must be non-empty (an empty key surfaces a programmer error
// — every C# concrete download-client class has both a Type.Name
// and a ConfigContract).
//
// Returns [ErrAlreadyRegistered] when the key is already present.
func (r *Registry) Register(implementation, configContract string, factory Constructor) error {
	if implementation == "" {
		return errors.New("downloadclients: Register: empty implementation")
	}
	if configContract == "" {
		return errors.New("downloadclients: Register: empty configContract")
	}
	if factory == nil {
		return errors.New("downloadclients: Register: nil factory")
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
// Returns [ErrNotRegistered] when the key was never registered.
// Used by tests that want to swap a real factory for a stub.
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

// Names returns every registered (Implementation, ConfigContract)
// pair sorted by Implementation then ConfigContract. Useful for the
// v3 API's "schema" endpoint (which lists every available client
// type) and for tests that assert the catalog matches a known set.
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

// Construct is the convenience wrapper that resolves the row's
// (Implementation, ConfigContract) pair to a Constructor and invokes
// it. Returns the constructed Provider or the propagated error.
//
// nil row → typed error (defensive guard for the orchestrator's
// per-row loop).
func (r *Registry) Construct(row *DownloadClient, deps Dependencies) (Provider, error) {
	if row == nil {
		return nil, errors.New("downloadclients: Construct: nil row")
	}
	factory, err := r.Lookup(row.Implementation, row.ConfigContract)
	if err != nil {
		return nil, err
	}
	return factory(row, deps)
}

// Implementation returns the registry key's Implementation column.
// Exposed so callers iterating over [Registry.Names] can read the
// fields without re-deriving the unexported struct shape.
func (k registryKey) Implementation() string { return k.implementation }

// ConfigContract returns the registry key's ConfigContract column.
func (k registryKey) ConfigContract() string { return k.configContract }

// String returns "impl/contract" — convenient for log lines and
// test assertions.
func (k registryKey) String() string {
	return k.implementation + "/" + k.configContract
}

// defaultRegistry is the package-global Registry instance. Concrete
// client packages register here at init() time.
var defaultRegistry = NewRegistry()

// Default returns the package-global Registry. Mirrors the C#
// `IDownloadClientFactory` static facade.
func Default() *Registry { return defaultRegistry }
