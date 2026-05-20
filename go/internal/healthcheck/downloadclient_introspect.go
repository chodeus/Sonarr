package healthcheck

import (
	"context"

	"sonarr.tv/sonarr/internal/downloadclients"
)

// DownloadClientStatusEnumerator hands a healthcheck one snapshot
// per enabled download client. Production wiring builds this against
// `*downloadclients.Repo` + `*downloadclients.Registry` (mirroring
// the `cmd/sonarr/downloadclients_lister.go` shape), constructing
// each Provider lazily and calling GetStatus per row.
//
// Per-row construction or status failures DO NOT abort the entire
// enumeration — they're recorded on the returned `EnumeratedDownloadClient`
// so the consuming check can log + skip, matching the C# `try/catch`
// arm in DownloadClientSortingCheck.cs:40-65 (Debug for
// DownloadClientException, Error for everything else). The check
// surfaces only the FIRST construction-or-status failure; consumers
// that want every failure can walk the slice themselves.
//
// EnabledDownloadClients (the method name) is the obvious match for
// the C# `IProvideDownloadClient.GetDownloadClients(true)` that DC
// healthchecks call.
//
// The single shared interface lets the three DC-status healthchecks
// (Sorting, RemovesCompleted, RootFolder) reuse one adapter — they
// only differ in which field of the returned ClientInfo they
// inspect.
type DownloadClientStatusEnumerator interface {
	EnabledDownloadClients(ctx context.Context) ([]EnumeratedDownloadClient, error)
}

// EnumeratedDownloadClient is one entry in the enumeration. Both
// the Status pointer and the Err can be set: when the client row
// exists but its Construct or GetStatus failed, Status is nil and
// Err is non-nil so the consuming check can log + skip with the
// name + reason.
type EnumeratedDownloadClient struct {
	// Name is the user-facing client name surfaced in healthcheck
	// warning messages.
	Name string
	// Status is the snapshot returned by Provider.GetStatus when
	// the per-row Construct + status RPC succeeded. nil when Err
	// is set.
	Status *downloadclients.ClientInfo
	// Err carries the per-row failure when Construct or GetStatus
	// errored. The C# DC healthchecks log + skip on
	// DownloadClientException; consumers should treat a non-nil
	// Err as "this client doesn't contribute to the check
	// outcome".
	Err error
}
