package healthcheck

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"sonarr.tv/sonarr/internal/importliststatus"
)

// ImportListStatusCheckName is the canonical Source identifier.
const ImportListStatusCheckName = "ImportListStatusCheck"

// EnabledImportList is the narrow projection of the
// ImportListDefinition row the check joins against. Pattern-mirror
// of [EnabledNotification] / EnabledIndexer — keeps the healthcheck
// package from importing the heavier internal/importlists surface
// (Provider interface, Settings, Registry, Factory etc.).
//
// RootFolderPath is included so [ImportListRootFolderCheck] can
// reuse the same projection — both checks need the (ID, Name)
// pair and the root-folder check additionally needs the path.
type EnabledImportList struct {
	// ID is the ImportLists.Id used as the join key against
	// `importliststatus.Status.ProviderID`.
	ID int
	// Name is the user-facing label surfaced in the some-down
	// Warning message (`importListNames`).
	Name string
	// RootFolderPath is the persisted import-list root folder.
	// Consumed by [ImportListRootFolderCheck]; the status check
	// ignores it.
	RootFolderPath string
}

// EnabledImportListLister returns every import-list the user has
// auto-add-enabled — i.e. the row's [ImportListDefinition.Enable]
// computed field is true (EnableAutomaticAdd=true). Mirrors the
// C# pattern at `ImportListStatusCheck.cs:27` where
// `_providerFactory.GetAvailableProviders()` returns the active
// subset (the [importlists.Factory] base-class `Active()` filter
// keeps only rows with `Enable == true`).
//
// Production wires this against the importlists.Repo via an
// adapter in cmd/sonarr/healthcheck_wiring.go that applies the
// EnableAutomaticAdd predicate at the boundary.
type EnabledImportListLister interface {
	EnabledImportLists(ctx context.Context) ([]EnabledImportList, error)
}

// AllImportListLister returns EVERY import-list (including
// auto-add-disabled). Mirrors the C# `_importListFactory.All()`
// call-site at `ImportListRootFolderCheck.cs:38` which iterates
// every persisted row regardless of EnableAutomaticAdd. Consumed
// by [ImportListRootFolderCheck].
type AllImportListLister interface {
	AllImportLists(ctx context.Context) ([]EnabledImportList, error)
}

// ImportListStatusBlockedLister wraps the
// `*importliststatus.Service.GetBlockedProviders(ctx)` surface.
// Same shape as [IndexerStatusBlockedLister] /
// [NotificationStatusBlockedLister] — kept per-package so the
// healthcheck doesn't import the wider importliststatus surface
// beyond the Status row + GetBlockedProviders.
type ImportListStatusBlockedLister interface {
	GetBlockedProviders(ctx context.Context) ([]importliststatus.Status, error)
}

// ImportListStatusCheck warns when one or more enabled
// import-list providers are inside their back-off window. Mirrors
// `src/NzbDrone.Core/HealthCheck/Checks/ImportListStatusCheck.cs:13-57`.
//
// Three outcomes:
//
//  1. Every enabled import-list is healthy (or no enabled import-
//     lists exist) → OK.
//  2. SOME enabled import-lists are blocked → Warning /
//     [ReasonImportListStatusUnavailable] with comma-separated
//     names.
//  3. ALL enabled import-lists are blocked → Error /
//     [ReasonImportListStatusAllUnavailable].
//
// Pattern-mirror of [NotificationStatusCheck]. Like that check
// (and unlike [IndexerStatusCheck]), the C# code has NO 6-hour
// recency window — every currently-blocked import-list contributes
// regardless of how long ago the failure happened. The Go port
// mirrors the C# behaviour exactly.
//
// Slice 4.AQ (closes queue [I5] + Phase 4 focused-subset 4).
type ImportListStatusCheck struct {
	lists    EnabledImportListLister
	statuses ImportListStatusBlockedLister
}

// NewImportListStatusCheck builds the check. lists + statuses
// MUST be non-nil.
func NewImportListStatusCheck(lists EnabledImportListLister, statuses ImportListStatusBlockedLister) (*ImportListStatusCheck, error) {
	if lists == nil {
		return nil, fmt.Errorf("healthcheck: NewImportListStatusCheck: nil EnabledImportListLister")
	}
	if statuses == nil {
		return nil, fmt.Errorf("healthcheck: NewImportListStatusCheck: nil ImportListStatusBlockedLister")
	}
	return &ImportListStatusCheck{lists: lists, statuses: statuses}, nil
}

// Name returns the canonical check identifier.
func (*ImportListStatusCheck) Name() string { return ImportListStatusCheckName }

// CheckOnStartup defaults true.
func (*ImportListStatusCheck) CheckOnStartup() bool { return true }

// CheckOnSchedule defaults true.
func (*ImportListStatusCheck) CheckOnSchedule() bool { return true }

// EventTriggers wires the C# `[CheckOn(...)]` attributes at
// `ImportListStatusCheck.cs:10-12` — ProviderUpdated /
// ProviderDeleted / ProviderStatusChanged events on
// `IImportList`. The Go-port equivalent event type
// (importliststatus.StatusChangedEvent) is published by
// importliststatus.Service after each Record* mutation; the
// healthcheck event-trigger plumbing wires that up at boot
// alongside the existing IndexerStatusCheck +
// NotificationStatusCheck precedents (both currently empty —
// see those checks' EventTriggers). For consistency this slice
// ships an empty triggers slice; the bindings land alongside the
// indexer-status + notification-status bindings once a shared
// mechanism is decided.
func (*ImportListStatusCheck) EventTriggers() []EventTrigger { return nil }

// Run mirrors the C# `Check()` method at `:25-56`.
//
// Errors from either underlying lookup surface as Warning rather
// than crashing — same precedent as
// [NotificationStatusCheck.Run] / [IndexerStatusCheck.Run].
func (c *ImportListStatusCheck) Run(ctx context.Context) HealthCheck {
	enabled, err := c.lists.EnabledImportLists(ctx)
	if err != nil {
		return New(
			ImportListStatusCheckName,
			ResultWarning,
			ReasonImportListStatusUnavailable,
			"Unable to enumerate import lists: "+err.Error(),
			"#import-lists-are-unavailable-due-to-failures",
		)
	}
	if len(enabled) == 0 {
		// No enabled import lists → vacuously OK. Mirrors the C#
		// `enabledProviders` empty case which produces an empty
		// join + Empty() short-circuit at `:34`.
		return OK(ImportListStatusCheckName)
	}

	blocked, err := c.statuses.GetBlockedProviders(ctx)
	if err != nil {
		return New(
			ImportListStatusCheckName,
			ResultWarning,
			ReasonImportListStatusUnavailable,
			"Unable to enumerate import list statuses: "+err.Error(),
			"#import-lists-are-unavailable-due-to-failures",
		)
	}

	blockedByID := make(map[int]bool, len(blocked))
	for _, s := range blocked {
		blockedByID[s.ProviderID] = true
	}

	var down []EnabledImportList
	for _, l := range enabled {
		if blockedByID[l.ID] {
			down = append(down, l)
		}
	}
	if len(down) == 0 {
		return OK(ImportListStatusCheckName)
	}
	if len(down) == len(enabled) {
		return New(
			ImportListStatusCheckName,
			ResultError,
			ReasonImportListStatusAllUnavailable,
			"All import lists are unavailable due to failures",
			"#import-lists-are-unavailable-due-to-failures",
		)
	}

	// Sort by Name (case-insensitive) for stable rendering across
	// runs. Same precedent as IndexerStatusCheck +
	// NotificationStatusCheck — the C# tree relies on the LINQ
	// Join order; the Go port pins it explicitly.
	sort.Slice(down, func(i, j int) bool {
		return strings.ToLower(down[i].Name) < strings.ToLower(down[j].Name)
	})
	names := make([]string, 0, len(down))
	for _, l := range down {
		names = append(names, l.Name)
	}
	return New(
		ImportListStatusCheckName,
		ResultWarning,
		ReasonImportListStatusUnavailable,
		"Import lists unavailable due to failures: "+strings.Join(names, ", "),
		"#import-lists-are-unavailable-due-to-failures",
	)
}
