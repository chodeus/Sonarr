package importlists

import (
	"context"
	"log/slog"

	"sonarr.tv/sonarr/internal/series"
)

// This file ports the C# clean-library arc of `ImportListSyncService`
// (`src/NzbDrone.Core/ImportLists/ImportListSyncService.cs:278-341`):
// after a successful all-lists sync (or an import-list delete), if the
// ListSyncLevel is not Disabled and every list synced cleanly with at
// least one pending removal, walk the library and apply the configured
// action to each series no longer present on any list.

// ListSyncLevel constants mirror the C# `ListSyncLevelType` enum
// (`src/NzbDrone.Core/Configuration/ListSyncLevelType.cs`). They are the
// string values config.ListSyncLevel() returns.
const (
	listSyncLevelDisabled         = "Disabled"
	listSyncLevelLogOnly          = "LogOnly"
	listSyncLevelKeepAndUnmonitor = "KeepAndUnmonitor"
	listSyncLevelKeepAndTag       = "KeepAndTag"
)

// ListDeletedEvent is the Go analogue of the C# generic
// `ProviderDeletedEvent<IImportList>` (ThingiProvider.Events). It is
// published when an import list is deleted (single or bulk) so the
// three C# IHandleAsync handlers can run: delete the list's persisted
// items (importlistitems), delete its status row (importliststatus),
// and TryCleanLibrary (this service). ProviderID is the deleted list's
// row id.
type ListDeletedEvent struct {
	ProviderID int
}

// HandleImportListDeleted ports C#
// `ImportListSyncService.HandleAsync(ProviderDeletedEvent<IImportList>)`
// at `ImportListSyncService.cs:338-341` — it simply runs TryCleanLibrary.
// Subscribed to [ListDeletedEvent] in main.go.
func (s *SyncService) HandleImportListDeleted(ctx context.Context) {
	s.tryCleanLibrary(ctx)
}

// cleanLibraryConfig reads the clean-library mode + tag. Satisfied by
// *config.Provider (ListSyncLevel/ListSyncTag).
type cleanLibraryConfig interface {
	ListSyncLevel() string
	ListSyncTag() int
}

// LibraryItem is the minimal projection of one persisted import-list
// candidate CleanLibrary tests each series against. The concrete
// adapter maps internal/importlistitems.Item → this (NULL ids → 0/""),
// keeping this package free of that dependency. The zero defaults are
// deliberate: they replicate the C# match where `ImportListItemInfo`'s
// unset int ids are 0.
type LibraryItem struct {
	TvdbID    int
	ImdbID    string
	TmdbID    int
	MalID     int
	AniListID int
}

// libraryItemLister returns the union of every list's persisted
// candidates. Satisfied by the importlistitems.Service adapter.
type libraryItemLister interface {
	AllItems(ctx context.Context) ([]LibraryItem, error)
}

// librarySeriesCleaner reads every library series and persists the
// unmonitor/tag mutations. Satisfied by a *series.Service adapter
// (GetAllSeries + UpdateMany).
type librarySeriesCleaner interface {
	AllSeries(ctx context.Context) ([]series.Series, error)
	UpdateSeries(ctx context.Context, batch []series.Series) error
}

// cleanStatusGate gates the clean (AllListsSuccessfulWithAPendingClean)
// and finalizes it (MarkListsAsCleaned). Satisfied by
// *importliststatus.Service.
type cleanStatusGate interface {
	AllListsSuccessfulWithAPendingClean(ctx context.Context, listIDs []int) (bool, error)
	MarkListsAsCleaned(ctx context.Context) error
}

// tryCleanLibrary ports C# `ImportListSyncService.TryCleanLibrary` at
// `:278-289`. No-op unless the clean-library deps are wired AND the
// ListSyncLevel is not Disabled AND every auto-add list synced cleanly
// with at least one pending removal.
func (s *SyncService) tryCleanLibrary(ctx context.Context) {
	if s.cleanConfig == nil || s.itemLister == nil || s.seriesCleaner == nil || s.cleanGate == nil {
		return
	}
	if s.cleanConfig.ListSyncLevel() == listSyncLevelDisabled {
		return
	}

	listIDs, err := s.autoAddListIDs(ctx)
	if err != nil {
		s.logger.Warn("importlists: clean: load auto-add lists failed", slog.Any("err", err))
		return
	}

	pending, err := s.cleanGate.AllListsSuccessfulWithAPendingClean(ctx, listIDs)
	if err != nil {
		s.logger.Warn("importlists: clean: pending-clean gate failed", slog.Any("err", err))
		return
	}
	if pending {
		s.cleanLibrary(ctx)
	}
}

// cleanLibrary ports C# `ImportListSyncService.CleanLibrary` at
// `:291-336`. For every library series not found on any list, applies
// the configured ListSyncLevel action: LogOnly logs; KeepAndUnmonitor
// unmonitors a still-monitored series; KeepAndTag tags an as-yet-untagged
// series. The mutated rows are persisted via UpdateSeries and every
// list's pending-clean flag is then cleared.
//
// SAFETY: this never deletes series rows — C# only unmonitors or tags,
// and so does the Go port. The double ListSyncLevel check (here and in
// tryCleanLibrary) is faithful to the C# re-check at `:293-296`.
func (s *SyncService) cleanLibrary(ctx context.Context) {
	level := s.cleanConfig.ListSyncLevel()
	if level == listSyncLevelDisabled {
		return
	}

	seriesInLibrary, err := s.seriesCleaner.AllSeries(ctx)
	if err != nil {
		s.logger.Warn("importlists: clean: load series failed", slog.Any("err", err))
		return
	}
	allItems, err := s.itemLister.AllItems(ctx)
	if err != nil {
		s.logger.Warn("importlists: clean: load list items failed", slog.Any("err", err))
		return
	}

	var toUpdate []series.Series
	for i := range seriesInLibrary {
		sr := seriesInLibrary[i]
		if seriesOnAnyList(sr, allItems) {
			continue
		}

		switch level {
		case listSyncLevelLogOnly:
			s.logger.Info("importlists: clean: series in library but not on any list — consider unmonitoring or removing",
				slog.Int("series_id", sr.ID), slog.String("title", sr.Title))
		case listSyncLevelKeepAndUnmonitor:
			if sr.Monitored {
				s.logger.Info("importlists: clean: series not on any list — keeping but unmonitoring",
					slog.Int("series_id", sr.ID), slog.String("title", sr.Title))
				sr.Monitored = false
				toUpdate = append(toUpdate, sr)
			}
		case listSyncLevelKeepAndTag:
			tag := s.cleanConfig.ListSyncTag()
			if !containsInt(sr.Tags, tag) {
				s.logger.Info("importlists: clean: series not on any list — keeping but tagging",
					slog.Int("series_id", sr.ID), slog.String("title", sr.Title), slog.Int("tag", tag))
				sr.Tags = append(append([]int(nil), sr.Tags...), tag)
				toUpdate = append(toUpdate, sr)
			}
		}
	}

	if err := s.seriesCleaner.UpdateSeries(ctx, toUpdate); err != nil {
		s.logger.Warn("importlists: clean: update series failed", slog.Any("err", err))
		return
	}
	if err := s.cleanGate.MarkListsAsCleaned(ctx); err != nil {
		s.logger.Warn("importlists: clean: mark-cleaned failed", slog.Any("err", err))
	}
}

// seriesOnAnyList ports the C# match predicate at `:304-309`. A series
// counts as "still on a list" if any list item shares its TvdbId, a
// non-blank ImdbId, its TmdbId, a MalId, or an AniListId.
//
// FAITHFUL C# QUIRK: TvdbId and TmdbId are compared with a bare `==`
// (no >0 guard, unlike ImdbId/MalId/AniListId). So a series with
// TmdbId==0 (TMDb unknown) matches any list item that also has TmdbId==0
// — which is common, since most providers don't resolve TMDb. The net
// effect is conservative (more series are kept), erring on the side of
// NOT touching user state — the desired safety posture.
func seriesOnAnyList(sr series.Series, items []LibraryItem) bool {
	for _, l := range items {
		if l.TvdbID == sr.TvdbID {
			return true
		}
		if l.ImdbID != "" && sr.ImdbID != "" && l.ImdbID == sr.ImdbID {
			return true
		}
		if l.TmdbID == sr.TmdbID {
			return true
		}
		if l.MalID > 0 && containsInt(sr.MalIDs, l.MalID) {
			return true
		}
		if l.AniListID > 0 && containsInt(sr.AniListIDs, l.AniListID) {
			return true
		}
	}
	return false
}

// autoAddListIDs returns the ids of every auto-add-enabled list,
// unfiltered by back-off state — mirrors the C#
// `_importListFactory.AutomaticAddEnabled(false)` at
// `ImportListSyncService.cs:60`.
func (s *SyncService) autoAddListIDs(ctx context.Context) ([]int, error) {
	providers, err := s.factory.AutomaticAddEnabled(ctx, false)
	if err != nil {
		return nil, err
	}
	ids := make([]int, 0, len(providers))
	for _, p := range providers {
		if def := p.Definition(); def != nil {
			ids = append(ids, def.ID)
		}
	}
	return ids, nil
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
