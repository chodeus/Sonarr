package config

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/quality"
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

// CopyUsingHardlinks mirrors `ConfigService.cs:209-214`. Default true
// at `:211`. Drives the [episodefilemoving.Service.CopyEpisodeFile]
// hardlink-vs-copy decision — when true, the writer first tries
// [disk.Provider.TryCreateHardLink] before falling back to plain
// copy; when false, it always copies. The matching writer-path is
// at `internal/episodefilemoving/episodefilemoving.go:471`.
//
// Read by the slice-3.96 episodefilemoving.Service binding through
// Go's structural typing — *config.Provider satisfies the writer's
// narrow [Config] interface (`CopyUsingHardlinks() bool`) directly.
func (p *Provider) CopyUsingHardlinks() bool {
	return p.getBool("CopyUsingHardlinks", true)
}

// SetCopyUsingHardlinks writes the new value, publishing
// ConfigSavedEvent on change.
func (p *Provider) SetCopyUsingHardlinks(ctx context.Context, v bool) error {
	_, err := p.setBool(ctx, "CopyUsingHardlinks", v)
	return err
}

// EpisodeTitleRequired mirrors `ConfigService.cs:258-263`. Returns the
// raw stored enum identifier — defaults to `"Always"` when the row is
// missing OR empty (matches C# `GetValueEnum(..., Always)`).
//
// Callers in [internal/episodeimport] resolve to a typed
// [episodeimport.EpisodeTitleRequiredType] via
// `episodeimport.ParseEpisodeTitleRequiredType`. Returning the raw
// string here keeps the config package free of the
// `MediaFiles/EpisodeImport` import that defines the enum (C# leaks
// the type by reference; the Go port pushes parsing one layer up to
// preserve the dependency tree).
func (p *Provider) EpisodeTitleRequired() string {
	return p.getString("EpisodeTitleRequired", "Always")
}

// SetEpisodeTitleRequired writes the new enum identifier, publishing
// ConfigSavedEvent on change. Caller should pass one of `"Always"`,
// `"BulkSeasonReleases"`, or `"Never"` — the persistence layer doesn't
// validate (matches C# `SetValue` posture).
func (p *Provider) SetEpisodeTitleRequired(ctx context.Context, v string) error {
	_, err := p.setString(ctx, "EpisodeTitleRequired", v)
	return err
}

// UserRejectedExtensions mirrors `ConfigService.cs:265-269`. Default
// "" — empty disables the user filter (matches C# `string.Empty`).
//
// Read by [downloadedimport.ConfigReader] (slice 3.79c) — the
// orchestrator's per-file extension-rejection ladder consults this
// list AFTER the dangerous / executable / archive checks at C#
// `DownloadedEpisodesImportService.cs:301-321`.
func (p *Provider) UserRejectedExtensions() string {
	return p.getString("UserRejectedExtensions", "")
}

// getInt parses the cached value as int, falling back to def for
// missing/empty/unparseable entries.
func (p *Provider) getInt(key string, def int) int {
	s := p.getString(key, "")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}

func (p *Provider) setInt(ctx context.Context, key string, v int) (bool, error) {
	return p.setString(ctx, key, strconv.Itoa(v))
}

// DownloadPropersAndRepacks mirrors `ConfigService.cs:130-136`. Returns
// the parsed user preference; default is [quality.ProperPreferAndUpgrade]
// per C# `:134`. Recognises both the C# enum-identifier form
// (`"PreferAndUpgrade"`) and the Go kebab-case form
// (`"prefer-and-upgrade"`) — see [quality.ParseProperDownloadType].
func (p *Provider) DownloadPropersAndRepacks() quality.ProperDownloadType {
	return quality.ParseProperDownloadType(p.getString("DownloadPropersAndRepacks", "PreferAndUpgrade"))
}

// SetDownloadPropersAndRepacks writes the new value, publishing
// ConfigSavedEvent on change.
func (p *Provider) SetDownloadPropersAndRepacks(ctx context.Context, v quality.ProperDownloadType) error {
	_, err := p.setString(ctx, "DownloadPropersAndRepacks", v.String())
	return err
}

// Retention mirrors `ConfigService.cs:?`. Default 0 — the C# default
// at `GetValueInt("Retention", 0)`. Days of usenet retention configured
// by the user; 0 disables the [decisionengine.RetentionSpecification]
// check. Read by [decisionengine.RetentionSpecification] (slice 3.A4).
func (p *Provider) Retention() int {
	return p.getInt("Retention", 0)
}

// SetRetention writes the new value, publishing ConfigSavedEvent on
// change.
func (p *Provider) SetRetention(ctx context.Context, v int) error {
	_, err := p.setInt(ctx, "Retention", v)
	return err
}

// MinimumFreeSpaceWhenImporting mirrors `ConfigService.cs:202-207`. The
// stored unit is megabytes; default 100 per C# `:204`.
func (p *Provider) MinimumFreeSpaceWhenImporting() int {
	return p.getInt("MinimumFreeSpaceWhenImporting", 100)
}

// SetMinimumFreeSpaceWhenImporting writes the new value, publishing
// ConfigSavedEvent on change.
func (p *Provider) SetMinimumFreeSpaceWhenImporting(ctx context.Context, v int) error {
	_, err := p.setInt(ctx, "MinimumFreeSpaceWhenImporting", v)
	return err
}

// SkipFreeSpaceCheckWhenImporting mirrors `ConfigService.cs:195-200`.
// Default false at `:197`.
func (p *Provider) SkipFreeSpaceCheckWhenImporting() bool {
	return p.getBool("SkipFreeSpaceCheckWhenImporting", false)
}

// SetSkipFreeSpaceCheckWhenImporting writes the new value, publishing
// ConfigSavedEvent on change.
func (p *Provider) SetSkipFreeSpaceCheckWhenImporting(ctx context.Context, v bool) error {
	_, err := p.setBool(ctx, "SkipFreeSpaceCheckWhenImporting", v)
	return err
}

// DownloadClientWorkingFolders mirrors `ConfigService.cs:181-186`. The
// stored value is a `|`-separated list of unpack-prefixes; default
// `"_UNPACK_|_FAILED_"` per C# `:183`. Callers split on `|` themselves
// (see `Specifications/NotUnpackingSpecification.cs:34`); the Go
// [internal/episodeimport.NotUnpackingSpecification] does the same.
func (p *Provider) DownloadClientWorkingFolders() string {
	return p.getString("DownloadClientWorkingFolders", "_UNPACK_|_FAILED_")
}

// SetDownloadClientWorkingFolders writes the new value, publishing
// ConfigSavedEvent on change.
func (p *Provider) SetDownloadClientWorkingFolders(ctx context.Context, v string) error {
	_, err := p.setString(ctx, "DownloadClientWorkingFolders", v)
	return err
}

// EnableMediaInfo mirrors `ConfigService.cs:216-221`. Drives the
// AggregationService.Augment pass: when false, MediaInfo extraction
// is skipped for files that already live in the library
// (ExistingFile=true).
//
// Default true at C# `:218` — note this is the only config getter in
// this slice that defaults to true; the existing
// `getBool(key, def bool) bool` helper accepts the def parameter
// (the suppressed-unparam comment at its call site documents the
// forward-looking shape; this addition is the first real default-true
// caller).
func (p *Provider) EnableMediaInfo() bool {
	return p.getBool("EnableMediaInfo", true)
}

// SetEnableMediaInfo writes the new value, publishing ConfigSavedEvent
// on change.
func (p *Provider) SetEnableMediaInfo(ctx context.Context, v bool) error {
	_, err := p.setBool(ctx, "EnableMediaInfo", v)
	return err
}

// RecycleBin mirrors `ConfigService.cs:100-104`. Default "" at
// `:102`. Empty value disables the recycle-bin feature — the
// recyclebin.Service [DeleteFile] method falls through to a hard
// delete, matching the C# `string.IsNullOrWhiteSpace` arm at
// `RecycleBinProvider.cs:46-51`.
func (p *Provider) RecycleBin() string {
	return p.getString("RecycleBin", "")
}

// SetRecycleBin writes the new value, publishing ConfigSavedEvent
// on change.
func (p *Provider) SetRecycleBin(ctx context.Context, v string) error {
	_, err := p.setString(ctx, "RecycleBin", v)
	return err
}

// RecycleBinCleanupDays mirrors `ConfigService.cs:106-110`. Default
// 7 at `:108`. A value of 0 disables Cleanup entirely (matches the
// C# `cleanupDays == 0` short-circuit at
// `RecycleBinProvider.cs:172-176`).
func (p *Provider) RecycleBinCleanupDays() int {
	return p.getInt("RecycleBinCleanupDays", 7)
}

// SetRecycleBinCleanupDays writes the new value, publishing
// ConfigSavedEvent on change.
func (p *Provider) SetRecycleBinCleanupDays(ctx context.Context, v int) error {
	_, err := p.setInt(ctx, "RecycleBinCleanupDays", v)
	return err
}

// MaximumSize mirrors `ConfigService.cs:119-123`. Default 0 — the C#
// default at `GetValueInt("MaximumSize", 0)`. A value of 0 disables
// the global maximum-size cap entirely; any positive value is a
// release-size ceiling (in MB) applied by
// [decisionengine.MaximumSizeSpecification].
func (p *Provider) MaximumSize() int {
	return p.getInt("MaximumSize", 0)
}

// SetMaximumSize writes the new value, publishing ConfigSavedEvent
// on change.
func (p *Provider) SetMaximumSize(ctx context.Context, v int) error {
	_, err := p.setInt(ctx, "MaximumSize", v)
	return err
}

// MinimumAge mirrors `ConfigService.cs:125-129`. Default 0 — the C#
// default at `GetValueInt("MinimumAge", 0)`. A value of 0 disables
// the minimum-age floor; any positive value is the minimum age in
// minutes a usenet release must have before
// [decisionengine.MinimumAgeSpecification] will accept it.
func (p *Provider) MinimumAge() int {
	return p.getInt("MinimumAge", 0)
}

// SetMinimumAge writes the new value, publishing ConfigSavedEvent
// on change.
func (p *Provider) SetMinimumAge(ctx context.Context, v int) error {
	_, err := p.setInt(ctx, "MinimumAge", v)
	return err
}

// AutoUnmonitorPreviouslyDownloadedEpisodes mirrors
// `ConfigService.cs:88-92`. Default false — C# `GetValueBoolean`
// without an explicit default falls back to false. Drives the
// auto-unmonitor cascade in [episodes.Service.HandleEpisodeFileDeleted]
// (slice 3.AA): when true, deleting a file cascades to clearing the
// monitored flag on the linked episodes.
//
// Read by [episodes.Service] through Go's structural typing —
// *config.Provider satisfies the narrow
// [episodes.AutoUnmonitorMissingProvider] interface directly.
func (p *Provider) AutoUnmonitorPreviouslyDownloadedEpisodes() bool {
	return p.getBool("AutoUnmonitorPreviouslyDownloadedEpisodes", false)
}

// SetAutoUnmonitorPreviouslyDownloadedEpisodes writes the new value,
// publishing ConfigSavedEvent on change.
func (p *Provider) SetAutoUnmonitorPreviouslyDownloadedEpisodes(ctx context.Context, v bool) error {
	_, err := p.setBool(ctx, "AutoUnmonitorPreviouslyDownloadedEpisodes", v)
	return err
}

// EnableCompletedDownloadHandling mirrors `ConfigService.cs:139-143`.
// Default true at `:141` — note this is one of the few config getters
// that defaults to true. Drives the
// [decisionengine.AlreadyImportedSpec] gate (slice 3.AL): when CDH is
// disabled the spec accepts every release without consulting history,
// matching the C# `:33-37` short-circuit at the top of IsSatisfiedBy.
func (p *Provider) EnableCompletedDownloadHandling() bool {
	return p.getBool("EnableCompletedDownloadHandling", true)
}

// SetEnableCompletedDownloadHandling writes the new value, publishing
// ConfigSavedEvent on change.
func (p *Provider) SetEnableCompletedDownloadHandling(ctx context.Context, v bool) error {
	_, err := p.setBool(ctx, "EnableCompletedDownloadHandling", v)
	return err
}

// SeasonPackUpgrade mirrors `ConfigService.cs:271-275`. Returns the
// raw stored enum identifier (`"All"`, `"Threshold"`, `"Any"`);
// defaults to `"All"` when the row is missing or empty (matches C#
// `GetValueEnum("SeasonPackUpgrade", All)`).
//
// The decisionengine consumer parses to a typed
// [decisionengine.SeasonPackUpgradeType] via
// [decisionengine.ParseSeasonPackUpgradeType] at the wiring adapter —
// keeps the config package free of the decisionengine import (same
// dep-shape dodge as `EpisodeTitleRequired` returning a raw string).
//
// Read by [decisionengine.UpgradeDiskSpecification].
func (p *Provider) SeasonPackUpgrade() string {
	return p.getString("SeasonPackUpgrade", "All")
}

// SetSeasonPackUpgrade writes the new enum identifier, publishing
// ConfigSavedEvent on change. Caller should pass one of `"All"`,
// `"Threshold"`, or `"Any"` — the persistence layer doesn't validate
// (matches C# `SetValue` posture).
func (p *Provider) SetSeasonPackUpgrade(ctx context.Context, v string) error {
	_, err := p.setString(ctx, "SeasonPackUpgrade", v)
	return err
}

// SeasonPackUpgradeThreshold mirrors `ConfigService.cs:277-281`.
// Default 100.0 — C# `GetValueDouble("SeasonPackUpgradeThreshold", 100.0)`.
// Read in tandem with [SeasonPackUpgrade]; only consulted when the
// upgrade-mode is `"Threshold"`.
//
// Stored as a string in the Config table; the getter parses via
// strconv.ParseFloat with a 100.0 fallback for missing / empty /
// unparseable rows. Caller domain is decisionengine.UpgradeDiskSpec
// where the value compares against an integer percent-upgradable —
// the C# `double` is preserved verbatim so a user-stored fractional
// threshold (e.g. 87.5%) round-trips byte-identically.
func (p *Provider) SeasonPackUpgradeThreshold() float64 {
	return p.getFloat64("SeasonPackUpgradeThreshold", 100.0)
}

// SetSeasonPackUpgradeThreshold writes the new value, publishing
// ConfigSavedEvent on change.
func (p *Provider) SetSeasonPackUpgradeThreshold(ctx context.Context, v float64) error {
	_, err := p.setFloat64(ctx, "SeasonPackUpgradeThreshold", v)
	return err
}

// FileDate mirrors `ConfigService.cs:174-179`. Default `"None"` per
// C# `GetValueEnum("FileDate", FileDateType.None)`. Returns the raw
// stored enum-identifier string (`"None"` / `"LocalAirDate"` /
// `"UtcAirDate"`); the consumer ([episodefilemoving.UpdateService])
// parses it into a typed enum locally — same posture as
// [SeasonPackUpgrade] / [DownloadPropersAndRepacks].
//
// Read by the slice-3.BK [episodefilemoving.UpdateService.ChangeFileDateForFile]
// + the SeriesScannedEvent handler that follows. Closes queue [14].
func (p *Provider) FileDate() string {
	return p.getString("FileDate", "None")
}

// SetFileDate writes the new enum identifier, publishing
// ConfigSavedEvent on change. Caller should pass one of `"None"`,
// `"LocalAirDate"`, or `"UtcAirDate"` — the persistence layer
// doesn't validate (matches the C# `SetValue` posture).
func (p *Provider) SetFileDate(ctx context.Context, v string) error {
	_, err := p.setString(ctx, "FileDate", v)
	return err
}

// UseScriptImport mirrors `ConfigService.cs:223-228`. Default `false`
// per C# `GetValueBoolean("UseScriptImport", false)`. Read by the
// slice-3.BL [episodefilemoving.ImportScriptService.TryImport] which
// short-circuits to `DeferMove` when the flag is off.
func (p *Provider) UseScriptImport() bool {
	return p.getBool("UseScriptImport", false)
}

// SetUseScriptImport writes the new value, publishing ConfigSavedEvent
// on change.
func (p *Provider) SetUseScriptImport(ctx context.Context, v bool) error {
	_, err := p.setBool(ctx, "UseScriptImport", v)
	return err
}

// ScriptImportPath mirrors `ConfigService.cs:230-235`. Default empty
// string — C# `GetValue("ScriptImportPath")` returns null which the
// Go-port collapses to "". Path to the user-supplied script the
// ImportScriptService invokes during a move/copy with two positional
// args (sourcePath, destinationPath). Empty path with UseScriptImport=true
// surfaces as a script-spawn error per the C# behaviour.
func (p *Provider) ScriptImportPath() string {
	return p.getString("ScriptImportPath", "")
}

// SetScriptImportPath writes the new value, publishing ConfigSavedEvent
// on change.
func (p *Provider) SetScriptImportPath(ctx context.Context, v string) error {
	_, err := p.setString(ctx, "ScriptImportPath", v)
	return err
}

// ApplicationUrl mirrors `ConfigService.cs:421`. Default empty string.
// Surfaced to user-supplied scripts (and notification provider Phase 4
// surfaces) as the externally-visible URL of this Sonarr instance.
func (p *Provider) ApplicationUrl() string {
	return p.getString("ApplicationUrl", "")
}

// SetApplicationUrl writes the new value, publishing ConfigSavedEvent
// on change.
func (p *Provider) SetApplicationUrl(ctx context.Context, v string) error {
	_, err := p.setString(ctx, "ApplicationUrl", v)
	return err
}

// getFloat64 parses the cached value as float64, falling back to def
// for missing / empty / unparseable entries. Mirrors the strconv-based
// posture of [getInt] / [getBool].
func (p *Provider) getFloat64(key string, def float64) float64 {
	s := p.getString(key, "")
	if s == "" {
		return def
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return def
	}
	return v
}

func (p *Provider) setFloat64(ctx context.Context, key string, v float64) (bool, error) {
	return p.setString(ctx, key, strconv.FormatFloat(v, 'f', -1, 64))
}
