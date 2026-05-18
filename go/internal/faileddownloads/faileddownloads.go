// Package faileddownloads ports a minimal slice of C#
// `FailedDownloadService` (`src/NzbDrone.Core/Download/FailedDownloadService.cs`).
//
// Phase 5.E4 scope: the `MarkAsFailed(int historyId)` entry point
// invoked by `POST /api/v3/history/failed/{id}`. The remaining C#
// methods (`MarkAsFailed(TrackedDownload, …)`, `Check`,
// `ProcessFailed`) are wired into the tracked-downloads orchestration
// path and ship in a later slice.
//
// What this slice does:
//
//  1. Loads the history.Entry by id (via [history.Service.Get]).
//  2. Decodes Quality (JSON string -> quality.Model) + Languages
//     (int ids -> language.Language values).
//  3. Builds a trackeddownloads.DownloadFailedEvent with the row
//     data and Source="ManualOverride", Message="Manually marked
//     as failed".
//  4. Publishes via [messaging.Bus] so the existing IHandle
//     subscribers (blocklist + history-write + redownload) react
//     identically to the auto-fail path.
//
// What this slice DEFERS (with rationale):
//
//   - Grabbed-history aggregation: the C# action calls
//     `historyService.FindByDownloadID(downloadId, Grabbed)` and
//     unions every matching row's EpisodeId into the event's
//     EpisodeIDs. The Go port emits only the loaded row's
//     EpisodeID; multi-grab-per-download deduplication is a
//     follow-up. The single-grab-per-download case (the common one)
//     is fully covered.
//   - SkipRedownload + ReleaseSource flags: the C# API doesn't
//     accept these via the route (only the TrackedDownload overload
//     does), so the event is published with both at zero values.
package faileddownloads

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/history"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

// HistoryReader narrows *history.Service to the Get method this
// package needs. Stubbable in tests.
type HistoryReader interface {
	Get(ctx context.Context, id int) (*history.Entry, error)
}

// Service ports a subset of `IFailedDownloadService`. Wraps a
// history reader + an event bus. Stateless after construction.
type Service struct {
	history HistoryReader
	bus     *messaging.Bus
}

// NewService binds a HistoryReader + Bus. Both required; nil reader
// or nil bus returns an error so the boot site fails loudly rather
// than silently no-opping the manual-mark-as-failed flow.
func NewService(h HistoryReader, bus *messaging.Bus) (*Service, error) {
	if h == nil {
		return nil, errors.New("faileddownloads: nil history reader")
	}
	if bus == nil {
		return nil, errors.New("faileddownloads: nil bus")
	}
	return &Service{history: h, bus: bus}, nil
}

// MarkAsFailed ports the route-invoked
// `FailedDownloadService.MarkAsFailed(int historyId)` overload at
// `FailedDownloadService.cs:34-61`. Loads the history row, builds
// the DownloadFailedEvent, publishes via the bus. Returns
// history.ErrNotFound when the id doesn't match a row.
func (s *Service) MarkAsFailed(ctx context.Context, historyID int) error {
	if s == nil {
		return errors.New("faileddownloads: MarkAsFailed on nil receiver")
	}
	entry, err := s.history.Get(ctx, historyID)
	if err != nil {
		return err
	}
	q, err := decodeQuality(entry.Quality)
	if err != nil {
		return fmt.Errorf("faileddownloads: decode quality for history %d: %w", historyID, err)
	}
	ev := trackeddownloads.DownloadFailedEvent{
		SeriesID:       entry.SeriesID,
		EpisodeIDs:     []int{entry.EpisodeID},
		Quality:        q,
		SourceTitle:    entry.SourceTitle,
		DownloadClient: entry.Data["DownloadClient"],
		DownloadID:     entry.DownloadID,
		Message:        "Manually marked as failed",
		Source:         "ManualOverride",
		Data:           entry.Data,
		Languages:      decodeLanguages(entry.Languages),
		ReleaseSource:  decisionengine.ReleaseSourceUnknown,
	}
	if err := messaging.Publish(ctx, s.bus, ev); err != nil {
		return fmt.Errorf("faileddownloads: publish for history %d: %w", historyID, err)
	}
	return nil
}

// decodeQuality unmarshals the persisted-row Quality JSON into a
// quality.Model. Empty / whitespace-only input collapses to the
// zero-value Model (matches the C# code path where a NULL or empty
// column produces a default-constructed QualityModel).
func decodeQuality(raw string) (quality.Model, error) {
	if raw == "" {
		return quality.Model{}, nil
	}
	var m quality.Model
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return quality.Model{}, err
	}
	return m, nil
}

// decodeLanguages maps the persisted []int onto []language.Language
// via the registry. Unknown ids are silently dropped (the C# code
// also tolerates legacy / removed languages by skipping rather than
// erroring; matches the lookup-failure path at `LanguageHelper`).
func decodeLanguages(ids []int) []language.Language {
	if len(ids) == 0 {
		return nil
	}
	out := make([]language.Language, 0, len(ids))
	for _, id := range ids {
		if l, ok := language.FindByID(id); ok {
			out = append(out, l)
		}
	}
	return out
}
