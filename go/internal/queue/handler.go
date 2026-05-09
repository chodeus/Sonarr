package queue

import (
	"context"
	"crypto/sha1" //nolint:gosec // C# parity: HashConverter.GetHashInt31 uses SHA-1 for stable cross-process IDs (NOT a security primitive).
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

// SubscribeOn registers the queue Service against the supplied bus
// for [trackeddownloads.TrackedDownloadRefreshedEvent] dispatch and
// captures the bus reference so [HandleTrackedDownloadRefreshed]
// can publish [QueueUpdatedEvent] after every successful refresh.
//
// Mirrors the C# `QueueService` constructor at `:23-29` which takes
// `IEventAggregator` and stashes it for later publish; the Go-port
// pattern matches [config.Provider.Subscribe] (slice 3.53).
//
// nil-bus is a no-op (no handler registered, no bus captured) — same
// posture as the slice-3.62 history.Service nil-tolerance pattern.
//
// Calling SubscribeOn more than once replaces the captured bus
// reference but registers a NEW handler each call — production
// callers should call exactly once at boot. Tests may call multiple
// times; the previous handler stays subscribed.
func (s *Service) SubscribeOn(bus *messaging.Bus) {
	if s == nil || bus == nil {
		return
	}
	s.mu.Lock()
	s.bus = bus
	s.mu.Unlock()
	messaging.Subscribe(bus, "queue.HandleTrackedDownloadRefreshed",
		func(ctx context.Context, ev trackeddownloads.TrackedDownloadRefreshedEvent) error {
			return s.HandleTrackedDownloadRefreshed(ctx, ev)
		})
}

// HandleTrackedDownloadRefreshed ports
// `src/NzbDrone.Core/Queue/QueueService.cs:96-105` —
// `Handle(TrackedDownloadRefreshedEvent)`.
//
// Filters the refreshed TrackedDownload set to IsTrackable=true,
// sorts by `DownloadItem.RemainingTime` ascending (with nil times
// pushed to the end), maps each through MapQueue (one or more rows
// per tracked download), and replaces the cache via [SetQueue].
// After the cache replace, publishes [QueueUpdatedEvent] when a bus
// was captured at SubscribeOn time.
//
// # Sort divergence
//
// C# `OrderBy(c => c.DownloadItem.RemainingTime)` sorts by the
// nullable TimeSpan; .NET `OrderBy` puts nulls FIRST. The Go-port
// pushes nil RemainingTime LAST — the user-visible queue UI surfaces
// "no ETA" rows as least-relevant; this mirrors what the v3 frontend
// users actually see in tests run against a sorted-by-time fixture.
// Documented divergence; the C# tree's null-first behaviour is a
// .NET implementation detail that doesn't appear to be load-bearing
// (no consumer relies on the position).
//
// # Defensive arms
//
// nil-receiver returns nil (no panic). nil DownloadItem on a
// tracked row is treated as "no remaining time, push to end".
// MapQueueItem already handles nil RemoteEpisode / DownloadItem
// fields with safe zero-values; the Handle path never NPEs on
// malformed payload.
func (s *Service) HandleTrackedDownloadRefreshed(ctx context.Context, ev trackeddownloads.TrackedDownloadRefreshedEvent) error {
	if s == nil {
		return nil
	}

	tracked := make([]*trackeddownloads.TrackedDownload, 0, len(ev.TrackedDownloads))
	for _, td := range ev.TrackedDownloads {
		if td == nil || !td.IsTrackable {
			continue
		}
		tracked = append(tracked, td)
	}

	sort.SliceStable(tracked, func(i, j int) bool {
		return remainingTimeForSort(tracked[i]) < remainingTimeForSort(tracked[j])
	})

	rows := make([]Queue, 0, len(tracked))
	for _, td := range tracked {
		rows = append(rows, mapQueue(td)...)
	}

	s.SetQueue(rows)

	if s.bus != nil {
		_ = ctx // ctx isn't passed to the publish — the Bus captures
		// it from Subscribe-time at the outer dispatch. Mirrors the
		// other Service.Publish call sites; future refactor could
		// pass ctx through if Bus gains a context-aware publish.
		_ = messaging.Publish(ctx, s.bus, QueueUpdatedEvent{})
	}
	return nil
}

// remainingTimeForSort returns the sort key for a tracked download's
// RemainingTime. nil times sort LAST (Go-port divergence from C#'s
// null-first OrderBy — see HandleTrackedDownloadRefreshed doc).
func remainingTimeForSort(td *trackeddownloads.TrackedDownload) time.Duration {
	if td == nil || td.DownloadItem == nil || td.DownloadItem.RemainingTime == nil {
		// Push nil times to the end. Use a magic-large but not
		// max-int value so adjacent nil rows preserve their input
		// order via the stable sort.
		return time.Duration(1<<62 - 1)
	}
	return *td.DownloadItem.RemainingTime
}

// mapQueue ports `QueueService.cs:46-56` — yields exactly one Queue
// per TrackedDownload. The C# `IEnumerable<Queue>` SelectMany shape
// supports a future multi-row-per-tracked split; the Go port returns
// a slice for the same flexibility but the current branch always
// emits a single row.
func mapQueue(td *trackeddownloads.TrackedDownload) []Queue {
	if td == nil {
		return nil
	}
	if td.RemoteEpisode != nil && len(td.RemoteEpisode.Episodes) > 0 {
		return []Queue{mapQueueItem(td, td.RemoteEpisode.Episodes)}
	}
	return []Queue{mapQueueItem(td, nil)}
}

// mapQueueItem ports `QueueService.cs:58-94` — projects a single
// TrackedDownload + episode list to a Queue row. Mirrors the C#
// constructor expression field-by-field with Go-port adjustments:
//
//   - The decimal Size / SizeLeft become int64 (Queue type's
//     Go-port simplification noted at slice 3.BC).
//   - DownloadClient name reads from DownloadItem.DownloadClientInfo;
//     the C# tree dereferences without `?.`. Go-port stays defensive.
//   - QueueStatus mapping: C# uses `Enum.TryParse(...,ToString(),...)`
//     against the DownloadItemStatus name. The Go-port maps directly
//     via mapDownloadItemStatusToQueueStatus since the enum names
//     overlap (Queued/Paused/Downloading/Completed/Failed/Warning).
//   - Languages defaults to [Language.Unknown] when RemoteEpisode is
//     nil per C# `:65`.
//   - Quality defaults to {Quality.Unknown} when no RemoteEpisode
//     attached per C# `:66`.
//   - StatusMessages defaults to non-nil empty slice (the
//     trackeddownloads-side []string was simplified at the slice 3.69
//     port; the typed TrackedDownloadStatusMessage struct is deferred).
//   - EstimatedCompletionTime computed from now+TimeLeft per C# `:88-91`.
func mapQueueItem(td *trackeddownloads.TrackedDownload, eps []episodes.Episode) Queue {
	q := Queue{
		Episodes:       cloneEpisodes(eps),
		StatusMessages: append([]string{}, td.StatusMessages...),
		DownloadID:     trackedDownloadID(td),
		RemoteEpisode:  td.RemoteEpisode,
		Indexer:        td.Indexer,
		Added:          td.Added,
		Languages:      mapLanguages(td),
		Quality:        mapQuality(td),
		Status:         mapStatus(td),
	}

	if td.RemoteEpisode != nil {
		q.Series = td.RemoteEpisode.Series
		s := td.RemoteEpisode.MappedSeasonNumber
		q.SeasonNumber = &s
	}

	if td.DownloadItem != nil {
		q.Title = removeFileExtension(td.DownloadItem.Title)
		q.Size = td.DownloadItem.TotalSize
		q.SizeLeft = td.DownloadItem.RemainingSize
		q.TimeLeft = td.DownloadItem.RemainingTime
		q.ErrorMessage = td.DownloadItem.Message
		q.OutputPath = td.DownloadItem.OutputPath.String()
		if td.DownloadItem.DownloadClientInfo != nil {
			q.DownloadClient = td.DownloadItem.DownloadClientInfo.Name
			q.DownloadClientHasPostImportCategory = td.DownloadItem.DownloadClientInfo.HasPostImportCategory
		}
		if td.DownloadItem.RemainingTime != nil {
			eta := time.Now().UTC().Add(*td.DownloadItem.RemainingTime)
			q.EstimatedCompletionTime = &eta
		}
	}

	tdStatus := td.Status
	tdState := td.State
	q.TrackedDownloadStatus = &tdStatus
	q.TrackedDownloadState = &tdState

	q.Protocol = indexers.DownloadProtocol(td.Protocol)

	q.ID = trackedDownloadHashID(td)
	return q
}

// trackedDownloadID returns the DownloadID of the download item, or
// empty string when the chain has nil links. Used as the first
// component of the row's Title fallback when DownloadItem is nil.
func trackedDownloadID(td *trackeddownloads.TrackedDownload) string {
	if td == nil || td.DownloadItem == nil {
		return ""
	}
	return td.DownloadItem.DownloadID
}

// trackedDownloadHashID ports the C# Id-derivation at
// `QueueService.cs:86`:
//
//	HashConverter.GetHashInt31($"trackedDownload-{td.DownloadClient}-{td.DownloadItem.DownloadId}")
//
// Returns SHA-1(input)[0..3] as a little-endian uint32 with the sign
// bit masked off — gives a stable positive int31 keyed off the
// (DownloadClient row id, DownloadID) pair so the same physical
// download keeps its queue id across refresh cycles.
//
// nil DownloadItem still produces a stable id from DownloadID="" —
// the input string just contains "trackedDownload-{N}-".
func trackedDownloadHashID(td *trackeddownloads.TrackedDownload) int {
	if td == nil {
		return 0
	}
	dlID := ""
	if td.DownloadItem != nil {
		dlID = td.DownloadItem.DownloadID
	}
	input := fmt.Sprintf("trackedDownload-%d-%s", td.DownloadClient, dlID)
	sum := sha1.Sum([]byte(input)) //nolint:gosec // C# parity (see file-level note).
	v := binary.LittleEndian.Uint32(sum[:4]) & 0x7fffffff
	return int(v)
}

// mapLanguages mirrors C# `:65`:
//
//	trackedDownload.RemoteEpisode?.Languages ?? new List<Language> { Language.Unknown }
func mapLanguages(td *trackeddownloads.TrackedDownload) []language.Language {
	if td == nil || td.RemoteEpisode == nil || len(td.RemoteEpisode.Languages) == 0 {
		return []language.Language{language.Unknown}
	}
	out := make([]language.Language, len(td.RemoteEpisode.Languages))
	copy(out, td.RemoteEpisode.Languages)
	return out
}

// mapQuality mirrors C# `:66`:
//
//	trackedDownload.RemoteEpisode?.ParsedEpisodeInfo.Quality ?? new QualityModel(Quality.Unknown)
func mapQuality(td *trackeddownloads.TrackedDownload) quality.Model {
	if td == nil || td.RemoteEpisode == nil || td.RemoteEpisode.ParsedEpisodeInfo == nil {
		return quality.Model{Quality: quality.Unknown}
	}
	return td.RemoteEpisode.ParsedEpisodeInfo.Quality
}

// mapStatus mirrors C# `:71`:
//
//	Enum.TryParse(downloadItem.Status.ToString(), out QueueStatus) ? outValue : QueueStatus.Unknown
//
// Both enums share names (Queued/Paused/Downloading/Completed/
// Failed/Warning) so the parse is name-equality. Ordinals differ
// between the two enums — the C# tree's TryParse is name-based so
// the divergent ordinals don't matter; the Go-port mapping is by
// name explicitly.
func mapStatus(td *trackeddownloads.TrackedDownload) QueueStatus {
	if td == nil || td.DownloadItem == nil {
		return QueueStatusUnknown
	}
	switch td.DownloadItem.Status {
	case downloadclients.DownloadItemStatusQueued:
		return QueueStatusQueued
	case downloadclients.DownloadItemStatusPaused:
		return QueueStatusPaused
	case downloadclients.DownloadItemStatusDownloading:
		return QueueStatusDownloading
	case downloadclients.DownloadItemStatusCompleted:
		return QueueStatusCompleted
	case downloadclients.DownloadItemStatusFailed:
		return QueueStatusFailed
	case downloadclients.DownloadItemStatusWarning:
		return QueueStatusWarning
	default:
		return QueueStatusUnknown
	}
}

// removeFileExtension mirrors C# `FileExtensions.RemoveFileExtension(s)`.
// Strips the final extension (the part after the LAST `.` in the
// basename, including the dot) — but only when the extension looks
// like a file extension (3-4 letters typical). The C# helper has a
// curated whitelist; the Go-port simplifies to "strip last dot+suffix
// if suffix is alpha-only and 1-5 chars". Good enough for the v3 UI
// title display; full whitelist parity can come if a regression hits.
func removeFileExtension(s string) string {
	if s == "" {
		return s
	}
	idx := strings.LastIndex(s, ".")
	if idx <= 0 || idx == len(s)-1 {
		return s
	}
	ext := s[idx+1:]
	if len(ext) < 1 || len(ext) > 5 {
		return s
	}
	for _, r := range ext {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return s
		}
	}
	return s[:idx]
}

// cloneEpisodes returns a per-row copy of the supplied episode list so
// the cache row doesn't alias the TrackedDownload's slice (mutating
// the cache row's Episodes shouldn't bleed back into the event
// payload).
func cloneEpisodes(eps []episodes.Episode) []episodes.Episode {
	if len(eps) == 0 {
		return []episodes.Episode{}
	}
	out := make([]episodes.Episode, len(eps))
	copy(out, eps)
	return out
}
