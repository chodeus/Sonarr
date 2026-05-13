package healthcheck

import (
	"context"
	"fmt"
)

// DownloadClientCheckName is the canonical Source identifier.
const DownloadClientCheckName = "DownloadClientCheck"

// DownloadClientLister narrows the download-client provider surface
// the check needs. Mirrors the C# `_downloadClientProvider
// .GetDownloadClients()` accessor at
// `src/NzbDrone.Core/Download/DownloadClientProvider.cs` — returns
// the enabled subset.
//
// EnabledDownloadClientCount returns the row count of enabled
// download-clients (the C# `.Empty()` check only consumes the count).
// Full per-row health probing (the C# loop that calls
// `downloadClient.GetItems()` per client to detect communication
// failures) is DEFERRED until the download-client provider port
// lands a probe surface — for the empty-DB byte-eq gate the count
// alone decides.
type DownloadClientLister interface {
	EnabledDownloadClientCount(ctx context.Context) (int, error)
}

// DownloadClientCheck warns when no download client is configured.
// Mirrors `src/NzbDrone.Core/HealthCheck/Checks/DownloadClientCheck.cs`.
//
// Outcomes (subset wired here):
//
//  1. Zero enabled download clients → Warning /
//     `ReasonDownloadClientCheckNoneAvailable`.
//  2. ≥1 enabled → OK.
//
// The C# tree's per-client communication probe
// (`ReasonDownloadClientCheckUnableToCommunicate`) is DEFERRED until
// the Go-side download-client provider exposes a probe surface; the
// empty-DB recording emits only the NoneAvailable branch.
type DownloadClientCheck struct {
	clients DownloadClientLister
}

// NewDownloadClientCheck builds the check. clients MUST be non-nil.
func NewDownloadClientCheck(clients DownloadClientLister) (*DownloadClientCheck, error) {
	if clients == nil {
		return nil, fmt.Errorf("healthcheck: NewDownloadClientCheck: nil DownloadClientLister")
	}
	return &DownloadClientCheck{clients: clients}, nil
}

// Name reports the canonical Source identifier.
func (*DownloadClientCheck) Name() string { return DownloadClientCheckName }

// CheckOnStartup mirrors the C# default `true`.
func (*DownloadClientCheck) CheckOnStartup() bool { return true }

// CheckOnSchedule mirrors the C# default `true`.
func (*DownloadClientCheck) CheckOnSchedule() bool { return true }

// EventTriggers — the C# tree registers four download-client
// provider events. Wiring those up requires the Go provider event
// types which aren't ported yet; the check only runs on startup +
// scheduled batch.
func (*DownloadClientCheck) EventTriggers() []EventTrigger { return nil }

// Run executes the check.
func (c *DownloadClientCheck) Run(ctx context.Context) HealthCheck {
	count, err := c.clients.EnabledDownloadClientCount(ctx)
	if err != nil {
		return New(
			DownloadClientCheckName,
			ResultWarning,
			ReasonDownloadClientCheckNoneAvailable,
			"Unable to enumerate download clients: "+err.Error(),
			"#no-download-client-is-available",
		)
	}
	if count == 0 {
		return New(
			DownloadClientCheckName,
			ResultWarning,
			ReasonDownloadClientCheckNoneAvailable,
			"No download client is available",
			"#no-download-client-is-available",
		)
	}
	return OK(DownloadClientCheckName)
}
