package importlistitems

import (
	"context"
	"errors"
	"fmt"
)

// Service ports the C# `ImportListItemService` at
// `src/NzbDrone.Core/ImportLists/ImportListItems/ImportListItemService.cs:17-112`.
//
// It owns the per-list reconciliation of the ImportListItems table:
// after a list's Fetch returns its current candidate set, SyncSeriesForList
// diffs that set against the persisted rows for the list (insert new,
// update matched, delete vanished) and reports how many rows were removed.
// The removed count drives the import-list status service's
// HasRemovedItemSinceLastClean flag (queue [I4c] — clean-library trigger).
//
// All() (cross-list) is the read CleanLibrary walks to decide which
// library series are no longer on any list. It is inherited from the
// embedded repo.Repository[Item].Service surfaces it explicitly so
// consumers depend on the Service, not the Repo.
type Service struct {
	repo *Repo
}

// NewService binds a Repo into a Service.
func NewService(r *Repo) (*Service, error) {
	if r == nil {
		return nil, errors.New("importlistitems: NewService: nil repo")
	}
	return &Service{repo: r}, nil
}

// All ports C# `ImportListItemService.All()` at `:64-67` — every row
// across every list. CleanLibrary uses this to test each library series
// against the union of all list items.
func (s *Service) All(ctx context.Context) ([]Item, error) {
	return s.repo.All(ctx)
}

// SyncSeriesForList ports C# `ImportListItemService.SyncSeriesForList`
// at `:26-62`. Diffs the freshly-fetched candidate set (listSeries)
// against the rows persisted for listID:
//
//   - an item with no matching existing row → insert.
//   - an item matching an existing row → update that row's mutable
//     fields (the existing row is consumed so it can't match twice).
//   - any existing row left unmatched after the walk → delete.
//
// Returns the number of rows deleted (the "removed" count C# feeds into
// UpdateListSyncStatus). Each inserted/updated row's ImportListID is
// forced to listID so a caller can pass items without stamping it.
func (s *Service) SyncSeriesForList(ctx context.Context, listSeries []Item, listID int) (int, error) {
	existing, err := s.repo.AllByImportListID(ctx, listID)
	if err != nil {
		return 0, fmt.Errorf("importlistitems: syncSeriesForList(%d): load existing: %w", listID, err)
	}

	matched := make([]bool, len(existing))
	var toAdd []*Item
	var toUpdate []*Item

	for i := range listSeries {
		item := listSeries[i] // value copy
		idx := findItemIndex(existing, matched, item)
		if idx < 0 {
			it := item
			it.ImportListID = listID
			toAdd = append(toAdd, &it)
			continue
		}

		// Remove from the candidate pool (so we'll only be left with
		// rows to delete at the end) and copy the fresh fields onto the
		// existing row — mirrors C# `:43-54`.
		matched[idx] = true
		e := &existing[idx]
		e.ImportListID = listID
		e.Title = item.Title
		e.Year = item.Year
		e.TvdbID = item.TvdbID
		e.ImdbID = item.ImdbID
		e.TmdbID = item.TmdbID
		e.MalID = item.MalID
		e.AniListID = item.AniListID
		e.ReleaseDate = item.ReleaseDate
		toUpdate = append(toUpdate, e)
	}

	var toDelete []Item
	for i := range existing {
		if !matched[i] {
			toDelete = append(toDelete, existing[i])
		}
	}

	if err := s.repo.InsertMany(ctx, toAdd); err != nil {
		return 0, fmt.Errorf("importlistitems: syncSeriesForList(%d): insert: %w", listID, err)
	}
	if err := s.repo.UpdateMany(ctx, toUpdate); err != nil {
		return 0, fmt.Errorf("importlistitems: syncSeriesForList(%d): update: %w", listID, err)
	}
	if err := s.repo.DeleteMany(ctx, toDelete); err != nil {
		return 0, fmt.Errorf("importlistitems: syncSeriesForList(%d): delete: %w", listID, err)
	}

	return len(toDelete), nil
}

// DeleteForList ports C# `ImportListItemService.HandleAsync(
// ProviderDeletedEvent<IImportList>)` at `:74-78` — when an import list
// is deleted, drop every candidate row it produced.
func (s *Service) DeleteForList(ctx context.Context, listID int) error {
	if _, err := s.repo.DeleteByImportListID(ctx, listID); err != nil {
		return fmt.Errorf("importlistitems: deleteForList(%d): %w", listID, err)
	}
	return nil
}

// findItemIndex ports C# `ImportListItemService.FindItem` at `:80-111`.
// Returns the index of the first not-yet-matched existing row that
// matches item on any external identifier, or -1. Each comparison is
// guarded so a zero/blank id on either side never matches — except the
// behaviour is faithful to the C# guards (TvdbId/TmdbId/MalId/AniListId
// require both sides > 0; ImdbId requires both sides non-blank).
func findItemIndex(existing []Item, matched []bool, item Item) int {
	for i := range existing {
		if matched[i] {
			continue
		}
		e := existing[i]
		if e.TvdbID > 0 && item.TvdbID > 0 && e.TvdbID == item.TvdbID {
			return i
		}
		if ev := strVal(e.ImdbID); ev != "" {
			if iv := strVal(item.ImdbID); iv != "" && ev == iv {
				return i
			}
		}
		if ev := intVal(e.TmdbID); ev > 0 && intVal(item.TmdbID) == ev {
			return i
		}
		if ev := intVal(e.MalID); ev > 0 && intVal(item.MalID) == ev {
			return i
		}
		if ev := intVal(e.AniListID); ev > 0 && intVal(item.AniListID) == ev {
			return i
		}
	}
	return -1
}

func intVal(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func strVal(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
