package healthcheck

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// ImportListRootFolderCheckName is the canonical Source identifier.
const ImportListRootFolderCheckName = "ImportListRootFolderCheck"

// ImportListRootFolderPathLister returns the list of configured
// (system-wide) root-folder paths. Mirrors C#
// `_rootFolderService.All()` at
// `ImportListRootFolderCheck.cs:39`. The Go port returns just the
// Path strings — the check doesn't need the surrounding RootFolder
// metadata.
//
// Production wires this against rootfolders.Repo via an adapter
// in cmd/sonarr/healthcheck_wiring.go.
type ImportListRootFolderPathLister interface {
	AllRootFolderPaths(ctx context.Context) ([]string, error)
}

// ImportListFolderProbe wraps `disk.Provider.FolderExists`. Same
// shape as [RootFolderDisk] in root_folder.go but without the
// FolderEmpty probe (the import-list check doesn't care about
// empty-folder semantics — only existence).
type ImportListFolderProbe interface {
	FolderExists(path string) bool
}

// ImportListRootFolderCheck flags persisted import lists whose
// configured RootFolderPath no longer resolves to a valid /
// existing root folder. Mirrors
// `src/NzbDrone.Core/HealthCheck/Checks/ImportListRootFolderCheck.cs:22-96`.
//
// Two outcomes:
//
//  1. Every import-list's RootFolderPath matches a known root and
//     exists on disk → OK.
//  2. One or more import-lists point at missing root folders →
//     Error / [ReasonImportListRootFolderMissing] (single) or
//     [ReasonImportListRootFolderMultipleMissing] (multiple).
//
// The C# code uses Error for both single and multiple missing
// cases (not Warning). The Go port mirrors exactly. Per-root group
// of import-list names is surfaced in the message: each missing
// root renders as "path (List1, List2)"; multiple missing roots
// join with " | ".
//
// A root is "missing" when ANY of the following hold (matches C#
// `:54-57`):
//
//   - RootFolderPath is empty or whitespace.
//   - RootFolderPath doesn't appear in the system's configured
//     RootFolders.
//   - RootFolderPath fails the FolderExists probe.
//
// Slice 4.AQ (closes queue [I5] + Phase 4 focused-subset 4).
type ImportListRootFolderCheck struct {
	lists AllImportListLister
	roots ImportListRootFolderPathLister
	disk  ImportListFolderProbe
}

// NewImportListRootFolderCheck builds the check. lists + roots +
// disk MUST be non-nil.
func NewImportListRootFolderCheck(lists AllImportListLister, roots ImportListRootFolderPathLister, disk ImportListFolderProbe) (*ImportListRootFolderCheck, error) {
	if lists == nil {
		return nil, fmt.Errorf("healthcheck: NewImportListRootFolderCheck: nil AllImportListLister")
	}
	if roots == nil {
		return nil, fmt.Errorf("healthcheck: NewImportListRootFolderCheck: nil ImportListRootFolderPathLister")
	}
	if disk == nil {
		return nil, fmt.Errorf("healthcheck: NewImportListRootFolderCheck: nil ImportListFolderProbe")
	}
	return &ImportListRootFolderCheck{lists: lists, roots: roots, disk: disk}, nil
}

// Name returns the canonical check identifier.
func (*ImportListRootFolderCheck) Name() string { return ImportListRootFolderCheckName }

// CheckOnStartup defaults true.
func (*ImportListRootFolderCheck) CheckOnStartup() bool { return true }

// CheckOnSchedule defaults true.
func (*ImportListRootFolderCheck) CheckOnSchedule() bool { return true }

// EventTriggers ports the C# `[CheckOn(...)]` attributes at
// `ImportListRootFolderCheck.cs:15-21` — ProviderUpdated /
// ProviderDeleted on IImportList, ModelEvent<RootFolder>,
// SeriesDeletedEvent, SeriesMovedEvent, EpisodeImportedEvent
// (FailedOnly), EpisodeImportFailedEvent (SuccessfulOnly). Per
// the precedent in [RootFolderCheck.EventTriggers] +
// [NotificationStatusCheck.EventTriggers] the Go port ships an
// empty triggers slice; the bindings land alongside the broader
// healthcheck event-trigger mechanism (whichever ports first wins).
func (*ImportListRootFolderCheck) EventTriggers() []EventTrigger { return nil }

// Run mirrors the C# `Check()` method at `:36-90`. Errors from
// either underlying lookup surface as Warning rather than crashing.
func (c *ImportListRootFolderCheck) Run(ctx context.Context) HealthCheck {
	lists, err := c.lists.AllImportLists(ctx)
	if err != nil {
		return New(
			ImportListRootFolderCheckName,
			ResultWarning,
			ReasonImportListRootFolderMissing,
			"Unable to enumerate import lists: "+err.Error(),
			"#import-list-missing-root-folder",
		)
	}
	if len(lists) == 0 {
		return OK(ImportListRootFolderCheckName)
	}

	rootPaths, err := c.roots.AllRootFolderPaths(ctx)
	if err != nil {
		return New(
			ImportListRootFolderCheckName,
			ResultWarning,
			ReasonImportListRootFolderMissing,
			"Unable to enumerate root folders: "+err.Error(),
			"#import-list-missing-root-folder",
		)
	}
	rootSet := make(map[string]struct{}, len(rootPaths))
	for _, p := range rootPaths {
		rootSet[normalizeRoot(p)] = struct{}{}
	}

	// missingOrder preserves first-seen order so the rendered
	// message is deterministic across runs. The C# tree uses a
	// Dictionary which gives insertion-order on the .NET 6 runtime;
	// the Go port pins this explicitly via a parallel slice.
	missingByPath := make(map[string][]string)
	missingOrder := make([]string, 0)

	for _, list := range lists {
		path := list.RootFolderPath
		if _, seen := missingByPath[path]; seen {
			missingByPath[path] = append(missingByPath[path], list.Name)
			continue
		}

		if rootMissing(path, rootSet, c.disk) {
			missingByPath[path] = []string{list.Name}
			missingOrder = append(missingOrder, path)
		}
	}

	if len(missingOrder) == 0 {
		return OK(ImportListRootFolderCheckName)
	}

	if len(missingOrder) == 1 {
		root := missingOrder[0]
		names := append([]string(nil), missingByPath[root]...)
		sort.SliceStable(names, func(i, j int) bool {
			return strings.ToLower(names[i]) < strings.ToLower(names[j])
		})
		return New(
			ImportListRootFolderCheckName,
			ResultError,
			ReasonImportListRootFolderMissing,
			"Missing root folder for import list: "+formatMissingRoot(root, names),
			"#import-list-missing-root-folder",
		)
	}

	parts := make([]string, 0, len(missingOrder))
	for _, root := range missingOrder {
		// Per-root list-names sort case-insensitively so multi-list
		// renders are stable.
		names := append([]string(nil), missingByPath[root]...)
		sort.SliceStable(names, func(i, j int) bool {
			return strings.ToLower(names[i]) < strings.ToLower(names[j])
		})
		parts = append(parts, formatMissingRoot(root, names))
	}
	return New(
		ImportListRootFolderCheckName,
		ResultError,
		ReasonImportListRootFolderMultipleMissing,
		"Multiple import list root folders are missing: "+strings.Join(parts, " | "),
		"#import-list-missing-root-folder",
	)
}

// rootMissing implements the C# missing-root predicate at `:54-57`:
//
//   - empty/whitespace path → missing.
//   - path not in the configured root-folders set → missing.
//   - path fails FolderExists → missing.
//
// The C# `IsPathValid(PathValidationType.CurrentOs)` check at `:55`
// is omitted here — the Go port relies on FolderExists to catch any
// path that's malformed for the current OS, matching the same
// substitution the existing [RootFolderCheck] makes (see
// pathLooksValid in root_folder.go for context).
func rootMissing(path string, configuredRoots map[string]struct{}, disk ImportListFolderProbe) bool {
	if strings.TrimSpace(path) == "" {
		return true
	}
	if _, ok := configuredRoots[normalizeRoot(path)]; !ok {
		return true
	}
	if !disk.FolderExists(path) {
		return true
	}
	return false
}

// normalizeRoot lower-cases + trims trailing slashes so the C#
// `PathEquals` semantics (case-insensitive on Windows + macOS, case-
// sensitive on Linux — but the C# call-site at `:56` uses
// `PathEquals` which IS case-insensitive across platforms via
// `OsInfo.PathStringComparison`).
//
// The Go port goes case-insensitive on every platform — matches C#
// behaviour on Windows + macOS; mismatches Linux. The deliberate
// divergence is documented here so a future slice can promote it
// to a per-platform helper if Linux users report false positives.
func normalizeRoot(p string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(p), "/\\"))
}

// formatMissingRoot ports C# `FormatRootFolder` at `:92-95`.
// Returns "path (List1, List2)" with the list names already
// case-insensitive-sorted by the caller.
func formatMissingRoot(rootPath string, listNames []string) string {
	return rootPath + " (" + strings.Join(listNames, ", ") + ")"
}
