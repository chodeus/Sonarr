// Package downloadmonitoring ports a focused subset of
// `NzbDrone.Core.Download.TrackedDownloads.DownloadMonitoringService`
// at `src/NzbDrone.Core/Download/TrackedDownloads/DownloadMonitoringService.cs`.
//
// The C# service handles ALL of:
//
//  1. Refresh-on-tick: poll every enabled download client's GetItems()
//     and re-project into the TrackedDownload cache.
//  2. Failed/completed download checks per item.
//  3. Bus event publish on every refresh
//     (TrackedDownloadRefreshedEvent + ProcessMonitoredDownloadsCommand).
//  4. IHandle subscribers for the on-demand refresh triggers
//     (EpisodeGrabbedEvent, EpisodeImportedEvent, etc.) — debounced 5s.
//
// This slice (5.J37) ships (1) and (3) only. The
// failed/completed-check arms (2) and the on-demand debounced
// refresh (4) are scheduled-task-driven only — the 1-minute cadence
// runs them naturally without the debounce wiring. The
// failed/completed dispatch is a follow-up — needs the
// trackeddownloads cache plumbing + failedown/completed services
// (`*faileddownloads.Service`, `*completeddownloads.Service` —
// `faileddownloads` already exists in the Go-port tree;
// `completeddownloads` is partial). Documented as a slice scope
// boundary.
//
// The wire shape callers observe matches C# at the Refresh boundary:
// invoking [Service.Refresh] enumerates clients, polls items, and
// fires QueueUpdatedEvent + TrackedDownloadRefreshedEvent. Audit
// fix B.3 (RefreshMonitoredDownloads scheduled-task handler).
package downloadmonitoring

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/queue"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

// ClientLister enumerates the currently-enabled download clients per
// protocol. `*download.ClientLister`-shaped values (e.g. the
// `downloadClientLister` adapter in cmd/sonarr/main.go) satisfy this
// directly via Go's structural typing.
type ClientLister interface {
	ListByProtocol(ctx context.Context, protocol indexers.DownloadProtocol) ([]downloadclients.Provider, error)
}

// DownloadItemTracker maps a polled download-client item to a
// [trackeddownloads.TrackedDownload] (parsing its title + matching a
// series), Tracks it in the tracked-download cache, and returns it for
// the queue-refresh event. Mirrors the per-item arm of C#
// `DownloadMonitoringService.Refresh` → `TrackedDownloadService.TrackDownload`.
//
// Optional (wired via [Service.SetTracker]). When unset, Refresh keeps
// the pre-J394 behaviour of polling for the item count + publishing an
// empty TrackedDownloadRefreshedEvent (the queue cache stays empty).
// Returns nil when the item can't be turned into a trackable download.
type DownloadItemTracker interface {
	TrackDownload(ctx context.Context, client downloadclients.Provider, item *downloadclients.Item) *trackeddownloads.TrackedDownload
}

// Service is the minimal Refresh orchestrator. Mirrors the C# class
// at `DownloadMonitoringService.cs` (subset — see package
// doc-comment). Goroutine-safe: stateless beyond construction-time
// deps.
type Service struct {
	clients ClientLister
	tracker DownloadItemTracker
	bus     *messaging.Bus
	log     *slog.Logger
}

// SetTracker wires the optional per-item [DownloadItemTracker] (J394).
// Once set, Refresh maps each polled item into a TrackedDownload so the
// queue cache populates from the live download clients.
func (s *Service) SetTracker(t DownloadItemTracker) {
	if s != nil {
		s.tracker = t
	}
}

// NewService binds the ClientLister + bus. log defaults to
// slog.Default when nil. bus may be nil — the Refresh poll still
// runs but publish is skipped (matches the established Phase 2/3
// nil-bus convention).
func NewService(clients ClientLister, bus *messaging.Bus, log *slog.Logger) (*Service, error) {
	if clients == nil {
		return nil, errors.New("downloadmonitoring: nil ClientLister")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{clients: clients, bus: bus, log: log}, nil
}

// Refresh polls every enabled download client (both usenet +
// torrent) and fires the bus events callers expect for a queue
// refresh cycle. Returns the number of items observed across all
// clients (zero when no clients enabled or all GetItems calls
// failed) and the first non-fatal poll error encountered (for
// observability — Refresh itself doesn't abort on per-client
// errors, matching the C# warn-and-continue posture at
// `DownloadMonitoringService.cs:91-105`).
//
// Mirrors C# `Refresh()` at `:55-79` (minus the
// ProcessMonitoredDownloads dispatch + per-item failed/completed
// checks — see package doc-comment).
func (s *Service) Refresh(ctx context.Context) (int, error) {
	var (
		totalItems int
		firstErr   error
		tracked    []*trackeddownloads.TrackedDownload
	)
	for _, protocol := range []indexers.DownloadProtocol{
		indexers.DownloadProtocolUsenet,
		indexers.DownloadProtocolTorrent,
	} {
		clients, err := s.clients.ListByProtocol(ctx, protocol)
		if err != nil {
			s.log.LogAttrs(ctx, slog.LevelWarn,
				"downloadmonitoring: ListByProtocol failed",
				slog.String("protocol", protocol.String()),
				slog.Any("err", err))
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, client := range clients {
			items, err := client.GetItems(ctx)
			if err != nil {
				s.log.LogAttrs(ctx, slog.LevelWarn,
					"downloadmonitoring: GetItems failed",
					slog.String("client", client.Name()),
					slog.String("protocol", protocol.String()),
					slog.Any("err", err))
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			totalItems += len(items)
			s.log.LogAttrs(ctx, slog.LevelDebug,
				"downloadmonitoring: client polled",
				slog.String("client", client.Name()),
				slog.String("protocol", protocol.String()),
				slog.Int("items", len(items)))

			// J394: map each polled item into a TrackedDownload so the
			// queue cache populates from the live client. Skipped when
			// no tracker is wired (preserves the count-only behaviour).
			if s.tracker != nil {
				for _, item := range items {
					if td := s.tracker.TrackDownload(ctx, client, item); td != nil {
						tracked = append(tracked, td)
					}
				}
			}
		}
	}

	if s.bus != nil {
		if err := messaging.Publish(ctx, s.bus, queue.QueueUpdatedEvent{}); err != nil {
			s.log.LogAttrs(ctx, slog.LevelWarn,
				"downloadmonitoring: QueueUpdatedEvent publish failed",
				slog.Any("err", err))
		}
		// TrackedDownloadRefreshedEvent carries the in-memory
		// TrackedDownload snapshot. J394 populates it from the poll
		// output via the wired tracker; when no tracker is set the
		// slice is nil (queue.Service.HandleTrackedDownloadRefreshed
		// tolerates an empty slice → empty queue).
		if err := messaging.Publish(ctx, s.bus, trackeddownloads.TrackedDownloadRefreshedEvent{
			TrackedDownloads: tracked,
		}); err != nil {
			s.log.LogAttrs(ctx, slog.LevelWarn,
				"downloadmonitoring: TrackedDownloadRefreshedEvent publish failed",
				slog.Any("err", err))
		}
	}

	if firstErr != nil {
		return totalItems, fmt.Errorf("downloadmonitoring: at least one client poll failed: %w", firstErr)
	}
	return totalItems, nil
}
