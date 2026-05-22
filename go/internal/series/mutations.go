package series

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/parser"
)

// PrepareForAdd ports the non-SkyHook subset of C#
// `AddSeriesService.SetPropertiesAndValidate` at
// `src/NzbDrone.Core/Tv/AddSeriesService.cs:142-167`. Mutates `s`
// in-place before persistence:
//
//  1. Sets [Series.Path] to `filepath.Join(s.RootFolderPath,
//     folderName(s))` when Path is empty AND RootFolderPath is set
//     AND `folderName` is non-nil AND returns a non-empty result.
//     Mirrors C# `:144-148` (`Path = Path.Combine(RootFolderPath,
//     _fileNameBuilder.GetSeriesFolder(newSeries))`). Callers that
//     don't have a folder-name builder available (tests, migrations)
//     pass nil to skip the arm; the row persists with empty Path
//     and the J185 folder-creation subscriber stays a no-op for
//     that row (existing C# behaviour when Path is omitted).
//
//  2. Sets [Series.CleanTitle] = [parser.CleanSeriesTitle](Title).
//     Mirrors C# `:150` (`CleanTitle = newSeries.Title.CleanSeriesTitle()`).
//     Used by series.Repo.FindByTitle to match user-entered queries
//     against the persisted row.
//
//  3. Sets [Series.SortTitle] = [NormalizeTitle](Title, TvdbID).
//     Mirrors C# `:151` (`SortTitle =
//     SeriesTitleNormalizer.Normalize(newSeries.Title, newSeries.TvdbId)`).
//     Used by the v3 API's series-list sort. The Go-port
//     NormalizeTitle delegates to parser.NormalizeTitle and
//     lowercases — same shape as C# Normalize.
//
//  4. Sets [Series.Added] to `now` (UTC) when zero. Mirrors C#
//     `:152` (`Added = DateTime.UtcNow`). Callers pass time.Now()
//     for production; tests pass a pinned time for determinism.
//
// The AddOptions-based monitor override (C# `:154-157`,
// `AddOptions.Monitor == MonitorTypes.None → Monitored = false`)
// is NOT ported here because the Go-port `series.Series` struct
// doesn't yet have an `AddOptions` field; the ImportListSync flow
// builds the Series row with `Monitored` already derived from
// `list.ShouldMonitor` (per `importlists.buildSeries` at
// `internal/importlists/syncservice.go:458-473`), so the gap is
// only a divergence for callers that PASS AddOptions explicitly
// (the v3 API series-POST path, when it lands).
//
// The validation step (C# `:159-164`,
// `_addSeriesValidator.Validate(newSeries)`) is also deferred —
// the validator's checks (path-not-already-used, root-folder-
// writability, etc.) need the rootfolders.Service + a series
// existence query; these can land as a follow-up
// `series.AddValidator` that PrepareForAdd composes with.
//
// Slice 5.J186 (closes the second piece of the C# AddSeriesService
// side-effect pipeline — J185 wired folder creation off
// SeriesAddedEvent, J186 makes the Path field non-empty for the
// import-list-add flow so that subscriber's gate actually fires).
func PrepareForAdd(s *Series, now time.Time, folderName func(*Series) string) {
	if s == nil {
		return
	}
	if s.Path == "" && s.RootFolderPath != "" && folderName != nil {
		if folder := folderName(s); folder != "" {
			s.Path = filepath.Join(s.RootFolderPath, folder)
		}
	}
	s.CleanTitle = parser.CleanSeriesTitle(s.Title)
	s.SortTitle = NormalizeTitle(s.Title, s.TvdbID)
	if s.Added == nil || s.Added.IsZero() {
		utc := now.UTC()
		s.Added = &utc
	}
}

// EpisodeMonitorCascade is the slice of `IEpisodeService` the C#
// SeriesService reaches for at `SeriesService.cs:217` —
// `_episodeService.SetEpisodeMonitoredBySeason(series.Id,
// season.SeasonNumber, season.Monitored)`.
//
// Defining the dependency here, where it's CONSUMED, lets episodes
// import series (which it already does for SeriesEditedEvent / etc.)
// while still being callable from series.UpdateSeries — the host wires
// `*episodes.Service` (which has `SetEpisodeMonitoredBySeason`) into
// the series service via [Service.SetEpisodeMonitorCascade].
//
// Returning the row count from the cascade isn't strictly necessary
// (the C# version doesn't propagate it), but it lets the caller
// surface "actually changed" vs "no-op" when telemetry needs it.
type EpisodeMonitorCascade interface {
	SetEpisodeMonitoredBySeason(ctx context.Context, seriesID, seasonNumber int, monitored bool) (int64, error)
}

// SetEpisodeMonitorCascade attaches the cascade used by
// [Service.UpdateSeries]. When unset, UpdateSeries skips the
// episode-monitored cascade entirely (matches the C# DI graph where
// the cascade is mandatory; the Go port treats it as optional so
// tests and migrations don't need a stub).
func (s *Service) SetEpisodeMonitorCascade(c EpisodeMonitorCascade) {
	s.episodes = c
}

// seasonMonitor is the minimal Season-shape used to compare
// monitored-state between stored and new series rows. Mirrors the
// `seasonNumber` + `monitored` fields of `NzbDrone.Core.Tv.Season`;
// other fields (Images, etc.) are ignored — any field not in this
// struct passes through the JSON round-trip via `json.RawMessage` in
// the caller (we deserialize only what we need to compare).
type seasonMonitor struct {
	SeasonNumber int  `json:"seasonNumber"`
	Monitored    bool `json:"monitored"`
}

// decodeSeasons unmarshals a Series.Seasons JSON blob into the
// monitor-comparison shape. Empty / whitespace-only input is treated
// as zero seasons (matches C# `null Seasons` collection iteration).
func decodeSeasons(blob string) ([]seasonMonitor, error) {
	if blob == "" {
		return nil, nil
	}
	var out []seasonMonitor
	if err := json.Unmarshal([]byte(blob), &out); err != nil {
		return nil, fmt.Errorf("series: decode Seasons JSON: %w", err)
	}
	return out, nil
}

// AddSeries ports `SeriesService.AddSeries(Series newSeries)` at
// `SeriesService.cs:73-79`. Inserts the row, re-loads the persisted
// shape (the C# version calls `GetSeries(newSeries.Id)` to surface
// any DB-side defaults) and publishes [SeriesAddedEvent].
//
// On nil bus, the publish is silently skipped (matches the established
// Phase 2 pattern across the other services).
func (s *Service) AddSeries(ctx context.Context, ns *Series) (*Series, error) {
	if ns == nil {
		return nil, errors.New("series: cannot add nil series")
	}
	if _, err := s.repo.Insert(ctx, ns); err != nil {
		return nil, fmt.Errorf("series: AddSeries: insert: %w", err)
	}
	// Re-fetch to pick up any DB-side defaults (e.g. NULL columns
	// coerced through the Scan path). Mirrors C# `GetSeries(newSeries.Id)`.
	stored, err := s.repo.Get(ctx, ns.ID)
	if err != nil {
		return nil, fmt.Errorf("series: AddSeries: refetch: %w", err)
	}
	if s.bus != nil {
		_ = messaging.Publish(ctx, s.bus, SeriesAddedEvent{Series: stored})
	}
	return stored, nil
}

// TagChangeProvider is the slice of `IAutoTaggingService` the C#
// SeriesService reaches for at `:281` (`_autoTaggingService.GetTagChanges(series)`).
//
// Returning `(toAdd, toRemove map[int]struct{}, err)` matches the
// `AutoTaggingChanges` shape exposed by the autotagging package
// without dragging in autotagging or rootfolders as series-package
// imports (which would create a series ↔ autotagging cycle —
// autotagging already imports series).
//
// The host wires this with a closure adapter that:
//
//  1. Calls autotagging.Service.All(ctx) for the AutoTags.
//  2. Calls a RootFolderProvider for the rootfolders.Folder list.
//  3. Calls autotagging.ChangesFor(series, autoTags, rootFolders).
//  4. Returns the resulting TagsToAdd/TagsToRemove sets.
type TagChangeProvider interface {
	GetTagChanges(ctx context.Context, s *Series) (toAdd, toRemove map[int]struct{}, err error)
}

// SetTagChangeProvider attaches the provider used by [Service.UpdateTags]
// (and, by extension, [Service.UpdateSeries] which calls UpdateTags
// per `:225`). When unset, UpdateTags is a no-op (matches the C# DI
// graph where the provider is mandatory; the Go port treats it as
// optional so tests and migrations don't need a stub).
func (s *Service) SetTagChangeProvider(p TagChangeProvider) {
	s.tagProvider = p
}

// UpdateTags ports `SeriesService.UpdateTags(Series series)` at
// `:275-311`. Computes the auto-tagging delta for `series`, applies
// it in place to `series.Tags`, and returns `(changed, err)`.
//
// `changed=true` is returned iff at least one tag was added or
// removed — mirrors the C# `tagsAdded.Any() || tagsRemoved.Any()`
// guard at `:301`.
//
// If no [TagChangeProvider] is set, returns `(false, nil)` —
// matches the "no-op when DI didn't wire it" tolerance.
func (s *Service) UpdateTags(ctx context.Context, series *Series) (bool, error) {
	if series == nil {
		return false, errors.New("series: cannot UpdateTags on nil series")
	}
	if s.tagProvider == nil {
		return false, nil
	}
	toAdd, toRemove, err := s.tagProvider.GetTagChanges(ctx, series)
	if err != nil {
		return false, fmt.Errorf("series: UpdateTags: %w", err)
	}

	existing := make(map[int]bool, len(series.Tags))
	for _, t := range series.Tags {
		existing[t] = true
	}

	changed := false
	// Apply removals first (mirrors C# `:283-290`).
	if len(toRemove) > 0 {
		next := make([]int, 0, len(series.Tags))
		for _, t := range series.Tags {
			if _, drop := toRemove[t]; drop {
				changed = true
				delete(existing, t)
				continue
			}
			next = append(next, t)
		}
		series.Tags = next
	}
	// Apply additions (mirrors C# `:292-299`). Only adds tags that
	// aren't already present.
	for tag := range toAdd {
		if existing[tag] {
			continue
		}
		series.Tags = append(series.Tags, tag)
		existing[tag] = true
		changed = true
	}
	return changed, nil
}

// UpdateOptions encapsulates the optional flags `SeriesService.UpdateSeries`
// takes in C# (`updateEpisodesToMatchSeason`, `publishUpdatedEvent`).
// Zero value = the C# defaults: episode-monitored cascade ON,
// publishing ON.
type UpdateOptions struct {
	// SkipEpisodeMonitoredCascade flips the C#
	// `updateEpisodesToMatchSeason` flag (default true): when set,
	// season-monitored toggle propagation to episodes is skipped.
	// Used by EpisodeMonitoredService to avoid cascade loops.
	SkipEpisodeMonitoredCascade bool

	// SkipPublishUpdatedEvent flips the C# `publishUpdatedEvent`
	// flag (default true): when set, no SeriesEditedEvent is fired.
	SkipPublishUpdatedEvent bool
}

// UpdateSeries ports `SeriesService.UpdateSeries(Series, bool, bool)` at
// `SeriesService.cs:203-234`.
//
// Order of operations (line-aligned with C#):
//
//  1. Load stored series (`:205`).
//  2. If cascade enabled AND an [EpisodeMonitorCascade] is wired:
//     for each Season in `ns.Seasons`, find the matching stored
//     Season and call `episodes.SetEpisodeMonitoredBySeason` when
//     the Monitored flag changed. Track whether any cascade hit
//     applied actual row changes — surfaces via the published
//     `SeriesEditedEvent.EpisodesChanged` flag (`:209-221`).
//  3. Restore `AddOptions` from stored (`:224`).
//  4. Apply auto-tagging delta in place (`:225`).
//  5. Persist via repo (`:227`).
//  6. If publishing enabled, fire SeriesEditedEvent (`:229-232`).
//
// Cascade tolerances:
//
//   - When [SetEpisodeMonitorCascade] is NOT wired, the season loop
//     is skipped silently; EpisodesChanged stays false. Tests and
//     migrations can omit the cascade.
//   - Empty/missing Seasons JSON in either side is treated as zero
//     seasons (no error).
//   - JSON-decode failures on the Seasons column are returned —
//     fail-closed rather than silently skip the cascade.
//
// Returns the updated row (re-loaded from the DB).
func (s *Service) UpdateSeries(ctx context.Context, ns *Series, opts UpdateOptions) (*Series, error) {
	if ns == nil {
		return nil, errors.New("series: cannot update nil series")
	}
	stored, err := s.repo.Get(ctx, ns.ID)
	if err != nil {
		return nil, fmt.Errorf("series: UpdateSeries: load stored: %w", err)
	}

	episodesChanged := false
	if !opts.SkipEpisodeMonitoredCascade && s.episodes != nil {
		newSeasons, err := decodeSeasons(ns.Seasons)
		if err != nil {
			return nil, fmt.Errorf("series: UpdateSeries: new seasons: %w", err)
		}
		storedSeasons, err := decodeSeasons(stored.Seasons)
		if err != nil {
			return nil, fmt.Errorf("series: UpdateSeries: stored seasons: %w", err)
		}
		// Index stored seasons by SeasonNumber for the C# `SingleOrDefault` lookup.
		storedByNum := make(map[int]seasonMonitor, len(storedSeasons))
		for _, sn := range storedSeasons {
			storedByNum[sn.SeasonNumber] = sn
		}
		for _, sn := range newSeasons {
			storedSn, ok := storedByNum[sn.SeasonNumber]
			if !ok || storedSn.Monitored == sn.Monitored {
				continue
			}
			if _, err := s.episodes.SetEpisodeMonitoredBySeason(ctx, ns.ID, sn.SeasonNumber, sn.Monitored); err != nil {
				return nil, fmt.Errorf("series: UpdateSeries: cascade season=%d: %w", sn.SeasonNumber, err)
			}
			// The C# version unconditionally sets `episodeMonitoredChanged = true`
			// once any season's flag toggles, regardless of whether the
			// cascade actually flipped any episode rows. Match that — the
			// EpisodesChanged signal is "the user toggled a season",
			// not "the cascade changed at least one row".
			episodesChanged = true
		}
	}

	// AddOptions is never updated through this path — preserve the
	// stored value verbatim. Mirrors C# `:224`
	// (`series.AddOptions = storedSeries.AddOptions;`).
	ns.AddOptions = stored.AddOptions

	// Apply auto-tagging delta in-place when a provider is wired.
	// Mirrors C# `:225` (`UpdateTags(series);`). The bool result is
	// dropped here — the C# version uses it only for logging.
	if _, err := s.UpdateTags(ctx, ns); err != nil {
		return nil, fmt.Errorf("series: UpdateSeries: tags: %w", err)
	}

	if err := s.repo.Update(ctx, ns); err != nil {
		return nil, fmt.Errorf("series: UpdateSeries: persist: %w", err)
	}
	updated, err := s.repo.Get(ctx, ns.ID)
	if err != nil {
		return nil, fmt.Errorf("series: UpdateSeries: refetch: %w", err)
	}
	if !opts.SkipPublishUpdatedEvent && s.bus != nil {
		_ = messaging.Publish(ctx, s.bus, SeriesEditedEvent{
			Series:          updated,
			OldSeries:       stored,
			EpisodesChanged: episodesChanged,
		})
	}
	return updated, nil
}

// RemoveAddOptions ports `SeriesService.RemoveAddOptions(Series)` at
// `:270-273`. The C# version uses
// `_seriesRepository.SetFields(series, s => s.AddOptions)` —
// a generic-repo "update only these columns" helper that the Go
// port doesn't have yet. We approximate with a Get + targeted
// modify + Update; the field-set guarantees only AddOptions is
// touched at the SQL layer.
//
// No event published (the C# version doesn't either).
func (s *Service) RemoveAddOptions(ctx context.Context, seriesID int) error {
	stored, err := s.repo.Get(ctx, seriesID)
	if err != nil {
		return fmt.Errorf("series: RemoveAddOptions: load: %w", err)
	}
	stored.AddOptions = ""
	if err := s.repo.Update(ctx, stored); err != nil {
		return fmt.Errorf("series: RemoveAddOptions: persist: %w", err)
	}
	return nil
}

// SeriesPathBuilder is the slice of `IBuildSeriesPaths` the C#
// SeriesService reaches for in the bulk-update path at `:246`. Mirrors
// `_seriesPathBuilder.BuildPath(series, useExistingRelativeFolder)`.
//
// `series.RootFolderPath` is the input; the builder produces an
// absolute Path by combining the root folder with a series-specific
// folder name (typically derived from `naming_config.SeriesFolderFormat`
// applied to the series + year). The full implementation lands with
// the Organizer/FileNameBuilder port; until then, callers can pass
// nil — UpdateMany then skips the per-row path rebuild and just
// persists the caller-supplied Path verbatim.
type SeriesPathBuilder interface {
	BuildPath(ctx context.Context, s *Series, useExistingRelativeFolder bool) (string, error)
}

// SetSeriesPathBuilder attaches the builder used by [Service.UpdateMany].
// When unset, UpdateMany skips path rebuilding (matches C#
// `if (!s.RootFolderPath.IsNullOrWhiteSpace()) ... else ...`
// short-circuit).
func (s *Service) SetSeriesPathBuilder(b SeriesPathBuilder) {
	s.pathBuilder = b
}

// UpdateMany ports `SeriesService.UpdateSeries(List<Series>, bool)` at
// `SeriesService.cs:236-263`. For each series:
//
//  1. If `s.RootFolderPath` is non-empty AND a [SeriesPathBuilder] is
//     wired, rebuild `s.Path` via the builder. Mirrors C# `:244-252`.
//  2. Apply auto-tagging delta in place via [Service.UpdateTags].
//  3. Persist via per-row Repo.Update (the generic Repo doesn't
//     expose UpdateMany; per-row in a loop is functionally
//     equivalent without the C# transaction).
//
// Publishes a single [SeriesBulkEditedEvent] carrying the updated
// list — mirrors the C# single-event-per-bulk semantic at `:260`.
//
// Implementation note: the C# `UpdateMany` is transactional (single
// SQL batch); the Go port issues per-row Updates because the generic
// Repo doesn't yet expose a transactional bulk helper. Worth
// revisiting when migrations 1.x bulk-update workloads land.
func (s *Service) UpdateMany(ctx context.Context, list []*Series, useExistingRelativeFolder bool) ([]*Series, error) {
	if len(list) == 0 {
		return nil, nil
	}
	for _, ns := range list {
		if ns == nil {
			return nil, errors.New("series: UpdateMany: encountered nil series")
		}
		if ns.RootFolderPath != "" && s.pathBuilder != nil {
			path, err := s.pathBuilder.BuildPath(ctx, ns, useExistingRelativeFolder)
			if err != nil {
				return nil, fmt.Errorf("series: UpdateMany: BuildPath %q: %w", ns.Title, err)
			}
			ns.Path = path
		}
		if _, err := s.UpdateTags(ctx, ns); err != nil {
			return nil, fmt.Errorf("series: UpdateMany: tags %q: %w", ns.Title, err)
		}
		if err := s.repo.Update(ctx, ns); err != nil {
			return nil, fmt.Errorf("series: UpdateMany: persist %q: %w", ns.Title, err)
		}
	}
	if s.bus != nil {
		out := make([]Series, 0, len(list))
		for _, ns := range list {
			out = append(out, *ns)
		}
		_ = messaging.Publish(ctx, s.bus, SeriesBulkEditedEvent{Series: out})
	}
	return list, nil
}

// DeleteSeries ports `SeriesService.DeleteSeries(List<int>, bool, bool)`
// at `SeriesService.cs:163-168`. Loads each series before delete (so
// the published event carries the deleted rows), then deletes via
// per-id Repo.Delete, then publishes [SeriesDeletedEvent].
//
// Missing IDs are silently dropped from the load step (matches the
// C# `_seriesRepository.Get(seriesIds)` shape, which returns only the
// matching rows). The Delete step is a per-id loop; idempotent
// double-deletes (ErrNotFound) are tolerated.
func (s *Service) DeleteSeries(ctx context.Context, seriesIDs []int, deleteFiles, addImportListExclusion bool) error {
	if len(seriesIDs) == 0 {
		return nil
	}
	rows := make([]Series, 0, len(seriesIDs))
	for _, id := range seriesIDs {
		row, err := s.repo.Get(ctx, id)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return fmt.Errorf("series: DeleteSeries: load %d: %w", id, err)
		}
		rows = append(rows, *row)
	}
	for _, id := range seriesIDs {
		if err := s.repo.Delete(ctx, id); err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return fmt.Errorf("series: DeleteSeries: delete %d: %w", id, err)
		}
	}
	if s.bus != nil && len(rows) > 0 {
		_ = messaging.Publish(ctx, s.bus, SeriesDeletedEvent{
			Series:                 rows,
			DeleteFiles:            deleteFiles,
			AddImportListExclusion: addImportListExclusion,
		})
	}
	return nil
}

// AddMany ports `SeriesService.AddSeries(List<Series> newSeries)` at
// `:81-87`. The C# version uses `_seriesRepository.InsertMany(...)`
// (one transaction); the Go port issues per-row Inserts since the
// generic `*repo.Repository` doesn't yet expose a bulk-insert helper.
//
// For typical batch sizes (import-list CSV imports of dozens of
// shows) the round-trip cost is acceptable; the C# transactional
// semantics matter more for tens of thousands. Worth revisiting when
// migrations 1.x land that need bulk inserts.
//
// Publishes a single [SeriesImportedEvent] carrying every assigned ID
// — mirrors C# `:84` exactly.
func (s *Service) AddMany(ctx context.Context, list []*Series) ([]*Series, error) {
	if len(list) == 0 {
		return nil, nil
	}
	ids := make([]int, 0, len(list))
	for _, ns := range list {
		if ns == nil {
			return nil, errors.New("series: AddMany: encountered nil series")
		}
		if _, err := s.repo.Insert(ctx, ns); err != nil {
			return nil, fmt.Errorf("series: AddMany: insert (idx=%d): %w", len(ids), err)
		}
		ids = append(ids, ns.ID)
	}
	if s.bus != nil {
		_ = messaging.Publish(ctx, s.bus, SeriesImportedEvent{SeriesIDs: ids})
	}
	return list, nil
}
