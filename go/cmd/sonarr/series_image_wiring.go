package main

import (
	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/mediacover"
	"sonarr.tv/sonarr/internal/series"
)

// seriesImagePopulator bundles the production *series.Service (the read
// surface the series List/ByID handlers need) with the *mediacover.Service
// so it ALSO satisfies apiv3.SeriesImagePopulator. The embedded
// *series.Service makes this a drop-in for the handlers'
// apiv3.SeriesLister / apiv3.SeriesByIDGetter parameters; the
// PopulateSeriesImages method below is the J367 optional-extension that
// the handlers discover via type-assertion (J339/J345 idiom).
//
// This is the Go-port equivalent of C# SeriesController's injected
// IMapCoversToLocal (`_coverMapper`), used by MapCoversToLocal
// (`SeriesController.cs:231-237`) to rewrite each resource's image URLs
// to the local `<urlBase>/MediaCover/<seriesId>/<type>.<ext>?lastWrite=<ticks>`
// form after ToResource.
type seriesImagePopulator struct {
	*series.Service
	mc *mediacover.Service
}

// PopulateSeriesImages rewrites each MediaCoverResource.Url to the local
// cover URL via mediacover.Service.ConvertToLocalUrls. It rebuilds the
// typed []mediacover.Cover from the wire resources (mapping the coverType
// string back to the frozen enum via mediacover.ParseCoverType), runs the
// converter (which keys off seriesID + CoverType and probes the on-disk
// file for the ?lastWrite cache-buster), then copies the rewritten Url
// back onto the wire resources. RemoteUrl + CoverType are untouched.
//
// Unknown/garbage coverType strings map to mediacover.CoverTypeUnknown,
// which ConvertToLocalUrls skips — leaving that entry's persisted Url
// intact (matches the C# converter's CoverTypeUnknown short-circuit).
func (s seriesImagePopulator) PopulateSeriesImages(seriesID int, images []apiv3.MediaCoverResource) {
	if s.mc == nil || len(images) == 0 {
		return
	}
	covers := make([]mediacover.Cover, len(images))
	for i, img := range images {
		ct, _ := mediacover.ParseCoverType(img.CoverType)
		covers[i] = mediacover.Cover{CoverType: ct, Url: img.Url, RemoteUrl: img.RemoteUrl}
	}
	s.mc.ConvertToLocalUrls(seriesID, covers)
	for i := range images {
		images[i].Url = covers[i].Url
	}
}

// Compile-time guards: the wrapper must satisfy both read interfaces the
// series handlers accept AND the optional image populator.
var (
	_ apiv3.SeriesLister         = seriesImagePopulator{}
	_ apiv3.SeriesByIDGetter     = seriesImagePopulator{}
	_ apiv3.SeriesImagePopulator = seriesImagePopulator{}
)
