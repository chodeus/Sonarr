package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// TagDetailsResource is the JSON shape produced by
// GET /api/v3/tag/detail. Mirrors
// `src/Sonarr.Api.V3/Tags/TagDetailsResource.cs` — the per-tag join
// projection across every table that references Tags.Id (DelayProfile,
// ImportList, Notification, Restriction, ReleaseProfile-excluded,
// Indexer, DownloadClient, AutoTagging, Series).
//
// This slice ships the wire-shape skeleton; the populated-row join
// awaits a follow-up that lands tag-consumer-lookup adapters on
// every consumer service. The empty-DB recording has zero records
// so the gap is unobservable today (DEFERRED — STATUS line 9 carries
// a note).
//
// Field-source mapping (C# → Go):
//
//	id                        → Tags.Id (omitempty per RestResource)
//	label                     → Tags.Label
//	delayProfileIds           → DelayProfiles.Tags contains Tag.Id
//	importListIds             → ImportLists.Tags contains Tag.Id
//	notificationIds           → Notifications.Tags contains Tag.Id
//	restrictionIds            → ReleaseProfiles.Tags contains Tag.Id
//	excludedReleaseProfileIds → ReleaseProfiles.IndexerId-excluded ref
//	indexerIds                → Indexers.Tags contains Tag.Id
//	downloadClientIds         → DownloadClients.Tags contains Tag.Id
//	autoTagIds                → AutoTaggings.Tags contains Tag.Id
//	seriesIds                 → Series.Tags contains Tag.Id
//
// Every join field uses []int (never nil — emit `[]` to match the
// C# default-constructed `new List<int>()`). The C# resource omits
// `id` when zero via RestResource's `JsonIgnore(WhenWritingDefault)`
// attribute, so the Go-side `omitempty` matches.
type TagDetailsResource struct {
	ID                        int    `json:"id,omitempty"`
	Label                     string `json:"label"`
	DelayProfileIDs           []int  `json:"delayProfileIds"`
	ImportListIDs             []int  `json:"importListIds"`
	NotificationIDs           []int  `json:"notificationIds"`
	RestrictionIDs            []int  `json:"restrictionIds"`
	ExcludedReleaseProfileIDs []int  `json:"excludedReleaseProfileIds"`
	IndexerIDs                []int  `json:"indexerIds"`
	DownloadClientIDs         []int  `json:"downloadClientIds"`
	AutoTagIDs                []int  `json:"autoTagIds"`
	SeriesIDs                 []int  `json:"seriesIds"`
}

// TagDetailLister is the read side the handler depends on. The
// production Go path has no aggregated tag-detail projection yet
// — cmd/sonarr substitutes an empty fixture today. When the
// projection lands, an adapter on top of tags.Service + consumer
// services will satisfy this interface.
type TagDetailLister interface {
	AllTagDetails(ctx context.Context) ([]TagDetailsResource, error)
}

// TagDetailHandler returns the http.Handler for GET /api/v3/tag/detail.
// Mirrors `TagDetailsController.GetAll` at
// `src/Sonarr.Api.V3/Tags/TagDetailsController.cs:25-29`. The C#
// path delegates to `_tagService.Details().ToResource()` which runs
// the per-consumer join inside `TagService.Details()`. The Go port
// inverts the dependency — the projection is precomputed by the
// adapter and the handler is a thin pass-through.
func TagDetailHandler(lister TagDetailLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := lister.AllTagDetails(r.Context())
		if err != nil {
			http.Error(w, fmt.Sprintf("tag/detail: %v", err), http.StatusInternalServerError)
			return
		}
		if rows == nil {
			rows = []TagDetailsResource{}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(rows)
		_, _ = w.Write(buf)
	})
}
