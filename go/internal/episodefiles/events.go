package episodefiles

import (
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/series"
)

// DeleteMediaFileReason ports
// `NzbDrone.Core.MediaFiles.DeleteMediaFileReason` at
// `src/NzbDrone.Core/MediaFiles/DeleteMediaFileReason.cs`. Value
// numbering matches the C# default enum order (zero-indexed); the
// C# enum is not persisted (it's an event-payload only) so the
// numbering is event-bus/runtime-only and does NOT need a frozen
// pin like the persisted Quality/Language IDs.
type DeleteMediaFileReason int

const (
	// ReasonMissingFromDisk fires when the housekeeper detects the
	// mkv has gone missing from disk and the row is being unlinked.
	// Triggers the EpisodeService missing-from-disk auto-unmonitor
	// path when AutoUnmonitorPreviouslyDownloadedEpisodes is set.
	ReasonMissingFromDisk DeleteMediaFileReason = iota

	// ReasonManual fires when the user clicks the "delete" button on
	// the episode-file in the UI.
	ReasonManual

	// ReasonUpgrade fires when an upgrade-import unlinks the prior
	// file in favor of a higher-quality replacement.
	ReasonUpgrade

	// ReasonNoLinkedEpisodes fires when housekeeping detects the
	// file is orphaned (no Episodes row points at it).
	ReasonNoLinkedEpisodes

	// ReasonManualOverride fires when the user explicitly overrides
	// an automatic action (e.g. force-import a flagged release).
	ReasonManualOverride
)

// String returns the canonical name — useful for log messages and
// for round-tripping through any future event-envelope serializer.
func (r DeleteMediaFileReason) String() string {
	switch r {
	case ReasonMissingFromDisk:
		return "MissingFromDisk"
	case ReasonManual:
		return "Manual"
	case ReasonUpgrade:
		return "Upgrade"
	case ReasonNoLinkedEpisodes:
		return "NoLinkedEpisodes"
	case ReasonManualOverride:
		return "ManualOverride"
	default:
		return "Unknown"
	}
}

// EpisodeFileAddedEvent ports
// `MediaFiles/Events/EpisodeFileAddedEvent.cs`. Carries the inserted
// File row so listeners can fan out: the EpisodeService binds
// EpisodeFileId on each linked episode and clears the
// missing-from-disk cache; the renamer pre-creates the destination
// folder; etc.
//
// `LinkedEpisodes` mirrors the C# `EpisodeFile.Episodes.Value`
// LazyLoaded list — the list of Episode rows the file is bound to.
// In C# this is implicit (loaded on first access by the Dapper
// LazyLoaded wrapper); the Go port plumbs it explicitly because
// there's no LazyLoaded analogue. Producers populate this before
// publishing — usually by calling
// `episodes.Service.GetEpisodesByFileID(file.ID)` in the Add path
// or by carrying the linked-episodes list through the import
// pipeline.
type EpisodeFileAddedEvent struct {
	EpisodeFile    *File
	LinkedEpisodes []episodes.Episode
}

// EpisodeFileDeletedEvent ports
// `MediaFiles/Events/EpisodeFileDeletedEvent.cs`. Carries the
// deleted File row + the reason so the EpisodeService handler
// can decide whether to auto-unmonitor (only ReasonMissingFromDisk
// + AutoUnmonitor flag triggers the cache write).
//
// `LinkedEpisodes` carries what was linked at delete time —
// sourced before the row is deleted (the C# version uses
// `LazyLoaded` to materialise it; the Go port plumbs it
// explicitly from the GetEpisodesByFileID lookup).
//
// `Series` is the parent series at delete time — the C# version
// uses it to materialise `episodeFile.Path =
// Path.Combine(Series.Path, RelativePath)` for downstream
// consumers. Producers SHOULD populate this when available.
type EpisodeFileDeletedEvent struct {
	EpisodeFile    *File
	LinkedEpisodes []episodes.Episode
	Series         *series.Series
	Reason         DeleteMediaFileReason
}

// EpisodeFileRenamedEvent ports
// `MediaFiles/Events/EpisodeFileRenamedEvent.cs`. Fires when the
// renamer moves a file on disk and updates the row's RelativePath.
// `OriginalPath` is the absolute path the file lived at before
// the rename — listeners (extras, subtitles) use it to update
// their own associated paths.
type EpisodeFileRenamedEvent struct {
	Series       *series.Series
	EpisodeFile  *File
	OriginalPath string
}

// RenamedEpisodeFile ports C# `MediaFiles/RenamedEpisodeFile.cs`: one
// successfully-renamed file plus where it lived before the move. Carried
// in [SeriesRenamedEvent].
type RenamedEpisodeFile struct {
	// EpisodeFile is the file row after the move (updated RelativePath).
	EpisodeFile *File
	// PreviousPath is the absolute path the file lived at pre-move.
	PreviousPath string
	// PreviousRelativePath is the series-relative path pre-move.
	PreviousRelativePath string
}

// SeriesRenamedEvent ports `MediaFiles/Events/SeriesRenamedEvent.cs`.
// Published by the rename path (C# `RenameEpisodeFileService.RenameFiles`
// at `:174`) once per series after at least one of its files was
// renamed, carrying every renamed file + its prior paths. Consumed by
// the extras/metadata writer (ExtraService.Handle(SeriesRenamedEvent) →
// MoveFilesAfterRename) to relocate sidecar files alongside the renamed
// media.
type SeriesRenamedEvent struct {
	Series       *series.Series
	RenamedFiles []RenamedEpisodeFile
}

// RenameCompletedEvent ports the C# `RenameCompletedEvent : IEvent`
// (`src/NzbDrone.Core/MediaFiles/Events/RenameCompletedEvent.cs`).
// Published once at the end of a RenameFiles / RenameSeries command
// (`RenameEpisodeFileService.cs:190,206`). Its sole consumer is the
// notifications ProcessQueue drain (flushes the Plex/Kodi/Emby
// coalescing buffers after a rename batch). Carries no payload.
type RenameCompletedEvent struct{}

// DeleteCompletedEvent ports the C# `DeleteCompletedEvent : IEvent`
// (`src/NzbDrone.Core/MediaFiles/Events/DeleteCompletedEvent.cs`).
// Published by `MediaFileDeletionService` after a manual single
// episode-file delete (`:99`) and after the series-delete cascade
// folder removal (`:210`). Its sole consumer is the notifications
// ProcessQueue drain. Carries no payload.
//
// Go-port divergence (unobservable): the Go port publishes this from the
// manual single-file delete path only (episodeFileDeleteAdapter); the
// series-delete cascade does not emit it. Because the only consumer is
// the ProcessQueue drain — a no-op for every currently-ported provider
// (webhook/Discord ProcessQueue are no-ops; the buffering Plex/Kodi/Emby
// providers aren't ported) — the difference has no observable effect.
type DeleteCompletedEvent struct{}

// EpisodeFileUpdatedEvent is a Go-port additive event fired by
// [Service.Update] / [Service.UpdateMany] after a successful repo
// update. C# Sonarr emits the equivalent signal via Dapper's
// `ModelEvent<EpisodeFile>` (a generic event the persistence layer
// fires implicitly on every Repository.Update — see
// `src/NzbDrone.Core/Datastore/Events/ModelEvent.cs` +
// `BasicRepository<T>.UpdateFields`). The Go port doesn't have a
// generic ModelEvent analogue, so each Service that needs the
// "row mutated" signal publishes its own typed event.
//
// Consumers: the SignalR `episodefile` channel's Sync broadcast
// (cmd/sonarr/signalr_wiring.go subscriber) — the React UI listens
// on Sync to refresh the episode-file list after a SetQuality / Editor
// edit. `Series` is populated when a SeriesProvider is wired so the
// broadcaster can materialise the C# `Path.Combine(Series.Path,
// RelativePath)` wire shape (mirrors the EpisodeFileDeletedEvent
// path-materialisation pattern).
type EpisodeFileUpdatedEvent struct {
	EpisodeFile *File
	Series      *series.Series
}
