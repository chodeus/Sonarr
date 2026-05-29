// Package downloadeventhub ports the DownloadCompletedEvent arm of the
// C# `DownloadEventHub` (`src/NzbDrone.Core/Download/DownloadEventHub.cs`)
// — the consumer that tells the download client a completed download has
// been imported and (when the client is configured to) removes it from
// the client's history.
//
// J455 scope — the DownloadCompletedEvent handler (`DownloadEventHub.cs:49-69`):
//  1. MarkItemAsImported on the originating client (always; e.g. the
//     qBittorrent post-import category move). Best-effort: an
//     unsupported client logs at debug, any other error at error.
//  2. Gate: skip removal when the item can't be removed or the client
//     still reports it Downloading.
//  3. When the client's RemoveCompletedDownloads flag is set, RemoveItem
//     (deleteData=true) + StopTracking the download.
//
// J457 scope — the DownloadFailedEvent handler (`DownloadEventHub.cs:29-47`):
// when the item is removable and the originating client's
// RemoveFailedDownloads flag is set, RemoveItem (deleteData=true) +
// StopTracking. No mark-imported, no Downloading gate. This closes the
// gap where an auto-failed download (FailedDownloadService → the Go
// failure arm) was never removed from the client. Faithful to C#, the
// hub handles every DownloadFailedEvent regardless of source (the
// RemoveFailedDownloads config flag governs); a manual queue-delete that
// already removed the item just hits a swallowed not-found on the
// second RemoveItem.
//
// The C# hub also handles DownloadCanBeRemovedEvent (the
// RemoveCompletedDownloads sweep from DownloadProcessingService); that
// event + sweep aren't ported in Go — the J455 immediate-removal covers
// the common case. Tracked as a separate follow-up.
package downloadeventhub

import (
	"context"
	"errors"
	"log/slog"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

// ClientResolver builds the live download-client provider for a config
// row id (mirrors C# `_downloadClientProvider.Get(id)`).
type ClientResolver interface {
	ResolveClient(ctx context.Context, clientID int) (downloadclients.Provider, error)
}

// ConfigLister reads the persisted download-client rows. The handler
// uses it to read the RemoveCompletedDownloads flag for the originating
// client (mirrors reading `downloadClient.Definition.RemoveCompletedDownloads`).
// *downloadclients.Repo satisfies it via All.
type ConfigLister interface {
	All(ctx context.Context) ([]downloadclients.DownloadClient, error)
}

// TrackingStopper drops a download from the tracked-download cache after
// it's removed from the client (mirrors C#
// `_trackedDownloadService.StopTracking`). *trackeddownloads.Service
// satisfies it.
type TrackingStopper interface {
	// StopTracking drops the download from the cache; the returned bool
	// (whether a row was present) is unused here, matching the C# call
	// site that ignores it.
	StopTracking(downloadID string) bool
}

// Service is the ported DownloadCompletedEvent handler.
type Service struct {
	clients ClientResolver
	config  ConfigLister
	tracked TrackingStopper
	log     *slog.Logger
}

// NewService binds the collaborators. All three are required.
func NewService(clients ClientResolver, config ConfigLister, tracked TrackingStopper, log *slog.Logger) (*Service, error) {
	if clients == nil || config == nil || tracked == nil {
		return nil, errors.New("downloadeventhub: nil clients, config, or tracked")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{clients: clients, config: config, tracked: tracked, log: log}, nil
}

// HandleDownloadCompleted ports `DownloadEventHub.Handle(DownloadCompletedEvent)`
// (`:49-69`). Always returns nil — like the C# try/catch arms, a client
// error never propagates back to the bus (it would just be logged), and
// the import itself already succeeded.
func (s *Service) HandleDownloadCompleted(ctx context.Context, ev *trackeddownloads.DownloadCompletedEvent) error {
	if ev == nil || ev.TrackedDownload == nil || ev.TrackedDownload.DownloadItem == nil {
		return nil
	}
	td := ev.TrackedDownload
	client, err := s.clients.ResolveClient(ctx, td.DownloadClient)
	if err != nil {
		// Can't resolve the client → can't mark/remove. Mirrors the C#
		// posture where a missing client is logged, not fatal.
		s.log.Warn("downloadeventhub: resolve client failed",
			"downloadClient", td.DownloadClient, "downloadId", td.DownloadID, "err", err)
		return nil
	}

	s.markImported(ctx, client, td)

	// Removal gate (C# `:57-66`): skip when the item can't be removed or
	// the client still reports it actively downloading.
	if !td.DownloadItem.CanBeRemoved || td.DownloadItem.Status == downloadclients.DownloadItemStatusDownloading {
		return nil
	}
	removeCompleted, err := s.removeCompletedFor(ctx, td.DownloadClient)
	if err != nil {
		s.log.Warn("downloadeventhub: read RemoveCompletedDownloads failed",
			"downloadClient", td.DownloadClient, "err", err)
		return nil
	}
	if !removeCompleted {
		return nil
	}
	s.removeFromClient(ctx, client, td)
	return nil
}

// HandleDownloadFailed ports `DownloadEventHub.Handle(DownloadFailedEvent)`
// (`:29-47`): when the item is removable and the originating client's
// RemoveFailedDownloads flag is set, remove it from the client +
// StopTracking. Returns nil always (best-effort, like the completed
// arm). A nil TrackedDownload (manual-failed push with no cache record)
// is skipped — we can't resolve the client without it (mirrors the C#
// `trackedDownload == null` guard).
func (s *Service) HandleDownloadFailed(ctx context.Context, ev *trackeddownloads.DownloadFailedEvent) error {
	if ev == nil || ev.TrackedDownload == nil || ev.TrackedDownload.DownloadItem == nil {
		return nil
	}
	td := ev.TrackedDownload
	if !td.DownloadItem.CanBeRemoved {
		return nil
	}
	removeFailed, err := s.removeFailedFor(ctx, td.DownloadClient)
	if err != nil {
		s.log.Warn("downloadeventhub: read RemoveFailedDownloads failed",
			"downloadClient", td.DownloadClient, "err", err)
		return nil
	}
	if !removeFailed {
		return nil
	}
	client, err := s.clients.ResolveClient(ctx, td.DownloadClient)
	if err != nil {
		s.log.Warn("downloadeventhub: resolve client failed (failed arm)",
			"downloadClient", td.DownloadClient, "downloadId", td.DownloadID, "err", err)
		return nil
	}
	s.removeFromClient(ctx, client, td)
	return nil
}

// HandleDownloadCanBeRemoved ports `DownloadEventHub.Handle(DownloadCanBeRemovedEvent)`
// (`:71-84`): the deferred-removal path. When the item is removable and
// the client's RemoveCompletedDownloads flag is set, remove it +
// StopTracking. Identical gate to the completed arm's removal half,
// minus the mark-imported (already done at completion) and the
// Downloading status gate (the sweep only emits for removable items).
func (s *Service) HandleDownloadCanBeRemoved(ctx context.Context, ev *trackeddownloads.DownloadCanBeRemovedEvent) error {
	if ev == nil || ev.TrackedDownload == nil || ev.TrackedDownload.DownloadItem == nil {
		return nil
	}
	td := ev.TrackedDownload
	if !td.DownloadItem.CanBeRemoved {
		return nil
	}
	removeCompleted, err := s.removeCompletedFor(ctx, td.DownloadClient)
	if err != nil {
		s.log.Warn("downloadeventhub: read RemoveCompletedDownloads failed (can-be-removed arm)",
			"downloadClient", td.DownloadClient, "err", err)
		return nil
	}
	if !removeCompleted {
		return nil
	}
	client, err := s.clients.ResolveClient(ctx, td.DownloadClient)
	if err != nil {
		s.log.Warn("downloadeventhub: resolve client failed (can-be-removed arm)",
			"downloadClient", td.DownloadClient, "downloadId", td.DownloadID, "err", err)
		return nil
	}
	s.removeFromClient(ctx, client, td)
	return nil
}

// markImported runs MarkItemAsImported, mirroring the C# helper's
// try/catch (`:104-117`): an unsupported client is a debug log, any
// other failure an error log — never fatal.
func (s *Service) markImported(ctx context.Context, client downloadclients.Provider, td *trackeddownloads.TrackedDownload) {
	if err := client.MarkItemAsImported(ctx, td.DownloadItem); err != nil {
		if errors.Is(err, downloadclients.ErrMarkAsImportedUnsupported) {
			s.log.Debug("downloadeventhub: MarkItemAsImported unsupported by client", "downloadId", td.DownloadID)
			return
		}
		s.log.Error("downloadeventhub: MarkItemAsImported failed", "downloadId", td.DownloadID, "err", err)
	}
}

// removeFromClient runs RemoveItem + StopTracking, mirroring the C#
// helper (`:86-103`): a removal failure is logged, never fatal, and
// StopTracking only runs on a successful removal (matching the C# order
// where StopTracking follows RemoveItem in the same try block).
func (s *Service) removeFromClient(ctx context.Context, client downloadclients.Provider, td *trackeddownloads.TrackedDownload) {
	if err := client.RemoveItem(ctx, td.DownloadItem, true); err != nil {
		s.log.Error("downloadeventhub: RemoveItem failed", "downloadId", td.DownloadID, "err", err)
		return
	}
	s.tracked.StopTracking(td.DownloadID)
}

// removeCompletedFor reports the originating client's
// RemoveCompletedDownloads flag (C# `definition.RemoveCompletedDownloads`).
// A client id with no matching row reports false (don't remove what we
// can't confirm the config for).
func (s *Service) removeCompletedFor(ctx context.Context, clientID int) (bool, error) {
	rows, err := s.config.All(ctx)
	if err != nil {
		return false, err
	}
	for i := range rows {
		if rows[i].ID == clientID {
			return rows[i].RemoveCompletedDownloads, nil
		}
	}
	return false, nil
}

// removeFailedFor reports the originating client's RemoveFailedDownloads
// flag (C# `definition.RemoveFailedDownloads`). A client id with no
// matching row reports false.
func (s *Service) removeFailedFor(ctx context.Context, clientID int) (bool, error) {
	rows, err := s.config.All(ctx)
	if err != nil {
		return false, err
	}
	for i := range rows {
		if rows[i].ID == clientID {
			return rows[i].RemoveFailedDownloads, nil
		}
	}
	return false, nil
}
