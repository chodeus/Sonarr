package healthcheck

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"strings"

	"sonarr.tv/sonarr/internal/configfile"
)

// DownloadClientSortingCheckName is the canonical Source identifier.
const DownloadClientSortingCheckName = "DownloadClientSortingCheck"

// DownloadClientSortingCheck warns when any enabled download client
// reports a non-empty `SortingMode` — Sonarr's import pipeline
// assumes the client leaves the completed download folder
// untouched, so a sorting/move feature on the client side will
// break category-aware imports. Mirrors
// `src/NzbDrone.Core/HealthCheck/Checks/DownloadClientSortingCheck.cs:1-71`.
//
// The C# tree iterates every enabled client and short-circuits on
// the FIRST client that reports a non-empty SortingMode. The Go
// port keeps the same semantic — one offender is enough to fail
// the check; the message names that client + its mode string.
//
// Per-client construction failures are logged + skipped (matches
// the C# `catch (DownloadClientException) { _logger.Debug(...) }`
// arm). Total-enumeration failures surface as Warning.
type DownloadClientSortingCheck struct {
	clients DownloadClientStatusEnumerator
	log     *slog.Logger
}

// NewDownloadClientSortingCheck builds the check. clients MUST be
// non-nil. logger defaults to slog.Default() when nil.
func NewDownloadClientSortingCheck(clients DownloadClientStatusEnumerator, log *slog.Logger) (*DownloadClientSortingCheck, error) {
	if clients == nil {
		return nil, fmt.Errorf("healthcheck: NewDownloadClientSortingCheck: nil DownloadClientStatusEnumerator")
	}
	if log == nil {
		log = slog.Default()
	}
	return &DownloadClientSortingCheck{clients: clients, log: log}, nil
}

// Name returns the canonical check identifier.
func (*DownloadClientSortingCheck) Name() string { return DownloadClientSortingCheckName }

// CheckOnStartup defaults true.
func (*DownloadClientSortingCheck) CheckOnStartup() bool { return true }

// CheckOnSchedule defaults true.
func (*DownloadClientSortingCheck) CheckOnSchedule() bool { return true }

// EventTriggers mirrors the four C# `[CheckOn]` attributes at
// `DownloadClientSortingCheck.cs:15-18` (DC + RootFolder +
// RemotePathMapping events). The C# provider-lifecycle event types
// aren't ported yet; the closest available proxy is the config-saved
// event from the v3 admin write path.
func (*DownloadClientSortingCheck) EventTriggers() []EventTrigger {
	return []EventTrigger{
		{EventType: reflect.TypeOf(configfile.ConfigFileSavedEvent{})},
	}
}

// Run mirrors the C# `Check()` method at `:34-69`.
func (c *DownloadClientSortingCheck) Run(ctx context.Context) HealthCheck {
	clients, err := c.clients.EnabledDownloadClients(ctx)
	if err != nil {
		return New(
			DownloadClientSortingCheckName,
			ResultWarning,
			ReasonDownloadClientSorting,
			"Unable to enumerate download clients: "+err.Error(),
			"#download-folder-and-library-folder-not-different-folders",
		)
	}
	for _, dc := range clients {
		if dc.Err != nil {
			c.log.Debug("downloadClient sorting check: skipping client",
				slog.String("client", dc.Name),
				slog.Any("err", dc.Err),
			)
			continue
		}
		if dc.Status == nil {
			continue
		}
		mode := strings.TrimSpace(dc.Status.SortingMode)
		if mode == "" {
			continue
		}
		return New(
			DownloadClientSortingCheckName,
			ResultWarning,
			ReasonDownloadClientSorting,
			fmt.Sprintf("Download client %s has sorting enabled for the Sonarr category (mode: %s); disable sorting on the client side to let Sonarr handle imports", dc.Name, mode),
			"#download-folder-and-library-folder-not-different-folders",
		)
	}
	return OK(DownloadClientSortingCheckName)
}
