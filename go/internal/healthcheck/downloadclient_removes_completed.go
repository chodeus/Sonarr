package healthcheck

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"

	"sonarr.tv/sonarr/internal/configfile"
)

// DownloadClientRemovesCompletedDownloadsCheckName is the canonical
// Source identifier.
const DownloadClientRemovesCompletedDownloadsCheckName = "DownloadClientRemovesCompletedDownloadsCheck"

// DownloadClientRemovesCompletedDownloadsCheck warns when any enabled
// download client has its self-remove-completed feature on. Sonarr
// can't import what the client has already deleted, so the import
// pipeline silently misses grabs. Mirrors
// `src/NzbDrone.Core/HealthCheck/Checks/DownloadClientRemovesCompletedDownloadsCheck.cs:1-69`.
//
// The C# tree iterates every enabled client and short-circuits on
// the FIRST client that reports `RemovesCompletedDownloads=true`.
// Per-client construction failures are logged + skipped.
type DownloadClientRemovesCompletedDownloadsCheck struct {
	clients DownloadClientStatusEnumerator
	log     *slog.Logger
}

// NewDownloadClientRemovesCompletedDownloadsCheck builds the check.
// clients MUST be non-nil. log defaults to slog.Default() when nil.
func NewDownloadClientRemovesCompletedDownloadsCheck(clients DownloadClientStatusEnumerator, log *slog.Logger) (*DownloadClientRemovesCompletedDownloadsCheck, error) {
	if clients == nil {
		return nil, fmt.Errorf("healthcheck: NewDownloadClientRemovesCompletedDownloadsCheck: nil DownloadClientStatusEnumerator")
	}
	if log == nil {
		log = slog.Default()
	}
	return &DownloadClientRemovesCompletedDownloadsCheck{clients: clients, log: log}, nil
}

// Name returns the canonical check identifier.
func (*DownloadClientRemovesCompletedDownloadsCheck) Name() string {
	return DownloadClientRemovesCompletedDownloadsCheckName
}

// CheckOnStartup defaults true.
func (*DownloadClientRemovesCompletedDownloadsCheck) CheckOnStartup() bool { return true }

// CheckOnSchedule defaults true.
func (*DownloadClientRemovesCompletedDownloadsCheck) CheckOnSchedule() bool { return true }

// EventTriggers mirrors the four C# `[CheckOn]` attributes at
// `DownloadClientRemovesCompletedDownloadsCheck.cs:14-17`. The C#
// provider-lifecycle event types aren't ported yet; closest proxy
// is the config-saved event.
func (*DownloadClientRemovesCompletedDownloadsCheck) EventTriggers() []EventTrigger {
	return []EventTrigger{
		{EventType: reflect.TypeOf(configfile.ConfigFileSavedEvent{})},
	}
}

// Run mirrors the C# `Check()` method at `:33-67`.
func (c *DownloadClientRemovesCompletedDownloadsCheck) Run(ctx context.Context) HealthCheck {
	clients, err := c.clients.EnabledDownloadClients(ctx)
	if err != nil {
		return New(
			DownloadClientRemovesCompletedDownloadsCheckName,
			ResultWarning,
			ReasonDownloadClientRemovesCompletedDownloads,
			"Unable to enumerate download clients: "+err.Error(),
			"#download-client-removes-completed-downloads",
		)
	}
	for _, dc := range clients {
		if dc.Err != nil {
			c.log.Debug("downloadClient removes-completed check: skipping client",
				slog.String("client", dc.Name),
				slog.Any("err", dc.Err),
			)
			continue
		}
		if dc.Status == nil {
			continue
		}
		if dc.Status.RemovesCompletedDownloads {
			return New(
				DownloadClientRemovesCompletedDownloadsCheckName,
				ResultWarning,
				ReasonDownloadClientRemovesCompletedDownloads,
				fmt.Sprintf("Download client %s is removing completed downloads; this can cause imports to silently miss episodes — disable the client-side auto-remove and let Sonarr remove on successful import instead", dc.Name),
				"#download-client-removes-completed-downloads",
			)
		}
	}
	return OK(DownloadClientRemovesCompletedDownloadsCheckName)
}
