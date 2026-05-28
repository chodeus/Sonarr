package main

import (
	"context"
	"time"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/mediacover"
	"sonarr.tv/sonarr/internal/series"
	"sonarr.tv/sonarr/internal/seriesstats"
)

// seriesStatsEpisodeLister + seriesStatsFileLister narrow the
// episode/episodefile read surfaces the statistics enricher needs.
// *episodes.Repo satisfies the first; *episodefiles.Service the second.
type seriesStatsEpisodeLister interface {
	AllBySeriesID(ctx context.Context, seriesID int) ([]episodes.Episode, error)
}

type seriesStatsFileLister interface {
	GetFilesBySeries(ctx context.Context, seriesID int) ([]episodefiles.File, error)
}

// seriesReadEnricher bundles the production *series.Service (the read
// surface the series List/ByID handlers need) with the dependencies for
// the two optional-extension projections the handlers discover via
// type-assertion: image-URL rewriting (*mediacover.Service) and
// statistics linking (episode + episodefile readers). The embedded
// *series.Service makes this a drop-in for the handlers'
// apiv3.SeriesLister / apiv3.SeriesByIDGetter parameters.
//
// One value implements BOTH apiv3.SeriesImagePopulator (J367) AND
// apiv3.SeriesStatsPopulator (J369) because the handlers pass a single
// lister/getter through to both ApplyXxxPopulator helpers.
type seriesReadEnricher struct {
	*series.Service
	mc    *mediacover.Service
	eps   seriesStatsEpisodeLister
	files seriesStatsFileLister
}

// PopulateSeriesImages rewrites each MediaCoverResource.Url to the local
// cover URL via mediacover.Service.ConvertToLocalUrls (the Go-port
// equivalent of C# SeriesController.MapCoversToLocal). See J367.
func (s seriesReadEnricher) PopulateSeriesImages(seriesID int, images []apiv3.MediaCoverResource) {
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

// PopulateSeriesStats computes the per-series + per-season episode/file
// statistics and links them onto the resource (the Go-port equivalent
// of C# SeriesController.LinkSeriesStatistics). It loads the series'
// episodes + files, runs the seriesstats.Compute aggregation against
// "now", and delegates the wire-shape linking to
// apiv3.LinkSeriesStatistics.
//
// Loading is per-series (one episode query + one file query per
// resource) — the established aggregate-in-Go posture. The list path is
// thus N+1; the by-id path (the hot one for the series detail page) is
// a single series. A load error leaves statistics nil (the resource
// degrades to the no-stats shape rather than failing the request).
func (s seriesReadEnricher) PopulateSeriesStats(ctx context.Context, res *apiv3.SeriesResource) {
	if res == nil || s.eps == nil || s.files == nil || res.ID == 0 {
		return
	}
	eps, err := s.eps.AllBySeriesID(ctx, res.ID)
	if err != nil {
		return
	}
	files, err := s.files.GetFilesBySeries(ctx, res.ID)
	if err != nil {
		return
	}
	st := seriesstats.Compute(eps, files, time.Now().UTC())
	apiv3.LinkSeriesStatistics(res, st)
}

// Compile-time guards: the wrapper must satisfy both read interfaces the
// series handlers accept AND both optional populators.
var (
	_ apiv3.SeriesLister         = seriesReadEnricher{}
	_ apiv3.SeriesByIDGetter     = seriesReadEnricher{}
	_ apiv3.SeriesImagePopulator = seriesReadEnricher{}
	_ apiv3.SeriesStatsPopulator = seriesReadEnricher{}
)
