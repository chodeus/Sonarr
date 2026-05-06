package healthcheck

import (
	"context"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"sonarr.tv/sonarr/internal/series"
)

// RootFolderCheckName is the canonical Source identifier.
const RootFolderCheckName = "RootFolderCheck"

// RootFolderSeriesPaths is the narrow series-side dependency the
// check needs — `seriesID → path` map. The Go port wraps the
// existing `series.Service.AllSeriesPaths(ctx)` method.
type RootFolderSeriesPaths interface {
	AllSeriesPaths(ctx context.Context) (map[int]string, error)
}

// RootFolderResolver maps a series filesystem path to its containing
// root-folder path. Wraps the existing
// `rootfolders.Service.GetBestRootFolderPath(ctx, path)` method.
//
// The C# tree calls `RootFolderService.GetBestRootFolderPath(s.Value)`
// for every series path; the Go port follows the same shape.
type RootFolderResolver interface {
	GetBestRootFolderPath(ctx context.Context, seriesPath string) (string, error)
}

// RootFolderDisk is the narrow disk surface the check needs.
// Implementations typically wrap a real disk provider (`os.Stat`,
// `os.ReadDir`).
type RootFolderDisk interface {
	FolderExists(path string) bool
	FolderEmpty(path string) bool
}

// RootFolderCheck warns / errors when a series root folder is
// missing from disk OR the root folder exists but contains no
// series. Mirrors
// `src/NzbDrone.Core/HealthCheck/Checks/RootFolderCheck.cs:1-104`.
//
// Three distinct outcomes:
//
//  1. One root folder missing → Error / ReasonRootFolderMissing.
//  2. Multiple root folders missing → Error / ReasonRootFolderMultipleMissing.
//  3. One or more root folders exist-but-empty → Warning / ReasonRootFolderEmpty.
//
// The check is dependency-light: it pulls every series path,
// resolves to the matching root folder, and probes disk.
//
// The C# version uses `IsPathValid(PathValidationType.CurrentOs)` to
// flag malformed paths before disk-probing. The Go port substitutes a
// simpler heuristic: empty or non-absolute paths fail validation.
// FolderExists handles the rest. A future slice can replace this with
// a richer per-OS validator once one is needed beyond this check.
type RootFolderCheck struct {
	seriesPaths RootFolderSeriesPaths
	resolver    RootFolderResolver
	disk        RootFolderDisk
}

// NewRootFolderCheck builds the check. All three dependencies are
// required. nil triggers a panic at construction (the C# tree
// dependency-injects via the IoC container; the Go port surfaces
// unconfigured wiring loudly).
func NewRootFolderCheck(seriesPaths RootFolderSeriesPaths, resolver RootFolderResolver, disk RootFolderDisk) *RootFolderCheck {
	if seriesPaths == nil || resolver == nil || disk == nil {
		panic("healthcheck: NewRootFolderCheck requires non-nil seriesPaths, resolver, and disk")
	}
	return &RootFolderCheck{seriesPaths: seriesPaths, resolver: resolver, disk: disk}
}

// Name returns the canonical check identifier.
func (*RootFolderCheck) Name() string { return RootFolderCheckName }

// CheckOnStartup defaults true.
func (*RootFolderCheck) CheckOnStartup() bool { return true }

// CheckOnSchedule defaults true.
func (*RootFolderCheck) CheckOnSchedule() bool { return true }

// EventTriggers declares the event-driven bindings.
//
// The C# tree decorates the class with four `[CheckOn]` attributes:
// SeriesDeletedEvent, SeriesMovedEvent, EpisodeImportedEvent
// (FailedOnly), EpisodeImportFailedEvent (SuccessfulOnly). Only
// SeriesDeletedEvent is currently ported in the Go tree; the
// remaining three bindings land alongside their event types
// (SeriesMovedEvent + EpisodeImported* are Phase 3 follow-ups
// inside the series / mediafiles subsystems).
func (*RootFolderCheck) EventTriggers() []EventTrigger {
	return []EventTrigger{
		{
			EventType: reflect.TypeOf(series.SeriesDeletedEvent{}),
			Condition: CheckOnAlways,
		},
	}
}

// Run performs the check. Mirrors C# `Check()` at `:31-102`:
//
//  1. Gather every series path; resolve each to its root folder.
//  2. Distinct → unique root-folder list.
//  3. Partition into missing (invalid OR FolderExists==false) and
//     present.
//  4. If any missing → return Error (single vs multi reason).
//  5. Else partition present into empty (FolderEmpty==true) and full.
//  6. If any empty → return Warning.
//  7. Otherwise OK.
//
// Errors from the underlying resolver are propagated as a check
// failure — the C# tree throws and the orchestrator's panic guard
// converts to "DecisionError"; the Go port returns a check-level
// Warning so the orchestrator records the failure without crashing.
func (c *RootFolderCheck) Run(ctx context.Context) HealthCheck {
	paths, err := c.seriesPaths.AllSeriesPaths(ctx)
	if err != nil {
		return New(
			RootFolderCheckName,
			ResultWarning,
			ReasonRootFolderMissing,
			"Unable to enumerate series paths: "+err.Error(),
			"#missing-root-folder",
		)
	}

	roots := make(map[string]struct{})
	for _, p := range paths {
		root, err := c.resolver.GetBestRootFolderPath(ctx, p)
		if err != nil {
			// A resolver error on a single path doesn't fail the check
			// outright — record it as a missing-root finding.
			roots[p] = struct{}{}
			continue
		}
		if root != "" {
			roots[root] = struct{}{}
		}
	}

	missing := make([]string, 0)
	present := make([]string, 0)
	for r := range roots {
		if !pathLooksValid(r) || !c.disk.FolderExists(r) {
			missing = append(missing, r)
		} else {
			present = append(present, r)
		}
	}
	sort.Strings(missing)
	sort.Strings(present)

	if len(missing) == 1 {
		return New(
			RootFolderCheckName,
			ResultError,
			ReasonRootFolderMissing,
			"Missing root folder: "+missing[0],
			"#missing-root-folder",
		)
	}
	if len(missing) > 1 {
		return New(
			RootFolderCheckName,
			ResultError,
			ReasonRootFolderMultipleMissing,
			"Multiple root folders are missing: "+strings.Join(missing, " | "),
			"#missing-root-folder",
		)
	}

	empty := make([]string, 0)
	for _, r := range present {
		if c.disk.FolderEmpty(r) {
			empty = append(empty, r)
		}
	}

	if len(empty) == 1 {
		return New(
			RootFolderCheckName,
			ResultWarning,
			ReasonRootFolderEmpty,
			"Root folder is empty: "+empty[0],
			"#empty-root-folder",
		)
	}
	if len(empty) > 1 {
		return New(
			RootFolderCheckName,
			ResultWarning,
			ReasonRootFolderEmpty,
			"Multiple root folders are empty: "+strings.Join(empty, " | "),
			"#empty-root-folder",
		)
	}

	return OK(RootFolderCheckName)
}

// pathLooksValid is the Go-port substitute for the C# `IsPathValid(
// PathValidationType.CurrentOs)` predicate. Returns false for empty
// or relative paths; true otherwise. Subsequent failure modes (path
// uses wrong OS separators on the current OS, contains forbidden
// characters, etc.) are caught by the FolderExists probe.
//
// The C# implementation lives in `Common/Disk/PathValidation.cs` and
// has a richer per-OS validator. The Go port can replace this stub
// when something beyond the existence check is needed.
func pathLooksValid(p string) bool {
	if strings.TrimSpace(p) == "" {
		return false
	}
	return filepath.IsAbs(p)
}
