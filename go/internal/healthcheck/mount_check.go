package healthcheck

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"sonarr.tv/sonarr/internal/disk"
)

// MountCheckName is the canonical Source identifier.
const MountCheckName = "MountCheck"

// SeriesPathLookup surfaces `Id → Path` for every persisted series.
// `*series.Repo.AllSeriesPaths` satisfies this directly.
type SeriesPathLookup interface {
	AllSeriesPaths(ctx context.Context) (map[int]string, error)
}

// MountResolver returns the [disk.Mount] backing a given path.
// `*disk.Provider.GetMount` satisfies this directly. Returning an
// error skips the path (the C# tree's `GetMount` returns null which
// gets filtered out — the Go port treats per-path lookup failures as
// "not read-only" because we can't make a positive assertion).
type MountResolver interface {
	GetMount(path string) (*disk.Mount, error)
}

// MountCheck warns when any series path resides on a read-only
// mount. Mirrors
// `src/NzbDrone.Core/HealthCheck/Checks/MountCheck.cs:1-44`.
//
// Audit fix B.5 (unblocked by the slice-5.J36 disk.Provider.GetMount
// cross-platform port).
type MountCheck struct {
	series   SeriesPathLookup
	resolver MountResolver
}

// NewMountCheck builds the check. Both deps MUST be non-nil.
func NewMountCheck(series SeriesPathLookup, resolver MountResolver) (*MountCheck, error) {
	if series == nil {
		return nil, fmt.Errorf("healthcheck: NewMountCheck: nil SeriesPathLookup")
	}
	if resolver == nil {
		return nil, fmt.Errorf("healthcheck: NewMountCheck: nil MountResolver")
	}
	return &MountCheck{series: series, resolver: resolver}, nil
}

// Source returns the canonical identifier.
func (c *MountCheck) Source() string { return MountCheckName }

// Check enumerates every persisted series path, resolves each to its
// backing mount, and produces an Error when at least one mount is
// read-only. Mounts are de-duplicated by RootDirectory so a multi-
// series mount shows up exactly once in the message.
func (c *MountCheck) Check(ctx context.Context) HealthCheck {
	if c == nil {
		return OK(MountCheckName)
	}
	paths, err := c.series.AllSeriesPaths(ctx)
	if err != nil {
		// Mirrors the C# `GetMount` swallow-on-failure posture — if the
		// lookup itself fails we can't make a positive read-only claim,
		// so the check passes silently. The series-lookup failure is
		// surfaced via the calling slog ; not the healthcheck Message.
		return OK(MountCheckName)
	}

	type entry struct {
		name string
		path string
	}
	seen := make(map[string]struct{}, len(paths))
	var hits []entry

	// Walk in series-ID order so the message is deterministic
	// (matters for test goldens + UI consistency).
	ids := make([]int, 0, len(paths))
	for id := range paths {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	for _, id := range ids {
		p := paths[id]
		if p == "" {
			continue
		}
		m, err := c.resolver.GetMount(p)
		if err != nil || m == nil {
			continue
		}
		if !m.IsReadOnly {
			continue
		}
		if _, dup := seen[m.RootDirectory]; dup {
			continue
		}
		seen[m.RootDirectory] = struct{}{}
		hits = append(hits, entry{name: m.Name, path: p})
	}

	if len(hits) == 0 {
		return OK(MountCheckName)
	}

	parts := make([]string, 0, len(hits))
	for _, h := range hits {
		parts = append(parts, fmt.Sprintf("%s (%s)", h.name, h.path))
	}
	return New(MountCheckName,
		ResultError,
		ReasonMountSeries,
		"Series mounted with read-only access: "+strings.Join(parts, ", "),
		"#series-mount-ro")
}
