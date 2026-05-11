package healthcheck

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"sonarr.tv/sonarr/internal/notificationstatus"
)

// NotificationStatusCheckName is the canonical Source identifier.
const NotificationStatusCheckName = "NotificationStatusCheck"

// EnabledNotification is the narrow projection of the
// NotificationDefinition row the check joins against. Mirrors the
// EnabledIndexer / DownloadClientStatusCheck precedents — keeps the
// healthcheck package from importing the heavier
// internal/notifications surface (Provider interface, Settings,
// Registry, etc.).
type EnabledNotification struct {
	// ID is the Notifications.Id used as the join key against
	// `notificationstatus.Status.ProviderID`.
	ID int
	// Name is the user-facing label surfaced in the some-down
	// Warning message (`notificationNames`).
	Name string
}

// EnabledNotificationLister returns every notification provider the
// user has enabled — i.e. the row's [NotificationDefinition.Enable]
// computed field is true (any On* flag set). Mirrors the C# pattern
// at `NotificationStatusCheck.cs:27` where
// `_providerFactory.GetAvailableProviders()` returns the active
// subset (the [notifications.Factory] base-class `Active()` filter
// keeps only rows with `Enable == true`).
//
// Production wires this against `*notifications.Factory` — the
// host adapter pulls the All-rows list, filters to enabled, and
// projects to EnabledNotification. The factory's per-event
// accessors (OnGrabEnabled etc.) are NOT used here because the
// check considers a provider "enabled" iff ANY OnX flag is true,
// not per-event.
type EnabledNotificationLister interface {
	EnabledNotifications(ctx context.Context) ([]EnabledNotification, error)
}

// NotificationStatusBlockedLister wraps the
// `*notificationstatus.Service.GetBlockedProviders(ctx)` surface.
// Same shape as [IndexerStatusBlockedLister] /
// [DownloadClientStatusBlockedLister] — kept per-package so the
// healthcheck doesn't import the wider notificationstatus surface
// beyond the Status row + GetBlockedProviders.
type NotificationStatusBlockedLister interface {
	GetBlockedProviders(ctx context.Context) ([]notificationstatus.Status, error)
}

// NotificationStatusCheck warns when one or more enabled
// notification providers are inside their back-off window. Mirrors
// `src/NzbDrone.Core/HealthCheck/Checks/NotificationStatusCheck.cs:8-58`.
//
// Three outcomes:
//
//  1. Every enabled notification is healthy (or no enabled
//     notifications exist) → OK.
//  2. SOME enabled notifications are blocked → Warning /
//     [ReasonNotificationStatusSingle] with comma-separated names.
//  3. ALL enabled notifications are blocked → Error /
//     [ReasonNotificationStatusAll].
//
// Differs from [IndexerStatusCheck] in one shape detail: the C#
// IndexerStatusCheck filters blocked rows by a 6-hour recency
// window (only "recently failed" indexers contribute); the C#
// NotificationStatusCheck has NO recency filter — every
// currently-blocked notification contributes. The Go port mirrors
// the C# behaviour exactly. Slice 4.AI (closes queue [N5]).
type NotificationStatusCheck struct {
	notifications EnabledNotificationLister
	statuses      NotificationStatusBlockedLister
}

// NewNotificationStatusCheck builds the check. notifications +
// statuses MUST be non-nil.
func NewNotificationStatusCheck(notifications EnabledNotificationLister, statuses NotificationStatusBlockedLister) (*NotificationStatusCheck, error) {
	if notifications == nil {
		return nil, fmt.Errorf("healthcheck: NewNotificationStatusCheck: nil EnabledNotificationLister")
	}
	if statuses == nil {
		return nil, fmt.Errorf("healthcheck: NewNotificationStatusCheck: nil NotificationStatusBlockedLister")
	}
	return &NotificationStatusCheck{notifications: notifications, statuses: statuses}, nil
}

// Name returns the canonical check identifier.
func (*NotificationStatusCheck) Name() string { return NotificationStatusCheckName }

// CheckOnStartup defaults true.
func (*NotificationStatusCheck) CheckOnStartup() bool { return true }

// CheckOnSchedule defaults true.
func (*NotificationStatusCheck) CheckOnSchedule() bool { return true }

// EventTriggers wires the C# `[CheckOn(...)]` attributes at
// `NotificationStatusCheck.cs:10-12` — ProviderUpdated /
// ProviderDeleted / ProviderStatusChanged events on
// `INotification`. The Go-port equivalent event type
// (notificationstatus.StatusChangedEvent) is published by
// notificationstatus.Service after each Record* mutation; the
// healthcheck event-trigger plumbing wires that up at boot
// alongside the existing IndexerStatusCheck precedent
// (currently itself empty — see IndexerStatusCheck.EventTriggers
// at internal/healthcheck/indexer_status.go:108-115). For
// consistency this slice ships an empty triggers slice as well;
// the bindings land alongside the indexer-status binding once a
// shared mechanism is decided.
func (*NotificationStatusCheck) EventTriggers() []EventTrigger { return nil }

// Run mirrors the C# `Check()` method at `:25-56`.
//
// Errors from either underlying lookup surface as Warning rather
// than crashing — same precedent as [IndexerStatusCheck.Run].
func (c *NotificationStatusCheck) Run(ctx context.Context) HealthCheck {
	enabled, err := c.notifications.EnabledNotifications(ctx)
	if err != nil {
		return New(
			NotificationStatusCheckName,
			ResultWarning,
			ReasonNotificationStatusSingle,
			"Unable to enumerate notifications: "+err.Error(),
			"#notifications-are-unavailable-due-to-failures",
		)
	}
	if len(enabled) == 0 {
		// No enabled notifications → vacuously OK. Mirrors the C#
		// `enabledProviders` empty case which produces an empty
		// join + Empty() short-circuit at `:34`.
		return OK(NotificationStatusCheckName)
	}

	blocked, err := c.statuses.GetBlockedProviders(ctx)
	if err != nil {
		return New(
			NotificationStatusCheckName,
			ResultWarning,
			ReasonNotificationStatusSingle,
			"Unable to enumerate notification statuses: "+err.Error(),
			"#notifications-are-unavailable-due-to-failures",
		)
	}

	blockedByID := make(map[int]bool, len(blocked))
	for _, s := range blocked {
		blockedByID[s.ProviderID] = true
	}

	var down []EnabledNotification
	for _, n := range enabled {
		if blockedByID[n.ID] {
			down = append(down, n)
		}
	}
	if len(down) == 0 {
		return OK(NotificationStatusCheckName)
	}
	if len(down) == len(enabled) {
		return New(
			NotificationStatusCheckName,
			ResultError,
			ReasonNotificationStatusAll,
			"All notifications are unavailable due to failures",
			"#notifications-are-unavailable-due-to-failures",
		)
	}

	// Sort by Name (case-insensitive) for stable rendering across
	// runs. Same precedent as IndexerStatusCheck — the C# tree
	// relies on the LINQ Join order; the Go port pins it
	// explicitly.
	sort.Slice(down, func(i, j int) bool {
		return strings.ToLower(down[i].Name) < strings.ToLower(down[j].Name)
	})
	names := make([]string, 0, len(down))
	for _, n := range down {
		names = append(names, n.Name)
	}
	return New(
		NotificationStatusCheckName,
		ResultWarning,
		ReasonNotificationStatusSingle,
		"Notifications unavailable due to failures: "+strings.Join(names, ", "),
		"#notifications-are-unavailable-due-to-failures",
	)
}
