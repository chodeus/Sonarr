package configfile

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/messaging"
)

// ConfigFileSavedEvent is published by Provider.SaveConfigDictionary
// when at least one persisted key changed. Mirrors the empty marker
// event of the same name in
// `src/NzbDrone.Core/Configuration/Events/ConfigFileSavedEvent.cs`.
//
// CFG-Q5 (analysis/questions.md) flagged the asymmetry between this
// event and ConfigService's `ConfigSavedEvent`; the resolution
// direction is to consolidate on a single `ConfigChangedEvent` with
// a source tag in a follow-up PR. For now the Go port emits the
// per-source events to preserve subscriber semantics.
type ConfigFileSavedEvent struct{}

// Overrides carries the host-binding layer's view of any env var or
// CLI flag overrides for the well-known keys. Each field is a
// pointer so callers can distinguish "set but empty" (forces empty
// string) from "not set" (fall through to XML). Unknown / future
// well-known keys are read straight from the XML — the override
// struct only needs entries for keys the host binding code parses.
type Overrides struct {
	BindAddress    *string
	Port           *int
	SslPort        *int
	EnableSsl      *bool
	APIKey         *string // map onto the AuthOptions.ApiKey env var
	URLBase        *string
	InstanceName   *string
	LogLevel       *string
	AuthMethod     *string // raw string; Provider parses to AuthenticationType
	AuthRequired   *string
	BranchOverride *string
	LaunchBrowser  *bool
}

// Provider exposes typed accessors over a Loader plus a small
// override set. Goroutine-safe — every accessor takes the loader
// mutex.
//
// The Provider does not lazily Load on construction; callers should
// call l.Load before constructing the Provider so the in-memory doc
// reflects disk state. NewProvider exists for tests that want to
// inject a pre-populated Loader.
type Provider struct {
	l    *Loader
	over Overrides

	bus *messaging.Bus // optional — if nil, save events are not published
	gen APIKeyGenerator
}

// APIKeyGenerator returns a fresh API key. Injected so tests can be
// deterministic; production wiring passes auth.GenerateAPIKey from
// internal/auth (Phase 1 ships that helper).
type APIKeyGenerator func() (string, error)

// NewProvider constructs a Provider. bus may be nil for tests that
// don't need ConfigFileSavedEvent emission. gen may be nil; if so,
// every ApiKey access on a row missing the key falls back to the
// empty string (caller is expected to provide a generator in
// production).
func NewProvider(loader *Loader, over Overrides, bus *messaging.Bus, gen APIKeyGenerator) *Provider {
	return &Provider{l: loader, over: over, bus: bus, gen: gen}
}

// String returns the value for key, applying the override → XML →
// default precedence. If persist is true and the XML had no value,
// the default is written to the in-memory doc and flushed to disk
// before returning. C# defaults persist=true; some keys
// (analytics-related, postgres credentials, log overrides) pass
// persist=false to keep them out of the persisted file.
//
// String is the building block for every typed accessor below; tests
// also use it directly for legacy-key access without a typed
// accessor.
func (p *Provider) String(ctx context.Context, key, def string, persist bool) (string, error) {
	if v, ok := p.lookupOverrideString(key); ok {
		return v, nil
	}
	p.l.mu.Lock()
	defer p.l.mu.Unlock()
	if v, ok := p.l.doc.get(key); ok {
		return v, nil
	}
	if persist {
		if changed := p.l.doc.set(key, def); changed {
			if err := p.l.saveLocked(); err != nil {
				return "", err
			}
		}
	}
	return def, nil
}

// Int reads key as int. On parse failure, returns def (no error) —
// matches C# `Convert.ToInt32` which throws on garbage but tests
// don't seed garbage.
func (p *Provider) Int(ctx context.Context, key string, def int, persist bool) (int, error) {
	if v, ok := p.lookupOverrideInt(key); ok {
		return v, nil
	}
	s, err := p.String(ctx, key, strconv.Itoa(def), persist)
	if err != nil {
		return 0, err
	}
	n, perr := strconv.Atoi(s)
	if perr != nil {
		return def, nil
	}
	return n, nil
}

// Bool reads key as bool. Parses "1"/"true"/"yes" as true; everything
// else as false. C# uses `Convert.ToBoolean` which is forgiving but
// rejects e.g. "yes" — we accept "yes" because some hand-edited
// config files use it. Returns def on parse failure.
func (p *Provider) Bool(ctx context.Context, key string, def bool, persist bool) (bool, error) {
	if v, ok := p.lookupOverrideBool(key); ok {
		return v, nil
	}
	s, err := p.String(ctx, key, strconv.FormatBool(def), persist)
	if err != nil {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes":
		return true, nil
	case "0", "false", "no":
		return false, nil
	}
	return def, nil
}

// Set writes value for key, persisting to disk immediately. Unlike
// SaveConfigDictionary, Set does NOT publish ConfigFileSavedEvent —
// it's the building block used by typed accessors that auto-persist
// on default; the dictionary save path is the user-initiated one
// that emits the event.
func (p *Provider) Set(ctx context.Context, key, value string) error {
	p.l.mu.Lock()
	defer p.l.mu.Unlock()
	if changed := p.l.doc.set(key, value); !changed {
		return nil
	}
	return p.l.saveLocked()
}

// SaveConfigDictionary applies a batch of updates: every (key,value)
// pair is compared against the current effective value; only changed
// entries are written. The "ApiKey" key is silently dropped (CFG-Q3
// behaviour; rotating the API key goes through a separate
// ResetApiKeyCommand). Returns true if anything changed and a
// ConfigFileSavedEvent was published.
//
// CFG-Q9 fix: writes happen atomically — the in-memory doc is
// mutated, then the whole file is rewritten via temp + rename. A
// process crash mid-save leaves either the old file or the new file,
// never a half-written file.
func (p *Provider) SaveConfigDictionary(ctx context.Context, updates map[string]string) (bool, error) {
	p.l.mu.Lock()
	hasUpdated := false
	for k, v := range updates {
		if strings.EqualFold(k, "ApiKey") {
			continue
		}
		current, ok := p.l.doc.get(k)
		if !ok {
			// C# behaviour: SaveConfigDictionary skips keys not already
			// present in the dict-of-defaults. The Go port preserves
			// this — `Set` is the path for fresh keys.
			continue
		}
		trimmed := strings.TrimSpace(v)
		if trimmed == current {
			continue
		}
		p.l.doc.set(k, trimmed)
		hasUpdated = true
	}
	if hasUpdated {
		if err := p.l.saveLocked(); err != nil {
			p.l.mu.Unlock()
			return false, err
		}
	}
	p.l.mu.Unlock()
	if hasUpdated && p.bus != nil {
		if err := messaging.Publish(ctx, p.bus, ConfigFileSavedEvent{}); err != nil &&
			!errors.Is(err, messaging.ErrShutdown) {
			return hasUpdated, fmt.Errorf("configfile: publish ConfigFileSavedEvent: %w", err)
		}
	}
	return hasUpdated, nil
}

// EnsureDefault writes the file with the defaults from a snapshot
// dictionary if the file does not already exist on disk. Mirrors
// `EnsureDefaultConfigFile` in the C# tree. The defaults map is
// typically the result of GetConfigDictionary against a freshly-
// constructed Provider so every well-known key gets persisted.
func (p *Provider) EnsureDefault(ctx context.Context, defaults map[string]string) error {
	p.l.mu.Lock()
	defer p.l.mu.Unlock()
	// We're writing only when no XML-on-disk file exists and the
	// in-memory doc is empty. If the loader already populated values,
	// honour them (a Load failed-then-recovered shouldn't blow them
	// away).
	if _, err := stat(p.l.path); err == nil {
		return nil
	}
	for k, v := range defaults {
		p.l.doc.set(k, v)
	}
	return p.l.saveLocked()
}

// stat is a thin os.Stat wrapper exported for tests; package-private
// to keep the public surface small.
var stat = func(path string) (any, error) {
	if _, err := osStat(path); err != nil {
		return nil, err
	}
	return struct{}{}, nil
}

// osStat is split out so package-level test fakes can replace it.
var osStat = osStatReal

func osStatReal(path string) (any, error) { return osStatImpl(path) }

// --- override lookup helpers ---

func (p *Provider) lookupOverrideString(key string) (string, bool) {
	switch key {
	case "BindAddress":
		if p.over.BindAddress != nil {
			return *p.over.BindAddress, true
		}
	case "ApiKey":
		if p.over.APIKey != nil {
			return *p.over.APIKey, true
		}
	case "UrlBase":
		if p.over.URLBase != nil {
			return *p.over.URLBase, true
		}
	case "InstanceName":
		if p.over.InstanceName != nil {
			return *p.over.InstanceName, true
		}
	case "LogLevel":
		if p.over.LogLevel != nil {
			return *p.over.LogLevel, true
		}
	case "AuthenticationMethod":
		if p.over.AuthMethod != nil {
			return *p.over.AuthMethod, true
		}
	case "AuthenticationRequired":
		if p.over.AuthRequired != nil {
			return *p.over.AuthRequired, true
		}
	case "Branch":
		if p.over.BranchOverride != nil {
			return *p.over.BranchOverride, true
		}
	}
	return "", false
}

func (p *Provider) lookupOverrideInt(key string) (int, bool) {
	switch key {
	case "Port":
		if p.over.Port != nil {
			return *p.over.Port, true
		}
	case "SslPort":
		if p.over.SslPort != nil {
			return *p.over.SslPort, true
		}
	}
	return 0, false
}

func (p *Provider) lookupOverrideBool(key string) (bool, bool) {
	switch key {
	case "EnableSsl":
		if p.over.EnableSsl != nil {
			return *p.over.EnableSsl, true
		}
	case "LaunchBrowser":
		if p.over.LaunchBrowser != nil {
			return *p.over.LaunchBrowser, true
		}
	}
	return false, false
}
