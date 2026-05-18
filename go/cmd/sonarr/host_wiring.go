package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/autotagging"
	"sonarr.tv/sonarr/internal/blocklist"
	"sonarr.tv/sonarr/internal/config"
	"sonarr.tv/sonarr/internal/configfile"
	"sonarr.tv/sonarr/internal/customfilters"
	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/delayprofiles"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/frontend"
	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/history"
	"sonarr.tv/sonarr/internal/host"
	"sonarr.tv/sonarr/internal/importlistexclusions"
	"sonarr.tv/sonarr/internal/importlists"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/localization"
	"sonarr.tv/sonarr/internal/metadata"
	"sonarr.tv/sonarr/internal/namingconfig"
	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/pendingreleases"
	"sonarr.tv/sonarr/internal/qualitydefinitions"
	"sonarr.tv/sonarr/internal/qualityprofiles"
	"sonarr.tv/sonarr/internal/queue"
	"sonarr.tv/sonarr/internal/releaseprofiles"
	"sonarr.tv/sonarr/internal/remotepathmappings"
	"sonarr.tv/sonarr/internal/rootfolders"
	"sonarr.tv/sonarr/internal/series"
	"sonarr.tv/sonarr/internal/signalr"
	"sonarr.tv/sonarr/internal/tags"
)

// buildHostServer assembles the *host.Server from the runtime
// dependencies serve() already has in scope. Lives in its own file
// so main.go's wiring block stays scannable as Phase 5 grows.
//
// Mirrors `src/NzbDrone.Host/Bootstrap.cs:CreateConsoleHostBuilder`
// where BindAddress + Port are pulled from the configuration before
// the URL list is handed to Kestrel.
func buildHostServer(ctx context.Context, cfg *configfile.Provider, logger *slog.Logger, configRepo *config.Repo, configProv *config.Provider, qualityDefSvc *qualitydefinitions.Service, qualityProfilesSvc *qualityprofiles.Service, tagsSvc *tags.Service, autoTagsSvc *autotagging.Service, releaseProfilesSvc *releaseprofiles.Service, customFormatsSvc *customformats.Service, delayProfilesSvc *delayprofiles.Service, customFiltersSvc *customfilters.Service, queueSvc *queue.Service, rootFoldersSvc *rootfolders.Service, remotePathMappingsSvc *remotepathmappings.Service, namingConfigSvc *namingconfig.Service, notificationsRepo *notifications.Repo, indexersRepo *indexers.Repo, importListsRepo *importlists.Repo, importListExclusionsRepo *importlistexclusions.Repo, downloadClientsRepo *downloadclients.Repo, episodesSvc *episodes.Service, episodesRepo *episodes.Repo, seriesSvc *series.Service, healthSvc *healthcheck.Service, metadataRepo *metadata.Repo, signalRHub *signalr.Hub, queueStatusPauser apiv3.Pauser, systemStatus apiv3.SystemStatusContext, pendingReleasesRepo *pendingreleases.Repo, blocklistRepo *blocklist.Repo, historyRepo *history.Repo, frontendDir string) (*host.Server, error) {
	bind, err := cfg.BindAddress(ctx)
	if err != nil {
		return nil, fmt.Errorf("host: bind address: %w", err)
	}
	port, err := cfg.Port(ctx)
	if err != nil {
		return nil, fmt.Errorf("host: port: %w", err)
	}
	opts := []host.Option{
		host.WithRoutes(v3Routes(configProv, cfg, logger, qualityDefSvc, qualityProfilesSvc, tagsSvc, autoTagsSvc, releaseProfilesSvc, customFormatsSvc, delayProfilesSvc, customFiltersSvc, queueSvc, rootFoldersSvc, remotePathMappingsSvc, namingConfigSvc, notificationsRepo, indexersRepo, importListsRepo, importListExclusionsRepo, downloadClientsRepo, episodesSvc, episodesRepo, seriesSvc, healthSvc, metadataRepo, signalRHub, queueStatusPauser, systemStatus, pendingReleasesRepo, blocklistRepo, historyRepo)...),
	}
	if frontendDir != "" {
		opts = append(opts, host.WithStaticDir(frontendDir))
		logger.Info("host static mount", "dir", frontendDir)
	} else {
		// Slice 4.FG: --frontenddir unset → fall back to the
		// embedded bundle. Production builds replace
		// go/internal/frontend/bundle/ with the real React build
		// before `go build`; the shipped placeholder explains
		// itself when an operator forgets the copy step.
		opts = append(opts, host.WithStaticFS(frontend.FS()))
		logger.Info("host static mount", "source", "embedded")
	}
	// Slice 4.FE: ConfigFile.UrlBase prefix support — when set,
	// every route is served under <base>/. The host's stripURLBase
	// middleware peels the prefix before dispatching so the route
	// registrations above stay prefix-free.
	if urlBase, err := cfg.URLBase(ctx); err == nil && urlBase != "" {
		opts = append(opts, host.WithURLBase(urlBase))
		logger.Info("host urlbase mount", "base", urlBase)
	}
	return host.New(
		host.Config{BindAddress: bind, Port: port},
		logger,
		configRepoProbe{configRepo},
		// *configfile.Provider satisfies host.APIKeyResolver via
		// its APIKey(ctx) (string, error) method — no adapter
		// needed.
		cfg,
		opts...,
	), nil
}

// emptyTagDetailLister stands in for the future tag-detail join.
// /api/v3/tag/detail's empty-DB recording shows 0 entries — the
// full C# projection joins Tag rows with every consumer table
// (DelayProfile/ImportList/Notification/Restriction/ReleaseProfile-
// excluded/Indexer/DownloadClient/AutoTagging/Series). Today no
// tags exist in the recording, so an honest zero result matches
// byte-eq. Populated-row join DEFERRED.
type emptyTagDetailLister struct{}

func (emptyTagDetailLister) AllTagDetails(context.Context) ([]apiv3.TagDetailsResource, error) {
	return nil, nil
}

// emptyUpdateLister stands in for the future Update package
// provider. /api/v3/update's empty-DB recording shows 0 entries.
// The C# UpdateController fetches recent packages via
// RecentUpdateProvider; that provider isn't ported yet, so the
// stub keeps byte-eq while the wider Update sub-arc lands later.
type emptyUpdateLister struct{}

func (emptyUpdateLister) RecentUpdates(context.Context) ([]apiv3.UpdateResource, error) {
	return nil, nil
}

// emptyQueuePagedLister stands in for the future queue.Service paged
// projection. /api/v3/queue's empty-DB recording shows 0 records, so
// an honest zero result matches byte-eq today; populated-row mapping
// awaits the queue.Service's paged surface — DEFERRED in STATUS.
type emptyQueuePagedLister struct{}

func (emptyQueuePagedLister) PagedQueue(context.Context, apiv3.PagingRequest) ([]apiv3.QueueResource, int, error) {
	return nil, 0, nil
}

// pendingCountLister wraps the pendingreleases.Repo's row count for
// /api/v3/queue/status. Slice 4.EB closed the emptyPendingLister stub
// — the queue summary now reflects real pending-release rows. C#'s
// `_pendingReleaseService.GetPendingQueue()` returns full row shape;
// the queue/status handler only consumes the cardinality, so the
// Repo.Count short-circuit is enough until a Service-level projection
// lands for richer downstream consumers.
type pendingCountLister struct{ repo *pendingreleases.Repo }

func (p pendingCountLister) PendingCount(ctx context.Context) (int, error) {
	return p.repo.Count(ctx)
}

// blocklistPagedAdapter wraps *blocklist.Repo to satisfy
// apiv3.BlocklistPagedLister. The fresh-DB contract gate is the
// empty-array shape; for populated DBs the adapter reads All() and
// slices in-memory by Page/PageSize. SortKey/SortDirection params
// are honoured at the envelope level but not yet applied to the
// underlying query — DEFERRED until a richer paged repo method
// lands (`SELECT ... ORDER BY ... LIMIT N OFFSET M`). Per-record
// fields beyond the skeleton (Title from Episode join, etc.) are
// also DEFERRED.
type blocklistPagedAdapter struct{ repo *blocklist.Repo }

func (a blocklistPagedAdapter) PagedBlocklist(ctx context.Context, req apiv3.PagingRequest) ([]apiv3.BlocklistResource, int, error) {
	rows, err := a.repo.All(ctx)
	if err != nil {
		return nil, 0, err
	}
	total := len(rows)
	page := paginateInMemory(rows, req)
	out := make([]apiv3.BlocklistResource, 0, len(page))
	for _, e := range page {
		out = append(out, apiv3.BlocklistResource{
			ID:          e.ID,
			SeriesID:    e.SeriesID,
			SourceTitle: e.SourceTitle,
			Date:        e.Date.UTC().Format("2006-01-02T15:04:05Z"),
			// Title is derived from Episode.Title in C# — DEFERRED
			// until the episode-join wiring lands.
		})
	}
	return out, total, nil
}

// historyPagedAdapter wraps *history.Repo. Same caveats as blocklist:
// in-memory paging, skeleton fields only, sort params unused.
type historyPagedAdapter struct{ repo *history.Repo }

func (a historyPagedAdapter) PagedHistory(ctx context.Context, req apiv3.PagingRequest) ([]apiv3.HistoryResource, int, error) {
	rows, err := a.repo.All(ctx)
	if err != nil {
		return nil, 0, err
	}
	total := len(rows)
	page := paginateInMemory(rows, req)
	out := make([]apiv3.HistoryResource, 0, len(page))
	for _, e := range page {
		out = append(out, apiv3.HistoryResource{
			ID:          e.ID,
			SeriesID:    e.SeriesID,
			EpisodeID:   e.EpisodeID,
			SourceTitle: e.SourceTitle,
			Date:        e.Date.UTC().Format("2006-01-02T15:04:05Z"),
			DownloadID:  e.DownloadID,
			// EventType in C# is JsonStringEnumConverter+CamelCase
			// on EpisodeHistoryEventType; the Go-port mapping is
			// DEFERRED until the event-type-name table lands.
		})
	}
	return out, total, nil
}

// paginateInMemory clamps the page window to the slice and returns
// the requested rows. Pure helper; works for any slice type so both
// adapters share it via Go generics.
func paginateInMemory[T any](all []T, req apiv3.PagingRequest) []T {
	if req.PageSize <= 0 {
		return nil
	}
	start := (req.Page - 1) * req.PageSize
	if start < 0 {
		start = 0
	}
	if start >= len(all) {
		return nil
	}
	end := start + req.PageSize
	if end > len(all) {
		end = len(all)
	}
	return all[start:end]
}

// wantedRepoLister bridges /api/v3/wanted/{missing,cutoff} to the
// real episodes-repo predicate query introduced in slice 4.ED. Closes
// the `emptyWantedLister` DEFERRED note carried since slice 4.DV.
//
// Missing branch: routes to `episodes.Repo.WantedMissing` —
// monitored-only is honoured (the C# controller's default), Episode
// rows are mapped via the same `apiv3.ToEpisodeResource` projection the
// /api/v3/episode handler uses, and the result is paged in memory.
// SortKey + SortDirection at the SQL level remain DEFERRED (same
// posture as the blocklist + history adapters that slice 4.EC
// production-wired).
//
// Cutoff branch: returns an empty page. C# `EpisodeCutoffService`
// resolves the quality-cutoff predicate from quality profiles +
// episode-files metadata — the qualityprofile port hasn't landed
// yet, so an honest empty result matches the fresh-DB recording
// byte-eq and avoids returning false positives in populated DBs.
// Tracked DEFERRED until the qualityprofile port lands.
//
// `now` is a function rather than a captured `time.Time` so each
// request observes the live wall clock (matching C#
// `DateTime.UtcNow` per invocation).
type wantedRepoLister struct {
	episodes *episodes.Repo
	now      func() time.Time
}

func (w wantedRepoLister) PagedWanted(ctx context.Context, req apiv3.PagingRequest, scope apiv3.WantedScope) ([]apiv3.EpisodeResource, int, error) {
	if scope == apiv3.WantedCutoff {
		return nil, 0, nil
	}
	// MissingController's default — `monitored=true`. The recorded
	// corpus exercises only the default, so the boolean toggle stays
	// pinned here until query-string parsing for the alternate branch
	// lands.
	rows, err := w.episodes.WantedMissing(ctx, w.now(), true)
	if err != nil {
		return nil, 0, err
	}
	total := len(rows)
	page := paginateInMemory(rows, req)
	out := make([]apiv3.EpisodeResource, 0, len(page))
	for _, e := range page {
		out = append(out, apiv3.ToEpisodeResource(e))
	}
	return out, total, nil
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

// 5.B13b setters — composite write surface for /api/v3/config/ui PUT.
// DB-backed setters route to *config.Provider; the XML-backed Theme
// setter routes to *configfile.Provider.
func (u uiConfigGetter) SetFirstDayOfWeek(ctx context.Context, v int) error {
	return u.cfg.SetFirstDayOfWeek(ctx, v)
}
func (u uiConfigGetter) SetCalendarWeekColumnHeader(ctx context.Context, v string) error {
	return u.cfg.SetCalendarWeekColumnHeader(ctx, v)
}
func (u uiConfigGetter) SetShortDateFormat(ctx context.Context, v string) error {
	return u.cfg.SetShortDateFormat(ctx, v)
}
func (u uiConfigGetter) SetLongDateFormat(ctx context.Context, v string) error {
	return u.cfg.SetLongDateFormat(ctx, v)
}
func (u uiConfigGetter) SetTimeFormat(ctx context.Context, v string) error {
	return u.cfg.SetTimeFormat(ctx, v)
}
func (u uiConfigGetter) SetTimeZone(ctx context.Context, v string) error {
	return u.cfg.SetTimeZone(ctx, v)
}
func (u uiConfigGetter) SetShowRelativeDates(ctx context.Context, v bool) error {
	return u.cfg.SetShowRelativeDates(ctx, v)
}
func (u uiConfigGetter) SetEnableColorImpairedMode(ctx context.Context, v bool) error {
	return u.cfg.SetEnableColorImpairedMode(ctx, v)
}
func (u uiConfigGetter) SetUILanguage(ctx context.Context, v int) error {
	return u.cfg.SetUILanguage(ctx, v)
}
func (u uiConfigGetter) SetTheme(ctx context.Context, v string) error {
	return u.xmlCfg.SetTheme(ctx, v)
}

// hostConfigGetter composes *configfile.Provider and *config.Provider
// into the single interface /api/v3/config/host needs. The two C#
// services (IConfigFileProvider + IConfigService) are collapsed into
// one getter at the Go REST boundary.
type hostConfigGetter struct {
	cfg    *config.Provider
	xmlCfg *configfile.Provider
}

func (h hostConfigGetter) BindAddress(ctx context.Context) (string, error) {
	return h.xmlCfg.BindAddress(ctx)
}
func (h hostConfigGetter) Port(ctx context.Context) (int, error) { return h.xmlCfg.Port(ctx) }
func (h hostConfigGetter) SslPort(ctx context.Context) (int, error) {
	return h.xmlCfg.SslPort(ctx)
}
func (h hostConfigGetter) EnableSsl(ctx context.Context) (bool, error) {
	return h.xmlCfg.EnableSsl(ctx)
}
func (h hostConfigGetter) LaunchBrowser(ctx context.Context) (bool, error) {
	return h.xmlCfg.LaunchBrowser(ctx)
}
func (h hostConfigGetter) AuthenticationMethod(ctx context.Context) (string, error) {
	return h.xmlCfg.AuthenticationMethod(ctx)
}
func (h hostConfigGetter) AuthenticationRequired(ctx context.Context) (string, error) {
	return h.xmlCfg.AuthenticationRequired(ctx)
}
func (h hostConfigGetter) AnalyticsEnabled(ctx context.Context) (bool, error) {
	return h.xmlCfg.AnalyticsEnabled(ctx)
}
func (h hostConfigGetter) LogLevel(ctx context.Context) (string, error) {
	return h.xmlCfg.LogLevel(ctx)
}
func (h hostConfigGetter) LogSizeLimit(ctx context.Context) (int, error) {
	return h.xmlCfg.LogSizeLimit(ctx)
}
func (h hostConfigGetter) ConsoleLogLevel(ctx context.Context) (string, error) {
	return h.xmlCfg.ConsoleLogLevel(ctx)
}
func (h hostConfigGetter) Branch(ctx context.Context) (string, error) {
	return h.xmlCfg.Branch(ctx)
}
func (h hostConfigGetter) APIKey(ctx context.Context) (string, error) {
	return h.xmlCfg.APIKey(ctx)
}
func (h hostConfigGetter) SslCertPath(ctx context.Context) (string, error) {
	return h.xmlCfg.SslCertPath(ctx)
}
func (h hostConfigGetter) SslKeyPath(ctx context.Context) (string, error) {
	return h.xmlCfg.SslKeyPath(ctx)
}
func (h hostConfigGetter) SslCertPassword(ctx context.Context) (string, error) {
	return h.xmlCfg.SslCertPassword(ctx)
}
func (h hostConfigGetter) URLBase(ctx context.Context) (string, error) {
	return h.xmlCfg.URLBase(ctx)
}
func (h hostConfigGetter) InstanceName(ctx context.Context) (string, error) {
	return h.xmlCfg.InstanceName(ctx)
}
func (h hostConfigGetter) TrustCgnatIpAddresses(ctx context.Context) (bool, error) {
	return h.xmlCfg.TrustCgnatIpAddresses(ctx)
}
func (h hostConfigGetter) UpdateAutomatically(ctx context.Context) (bool, error) {
	return h.xmlCfg.UpdateAutomatically(ctx)
}
func (h hostConfigGetter) UpdateMechanism(ctx context.Context) (string, error) {
	return h.xmlCfg.UpdateMechanism(ctx)
}
func (h hostConfigGetter) UpdateScriptPath(ctx context.Context) (string, error) {
	return h.xmlCfg.UpdateScriptPath(ctx)
}
func (h hostConfigGetter) ApplicationUrl() string          { return h.cfg.ApplicationUrl() }
func (h hostConfigGetter) BackupFolder() string            { return h.cfg.BackupFolder() }
func (h hostConfigGetter) BackupInterval() int             { return h.cfg.BackupInterval() }
func (h hostConfigGetter) BackupRetention() int            { return h.cfg.BackupRetention() }
func (h hostConfigGetter) CertificateValidation() string   { return h.cfg.CertificateValidation() }
func (h hostConfigGetter) ProxyEnabled() bool              { return h.cfg.ProxyEnabled() }
func (h hostConfigGetter) ProxyType() string               { return h.cfg.ProxyType() }
func (h hostConfigGetter) ProxyHostname() string           { return h.cfg.ProxyHostname() }
func (h hostConfigGetter) ProxyPort() int                  { return h.cfg.ProxyPort() }
func (h hostConfigGetter) ProxyUsername() string           { return h.cfg.ProxyUsername() }
func (h hostConfigGetter) ProxyPassword() string           { return h.cfg.ProxyPassword() }
func (h hostConfigGetter) ProxyBypassFilter() string       { return h.cfg.ProxyBypassFilter() }
func (h hostConfigGetter) ProxyBypassLocalAddresses() bool { return h.cfg.ProxyBypassLocalAddresses() }

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
func v3Routes(configProv *config.Provider, cfg *configfile.Provider, logger *slog.Logger, qualityDefSvc *qualitydefinitions.Service, qualityProfilesSvc *qualityprofiles.Service, tagsSvc *tags.Service, autoTagsSvc *autotagging.Service, releaseProfilesSvc *releaseprofiles.Service, customFormatsSvc *customformats.Service, delayProfilesSvc *delayprofiles.Service, customFiltersSvc *customfilters.Service, queueSvc *queue.Service, rootFoldersSvc *rootfolders.Service, remotePathMappingsSvc *remotepathmappings.Service, namingConfigSvc *namingconfig.Service, notificationsRepo *notifications.Repo, indexersRepo *indexers.Repo, importListsRepo *importlists.Repo, importListExclusionsRepo *importlistexclusions.Repo, downloadClientsRepo *downloadclients.Repo, episodesSvc *episodes.Service, episodesRepo *episodes.Repo, seriesSvc *series.Service, healthSvc *healthcheck.Service, metadataRepo *metadata.Repo, signalRHub *signalr.Hub, queueStatusPauser apiv3.Pauser, systemStatus apiv3.SystemStatusContext, pendingReleasesRepo *pendingreleases.Repo, blocklistRepo *blocklist.Repo, historyRepo *history.Repo) []host.Route {
	langs := language.All()
	localizationSvc := localization.NewService()
	uiCfg := uiConfigGetter{cfg: configProv, xmlCfg: cfg}
	hostCfg := hostConfigGetter{cfg: configProv, xmlCfg: cfg}
	// 5.B2: per-resource SignalR emitter for the rootfolder writes.
	// The rootfolder controller in C# extends RestControllerWithSignalR
	// which auto-broadcasts on Create/Delete; the Go port fires the
	// broadcast directly from the handler (no domain-event indirection
	// for this resource — there's no RootFolderUpdatedEvent in C#).
	rootFolderEmitter := signalr.NewV3Emitter[apiv3.RootFolderResource](signalRHub, "rootfolder")
	return []host.Route{
		{Method: "GET", Path: "/api/v3/autotagging", Handler: apiv3.AutoTaggingHandler(autoTagsSvc)},
		{Method: "GET", Path: "/api/v3/autotagging/{id}", Handler: apiv3.AutoTaggingByIDHandler(autoTagsSvc)},
		{Method: "GET", Path: "/api/v3/autotagging/schema", Handler: apiv3.AutoTaggingSchemaHandler()},
		{Method: "POST", Path: "/api/v3/autotagging", Handler: apiv3.AutoTaggingCreateHandler(autoTagsSvc)},
		{Method: "PUT", Path: "/api/v3/autotagging/{id}", Handler: apiv3.AutoTaggingUpdateHandler(autoTagsSvc)},
		{Method: "DELETE", Path: "/api/v3/autotagging/{id}", Handler: apiv3.AutoTaggingDeleteHandler(autoTagsSvc)},
		{Method: "GET", Path: "/api/v3/blocklist", Handler: apiv3.BlocklistHandler(blocklistPagedAdapter{repo: blocklistRepo})},
		{Method: "GET", Path: "/api/v3/calendar", Handler: apiv3.CalendarHandler(episodesSvc)},
		{Method: "GET", Path: "/api/v3/config/downloadclient", Handler: apiv3.DownloadClientConfigHandler(configProv)},
		{Method: "PUT", Path: "/api/v3/config/downloadclient/{id}", Handler: apiv3.DownloadClientConfigUpdateHandler(configProv)},
		{Method: "GET", Path: "/api/v3/config/host", Handler: apiv3.HostConfigHandler(hostCfg)},
		{Method: "GET", Path: "/api/v3/config/indexer", Handler: apiv3.IndexerConfigHandler(configProv)},
		{Method: "PUT", Path: "/api/v3/config/indexer/{id}", Handler: apiv3.IndexerConfigUpdateHandler(configProv)},
		{Method: "GET", Path: "/api/v3/config/mediamanagement", Handler: apiv3.MediaManagementConfigHandler(configProv)},
		{Method: "PUT", Path: "/api/v3/config/mediamanagement/{id}", Handler: apiv3.MediaManagementConfigUpdateHandler(configProv)},
		{Method: "GET", Path: "/api/v3/config/naming", Handler: apiv3.NamingConfigHandler(namingConfigSvc)},
		{Method: "GET", Path: "/api/v3/config/naming/examples", Handler: apiv3.NamingConfigExamplesHandler(namingConfigSvc)},
		{Method: "PUT", Path: "/api/v3/config/naming/{id}", Handler: apiv3.NamingConfigUpdateHandler(namingConfigSvc)},
		{Method: "GET", Path: "/api/v3/config/ui", Handler: apiv3.UIConfigHandler(uiCfg)},
		{Method: "PUT", Path: "/api/v3/config/ui/{id}", Handler: apiv3.UIConfigUpdateHandler(uiCfg)},
		{Method: "GET", Path: "/api/v3/customfilter", Handler: apiv3.CustomFilterHandler(customFiltersSvc)},
		{Method: "GET", Path: "/api/v3/customfilter/{id}", Handler: apiv3.CustomFilterByIDHandler(customFiltersSvc)},
		{Method: "POST", Path: "/api/v3/customfilter", Handler: apiv3.CustomFilterCreateHandler(customFiltersSvc)},
		{Method: "PUT", Path: "/api/v3/customfilter/{id}", Handler: apiv3.CustomFilterUpdateHandler(customFiltersSvc)},
		{Method: "DELETE", Path: "/api/v3/customfilter/{id}", Handler: apiv3.CustomFilterDeleteHandler(customFiltersSvc)},
		{Method: "GET", Path: "/api/v3/customformat", Handler: apiv3.CustomFormatHandler(customFormatsSvc)},
		{Method: "GET", Path: "/api/v3/customformat/{id}", Handler: apiv3.CustomFormatByIDHandler(customFormatsSvc)},
		{Method: "GET", Path: "/api/v3/customformat/schema", Handler: apiv3.CustomFormatSchemaHandler()},
		{Method: "POST", Path: "/api/v3/customformat", Handler: apiv3.CustomFormatCreateHandler(customFormatsSvc)},
		{Method: "PUT", Path: "/api/v3/customformat/{id}", Handler: apiv3.CustomFormatUpdateHandler(customFormatsSvc)},
		{Method: "PUT", Path: "/api/v3/customformat/bulk", Handler: apiv3.CustomFormatBulkUpdateHandler(customFormatsSvc)},
		{Method: "DELETE", Path: "/api/v3/customformat/{id}", Handler: apiv3.CustomFormatDeleteHandler(customFormatsSvc)},
		{Method: "DELETE", Path: "/api/v3/customformat/bulk", Handler: apiv3.CustomFormatBulkDeleteHandler(customFormatsSvc)},
		{Method: "GET", Path: "/api/v3/delayprofile", Handler: apiv3.DelayProfileHandler(delayProfilesSvc)},
		{Method: "GET", Path: "/api/v3/delayprofile/{id}", Handler: apiv3.DelayProfileByIDHandler(delayProfilesSvc)},
		{Method: "POST", Path: "/api/v3/delayprofile", Handler: apiv3.DelayProfileCreateHandler(delayProfilesSvc)},
		{Method: "PUT", Path: "/api/v3/delayprofile/{id}", Handler: apiv3.DelayProfileUpdateHandler(delayProfilesSvc)},
		{Method: "PUT", Path: "/api/v3/delayprofile/reorder/{id}", Handler: apiv3.DelayProfileReorderHandler(delayProfilesSvc)},
		{Method: "DELETE", Path: "/api/v3/delayprofile/{id}", Handler: apiv3.DelayProfileDeleteHandler(delayProfilesSvc)},
		{Method: "GET", Path: "/api/v3/downloadclient", Handler: apiv3.DownloadClientHandler(downloadClientsRepo)},
		{Method: "GET", Path: "/api/v3/downloadclient/schema", Handler: apiv3.DownloadClientSchemaHandler()},
		{Method: "GET", Path: "/api/v3/episode", Handler: apiv3.EpisodeListHandler(episodesSvc)},
		{Method: "GET", Path: "/api/v3/health", Handler: apiv3.HealthHandler(healthSvc)},
		{Method: "GET", Path: "/api/v3/history", Handler: apiv3.HistoryHandler(historyPagedAdapter{repo: historyRepo})},
		{Method: "GET", Path: "/api/v3/importlist", Handler: apiv3.ImportListHandler(importListsRepo)},
		{Method: "GET", Path: "/api/v3/importlist/schema", Handler: apiv3.ImportListSchemaHandler()},
		{Method: "GET", Path: "/api/v3/importlistexclusion", Handler: apiv3.ImportListExclusionHandler(importListExclusionsRepo)},
		{Method: "GET", Path: "/api/v3/importlistexclusion/{id}", Handler: apiv3.ImportListExclusionByIDHandler(importListExclusionsRepo)},
		{Method: "POST", Path: "/api/v3/importlistexclusion", Handler: apiv3.ImportListExclusionCreateHandler(importListExclusionsRepo)},
		{Method: "PUT", Path: "/api/v3/importlistexclusion/{id}", Handler: apiv3.ImportListExclusionUpdateHandler(importListExclusionsRepo)},
		{Method: "DELETE", Path: "/api/v3/importlistexclusion/{id}", Handler: apiv3.ImportListExclusionDeleteHandler(importListExclusionsRepo)},
		{Method: "DELETE", Path: "/api/v3/importlistexclusion/bulk", Handler: apiv3.ImportListExclusionBulkDeleteHandler(importListExclusionsRepo)},
		{Method: "GET", Path: "/api/v3/indexer", Handler: apiv3.IndexerHandler(indexersRepo)},
		{Method: "GET", Path: "/api/v3/indexer/schema", Handler: apiv3.IndexerSchemaHandler()},
		{Method: "GET", Path: "/api/v3/language", Handler: apiv3.LanguageHandler(langs)},
		{Method: "GET", Path: "/api/v3/localization", Handler: apiv3.LocalizationHandler(localizationSvc)},
		{Method: "GET", Path: "/api/v3/metadata", Handler: apiv3.MetadataListHandler(metadataRepo)},
		{Method: "GET", Path: "/api/v3/metadata/schema", Handler: apiv3.MetadataSchemaHandler()},
		{Method: "GET", Path: "/api/v3/localization/language", Handler: apiv3.LocalizationLanguageHandler(configProv.UILanguage)},
		{Method: "GET", Path: "/api/v3/notification", Handler: apiv3.NotificationHandler(notificationsRepo)},
		{Method: "GET", Path: "/api/v3/notification/schema", Handler: apiv3.NotificationSchemaHandler()},
		{Method: "GET", Path: "/api/v3/qualitydefinition", Handler: apiv3.QualityDefinitionHandler(qualityDefSvc)},
		{Method: "GET", Path: "/api/v3/qualitydefinition/{id}", Handler: apiv3.QualityDefinitionByIDHandler(qualityDefSvc)},
		{Method: "GET", Path: "/api/v3/qualitydefinition/limits", Handler: apiv3.QualityDefinitionLimitsHandler()},
		{Method: "PUT", Path: "/api/v3/qualitydefinition/{id}", Handler: apiv3.QualityDefinitionUpdateHandler(qualityDefSvc, qualityProfilesSvc)},
		{Method: "PUT", Path: "/api/v3/qualitydefinition/update", Handler: apiv3.QualityDefinitionUpdateManyHandler(qualityDefSvc, qualityDefSvc, qualityProfilesSvc)},
		{Method: "GET", Path: "/api/v3/qualityprofile", Handler: apiv3.QualityProfileListHandler(qualityProfilesSvc, customFormatsSvc)},
		{Method: "GET", Path: "/api/v3/qualityprofile/{id}", Handler: apiv3.QualityProfileByIDHandler(qualityProfilesSvc, customFormatsSvc)},
		{Method: "GET", Path: "/api/v3/qualityprofile/schema", Handler: apiv3.QualityProfileSchemaHandler(qualityProfilesSvc, customFormatsSvc)},
		{Method: "POST", Path: "/api/v3/qualityprofile", Handler: apiv3.QualityProfileCreateHandler(qualityProfilesSvc, customFormatsSvc)},
		{Method: "PUT", Path: "/api/v3/qualityprofile/{id}", Handler: apiv3.QualityProfileUpdateHandler(qualityProfilesSvc, customFormatsSvc)},
		{Method: "DELETE", Path: "/api/v3/qualityprofile/{id}", Handler: apiv3.QualityProfileDeleteHandler(qualityProfilesSvc)},
		{Method: "GET", Path: "/api/v3/queue", Handler: apiv3.QueueHandler(emptyQueuePagedLister{})},
		{Method: "GET", Path: "/api/v3/queue/status", Handler: apiv3.QueueStatusHandler(queueSvc, pendingCountLister{repo: pendingReleasesRepo}, queueStatusPauser)},
		{Method: "GET", Path: "/api/v3/releaseprofile", Handler: apiv3.ReleaseProfileHandler(releaseProfilesSvc)},
		{Method: "GET", Path: "/api/v3/releaseprofile/{id}", Handler: apiv3.ReleaseProfileByIDHandler(releaseProfilesSvc)},
		{Method: "POST", Path: "/api/v3/releaseprofile", Handler: apiv3.ReleaseProfileCreateHandler(releaseProfilesSvc)},
		{Method: "PUT", Path: "/api/v3/releaseprofile/{id}", Handler: apiv3.ReleaseProfileUpdateHandler(releaseProfilesSvc)},
		{Method: "DELETE", Path: "/api/v3/releaseprofile/{id}", Handler: apiv3.ReleaseProfileDeleteHandler(releaseProfilesSvc)},
		{Method: "GET", Path: "/api/v3/remotepathmapping", Handler: apiv3.RemotePathMappingHandler(remotePathMappingsSvc)},
		{Method: "GET", Path: "/api/v3/remotepathmapping/{id}", Handler: apiv3.RemotePathMappingByIDHandler(remotePathMappingsSvc)},
		{Method: "POST", Path: "/api/v3/remotepathmapping", Handler: apiv3.RemotePathMappingCreateHandler(remotePathMappingsSvc)},
		{Method: "PUT", Path: "/api/v3/remotepathmapping/{id}", Handler: apiv3.RemotePathMappingUpdateHandler(remotePathMappingsSvc)},
		{Method: "DELETE", Path: "/api/v3/remotepathmapping/{id}", Handler: apiv3.RemotePathMappingDeleteHandler(remotePathMappingsSvc)},
		{Method: "GET", Path: "/api/v3/rootfolder", Handler: apiv3.RootFolderHandler(rootFoldersSvc)},
		{Method: "GET", Path: "/api/v3/rootfolder/{id}", Handler: apiv3.RootFolderByIDHandler(rootFoldersSvc)},
		{Method: "POST", Path: "/api/v3/rootfolder", Handler: apiv3.RootFolderCreateHandler(rootFoldersSvc, rootFolderEmitter)},
		{Method: "DELETE", Path: "/api/v3/rootfolder/{id}", Handler: apiv3.RootFolderDeleteHandler(rootFoldersSvc, rootFolderEmitter)},
		{Method: "GET", Path: "/api/v3/series", Handler: apiv3.SeriesListHandler(seriesSvc)},
		{Method: "GET", Path: "/api/v3/system/status", Handler: apiv3.SystemStatusHandler(systemStatus, cfg)},
		{Method: "GET", Path: "/api/v3/tag", Handler: apiv3.TagHandler(tagsSvc)},
		{Method: "GET", Path: "/api/v3/tag/{id}", Handler: apiv3.TagByIDHandler(tagsSvc)},
		{Method: "POST", Path: "/api/v3/tag", Handler: apiv3.TagCreateHandler(tagsSvc)},
		{Method: "PUT", Path: "/api/v3/tag/{id}", Handler: apiv3.TagUpdateHandler(tagsSvc)},
		{Method: "DELETE", Path: "/api/v3/tag/{id}", Handler: apiv3.TagDeleteHandler(tagsSvc)},
		{Method: "GET", Path: "/api/v3/tag/detail", Handler: apiv3.TagDetailHandler(emptyTagDetailLister{})},
		{Method: "GET", Path: "/api/v3/update", Handler: apiv3.UpdateHandler(emptyUpdateLister{})},
		{Method: "GET", Path: "/api/v3/wanted/cutoff", Handler: apiv3.WantedHandler(wantedRepoLister{episodes: episodesRepo, now: func() time.Time { return time.Now().UTC() }}, apiv3.WantedCutoff)},
		{Method: "GET", Path: "/api/v3/wanted/missing", Handler: apiv3.WantedHandler(wantedRepoLister{episodes: episodesRepo, now: func() time.Time { return time.Now().UTC() }}, apiv3.WantedMissing)},
		// SignalR negotiate endpoint — the JS client posts here
		// first to discover the per-connection id and the available
		// transports before opening the WebSocket. Auth is the
		// shared requireAPIKey middleware; the SignalR-specific
		// `?access_token=` query was added to parseAPIKey in slice
		// 4.ES. The WebSocket transport itself sits at
		// /signalr/messages and is registered below.
		{Method: "POST", Path: "/signalr/messages/negotiate", Handler: signalr.NegotiateHandler(nil)},
		{Method: "OPTIONS", Path: "/signalr/messages/negotiate", Handler: signalr.NegotiateHandler(nil), Anonymous: true},
		{Method: "GET", Path: "/signalr/messages", Handler: signalr.WebSocketHandler(signalRHub, logger)},
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
