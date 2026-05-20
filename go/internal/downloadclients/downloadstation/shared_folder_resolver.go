package downloadstation

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"sonarr.tv/sonarr/internal/ospath"
)

// SharedFolderResolver ports the C# class
// `Download/Clients/DownloadStation/SharedFolderResolver.cs:14-55`.
//
// Synology DownloadStation reports per-task download directories as
// `<sharedFolder>/<subpath>` strings (e.g. `/data/tv/Show.S01E02`),
// where SharedFolder is a Synology administrative concept (e.g.
// `/data`) that maps to a physical mount point on the box (e.g.
// `/volume1/data`). The resolver:
//
//  1. Parses the first path component as the SharedFolder name.
//  2. Looks up the SharedFolder -> physical-path mapping via
//     [Proxy.GetSharedFolderMapping] (J120) — first hit caches the
//     result; subsequent calls for the same SharedFolder within the
//     TTL window short-circuit.
//  3. Substitutes the SharedFolder prefix on the input path with the
//     mapping's physical path, yielding a Sonarr-side absolute path
//     suitable for DiskProvider operations.
//
// The C# resolver keys the cache on `<serialNumber>:<sharedFolder>`
// — the serial number is fetched from a separate IDSMInfoProxy +
// SerialNumberProvider chain that the Go port hasn't shipped. The
// Go port keys on `<host>:<sharedFolder>` instead, which gives
// equivalent isolation across multiple Synology boxes; the
// SerialNumber-based key would survive a Host reconfig (e.g.
// switching between LAN and reverse-proxy URLs for the same box)
// where the Host-based key wouldn't — that's a minor cache
// regression on Host changes, acceptable for the J121 slice scope
// since the cache TTL is 1h.
//
// Slice 5.J121 (third slice of the audit B.6 DownloadStation
// SharedFolderResolver arc; J120 ported the FileStationProxy,
// J122 wires the resolver into TestOutputPath + GetStatus +
// deleteData disk arm).
type SharedFolderResolver struct {
	proxy    *Proxy
	ttl      time.Duration
	now      func() time.Time
	mu       sync.Mutex
	mappings map[string]sharedFolderEntry
}

// sharedFolderEntry holds one cached mapping with its TTL expiry.
type sharedFolderEntry struct {
	mapping   *SharedFolderMapping
	expiresAt time.Time
}

// DefaultSharedFolderTTL mirrors the C# `_cache.Get(..., TimeSpan.FromHours(1))`
// at `SharedFolderResolver.cs:49` — 1-hour rolling cache.
const DefaultSharedFolderTTL = 1 * time.Hour

// SharedFolderResolverOption tunes [NewSharedFolderResolver].
type SharedFolderResolverOption func(*SharedFolderResolver)

// WithSharedFolderTTL overrides the rolling-cache window. 0 disables
// caching (every Resolve re-walks the proxy).
func WithSharedFolderTTL(d time.Duration) SharedFolderResolverOption {
	return func(r *SharedFolderResolver) { r.ttl = d }
}

// WithSharedFolderClock pins the clock used for cache-expiry checks.
// Tests use this to step over the TTL boundary without time.Sleep.
func WithSharedFolderClock(now func() time.Time) SharedFolderResolverOption {
	return func(r *SharedFolderResolver) {
		if now != nil {
			r.now = now
		}
	}
}

// NewSharedFolderResolver constructs a resolver backed by the given
// FileStation proxy. Defaults: 1h cache TTL, wall-clock now. nil
// proxy panics — the resolver is useless without a proxy and the
// caller should construct one with the proper deps.
func NewSharedFolderResolver(proxy *Proxy, opts ...SharedFolderResolverOption) *SharedFolderResolver {
	if proxy == nil {
		panic("downloadstation: NewSharedFolderResolver: nil Proxy")
	}
	r := &SharedFolderResolver{
		proxy:    proxy,
		ttl:      DefaultSharedFolderTTL,
		now:      time.Now,
		mappings: make(map[string]sharedFolderEntry),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Resolve ports C# `SharedFolderResolver.RemapToFullPath` at
// `:44-54`.
//
// Algorithm:
//  1. Split the input path at the first `/` after position 1 to
//     isolate the SharedFolder component. An input like `/data` (no
//     subpath) treats the whole input as the SharedFolder; `/data/
//     subdir/file` extracts `/data`.
//  2. Cache lookup via `<host>:<sharedFolder>`. Hit within TTL
//     returns the cached mapping; miss invokes
//     [Proxy.GetSharedFolderMapping] (J120) and stores the result.
//     Errors from the proxy propagate unchanged so callers can
//     distinguish "couldn't resolve" from "no remap" — neither is
//     cached.
//  3. Substitute: `fullPath = mapping.PhysicalPath + (input - mapping.SharedFolder)`.
//     The right-hand side strips the SharedFolder prefix and the
//     result is appended via the OsPath `+`-operator (Add).
//
// Returns the resolved OsPath, or an empty OsPath + error when the
// proxy lookup fails. The C# tree's catch + rethrow at `:36-41` is
// preserved by propagating the error verbatim.
func (r *SharedFolderResolver) Resolve(ctx context.Context, s Settings, path string) (ospath.OsPath, error) {
	if strings.TrimSpace(path) == "" {
		return ospath.OsPath{}, fmt.Errorf("downloadstation: SharedFolderResolver: empty path")
	}
	sharedFolder := extractSharedFolder(path)
	if sharedFolder == "" {
		return ospath.OsPath{}, fmt.Errorf("downloadstation: SharedFolderResolver: cannot extract shared folder from %q", path)
	}
	key := s.Host + ":" + sharedFolder
	mapping, err := r.lookup(ctx, key, sharedFolder, s)
	if err != nil {
		return ospath.OsPath{}, err
	}
	input := ospath.New(path)
	sharedPath := ospath.New(sharedFolder)
	physicalPath := ospath.New(mapping.PhysicalPath)
	// (input - sharedFolder) is the relative-path tail; physicalPath + tail
	// recombines into the Sonarr-side path.
	relative := input.Sub(sharedPath)
	return physicalPath.Add(relative), nil
}

// lookup returns a cached mapping when the entry is within its TTL,
// otherwise fires GetSharedFolderMapping and caches the result.
// Errors are NOT cached so a transient API failure doesn't poison
// the lookup.
func (r *SharedFolderResolver) lookup(ctx context.Context, key, sharedFolder string, s Settings) (*SharedFolderMapping, error) {
	if r.ttl > 0 {
		r.mu.Lock()
		entry, ok := r.mappings[key]
		r.mu.Unlock()
		if ok && entry.expiresAt.After(r.now()) {
			return entry.mapping, nil
		}
	}
	mapping, err := r.proxy.GetSharedFolderMapping(ctx, s, sharedFolder)
	if err != nil {
		return nil, err
	}
	if r.ttl > 0 {
		r.mu.Lock()
		r.mappings[key] = sharedFolderEntry{
			mapping:   mapping,
			expiresAt: r.now().Add(r.ttl),
		}
		r.mu.Unlock()
	}
	return mapping, nil
}

// extractSharedFolder returns the first path component of the input
// — the SharedFolder name. Mirrors C# `SharedFolderResolver.cs:46-47`:
// finds the first `/` after position 1 and slices up to it; when no
// further `/` is found, the entire input is the SharedFolder.
//
// Examples:
//
//	/data/tv/Show     → /data
//	/data             → /data
//	data/tv           → data
//	/                 → ""
func extractSharedFolder(path string) string {
	if path == "" || path == "/" {
		return ""
	}
	// C# uses IndexOf('/', 1) — search starts at offset 1, skipping
	// any leading `/`. Replicate exactly.
	if len(path) < 2 {
		return path
	}
	idx := strings.Index(path[1:], "/")
	if idx == -1 {
		return path
	}
	return path[:idx+1]
}
