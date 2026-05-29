// Package tagdetail ports the read side of C# `TagService.Details()` /
// `TagService.Details(int)`
// (`src/NzbDrone.Core/Tags/TagService.cs:88-152`) — the tag-usage
// aggregator backing GET /api/v3+v5/tag/detail.
//
// For each tag it reports which rows of the nine consumer tables
// (DelayProfile, ImportList, Notification, ReleaseProfile [+excluded],
// Series, Indexer, AutoTag, DownloadClient) reference that tag id.
//
// The package owns the pure per-tag join logic; the data-loading is
// supplied by the caller (cmd/sonarr) via [Loaders] closures so this
// package stays decoupled from the nine consumer packages. The C#
// `GetAutoTagIds` rule — an AutoTag is also a consumer when one of its
// specifications is a TagSpecification whose Value == the tag id — is
// applied by folding those spec values into the AutoTag entity's Tags
// at load time (see cmd/sonarr), so the generic "Tags contains id"
// filter reproduces it exactly.
package tagdetail

import "context"

// Tag is the minimal tag row the aggregator needs (id + label).
type Tag struct {
	ID    int
	Label string
}

// Entity is one tag-consuming row: its own id plus the tag ids it
// references.
type Entity struct {
	ID   int
	Tags []int
}

// Detail is the aggregated per-tag usage projection. Mirrors C#
// `TagDetails`. Every id slice is non-nil (empty ⇒ `[]int{}`) so the
// wire layer emits `[]` rather than `null`.
type Detail struct {
	ID                        int
	Label                     string
	DelayProfileIDs           []int
	ImportListIDs             []int
	NotificationIDs           []int
	RestrictionIDs            []int
	ExcludedReleaseProfileIDs []int
	IndexerIDs                []int
	DownloadClientIDs         []int
	AutoTagIDs                []int
	SeriesIDs                 []int
}

// EntityLoader loads all rows of one consumer table as [Entity]s.
type EntityLoader func(ctx context.Context) ([]Entity, error)

// Loaders bundles the data sources. Each is loaded once per
// AllTagDetails call (mirrors the C# `Details()` shape that loads
// every list once, then filters per tag). A nil loader contributes no
// rows (treated as empty).
type Loaders struct {
	Tags                    func(ctx context.Context) ([]Tag, error)
	DelayProfiles           EntityLoader
	ImportLists             EntityLoader
	Notifications           EntityLoader
	ReleaseProfiles         EntityLoader
	ExcludedReleaseProfiles EntityLoader
	Series                  EntityLoader
	Indexers                EntityLoader
	AutoTags                EntityLoader
	DownloadClients         EntityLoader
}

// Service is the tag-detail aggregator.
type Service struct {
	l Loaders
}

// NewService constructs the aggregator from the supplied loaders.
func NewService(l Loaders) *Service { return &Service{l: l} }

// loaded holds every consumer list, fetched once.
type loaded struct {
	tags            []Tag
	delayProfiles   []Entity
	importLists     []Entity
	notifications   []Entity
	releaseProfiles []Entity
	excludedRPs     []Entity
	series          []Entity
	indexers        []Entity
	autoTags        []Entity
	downloadClients []Entity
}

func loadEntities(ctx context.Context, fn EntityLoader) ([]Entity, error) {
	if fn == nil {
		return nil, nil
	}
	return fn(ctx)
}

func (s *Service) loadAll(ctx context.Context) (*loaded, error) {
	var (
		out loaded
		err error
	)
	if s.l.Tags != nil {
		if out.tags, err = s.l.Tags(ctx); err != nil {
			return nil, err
		}
	}
	if out.delayProfiles, err = loadEntities(ctx, s.l.DelayProfiles); err != nil {
		return nil, err
	}
	if out.importLists, err = loadEntities(ctx, s.l.ImportLists); err != nil {
		return nil, err
	}
	if out.notifications, err = loadEntities(ctx, s.l.Notifications); err != nil {
		return nil, err
	}
	if out.releaseProfiles, err = loadEntities(ctx, s.l.ReleaseProfiles); err != nil {
		return nil, err
	}
	if out.excludedRPs, err = loadEntities(ctx, s.l.ExcludedReleaseProfiles); err != nil {
		return nil, err
	}
	if out.series, err = loadEntities(ctx, s.l.Series); err != nil {
		return nil, err
	}
	if out.indexers, err = loadEntities(ctx, s.l.Indexers); err != nil {
		return nil, err
	}
	if out.autoTags, err = loadEntities(ctx, s.l.AutoTags); err != nil {
		return nil, err
	}
	if out.downloadClients, err = loadEntities(ctx, s.l.DownloadClients); err != nil {
		return nil, err
	}
	return &out, nil
}

// detailFor builds the Detail for one tag from the pre-loaded lists.
func detailFor(tag Tag, d *loaded) Detail {
	return Detail{
		ID:                        tag.ID,
		Label:                     tag.Label,
		DelayProfileIDs:           idsReferencing(d.delayProfiles, tag.ID),
		ImportListIDs:             idsReferencing(d.importLists, tag.ID),
		NotificationIDs:           idsReferencing(d.notifications, tag.ID),
		RestrictionIDs:            idsReferencing(d.releaseProfiles, tag.ID),
		ExcludedReleaseProfileIDs: idsReferencing(d.excludedRPs, tag.ID),
		SeriesIDs:                 idsReferencing(d.series, tag.ID),
		IndexerIDs:                idsReferencing(d.indexers, tag.ID),
		AutoTagIDs:                idsReferencing(d.autoTags, tag.ID),
		DownloadClientIDs:         idsReferencing(d.downloadClients, tag.ID),
	}
}

// AllTagDetails ports `Details()` — one Detail per tag, in the tag
// list's order (the caller's Tags loader sorts by label, matching C#
// `All().OrderBy(Label)`).
func (s *Service) AllTagDetails(ctx context.Context) ([]Detail, error) {
	d, err := s.loadAll(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Detail, 0, len(d.tags))
	for _, tag := range d.tags {
		out = append(out, detailFor(tag, d))
	}
	return out, nil
}

// TagDetail ports `Details(int tagId)` — the projection for a single
// tag. Returns (nil, false) when no tag has the given id.
func (s *Service) TagDetail(ctx context.Context, id int) (*Detail, bool, error) {
	d, err := s.loadAll(ctx)
	if err != nil {
		return nil, false, err
	}
	for _, tag := range d.tags {
		if tag.ID == id {
			detail := detailFor(tag, d)
			return &detail, true, nil
		}
	}
	return nil, false, nil
}

// idsReferencing returns the ids of every entity whose Tags contain
// tagID. Always non-nil. Order preserves the source list order
// (mirrors C# `.Where(...).Select(c => c.Id)` over the loaded list).
func idsReferencing(entities []Entity, tagID int) []int {
	out := []int{}
	for _, e := range entities {
		for _, t := range e.Tags {
			if t == tagID {
				out = append(out, e.ID)
				break
			}
		}
	}
	return out
}
