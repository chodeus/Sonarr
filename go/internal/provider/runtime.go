package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Provider is the family-agnostic runtime interface every provider class
// implements. C# cite: src/NzbDrone.Core/ThingiProvider/IProvider.cs:7-16.
//
// Per-family interfaces (IIndexer / IDownloadClient / INotification /
// IImportList / IMetadata) extend IProvider with family-specific methods
// (Fetch, Download, OnGrab, etc.). Those will land with each family port.
// This package only owns the IProvider surface.
type Provider interface {
	// Name returns the user-visible provider class name (e.g. "Newznab").
	// Mirrors IProvider.Name.
	Name() string

	// ConfigContract returns the Settings class name (e.g. "NewznabSettings").
	// Used by the API/v3 to drive schema rendering. Mirrors IProvider.ConfigContract
	// (the C# version returns Type, the Go port returns the string identifier
	// that round-trips through the database).
	ConfigContract() string

	// Message returns an optional status banner — typically nil. Mirrors
	// IProvider.Message.
	Message() *Message

	// DefaultDefinitions returns one or more pre-canned Definitions the
	// UI offers when the user adds a new instance of this provider class.
	// Most classes return one entry; some (e.g. Trakt's import lists)
	// return several presets. Mirrors IProvider.DefaultDefinitions.
	DefaultDefinitions() []*Definition

	// Definition returns the Definition the provider was constructed with
	// (or nil for the unbound "factory shape" case). Mirrors the C#
	// `Definition { get; set; }` property.
	Definition() *Definition

	// SetDefinition mutates the bound Definition. Used during the
	// row→runtime hydration path. Mirrors the C# setter.
	SetDefinition(d *Definition)

	// Test runs a connection/validation probe and returns the result.
	// Cancellation honoured via ctx (the C# version is sync; the Go port
	// is ctx-aware so HTTP calls can be cancelled). Mirrors IProvider.Test().
	Test(ctx context.Context) ValidationResult

	// RequestAction is the catch-all hook for UI-driven actions outside
	// the standard Test/Save lifecycle (e.g. the "browse remote folders"
	// button on download-clients). stage names the action; query carries
	// the payload. Most providers return nil. Mirrors IProvider.RequestAction.
	RequestAction(stage string, query map[string]string) (any, error)
}

// Factory hydrates persisted rows into runtime Definition values.
//
// The flow:
//  1. The per-family repo (e.g. indexers.Repo) loads a row.
//  2. Service code converts the row to a Row interface and calls
//     LoadDefinition.
//  3. LoadDefinition looks up the constructor in the Registry, calls it
//     to get a fresh Settings instance, json.Unmarshal's the row's
//     SettingsRaw into it, and packs everything into a *Definition.
//  4. The caller then matches Implementation against the per-family
//     Provider class registry to instantiate a runtime Provider.
type Factory interface {
	LoadDefinition(family Family, row Row) (*Definition, error)
}

// Row is the minimal slice-of-fields a per-family repo must expose so
// the Factory can build a Definition without taking a hard dependency
// on each family's row type. Per-family slices implement Row on their
// row types via tiny adapter methods.
type Row interface {
	GetID() int
	GetName() string
	GetImplementation() string
	GetConfigContract() string
	GetEnable() bool
	GetSettingsJSON() string
	GetTags() []int
	GetMessage() *Message
}

// NewFactory returns a Factory backed by reg. reg is typically the
// process-wide Registry (provider.Default()) but tests may inject a
// custom one to control which constructors are available.
func NewFactory(reg *Registry) Factory {
	return &registryFactory{reg: reg}
}

type registryFactory struct {
	reg *Registry
}

// LoadDefinition implements Factory. Behaviour:
//   - Empty Implementation → ErrMissingImplementation (the row is corrupt).
//   - Constructor not registered → returns the Definition with Settings=nil
//     and SettingsRaw populated, plus a *Message of MessageError. This
//     mirrors C# RemoveMissingImplementations semantics — the service
//     layer decides whether to delete or surface; the factory itself
//     does not destroy data.
//   - Constructor registered but nil → same as not-registered (Phase 1
//     catalog rows return nil constructors; queue [4]+ wires the real ones).
//   - SettingsRaw empty → constructor() with no JSON unmarshal.
//   - SettingsRaw set + constructor returns non-Config → ErrSettingsNotConfig.
//   - JSON unmarshal failure → wrapped error.
func (f *registryFactory) LoadDefinition(family Family, row Row) (*Definition, error) {
	if row == nil {
		return nil, ErrNilRow
	}
	impl := row.GetImplementation()
	contract := row.GetConfigContract()
	if impl == "" {
		return nil, ErrMissingImplementation
	}

	def := &Definition{
		ID:             row.GetID(),
		Name:           row.GetName(),
		Implementation: impl,
		ConfigContract: contract,
		Enable:         row.GetEnable(),
		SettingsRaw:    row.GetSettingsJSON(),
		Message:        row.GetMessage(),
		Tags:           row.GetTags(),
	}
	if def.Tags == nil {
		def.Tags = []int{}
	}

	key := Key{Impl: impl, Contract: contract}
	ctor, ok := f.reg.Lookup(family, key)
	if !ok || ctor == nil {
		// Not registered or no constructor — keep raw JSON, surface message.
		if def.Message == nil {
			def.Message = NewErrorMessage(fmt.Sprintf("Implementation %q (contract %q) is not registered", impl, contract))
		}
		return def, nil
	}

	settings := ctor()
	cfg, isConfig := settings.(Config)
	if !isConfig {
		return nil, fmt.Errorf("%w: family=%q impl=%q contract=%q got %T", ErrSettingsNotConfig, family, impl, contract, settings)
	}

	if def.SettingsRaw != "" {
		if err := json.Unmarshal([]byte(def.SettingsRaw), cfg); err != nil {
			return nil, fmt.Errorf("provider: unmarshal settings for %s/%s: %w", impl, contract, err)
		}
	}
	def.Settings = cfg
	return def, nil
}

// MarshalSettings is the inverse of the SettingsRaw→Settings hydration:
// given a Definition with a non-nil Settings, write back its JSON form
// into SettingsRaw. Used by service code on save.
func MarshalSettings(d *Definition) error {
	if d == nil {
		return ErrNilDefinition
	}
	if d.Settings == nil {
		// No typed settings — leave SettingsRaw alone. This covers the
		// "missing implementation" round-trip path where we want to
		// preserve the raw JSON without re-marshalling.
		return nil
	}
	b, err := json.Marshal(d.Settings)
	if err != nil {
		return fmt.Errorf("provider: marshal settings for %s/%s: %w", d.Implementation, d.ConfigContract, err)
	}
	d.SettingsRaw = string(b)
	return nil
}

// Sentinel errors.
var (
	// ErrMissingImplementation is returned by LoadDefinition when the
	// row's Implementation column is empty — corrupt row, not a missing
	// constructor.
	ErrMissingImplementation = errors.New("provider: row Implementation is empty")

	// ErrSettingsNotConfig is returned by LoadDefinition when the
	// Constructor wired into the registry returns a value that doesn't
	// implement Config. This is a programming error in the family-port
	// slice — surface it loudly.
	ErrSettingsNotConfig = errors.New("provider: registered constructor returned a value that does not implement Config")

	// ErrNilRow is returned by LoadDefinition when row is nil.
	ErrNilRow = errors.New("provider: nil row")

	// ErrNilDefinition is returned by MarshalSettings when called on nil.
	ErrNilDefinition = errors.New("provider: nil definition")
)
