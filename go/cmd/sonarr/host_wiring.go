package main

import (
	"context"
	"fmt"
	"log/slog"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/autotagging"
	"sonarr.tv/sonarr/internal/config"
	"sonarr.tv/sonarr/internal/configfile"
	"sonarr.tv/sonarr/internal/customfilters"
	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/delayprofiles"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/host"
	"sonarr.tv/sonarr/internal/importlists"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/namingconfig"
	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/qualitydefinitions"
	"sonarr.tv/sonarr/internal/queue"
	"sonarr.tv/sonarr/internal/releaseprofiles"
	"sonarr.tv/sonarr/internal/remotepathmappings"
	"sonarr.tv/sonarr/internal/rootfolders"
	"sonarr.tv/sonarr/internal/tags"
)

// buildHostServer assembles the *host.Server from the runtime
// dependencies serve() already has in scope. Lives in its own file
// so main.go's wiring block stays scannable as Phase 5 grows.
//
// Mirrors `src/NzbDrone.Host/Bootstrap.cs:CreateConsoleHostBuilder`
// where BindAddress + Port are pulled from the configuration before
// the URL list is handed to Kestrel.
func buildHostServer(ctx context.Context, cfg *configfile.Provider, logger *slog.Logger, configRepo *config.Repo, configProv *config.Provider, qualityDefSvc *qualitydefinitions.Service, tagsSvc *tags.Service, autoTagsSvc *autotagging.Service, releaseProfilesSvc *releaseprofiles.Service, customFormatsSvc *customformats.Service, delayProfilesSvc *delayprofiles.Service, customFiltersSvc *customfilters.Service, queueSvc *queue.Service, rootFoldersSvc *rootfolders.Service, remotePathMappingsSvc *remotepathmappings.Service, namingConfigSvc *namingconfig.Service, notificationsRepo *notifications.Repo, indexersRepo *indexers.Repo, importListsRepo *importlists.Repo, downloadClientsRepo *downloadclients.Repo, episodesSvc *episodes.Service) (*host.Server, error) {
	bind, err := cfg.BindAddress(ctx)
	if err != nil {
		return nil, fmt.Errorf("host: bind address: %w", err)
	}
	port, err := cfg.Port(ctx)
	if err != nil {
		return nil, fmt.Errorf("host: port: %w", err)
	}
	return host.New(
		host.Config{BindAddress: bind, Port: port},
		logger,
		configRepoProbe{configRepo},
		// *configfile.Provider satisfies host.APIKeyResolver via
		// its APIKey(ctx) (string, error) method — no adapter
		// needed.
		cfg,
		host.WithRoutes(v3Routes(configProv, cfg, qualityDefSvc, tagsSvc, autoTagsSvc, releaseProfilesSvc, customFormatsSvc, delayProfilesSvc, customFiltersSvc, queueSvc, rootFoldersSvc, remotePathMappingsSvc, namingConfigSvc, notificationsRepo, indexersRepo, importListsRepo, downloadClientsRepo, episodesSvc)...),
	), nil
}

// emptyPendingLister stands in for a future pendingreleases.Service
// queue-projection. C# `_pendingReleaseService.GetPendingQueue()`
// returns the list of pending-release rows, but the Go
// pendingreleases.Service does not yet expose a parallel surface
// (only AllBySeriesID). Until that lands, the queue/status handler
// reports 0 pending items — correct for an empty DB and a
// non-functional-but-non-divergent placeholder for populated DBs
// (DEFERRED — flagged in STATUS line 9).
type emptyPendingLister struct{}

func (emptyPendingLister) PendingCount(context.Context) (int, error) { return 0, nil }

// emptyWantedLister stands in for a future wanted-episodes service.
// C# `IWantedEpisodeService` returns paged Missing/CutoffUnmet
// projections — the Go port doesn't have a parallel service yet, so
// this stub reports 0 records (correct for a fresh DB; degenerate
// for populated DBs until the wanted port lands — DEFERRED in
// STATUS).
type emptyWantedLister struct{}

func (emptyWantedLister) PagedWanted(context.Context, apiv3.PagingRequest, apiv3.WantedScope) ([]apiv3.EpisodeResource, int, error) {
	return nil, 0, nil
}

// uiConfigGetter composes the two underlying providers
// (*config.Provider for DB-table-backed UI keys + *configfile.Provider
// for the XML-backed Theme) into the single interface the
// /api/v3/config/ui handler needs.
type uiConfigGetter struct {
	cfg    *config.Provider
	xmlCfg *configfile.Provider
}

func (u uiConfigGetter) FirstDayOfWeek() int              { return u.cfg.FirstDayOfWeek() }
func (u uiConfigGetter) CalendarWeekColumnHeader() string { return u.cfg.CalendarWeekColumnHeader() }
func (u uiConfigGetter) ShortDateFormat() string          { return u.cfg.ShortDateFormat() }
func (u uiConfigGetter) LongDateFormat() string           { return u.cfg.LongDateFormat() }
func (u uiConfigGetter) TimeFormat() string               { return u.cfg.TimeFormat() }
func (u uiConfigGetter) TimeZone() string                 { return u.cfg.TimeZone() }
func (u uiConfigGetter) ShowRelativeDates() bool          { return u.cfg.ShowRelativeDates() }
func (u uiConfigGetter) EnableColorImpairedMode() bool    { return u.cfg.EnableColorImpairedMode() }
func (u uiConfigGetter) UILanguage() int                  { return u.cfg.UILanguage() }
func (u uiConfigGetter) Theme(ctx context.Context) (string, error) {
	return u.xmlCfg.Theme(ctx)
}

// v3Routes collects the v3 controllers wired in this slice. As more
// controllers port, they're added here in alphabetical order.
//
// configProv carries the persistent config table (cached by
// *config.Provider with bus invalidation), so closures that read
// UILanguage / other Config table keys re-resolve on every request.
// qualityDefSvc carries the seeded quality-definitions table with a
// 5-second cache, mirroring `QualityDefinitionService.All`.
// tagsSvc owns the tag table (no cache — *tags.Service hits the repo
// per call, matching the C# IRepository<Tag> behavior).
// autoTagsSvc + releaseProfilesSvc own their respective tables; both
// expose All directly via Go's structural typing on the Lister
// interfaces.
func v3Routes(configProv *config.Provider, cfg *configfile.Provider, qualityDefSvc *qualitydefinitions.Service, tagsSvc *tags.Service, autoTagsSvc *autotagging.Service, releaseProfilesSvc *releaseprofiles.Service, customFormatsSvc *customformats.Service, delayProfilesSvc *delayprofiles.Service, customFiltersSvc *customfilters.Service, queueSvc *queue.Service, rootFoldersSvc *rootfolders.Service, remotePathMappingsSvc *remotepathmappings.Service, namingConfigSvc *namingconfig.Service, notificationsRepo *notifications.Repo, indexersRepo *indexers.Repo, importListsRepo *importlists.Repo, downloadClientsRepo *downloadclients.Repo, episodesSvc *episodes.Service) []host.Route {
	langs := language.All()
	uiCfg := uiConfigGetter{cfg: configProv, xmlCfg: cfg}
	return []host.Route{
		{Method: "GET", Path: "/api/v3/autotagging", Handler: apiv3.AutoTaggingHandler(autoTagsSvc)},
		{Method: "GET", Path: "/api/v3/calendar", Handler: apiv3.CalendarHandler(episodesSvc)},
		{Method: "GET", Path: "/api/v3/config/downloadclient", Handler: apiv3.DownloadClientConfigHandler(configProv)},
		{Method: "GET", Path: "/api/v3/config/indexer", Handler: apiv3.IndexerConfigHandler(configProv)},
		{Method: "GET", Path: "/api/v3/config/mediamanagement", Handler: apiv3.MediaManagementConfigHandler(configProv)},
		{Method: "GET", Path: "/api/v3/config/naming", Handler: apiv3.NamingConfigHandler(namingConfigSvc)},
		{Method: "GET", Path: "/api/v3/config/ui", Handler: apiv3.UIConfigHandler(uiCfg)},
		{Method: "GET", Path: "/api/v3/customfilter", Handler: apiv3.CustomFilterHandler(customFiltersSvc)},
		{Method: "GET", Path: "/api/v3/customformat", Handler: apiv3.CustomFormatHandler(customFormatsSvc)},
		{Method: "GET", Path: "/api/v3/delayprofile", Handler: apiv3.DelayProfileHandler(delayProfilesSvc)},
		{Method: "GET", Path: "/api/v3/downloadclient", Handler: apiv3.DownloadClientHandler(downloadClientsRepo)},
		{Method: "GET", Path: "/api/v3/episode", Handler: apiv3.EpisodeListHandler(episodesSvc)},
		{Method: "GET", Path: "/api/v3/importlist", Handler: apiv3.ImportListHandler(importListsRepo)},
		{Method: "GET", Path: "/api/v3/indexer", Handler: apiv3.IndexerHandler(indexersRepo)},
		{Method: "GET", Path: "/api/v3/language", Handler: apiv3.LanguageHandler(langs)},
		{Method: "GET", Path: "/api/v3/localization/language", Handler: apiv3.LocalizationLanguageHandler(configProv.UILanguage)},
		{Method: "GET", Path: "/api/v3/notification", Handler: apiv3.NotificationHandler(notificationsRepo)},
		{Method: "GET", Path: "/api/v3/qualitydefinition", Handler: apiv3.QualityDefinitionHandler(qualityDefSvc)},
		{Method: "GET", Path: "/api/v3/queue/status", Handler: apiv3.QueueStatusHandler(queueSvc, emptyPendingLister{})},
		{Method: "GET", Path: "/api/v3/releaseprofile", Handler: apiv3.ReleaseProfileHandler(releaseProfilesSvc)},
		{Method: "GET", Path: "/api/v3/remotepathmapping", Handler: apiv3.RemotePathMappingHandler(remotePathMappingsSvc)},
		{Method: "GET", Path: "/api/v3/rootfolder", Handler: apiv3.RootFolderHandler(rootFoldersSvc)},
		{Method: "GET", Path: "/api/v3/tag", Handler: apiv3.TagHandler(tagsSvc)},
		{Method: "GET", Path: "/api/v3/wanted/cutoff", Handler: apiv3.WantedHandler(emptyWantedLister{}, apiv3.WantedCutoff)},
		{Method: "GET", Path: "/api/v3/wanted/missing", Handler: apiv3.WantedHandler(emptyWantedLister{}, apiv3.WantedMissing)},
	}
}

// configRepoProbe adapts *config.Repo to host.ConfigProbe. The
// C# PingController calls `_configRepository.All()` purely for its
// side-effect (raise on DB failure); the Go probe collapses that
// to a single-bool error signal.
type configRepoProbe struct{ r *config.Repo }

func (p configRepoProbe) All(ctx context.Context) error {
	_, err := p.r.All(ctx)
	return err
}
