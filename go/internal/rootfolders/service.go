package rootfolders

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// DiskProvider is the slice of `NzbDrone.Common.Disk.IDiskProvider` the C#
// `RootFolderService` reaches for at `RootFolderService.cs:107-211`.
// Six path-keyed methods cover the full Add/GetDetails/GetUnmappedFolders
// surface; nothing else from `IDiskProvider` is exercised here. Pulled out
// as an interface so tests can inject a stub without dragging in a real
// disk-provider port. The wider real impl lands in Phase 3 (mediafiles).
type DiskProvider interface {
	FolderExists(path string) bool
	FolderWritable(path string) bool
	FolderEmpty(path string) bool
	GetAvailableSpace(path string) (int64, error)
	GetTotalSize(path string) (int64, error)
	GetDirectories(path string) ([]string, error)
}

// SeriesPaths is the slice of `ISeriesRepository.AllSeriesPaths()` the C#
// `RootFolderService` calls at `:75`, `:123`, `:188`. One method returning
// `seriesId → path`. Stubbed in this slice; the real implementation lands
// when SeriesService ports.
type SeriesPaths interface {
	AllSeriesPaths(ctx context.Context) (map[int]string, error)
}

// NamingConfigProvider is the slice of `INamingConfigService.GetConfig()`
// the C# `RootFolderService` reaches for at `:154` — only the
// `SeriesFolderFormat` field is actually read. Tests inject a stub; the
// real wiring binds `*namingconfig.Service.GetConfig`'s SeriesFolderFormat
// at the host boundary.
type NamingConfigProvider interface {
	GetSeriesFolderFormat(ctx context.Context) (string, error)
}

// Validation sentinels mirror the distinct exception types thrown by
// `RootFolderService.Add` at `:102-120`. Callers (REST handlers, the
// future Add flow) classify via `errors.Is` rather than string-matching.
var (
	// ErrInvalidPath maps to C# `ArgumentException("Invalid path")` at `:104`.
	ErrInvalidPath = errors.New("rootfolders: invalid path")

	// ErrFolderNotFound maps to C# `DirectoryNotFoundException(...)` at `:109`.
	ErrFolderNotFound = errors.New("rootfolders: folder does not exist")

	// ErrDuplicatePath maps to C# `InvalidOperationException("Recent directory already exists.")` at `:114`.
	ErrDuplicatePath = errors.New("rootfolders: path already configured")

	// ErrFolderNotWritable maps to C# `UnauthorizedAccessException(...)` at `:119`.
	ErrFolderNotWritable = errors.New("rootfolders: folder is not writable")
)

// detailsTimeout is the wall-clock bound for the async GetDetails work.
// Mirrors `RootFolderService.GetDetails` at `:212` —
// `Task.Run(...).Wait(timeout ? 5000 : -1)`.
const detailsTimeout = 5 * time.Second

// bestPathTTL is the upper bound on entries in the GetBestRootFolderPath
// cache. The C# version uses `TimeSpan.FromDays(1)` at `:197`; mutations
// (Add/Remove) clear the entire cache, so the TTL is a defensive bound
// that rarely triggers in practice — but kept for parity.
const bestPathTTL = 24 * time.Hour

// Service ports `RootFolderService.cs:26-228`. Wraps the Repo with a
// validating Add, a per-path GetBestRootFolderPath cache, and the async
// GetDetails / GetUnmappedFolders disk-stat collection.
//
// The C# class doesn't define an `IEventAggregator` field and emits no
// events; the Go port mirrors that — no `bus *messaging.Bus`. Higher-level
// config-changed events (when they land) can subscribe at the surrounding
// boundary if needed.
//
// The bestPath cache uses mutation-driven invalidation per the established
// Phase 2 pattern. Unlike the Mapping-list cache in
// `remotepathmappings.Service` (single slice), this is a per-path map —
// repeated lookups for the same path stick until the next Add/Remove.
type Service struct {
	repo   *Repo
	disk   DiskProvider
	series SeriesPaths
	naming NamingConfigProvider

	mu          sync.Mutex
	bestPathMap map[string]bestPathEntry

	// clock + detailsBound are settable for tests. Production callers
	// use the package defaults.
	clock         func() time.Time
	detailsBound  time.Duration
	bestPathBound time.Duration
}

type bestPathEntry struct {
	result  string
	expires time.Time
}

// NewService binds a Repo + DiskProvider + SeriesPaths + NamingConfig
// provider into a Service. All four are required — there's no useful
// "skip disk provider" mode (unlike RemotePathMapping where a migration
// caller might pass a always-true stub).
func NewService(r *Repo, disk DiskProvider, series SeriesPaths, naming NamingConfigProvider) (*Service, error) {
	if r == nil {
		return nil, errors.New("rootfolders: nil repo")
	}
	if disk == nil {
		return nil, errors.New("rootfolders: nil disk provider")
	}
	if series == nil {
		return nil, errors.New("rootfolders: nil series-paths provider")
	}
	if naming == nil {
		return nil, errors.New("rootfolders: nil naming-config provider")
	}
	return &Service{
		repo:          r,
		disk:          disk,
		series:        series,
		naming:        naming,
		bestPathMap:   make(map[string]bestPathEntry),
		clock:         time.Now,
		detailsBound:  detailsTimeout,
		bestPathBound: bestPathTTL,
	}, nil
}

// SetClock injects a deterministic clock for tests of cache TTL expiry.
// Production callers should leave this alone.
func (s *Service) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clock = now
}

// SetDetailsTimeout overrides the GetDetails async wall-clock bound for
// tests. Production callers should leave this at the package default.
func (s *Service) SetDetailsTimeout(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.detailsBound = d
}

// All returns every persisted RootFolder. Mirrors C# `:65-70` — no cache
// at this level (the C# version doesn't cache the All() list either; the
// `_cache` field is keyed by path for GetBestRootFolderPath only).
func (s *Service) All(ctx context.Context) ([]RootFolder, error) {
	rows, err := s.repo.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("rootfolders: All: %w", err)
	}
	return rows, nil
}

// AllWithUnmappedFolders returns every RootFolder with disk-stat fields
// (Accessible, IsEmpty, FreeSpace, TotalSpace) and the UnmappedFolders
// list populated via async disk walks. Mirrors C# `:72-96`.
//
// Disk failures on individual rows are logged-and-suppressed in the C#
// version (`folder.UnmappedFolders = new List<UnmappedFolder>();`) so the
// UI can still render the list and offer a delete. The Go port mirrors
// that: on error, the row keeps its zero details fields and an empty
// unmapped list.
func (s *Service) AllWithUnmappedFolders(ctx context.Context) ([]Folder, error) {
	rows, err := s.repo.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("rootfolders: AllWithUnmappedFolders: %w", err)
	}
	seriesPaths, err := s.series.AllSeriesPaths(ctx)
	if err != nil {
		return nil, fmt.Errorf("rootfolders: load series paths: %w", err)
	}
	out := make([]Folder, 0, len(rows))
	for _, row := range rows {
		f := Folder{ID: row.ID, Path: row.Path}
		s.fillDetails(ctx, &f, seriesPaths, true)
		out = append(out, f)
	}
	return out, nil
}

// Add validates and inserts a RootFolder. Mirrors C# `:98-129`.
//
// On success the persisted form is returned with disk-stat fields and the
// unmapped folders list populated (the C# version mutates the input
// `rootFolder` reference in place and returns it; the Go port does the
// same to the input pointer and additionally returns it for ergonomic
// chaining).
func (s *Service) Add(ctx context.Context, rf *RootFolder) (*Folder, error) {
	if rf == nil {
		return nil, errors.New("rootfolders: cannot add nil root folder")
	}
	if strings.TrimSpace(rf.Path) == "" || !filepath.IsAbs(rf.Path) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidPath, rf.Path)
	}
	if !s.disk.FolderExists(rf.Path) {
		return nil, fmt.Errorf("%w: %s", ErrFolderNotFound, rf.Path)
	}

	all, err := s.All(ctx)
	if err != nil {
		return nil, err
	}
	for _, existing := range all {
		if pathEquals(existing.Path, rf.Path) {
			return nil, fmt.Errorf("%w: %s", ErrDuplicatePath, rf.Path)
		}
	}

	if !s.disk.FolderWritable(rf.Path) {
		return nil, fmt.Errorf("%w: %s", ErrFolderNotWritable, rf.Path)
	}

	if _, err := s.repo.Insert(ctx, rf); err != nil {
		return nil, fmt.Errorf("rootfolders: insert: %w", err)
	}
	seriesPaths, err := s.series.AllSeriesPaths(ctx)
	if err != nil {
		return nil, fmt.Errorf("rootfolders: load series paths: %w", err)
	}
	f := Folder{ID: rf.ID, Path: rf.Path}
	s.fillDetails(ctx, &f, seriesPaths, true)
	s.invalidateBestPath()
	return &f, nil
}

// Remove deletes a RootFolder by ID and clears the bestPath cache.
// Mirrors C# `:131-135`.
func (s *Service) Remove(ctx context.Context, id int) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("rootfolders: delete: %w", err)
	}
	s.invalidateBestPath()
	return nil
}

// Get fetches one RootFolder by ID with details populated. The `timeout`
// flag mirrors the C# parameter at `:185` — when true, the disk-stat work
// is bounded by [detailsTimeout]; when false, it runs to completion (the
// C# `Wait(-1)` infinite-wait semantic).
func (s *Service) Get(ctx context.Context, id int, timeout bool) (*Folder, error) {
	row, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	seriesPaths, err := s.series.AllSeriesPaths(ctx)
	if err != nil {
		return nil, fmt.Errorf("rootfolders: load series paths: %w", err)
	}
	f := Folder{ID: row.ID, Path: row.Path}
	s.fillDetails(ctx, &f, seriesPaths, timeout)
	return &f, nil
}

// GetBestRootFolderPath returns the longest-prefix RootFolder.Path that's
// a parent of `path`, falling back to `filepath.Dir(path)` when no folder
// matches. Mirrors C# `:195-227`.
//
// Backed by a per-path cache (mutation-invalidated, with a defensive 24h
// TTL bound). On cache miss the cache stores the result and the bound
// expiry; lookups within bound just return the cached value.
func (s *Service) GetBestRootFolderPath(ctx context.Context, path string) (string, error) {
	s.mu.Lock()
	now := s.clock()
	if entry, ok := s.bestPathMap[path]; ok && now.Before(entry.expires) {
		s.mu.Unlock()
		return entry.result, nil
	}
	bound := s.bestPathBound
	s.mu.Unlock()

	rows, err := s.repo.All(ctx)
	if err != nil {
		return "", fmt.Errorf("rootfolders: GetBestRootFolderPath: %w", err)
	}
	folders := make([]Folder, 0, len(rows))
	for _, r := range rows {
		folders = append(folders, Folder{ID: r.ID, Path: r.Path})
	}
	result := BestPath(path, folders)

	s.mu.Lock()
	s.bestPathMap[path] = bestPathEntry{result: result, expires: now.Add(bound)}
	s.mu.Unlock()
	return result, nil
}

// invalidateBestPath clears the per-path cache. Mirrors C# `_cache.Clear()`
// at `:126` and `:134`. The map is replaced rather than ranged-and-deleted
// so concurrent readers in transit can't see partial state.
func (s *Service) invalidateBestPath() {
	s.mu.Lock()
	s.bestPathMap = make(map[string]bestPathEntry)
	s.mu.Unlock()
}

// pathEquals reports whether two filesystem paths refer to the same
// location, using case-insensitive comparison after `filepath.Clean`.
// Mirrors the C# `string.PathEquals` extension. The C# version is
// case-sensitive on Linux and case-insensitive on Windows; the Go port
// uses case-insensitive everywhere (matching the established convention
// in `logic.IsParentPath`) since the regression cost of accepting a dup
// `/tv` vs `/TV` is lower than letting them through and silently
// double-walking.
func pathEquals(a, b string) bool {
	if a == "" || b == "" {
		return a == b
	}
	ca := strings.TrimRight(filepath.Clean(a), `/\`)
	cb := strings.TrimRight(filepath.Clean(b), `/\`)
	return strings.EqualFold(ca, cb)
}

// detailsResult bundles the data the async disk-stat collector produces.
// The collector writes a single value to a buffered channel; the wait
// path applies the result iff it arrives before the timeout. Bundling
// the data into a value type avoids a data race on the Folder struct
// when the goroutine outlives a timed-out wait.
type detailsResult struct {
	accessible bool
	isEmpty    bool
	freeSpace  *int64
	totalSpace *int64
	unmapped   []UnmappedFolder
	err        error
}

// fillDetails runs the disk-stat collection on `f` with optional 5s wall
// clock bound. Mirrors `RootFolderService.GetDetails` at `:200-213`.
//
// On disk errors mid-collection, the partially-collected data is
// discarded and `f` keeps its zero defaults — same observable shape as
// the C# `try/catch` swallow at `:88-92` (caller-side, in
// AllWithUnmappedFolders).
func (s *Service) fillDetails(ctx context.Context, f *Folder, seriesPaths map[int]string, timeout bool) {
	s.mu.Lock()
	bound := s.detailsBound
	s.mu.Unlock()

	ch := make(chan detailsResult, 1)
	go func() {
		ch <- s.gatherDetails(ctx, f.Path, seriesPaths)
	}()

	if !timeout {
		applyDetails(f, <-ch)
		return
	}
	select {
	case res := <-ch:
		applyDetails(f, res)
	case <-time.After(bound):
		// Goroutine still running; the buffered channel lets it write
		// its result and exit cleanly without blocking. We just don't
		// apply it — `f` keeps its zero defaults.
	}
}

func applyDetails(f *Folder, res detailsResult) {
	if res.err != nil {
		f.UnmappedFolders = []UnmappedFolder{}
		return
	}
	f.Accessible = res.accessible
	f.IsEmpty = res.isEmpty
	f.FreeSpace = res.freeSpace
	f.TotalSpace = res.totalSpace
	if res.unmapped == nil {
		f.UnmappedFolders = []UnmappedFolder{}
	} else {
		f.UnmappedFolders = res.unmapped
	}
}

func (s *Service) gatherDetails(ctx context.Context, path string, seriesPaths map[int]string) detailsResult {
	if !s.disk.FolderExists(path) {
		return detailsResult{}
	}
	res := detailsResult{accessible: true}
	res.isEmpty = s.disk.FolderEmpty(path)
	if free, err := s.disk.GetAvailableSpace(path); err == nil {
		f := free
		res.freeSpace = &f
	}
	if total, err := s.disk.GetTotalSize(path); err == nil {
		t := total
		res.totalSpace = &t
	}
	unmapped, err := s.getUnmappedFolders(ctx, path, seriesPaths)
	if err != nil {
		res.err = err
		return res
	}
	res.unmapped = unmapped
	return res
}

// getUnmappedFolders walks the directories under `path` to a depth derived
// from the NamingConfig.SeriesFolderFormat separator count, removes
// `seriesPaths` members (case-insensitive), filters out special folders,
// and returns the result sorted by name. Mirrors C# `:137-183`.
func (s *Service) getUnmappedFolders(ctx context.Context, path string, seriesPaths map[int]string) ([]UnmappedFolder, error) {
	if path == "" {
		return nil, errors.New("rootfolders: empty path provided")
	}
	if !s.disk.FolderExists(path) {
		return []UnmappedFolder{}, nil
	}

	format, err := s.naming.GetSeriesFolderFormat(ctx)
	if err != nil {
		return nil, fmt.Errorf("rootfolders: read series-folder-format: %w", err)
	}
	subFolderDepth := strings.Count(format, string(filepath.Separator))

	dirs, err := s.disk.GetDirectories(path)
	if err != nil {
		return nil, fmt.Errorf("rootfolders: list directories %q: %w", path, err)
	}

	for i := 0; i < subFolderDepth; i++ {
		next := make([]string, 0, len(dirs))
		for _, d := range dirs {
			children, err := s.disk.GetDirectories(d)
			if err != nil {
				return nil, fmt.Errorf("rootfolders: list directories %q: %w", d, err)
			}
			next = append(next, children...)
		}
		dirs = next
	}

	known := make(map[string]bool, len(seriesPaths))
	for _, p := range seriesPaths {
		known[strings.ToLower(strings.TrimRight(filepath.Clean(p), `/\`))] = true
	}

	results := make([]UnmappedFolder, 0, len(dirs))
	for _, d := range dirs {
		key := strings.ToLower(strings.TrimRight(filepath.Clean(d), `/\`))
		if known[key] {
			continue
		}
		base := filepath.Base(d)
		if IsSpecialFolder(base) {
			continue
		}
		rel, err := filepath.Rel(path, d)
		if err != nil {
			rel = base
		}
		results = append(results, UnmappedFolder{
			Name:         base,
			Path:         d,
			RelativePath: rel,
		})
	}

	sort.SliceStable(results, func(i, j int) bool {
		return strings.ToLower(results[i].Name) < strings.ToLower(results[j].Name)
	})
	return results, nil
}
