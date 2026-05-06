package config

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"sonarr.tv/sonarr/internal/messaging"
)

// ConfigSavedEvent is published by Provider's typed setters when a
// persisted value actually changed. Mirrors the empty marker event of
// the same name at
// `src/NzbDrone.Core/Configuration/Events/ConfigSavedEvent.cs:5`.
//
// CFG-Q5 (analysis/questions.md) flagged the asymmetry between this
// event and `configfile.ConfigFileSavedEvent`; the resolution
// direction is to consolidate on a single `ConfigChangedEvent` with a
// source tag in a follow-up. For now the Go port emits the
// per-source events to preserve subscriber semantics — the
// healthcheck wiring at `internal/healthcheck/api_key_validation.go:23`
// documents that consolidation in its own commentary.
type ConfigSavedEvent struct{}

// Provider ports the IConfigService surface at
// `src/NzbDrone.Core/Configuration/ConfigService.cs:24-525` over a
// `*Repo`. Only the five getters DiskScanService reaches for at
// `:104, :106, :272, :279, :290` (CreateEmptySeriesFolders,
// DeleteEmptyFolders, SetPermissionsLinux, ChmodFolder, ChownGroup)
// are exposed in this slice — further well-known accessors port as
// their callers land.
//
// Keys are lower-cased before they hit the Repo, matching
// `ConfigService.cs:498` (`key = key.ToLowerInvariant()`). The
// existing `*Repo` keeps whatever key the caller passed; only the
// Provider normalises.
//
// # Cache parity
//
// The C# class lazily fills `_cache` on first read and clears it on
// every write (`:518-524`). The Go port follows the same pattern but
// resolves a different Go-port asymmetry along the way: getters at
// the IConfigService property surface take no `ctx`, so the cache
// must be primed eagerly. `Load(ctx)` is called once at boot;
// subsequent reads are lock-protected map lookups with no I/O. Setters
// update the cache in-place (single-key write under the lock) before
// publishing `ConfigSavedEvent` — this keeps a getter on the
// publishing goroutine consistent with the value just written, and
// avoids a round-trip back to the DB on every Set.
//
// `Subscribe(bus)` wires a sync handler onto `ConfigSavedEvent` that
// reloads the cache from disk. Today every writer flows through this
// Provider so the self-publish → self-subscribe path is one extra
// `repo.All` per Set; mirrors the rootfolders bestPath cache invalidation
// pattern (`internal/rootfolders/service.go:294-303`) so future
// out-of-band writers (e.g. a migration script writing the Config
// table directly) keep getters fresh.
type Provider struct {
	repo *Repo
	bus  *messaging.Bus

	mu    sync.RWMutex
	cache map[string]string // lowercase key → raw string value
}

// NewProvider binds a Repo + optional Bus into a Provider. bus may be
// nil for tests that don't care about ConfigSavedEvent emission;
// production wiring at `cmd/sonarr/main.go` always passes the
// process-wide messaging.Bus so subscribers (cache invalidation,
// healthchecks via [CheckOn(ConfigSavedEvent)]) fire.
//
// NewProvider does NOT call Load — callers are expected to invoke
// `Load(ctx)` before the first read. Boot wiring does this; tests do
// it inside their fixtures. Splitting construction from initial
// fetch matches the configfile.NewProvider posture and lets host code
// log the boot time of each I/O step independently.
func NewProvider(repo *Repo, bus *messaging.Bus) (*Provider, error) {
	if repo == nil {
		return nil, errors.New("config: nil repo")
	}
	return &Provider{
		repo:  repo,
		bus:   bus,
		cache: map[string]string{},
	}, nil
}

// Load fully rebuilds the in-memory cache from the Config table.
// Idempotent — call at boot, then again from a ConfigSavedEvent
// handler if you've wired one via Subscribe. Returns the underlying
// `repo.All` error wrapped with a package prefix.
func (p *Provider) Load(ctx context.Context) error {
	all, err := p.repo.All(ctx)
	if err != nil {
		return fmt.Errorf("config: load: %w", err)
	}
	next := make(map[string]string, len(all))
	for _, e := range all {
		next[strings.ToLower(e.Key)] = e.Value
	}
	p.mu.Lock()
	p.cache = next
	p.mu.Unlock()
	return nil
}

// Subscribe registers a sync handler that reloads the cache on every
// ConfigSavedEvent. Pass the same Bus that was given to NewProvider —
// nil bus is a no-op so the wiring is forgiving. Call once at boot
// AFTER Load so the first event sees a populated cache.
func (p *Provider) Subscribe(bus *messaging.Bus) {
	if bus == nil {
		return
	}
	messaging.Subscribe(bus, "config.Provider.invalidate",
		func(ctx context.Context, _ ConfigSavedEvent) error {
			return p.Load(ctx)
		})
}

// getString returns the cached string value for key, or def if the
// key is missing OR the cached value is empty. Mirrors C# `GetValue`
// at `:454-474`: `!string.IsNullOrEmpty(dbValue)`.
func (p *Provider) getString(key, def string) string {
	p.mu.RLock()
	v, ok := p.cache[strings.ToLower(key)]
	p.mu.RUnlock()
	if !ok || v == "" {
		return def
	}
	return v
}

// getBool parses the cached value as bool, falling back to def for
// missing/empty/unparseable entries. Accepts "1"/"true"/"yes" as true
// and "0"/"false"/"no" as false (case-insensitive, whitespace-trimmed)
// — matches the configfile.Provider.Bool surface and the historic
// hand-edited config files that use "yes" / "no".
func (p *Provider) getBool(key string, def bool) bool {
	s := p.getString(key, "")
	if s == "" {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes":
		return true
	case "0", "false", "no":
		return false
	}
	return def
}

// setString upserts (key,value) through the Repo, updates the
// in-memory cache, and publishes ConfigSavedEvent if anything actually
// changed. Returns true on a real change so callers (the typed
// setters) can avoid spamming the bus on idempotent writes.
//
// The pre-write compare is best-effort: it reads the cache, not the
// DB. If the cache is stale (e.g. an out-of-band writer changed the
// row since the last Load) we may write redundantly — but the Repo's
// ON CONFLICT upsert is itself idempotent, and the published event
// will trigger every subscribed Provider to reload.
func (p *Provider) setString(ctx context.Context, key, value string) (bool, error) {
	lk := strings.ToLower(key)
	p.mu.RLock()
	prev, had := p.cache[lk]
	p.mu.RUnlock()
	if had && prev == value {
		return false, nil
	}
	if _, err := p.repo.Set(ctx, lk, value); err != nil {
		return false, fmt.Errorf("config: set(%q): %w", lk, err)
	}
	p.mu.Lock()
	p.cache[lk] = value
	p.mu.Unlock()
	if p.bus != nil {
		if err := messaging.Publish(ctx, p.bus, ConfigSavedEvent{}); err != nil &&
			!errors.Is(err, messaging.ErrShutdown) {
			return true, fmt.Errorf("config: publish ConfigSavedEvent: %w", err)
		}
	}
	return true, nil
}

func (p *Provider) setBool(ctx context.Context, key string, v bool) (bool, error) {
	return p.setString(ctx, key, strconv.FormatBool(v))
}

// CreateEmptySeriesFolders mirrors `ConfigService.cs:160-165`. False
// is the C# default at `:162`.
func (p *Provider) CreateEmptySeriesFolders() bool {
	return p.getBool("CreateEmptySeriesFolders", false)
}

// SetCreateEmptySeriesFolders writes the new value, publishing
// ConfigSavedEvent on change. Mirrors the property setter at
// `ConfigService.cs:164`.
func (p *Provider) SetCreateEmptySeriesFolders(ctx context.Context, v bool) error {
	_, err := p.setBool(ctx, "CreateEmptySeriesFolders", v)
	return err
}

// DeleteEmptyFolders mirrors `ConfigService.cs:167-172`. Default
// false at `:169`.
func (p *Provider) DeleteEmptyFolders() bool {
	return p.getBool("DeleteEmptyFolders", false)
}

// SetDeleteEmptyFolders writes the new value, publishing
// ConfigSavedEvent on change.
func (p *Provider) SetDeleteEmptyFolders(ctx context.Context, v bool) error {
	_, err := p.setBool(ctx, "DeleteEmptyFolders", v)
	return err
}

// SetPermissionsLinux mirrors `ConfigService.cs:283-288`. Default
// false at `:285`.
func (p *Provider) SetPermissionsLinux() bool {
	return p.getBool("SetPermissionsLinux", false)
}

// SetSetPermissionsLinux writes the new value, publishing
// ConfigSavedEvent on change. Naming preserves the C# property name
// even though the doubled "Set" is awkward — keeps grep parity with
// the C# source.
func (p *Provider) SetSetPermissionsLinux(ctx context.Context, v bool) error {
	_, err := p.setBool(ctx, "SetPermissionsLinux", v)
	return err
}

// ChmodFolder mirrors `ConfigService.cs:290-295`. Default "755" at
// `:292`.
func (p *Provider) ChmodFolder() string {
	return p.getString("ChmodFolder", "755")
}

// SetChmodFolder writes the new value, publishing ConfigSavedEvent
// on change.
func (p *Provider) SetChmodFolder(ctx context.Context, v string) error {
	_, err := p.setString(ctx, "ChmodFolder", v)
	return err
}

// ChownGroup mirrors `ConfigService.cs:297-302`. Default "" at
// `:299`.
func (p *Provider) ChownGroup() string {
	return p.getString("ChownGroup", "")
}

// SetChownGroup writes the new value, publishing ConfigSavedEvent
// on change.
func (p *Provider) SetChownGroup(ctx context.Context, v string) error {
	_, err := p.setString(ctx, "ChownGroup", v)
	return err
}
