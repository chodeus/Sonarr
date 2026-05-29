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
// The C# hub also handles DownloadFailedEvent (RemoveFailedDownloads) and
// DownloadCanBeRemovedEvent; neither is ported here — the Go failure arm
// drives blocklist/history/redownload via DownloadFailedEvent already,
// and DownloadCanBeRemovedEvent / the RemoveCompletedDownloads sweep
// aren't ported. Those are tracked as separate follow-ups.
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
