// Service for the EpisodeFiles domain. Mirrors
// `NzbDrone.Core.MediaFiles.MediaFileService` at
// `src/NzbDrone.Core/MediaFiles/MediaFileService.cs`.
//
// What this slice (Phase 3.2) ships:
//
//   - Service struct + NewService — repo + optional bus + optional
//     EpisodeProvider + optional SeriesProvider.
//   - Add(file) — Insert + publish EpisodeFileAddedEvent (with
//     linked episodes resolved via the EpisodeProvider when wired).
//   - Update(file) / UpdateMany([]*file).
//   - Delete(file, reason) — Delete + publish EpisodeFileDeletedEvent
//     (with linked episodes + parent Series resolved when wired).
//   - GetFile(id) / GetFiles(ids) / GetFilesBy{Series,SeriesIDs,Season} /
//     GetFilesWithoutMediaInfo / GetFilesWithRelativePath read accessors.
//   - FilterExistingFiles(paths, series) — diff a candidate file
//     list against on-disk rows (matches C# `:108-118`).
//   - HandleSeriesDeleted — bus-listener entry point for
//     SeriesDeletedEvent fan-out (replaces C# IHandleAsync).
//
// What's deferred:
//
//   - Path materialization on EpisodeFileDeletedEvent (the C# version
//     sets `episodeFile.Path = Path.Combine(Series.Path, RelativePath)`
//     before publishing). The Go port surfaces both `Series` and
//     `EpisodeFile` separately on the event so listeners can compute
//     it themselves; if a downstream consumer needs the
//     pre-computed Path field on the File struct, the Service can
//     populate it before publishing — for now the Path field on
//     `*File` stays as-stored (which is empty since the column
//     isn't persisted).
//   - The C# `EpisodeFile.Path` sync-on-publish quirk (`MediaFileService.cs:65`).
//     Mirrored above.
//
// Cache-less by design — matches the C# class which doesn't cache
// either (every read goes through the repo).

package episodefiles

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/series"
)

// EpisodeProvider returns the Episodes linked to a given EpisodeFile.
// Defined here (consumer side) so the episodefiles package doesn't
// take a hard dep on the episodes package's full Service API. The
// host wires `*episodes.Service` (which has GetEpisodesByFileID) via
// SetEpisodeProvider; *episodes.Service satisfies this interface
// (compile-time bridge in the test file).
type EpisodeProvider interface {
	GetEpisodesByFileID(ctx context.Context, fileID int) ([]episodes.Episode, error)
}

// SeriesProvider returns a Series by ID. Used for materializing
// EpisodeFileDeletedEvent.Series before publish. *series.Service
// satisfies this via GetSeries.
type SeriesProvider interface {
	GetSeries(ctx context.Context, id int) (*series.Series, error)
}

// Service wraps the Repo with the higher-level surface the rest of
// the app consumes — the C# `MediaFileService` analog.
type Service struct {
	repo            *Repo
	bus             *messaging.Bus
	episodeProvider EpisodeProvider
	seriesProvider  SeriesProvider
}

// NewService builds a Service. `bus` may be nil to skip publishing
// (matches the established Phase 2/3 pattern).
func NewService(r *Repo, bus *messaging.Bus) (*Service, error) {
	if r == nil {
		return nil, errors.New("episodefiles: nil repo")
	}
	return &Service{repo: r, bus: bus}, nil
}

// SetEpisodeProvider attaches the episodes-resolver used to populate
// `EpisodeFile{Added,Deleted}Event.LinkedEpisodes` before publish.
// When unset, those slices are left nil — listeners must not assume
// non-nil presence in tests/migrations.
func (s *Service) SetEpisodeProvider(p EpisodeProvider) {
	s.episodeProvider = p
}

// SetSeriesProvider attaches the series-resolver used to populate
// `EpisodeFileDeletedEvent.Series` before publish. When unset,
// the Series field is left nil.
func (s *Service) SetSeriesProvider(p SeriesProvider) {
	s.seriesProvider = p
}

// Add ports `MediaFileService.Add(EpisodeFile)` at
// `MediaFileService.cs:43-48`. Inserts the row and publishes
// EpisodeFileAddedEvent with the assigned ID and the linked
// episodes (when an EpisodeProvider is wired).
func (s *Service) Add(ctx context.Context, f *File) (*File, error) {
	if f == nil {
		return nil, errors.New("episodefiles: cannot add nil")
	}
	if _, err := s.repo.Insert(ctx, f); err != nil {
		return nil, fmt.Errorf("episodefiles: Add: %w", err)
	}
	stored, err := s.repo.Get(ctx, f.ID)
	if err != nil {
		return nil, fmt.Errorf("episodefiles: Add: refetch: %w", err)
	}
	if s.bus != nil {
		linked := s.resolveLinkedEpisodes(ctx, stored.ID)
		_ = messaging.Publish(ctx, s.bus, EpisodeFileAddedEvent{
			EpisodeFile:    stored,
			LinkedEpisodes: linked,
		})
	}
	return stored, nil
}

// Update ports `MediaFileService.Update(EpisodeFile)` at `:50-53`.
// Publishes [EpisodeFileUpdatedEvent] after a successful repo update
// (Go-port additive — see the event's doc-comment for the C#
// `ModelEvent<EpisodeFile>` divergence note). The publish is fire-
// and-forget; subscribers (SignalR broadcaster) warn-and-continue
// on their own.
func (s *Service) Update(ctx context.Context, f *File) error {
	if f == nil {
		return errors.New("episodefiles: cannot update nil")
	}
	if err := s.repo.Update(ctx, f); err != nil {
		return err
	}
	s.publishUpdated(ctx, f)
	return nil
}

// UpdateMany ports `MediaFileService.Update(List<EpisodeFile>)` at
// `:55-58`. Per-row update (the C# version uses `UpdateMany`, a
// single-batch helper; the Go port issues per-row updates because
// the generic Repository[T] doesn't yet expose a transactional bulk
// helper — same approximation as series.UpdateMany). One
// [EpisodeFileUpdatedEvent] is published per successfully-updated
// row; a mid-loop failure aborts and the events for already-updated
// rows are emitted (rolling-back the bus emissions isn't possible).
func (s *Service) UpdateMany(ctx context.Context, list []*File) error {
	for i, f := range list {
		if f == nil {
			return fmt.Errorf("episodefiles: UpdateMany: nil at idx %d", i)
		}
		if err := s.repo.Update(ctx, f); err != nil {
			return fmt.Errorf("episodefiles: UpdateMany(idx=%d,id=%d): %w", i, f.ID, err)
		}
		s.publishUpdated(ctx, f)
	}
	return nil
}

// publishUpdated emits [EpisodeFileUpdatedEvent] with the
// Series materialised (when a SeriesProvider is wired) so SignalR
// subscribers can project `Path.Combine(Series.Path, RelativePath)`
// without a per-broadcast lookup. Best-effort: when bus is nil
// (matches the established skip-publish pattern), or when the
// SeriesProvider lookup fails, the field stays nil and consumers
// fall back to their own resolution.
func (s *Service) publishUpdated(ctx context.Context, f *File) {
	if s.bus == nil {
		return
	}
	parent := s.resolveSeries(ctx, f.SeriesID)
	_ = messaging.Publish(ctx, s.bus, EpisodeFileUpdatedEvent{
		EpisodeFile: f,
		Series:      parent,
	})
}

// Delete ports `MediaFileService.Delete(EpisodeFile, DeleteMediaFileReason)`
// at `:60-68`. Resolves linked episodes + parent Series, deletes the
// row, then publishes EpisodeFileDeletedEvent. Resolution happens
// BEFORE delete so the FK doesn't drop the data we need for the
// event payload.
func (s *Service) Delete(ctx context.Context, f *File, reason DeleteMediaFileReason) error {
	if f == nil {
		return errors.New("episodefiles: cannot delete nil")
	}
	linked := s.resolveLinkedEpisodes(ctx, f.ID)
	parent := s.resolveSeries(ctx, f.SeriesID)
	if err := s.repo.Delete(ctx, f.ID); err != nil {
		return fmt.Errorf("episodefiles: Delete(%d): %w", f.ID, err)
	}
	if s.bus != nil {
		_ = messaging.Publish(ctx, s.bus, EpisodeFileDeletedEvent{
			EpisodeFile:    f,
			LinkedEpisodes: linked,
			Series:         parent,
			Reason:         reason,
		})
	}
	return nil
}

// GetFile ports `MediaFileService.Get(int)` at `:96-99`.
func (s *Service) GetFile(ctx context.Context, id int) (*File, error) {
	return s.repo.Get(ctx, id)
}

// GetFiles ports `MediaFileService.Get(IEnumerable<int>)` at `:101-104`.
func (s *Service) GetFiles(ctx context.Context, ids []int) ([]File, error) {
	return s.repo.GetMany(ctx, ids)
}

// GetFilesBySeries ports `MediaFileService.GetFilesBySeries(int)` at
// `:70-73`.
func (s *Service) GetFilesBySeries(ctx context.Context, seriesID int) ([]File, error) {
	return s.repo.AllBySeriesID(ctx, seriesID)
}

// GetFilesBySeriesIDs ports `MediaFileService.GetFilesBySeriesIds`
// at `:75-78`.
func (s *Service) GetFilesBySeriesIDs(ctx context.Context, seriesIDs []int) ([]File, error) {
	return s.repo.AllBySeriesIDs(ctx, seriesIDs)
}

// GetFilesBySeason ports `MediaFileService.GetFilesBySeason(int, int)`
// at `:80-83`.
func (s *Service) GetFilesBySeason(ctx context.Context, seriesID, seasonNumber int) ([]File, error) {
	return s.repo.AllBySeason(ctx, seriesID, seasonNumber)
}

// GetFilesWithoutMediaInfo ports `:90-93`.
func (s *Service) GetFilesWithoutMediaInfo(ctx context.Context) ([]File, error) {
	return s.repo.AllWithoutMediaInfo(ctx)
}

// GetFilesWithRelativePath ports `:106-109`.
func (s *Service) GetFilesWithRelativePath(ctx context.Context, seriesID int, relativePath string) ([]File, error) {
	return s.repo.AllWithRelativePath(ctx, seriesID, relativePath)
}

// FilterExistingFiles ports
// `MediaFileService.FilterExistingFiles(List<string>, Series)` at
// `:108-118` and the helper at `:120-130`. Walks the list of
// candidate file paths and removes any that already correspond to
// a persisted EpisodeFile under the same Series.
//
// Comparison is path-based: Series.Path + "/" + RelativePath →
// expected on-disk path → compared to the candidate via
// `filepath.Clean` to normalise separator/trailing-slash variance
// (mirrors C# `PathEqualityComparer.Instance` which case-folds on
// Windows but uses raw equality on Unix; the Go port matches Unix
// behavior via filepath.Clean).
func (s *Service) FilterExistingFiles(ctx context.Context, candidates []string, parent *series.Series) ([]string, error) {
	if parent == nil {
		return nil, errors.New("episodefiles: FilterExistingFiles: nil series")
	}
	files, err := s.repo.AllBySeriesID(ctx, parent.ID)
	if err != nil {
		return nil, fmt.Errorf("episodefiles: FilterExistingFiles: %w", err)
	}
	if len(files) == 0 {
		return candidates, nil
	}
	existing := make(map[string]struct{}, len(files))
	for _, f := range files {
		if f.RelativePath == "" {
			continue
		}
		full := filepath.Clean(filepath.Join(parent.Path, f.RelativePath))
		existing[full] = struct{}{}
	}
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if _, hit := existing[filepath.Clean(c)]; hit {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

// HandleSeriesDeleted ports
// `MediaFileService.HandleAsync(SeriesDeletedEvent)` at `:131-134`.
// Bus-listener entry point — the host registers this with
// `messaging.Subscribe(bus, "episodefiles.seriesDeleted",
// svc.HandleSeriesDeleted)`. Returns the rows-deleted count for
// telemetry; ignored by the bus.
//
// Note: the C# version doesn't publish per-file
// EpisodeFileDeletedEvent for the deleted rows — the bulk
// SeriesDeletedEvent listener fans out via direct repo DELETE.
// Replicating that here means downstream consumers (extras,
// subtitles cleanup) still need to wire their own
// SeriesDeletedEvent listeners; they don't get a derived
// EpisodeFileDeletedEvent.
func (s *Service) HandleSeriesDeleted(ctx context.Context, ev series.SeriesDeletedEvent) error {
	if len(ev.Series) == 0 {
		return nil
	}
	ids := make([]int, len(ev.Series))
	for i, ser := range ev.Series {
		ids[i] = ser.ID
	}
	if _, err := s.repo.DeleteForSeries(ctx, ids); err != nil {
		return fmt.Errorf("episodefiles: HandleSeriesDeleted: %w", err)
	}
	return nil
}

// resolveLinkedEpisodes returns the linked episodes for the given
// file ID via the wired EpisodeProvider, or nil when no provider
// is wired or the lookup fails. Failures during resolution don't
// block the caller — the linkage is best-effort metadata for the
// event payload, not load-bearing data.
func (s *Service) resolveLinkedEpisodes(ctx context.Context, fileID int) []episodes.Episode {
	if s.episodeProvider == nil {
		return nil
	}
	out, err := s.episodeProvider.GetEpisodesByFileID(ctx, fileID)
	if err != nil {
		return nil
	}
	return out
}

// resolveSeries returns the parent series for the given seriesID
// via the wired SeriesProvider, or nil when no provider is wired
// or the series no longer exists.
func (s *Service) resolveSeries(ctx context.Context, seriesID int) *series.Series {
	if s.seriesProvider == nil {
		return nil
	}
	out, err := s.seriesProvider.GetSeries(ctx, seriesID)
	if err != nil {
		return nil
	}
	return out
}

// joinPath is a tiny helper that mirrors the trim-and-join the C#
// `Path.Combine` does — exposed so callers (and tests) can
// pre-compute the same canonical path FilterExistingFiles
// compares against.
//
//nolint:unused // reserved for callers that want to pre-compute the path
func joinPath(parent, rel string) string {
	parent = strings.TrimRight(parent, "/")
	rel = strings.TrimLeft(rel, "/")
	return filepath.Clean(parent + "/" + rel)
}

// DeleteBySeriesID cascades episode-file row deletion when a series
// is removed. Bulk SQL via the repo; no per-row event emission (the
// Go port has no IHandle<EpisodeFileDeletedEvent> consumer that
// reacts to series-delete cascades — per-row delete events still
// fire from the per-file [Delete] path for the import-rename flow).
//
// IMPORTANT: DB-row cascade ONLY. The on-disk .mkv/.mp4 files are
// NOT removed here; that requires the DiskProvider port + the
// API-layer ?deleteFiles=true plumbing wired through (DEFERRED).
//
// Slice 5.D4 port pre-work — used by slice 5.D5's API-layer cascade.
func (s *Service) DeleteBySeriesID(ctx context.Context, seriesID int) (int64, error) {
	return s.repo.DeleteBySeriesID(ctx, seriesID)
}
