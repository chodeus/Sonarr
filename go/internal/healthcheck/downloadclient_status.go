package healthcheck

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"sonarr.tv/sonarr/internal/downloadclientstatus"
)

// DownloadClientStatusCheckName is the canonical Source identifier.
const DownloadClientStatusCheckName = "DownloadClientStatusCheck"

// EnabledDownloadClient is the narrow projection of the
// DownloadClientDefinition row the check joins against — id + display
// name. Mirrors the [EnabledIndexer] / [EnabledNotification]
// precedents, keeping the healthcheck package off the heavier
// internal/downloadclients provider surface.
type EnabledDownloadClient struct {
	// ID is the DownloadClients.Id used as the join key against
	// `downloadclientstatus.Status.ProviderID`.
	ID int
	// Name is the user-facing label surfaced in the some-down
	// Warning message (`downloadClientNames`).
	Name string
}

// EnabledDownloadClientStatusLister returns every download client the
// user has enabled (the row's `Enable` column is true). Mirrors the C#
// `DownloadClientStatusCheck.cs:28` —
// `_providerFactory.GetAvailableProviders()` returns the enabled
// subset; the Go port flattens to (ID, Name) since the healthcheck
// needs nothing else. Production wires this against the download-client
// repo, projecting enabled rows.
type EnabledDownloadClientStatusLister interface {
	EnabledDownloadClientsForStatus(ctx context.Context) ([]EnabledDownloadClient, error)
}

// DownloadClientStatusBlockedLister wraps the
// `*downloadclientstatus.Service.GetBlockedProviders(ctx)` surface —
// the subset of status rows still inside their backoff window. Same
// shape as [IndexerStatusBlockedLister] / [NotificationStatusBlockedLister].
type DownloadClientStatusBlockedLister interface {
	GetBlockedProviders(ctx context.Context) ([]downloadclientstatus.Status, error)
}

// DownloadClientStatusCheck warns when one or more enabled download
// clients are inside their back-off window. Mirrors
// `src/NzbDrone.Core/HealthCheck/Checks/DownloadClientStatusCheck.cs:13-66`.
//
// Three outcomes:
//
//  1. Every enabled download client is healthy (or no enabled clients
//     exist) → OK.
//  2. SOME enabled clients are blocked → Warning /
//     [ReasonDownloadClientStatusSingleClient] with comma-separated names.
//  3. ALL enabled clients are blocked → Error /
//     [ReasonDownloadClientStatusAllClients].
//
// Like [NotificationStatusCheck] (and unlike [IndexerStatusCheck]),
// there is NO recency window — every currently-blocked client
// contributes. The Go port mirrors the C# behaviour exactly.
type DownloadClientStatusCheck struct {
	clients  EnabledDownloadClientStatusLister
	statuses DownloadClientStatusBlockedLister
}

// NewDownloadClientStatusCheck builds the check. clients + statuses
// MUST be non-nil.
func NewDownloadClientStatusCheck(clients EnabledDownloadClientStatusLister, statuses DownloadClientStatusBlockedLister) (*DownloadClientStatusCheck, error) {
	if clients == nil {
		return nil, fmt.Errorf("healthcheck: NewDownloadClientStatusCheck: nil EnabledDownloadClientStatusLister")
	}
	if statuses == nil {
		return nil, fmt.Errorf("healthcheck: NewDownloadClientStatusCheck: nil DownloadClientStatusBlockedLister")
	}
	return &DownloadClientStatusCheck{clients: clients, statuses: statuses}, nil
}

// Name returns the canonical check identifier.
func (*DownloadClientStatusCheck) Name() string { return DownloadClientStatusCheckName }

// CheckOnStartup defaults true.
func (*DownloadClientStatusCheck) CheckOnStartup() bool { return true }

// CheckOnSchedule defaults true.
func (*DownloadClientStatusCheck) CheckOnSchedule() bool { return true }

// EventTriggers wires the C# `[CheckOn(...)]` attributes at
// `DownloadClientStatusCheck.cs:10-12` (ProviderUpdated /
// ProviderDeleted / ProviderStatusChanged on IDownloadClient). The
// Go-port equivalent event bindings land alongside the IndexerStatus /
// NotificationStatus precedents (both currently empty), so this slice
// ships an empty triggers slice for a uniform registration shape.
func (*DownloadClientStatusCheck) EventTriggers() []EventTrigger { return nil }

// Run mirrors the C# `Check()` method at `:29-65`.
//
// Errors from either underlying lookup surface as Warning rather than
// crashing — same precedent as [NotificationStatusCheck.Run].
func (c *DownloadClientStatusCheck) Run(ctx context.Context) HealthCheck {
	enabled, err := c.clients.EnabledDownloadClientsForStatus(ctx)
	if err != nil {
		return New(
			DownloadClientStatusCheckName,
			ResultWarning,
			ReasonDownloadClientStatusSingleClient,
			"Unable to enumerate download clients: "+err.Error(),
			"#download-clients-are-unavailable-due-to-failures",
		)
	}
	if len(enabled) == 0 {
		// No enabled download clients → vacuously OK. Mirrors the C#
		// `enabledProviders` empty case which produces an empty join +
		// Empty() short-circuit at `:38`.
		return OK(DownloadClientStatusCheckName)
	}

	blocked, err := c.statuses.GetBlockedProviders(ctx)
	if err != nil {
		return New(
			DownloadClientStatusCheckName,
			ResultWarning,
			ReasonDownloadClientStatusSingleClient,
			"Unable to enumerate download client statuses: "+err.Error(),
			"#download-clients-are-unavailable-due-to-failures",
		)
	}

	blockedByID := make(map[int]bool, len(blocked))
	for _, s := range blocked {
		blockedByID[s.ProviderID] = true
	}

	var down []EnabledDownloadClient
	for _, dc := range enabled {
		if blockedByID[dc.ID] {
			down = append(down, dc)
		}
	}
	if len(down) == 0 {
		return OK(DownloadClientStatusCheckName)
	}
	if len(down) == len(enabled) {
		return New(
			DownloadClientStatusCheckName,
			ResultError,
			ReasonDownloadClientStatusAllClients,
			"All download clients are unavailable due to failures",
			"#download-clients-are-unavailable-due-to-failures",
		)
	}

	// Sort by Name (case-insensitive) for stable rendering — same
	// precedent as IndexerStatusCheck / NotificationStatusCheck (the C#
	// tree relies on LINQ Join order; the Go port pins it explicitly).
	sort.Slice(down, func(i, j int) bool {
		return strings.ToLower(down[i].Name) < strings.ToLower(down[j].Name)
	})
	names := make([]string, 0, len(down))
	for _, dc := range down {
		names = append(names, dc.Name)
	}
	return New(
		DownloadClientStatusCheckName,
		ResultWarning,
		ReasonDownloadClientStatusSingleClient,
		"Download clients unavailable due to failures: "+strings.Join(names, ", "),
		"#download-clients-are-unavailable-due-to-failures",
	)
}
