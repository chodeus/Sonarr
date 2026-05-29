package main

import (
	"context"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/autotagging"
	"sonarr.tv/sonarr/internal/delayprofiles"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/importlists"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/releaseprofiles"
	"sonarr.tv/sonarr/internal/series"
	"sonarr.tv/sonarr/internal/tagdetail"
	"sonarr.tv/sonarr/internal/tags"
)

// tagDetailAdapter wraps the *tagdetail.Service aggregator (G9) and
// projects its neutral [tagdetail.Detail] rows onto the wire
// [apiv3.TagDetailsResource], satisfying apiv3.TagDetailLister +
// apiv3.TagDetailByIDGetter.
type tagDetailAdapter struct {
	svc *tagdetail.Service
}

func (a tagDetailAdapter) AllTagDetails(ctx context.Context) ([]apiv3.TagDetailsResource, error) {
	rows, err := a.svc.AllTagDetails(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]apiv3.TagDetailsResource, 0, len(rows))
	for _, d := range rows {
		out = append(out, tagDetailToResource(d))
	}
	return out, nil
}

func (a tagDetailAdapter) TagDetail(ctx context.Context, id int) (*apiv3.TagDetailsResource, error) {
	d, ok, err := a.svc.TagDetail(ctx, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apiv3.ErrTagDetailNotFound
	}
	res := tagDetailToResource(*d)
	return &res, nil
}

func tagDetailToResource(d tagdetail.Detail) apiv3.TagDetailsResource {
	return apiv3.TagDetailsResource{
		ID:                        d.ID,
		Label:                     d.Label,
		DelayProfileIDs:           d.DelayProfileIDs,
		ImportListIDs:             d.ImportListIDs,
		NotificationIDs:           d.NotificationIDs,
		RestrictionIDs:            d.RestrictionIDs,
		ExcludedReleaseProfileIDs: d.ExcludedReleaseProfileIDs,
		IndexerIDs:                d.IndexerIDs,
		DownloadClientIDs:         d.DownloadClientIDs,
		AutoTagIDs:                d.AutoTagIDs,
		SeriesIDs:                 d.SeriesIDs,
	}
}

// newTagDetailLister builds the G9 tag-usage aggregator from the nine
// already-wired consumer services + the tag service. Returns an
// emptyTagDetailLister when the tag service is unset (defensive — the
// endpoint then keeps its zero-result shape). Mirrors C#
// TagService.Details().
func newTagDetailLister(
	tagsSvc *tags.Service,
	delayProfilesSvc *delayprofiles.Service,
	importListsRepo *importlists.Repo,
	notificationsRepo *notifications.Repo,
	releaseProfilesSvc *releaseprofiles.Service,
	seriesSvc *series.Service,
	indexersRepo *indexers.Repo,
	autoTagsSvc *autotagging.Service,
	downloadClientsRepo *downloadclients.Repo,
) interface {
	apiv3.TagDetailLister
	apiv3.TagDetailByIDGetter
} {
	if tagsSvc == nil {
		return emptyTagDetailLister{}
	}
	loaders := tagdetail.Loaders{
		Tags: func(ctx context.Context) ([]tagdetail.Tag, error) {
			rows, err := tagsSvc.All(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]tagdetail.Tag, 0, len(rows))
			for _, t := range rows {
				out = append(out, tagdetail.Tag{ID: t.ID, Label: t.Label})
			}
			return out, nil
		},
	}
	if delayProfilesSvc != nil {
		loaders.DelayProfiles = func(ctx context.Context) ([]tagdetail.Entity, error) {
			rows, err := delayProfilesSvc.All(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]tagdetail.Entity, 0, len(rows))
			for _, r := range rows {
				out = append(out, tagdetail.Entity{ID: r.ID, Tags: r.Tags})
			}
			return out, nil
		}
	}
	if importListsRepo != nil {
		loaders.ImportLists = func(ctx context.Context) ([]tagdetail.Entity, error) {
			rows, err := importListsRepo.All(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]tagdetail.Entity, 0, len(rows))
			for _, r := range rows {
				out = append(out, tagdetail.Entity{ID: r.ID, Tags: r.Tags})
			}
			return out, nil
		}
	}
	if notificationsRepo != nil {
		loaders.Notifications = func(ctx context.Context) ([]tagdetail.Entity, error) {
			rows, err := notificationsRepo.All(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]tagdetail.Entity, 0, len(rows))
			for _, r := range rows {
				out = append(out, tagdetail.Entity{ID: r.ID, Tags: r.Tags})
			}
			return out, nil
		}
	}
	if releaseProfilesSvc != nil {
		loaders.ReleaseProfiles = func(ctx context.Context) ([]tagdetail.Entity, error) {
			rows, err := releaseProfilesSvc.All(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]tagdetail.Entity, 0, len(rows))
			for _, r := range rows {
				out = append(out, tagdetail.Entity{ID: r.ID, Tags: r.Tags})
			}
			return out, nil
		}
		// ExcludedReleaseProfileIds uses the SAME rows, filtered on
		// ExcludedTags (C# `AllExcludedForTag` / `c.ExcludedTags.Contains`).
		loaders.ExcludedReleaseProfiles = func(ctx context.Context) ([]tagdetail.Entity, error) {
			rows, err := releaseProfilesSvc.All(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]tagdetail.Entity, 0, len(rows))
			for _, r := range rows {
				out = append(out, tagdetail.Entity{ID: r.ID, Tags: r.ExcludedTags})
			}
			return out, nil
		}
	}
	if seriesSvc != nil {
		loaders.Series = func(ctx context.Context) ([]tagdetail.Entity, error) {
			rows, err := seriesSvc.GetAllSeries(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]tagdetail.Entity, 0, len(rows))
			for _, r := range rows {
				out = append(out, tagdetail.Entity{ID: r.ID, Tags: r.Tags})
			}
			return out, nil
		}
	}
	if indexersRepo != nil {
		loaders.Indexers = func(ctx context.Context) ([]tagdetail.Entity, error) {
			rows, err := indexersRepo.All(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]tagdetail.Entity, 0, len(rows))
			for _, r := range rows {
				out = append(out, tagdetail.Entity{ID: r.ID, Tags: r.Tags})
			}
			return out, nil
		}
	}
	if autoTagsSvc != nil {
		loaders.AutoTags = func(ctx context.Context) ([]tagdetail.Entity, error) {
			rows, err := autoTagsSvc.All(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]tagdetail.Entity, 0, len(rows))
			for _, r := range rows {
				if r == nil {
					continue
				}
				// C# GetAutoTagIds: an AutoTag is a consumer of a tag when
				// its Tags contain the id OR one of its specifications is a
				// TagSpecification with Value == the id. Fold the spec
				// values into the entity's tag set so the generic
				// "contains id" filter reproduces both arms (Distinct in C#
				// — the generic filter already de-dups per entity).
				ids := append([]int(nil), r.Tags...)
				for _, spec := range r.Specifications {
					if ts, ok := spec.(*autotagging.TagSpec); ok {
						ids = append(ids, ts.Value)
					}
				}
				out = append(out, tagdetail.Entity{ID: r.ID, Tags: ids})
			}
			return out, nil
		}
	}
	if downloadClientsRepo != nil {
		loaders.DownloadClients = func(ctx context.Context) ([]tagdetail.Entity, error) {
			rows, err := downloadClientsRepo.All(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]tagdetail.Entity, 0, len(rows))
			for _, r := range rows {
				out = append(out, tagdetail.Entity{ID: r.ID, Tags: r.Tags})
			}
			return out, nil
		}
	}
	return tagDetailAdapter{svc: tagdetail.NewService(loaders)}
}
