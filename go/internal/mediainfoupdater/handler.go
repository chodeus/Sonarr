package mediainfoupdater

import (
	"context"
	"fmt"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/mediafiles"
	"sonarr.tv/sonarr/internal/messaging"
)

// SubscribeOn registers HandleSeriesScanned against the supplied bus.
// nil-bus / nil-receiver are no-ops (matches the slice-3.BE
// queue.Service.SubscribeOn posture).
//
// Callers wire this once at boot after constructing the
// UpdateService. The handler runs synchronously on the dispatch
// goroutine — long-running media-info reads block the bus dispatch;
// production may want to wrap with messaging.SubscribeAsync once
// the reader is heavy (sub-arc 13c). For now the synchronous
// shape mirrors the C# IHandle dispatch tier.
func (u *UpdateService) SubscribeOn(bus *messaging.Bus) {
	if u == nil || bus == nil {
		return
	}
	messaging.Subscribe(bus, "mediainfoupdater.HandleSeriesScanned",
		func(ctx context.Context, ev mediafiles.SeriesScannedEvent) error {
			return u.HandleSeriesScanned(ctx, ev)
		})
}

// HandleSeriesScanned ports
// `MediaFiles/MediaInfo/UpdateMediaInfoService.cs:40-57` —
// `Handle(SeriesScannedEvent)`. Walks every persisted media file
// for the scanned series and re-extracts MediaInfo for rows that
// either lack a persisted blob OR carry a SchemaRevision below
// [MinimumSchemaRevision].
//
// # Algorithm (mirrors C# `:42-56`)
//
//  1. Gate on EnableMediaInfo — when disabled, log debug + return
//     without touching the DB or disk.
//  2. Fetch all MediaFile rows for the series via the FileReader
//     narrow interface (production binding: episodefiles.Service.
//     GetFilesBySeries).
//  3. Filter to rows where MediaInfo == nil OR
//     MediaInfo.SchemaRevision < MinimumSchemaRevision.
//  4. For each filtered row, call UpdateMediaInfo (which itself
//     handles the FileExists check, the reader call, and the
//     persistence).
//
// # Defensive arms
//
// nil-receiver / nil-event-Series → no-op (no panic, no error).
// Reader error propagates wrapped — the C# tree lets the
// IMediaFileService throw bubble up to the IHandle dispatcher,
// which logs + re-raises; the Go-port surfaces a typed error so
// the bus can decide whether to log or retry.
//
// Per-file UpdateMediaInfo errors propagate at the FIRST failure
// (the C# tree's foreach swallows nothing — same semantic).
func (u *UpdateService) HandleSeriesScanned(ctx context.Context, ev mediafiles.SeriesScannedEvent) error {
	if u == nil {
		return nil
	}
	if ev.Series == nil {
		return nil
	}
	if !u.cfg.EnableMediaInfo() {
		u.log.Debug("mediainfoupdater: HandleSeriesScanned skipped — EnableMediaInfo is disabled",
			"seriesID", ev.Series.ID)
		return nil
	}

	files, err := u.fileReader.GetFilesBySeries(ctx, ev.Series.ID)
	if err != nil {
		return fmt.Errorf("mediainfoupdater: HandleSeriesScanned: GetFilesBySeries(%d): %w", ev.Series.ID, err)
	}

	for i := range files {
		f := &files[i]
		if !needsMediaInfoRefresh(f) {
			continue
		}
		if _, err := u.UpdateMediaInfo(ctx, f, ev.Series); err != nil {
			return fmt.Errorf("mediainfoupdater: HandleSeriesScanned(file=%d): %w", f.ID, err)
		}
	}
	return nil
}

// needsMediaInfoRefresh mirrors the C# filter at
// `UpdateMediaInfoService.cs:49-51`:
//
//	c.MediaInfo == null || c.MediaInfo.SchemaRevision < MinimumSchemaRevision
//
// Returns true when the row should be re-extracted on this scan.
func needsMediaInfoRefresh(f *episodefiles.File) bool {
	if f == nil {
		return false
	}
	if f.MediaInfo == nil {
		return true
	}
	return f.MediaInfo.SchemaRevision < MinimumSchemaRevision
}
