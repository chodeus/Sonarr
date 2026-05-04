package remotepathmappings

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"sonarr.tv/sonarr/internal/ospath"
)

// DiskProvider is the slice of `IDiskProvider` the C# service uses —
// `FolderExists` at `RemotePathMappingService.cs:117`. Pulled out as
// a one-method interface so tests can inject a stub without dragging
// in a real disk-provider port.
type DiskProvider interface {
	FolderExists(path string) bool
}

// Validation sentinels mirror the distinct exception types thrown by
// `RemotePathMappingService.ValidateMapping` at
// `RemotePathMappingService.cs:92-126`. Callers (REST handlers, the
// Add/Update flows) should classify errors via `errors.Is` rather
// than string-matching the wrapped messages.
var (
	// ErrInvalidHost mirrors the C# ArgumentException("Invalid Host").
	ErrInvalidHost = errors.New("remotepathmappings: invalid host")

	// ErrInvalidRemotePath covers both "must not start with a space"
	// and "cannot be empty" — the C# code throws a separate
	// ArgumentException for each but the Go port collapses them since
	// no caller distinguishes.
	ErrInvalidRemotePath = errors.New("remotepathmappings: invalid remote path")

	// ErrInvalidLocalPath covers "cannot be empty and must not be the
	// root" — i.e. empty OR unrooted.
	ErrInvalidLocalPath = errors.New("remotepathmappings: invalid local path")

	// ErrLocalFolderNotFound maps to C#
	// `DirectoryNotFoundException("Can't add mount point directory that doesn't exist.")`.
	ErrLocalFolderNotFound = errors.New("remotepathmappings: local folder does not exist")

	// ErrDuplicateMapping maps to C#
	// `InvalidOperationException("RemotePath already configured.")`.
	ErrDuplicateMapping = errors.New("remotepathmappings: mapping already configured")
)

// Service ports `RemotePathMappingService.cs:25-189`. Wraps the Repo
// with a typed cache, the per-Add path-normalization (every
// LocalPath/RemotePath is forced through `OsPath.AsDirectory()` so
// the persisted form always carries a trailing platform separator),
// the validation block, and the bidirectional remap helpers.
//
// The C# class injects `IDownloadClientRepository` in its
// constructor but never uses it (DryIoc dependency-graph shim);
// dropped from the Go port.
//
// Cache strategy follows the established Phase 2 service-wrapper
// pattern: lazily populated `[]Mapping` slice, RWMutex-protected,
// invalidated on every mutation. The C# `ICached<>.Get(key, fn,
// TimeSpan.FromSeconds(10))` time-bounded fallback is omitted —
// every All() call goes through the same code path that mutations
// invalidate, so the 10s TTL is a defensive bound that never
// triggers in practice.
//
// No event publication: the C# class doesn't define any
// `IEventAggregator` field and emits no events on
// Add/Update/Remove. Higher-level config-changed events (when they
// land) can subscribe at the surrounding boundary.
type Service struct {
	repo *Repo
	disk DiskProvider

	mu     sync.RWMutex
	cache  []Mapping
	loaded bool
}

// NewService binds a Repo + DiskProvider into a Service. The disk
// provider is required — Add validation depends on it. Callers in
// migration / batch contexts that want to skip the FolderExists
// check can pass a stub that always returns true.
func NewService(r *Repo, disk DiskProvider) (*Service, error) {
	if r == nil {
		return nil, errors.New("remotepathmappings: nil repo")
	}
	if disk == nil {
		return nil, errors.New("remotepathmappings: nil disk provider")
	}
	return &Service{repo: r, disk: disk}, nil
}

// All returns the cached mapping list, populating on first call.
// Mirrors `RemotePathMappingService.All()` at `:46-49`.
func (s *Service) All(ctx context.Context) ([]Mapping, error) {
	s.mu.RLock()
	if s.loaded {
		out := append([]Mapping(nil), s.cache...)
		s.mu.RUnlock()
		return out, nil
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded {
		return append([]Mapping(nil), s.cache...), nil
	}
	rows, err := s.repo.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("remotepathmappings: load: %w", err)
	}
	s.cache = append(s.cache[:0], rows...)
	s.loaded = true
	return append([]Mapping(nil), s.cache...), nil
}

// Get fetches a mapping by ID, bypassing the cache (mirrors the C#
// `_remotePathMappingRepository.Get(id)` direct call).
func (s *Service) Get(ctx context.Context, id int) (*Mapping, error) {
	return s.repo.Get(ctx, id)
}

// Add validates and inserts a mapping. Mirrors C# `:51-65`.
//
// Both paths are normalized via `OsPath.AsDirectory().FullPath()`
// BEFORE validation runs, so:
//
//   - Slashes are flipped to the platform-appropriate kind
//     (`D:/foo` → `D:\foo`).
//   - A trailing separator is forced on
//     (`D:\mounted\storage` → `D:\mounted\storage\`).
//
// The mapping pointer is mutated in place to reflect the cleaned
// paths; on success the caller sees the persisted form (which is
// what the C# `Insert` returns).
func (s *Service) Add(ctx context.Context, m *Mapping) (*Mapping, error) {
	if m == nil {
		return nil, errors.New("remotepathmappings: cannot add nil mapping")
	}
	m.LocalPath = ospath.New(m.LocalPath).AsDirectory().FullPath()
	m.RemotePath = ospath.New(m.RemotePath).AsDirectory().FullPath()

	all, err := s.All(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.validate(all, m); err != nil {
		return nil, err
	}
	if _, err := s.repo.Insert(ctx, m); err != nil {
		return nil, fmt.Errorf("remotepathmappings: insert: %w", err)
	}
	s.invalidate()
	return m, nil
}

// Update validates and persists changes to an existing mapping.
// Mirrors C# `:79-90`. Validation runs against all OTHER mappings
// (the row being updated is excluded so editing it doesn't trip the
// duplicate check against itself).
func (s *Service) Update(ctx context.Context, m *Mapping) (*Mapping, error) {
	if m == nil {
		return nil, errors.New("remotepathmappings: cannot update nil mapping")
	}
	all, err := s.All(ctx)
	if err != nil {
		return nil, err
	}
	others := make([]Mapping, 0, len(all))
	for _, r := range all {
		if r.ID != m.ID {
			others = append(others, r)
		}
	}
	if err := s.validate(others, m); err != nil {
		return nil, err
	}
	if err := s.repo.Update(ctx, m); err != nil {
		return nil, fmt.Errorf("remotepathmappings: update: %w", err)
	}
	s.invalidate()
	return m, nil
}

// Remove deletes a mapping by ID. Mirrors C# `:67-72`.
func (s *Service) Remove(ctx context.Context, id int) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("remotepathmappings: delete: %w", err)
	}
	s.invalidate()
	return nil
}

func (s *Service) invalidate() {
	s.mu.Lock()
	s.cache = nil
	s.loaded = false
	s.mu.Unlock()
}

func (s *Service) validate(existing []Mapping, m *Mapping) error {
	if strings.TrimSpace(m.Host) == "" {
		return ErrInvalidHost
	}
	if strings.HasPrefix(m.RemotePath, " ") {
		return fmt.Errorf("%w: must not start with a space", ErrInvalidRemotePath)
	}
	remote := ospath.New(m.RemotePath)
	local := ospath.New(m.LocalPath)
	if remote.IsEmpty() {
		return fmt.Errorf("%w: cannot be empty", ErrInvalidRemotePath)
	}
	if local.IsEmpty() || !local.IsRooted() {
		return ErrInvalidLocalPath
	}
	if !s.disk.FolderExists(local.FullPath()) {
		return fmt.Errorf("%w: %s", ErrLocalFolderNotFound, local.FullPath())
	}
	for _, r := range existing {
		if r.Host == m.Host && r.RemotePath == m.RemotePath {
			return ErrDuplicateMapping
		}
	}
	return nil
}

// RemapRemoteToLocal walks the cached mappings and substitutes the
// matching mapping's LocalPath prefix for its RemotePath prefix.
// Returns `remotePath` unchanged when no mapping matches or the path
// is empty. Mirrors `RemotePathMappingService.cs:128-157`.
//
// Host comparison is case-insensitive (matches the C#
// `StringComparison.InvariantCultureIgnoreCase`); the path-prefix
// match uses `OsPath.Contains` which already handles
// platform-appropriate case sensitivity.
func (s *Service) RemapRemoteToLocal(ctx context.Context, host string, remotePath ospath.OsPath) (ospath.OsPath, error) {
	if remotePath.IsEmpty() {
		return remotePath, nil
	}
	mappings, err := s.All(ctx)
	if err != nil {
		return remotePath, err
	}
	if len(mappings) == 0 {
		return remotePath, nil
	}
	for _, mapping := range mappings {
		mappingRemote := ospath.New(mapping.RemotePath)
		if strings.EqualFold(host, mapping.Host) && mappingRemote.Contains(remotePath) {
			localBase := ospath.New(mapping.LocalPath)
			rel := remotePath.Sub(mappingRemote)
			return localBase.Add(rel), nil
		}
	}
	return remotePath, nil
}

// RemapLocalToRemote is the inverse of [Service.RemapRemoteToLocal].
// Mirrors `RemotePathMappingService.cs:159-188`.
func (s *Service) RemapLocalToRemote(ctx context.Context, host string, localPath ospath.OsPath) (ospath.OsPath, error) {
	if localPath.IsEmpty() {
		return localPath, nil
	}
	mappings, err := s.All(ctx)
	if err != nil {
		return localPath, err
	}
	if len(mappings) == 0 {
		return localPath, nil
	}
	for _, mapping := range mappings {
		mappingLocal := ospath.New(mapping.LocalPath)
		if strings.EqualFold(host, mapping.Host) && mappingLocal.Contains(localPath) {
			remoteBase := ospath.New(mapping.RemotePath)
			rel := localPath.Sub(mappingLocal)
			return remoteBase.Add(rel), nil
		}
	}
	return localPath, nil
}
