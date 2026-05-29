package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	autotaggingv5 "sonarr.tv/sonarr/internal/api/v5/autotagging"
	blocklistv5 "sonarr.tv/sonarr/internal/api/v5/blocklist"
	calendarv5 "sonarr.tv/sonarr/internal/api/v5/calendar"
	commandv5 "sonarr.tv/sonarr/internal/api/v5/command"
	connectionv5 "sonarr.tv/sonarr/internal/api/v5/connection"
	customfilterv5 "sonarr.tv/sonarr/internal/api/v5/customfilter"
	customformatv5 "sonarr.tv/sonarr/internal/api/v5/customformat"
	diskspacev5 "sonarr.tv/sonarr/internal/api/v5/diskspace"
	episodev5 "sonarr.tv/sonarr/internal/api/v5/episode"
	episodefilev5 "sonarr.tv/sonarr/internal/api/v5/episodefile"
	filesystemv5 "sonarr.tv/sonarr/internal/api/v5/filesystem"
	generalsettingsv5 "sonarr.tv/sonarr/internal/api/v5/generalsettings"
	healthv5 "sonarr.tv/sonarr/internal/api/v5/health"
	historyv5 "sonarr.tv/sonarr/internal/api/v5/history"
	importlistv5 "sonarr.tv/sonarr/internal/api/v5/importlist"
	importlistexclusionv5 "sonarr.tv/sonarr/internal/api/v5/importlistexclusion"
	importlistsettingsv5 "sonarr.tv/sonarr/internal/api/v5/importlistsettings"
	indexerv5 "sonarr.tv/sonarr/internal/api/v5/indexer"
	indexersettingsv5 "sonarr.tv/sonarr/internal/api/v5/indexersettings"
	localizationv5 "sonarr.tv/sonarr/internal/api/v5/localization"
	logv5 "sonarr.tv/sonarr/internal/api/v5/log"
	manualimportv5 "sonarr.tv/sonarr/internal/api/v5/manualimport"
	mediamanagementsettingsv5 "sonarr.tv/sonarr/internal/api/v5/mediamanagementsettings"
	metadatav5 "sonarr.tv/sonarr/internal/api/v5/metadata"
	namingsettingsv5 "sonarr.tv/sonarr/internal/api/v5/namingsettings"
	parsev5 "sonarr.tv/sonarr/internal/api/v5/parse"
	qualitydefinitionv5 "sonarr.tv/sonarr/internal/api/v5/qualitydefinition"
	qualityprofilev5 "sonarr.tv/sonarr/internal/api/v5/qualityprofile"
	queuev5 "sonarr.tv/sonarr/internal/api/v5/queue"
	queueactionv5 "sonarr.tv/sonarr/internal/api/v5/queueaction"
	queuedetailsv5 "sonarr.tv/sonarr/internal/api/v5/queuedetails"
	queuestatusv5 "sonarr.tv/sonarr/internal/api/v5/queuestatus"
	releasev5 "sonarr.tv/sonarr/internal/api/v5/release"
	releaseprofilev5 "sonarr.tv/sonarr/internal/api/v5/releaseprofile"
	releasepushv5 "sonarr.tv/sonarr/internal/api/v5/releasepush"
	remotepathmappingv5 "sonarr.tv/sonarr/internal/api/v5/remotepathmapping"
	renameepisodev5 "sonarr.tv/sonarr/internal/api/v5/renameepisode"
	rootfolderv5 "sonarr.tv/sonarr/internal/api/v5/rootfolder"
	seasonpassv5 "sonarr.tv/sonarr/internal/api/v5/seasonpass"
	seriesv5 "sonarr.tv/sonarr/internal/api/v5/series"
	seriesfolderv5 "sonarr.tv/sonarr/internal/api/v5/seriesfolder"
	seriesimportv5 "sonarr.tv/sonarr/internal/api/v5/seriesimport"
	serieslookupv5 "sonarr.tv/sonarr/internal/api/v5/serieslookup"
	systemv5 "sonarr.tv/sonarr/internal/api/v5/system"
	systembackupv5 "sonarr.tv/sonarr/internal/api/v5/systembackup"
	systemtaskv5 "sonarr.tv/sonarr/internal/api/v5/systemtask"
	tagv5 "sonarr.tv/sonarr/internal/api/v5/tag"
	uisettingsv5 "sonarr.tv/sonarr/internal/api/v5/uisettings"
	updatev5 "sonarr.tv/sonarr/internal/api/v5/update"
	updatesettingsv5 "sonarr.tv/sonarr/internal/api/v5/updatesettings"
	wantedv5 "sonarr.tv/sonarr/internal/api/v5/wanted"
	"sonarr.tv/sonarr/internal/auth"
	"sonarr.tv/sonarr/internal/autotagging"
	"sonarr.tv/sonarr/internal/backup"
	"sonarr.tv/sonarr/internal/blocklist"
	"sonarr.tv/sonarr/internal/commands"
	"sonarr.tv/sonarr/internal/config"
	"sonarr.tv/sonarr/internal/configfile"
	"sonarr.tv/sonarr/internal/customfilters"
	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/delayprofiles"
	"sonarr.tv/sonarr/internal/disk"
	"sonarr.tv/sonarr/internal/download"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodemonitored"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/faileddownloads"
	"sonarr.tv/sonarr/internal/frontend"
	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/history"
	"sonarr.tv/sonarr/internal/host"
	"sonarr.tv/sonarr/internal/importlistexclusions"
	"sonarr.tv/sonarr/internal/importlists"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/indexersearch"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/lifecycle"
	"sonarr.tv/sonarr/internal/localization"
	"sonarr.tv/sonarr/internal/logs"
	"sonarr.tv/sonarr/internal/mediacover"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/metadata"
	"sonarr.tv/sonarr/internal/namingconfig"
	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/parsingservice"
	"sonarr.tv/sonarr/internal/pendingreleases"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/qualitydefinitions"
	"sonarr.tv/sonarr/internal/qualityprofiles"
	"sonarr.tv/sonarr/internal/queue"
	"sonarr.tv/sonarr/internal/releasecache"
	"sonarr.tv/sonarr/internal/releaseprofiles"
	"sonarr.tv/sonarr/internal/remotepathmappings"
	"sonarr.tv/sonarr/internal/rootfolders"
	"sonarr.tv/sonarr/internal/scheduledtasks"
	"sonarr.tv/sonarr/internal/series"
	"sonarr.tv/sonarr/internal/signalr"
	"sonarr.tv/sonarr/internal/tags"
	"sonarr.tv/sonarr/internal/update"
)

// buildHostServer assembles the *host.Server from the runtime
// dependencies serve() already has in scope. Lives in its own file
// so main.go's wiring block stays scannable as Phase 5 grows.
//
// Mirrors `src/NzbDrone.Host/Bootstrap.cs:CreateConsoleHostBuilder`
// where BindAddress + Port are pulled from the configuration before
// the URL list is handed to Kestrel.
func buildHostServer(ctx context.Context, cfg *configfile.Provider, authSvc *auth.Service, indexerSearchSvc *indexersearch.Service, logger *slog.Logger, configRepo *config.Repo, configProv *config.Provider, qualityDefSvc *qualitydefinitions.Service, qualityProfilesSvc *qualityprofiles.Service, tagsSvc *tags.Service, autoTagsSvc *autotagging.Service, releaseProfilesSvc *releaseprofiles.Service, customFormatsSvc *customformats.Service, delayProfilesSvc *delayprofiles.Service, customFiltersSvc *customfilters.Service, queueSvc *queue.Service, rootFoldersSvc *rootfolders.Service, remotePathMappingsSvc *remotepathmappings.Service, namingConfigSvc *namingconfig.Service, notificationsRepo *notifications.Repo, indexersRepo *indexers.Repo, importListsRepo *importlists.Repo, importListExclusionsRepo *importlistexclusions.Repo, downloadClientsRepo *downloadclients.Repo, episodesSvc *episodes.Service, episodesRepo *episodes.Repo, seriesSvc *series.Service, mediaCoverConvertSvc *mediacover.Service, healthSvc *healthcheck.Service, metadataRepo *metadata.Repo, signalRHub *signalr.Hub, queueStatusPauser apiv3.Pauser, systemStatus apiv3.SystemStatusContext, pendingReleasesRepo *pendingreleases.Repo, blocklistRepo *blocklist.Repo, historyRepo *history.Repo, lifecycleSvc *lifecycle.Service, cmdSvc *commands.Service, cmdRegistry *commands.CommandRegistry, failedDownloadsSvc *faileddownloads.Service, taskSvc *scheduledtasks.Service, backupSvc *backup.Service, episodeFilesSvc *episodefiles.Service, diskProv *disk.Provider, bus *messaging.Bus, processDownloadDecisions *download.ProcessDownloadDecisions, downloadDecider *decisionengine.DownloadDecisionMaker, releaseCache *releasecache.Cache, parsingSvc *parsingservice.Service, manualImportDecisionRejector apiv3.ManualImportDecisionRejector, episodeFileDeleter *episodeFileDeleteAdapter, queueRemover apiv3.QueueWriter, recentUpdates apiv3.UpdateLister, logReader apiv3.LogLister, backupTempPath string, logFolderPath, frontendDir string) (*host.Server, error) {
	bind, err := cfg.BindAddress(ctx)
	if err != nil {
		return nil, fmt.Errorf("host: bind address: %w", err)
	}
	port, err := cfg.Port(ctx)
	if err != nil {
		return nil, fmt.Errorf("host: port: %w", err)
	}
	// Audit fix B.9: authAdapter bundles cfg + authSvc so the host
	// middleware can validate Forms-mode session cookies. It also
	// satisfies host.APIKeyResolver / AuthPolicy / AuthMethodResolver
	// via the embedded *configfile.Provider so no other call sites
	// change.
	authAdp := newAuthAdapter(cfg, authSvc)
	loginRoutes := []host.Route{
		{Method: "POST", Path: "/login", Handler: auth.LoginHandler(authSvc, authAdp), Anonymous: true},
		{Method: "GET", Path: "/logout", Handler: auth.LogoutHandler(authAdp), Anonymous: true},
		// J204 — /initialize.json must be Anonymous because the React
		// bundle hits it BEFORE knowing the API key. Any requireAPIKey
		// wrap here hard-blocks the UI from booting. C# parallel:
		// `InitializeJsonController` is `[Authorize(Policy = "UI")]`
		// which only requires forms-auth session cookies (not API key);
		// the Go-port defers the forms-auth gate until that middleware
		// ports — current behaviour matches the C# AuthenticationMethod=None
		// default path.
		{Method: "GET", Path: "/initialize.json", Handler: host.InitializeJSONHandler(cfg), Anonymous: true},
	}
	v3 := v3Routes(configProv, cfg, indexerSearchSvc, logger, qualityDefSvc, qualityProfilesSvc, tagsSvc, autoTagsSvc, releaseProfilesSvc, customFormatsSvc, delayProfilesSvc, customFiltersSvc, queueSvc, rootFoldersSvc, remotePathMappingsSvc, namingConfigSvc, notificationsRepo, indexersRepo, importListsRepo, importListExclusionsRepo, downloadClientsRepo, episodesSvc, episodesRepo, seriesSvc, mediaCoverConvertSvc, healthSvc, metadataRepo, signalRHub, queueStatusPauser, systemStatus, pendingReleasesRepo, blocklistRepo, historyRepo, lifecycleSvc, cmdSvc, cmdRegistry, failedDownloadsSvc, taskSvc, backupSvc, episodeFilesSvc, diskProv, bus, processDownloadDecisions, downloadDecider, releaseCache, parsingSvc, manualImportDecisionRejector, episodeFileDeleter, queueRemover, recentUpdates, logReader, backupTempPath, logFolderPath)
	opts := []host.Option{
		host.WithRoutes(append(loginRoutes, v3...)...),
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
	// J205 — wire the UI theme into the host so the HTML template
	// middleware can substitute `_THEME_` in index.html / login.html.
	// Lookup failures degrade silently to "auto" (the host's default
	// when WithTheme is never called) — a transient config-store hiccup
	// shouldn't block boot.
	if theme, err := cfg.Theme(ctx); err == nil && theme != "" {
		opts = append(opts, host.WithTheme(theme))
	}
	return host.New(
		host.Config{BindAddress: bind, Port: port},
		logger,
		configRepoProbe{configRepo},
		// *authAdapter satisfies host.APIKeyResolver via its
		// embedded *configfile.Provider, plus host.AuthPolicy /
		// AuthMethodResolver / SessionVerifier for the Forms +
		// External + DisabledForLocalAddresses branches.
		authAdp,
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

func (emptyTagDetailLister) TagDetail(context.Context, int) (*apiv3.TagDetailsResource, error) {
	return nil, apiv3.ErrTagDetailNotFound
}

// recentUpdateLister adapts *update.Service to apiv3.UpdateLister
// (J405). It ports C# RecentUpdateProvider → UpdateController feed:
// fetches the recent release packages from services.sonarr.tv and
// projects each to an apiv3.UpdateResource. The handler's
// markRecentUpdates (J403) then flags Latest/Installable/Installed.
// A nil service (update wiring absent) yields an empty list — the same
// byte-eq-safe posture the old emptyUpdateLister stub gave.
type recentUpdateLister struct {
	svc *update.Service
}

// logLister adapts *logs.Repo to apiv3.LogLister (J409): runs the paged
// log read and projects each row to an apiv3.LogResource. A nil repo
// (log DB disabled) yields an empty page, matching the C#
// LogDbEnabled=false short-circuit.
type logLister struct {
	repo *logs.Repo
}

func (l logLister) PagedLogs(ctx context.Context, page, pageSize int, sortKey string, desc bool, levels []string) ([]apiv3.LogResource, int, error) {
	if l.repo == nil {
		return nil, 0, nil
	}
	pg, err := l.repo.Paged(ctx, logs.PageQuery{Page: page, PageSize: pageSize, SortKey: sortKey, Desc: desc, Levels: levels})
	if err != nil {
		return nil, 0, err
	}
	out := make([]apiv3.LogResource, 0, len(pg.Records))
	for _, r := range pg.Records {
		out = append(out, apiv3.LogResource{
			ID:            r.ID,
			Time:          r.Time.UTC().Format("2006-01-02T15:04:05Z"),
			Level:         r.Level,
			Logger:        r.Logger,
			Message:       r.Message,
			Exception:     r.Exception,
			ExceptionType: r.ExceptionType,
		})
	}
	return out, pg.TotalRecords, nil
}

func (l recentUpdateLister) RecentUpdates(ctx context.Context) ([]apiv3.UpdateResource, error) {
	if l.svc == nil {
		return nil, nil
	}
	pkgs, err := l.svc.RecentUpdates(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]apiv3.UpdateResource, 0, len(pkgs))
	for _, p := range pkgs {
		out = append(out, apiv3.UpdateResource{
			Version:     p.Version,
			Branch:      p.Branch,
			ReleaseDate: p.ReleaseDate,
			FileName:    p.FileName,
			URL:         p.URL,
			Hash:        p.Hash,
			Changes:     apiv3.UpdateChanges{New: p.Changes.New, Fixed: p.Changes.Fixed},
		})
	}
	return out, nil
}

// queuePagedAdapter satisfies apiv3.QueuePagedLister by projecting the
// in-memory queue.Service cache into the paged QueueResource shape.
// Mirrors C# `QueueController.GetQueue` → `QueueResourceMapper.ToResource`
// at `src/Sonarr.Api.V3/Queue/QueueResource.cs:58-107`.
//
// J347 — Arc 3 slice 1 projected the scalar field set. Sub-resource
// hydration is now COMPLETE (the queue.Queue row carries the Series +
// Episodes domain objects, so no extra lookups were needed): Series +
// Episode hydrate-then-strip (J348), CustomFormats (J349),
// EpisodeIDs/SeasonNumbers (J351), Episodes plural (J352), and the
// CustomFormatScore recompute against the live profile (J359) — see
// projectQueueResource. Pagination follows the historyPagedAdapter
// convention (paginateInMemory in cache order); filter/sort applied via
// queueFilterCriteria + SortQueue (J357/J358).
type queuePagedAdapter struct {
	svc         *queue.Service
	qualityDefs *qualitydefinitions.Service
	// qualityProfiles backs the J359 CustomFormatScore recompute (C#
	// QueueResource.cs:75 recomputes against the CURRENT series quality
	// profile rather than the decision-time precomputed score). nil →
	// the precomputed score is kept (defensive fallback).
	qualityProfiles *qualityprofiles.Service
}

func (a queuePagedAdapter) PagedQueue(ctx context.Context, req apiv3.PagingRequest, filter apiv3.QueueFilter) ([]apiv3.QueueResource, int, error) {
	rows := a.svc.GetQueue()
	// J358 — apply the C# filter surface (seriesIds / protocol /
	// languages / quality / status + includeUnknownSeriesItems) BEFORE
	// sort + pagination. TotalRecords reflects the FILTERED count (C#
	// `pagingSpec.TotalRecords = fullQueue.Count`).
	rows = queue.FilterQueue(rows, queueFilterCriteria(filter))
	total := len(rows)
	// J357 — sort the filtered queue by the requested key BEFORE
	// paginating (mirrors C# QueueController.GetQueue MapToPagingSpec).
	// SortDirection defaults to "descending" (PagingRequest default);
	// only an explicit "ascending" flips it.
	ascending := strings.EqualFold(req.SortDirection, "ascending")
	rows = queue.SortQueue(rows, req.SortKey, ascending, a.qualityWeightFunc(ctx))
	page := paginateInMemory(rows, req)
	out := make([]apiv3.QueueResource, 0, len(page))
	for _, q := range page {
		res := projectQueueResource(q)
		// J359 — recompute CustomFormatScore against the CURRENT series
		// quality profile (C# QueueResource.cs:75
		// `Series.QualityProfile.CalculateCustomFormatScore(...)`). The
		// J349 projection reused q.RemoteEpisode's decision-time score,
		// which goes stale after a profile CF-score edit. Recompute from
		// the matched CustomFormats + the live profile; keep the
		// precomputed score on any lookup failure (defensive).
		if q.Series != nil && q.RemoteEpisode != nil {
			if score, ok := a.recomputeCustomFormatScore(ctx, q.Series.QualityProfileID, q.RemoteEpisode.CustomFormats); ok {
				res.CustomFormatScore = score
			}
		}
		out = append(out, res)
	}
	return out, total, nil
}

// recomputeCustomFormatScore resolves the series' current quality
// profile and scores the matched CustomFormats against it (C#
// `QualityProfile.CalculateCustomFormatScore`). Returns (score, true) on
// success; (0, false) when the profile can't be resolved so the caller
// keeps the decision-time precomputed score.
func (a queuePagedAdapter) recomputeCustomFormatScore(ctx context.Context, profileID int, formats []customformats.CustomFormat) (int, bool) {
	if a.qualityProfiles == nil || profileID == 0 {
		return 0, false
	}
	prof, err := a.qualityProfiles.Get(ctx, profileID)
	if err != nil || prof == nil {
		return 0, false
	}
	items, err := prof.ParsedFormatItems()
	if err != nil {
		return 0, false
	}
	present := make(map[int]bool, len(formats))
	for _, f := range formats {
		present[f.ID] = true
	}
	return qualityprofiles.CalculateCustomFormatScore(items, present), true
}

// queueFilterCriteria maps the wire-level apiv3.QueueFilter to the
// domain queue.FilterCriteria — parsing the protocol + status enum
// strings (case-insensitive) and building the id sets. Unrecognised
// protocol / status values drop to "no constraint" for that field
// (matching the C# nullable-enum binder which leaves an unparsed value
// null).
func queueFilterCriteria(f apiv3.QueueFilter) queue.FilterCriteria {
	c := queue.FilterCriteria{IncludeUnknownSeriesItems: f.IncludeUnknownSeriesItems}
	if len(f.SeriesIDs) > 0 {
		c.SeriesIDs = intSet(f.SeriesIDs)
	}
	if len(f.Languages) > 0 {
		c.Languages = intSet(f.Languages)
	}
	if len(f.Quality) > 0 {
		c.Quality = intSet(f.Quality)
	}
	switch strings.ToLower(f.Protocol) {
	case "usenet":
		p := indexers.DownloadProtocolUsenet
		c.Protocol = &p
	case "torrent":
		p := indexers.DownloadProtocolTorrent
		c.Protocol = &p
	}
	if len(f.Status) > 0 {
		set := make(map[queue.QueueStatus]bool, len(f.Status))
		for _, s := range f.Status {
			if st, ok := parseQueueStatus(s); ok {
				set[st] = true
			}
		}
		if len(set) > 0 {
			c.Status = set
		}
	}
	return c
}

// intSet builds a presence set from an int slice.
func intSet(in []int) map[int]bool {
	set := make(map[int]bool, len(in))
	for _, n := range in {
		set[n] = true
	}
	return set
}

// parseQueueStatus resolves a wire enum name (case-insensitive, matching
// the camelCase QueueStatus projection) back to the typed value.
func parseQueueStatus(s string) (queue.QueueStatus, bool) {
	want := strings.ToLower(strings.TrimSpace(s))
	for st := queue.QueueStatusUnknown; st <= queue.QueueStatusFallback; st++ {
		if strings.ToLower(st.String()) == want {
			return st, true
		}
	}
	return queue.QueueStatusUnknown, false
}

// qualityWeightFunc builds the quality→weight lookup the "quality" sort
// key needs, from the qualitydefinitions catalogue (ordered by Weight
// ASC; higher weight = higher quality). Returns nil when the catalogue
// is unavailable — SortQueue then falls back to Quality.ID ordering.
func (a queuePagedAdapter) qualityWeightFunc(ctx context.Context) func(quality.Quality) int {
	if a.qualityDefs == nil {
		return nil
	}
	defs, err := a.qualityDefs.All(ctx)
	if err != nil || len(defs) == 0 {
		return nil
	}
	weights := make(map[int]int, len(defs))
	for _, d := range defs {
		weights[d.Quality.ID] = d.Weight
	}
	return func(q quality.Quality) int { return weights[q.ID] }
}

// projectQueueResource maps a single queue.Queue domain row to the
// scalar apiv3.QueueResource wire shape (J347). Subresources (Series /
// Episode / Episodes / EpisodeIDs / SeasonNumbers / StatusMessages) +
// CustomFormats + CustomFormatScore are left nil/zero — DEFERRED to the
// J348+ hydration slices. Enum fields route through CamelCaseEnumName
// to match the C# JsonStringEnumConverter + camelCase policy; Protocol
// uses the lowercase String() form the C# DownloadProtocol enum emits.
func projectQueueResource(q queue.Queue) apiv3.QueueResource {
	res := apiv3.QueueResource{
		ID:                                  q.ID,
		Title:                               q.Title,
		Status:                              apiv3.CamelCaseEnumName(q.Status.String()),
		DownloadID:                          q.DownloadID,
		Protocol:                            q.Protocol.String(),
		Quality:                             apiv3.ToQualityModelResource(q.Quality),
		Size:                                q.Size,
		SizeLeft:                            q.SizeLeft,
		TimeLeft:                            apiv3.FormatTimeLeft(q.TimeLeft),
		Indexer:                             q.Indexer,
		DownloadClient:                      q.DownloadClient,
		DownloadClientHasPostImportCategory: q.DownloadClientHasPostImportCategory,
		OutputPath:                          q.OutputPath,
		ErrorMessage:                        q.ErrorMessage,
		Added:                               q.Added,
		EstimatedCompletionTime:             q.EstimatedCompletionTime,
	}
	if q.Series != nil {
		sid := q.Series.ID
		res.SeriesID = &sid
		// J348 — Series subresource hydration. The queue row already
		// carries the *series.Series domain object; project it
		// unconditionally here. The QueueHandler strips it back to nil
		// when the includeSeries query flag is absent (C# gates the
		// projection on includeSeries; the Go-port hydrates-then-strips
		// to keep the QueuePagedLister interface free of wire concerns).
		sr := apiv3.ToSeriesResource(*q.Series)
		res.Series = &sr
	}
	// C# maps EpisodeId from the singular model.Episode; the Go domain
	// carries Episodes[] — use the first entry.
	if len(q.Episodes) > 0 {
		eid := q.Episodes[0].ID
		res.EpisodeID = &eid
		// J348 — Episode subresource hydration (singular, v3 shape).
		// Same hydrate-then-strip posture as Series above; gated by the
		// includeEpisode query flag in QueueHandler.
		er := apiv3.ToEpisodeResource(q.Episodes[0])
		res.Episode = &er
		// J352 — Episodes subresource hydration (plural, v5 shape).
		// C# v5 QueueResource.cs:59 sets Episodes = includeEpisodes ?
		// model.Episodes?.ToResource() : null. Hydrate the full list
		// here; the v5 handler (QueueHandlerV5) gates it on the
		// includeSubresources[] enum, and the v3 handler strips it
		// unconditionally (v3's wire carries only the singular Episode).
		eps := make([]apiv3.EpisodeResource, 0, len(q.Episodes))
		for _, e := range q.Episodes {
			eps = append(eps, apiv3.ToEpisodeResource(e))
		}
		res.Episodes = &eps
	}
	// J351 — v5 always-set list fields. C# v5 QueueResource.cs:56-57 sets
	// EpisodeIds = model.Episodes?.Select(e => e.Id) (always) +
	// SeasonNumbers = model.SeasonNumber.HasValue ? [v] : [] (always);
	// QueueResource.cs:81 sets EpisodesWithFilesCount = model.Episodes
	// ?.Count(e => e.HasFile). These are ungated v5-additive fields on
	// the shared apiv3 type — v3 clients ignore the extra keys (the
	// J324/J318 additive-divergence posture). EpisodeIDs + SeasonNumbers
	// always emit a non-nil pointer to mirror C#'s `= []` default
	// (empty → wire `[]`). EpisodesWithFilesCount uses omitempty so 0
	// collapses to absent (the J318 accepted trade-off).
	episodeIDs := make([]int, 0, len(q.Episodes))
	withFiles := 0
	for _, e := range q.Episodes {
		episodeIDs = append(episodeIDs, e.ID)
		if e.EpisodeFileID != nil && *e.EpisodeFileID > 0 {
			withFiles++
		}
	}
	res.EpisodeIDs = &episodeIDs
	seasonNumbers := []int{}
	if q.SeasonNumber != nil {
		seasonNumbers = []int{*q.SeasonNumber}
	}
	res.SeasonNumbers = &seasonNumbers
	res.EpisodesWithFilesCount = withFiles
	// Languages — extract persisted IDs then project via ToLanguageRefs.
	if len(q.Languages) > 0 {
		ids := make([]int, 0, len(q.Languages))
		for _, l := range q.Languages {
			ids = append(ids, l.ID)
		}
		res.Languages = apiv3.ToLanguageRefs(ids)
	}
	if q.TrackedDownloadStatus != nil {
		res.TrackedDownloadStatus = apiv3.CamelCaseEnumName(q.TrackedDownloadStatus.String())
	}
	if q.TrackedDownloadState != nil {
		res.TrackedDownloadState = apiv3.CamelCaseEnumName(q.TrackedDownloadState.String())
	}
	// J349 — CustomFormats + CustomFormatScore from the row's
	// RemoteEpisode Subject. C# QueueResource.cs:75 sets
	// CustomFormats = model.RemoteEpisode?.CustomFormats?.ToResource(false)
	// + CustomFormatScore = Series.QualityProfile.CalculateCustomFormatScore.
	// The Go Subject carries both the matched CustomFormats slice and the
	// decision-time-precomputed score, so we project both directly (no
	// QP recompute — see the QueueResource doc-comment rationale).
	if q.RemoteEpisode != nil {
		if len(q.RemoteEpisode.CustomFormats) > 0 {
			cf := make([]apiv3.CustomFormatResource, 0, len(q.RemoteEpisode.CustomFormats))
			for _, m := range q.RemoteEpisode.CustomFormats {
				cf = append(cf, apiv3.CustomFormatResource{ID: m.ID, Name: m.Name})
			}
			res.CustomFormats = cf
		}
		res.CustomFormatScore = q.RemoteEpisode.CustomFormatScore
	}
	// J350 — StatusMessages projection. C# QueueResource.cs:89 sets
	// StatusMessages = model.StatusMessages (always a non-null list).
	// The Go domain simplified the C# TrackedDownloadStatusMessage
	// {Title, Messages[]} shape to a flat []string at the trackeddownloads
	// layer; this slice (the "queue / UI integration slice" the
	// trackeddownloads doc-comment anticipated) synthesises each flat
	// string into the typed QueueStatusMessage as the Title (header
	// line) with an empty Messages list — the natural flat-string →
	// header mapping. The pointer is always set non-nil to mirror C#'s
	// non-null list (empty domain slice → wire `"statusMessages":[]`).
	msgs := make([]apiv3.QueueStatusMessage, 0, len(q.StatusMessages))
	for _, s := range q.StatusMessages {
		msgs = append(msgs, apiv3.QueueStatusMessage{Title: s, Messages: []string{}})
	}
	res.StatusMessages = &msgs
	return res
}

// emptyQueueDetailsLister is the empty-result stand-in for
// /queue/details. Empty-DB recording shows zero records; the
// production wiring substitutes the empty implementation today.
// Populated-row projection lands when the tracked-downloads /
// pending-release join surface ports.
type emptyQueueDetailsLister struct{}

func (emptyQueueDetailsLister) QueueDetails(context.Context, *int, []int) ([]apiv3.QueueResource, error) {
	return nil, nil
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

// blocklistPagedAdapter wraps *blocklist.Repo + 2 narrow lookups
// for populated-row CustomFormat calculation (J337 — same pattern
// as historyPagedAdapter at J336, minus the score lookup since
// BlocklistResource is score-less per the J297 wire-through note).
//
// The fresh-DB contract gate is the empty-array shape; for
// populated DBs the adapter reads All() and slices in-memory by
// Page/PageSize. SortKey/SortDirection params are honoured at the
// envelope level but not yet applied to the underlying query —
// DEFERRED until a richer paged repo method lands. Per-record
// fields beyond the skeleton (Title from Episode join, etc.) are
// also DEFERRED.
//
// J337 populated-row mapper wire-through: nil-safe lookups (any
// missing dep falls back to empty CustomFormats matching pre-J337
// stub behavior).
type blocklistPagedAdapter struct {
	repo             *blocklist.Repo
	seriesSvc        historySeriesLookup
	customFormatsSvc historyCustomFormatsLookup
}

func (a blocklistPagedAdapter) PagedBlocklist(ctx context.Context, req apiv3.PagingRequest) ([]apiv3.BlocklistResource, int, error) {
	rows, err := a.repo.All(ctx)
	if err != nil {
		return nil, 0, err
	}
	total := len(rows)
	page := paginateInMemory(rows, req)
	out := make([]apiv3.BlocklistResource, 0, len(page))

	// Per-request cache: load allFormats once + memoize series
	// lookups across rows of the same page.
	var allFormats []*customformats.Format
	if a.customFormatsSvc != nil {
		if formats, err := a.customFormatsSvc.All(ctx); err == nil {
			allFormats = formats
		}
	}
	seriesCache := map[int]*series.Series{}
	// J382 series-embed cache: BASE SeriesResource memoized across
	// rows so the per-page conversion happens once per distinct id;
	// the underlying domain-series loads are shared with the
	// customFormat path via seriesCache.
	seriesEmbedCache := map[int]*apiv3.SeriesResource{}

	for _, e := range page {
		episodeIDs := e.EpisodeIDs
		if episodeIDs == nil {
			// Match C# `required List<int> EpisodeIds` non-null
			// semantic — emit `[]` rather than `null` when the
			// persisted row has no episode-ids.
			episodeIDs = []int{}
		}
		entry := e // copy for pointer capture
		customFormats := computeBlocklistCustomFormats(ctx, &entry, a, allFormats, seriesCache)
		res := apiv3.BlocklistResource{
			ID:            e.ID,
			SeriesID:      e.SeriesID,
			EpisodeIDs:    episodeIDs,
			SourceTitle:   e.SourceTitle,
			Date:          e.Date.UTC().Format("2006-01-02T15:04:05Z"),
			Languages:     apiv3.ToLanguageRefs(e.Languages),
			Quality:       blocklistQualityResource(e.Quality),
			Protocol:      apiv3.BlocklistProtocolName(e.Protocol),
			Indexer:       e.Indexer,
			Message:       e.Message,
			Source:        e.Source,
			CustomFormats: customFormats,
			// Title is derived from Episode.Title in C# — DEFERRED
			// until the episode-join wiring lands. EpisodeIDs/
			// Protocol/Indexer/Message added at J311. Languages
			// (J314): typed []LanguageRef projection. Quality (J382):
			// decoded from the persisted opaque-JSON column via
			// blocklistQualityResource → apiv3.ToQualityModelResource;
			// nil/unparseable drops the omitempty key. Series (J382):
			// hydrated below (C# projects it unconditionally — blocklist
			// GET has no include flag). CustomFormats (J337): populated
			// via computeBlocklistCustomFormats; nil deps fall back to
			// empty slice. Note: BlocklistResource is score-less per the
			// J297 wire-through (no CustomFormatScore field).
		}
		if a.seriesSvc != nil && e.SeriesID != 0 {
			res.Series = a.hydrateSeriesEmbed(ctx, e.SeriesID, seriesCache, seriesEmbedCache)
		}
		out = append(out, res)
	}
	return out, total, nil
}

// hydrateSeriesEmbed loads the BASE SeriesResource for the J382 paged
// blocklist Series subresource, sharing the domain-series load with the
// customFormat path (seriesCache) and memoizing the converted resource
// (embedCache, negative-nil cached). Mirrors the historyPagedAdapter
// J380 embed helper. C# BlocklistResourceMapper projects Series
// unconditionally (model.Series.ToResource()).
func (a blocklistPagedAdapter) hydrateSeriesEmbed(ctx context.Context, seriesID int, seriesCache map[int]*series.Series, embedCache map[int]*apiv3.SeriesResource) *apiv3.SeriesResource {
	if sr, ok := embedCache[seriesID]; ok {
		return sr
	}
	ser, cached := seriesCache[seriesID]
	if !cached {
		s, err := a.seriesSvc.GetSeries(ctx, seriesID)
		if err != nil {
			seriesCache[seriesID] = nil
			embedCache[seriesID] = nil
			return nil
		}
		ser = s
		seriesCache[seriesID] = ser
	}
	var sr *apiv3.SeriesResource
	if ser != nil {
		r := apiv3.ToSeriesResource(*ser)
		sr = &r
	}
	embedCache[seriesID] = sr
	return sr
}

// blocklistQualityResource decodes the persisted opaque-JSON quality
// column (the bare-int Model shape) into the typed QualityModelResource
// DTO for the J382 paged blocklist Quality projection. Empty / unparseable
// → nil (drops the omitempty key). C# projects model.Quality directly.
func blocklistQualityResource(raw string) *apiv3.QualityModelResource {
	if raw == "" {
		return nil
	}
	var qm quality.Model
	if err := json.Unmarshal([]byte(raw), &qm); err != nil {
		return nil
	}
	return apiv3.ToQualityModelResource(qm)
}

// backupAdmin adapts *backup.Service + stdlib fs helpers to
// apiv3.BackupAdmin. The C# BackupController calls IDiskProvider
// directly for FileExists + DeleteFile; the Go port collapses the
// indirection into stdlib calls via the helper functions in
// internal/api/v3/system_backup.go.
// seriesFolderCleaner narrows the disk.Provider surface to the
// methods seriesCascadeAdapter actually uses (DeleteFolder for the
// cascade-delete path, MoveFolder for the moveFiles=true path).
// Production wires *disk.Provider; tests pass a recording fake.
// nil-safe — when the adapter holds a nil cleaner, the disk steps
// are skipped.
type seriesFolderCleaner interface {
	DeleteFolder(path string, recursive bool) error
	MoveFolder(src, dst string) error
}

// seriesCascadeAdapter bundles series + episodes + episodefiles
// services + a disk-folder cleaner so apiv3.SeriesDeleteHandler's
// cascade-aware delete path (gated on ?deleteFiles=true /
// body.DeleteFiles) can remove rows from 3 tables AND clean up the
// series folder on disk.
//
// Embedding *series.Service inherits the 5.D1a thin pass-through
// methods (Insert / Update / Delete / GetSeries / GetAllSeries) so
// the adapter satisfies apiv3.SeriesWriter without forwarding each
// method by hand; only DeleteCascade is implemented here.
//
// On-disk cascade matches C# `SeriesService.DeleteSeries` at
// `src/NzbDrone.Core/Tv/SeriesService.cs:222-242`: load the series,
// delete its folder via DiskProvider (logged + continue on error),
// then publish SeriesDeletedEvent which cascades the DB rows.
// 5.D8 collapses the C# event-bus indirection into a direct
// sequential call chain because no other Go-port consumer reacts
// to SeriesDeletedEvent today.
type seriesCascadeAdapter struct {
	*series.Service
	episodes     *episodes.Service
	episodefiles *episodefiles.Service
	disk         seriesFolderCleaner
	exclusions   *importlistexclusions.Service
	logger       *slog.Logger
}

// MoveFolder satisfies apiv3.SeriesMover. Delegates to the bundled
// *disk.Provider's MoveFolder method (5.D9a port pre-work). Nil-safe:
// when the adapter holds no disk cleaner (e.g. test wiring), the
// call is a no-op and the handler's type-assert continues to the
// DB-only update. Mirrors C# `MoveSeriesCommand` handler at
// `src/NzbDrone.Core/Tv/MoveSeriesService.cs` collapsing the
// command-bus indirection (no other Go-port consumer reacts to
// MoveSeriesCommand today).
func (a *seriesCascadeAdapter) MoveFolder(_ context.Context, src, dst string) error {
	if a.disk == nil {
		return nil
	}
	return a.disk.MoveFolder(src, dst)
}

// SetEpisodeMonitoredBySeason satisfies seriesv5.SeasonEpisodeMonitorer
// (J397): cascades the per-episode monitored state when the v5
// season-monitor endpoint flips a season's Monitored flag. Delegates to
// episodes.Service.SetEpisodeMonitoredBySeason (bulk WHERE update +
// EpisodesMonitoredChanged publish). No-op when no episodes service is
// wired.
func (a *seriesCascadeAdapter) SetEpisodeMonitoredBySeason(ctx context.Context, seriesID, seasonNumber int, monitored bool) error {
	if a.episodes == nil {
		return nil
	}
	_, err := a.episodes.SetEpisodeMonitoredBySeason(ctx, seriesID, seasonNumber, monitored)
	return err
}

// AddImportListExclusion satisfies apiv3.SeriesExclusionAdder. Loads
// the series row to read TvdbID + Title, then calls
// importlistexclusions.Service.Add. Mirrors C# `SeriesService.DeleteSeries`
// at SeriesService.cs:233-238 which inserts the exclusion BEFORE
// the row is removed (so the TvdbID + Title are still readable).
//
// Idempotent: if the TvdbID is already on the exclusions table (the
// schema's TvdbId UNIQUE constraint enforces this), the underlying
// Repo.Insert path returns an error which the C# tree swallows.
// The Go port surfaces the error today; a follow-up may swallow
// constraint-violations explicitly. For now real-world race-free
// usage shouldn't hit this since the React UI gates the flag on a
// confirmation dialog.
//
// Tvdb ID of 0 (a series with no TvdbID set — should be rare since
// the AddSeries pipeline enforces TvdbID > 0) is skipped to avoid
// inserting a 0-keyed exclusion. Logged.
func (a *seriesCascadeAdapter) AddImportListExclusion(ctx context.Context, id int) error {
	if a.exclusions == nil {
		return nil
	}
	row, err := a.GetSeries(ctx, id)
	if err != nil {
		return err
	}
	if row.TvdbID == 0 {
		if a.logger != nil {
			a.logger.Warn("series exclusion-add: skipping series with TvdbID=0", "id", id, "title", row.Title)
		}
		return nil
	}
	_, err = a.exclusions.Add(ctx, &importlistexclusions.Exclusion{
		TvdbID: row.TvdbID,
		Title:  row.Title,
	})
	return err
}

// DeleteCascade removes the on-disk series folder + child rows
// (episodefiles → episodes) → the series row itself.
//
// Order:
//
//  1. Load the series row to read Path (the on-disk folder location).
//  2. If Path is non-empty and a disk cleaner is wired, call
//     DeleteFolder(Path, recursive=true). Errors are LOGGED + the
//     cascade continues — matches C# DiskProvider behaviour where
//     on-disk delete failures (permission, in-use file) don't abort
//     the DB-row cleanup. Operators can manually remove leftover
//     files later; the database state stays consistent.
//  3. Bulk-delete episodefiles for the series.
//  4. Bulk-delete episodes for the series.
//  5. Delete the series row itself.
//
// FK-parent-last order (children first) ensures foreign-key
// constraints don't reject the series delete.
//
// Partial-failure semantics: if a DB-cascade step fails the series
// row stays; the user sees the error and can retry; orphaned child
// rows are recovered on retry. The on-disk failure is non-fatal
// (logged only) per the doc-comment above.
func (a *seriesCascadeAdapter) DeleteCascade(ctx context.Context, id int) error {
	// Step 1: load the series for its Path.
	row, err := a.GetSeries(ctx, id)
	if err != nil {
		return err
	}
	// Step 2: on-disk folder delete (best-effort, logged on failure).
	if a.disk != nil && row.Path != "" {
		if dErr := a.disk.DeleteFolder(row.Path, true); dErr != nil {
			if a.logger != nil {
				a.logger.Warn("series cascade: on-disk DeleteFolder failed; continuing with DB cleanup",
					"id", id, "path", row.Path, "err", dErr)
			}
		}
	}
	// Steps 3-5: DB-row cascade in FK-parent-last order.
	if _, err := a.episodefiles.DeleteBySeriesID(ctx, id); err != nil {
		return fmt.Errorf("series cascade: episodefiles: %w", err)
	}
	if _, err := a.episodes.DeleteBySeriesID(ctx, id); err != nil {
		return fmt.Errorf("series cascade: episodes: %w", err)
	}
	return a.Delete(ctx, id)
}

type backupAdmin struct{ svc *backup.Service }

func (a backupAdmin) GetBackups() ([]backup.Backup, error) { return a.svc.GetBackups() }
func (a backupAdmin) GetBackupFolderForType(t backup.Type) string {
	return a.svc.GetBackupFolderForType(t)
}
func (a backupAdmin) Restore(backupFileName string) error { return a.svc.Restore(backupFileName) }
func (a backupAdmin) FileExists(path string) bool         { return apiv3FileExists(path) }
func (a backupAdmin) DeleteFile(path string) error        { return apiv3DeleteFile(path) }

// backupUploader adapts *backup.Service + stdlib filesystem helpers to
// apiv3.BackupUploader. Mirrors C# BackupController.UploadAndRestore's
// IAppFolderInfo.TempFolder + IDiskProvider.SaveStream + Restore +
// DeleteFile call chain. The temp path is snapshot at boot from the
// detected AppFolderInfo; the C# tree does the same via DI constructor
// injection of IAppFolderInfo.
type backupUploader struct {
	svc      *backup.Service
	tempPath string
}

func (a backupUploader) TempPath() string { return a.tempPath }
func (a backupUploader) SaveStream(r io.Reader, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := io.Copy(f, r); err != nil {
		return fmt.Errorf("write %q: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %q: %w", path, err)
	}
	return nil
}
func (a backupUploader) Restore(backupFileName string) error { return a.svc.Restore(backupFileName) }
func (a backupUploader) DeleteFile(path string) error        { return apiv3DeleteFile(path) }

// registryNotificationTester adapts notifications.Registry +
// notifications.Dependencies to apiv3.NotificationTester. Construct
// runs the registry lookup + per-provider factory; Test runs the
// constructed provider's per-row connection self-check.
//
// Mirrors C# `ProviderFactory.Test(definition)` at
// `src/NzbDrone.Core/ThingiProvider/ProviderFactoryBase.cs:148-186`
// where the factory builds an instance of the concrete provider
// class and invokes its Test() method. The Go port collapses the C#
// factory cache (which keeps a single instance per definition for
// notifications.Service) into a per-call Construct — Test is a
// non-hot path and avoiding the cache simplifies the wiring.
type registryNotificationTester struct {
	registry *notifications.Registry
	deps     notifications.Dependencies
}

func (a *registryNotificationTester) Test(ctx context.Context, n *notifications.Notification) ([]notifications.TestFailure, error) {
	provider, err := a.registry.Construct(n, a.deps)
	if err != nil {
		return nil, err
	}
	return provider.Test(ctx)
}

// RequestAction satisfies apiv3.NotificationActioner. Mirrors C#
// `ProviderFactoryBase.RequestAction(definition, action, query)` at
// `ProviderFactoryBase.cs:204-220` — Construct the provider, then
// delegate to its per-action map (Plex OAuth, Trakt OAuth, Twitter
// PIN flow, PushBullet device enumeration, etc.). Concrete providers
// returning nil emit JSON `null`; the v3 React UI tolerates either.
func (a *registryNotificationTester) RequestAction(ctx context.Context, n *notifications.Notification, action string, query map[string]string) (any, error) {
	provider, err := a.registry.Construct(n, a.deps)
	if err != nil {
		return nil, err
	}
	return provider.RequestAction(ctx, action, query)
}

// registryIndexerTester adapts indexers.Registry + Dependencies to
// apiv3.IndexerTester. Same shape as registryNotificationTester —
// would collapse to a generic shim once a third provider family
// demands it.
type registryIndexerTester struct {
	registry *indexers.Registry
	deps     indexers.Dependencies
}

func (a *registryIndexerTester) Test(ctx context.Context, n *indexers.Indexer) ([]indexers.TestFailure, error) {
	provider, err := a.registry.Construct(n, a.deps)
	if err != nil {
		return nil, err
	}
	return provider.Test(ctx)
}

// RequestAction satisfies apiv3.IndexerActioner. Mirrors C#
// `IndexerFactory.RequestAction` — Construct the provider then
// delegate to its per-action map (Newznab getCategories /
// newznabPresets, etc.).
func (a *registryIndexerTester) RequestAction(ctx context.Context, n *indexers.Indexer, action string, query map[string]string) (any, error) {
	provider, err := a.registry.Construct(n, a.deps)
	if err != nil {
		return nil, err
	}
	return provider.RequestAction(ctx, action, query)
}

// registryImportListTester adapts importlists.Registry + Dependencies
// to apiv3.ImportListTester. Same shape as the indexer + notification
// tester adapters — would collapse to a generic shim once a fourth
// provider family demands it.
type registryImportListTester struct {
	registry *importlists.Registry
	deps     importlists.Dependencies
}

func (a *registryImportListTester) Test(ctx context.Context, n *importlists.ImportList) ([]importlists.TestFailure, error) {
	provider, err := a.registry.Construct(n, a.deps)
	if err != nil {
		return nil, err
	}
	return provider.Test(ctx)
}

// RequestAction satisfies apiv3.ImportListActioner.
func (a *registryImportListTester) RequestAction(ctx context.Context, n *importlists.ImportList, action string, query map[string]string) (any, error) {
	provider, err := a.registry.Construct(n, a.deps)
	if err != nil {
		return nil, err
	}
	return provider.RequestAction(ctx, action, query)
}

// registryDownloadClientTester adapts downloadclients.Registry +
// Dependencies to apiv3.DownloadClientTester. Same pattern as the
// indexer + notification + importlist twins.
type registryDownloadClientTester struct {
	registry *downloadclients.Registry
	deps     downloadclients.Dependencies
}

func (a *registryDownloadClientTester) Test(ctx context.Context, n *downloadclients.DownloadClient) ([]downloadclients.TestFailure, error) {
	provider, err := a.registry.Construct(n, a.deps)
	if err != nil {
		return nil, err
	}
	return provider.Test(ctx)
}

// RequestAction satisfies apiv3.DownloadClientActioner.
func (a *registryDownloadClientTester) RequestAction(ctx context.Context, n *downloadclients.DownloadClient, action string, query map[string]string) (any, error) {
	provider, err := a.registry.Construct(n, a.deps)
	if err != nil {
		return nil, err
	}
	return provider.RequestAction(ctx, action, query)
}

// registryMetadataTester adapts metadata.Registry + Dependencies to
// apiv3.MetadataTester (and the upcoming MetadataActioner for 5.C5d).
type registryMetadataTester struct {
	registry *metadata.Registry
	deps     metadata.Dependencies
}

func (a *registryMetadataTester) Test(ctx context.Context, n *metadata.Metadata) ([]metadata.TestFailure, error) {
	provider, err := a.registry.Construct(n, a.deps)
	if err != nil {
		return nil, err
	}
	return provider.Test(ctx)
}

// RequestAction satisfies apiv3.MetadataActioner.
func (a *registryMetadataTester) RequestAction(ctx context.Context, n *metadata.Metadata, action string, query map[string]string) (any, error) {
	provider, err := a.registry.Construct(n, a.deps)
	if err != nil {
		return nil, err
	}
	return provider.RequestAction(ctx, action, query)
}

// apiv3FileExists + apiv3DeleteFile defer to os.Stat / os.Remove.
// Named with the apiv3 prefix to keep the cmd-package surface clean
// since the v3 layer's unexported osFileExists / osDeleteFile aren't
// reachable from main package.
func apiv3FileExists(path string) bool {
	info, err := osStatCmd(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func apiv3DeleteFile(path string) error {
	if err := osRemoveCmd(path); err != nil {
		return fmt.Errorf("delete %q: %w", path, err)
	}
	return nil
}

// osStatCmd + osRemoveCmd are package-local shims so tests can swap
// them; the default implementations call os.Stat/os.Remove.
var (
	osStatCmd   = os.Stat
	osRemoveCmd = os.Remove
)

// mediaCoverLister adapts the boot-time-resolved MediaCover folder
// to apiv3.MediaCoverLister.
type mediaCoverLister struct{ folder string }

func (m mediaCoverLister) MediaCoverFolder() string { return m.folder }

// logFileLister adapts the boot-time-resolved log folder + url base
// to apiv3.LogFileLister. Both fields are captured at server start;
// the C# IConfigFileProvider.UrlBase getter does re-resolve per
// request, but the empty-DB recording shows urlbase doesn't change
// after boot — the snapshot keeps the handler hot path lock-free.
type logFileLister struct {
	folder       string
	updateFolder string
	urlBase      string
}

func (l logFileLister) LogFolder() string       { return l.folder }
func (l logFileLister) UpdateLogFolder() string { return l.updateFolder }
func (l logFileLister) URLBase() string         { return l.urlBase }

// diskSpaceFromRootFolders adapts *rootfolders.Service to
// apiv3.FreeSpaceLister. Each configured root folder becomes one
// DiskSpaceResource; rows with nil FreeSpace/TotalSpace (disk-stat
// probe failed) are dropped to match the C# behaviour where
// `_diskProvider.GetAvailableSpace(path)` returning null skips the
// row at `DiskSpaceService.cs:75-82`. Label stays empty pending a
// GetVolumeLabel adapter port.
type diskSpaceFromRootFolders struct{ svc *rootfolders.Service }

func (a diskSpaceFromRootFolders) GetFreeSpace(ctx context.Context) ([]apiv3.DiskSpaceResource, error) {
	folders, err := a.svc.AllWithUnmappedFolders(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]apiv3.DiskSpaceResource, 0, len(folders))
	for _, f := range folders {
		if f.FreeSpace == nil || f.TotalSpace == nil {
			continue
		}
		out = append(out, apiv3.DiskSpaceResource{
			Path:       f.Path,
			Label:      "",
			FreeSpace:  *f.FreeSpace,
			TotalSpace: *f.TotalSpace,
		})
	}
	return out, nil
}

// historyPagedAdapter wraps *history.Repo + 3 narrow lookups for
// populated-row CustomFormat + score calculation. In-memory paging,
// sort params unused — same posture as blocklistPagedAdapter.
//
// J336 populated-row mapper wire-through: the 3 lookups
// (seriesSvc/qualityProfilesSvc/customFormatsSvc) are nil-safe;
// when any is nil the adapter falls back to the empty
// CustomFormats slice + 0 score (matches the pre-J336 stub
// behavior). Production wiring injects all 3.
type historyPagedAdapter struct {
	repo               *history.Repo
	seriesSvc          historySeriesLookup
	qualityProfilesSvc historyQualityProfileLookup
	customFormatsSvc   historyCustomFormatsLookup
	// episodesSvc backs the J380 paged includeEpisode embed. nil-safe —
	// a missing dep degrades the embed to absent.
	episodesSvc historyEpisodeLookup
	// upgradable backs the J381 paged qualityCutoffNotMet flag. nil →
	// the flag stays at the false default (graceful degrade).
	upgradable *decisionengine.UpgradableSpecification
}

// Narrow lookup interfaces for historyPagedAdapter. Production
// wires *series.Service / *qualityprofiles.Service /
// *customformats.Service — each satisfies the corresponding
// interface via the same-named methods. Decoupled so the adapter
// is testable with fakes without dragging the full service ctors
// + their persistence DBs into the test.
//
// J336.
type historySeriesLookup interface {
	GetSeries(ctx context.Context, id int) (*series.Series, error)
}

type historyQualityProfileLookup interface {
	Get(ctx context.Context, id int) (*qualityprofiles.Profile, error)
}

type historyCustomFormatsLookup interface {
	All(ctx context.Context) ([]*customformats.Format, error)
}

// historyEpisodeLookup is the by-id episode read the J377 history
// includeEpisode embed needs. *episodes.Service satisfies it.
type historyEpisodeLookup interface {
	GetEpisode(ctx context.Context, id int) (*episodes.Episode, error)
}

func (a historyPagedAdapter) PagedHistory(ctx context.Context, req apiv3.PagingRequest, includeSeries, includeEpisode bool) ([]apiv3.HistoryResource, int, error) {
	rows, err := a.repo.All(ctx)
	if err != nil {
		return nil, 0, err
	}
	total := len(rows)
	page := paginateInMemory(rows, req)
	out := make([]apiv3.HistoryResource, 0, len(page))

	// Per-request caches: load allFormats once + memoize series +
	// quality-profile lookups across rows of the same page.
	var allFormats []*customformats.Format
	if a.customFormatsSvc != nil {
		if formats, err := a.customFormatsSvc.All(ctx); err == nil {
			allFormats = formats
		}
	}
	seriesCache := map[int]*series.Series{}
	profileCache := map[int]*qualityprofiles.Profile{}
	// J380 embed caches: BASE Series / Episode resources memoized
	// across rows of the same page (history lists repeat series ids).
	// Only populated when the respective include flag is set, so the
	// per-row DB lookup is skipped entirely when the client didn't ask.
	seriesEmbedCache := map[int]*apiv3.SeriesResource{}
	episodeEmbedCache := map[int]*apiv3.EpisodeResource{}

	for _, e := range page {
		entry := e // copy for pointer capture
		customFormats, customFormatScore := computeHistoryCustomFormats(ctx, &entry, a, allFormats, seriesCache, profileCache)
		eventTypeName := ""
		if e.EventType != nil {
			eventTypeName = history.EventType(*e.EventType).String()
		}
		res := apiv3.HistoryResource{
			ID:                e.ID,
			SeriesID:          e.SeriesID,
			EpisodeID:         e.EpisodeID,
			SourceTitle:       e.SourceTitle,
			Date:              e.Date.UTC().Format("2006-01-02T15:04:05Z"),
			DownloadID:        e.DownloadID,
			EventType:         eventTypeName,
			Languages:         apiv3.ToLanguageRefs(e.Languages),
			Data:              e.Data,
			CustomFormats:     customFormats,
			CustomFormatScore: customFormatScore,
			// EventType (J379): name-mapped via history.EventType.String(),
			// porting the since/series-path mapping (history.go:310-311)
			// into the paged adapter — was the last clean EventType gap.
			// Series / Episode (J380): BASE embeds hydrated below, gated
			// on the includeSeries / includeEpisode flags.
			// Data (J310): per-history grab-time metadata dict; nil
			// drops the key via omitempty, populated emits `"data":{...}`.
			// Languages (J314): typed []LanguageRef projection from
			// the persisted []int form via apiv3.ToLanguageRefs.
			// CustomFormats + CustomFormatScore (J336): populated via
			// computeHistoryCustomFormats using the calculator engine
			// (internal/customformats.ParseCustomFormatForHistory) +
			// qualityprofile score lookup (qualityprofiles.
			// CalculateCustomFormatScore). nil deps fall back to
			// empty slice + 0 score matching the pre-J336 stub.
		}
		// J381: qualityCutoffNotMet is computed UNCONDITIONALLY (C#
		// HistoryController.MapToResource sets it whenever model.Series
		// != null, not gated by an include flag — matches the J378
		// since/series path). The per-row series + profile loads reuse
		// the same seriesCache / profileCache the customFormat path
		// fills, so the cost is bounded by distinct ids per page.
		res.QualityCutoffNotMet = a.qualityCutoffNotMet(ctx, &entry, seriesCache, profileCache)
		if includeSeries && a.seriesSvc != nil && e.SeriesID != 0 {
			res.Series = a.hydrateSeriesEmbed(ctx, e.SeriesID, seriesEmbedCache)
		}
		if includeEpisode && a.episodesSvc != nil && e.EpisodeID != 0 {
			res.Episode = a.hydrateEpisodeEmbed(ctx, e.EpisodeID, episodeEmbedCache)
		}
		out = append(out, res)
	}
	return out, total, nil
}

// hydrateSeriesEmbed loads the BASE SeriesResource for the J380 paged
// includeSeries embed, memoized via the per-request cache (negative
// results cached as nil so a repeated missing id doesn't re-hit the
// service). Mirrors historyServiceWithPopulator.HistorySeriesResource.
func (a historyPagedAdapter) hydrateSeriesEmbed(ctx context.Context, seriesID int, cache map[int]*apiv3.SeriesResource) *apiv3.SeriesResource {
	if sr, ok := cache[seriesID]; ok {
		return sr
	}
	var sr *apiv3.SeriesResource
	if s, err := a.seriesSvc.GetSeries(ctx, seriesID); err == nil && s != nil {
		r := apiv3.ToSeriesResource(*s)
		sr = &r
	}
	cache[seriesID] = sr
	return sr
}

// hydrateEpisodeEmbed loads the BASE EpisodeResource for the J380 paged
// includeEpisode embed, memoized via the per-request cache. Mirrors
// historyServiceWithPopulator.HistoryEpisodeResource.
func (a historyPagedAdapter) hydrateEpisodeEmbed(ctx context.Context, episodeID int, cache map[int]*apiv3.EpisodeResource) *apiv3.EpisodeResource {
	if er, ok := cache[episodeID]; ok {
		return er
	}
	var er *apiv3.EpisodeResource
	if ep, err := a.episodesSvc.GetEpisode(ctx, episodeID); err == nil && ep != nil {
		r := apiv3.ToEpisodeResource(*ep)
		er = &r
	}
	cache[episodeID] = er
	return er
}

// qualityCutoffNotMet computes the J381 paged qualityCutoffNotMet flag:
// decode the row's persisted quality → resolve the owning series'
// quality profile → run the ported UpgradableSpecification.QualityCutoffNotMet
// (newQuality omitted, matching C# HistoryController.MapToResource). Reuses
// the per-request series + profile caches (negative-nil cached) so the
// loads are shared with the customFormat path. Any missing dep / decode /
// lookup failure degrades to false. Mirrors the J378
// historyServiceWithPopulator.PopulateHistoryQualityCutoffNotMet.
func (a historyPagedAdapter) qualityCutoffNotMet(ctx context.Context, e *history.Entry, seriesCache map[int]*series.Series, profileCache map[int]*qualityprofiles.Profile) bool {
	if a.upgradable == nil || a.seriesSvc == nil || a.qualityProfilesSvc == nil || e == nil || e.SeriesID == 0 || e.Quality == "" {
		return false
	}
	var qm quality.Model
	if err := json.Unmarshal([]byte(e.Quality), &qm); err != nil {
		return false
	}
	ser, cached := seriesCache[e.SeriesID]
	if !cached {
		s, err := a.seriesSvc.GetSeries(ctx, e.SeriesID)
		if err != nil {
			seriesCache[e.SeriesID] = nil
			return false
		}
		ser = s
		seriesCache[e.SeriesID] = ser
	}
	if ser == nil || ser.QualityProfileID == 0 {
		return false
	}
	prof, ok := profileCache[ser.QualityProfileID]
	if !ok {
		p, err := a.qualityProfilesSvc.Get(ctx, ser.QualityProfileID)
		if err == nil {
			prof = p
		}
		profileCache[ser.QualityProfileID] = prof
	}
	if prof == nil {
		return false
	}
	view, err := decisionengine.NewProfileView(prof)
	if err != nil {
		return false
	}
	return a.upgradable.QualityCutoffNotMet(view, qm, nil)
}

// computeHistoryCustomFormats runs the per-row CustomFormat
// calculator + score lookup, with per-request caches for series +
// qualityprofile. Returns empty slice + 0 score on any error or
// missing dep — preserves the pre-J336 stub behavior for failure
// paths.
//
// J336.
func computeHistoryCustomFormats(ctx context.Context, e *history.Entry, a historyPagedAdapter, allFormats []*customformats.Format, seriesCache map[int]*series.Series, profileCache map[int]*qualityprofiles.Profile) ([]apiv3.CustomFormatResource, int) {
	empty := []apiv3.CustomFormatResource{}
	if a.seriesSvc == nil || len(allFormats) == 0 || e == nil || e.SeriesID == 0 {
		return empty, 0
	}
	ser, cached := seriesCache[e.SeriesID]
	if !cached {
		s, err := a.seriesSvc.GetSeries(ctx, e.SeriesID)
		if err != nil {
			// Negative cache the failure so subsequent rows from
			// the same series don't re-hit the service.
			seriesCache[e.SeriesID] = nil
			return empty, 0
		}
		ser = s
		seriesCache[e.SeriesID] = ser
	}
	if ser == nil {
		return empty, 0
	}

	matched := customformats.ParseCustomFormatForHistory(e, ser, allFormats)
	res := make([]apiv3.CustomFormatResource, 0, len(matched))
	presentIDs := make(map[int]bool, len(matched))
	for _, m := range matched {
		res = append(res, apiv3.CustomFormatResource{ID: m.ID, Name: m.Name})
		presentIDs[m.ID] = true
	}
	if len(res) == 0 {
		res = empty
	}

	// Score lookup is optional — gated on having both a profile
	// service AND a non-zero QualityProfileID on the series row.
	score := 0
	if a.qualityProfilesSvc != nil && ser.QualityProfileID > 0 {
		profile, ok := profileCache[ser.QualityProfileID]
		if !ok {
			p, err := a.qualityProfilesSvc.Get(ctx, ser.QualityProfileID)
			if err == nil {
				profile = p
			}
			profileCache[ser.QualityProfileID] = profile
		}
		if profile != nil {
			if items, err := profile.ParsedFormatItems(); err == nil {
				score = qualityprofiles.CalculateCustomFormatScore(items, presentIDs)
			}
		}
	}

	return res, score
}

// episodesWithEpisodeFileLookup composes *episodes.Service with
// the episodeFileServiceWithPopulator wrapper so the v3 Episode
// handlers (List + ByID) can discover the EpisodeFileLookupForEpisode
// AND EpisodeFilePopulator interfaces via the J342 optional-
// extension type-assertion path. Used to populate the J340-pinned
// EpisodeResource.EpisodeFile subresource when the
// `includeEpisodeFile=true` query-param fires.
//
// J342.
type episodesWithEpisodeFileLookup struct {
	*episodes.Service
	epFilePop *episodeFileServiceWithPopulator
	// seriesSvc backs the J372 includeSeries embed — the v3 Episode
	// handlers discover apiv3.EpisodeSeriesLookup via type-assertion and
	// embed the BASE SeriesResource when includeSeries=true.
	seriesSvc historySeriesLookup
}

// GetSeries satisfies apiv3.EpisodeSeriesLookup by forwarding to the
// injected series service. nil-safe — a missing dep degrades the embed
// to absent (the omitempty pointer stays nil).
//
// J372.
func (e *episodesWithEpisodeFileLookup) GetSeries(ctx context.Context, id int) (*series.Series, error) {
	if e.seriesSvc == nil {
		return nil, series.ErrNotFound
	}
	return e.seriesSvc.GetSeries(ctx, id)
}

// GetFile satisfies apiv3.EpisodeFileLookupForEpisode by forwarding
// to the inner episodeFileServiceWithPopulator (which embeds
// *episodefiles.Service).
func (e *episodesWithEpisodeFileLookup) GetFile(ctx context.Context, id int) (*episodefiles.File, error) {
	if e.epFilePop == nil {
		return nil, episodefiles.ErrNotFound
	}
	return e.epFilePop.GetFile(ctx, id)
}

// PopulateEpisodeFile satisfies apiv3.EpisodeFilePopulator by
// forwarding to the inner wrapper's calculator-engine path.
func (e *episodesWithEpisodeFileLookup) PopulateEpisodeFile(ctx context.Context, f *episodefiles.File) ([]apiv3.CustomFormatResource, int) {
	if e.epFilePop == nil {
		return []apiv3.CustomFormatResource{}, 0
	}
	return e.epFilePop.PopulateEpisodeFile(ctx, f)
}

// PopulateQualityCutoffNotMet satisfies apiv3.EpisodeFileQualityCutoffPopulator
// by forwarding to the inner wrapper so the EpisodeResource.episodeFile
// embed (includeEpisodeFile branch) carries the same qualityCutoffNotMet
// flag as the standalone episodefile endpoints. J375.
func (e *episodesWithEpisodeFileLookup) PopulateQualityCutoffNotMet(ctx context.Context, f *episodefiles.File) bool {
	if e.epFilePop == nil {
		return false
	}
	return e.epFilePop.PopulateQualityCutoffNotMet(ctx, f)
}

// episodeFileServiceWithPopulator embeds *episodefiles.Service
// (preserves the full Reader + Writer surface via embedding) and
// adds PopulateEpisodeFile — which the v3 episodefile handlers
// (List + ByID + Editor + Bulk* + Update) discover via
// optional-extension type-assertion to run the CustomFormat
// calculator (internal/customformats.ParseCustomFormatForFile) +
// qualityprofile score lookup per row.
//
// J339 — companion to the J336/J338 history populated-mappers.
// Re-uses the same narrow-interface lookups; fresh per-call caches
// (per-row invocation by the handlers).
type episodeFileServiceWithPopulator struct {
	*episodefiles.Service
	seriesSvc          historySeriesLookup
	qualityProfilesSvc historyQualityProfileLookup
	customFormatsSvc   historyCustomFormatsLookup
	// upgradable backs the J375 qualityCutoffNotMet flag. nil → the
	// flag stays false (graceful degrade).
	upgradable *decisionengine.UpgradableSpecification
}

// PopulateQualityCutoffNotMet satisfies apiv3.EpisodeFileQualityCutoffPopulator.
// Decodes the file's persisted quality, loads the owning series' quality
// profile, and runs the ported UpgradableSpecification.QualityCutoffNotMet
// (newQuality omitted, matching the C# EpisodeFileResourceMapper call).
// Any missing dep / lookup failure degrades to false.
//
// J375.
func (e *episodeFileServiceWithPopulator) PopulateQualityCutoffNotMet(ctx context.Context, f *episodefiles.File) bool {
	if e.upgradable == nil || e.seriesSvc == nil || e.qualityProfilesSvc == nil || f == nil || f.SeriesID == 0 || f.Quality == "" {
		return false
	}
	var qm quality.Model
	if err := json.Unmarshal([]byte(f.Quality), &qm); err != nil {
		return false
	}
	ser, err := e.seriesSvc.GetSeries(ctx, f.SeriesID)
	if err != nil || ser == nil || ser.QualityProfileID == 0 {
		return false
	}
	p, err := e.qualityProfilesSvc.Get(ctx, ser.QualityProfileID)
	if err != nil || p == nil {
		return false
	}
	view, err := decisionengine.NewProfileView(p)
	if err != nil {
		return false
	}
	return e.upgradable.QualityCutoffNotMet(view, qm, nil)
}

// PopulateEpisodeFile satisfies apiv3.EpisodeFilePopulator. Runs
// the calculator + score lookup for a single file. Empty/missing
// deps return empty + 0 (preserves the pre-J339 stub behavior).
func (e *episodeFileServiceWithPopulator) PopulateEpisodeFile(ctx context.Context, f *episodefiles.File) ([]apiv3.CustomFormatResource, int) {
	empty := []apiv3.CustomFormatResource{}
	if e.seriesSvc == nil || e.customFormatsSvc == nil || f == nil || f.SeriesID == 0 {
		return empty, 0
	}
	allFormats, err := e.customFormatsSvc.All(ctx)
	if err != nil || len(allFormats) == 0 {
		return empty, 0
	}
	ser, err := e.seriesSvc.GetSeries(ctx, f.SeriesID)
	if err != nil || ser == nil {
		return empty, 0
	}
	matched := customformats.ParseCustomFormatForFile(f, ser, allFormats)
	res := make([]apiv3.CustomFormatResource, 0, len(matched))
	presentIDs := make(map[int]bool, len(matched))
	for _, m := range matched {
		res = append(res, apiv3.CustomFormatResource{ID: m.ID, Name: m.Name})
		presentIDs[m.ID] = true
	}
	score := 0
	if e.qualityProfilesSvc != nil && ser.QualityProfileID > 0 {
		if p, err := e.qualityProfilesSvc.Get(ctx, ser.QualityProfileID); err == nil && p != nil {
			if items, perr := p.ParsedFormatItems(); perr == nil {
				score = qualityprofiles.CalculateCustomFormatScore(items, presentIDs)
			}
		}
	}
	if len(res) == 0 {
		return empty, score
	}
	return res, score
}

// parsingServiceWithPopulator embeds *parsingservice.Service
// (satisfies apiv3.ParseMapper via the embedded Map method) and adds
// PopulateParseCustomFormats — which the v3 + v5 Parse handlers
// discover via optional-extension type-assertion (J345) to run the
// CustomFormat calculator (internal/customformats
// .ParseCustomFormatForRemoteEpisode) + qualityprofile score lookup
// against the just-mapped Subject.
//
// J345 — companion to the J339 episodeFileServiceWithPopulator.
// Re-uses the same narrow-interface lookups for parity. Each
// PopulateParseCustomFormats call allocates a single allFormats
// fetch + a single profile fetch — the call cadence is one per
// Parse request (not per-row) so a per-request cache is unnecessary.
type parsingServiceWithPopulator struct {
	*parsingservice.Service
	seriesSvc          historySeriesLookup
	qualityProfilesSvc historyQualityProfileLookup
	customFormatsSvc   historyCustomFormatsLookup
	// decisionRejector is the J356 ManualImport GetDecision rejector.
	// nil when the ImportDecisionMaker / AggregationService weren't
	// threaded in — the reprocess handler then skips the episodeIds
	// rejections (emits `[]`).
	decisionRejector apiv3.ManualImportDecisionRejector
}

// ReprocessDecisionRejections satisfies
// [apiv3.ManualImportDecisionRejector] by delegating to the injected
// decisionRejector (the J356 manualImportDecisionAdapter, which carries
// the ImportDecisionMaker + AggregationService). Delegation keeps the
// heavy LocalEpisode-assembly logic out of host_wiring while letting
// the reprocess handler discover the rejector via type-assertion on its
// existing calc dep. Returns an empty slice when no rejector is wired.
func (p *parsingServiceWithPopulator) ReprocessDecisionRejections(ctx context.Context, item *apiv3.ManualImportReprocessItem, parsed *parser.ParsedEpisodeInfo, episodeIDs []int) []apiv3.ManualImportRejectionResource {
	if p.decisionRejector == nil {
		return []apiv3.ManualImportRejectionResource{}
	}
	return p.decisionRejector.ReprocessDecisionRejections(ctx, item, parsed, episodeIDs)
}

// PopulateParseCustomFormats satisfies apiv3.ParsePopulator. Mirrors
// C# `ParseController.cs:53-54` — runs the calculator engine against
// the Subject's parsed-info + series + languages, then computes the
// score via the series' QualityProfile. Empty / missing deps return
// empty + 0 (preserves the pre-J345 deferred-state behavior).
func (p *parsingServiceWithPopulator) PopulateParseCustomFormats(ctx context.Context, subject *decisionengine.Subject) ([]apiv3.CustomFormatResource, int) {
	empty := []apiv3.CustomFormatResource{}
	if subject == nil || subject.ParsedEpisodeInfo == nil || p.customFormatsSvc == nil {
		return empty, 0
	}
	allFormats, err := p.customFormatsSvc.All(ctx)
	if err != nil || len(allFormats) == 0 {
		return empty, 0
	}
	// IndexerFlags from Subject.Release when present (zero for Parse
	// requests; non-zero for downstream RSS / Push paths that may
	// reuse this helper later).
	var indexerFlags parser.IndexerFlags
	if rel, ok := subject.Release.(*parser.ReleaseInfo); ok && rel != nil {
		indexerFlags = rel.IndexerFlags
	} else if rel, ok := subject.Release.(*parser.TorrentInfo); ok && rel != nil {
		indexerFlags = rel.IndexerFlags
	}
	matched := customformats.ParseCustomFormatForRemoteEpisode(
		subject.ParsedEpisodeInfo,
		subject.Series,
		subject.Languages,
		indexerFlags,
		0, // size=0 for the Parse-handler call shape (no real release)
		allFormats,
	)
	res := make([]apiv3.CustomFormatResource, 0, len(matched))
	presentIDs := make(map[int]bool, len(matched))
	for _, m := range matched {
		res = append(res, apiv3.CustomFormatResource{ID: m.ID, Name: m.Name})
		presentIDs[m.ID] = true
	}
	score := 0
	if p.qualityProfilesSvc != nil && subject.Series != nil && subject.Series.QualityProfileID > 0 {
		if prof, err := p.qualityProfilesSvc.Get(ctx, subject.Series.QualityProfileID); err == nil && prof != nil {
			if items, perr := prof.ParsedFormatItems(); perr == nil {
				score = qualityprofiles.CalculateCustomFormatScore(items, presentIDs)
			}
		}
	}
	if len(res) == 0 {
		return empty, score
	}
	return res, score
}

// CalculateForManualImport satisfies apiv3.ManualImportCustomFormatCalculator.
// Mirrors C# `ManualImportController.cs:62-63` for the title-based
// subset: resolves the series by id, fetches the format catalogue,
// runs the J345 customformats.ParseCustomFormatForRemoteEpisode
// calculator against the parsed-path info + the item's resolved
// languages + indexerFlags (size=0 — the bare-path endpoint can't feed
// the size/source specs), then computes the score via the series'
// QualityProfile. Empty / missing deps return empty + 0 (preserves the
// pre-J346 deferred-state behavior).
//
// J346.
func (p *parsingServiceWithPopulator) CalculateForManualImport(ctx context.Context, parsedInfo *parser.ParsedEpisodeInfo, seriesID int, languages []language.Language, indexerFlags int) ([]apiv3.CustomFormatResource, int) {
	empty := []apiv3.CustomFormatResource{}
	if parsedInfo == nil || seriesID == 0 || p.customFormatsSvc == nil || p.seriesSvc == nil {
		return empty, 0
	}
	allFormats, err := p.customFormatsSvc.All(ctx)
	if err != nil || len(allFormats) == 0 {
		return empty, 0
	}
	ser, err := p.seriesSvc.GetSeries(ctx, seriesID)
	if err != nil || ser == nil {
		return empty, 0
	}
	matched := customformats.ParseCustomFormatForRemoteEpisode(
		parsedInfo,
		ser,
		languages,
		parser.IndexerFlags(indexerFlags),
		0,
		allFormats,
	)
	res := make([]apiv3.CustomFormatResource, 0, len(matched))
	presentIDs := make(map[int]bool, len(matched))
	for _, m := range matched {
		res = append(res, apiv3.CustomFormatResource{ID: m.ID, Name: m.Name})
		presentIDs[m.ID] = true
	}
	score := 0
	if p.qualityProfilesSvc != nil && ser.QualityProfileID > 0 {
		if prof, err := p.qualityProfilesSvc.Get(ctx, ser.QualityProfileID); err == nil && prof != nil {
			if items, perr := prof.ParsedFormatItems(); perr == nil {
				score = qualityprofiles.CalculateCustomFormatScore(items, presentIDs)
			}
		}
	}
	if len(res) == 0 {
		return empty, score
	}
	return res, score
}

// historyServiceWithPopulator embeds *history.Service (satisfies
// apiv3.HistoryRangeReader via the embedded Since + GetBySeries
// methods) and adds PopulateHistoryEntry — which the v3 history
// handlers (HistorySinceHandler + HistorySeriesHandler) discover
// via optional-extension type-assertion to run the CustomFormat
// calculator + qualityprofile score lookup per row.
//
// J338 — companion to the J336 historyPagedAdapter populated-row
// mapper. Re-uses the same narrow-interface lookups; the
// computeHistoryCustomFormats helper does the per-row work. Each
// PopulateHistoryEntry call allocates fresh caches — the typical
// since/series payload is a small list (~10-100 entries) so the
// per-call cost is negligible; cross-request caching is an
// optimization deferred until a real workload demonstrates it
// matters.
type historyServiceWithPopulator struct {
	*history.Service
	seriesSvc          historySeriesLookup
	qualityProfilesSvc historyQualityProfileLookup
	customFormatsSvc   historyCustomFormatsLookup
	// episodesSvc backs the J377 includeEpisode embed. nil → no embed.
	episodesSvc historyEpisodeLookup
	// upgradable backs the J378 qualityCutoffNotMet flag. nil → false.
	upgradable *decisionengine.UpgradableSpecification
}

// PopulateHistoryQualityCutoffNotMet satisfies apiv3.HistoryQualityCutoffPopulator.
// Decodes the row's persisted quality, loads the owning series' quality
// profile, and runs the ported UpgradableSpecification.QualityCutoffNotMet
// (newQuality omitted, matching the C# HistoryController.MapToResource
// call). Any missing dep / lookup failure degrades to false. J378.
func (h *historyServiceWithPopulator) PopulateHistoryQualityCutoffNotMet(ctx context.Context, e *history.Entry) bool {
	if h.upgradable == nil || h.seriesSvc == nil || h.qualityProfilesSvc == nil || e == nil || e.SeriesID == 0 || e.Quality == "" {
		return false
	}
	var qm quality.Model
	if err := json.Unmarshal([]byte(e.Quality), &qm); err != nil {
		return false
	}
	ser, err := h.seriesSvc.GetSeries(ctx, e.SeriesID)
	if err != nil || ser == nil || ser.QualityProfileID == 0 {
		return false
	}
	p, err := h.qualityProfilesSvc.Get(ctx, ser.QualityProfileID)
	if err != nil || p == nil {
		return false
	}
	view, err := decisionengine.NewProfileView(p)
	if err != nil {
		return false
	}
	return h.upgradable.QualityCutoffNotMet(view, qm, nil)
}

// HistorySeriesResource satisfies apiv3.HistorySeriesEmbedder — loads the
// series by id and projects the BASE SeriesResource (C# model.Series
// .ToResource()). nil on any miss so the embed key drops. J377.
func (h *historyServiceWithPopulator) HistorySeriesResource(ctx context.Context, seriesID int) *apiv3.SeriesResource {
	if h.seriesSvc == nil || seriesID == 0 {
		return nil
	}
	s, err := h.seriesSvc.GetSeries(ctx, seriesID)
	if err != nil || s == nil {
		return nil
	}
	r := apiv3.ToSeriesResource(*s)
	return &r
}

// HistoryEpisodeResource satisfies apiv3.HistoryEpisodeEmbedder — loads
// the episode by id and projects the BASE EpisodeResource (C#
// model.Episode.ToResource()). nil on any miss. J377.
func (h *historyServiceWithPopulator) HistoryEpisodeResource(ctx context.Context, episodeID int) *apiv3.EpisodeResource {
	if h.episodesSvc == nil || episodeID == 0 {
		return nil
	}
	e, err := h.episodesSvc.GetEpisode(ctx, episodeID)
	if err != nil || e == nil {
		return nil
	}
	r := apiv3.ToEpisodeResource(*e)
	return &r
}

// PopulateHistoryEntry satisfies apiv3.HistoryEntryPopulator. Runs
// the J336 helper with a fresh per-call cache pair + the loaded
// allFormats slice.
func (h *historyServiceWithPopulator) PopulateHistoryEntry(ctx context.Context, e *history.Entry) (cf []apiv3.CustomFormatResource, score int) {
	var allFormats []*customformats.Format
	if h.customFormatsSvc != nil {
		if formats, err := h.customFormatsSvc.All(ctx); err == nil {
			allFormats = formats
		}
	}
	// Wrap into a synthetic historyPagedAdapter so we can re-use
	// computeHistoryCustomFormats verbatim (the helper signature
	// reads only the 3 lookup fields off the adapter struct).
	syntheticAdapter := historyPagedAdapter{
		seriesSvc:          h.seriesSvc,
		qualityProfilesSvc: h.qualityProfilesSvc,
		customFormatsSvc:   h.customFormatsSvc,
	}
	seriesCache := map[int]*series.Series{}
	profileCache := map[int]*qualityprofiles.Profile{}
	return computeHistoryCustomFormats(ctx, e, syntheticAdapter, allFormats, seriesCache, profileCache)
}

// computeBlocklistCustomFormats runs the per-row CustomFormat
// calculator for a blocklist entry — score-less variant of the
// J336 helper since BlocklistResource doesn't carry
// CustomFormatScore (per the J297 wire-through note). Returns an
// empty slice on any error or missing dep (preserves the pre-J337
// stub behavior).
//
// J337.
func computeBlocklistCustomFormats(ctx context.Context, e *blocklist.Entry, a blocklistPagedAdapter, allFormats []*customformats.Format, seriesCache map[int]*series.Series) []apiv3.CustomFormatResource {
	empty := []apiv3.CustomFormatResource{}
	if a.seriesSvc == nil || len(allFormats) == 0 || e == nil || e.SeriesID == 0 {
		return empty
	}
	ser, cached := seriesCache[e.SeriesID]
	if !cached {
		s, err := a.seriesSvc.GetSeries(ctx, e.SeriesID)
		if err != nil {
			seriesCache[e.SeriesID] = nil // negative cache
			return empty
		}
		ser = s
		seriesCache[e.SeriesID] = ser
	}
	if ser == nil {
		return empty
	}

	matched := customformats.ParseCustomFormatForBlocklist(e, ser, allFormats)
	res := make([]apiv3.CustomFormatResource, 0, len(matched))
	for _, m := range matched {
		res = append(res, apiv3.CustomFormatResource{ID: m.ID, Name: m.Name})
	}
	if len(res) == 0 {
		return empty
	}
	return res
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
	// profiles backs the J361 cutoff branch — the below-cutoff
	// quality-id sets are computed from every quality profile. nil →
	// the cutoff branch returns empty (pre-J361 posture).
	profiles *qualityprofiles.Service
	// seriesSvc backs the J383 includeSeries embed. nil → no embed
	// (applyEpisodeSeries's type-assertion still succeeds but the
	// GetSeries forward degrades to a miss → nil embed).
	seriesSvc historySeriesLookup
	// epFiles backs the J384 includeEpisodeFile embed. The wrapper
	// implements apiv3.EpisodeFileLookupForEpisode (GetFile via the
	// embedded service) + the populator interfaces. nil → no embed.
	epFiles *episodeFileServiceWithPopulator
	now     func() time.Time
}

// GetSeries satisfies apiv3.EpisodeSeriesLookup so applyEpisodeSeries can
// embed the BASE SeriesResource on the J383 includeSeries branch. nil-safe.
func (w wantedRepoLister) GetSeries(ctx context.Context, id int) (*series.Series, error) {
	if w.seriesSvc == nil {
		return nil, series.ErrNotFound
	}
	return w.seriesSvc.GetSeries(ctx, id)
}

func (w wantedRepoLister) PagedWanted(ctx context.Context, req apiv3.PagingRequest, scope apiv3.WantedScope, includeSeries, includeEpisodeFile, includeImages bool) ([]apiv3.EpisodeResource, int, error) {
	// MissingController + CutoffController default — `monitored=true`.
	// The recorded corpus exercises only the default, so the boolean
	// toggle stays pinned here until query-string parsing for the
	// alternate branch lands.
	var (
		rows []episodes.Episode
		err  error
	)
	if scope == apiv3.WantedCutoff {
		// J361 — compute the per-profile below-cutoff quality-id sets,
		// then enumerate episodes whose file falls below their series'
		// cutoff. Replaces the pre-J361 empty-result stub.
		below, berr := w.belowCutoffByProfile(ctx)
		if berr != nil {
			return nil, 0, berr
		}
		rows, err = w.episodes.WantedCutoffUnmet(ctx, below, true)
	} else {
		rows, err = w.episodes.WantedMissing(ctx, w.now(), true)
	}
	if err != nil {
		return nil, 0, err
	}
	total := len(rows)
	page := paginateInMemory(rows, req)
	out := make([]apiv3.EpisodeResource, 0, len(page))
	// J383: per-request series-embed cache (the wanted list repeats
	// series ids across episodes of the same show). Only consulted
	// when includeSeries fires.
	seriesEmbedCache := map[int]*apiv3.SeriesResource{}
	for _, e := range page {
		res := apiv3.ToEpisodeResource(e)
		// J383 — conditional BASE Series embed + Images projection,
		// reusing the shared episode helpers (applyEpisodeSeries J372,
		// applyEpisodeImages J371). w satisfies apiv3.EpisodeSeriesLookup
		// via GetSeries. Mirrors C# MapToResource(v, includeSeries, _,
		// includeImages).
		apiv3.ApplyEpisodeSeries(ctx, &res, e.SeriesID, w, includeSeries, seriesEmbedCache)
		// J384 — conditional EpisodeFile subresource. Missing rows have
		// no file (EpisodeFileID==0 → the helper no-ops); Cutoff rows
		// carry a below-cutoff file so the embed appears. w.epFiles
		// satisfies apiv3.EpisodeFileLookupForEpisode + the populator
		// interfaces; nil → no embed.
		if w.epFiles != nil {
			ep := e // pointer capture
			apiv3.ApplyEpisodeFileSubresource(ctx, &res, &ep, w.epFiles, includeEpisodeFile)
		}
		apiv3.ApplyEpisodeImages(&res, e, includeImages)
		out = append(out, res)
	}
	return out, total, nil
}

// belowCutoffByProfile delegates to [computeBelowCutoffByProfile].
func (w wantedRepoLister) belowCutoffByProfile(ctx context.Context) (map[int][]int, error) {
	return computeBelowCutoffByProfile(ctx, w.profiles)
}

// computeBelowCutoffByProfile builds the quality-profile-id →
// below-cutoff-quality-ids map [episodes.Repo.WantedCutoffUnmet]
// consumes, by running [qualityprofiles.QualitiesBelowCutoff] over every
// profile. Mirrors the profile sweep in C#
// `EpisodeCutoffService.EpisodesWhereCutoffUnmet`. nil profiles service →
// empty map. Shared by the wantedRepoLister cutoff branch (J361) + the
// CutoffUnmetEpisodeSearch command lister (J362).
func computeBelowCutoffByProfile(ctx context.Context, profiles *qualityprofiles.Service) (map[int][]int, error) {
	if profiles == nil {
		return nil, nil
	}
	profs, err := profiles.All(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[int][]int, len(profs))
	for i := range profs {
		ids, err := qualityprofiles.QualitiesBelowCutoff(&profs[i])
		if err != nil {
			return nil, err
		}
		if len(ids) > 0 {
			out[profs[i].ID] = ids
		}
	}
	return out, nil
}

// cutoffUnmetSearchLister satisfies [handlers.EpisodeCutoffUnmetLister]
// for the CutoffUnmetEpisodeSearch command (J362). Computes the
// per-profile below-cutoff sets then enumerates via
// [episodes.Repo.WantedCutoffUnmet] — the same surface the wanted/cutoff
// endpoint uses (J361).
type cutoffUnmetSearchLister struct {
	episodes *episodes.Repo
	profiles *qualityprofiles.Service
}

func (l cutoffUnmetSearchLister) CutoffUnmetEpisodes(ctx context.Context, monitored bool) ([]episodes.Episode, error) {
	below, err := computeBelowCutoffByProfile(ctx, l.profiles)
	if err != nil {
		return nil, err
	}
	return l.episodes.WantedCutoffUnmet(ctx, below, monitored)
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

// ---------- HostConfigWriter setters (slice 5.B13d) ----------

func (h hostConfigGetter) SetBindAddress(ctx context.Context, v string) error {
	return h.xmlCfg.SetBindAddress(ctx, v)
}
func (h hostConfigGetter) SetPort(ctx context.Context, v int) error {
	return h.xmlCfg.SetPort(ctx, v)
}
func (h hostConfigGetter) SetSslPort(ctx context.Context, v int) error {
	return h.xmlCfg.SetSslPort(ctx, v)
}
func (h hostConfigGetter) SetEnableSsl(ctx context.Context, v bool) error {
	return h.xmlCfg.SetEnableSsl(ctx, v)
}
func (h hostConfigGetter) SetLaunchBrowser(ctx context.Context, v bool) error {
	return h.xmlCfg.SetLaunchBrowser(ctx, v)
}
func (h hostConfigGetter) SetAuthenticationMethod(ctx context.Context, v string) error {
	return h.xmlCfg.SetAuthenticationMethod(ctx, v)
}
func (h hostConfigGetter) SetAuthenticationRequired(ctx context.Context, v string) error {
	return h.xmlCfg.SetAuthenticationRequired(ctx, v)
}
func (h hostConfigGetter) SetAnalyticsEnabled(ctx context.Context, v bool) error {
	return h.xmlCfg.SetAnalyticsEnabled(ctx, v)
}
func (h hostConfigGetter) SetLogLevel(ctx context.Context, v string) error {
	return h.xmlCfg.SetLogLevel(ctx, v)
}
func (h hostConfigGetter) SetLogSizeLimit(ctx context.Context, v int) error {
	return h.xmlCfg.SetLogSizeLimit(ctx, v)
}
func (h hostConfigGetter) SetConsoleLogLevel(ctx context.Context, v string) error {
	return h.xmlCfg.SetConsoleLogLevel(ctx, v)
}
func (h hostConfigGetter) SetBranch(ctx context.Context, v string) error {
	return h.xmlCfg.SetBranch(ctx, v)
}
func (h hostConfigGetter) SetSslCertPath(ctx context.Context, v string) error {
	return h.xmlCfg.SetSslCertPath(ctx, v)
}
func (h hostConfigGetter) SetSslKeyPath(ctx context.Context, v string) error {
	return h.xmlCfg.SetSslKeyPath(ctx, v)
}
func (h hostConfigGetter) SetSslCertPassword(ctx context.Context, v string) error {
	return h.xmlCfg.SetSslCertPassword(ctx, v)
}
func (h hostConfigGetter) SetURLBase(ctx context.Context, v string) error {
	return h.xmlCfg.SetURLBase(ctx, v)
}
func (h hostConfigGetter) SetInstanceName(ctx context.Context, v string) error {
	return h.xmlCfg.SetInstanceName(ctx, v)
}
func (h hostConfigGetter) SetTrustCgnatIpAddresses(ctx context.Context, v bool) error {
	return h.xmlCfg.SetTrustCgnatIpAddresses(ctx, v)
}
func (h hostConfigGetter) SetUpdateAutomatically(ctx context.Context, v bool) error {
	return h.xmlCfg.SetUpdateAutomatically(ctx, v)
}
func (h hostConfigGetter) SetUpdateMechanism(ctx context.Context, v string) error {
	return h.xmlCfg.SetUpdateMechanism(ctx, v)
}
func (h hostConfigGetter) SetUpdateScriptPath(ctx context.Context, v string) error {
	return h.xmlCfg.SetUpdateScriptPath(ctx, v)
}
func (h hostConfigGetter) SetApplicationUrl(ctx context.Context, v string) error {
	return h.cfg.SetApplicationUrl(ctx, v)
}
func (h hostConfigGetter) SetBackupFolder(ctx context.Context, v string) error {
	return h.cfg.SetBackupFolder(ctx, v)
}
func (h hostConfigGetter) SetBackupInterval(ctx context.Context, v int) error {
	return h.cfg.SetBackupInterval(ctx, v)
}
func (h hostConfigGetter) SetBackupRetention(ctx context.Context, v int) error {
	return h.cfg.SetBackupRetention(ctx, v)
}
func (h hostConfigGetter) SetCertificateValidation(ctx context.Context, v string) error {
	return h.cfg.SetCertificateValidation(ctx, v)
}
func (h hostConfigGetter) SetProxyEnabled(ctx context.Context, v bool) error {
	return h.cfg.SetProxyEnabled(ctx, v)
}
func (h hostConfigGetter) SetProxyType(ctx context.Context, v string) error {
	return h.cfg.SetProxyType(ctx, v)
}
func (h hostConfigGetter) SetProxyHostname(ctx context.Context, v string) error {
	return h.cfg.SetProxyHostname(ctx, v)
}
func (h hostConfigGetter) SetProxyPort(ctx context.Context, v int) error {
	return h.cfg.SetProxyPort(ctx, v)
}
func (h hostConfigGetter) SetProxyUsername(ctx context.Context, v string) error {
	return h.cfg.SetProxyUsername(ctx, v)
}
func (h hostConfigGetter) SetProxyPassword(ctx context.Context, v string) error {
	return h.cfg.SetProxyPassword(ctx, v)
}
func (h hostConfigGetter) SetProxyBypassFilter(ctx context.Context, v string) error {
	return h.cfg.SetProxyBypassFilter(ctx, v)
}
func (h hostConfigGetter) SetProxyBypassLocalAddresses(ctx context.Context, v bool) error {
	return h.cfg.SetProxyBypassLocalAddresses(ctx, v)
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
func v3Routes(configProv *config.Provider, cfg *configfile.Provider, indexerSearchSvc *indexersearch.Service, logger *slog.Logger, qualityDefSvc *qualitydefinitions.Service, qualityProfilesSvc *qualityprofiles.Service, tagsSvc *tags.Service, autoTagsSvc *autotagging.Service, releaseProfilesSvc *releaseprofiles.Service, customFormatsSvc *customformats.Service, delayProfilesSvc *delayprofiles.Service, customFiltersSvc *customfilters.Service, queueSvc *queue.Service, rootFoldersSvc *rootfolders.Service, remotePathMappingsSvc *remotepathmappings.Service, namingConfigSvc *namingconfig.Service, notificationsRepo *notifications.Repo, indexersRepo *indexers.Repo, importListsRepo *importlists.Repo, importListExclusionsRepo *importlistexclusions.Repo, downloadClientsRepo *downloadclients.Repo, episodesSvc *episodes.Service, episodesRepo *episodes.Repo, seriesSvc *series.Service, mediaCoverConvertSvc *mediacover.Service, healthSvc *healthcheck.Service, metadataRepo *metadata.Repo, signalRHub *signalr.Hub, queueStatusPauser apiv3.Pauser, systemStatus apiv3.SystemStatusContext, pendingReleasesRepo *pendingreleases.Repo, blocklistRepo *blocklist.Repo, historyRepo *history.Repo, lifecycleSvc *lifecycle.Service, cmdSvc *commands.Service, cmdRegistry *commands.CommandRegistry, failedDownloadsSvc *faileddownloads.Service, taskSvc *scheduledtasks.Service, backupSvc *backup.Service, episodeFilesSvc *episodefiles.Service, diskProv *disk.Provider, bus *messaging.Bus, processDownloadDecisions *download.ProcessDownloadDecisions, downloadDecider *decisionengine.DownloadDecisionMaker, releaseCache *releasecache.Cache, parsingSvc *parsingservice.Service, manualImportDecisionRejector apiv3.ManualImportDecisionRejector, episodeFileDeleter *episodeFileDeleteAdapter, queueRemover apiv3.QueueWriter, recentUpdates apiv3.UpdateLister, logReader apiv3.LogLister, backupTempPath, logFolderPath string) []host.Route {
	langs := language.All()
	localizationSvc := localization.NewService()
	// J367: the series read handlers (v3 + v5 GET list/by-id) discover
	// the optional apiv3.SeriesImagePopulator on their lister/getter via
	// type-assertion to rewrite image URLs to the local cover form
	// (mirrors C# SeriesController.MapCoversToLocal). Wrap *series.Service
	// with the *mediacover.Service converter; the embedded service keeps
	// it a drop-in for the handlers' read interfaces. Used ONLY at the
	// GET list/by-id sites — the write handlers keep the bare seriesSvc.
	// J369 extends the wrapper to also link episode/file statistics
	// (PopulateSeriesStats) via the episodes repo + episodefiles service.
	seriesImgSvc := seriesReadEnricher{Service: seriesSvc, mc: mediaCoverConvertSvc, eps: episodesRepo, files: episodeFilesSvc}
	uiCfg := uiConfigGetter{cfg: configProv, xmlCfg: cfg}
	hostCfg := hostConfigGetter{cfg: configProv, xmlCfg: cfg}
	// 5.B2: per-resource SignalR emitter for the rootfolder writes.
	// The rootfolder controller in C# extends RestControllerWithSignalR
	// which auto-broadcasts on Create/Delete; the Go port fires the
	// broadcast directly from the handler (no domain-event indirection
	// for this resource — there's no RootFolderUpdatedEvent in C#).
	rootFolderEmitter := signalr.NewV3Emitter[apiv3.RootFolderResource](signalRHub, "rootfolder")
	// 5.C1a: notification provider CUD wires the per-resource SignalR
	// emitter for "notification" + the Registry for implementation
	// validation. The registry is shared singleton from
	// internal/notifications/registry.go's Default() — concrete
	// providers register themselves via init() once their packages
	// are blank-imported by main.go (DEFERRED per Phase-4 subset-3
	// follow-up (a); empty registry today still passes byte-equal
	// tests because no row has Implementation populated).
	notificationEmitter := signalr.NewV3Emitter[apiv3.NotificationResource](signalRHub, "notification")
	notificationRegistry := notifications.Default()
	// 5.C1b: per-row Provider.Test() adapter. Wraps
	// notifications.Registry.Construct + provider.Test under a real
	// notifications.Dependencies. The Dependencies struct is empty
	// today — concrete providers needing HTTPClient/Logger will fail
	// Construct under their typed-cast guards. Wiring real deps is
	// tracked in Phase-4 subset-3 follow-up (a) alongside the
	// blank-import aggregator for webhook+discord init() blocks.
	notificationTester := &registryNotificationTester{
		registry: notificationRegistry,
		deps:     notifications.Dependencies{Logger: logger},
	}
	// 5.C2a: indexer provider CUD emitter for SignalR Created/Updated/Deleted.
	// No registry-checker yet — internal/indexers/ does not export a
	// Registry singleton today (notifications.Registry has no
	// counterpart). The Test/TestAll/Action endpoints in 5.C2b-d will
	// need one; for the basic-CUD slice the validation chain only
	// checks Name/Implementation/ConfigContract non-empty + name
	// uniqueness — no registry membership.
	indexerEmitter := signalr.NewV3Emitter[apiv3.IndexerResource](signalRHub, "indexer")
	// 5.C2b: indexer Test endpoint adapter. Wraps
	// indexers.Registry.Construct + provider.Test under
	// indexers.Dependencies. Zero concrete indexer providers register
	// today — every request to /indexer/test will return 400
	// "Unknown implementation" until the first concrete impl ports
	// (Newznab/Torznab/BroadcastTheNet/etc.). The wire boundary is
	// in place so subsequent slices can land concrete providers
	// without touching this wiring.
	indexerRegistry := indexers.Default()
	indexerTester := &registryIndexerTester{
		registry: indexerRegistry,
		deps:     indexers.Dependencies{Logger: logger},
	}
	// 5.C3a: importlist provider CUD emitter. The importlists.Registry
	// already ships (Sonarr + Plex providers register; per Phase-4
	// subset-4 follow-up (c) more providers will land later). 5.C3b
	// will wire a tester adapter on top of importlists.Factory.Test.
	importListEmitter := signalr.NewV3Emitter[apiv3.ImportListResource](signalRHub, "importlist")
	// 5.C3b: per-row Provider.Test() adapter for importlist family.
	// Unlike indexers (5.C2b), importlists ships a populated registry
	// today (Sonarr + Plex providers from Phase 4 subset 4).
	importListRegistry := importlists.Default()
	importListTester := &registryImportListTester{
		registry: importListRegistry,
		deps:     importlists.Dependencies{Logger: logger},
	}
	// 5.C4a: downloadclient provider CUD emitter. The
	// downloadclients.Registry already ships Sabnzbd + Transmission
	// concrete impls (Phase 4 baseline + subset 1); 5.C4b will wire a
	// tester adapter on top.
	downloadClientEmitter := signalr.NewV3Emitter[apiv3.DownloadClientResource](signalRHub, "downloadclient")
	// 5.C4b: per-row Provider.Test() adapter. Production Test works
	// today for Sabnzbd + Transmission; other impls 400 with "Unknown
	// implementation" until 5.G slices port them.
	downloadClientRegistry := downloadclients.Default()
	downloadClientTester := &registryDownloadClientTester{
		registry: downloadClientRegistry,
		deps:     downloadclients.Dependencies{Logger: logger},
	}
	// 5.C5a: metadata provider CUD emitter. The metadata package ships
	// 4 concrete writers (xbmc/plex/roksbox/wdtv); 5.C5b will wire a
	// tester adapter against metadata.Registry.Construct.
	metadataEmitter := signalr.NewV3Emitter[apiv3.MetadataResource](signalRHub, "metadata")
	// 5.C5b: per-row Provider.Test() adapter. Production Test works
	// today for all 4 concrete writers (Xbmc/Plex/Roksbox/Wdtv) — the
	// only family where every schema-listed provider has a real impl.
	metadataRegistry := metadata.Default()
	metadataTester := &registryMetadataTester{
		registry: metadataRegistry,
		deps:     metadata.Dependencies{Logger: logger},
	}
	// 5.D1a: series provider CUD emitter. *series.Service satisfies
	// apiv3.SeriesWriter via the slice 5.D1a Insert/Update/Delete
	// thin-pass-through methods (file-move cascade + deleteFiles
	// cascade + ImportListExclusion add DEFERRED — see series.Service
	// doc-comments for the side-effects not replicated).
	seriesEmitter := signalr.NewV3Emitter[apiv3.SeriesResource](signalRHub, "series")
	// 5.D5 wires the DB-row cascade through the API layer. The
	// adapter bundles series + episodes + episodefiles services so a
	// single DeleteCascade call removes rows from all 3 tables in
	// the correct FK-parent-last order (episodefiles → episodes →
	// series). On-disk physical-file delete via DiskProvider stays
	// DEFERRED. seriesWriter embeds *series.Service to inherit
	// Insert/Update/Delete + GetSeries/GetAllSeries; the type-assert
	// in apiv3.SeriesDeleteHandler picks up the DeleteCascade method.
	// 5.D8: pull the *disk.Provider in scope here for the on-disk
	// folder-cleanup step in seriesCascadeAdapter.DeleteCascade. The
	// adapter only needs DeleteFolder so we wire via the narrow
	// seriesFolderCleaner interface — production passes diskProv.
	// 5.D10: importlistexclusions.Service for ?addImportListExclusion=true
	// + body.AddImportListExclusion=true on the editor delete.
	exclusionsSvc := importlistexclusions.NewService(importListExclusionsRepo)
	seriesWriter := &seriesCascadeAdapter{
		Service:      seriesSvc,
		episodes:     episodesSvc,
		episodefiles: episodeFilesSvc,
		disk:         diskProv,
		exclusions:   exclusionsSvc,
		logger:       logger,
	}
	// J214 — episodemonitored.Service satisfies
	// apiv3.EpisodeMonitorCascader for the SeasonPassHandler. The
	// SeasonPass handler hands per-series MonitoringOptions cascades
	// off to this service which handles BOTH the per-episode toggle
	// AND the series persist (matches C# SeasonPassController.cs:53).
	// Constructing in v3Routes scope rather than threading through
	// buildHostServer's already-37-param signature — episodemonitored
	// has no internal state, so a fresh instance per host construct
	// is fine.
	episodeMonitoredSvc, err := episodemonitored.NewService(episodesSvc, seriesSvc, nil, logger)
	if err != nil {
		// Fail-soft: log and continue with nil cascader (the J214
		// fallback path uses svc.Update directly without the cascade).
		// A constructor failure here would mean episodesSvc or seriesSvc
		// is nil — a wider regression that the rest of host_wiring would
		// have caught already.
		logger.Warn("v3 routes: episodemonitored.NewService failed; seasonpass cascade disabled", "err", err)
	}
	// 5.E3: blocklistSvc construction is repeated here (and in main.go
	// at line 1979 for the IHandle subscribers) since both wires need
	// a Service. Service is stateless, so two instances are equivalent.
	blocklistSvc := blocklist.NewService(blocklistRepo, bus)
	// 5.I21: history.Service construction repeated here for the
	// `/api/v3/history/since` + `/api/v3/history/series` read paths.
	// Stateless service; the main.go-tier instance handles IHandle
	// subscribers, this instance feeds the HTTP read handlers.
	historySvc := history.NewService(historyRepo)
	// J338: wrap historySvc with a PopulateHistoryEntry implementer
	// so the HistorySinceHandler + HistorySeriesHandler can run the
	// CustomFormat calculator + score lookup per row via the
	// optional-extension type-assertion path inside the v3 handlers.
	// The wrapper embeds *history.Service (preserving all
	// HistoryRangeReader methods) + adds the populator method using
	// the same J336 deps as the paged adapter. Caches are per-request
	// (each PopulateHistoryEntry call is fresh — the request handler
	// invokes it per row in a tight loop, so the per-row caches are
	// fine here; for cross-request caching the wrapper could memoize
	// allFormats, but the recorded-corpus history.Since payloads are
	// always small lists where the per-call cost is negligible).
	// J375/J378: one upgradable-spec instance backs the qualityCutoffNotMet
	// flag on both the episodefile and history populators. configProv
	// satisfies decisionengine.ConfigReader (via DownloadPropersAndRepacks)
	// — same construction as main.go. A construction error leaves the
	// flag at false (graceful degrade). The spec is stateless (config +
	// log) so sharing one instance is safe.
	upgradableSpecForReads, _ := decisionengine.NewUpgradableSpecification(configProv, logger)
	historySvcPopulated := &historyServiceWithPopulator{
		Service:            historySvc,
		seriesSvc:          seriesSvc,
		qualityProfilesSvc: qualityProfilesSvc,
		customFormatsSvc:   customFormatsSvc,
		episodesSvc:        episodesSvc,
		upgradable:         upgradableSpecForReads,
	}
	// J339: episodefile populator wrapper — same pattern as the
	// history populator. Used by every v3+v5 episodefile handler
	// site so all of them route through the calculator engine for
	// populated rows. Test fixtures don't implement the populator
	// extension so the empty fallback fires verbatim.
	episodeFilesSvcPopulated := &episodeFileServiceWithPopulator{
		Service:            episodeFilesSvc,
		seriesSvc:          seriesSvc,
		qualityProfilesSvc: qualityProfilesSvc,
		customFormatsSvc:   customFormatsSvc,
		upgradable:         upgradableSpecForReads,
	}
	// J342: composite wrapper for the v3 Episode handlers — embeds
	// *episodes.Service (preserves the full GetEpisode +
	// GetEpisodesBySeries surface) + composes the J339 populator
	// wrapper so the J340-pinned EpisodeResource.EpisodeFile
	// subresource lights up when `includeEpisodeFile=true` query
	// param fires.
	episodesSvcWithEpisodeFile := &episodesWithEpisodeFileLookup{
		Service:   episodesSvc,
		epFilePop: episodeFilesSvcPopulated,
		seriesSvc: seriesSvc,
	}
	// J345: parsing-service populator wrapper — embeds
	// *parsingservice.Service (satisfies apiv3.ParseMapper via the
	// embedded Map method) and adds PopulateParseCustomFormats. Used
	// by the v3 + v5 Parse handlers via optional-extension type-
	// assertion to route the Parse response through the CustomFormat
	// calculator (against the Subject's parsedInfo + series +
	// languages) + qualityprofile score lookup.
	parsingSvcWithPopulator := &parsingServiceWithPopulator{
		Service:            parsingSvc,
		seriesSvc:          seriesSvc,
		qualityProfilesSvc: qualityProfilesSvc,
		customFormatsSvc:   customFormatsSvc,
		decisionRejector:   manualImportDecisionRejector,
	}
	// J387a: the ManualImport GET disk-walk lister. The same
	// manualImportDecisionAdapter that satisfies ManualImportDecisionRejector
	// also implements ManualImportLister (it already carries the
	// ImportDecisionMaker + disk + parsing deps); discover it via the
	// optional-extension type-assertion so the GET handler lights up
	// without a new buildHostServer param. nil-tolerant: a rejector that
	// doesn't implement the lister leaves the handler on its `[]` stub.
	var manualImportLister apiv3.ManualImportLister
	if l, ok := manualImportDecisionRejector.(apiv3.ManualImportLister); ok {
		manualImportLister = l
	}
	// 5.I7: log-file adapter sources LogFolder = <AppData>/logs and
	// URLBase from configfile.Provider. The folder string + URLBase
	// are resolved at boot in buildHostServer and snapshot into the
	// adapter so the handler doesn't re-query per-request.
	urlBase, _ := cfg.URLBase(context.Background())
	logFileSvc := logFileLister{
		folder:       logFolderPath,
		updateFolder: filepath.Join(filepath.Dir(logFolderPath), "UpdateLogs"),
		urlBase:      urlBase,
	}
	// 5.I10: mediacover folder is <AppData>/MediaCover by convention.
	// The log folder param was the only AppData-derived path threaded
	// through buildHostServer — extract the parent to keep main.go's
	// param list short.
	mediaCoverSvc := mediaCoverLister{folder: filepath.Join(filepath.Dir(logFolderPath), "MediaCover")}
	// 5.I3: disk space adapter sources from the rootfolders.Service —
	// each configured root folder is treated as one important path.
	// The optional-mounts path (C# `GetFixedDisksRootPaths` filtered
	// against `_regexSpecialDrive`) is DEFERRED until a cross-platform
	// GetMounts adapter lands; the rootfolder-only surface still
	// matches C# wire shape on empty DB (`[]`) and gives populated
	// DBs the right entries for the UI's "free space" widget.
	diskSpaceLister := diskSpaceFromRootFolders{svc: rootFoldersSvc}
	return []host.Route{
		{Method: "GET", Path: "/api/v3/autotagging", Handler: apiv3.AutoTaggingHandler(autoTagsSvc)},
		{Method: "GET", Path: "/api/v3/autotagging/{id}", Handler: apiv3.AutoTaggingByIDHandler(autoTagsSvc)},
		{Method: "GET", Path: "/api/v3/autotagging/schema", Handler: apiv3.AutoTaggingSchemaHandler()},
		{Method: "POST", Path: "/api/v3/autotagging", Handler: apiv3.AutoTaggingCreateHandler(autoTagsSvc)},
		{Method: "PUT", Path: "/api/v3/autotagging/{id}", Handler: apiv3.AutoTaggingUpdateHandler(autoTagsSvc)},
		{Method: "DELETE", Path: "/api/v3/autotagging/{id}", Handler: apiv3.AutoTaggingDeleteHandler(autoTagsSvc)},
		// v5 AutoTagging — same wire shape as v3, tightened status
		// code on DELETE /{id} (204). Audit row C.1.e — closed 5.J225.
		{Method: "GET", Path: "/api/v5/autotagging", Handler: autotaggingv5.ListHandler(autoTagsSvc)},
		{Method: "GET", Path: "/api/v5/autotagging/{id}", Handler: autotaggingv5.ByIDHandler(autoTagsSvc)},
		{Method: "GET", Path: "/api/v5/autotagging/schema", Handler: autotaggingv5.SchemaHandler()},
		{Method: "POST", Path: "/api/v5/autotagging", Handler: autotaggingv5.CreateHandler(autoTagsSvc)},
		{Method: "PUT", Path: "/api/v5/autotagging/{id}", Handler: autotaggingv5.UpdateHandler(autoTagsSvc)},
		{Method: "DELETE", Path: "/api/v5/autotagging/{id}", Handler: autotaggingv5.DeleteHandler(autoTagsSvc)},
		{Method: "GET", Path: "/api/v3/blocklist", Handler: apiv3.BlocklistHandler(blocklistPagedAdapter{repo: blocklistRepo, seriesSvc: seriesSvc, customFormatsSvc: customFormatsSvc})},
		{Method: "DELETE", Path: "/api/v3/blocklist/{id}", Handler: apiv3.BlocklistDeleteHandler(blocklistSvc)},
		{Method: "DELETE", Path: "/api/v3/blocklist/bulk", Handler: apiv3.BlocklistBulkDeleteHandler(blocklistSvc)},
		{Method: "GET", Path: "/api/v3/calendar", Handler: apiv3.CalendarHandler(episodesSvcWithEpisodeFile)},
		{Method: "GET", Path: "/api/v3/calendar/{id}", Handler: apiv3.EpisodeByIDHandler(episodesSvcWithEpisodeFile)},
		{Method: "GET", Path: "/feed/v3/calendar/Sonarr.ics", Handler: apiv3.CalendarFeedHandler(episodesSvc, seriesSvc)},
		{Method: "GET", Path: "/api/v3/command", Handler: apiv3.CommandListHandler(cmdSvc)},
		{Method: "GET", Path: "/api/v3/command/{id}", Handler: apiv3.CommandByIDHandler(cmdSvc)},
		{Method: "POST", Path: "/api/v3/command", Handler: apiv3.CommandCreateHandler(cmdSvc, cmdRegistry)},
		{Method: "DELETE", Path: "/api/v3/command/{id}", Handler: apiv3.CommandDeleteHandler(cmdSvc)},
		{Method: "GET", Path: "/api/v3/config/downloadclient", Handler: apiv3.DownloadClientConfigHandler(configProv)},
		{Method: "GET", Path: "/api/v3/config/downloadclient/{id}", Handler: apiv3.DownloadClientConfigHandler(configProv)},
		{Method: "PUT", Path: "/api/v3/config/downloadclient/{id}", Handler: apiv3.DownloadClientConfigUpdateHandler(configProv)},
		{Method: "GET", Path: "/api/v3/config/host", Handler: apiv3.HostConfigHandler(hostCfg)},
		{Method: "GET", Path: "/api/v3/config/host/{id}", Handler: apiv3.HostConfigHandler(hostCfg)},
		{Method: "PUT", Path: "/api/v3/config/host/{id}", Handler: apiv3.HostConfigUpdateHandler(hostCfg)},
		{Method: "GET", Path: "/api/v3/config/importlist", Handler: apiv3.ImportListConfigHandler(configProv)},
		{Method: "GET", Path: "/api/v3/config/importlist/{id}", Handler: apiv3.ImportListConfigHandler(configProv)},
		{Method: "PUT", Path: "/api/v3/config/importlist/{id}", Handler: apiv3.ImportListConfigUpdateHandler(configProv)},
		// v5 ImportList Settings — same wire shape + status codes as
		// v3's config/importlist; path-alias-only slice. Audit row
		// C.1.d — closed 5.J224.
		{Method: "GET", Path: "/api/v5/settings/importlist", Handler: importlistsettingsv5.GetHandler(configProv)},
		{Method: "GET", Path: "/api/v5/settings/importlist/{id}", Handler: importlistsettingsv5.GetHandler(configProv)},
		{Method: "PUT", Path: "/api/v5/settings/importlist/{id}", Handler: importlistsettingsv5.UpdateHandler(configProv)},
		// J279 — v5 UpdateSettings endpoint family (3 endpoints; 51
		// LOC C# across UpdateSettingsController + UpdateSettingsResource
		// + shared SettingsController<T> base 55 LOC). v5-NEW family
		// (v3 has no UpdateConfigController). Resource has 4 fields:
		// branch, updateAutomatically, updateMechanism (camelCase
		// enum), updateScriptPath. Status: GET → 200, PUT → 202
		// Accepted (matches SettingsController.SaveSettings
		// TypedAccepted). Validation: UpdateScriptPath must be an
		// absolute path when UpdateMechanism == script. Persists via
		// configfile.Provider typed setters (SetBranch /
		// SetUpdateAutomatically / SetUpdateMechanism /
		// SetUpdateScriptPath).
		{Method: "GET", Path: "/api/v5/settings/update", Handler: updatesettingsv5.GetHandler(cfg)},
		{Method: "GET", Path: "/api/v5/settings/update/{id}", Handler: updatesettingsv5.GetHandler(cfg)},
		{Method: "PUT", Path: "/api/v5/settings/update/{id}", Handler: updatesettingsv5.UpdateHandler(cfg)},
		// J280 — v5 IndexerSettings endpoint family (3 endpoints; 57
		// LOC C# across IndexerSettingsController + IndexerSettingsResource
		// + shared SettingsController<T> base). Pure path-alias slice
		// (J224 importlistsettings pattern). Resource shape byte-identical
		// to v3 IndexerConfigResource (5 fields: id/minimumAge/retention/
		// maximumSize/rssSyncInterval). Status: GET → 200, PUT → 202
		// (both match v3). Type aliases + variable aliases — re-export
		// the v3 handlers under the v5 route prefix.
		{Method: "GET", Path: "/api/v5/settings/indexer", Handler: indexersettingsv5.GetHandler(configProv)},
		{Method: "GET", Path: "/api/v5/settings/indexer/{id}", Handler: indexersettingsv5.GetHandler(configProv)},
		{Method: "PUT", Path: "/api/v5/settings/indexer/{id}", Handler: indexersettingsv5.UpdateHandler(configProv)},
		// J281 — v5 UiSettings endpoint family (3 endpoints; 75 LOC
		// C# across UiSettingsController + UiSettingsResource + shared
		// SettingsController<T> base). Pure path-alias slice (J280
		// pattern). Resource shape byte-identical to v3
		// UIConfigResource (11 fields). Status: GET → 200, PUT → 202.
		// SharedValidator (UiLanguage ≥ 1 + must exist in Language.All)
		// preserved verbatim via path-alias. Re-uses uiCfg adapter
		// declared at host_wiring.go:1030 (composite *config.Provider +
		// *configfile.Provider satisfying UIConfigWriter for theme
		// XML + 9 DB-backed fields).
		{Method: "GET", Path: "/api/v5/settings/ui", Handler: uisettingsv5.GetHandler(uiCfg)},
		{Method: "GET", Path: "/api/v5/settings/ui/{id}", Handler: uisettingsv5.GetHandler(uiCfg)},
		{Method: "PUT", Path: "/api/v5/settings/ui/{id}", Handler: uisettingsv5.UpdateHandler(uiCfg)},
		// J282 — v5 MediaManagementSettings endpoint family (3 endpoints;
		// 142 LOC C# across MediaManagementSettingsController +
		// MediaManagementSettingsResource). Pure path-alias slice (J280/J281
		// pattern). Resource shape byte-identical to v3
		// MediaManagementConfigResource (24 fields). Status: GET → 200,
		// PUT → 202. Validation chain (subset — RecycleBinCleanupDays ≥ 0,
		// MinimumFreeSpaceWhenImporting ≥ 100, absolute-path checks)
		// preserved verbatim. Deep filesystem validators (folderChmod /
		// folderWritable / pathExists / etc) DEFERRED on both surfaces
		// pending disk-validator adapter shims.
		{Method: "GET", Path: "/api/v5/settings/mediamanagement", Handler: mediamanagementsettingsv5.GetHandler(configProv)},
		{Method: "GET", Path: "/api/v5/settings/mediamanagement/{id}", Handler: mediamanagementsettingsv5.GetHandler(configProv)},
		{Method: "PUT", Path: "/api/v5/settings/mediamanagement/{id}", Handler: mediamanagementsettingsv5.UpdateHandler(configProv)},
		// J283 — v5 GeneralSettings endpoint family (3 endpoints; 207
		// LOC C# across GeneralSettingsController + GeneralSettingsResource).
		// ONE wire-shape divergence: v5 omits the v3-only
		// `trustCgnatIpAddresses` field (newer than the v5 fork). Other
		// 37 fields byte-identical. v5 handlers wrap v3's HostConfig
		// handlers with a field-strip middleware that removes the key
		// from the JSON response body; v3's DisallowUnknownFields
		// decoder accepts v5-shaped POST bodies (missing field defaults
		// to false). User-upsert SaveSettings override + richer
		// validation chain (FileExistsValidator, IsValidCertificate,
		// IUserService.Upsert) DEFERRED on the v5 surface — same gap
		// as v3 today, pending the user-account service port.
		{Method: "GET", Path: "/api/v5/settings/general", Handler: generalsettingsv5.GetHandler(hostCfg)},
		{Method: "GET", Path: "/api/v5/settings/general/{id}", Handler: generalsettingsv5.GetHandler(hostCfg)},
		{Method: "PUT", Path: "/api/v5/settings/general/{id}", Handler: generalsettingsv5.UpdateHandler(hostCfg)},
		// J284 — v5 NamingSettings endpoint family (4 endpoints; 161
		// LOC C# across NamingSettingsController + NamingSettingsResource).
		// Pure path-alias slice (J224/J280/J281/J282 pattern). Resource
		// byte-identical to v3 NamingConfigResource (12 fields).
		// ExampleResource byte-identical to v3 NamingExampleResource (8
		// fields). Status: GETs → 200, PUT → 202. Validation chain
		// (MultiEpisodeStyle ∈ [0,5], ColonReplacementFormat ∈ [0,4],
		// Custom requires non-empty CustomColonReplacementFormat) preserved
		// verbatim. Per-format token validators (ValidEpisodeFormat /
		// ValidDailyEpisodeFormat / etc) + IFilenameSampleService DEFERRED
		// on both surfaces — same J283/J282 pattern; examples handler
		// returns empty {}.
		{Method: "GET", Path: "/api/v5/settings/naming", Handler: namingsettingsv5.GetHandler(namingConfigSvc)},
		{Method: "GET", Path: "/api/v5/settings/naming/{id}", Handler: namingsettingsv5.GetHandler(namingConfigSvc)},
		{Method: "GET", Path: "/api/v5/settings/naming/examples", Handler: namingsettingsv5.ExamplesHandler(namingConfigSvc)},
		{Method: "PUT", Path: "/api/v5/settings/naming/{id}", Handler: namingsettingsv5.UpdateHandler(namingConfigSvc)},
		// J285 — v5 SeriesImport endpoint family (1 endpoint; 29 LOC
		// C# — single POST /series/import that bulk-adds Series). v5
		// returns 200 OK (Ok<List<SeriesResource>>) where v3 emits 201
		// Created on non-empty success — status-only divergence. Wraps
		// v3 SeriesImportHandler with a new inline `rewriteCreatedToOK`
		// (analogous to v5util.RewriteAcceptedToOK but for 201→200).
		// Empty input → 200 + `[]` (both v3 and v5 already match).
		// Validation failures (400) and 500-class errors pass through
		// unchanged. SeriesResource shape shared with v3 (full Series
		// family port DEFERRED per loop contract — Resource widens for
		// both surfaces simultaneously when the v5 Series family lands).
		{Method: "POST", Path: "/api/v5/series/import", Handler: seriesimportv5.PostHandler(seriesWriter, seriesEmitter)},
		// J303 — v5 Series endpoint family CRUD sub-slice (5 endpoints
		// of 6; 422 LOC C# — SeriesController). Four pure variable-
		// aliases to v3 (GET list / GET /{id} / POST / PUT) — statuses
		// already match (200 / 200 / 201 / 202). DELETE wrapped via
		// v5util.RewriteSuccessTo204 to demote v3's 200+`{}` → v5's
		// 204+empty matching `TypedResults.NoContent()` at
		// SeriesController.cs:248-254. SeriesResource v5-field
		// divergences (MalIds, AniListIds, OriginalCountry, IsExcluded,
		// LanguageProfileId-removal) DEFERRED — empty-DB Go-port emits
		// zero rows so the divergence is unobservable at shadow-traffic
		// level until populated-mapper service ports land. PUT
		// /{id}/season (v5-NEW per-id season-monitor endpoint at
		// SeriesController.cs:223-246) DEFERRED to a follow-up sub-slice
		// — needs single-season monitored-set on series.Service (new
		// code, no v3 equivalent).
		{Method: "GET", Path: "/api/v5/series", Handler: seriesv5.ListHandler(seriesImgSvc)},
		{Method: "GET", Path: "/api/v5/series/{id}", Handler: seriesv5.ByIDHandler(seriesImgSvc)},
		{Method: "POST", Path: "/api/v5/series", Handler: seriesv5.CreateHandler(seriesWriter, seriesEmitter)},
		{Method: "PUT", Path: "/api/v5/series/{id}", Handler: seriesv5.UpdateHandler(seriesWriter, seriesEmitter)},
		{Method: "DELETE", Path: "/api/v5/series/{id}", Handler: seriesv5.DeleteHandler(seriesWriter, seriesEmitter)},
		// J304 — v5 SeriesEditor endpoint family (2 endpoints; 116 LOC
		// C# — SeriesEditorController at [V5ApiController("series/editor")]).
		// Both endpoints are pure status-rewrite wrappers over v3's
		// SeriesEditorUpdateHandler / SeriesEditorDeleteHandler:
		//
		//   PUT    /api/v5/series/editor  202+body → 200+body  via v5util.RewriteAcceptedToOK
		//   DELETE /api/v5/series/editor  200+`{}` → 204+empty via v5util.RewriteSuccessTo204
		//
		// SignalR `series/updated` + `series/deleted` broadcasts still
		// fire per matched row (the wrapper is body+status-only;
		// emitter is wired through the v3 handler verbatim).
		// SeriesEditorResource bulk-body byte-identical to v3.
		{Method: "PUT", Path: "/api/v5/series/editor", Handler: seriesv5.EditorUpdateHandler(seriesWriter, seriesEmitter)},
		{Method: "DELETE", Path: "/api/v5/series/editor", Handler: seriesv5.EditorDeleteHandler(seriesWriter, seriesEmitter)},
		// J307 — v5-NEW per-id season-monitor endpoint at
		// SeriesController.cs:223-246 (the final Series-family residual
		// sub-slice after J303 CRUD + J304 Editor). No v3 equivalent —
		// genuinely new code path with a fresh SeasonResource wire
		// shape. Decodes the persisted Seasons JSON blob via the
		// []map[string]any preservation contract (matches J214
		// updateSeasonsJSON pattern) so non-monitored per-season fields
		// (Statistics snapshot, Images, etc.) round-trip unchanged.
		// SignalR series/updated broadcast fires with the freshly-
		// updated full SeriesResource (matches C#
		// `BroadcastResourceChange(ModelAction.Updated, GetSeriesResource(series, false))`).
		// Response: 200 + {seasonNumber, monitored} ONLY — Statistics +
		// Images deferred per C# default includeImages=false.
		{Method: "PUT", Path: "/api/v5/series/{id}/season", Handler: seriesv5.SeasonMonitorHandler(seriesWriter, seriesEmitter)},
		// J286 — v5 SeriesFolder endpoint family (1 endpoint; 34 LOC
		// C# — SeriesFolderController.GetFolder returning the computed
		// series folder name per the naming config, no disk write).
		// Pure path-alias slice — v3 emits the same 200+`{folder:"..."}`
		// shape via SeriesFolderHandler. Re-uses seriesSvc +
		// namingConfigSvc deps verbatim.
		{Method: "GET", Path: "/api/v5/series/{id}/folder", Handler: seriesfolderv5.GetHandler(seriesSvc, namingConfigSvc)},
		// J287 — v5 RenameEpisode endpoint family (2 endpoints; 48 LOC
		// C# — RenameEpisodeController). GET /rename + GET /rename/bulk.
		// Pure path-alias slice. RenameEpisodeResource byte-identical
		// to v3 (7 fields). Status: GET → 200 + `[]` (DEFERRED stub —
		// the underlying RenameEpisodeFileService.GetRenamePreviews
		// comparison driver isn't ported yet on either surface). 400 on
		// missing/invalid query params. v5 inherits the same deferred
		// `[]` shape; both surfaces widen via shared handler when the
		// comparison driver lands.
		{Method: "GET", Path: "/api/v5/rename", Handler: renameepisodev5.GetHandler()},
		{Method: "GET", Path: "/api/v5/rename/bulk", Handler: renameepisodev5.BulkHandler()},
		// J288 — v5 SeriesLookup endpoint family (1 endpoint; 58 LOC
		// C# — SeriesLookupController.Search GET /series/lookup?term=).
		// Pure path-alias slice — v3 emits the same 200+`[]` stub
		// (search-proxy integration DEFERRED on both surfaces:
		// SearchForNewSeries pipeline wired but cover-URL rewrite +
		// folder-name preview + IsExcluded check still pending). 400
		// on missing/empty term. Re-uses no service dep — zero-arg
		// stub.
		{Method: "GET", Path: "/api/v5/series/lookup", Handler: serieslookupv5.GetHandler()},
		// J289 — v5 QueueAction endpoint family (2 endpoints; 58 LOC
		// C# — QueueActionController.Grab single + bulk). ONE
		// status-only divergence: v5 returns 204 NoContent on success
		// where v3 emits 200+`{}` — wrapped via v5util.RewriteSuccessTo204.
		// Body shape on /grab/bulk: QueueBulkResource{Ids:int[]} —
		// identical to v3 QueueBulkGrabResource. 404 on missing
		// pending release / 400 on malformed body or invalid id /
		// 500 on dispatcher error all pass through unchanged because
		// the wrapper predicate only matches 200. First-failure-aborts-batch
		// semantics on /bulk inherited from v3 verbatim.
		{Method: "POST", Path: "/api/v5/queue/grab/{id}", Handler: queueactionv5.GrabHandler(pendingReleasesRepo, seriesSvc, processDownloadDecisions)},
		{Method: "POST", Path: "/api/v5/queue/grab/bulk", Handler: queueactionv5.GrabBulkHandler(pendingReleasesRepo, seriesSvc, processDownloadDecisions)},
		// J290 — v5 QueueDetails endpoint family (1 endpoint; 77 LOC
		// C# — QueueDetailsController). Pure path-alias slice. v3
		// emits the same 200+`[]` stub at /api/v3/queue/details (the
		// queue + pending-release projection chain is DEFERRED on
		// both surfaces per `internal/api/v3/queue.go:46-57`). v5-only
		// `includeSubresources` query param accepted but ignored at
		// the stub level (same as v3's includeSeries/includeEpisode
		// bools). Re-uses emptyQueueDetailsLister stub adapter.
		{Method: "GET", Path: "/api/v5/queue/details", Handler: queuedetailsv5.GetHandler(emptyQueueDetailsLister{})},
		// J291 — v5 QueueStatus endpoint family (1 endpoint; 80 LOC C#
		// — QueueStatusController only). Pure path-alias slice. v3
		// already exposes /api/v3/queue/status via apiv3.QueueStatusHandler
		// with matching 7-field wire shape (totalCount, count,
		// unknownCount, errors, warnings, unknownErrors, unknownWarnings).
		// Re-uses queueSvc + pendingCountLister + queueStatusPauser
		// deps verbatim — same Pauser bracket pattern.
		{Method: "GET", Path: "/api/v5/queue/status", Handler: queuestatusv5.GetHandler(queueSvc, pendingCountLister{repo: pendingReleasesRepo}, queueStatusPauser)},
		// J306 — v5 QueueController main endpoints (3 endpoints; 410 LOC
		// C# — QueueController; sibling sub-controllers wired at J289 +
		// J290 + J291). GET /api/v5/queue is a pure variable-alias to
		// v3 (PagedQueue stub adapter — populated-row branch is a
		// post-Wave-3 service-port arc). DELETE /api/v5/queue/{id} +
		// DELETE /api/v5/queue/bulk both wrap v3 via
		// v5util.RewriteSuccessTo204 to demote 200+`{}` -> 204+empty
		// matching TypedResults.NoContent() at QueueController.cs:81
		// + :136. Six side-effect query params (message,
		// removeFromClient, blocklist, skipRedownload, changeCategory)
		// PARSED-AND-IGNORED on BOTH surfaces today (v3 5.E2a deferred
		// upstream-client side-effects; v5 inherits via the wrapper).
		// SignalR queue/deleted broadcast fires only on actual remove
		// (silent-skip on missing id matches v3 queue_write.go:109-117).
		{Method: "GET", Path: "/api/v5/queue", Handler: queuev5.ListHandler(queuePagedAdapter{svc: queueSvc, qualityDefs: qualityDefSvc, qualityProfiles: qualityProfilesSvc})},
		{Method: "DELETE", Path: "/api/v5/queue/{id}", Handler: queuev5.DeleteHandler(queueRemover, nil)},
		{Method: "DELETE", Path: "/api/v5/queue/bulk", Handler: queuev5.BulkDeleteHandler(queueRemover, nil)},
		// J292 — v5 Episode endpoint family (4 endpoints; 85 LOC C#).
		// GET /episode + GET /episode/{id} are pure path-aliases. PUT
		// /episode/monitor wraps v3 via v5util.RewriteAcceptedToOK
		// (v5 demotes v3's 202 to 200, body preserved). PUT /episode/{id}
		// is v5-NEW (v3 has no per-id PUT — only the bulk /monitor)
		// — new v5-specific handler that calls SetEpisodeMonitored +
		// re-fetches + emits 200+EpisodeResource. v5 `includeSubresources`
		// query param invisible at stub level (J290 pattern).
		{Method: "GET", Path: "/api/v5/episode", Handler: episodev5.ListHandler(episodesSvc)},
		{Method: "GET", Path: "/api/v5/episode/{id}", Handler: episodev5.ByIDHandler(episodesSvc)},
		{Method: "PUT", Path: "/api/v5/episode/monitor", Handler: episodev5.MonitorHandler(episodesSvc)},
		{Method: "PUT", Path: "/api/v5/episode/{id}", Handler: episodev5.ByIDPutHandler(episodesSvc)},
		// J301 — v5 EpisodeFile endpoint family (6 endpoints; 205 LOC
		// C# — EpisodeFileController). First slice of the Wave 3 LARGE-
		// family arc (post-calculator). Three pure variable-aliases to
		// v3 (GET list / GET /{id} / PUT /{id}) + three v5util status-
		// rewrite wrappers (PUT /bulk demotes 202→200; DELETE /{id} +
		// DELETE /bulk demote 200+`{}` → 204+empty). The v3-only
		// `[HttpPut("editor")]` overload is NOT routed on v5 — C# v5
		// controller has no editor action (operators use PUT /bulk).
		// EpisodeFileResource carries the J298 customFormats[] +
		// customFormatScore widening unchanged via the alias.
		{Method: "GET", Path: "/api/v5/episodefile", Handler: episodefilev5.ListHandler(episodeFilesSvc, seriesSvc)},
		{Method: "GET", Path: "/api/v5/episodefile/{id}", Handler: episodefilev5.ByIDHandler(episodeFilesSvc)},
		{Method: "PUT", Path: "/api/v5/episodefile/{id}", Handler: episodefilev5.UpdateHandler(episodeFilesSvcPopulated)},
		{Method: "PUT", Path: "/api/v5/episodefile/bulk", Handler: episodefilev5.BulkUpdateHandler(episodeFilesSvcPopulated)},
		{Method: "DELETE", Path: "/api/v5/episodefile/{id}", Handler: episodefilev5.DeleteHandler(episodeFileDeleter)},
		{Method: "DELETE", Path: "/api/v5/episodefile/bulk", Handler: episodefilev5.BulkDeleteHandler(episodeFileDeleter)},
		// J293 — v5 ImportListExclusion endpoint family (6 endpoints;
		// 88 LOC C# — ImportListExclusionController). PATH MAP CHANGE
		// from v3: v5 root GET /importlistexclusion IS paged (no
		// non-paged variant); v3 has both /importlistexclusion (full)
		// AND /importlistexclusion/paged. v5 root aliases v3's
		// PagedHandler. TWO status divergences: DELETE /{id} + DELETE
		// /bulk both demote 200+empty → 204 via v5util.RewriteSuccessTo204.
		// Other 4 endpoints path-alias (POST→201, PUT→202, GETs→200).
		// Resource shape byte-identical (3 fields).
		{Method: "GET", Path: "/api/v5/importlistexclusion", Handler: importlistexclusionv5.GetHandler(importListExclusionsRepo)},
		{Method: "GET", Path: "/api/v5/importlistexclusion/{id}", Handler: importlistexclusionv5.ByIDHandler(importListExclusionsRepo)},
		{Method: "POST", Path: "/api/v5/importlistexclusion", Handler: importlistexclusionv5.CreateHandler(importListExclusionsRepo)},
		{Method: "PUT", Path: "/api/v5/importlistexclusion/{id}", Handler: importlistexclusionv5.UpdateHandler(importListExclusionsRepo)},
		{Method: "DELETE", Path: "/api/v5/importlistexclusion/{id}", Handler: importlistexclusionv5.DeleteHandler(importListExclusionsRepo)},
		{Method: "DELETE", Path: "/api/v5/importlistexclusion/bulk", Handler: importlistexclusionv5.BulkDeleteHandler(importListExclusionsRepo)},
		// J294 — v5 ReleasePush endpoint family (2 endpoints; 120 LOC C#
		// — ReleasePushController). POST /release/push is a pure
		// path-alias to v3's ReleasePushHandler (same 200+ReleaseResource
		// shape on success; 400+envelope on validation failure;
		// ReleaseResource projection inherits the J237 deferred chain).
		// GET /release/push/{id} is the inherited RestController endpoint
		// that the C# v5 controller does NOT override — push operations
		// aren't persisted so there's no underlying lookup. Always 404
		// (matching the C# inherited behaviour). New v5-specific stub
		// returns 404 directly. J266/J270/J274/J292 "v3-port-has-no-handler
		// gap → v5 ByID stub" pattern.
		{Method: "POST", Path: "/api/v5/release/push", Handler: releasepushv5.PostHandler(downloadDecider, processDownloadDecisions, releaseCache, indexersRepo, downloadClientsRepo)},
		{Method: "GET", Path: "/api/v5/release/push/{id}", Handler: releasepushv5.ByIDHandler()},
		// J305 — v5 ReleaseController GET sub-slice (1 of 2 main
		// endpoints; 343 LOC C# — ReleaseController). GET /api/v5/release
		// is a pure variable-alias to v3 — same `Ok<List<ReleaseResource>>`
		// shape + same `seriesId? + episodeId? + seasonNumber?` query
		// triple at ReleaseController.cs:190-205. POST /api/v5/release
		// DEFERRED to J306 — body shape diverges from v3 (v5 groups
		// override-info under a nested `override` object + search-info
		// under `searchInfo`; v3 flattens both on the root). New v5-
		// specific handler code per loop sub-slicing guidance.
		{Method: "GET", Path: "/api/v5/release", Handler: releasev5.ListHandler(indexerSearchSvc)},
		// J308 — v5 ReleaseController POST endpoint (the FINAL Wave 3
		// LARGE-family arc residual). v5 body shape diverges from v3:
		// v3 ReleaseDownloadBody flattens override-info on the root +
		// uses shouldOverride bool; v5 ReleaseGrabResource groups under
		// `override` + adds `searchInfo`. The v5 handler is a body-
		// translation adapter — decodes the v5 shape, translates to a
		// v3 ReleaseDownloadBody, runs the v3 handler against a
		// BufferingWriter, then either echoes the v5 body back on 200
		// success (matches C# `return TypedResults.Ok(release)` at
		// ReleaseController.cs:187) or passes the v3 error envelope
		// through verbatim. DisallowUnknownFields on the v5 decoder
		// defends against clients sending v3-flat-shape fields by
		// mistake. The v5 `searchInfo` fallback branch (C# lines
		// 133-159) is DEFERRED — today's cached Decision always
		// carries populated Series + Episodes so the fallback is
		// unreachable. Same cache + dispatcher + override-lookup deps
		// as v3 ReleaseDownloadHandler.
		{Method: "POST", Path: "/api/v5/release", Handler: releasev5.DownloadHandler(releaseCache, processDownloadDecisions, seriesSvc, episodesSvc, parsingSvc, episodesSvc)},
		{Method: "GET", Path: "/api/v3/config/indexer", Handler: apiv3.IndexerConfigHandler(configProv)},
		{Method: "GET", Path: "/api/v3/config/indexer/{id}", Handler: apiv3.IndexerConfigHandler(configProv)},
		{Method: "PUT", Path: "/api/v3/config/indexer/{id}", Handler: apiv3.IndexerConfigUpdateHandler(configProv)},
		{Method: "GET", Path: "/api/v3/config/mediamanagement", Handler: apiv3.MediaManagementConfigHandler(configProv)},
		{Method: "GET", Path: "/api/v3/config/mediamanagement/{id}", Handler: apiv3.MediaManagementConfigHandler(configProv)},
		{Method: "PUT", Path: "/api/v3/config/mediamanagement/{id}", Handler: apiv3.MediaManagementConfigUpdateHandler(configProv)},
		{Method: "GET", Path: "/api/v3/config/naming", Handler: apiv3.NamingConfigHandler(namingConfigSvc)},
		{Method: "GET", Path: "/api/v3/config/naming/{id}", Handler: apiv3.NamingConfigHandler(namingConfigSvc)},
		{Method: "GET", Path: "/api/v3/config/naming/examples", Handler: apiv3.NamingConfigExamplesHandler(namingConfigSvc)},
		{Method: "PUT", Path: "/api/v3/config/naming/{id}", Handler: apiv3.NamingConfigUpdateHandler(namingConfigSvc)},
		{Method: "GET", Path: "/api/v3/config/ui", Handler: apiv3.UIConfigHandler(uiCfg)},
		{Method: "GET", Path: "/api/v3/config/ui/{id}", Handler: apiv3.UIConfigHandler(uiCfg)},
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
		// v5 CustomFormat — same wire shape as v3, tightened status
		// codes on DELETE /{id} (204), PUT /bulk (200), DELETE /bulk
		// (204 empty). Audit row C.1.b — closed 5.J222.
		{Method: "GET", Path: "/api/v5/customformat", Handler: customformatv5.ListHandler(customFormatsSvc)},
		{Method: "GET", Path: "/api/v5/customformat/{id}", Handler: customformatv5.ByIDHandler(customFormatsSvc)},
		{Method: "GET", Path: "/api/v5/customformat/schema", Handler: customformatv5.SchemaHandler()},
		{Method: "POST", Path: "/api/v5/customformat", Handler: customformatv5.CreateHandler(customFormatsSvc)},
		{Method: "PUT", Path: "/api/v5/customformat/{id}", Handler: customformatv5.UpdateHandler(customFormatsSvc)},
		{Method: "PUT", Path: "/api/v5/customformat/bulk", Handler: customformatv5.BulkUpdateHandler(customFormatsSvc)},
		{Method: "DELETE", Path: "/api/v5/customformat/{id}", Handler: customformatv5.DeleteHandler(customFormatsSvc)},
		{Method: "DELETE", Path: "/api/v5/customformat/bulk", Handler: customformatv5.BulkDeleteHandler(customFormatsSvc)},
		{Method: "GET", Path: "/api/v3/delayprofile", Handler: apiv3.DelayProfileHandler(delayProfilesSvc)},
		{Method: "GET", Path: "/api/v3/delayprofile/{id}", Handler: apiv3.DelayProfileByIDHandler(delayProfilesSvc)},
		{Method: "POST", Path: "/api/v3/delayprofile", Handler: apiv3.DelayProfileCreateHandler(delayProfilesSvc)},
		{Method: "PUT", Path: "/api/v3/delayprofile/{id}", Handler: apiv3.DelayProfileUpdateHandler(delayProfilesSvc)},
		{Method: "PUT", Path: "/api/v3/delayprofile/reorder/{id}", Handler: apiv3.DelayProfileReorderHandler(delayProfilesSvc)},
		{Method: "DELETE", Path: "/api/v3/delayprofile/{id}", Handler: apiv3.DelayProfileDeleteHandler(delayProfilesSvc)},
		{Method: "GET", Path: "/api/v3/diskspace", Handler: apiv3.DiskSpaceHandler(diskSpaceLister)},
		// J251 — v5 DiskSpace endpoint family (Wave 3 first slice). Re-uses
		// the v3 handler via diskspacev5.GetHandler (variable alias). Same
		// wire shape; no v5-specific status-code tightening on this family.
		{Method: "GET", Path: "/api/v5/diskspace", Handler: diskspacev5.GetHandler(diskSpaceLister)},
		// J252 — v5 FileSystem endpoint family (3 GET endpoints; all match
		// v3 wire shape; variable-alias re-exports of the v3 handlers).
		{Method: "GET", Path: "/api/v5/filesystem", Handler: filesystemv5.ListHandler()},
		{Method: "GET", Path: "/api/v5/filesystem/type", Handler: filesystemv5.TypeHandler()},
		{Method: "GET", Path: "/api/v5/filesystem/mediafiles", Handler: filesystemv5.MediaFilesHandler()},
		// J254 — v5 Health endpoint family (single GET; v5 adds `reason`
		// field over v3 per the openapi v5 HealthResource schema).
		{Method: "GET", Path: "/api/v5/health", Handler: healthv5.ListHandler(healthSvc)},
		// J255 — v5 SeasonPass endpoint family (single POST; v5 returns
		// 204 NoContent while v3 returns 202 + `{}`; wrapped via the
		// new rewriteAcceptedTo204 middleware variant).
		{Method: "POST", Path: "/api/v5/seasonpass", Handler: seasonpassv5.PostHandler(seriesWriter, seriesEmitter, episodeMonitoredSvc)},
		// J257 — v5 CustomFilters endpoint family (5 endpoints; only
		// DELETE differs vs v3 — v5 returns 204 while v3 returns
		// 200+EMPTY body; demoted via v5util.RewriteSuccessTo204 whose
		// expanded predicate (per J256) matches 200+empty bodies).
		{Method: "GET", Path: "/api/v5/customfilter", Handler: customfilterv5.ListHandler(customFiltersSvc)},
		{Method: "GET", Path: "/api/v5/customfilter/{id}", Handler: customfilterv5.ByIDHandler(customFiltersSvc)},
		{Method: "POST", Path: "/api/v5/customfilter", Handler: customfilterv5.CreateHandler(customFiltersSvc)},
		{Method: "PUT", Path: "/api/v5/customfilter/{id}", Handler: customfilterv5.UpdateHandler(customFiltersSvc)},
		{Method: "DELETE", Path: "/api/v5/customfilter/{id}", Handler: customfilterv5.DeleteHandler(customFiltersSvc)},
		// J258 — v5 Parse endpoint family (single GET; v5 differs from
		// v3 ONLY in the empty-input case — v3 emits literal `null`,
		// v5 emits a ParseResource object with `title` field).
		{Method: "GET", Path: "/api/v5/parse", Handler: parsev5.GetHandler(parsingSvcWithPopulator)},
		// J259 — v5 Update endpoint family (single GET; pure path-alias
		// to v3 handler — same wire shape, same 200 status).
		{Method: "GET", Path: "/api/v5/update", Handler: updatev5.GetHandler(recentUpdates)},
		// J260 — v5 RemotePathMappings endpoint family (5 endpoints; TWO
		// status divergences: PUT 202→200 via v5util.RewriteAcceptedToOK,
		// DELETE 200+empty→204 via v5util.RewriteSuccessTo204).
		{Method: "GET", Path: "/api/v5/remotepathmapping", Handler: remotepathmappingv5.ListHandler(remotePathMappingsSvc)},
		{Method: "GET", Path: "/api/v5/remotepathmapping/{id}", Handler: remotepathmappingv5.ByIDHandler(remotePathMappingsSvc)},
		{Method: "POST", Path: "/api/v5/remotepathmapping", Handler: remotepathmappingv5.CreateHandler(remotePathMappingsSvc)},
		{Method: "PUT", Path: "/api/v5/remotepathmapping/{id}", Handler: remotepathmappingv5.UpdateHandler(remotePathMappingsSvc)},
		{Method: "DELETE", Path: "/api/v5/remotepathmapping/{id}", Handler: remotepathmappingv5.DeleteHandler(remotePathMappingsSvc)},
		// J261 — v5 Blocklist endpoint family (3 endpoints; GET paged
		// is path-alias; both DELETEs demote 200+`{}` → 204 via
		// v5util.RewriteSuccessTo204).
		{Method: "GET", Path: "/api/v5/blocklist", Handler: blocklistv5.GetHandler(blocklistPagedAdapter{repo: blocklistRepo, seriesSvc: seriesSvc, customFormatsSvc: customFormatsSvc})},
		{Method: "DELETE", Path: "/api/v5/blocklist/{id}", Handler: blocklistv5.DeleteHandler(blocklistSvc)},
		{Method: "DELETE", Path: "/api/v5/blocklist/bulk", Handler: blocklistv5.BulkDeleteHandler(blocklistSvc)},
		// J262 — v5 RootFolders endpoint family (4 endpoints; combo:
		// custom v5 Resource with `isEmpty` field PLUS DELETE 200+empty
		// → 204 via v5util.RewriteSuccessTo204).
		{Method: "GET", Path: "/api/v5/rootfolder", Handler: rootfolderv5.ListHandler(rootFoldersSvc)},
		{Method: "GET", Path: "/api/v5/rootfolder/{id}", Handler: rootfolderv5.ByIDHandler(rootFoldersSvc)},
		{Method: "POST", Path: "/api/v5/rootfolder", Handler: rootfolderv5.CreateHandler(rootFoldersSvc)},
		{Method: "DELETE", Path: "/api/v5/rootfolder/{id}", Handler: rootfolderv5.DeleteHandler(rootFoldersSvc)},
		// J263 — v5 QualityDefinition endpoint family. v5 Resource drops
		// the 3 size fields (minSize/maxSize/preferredSize); UpdateMany
		// moves to PUT `/qualitydefinition` (no /update suffix) AND
		// returns 200 (v3 returned 202); no /limits endpoint in v5.
		// All 4 handlers are v5-specific because the Resource shape
		// itself differs (J254/J262 pattern).
		{Method: "GET", Path: "/api/v5/qualitydefinition", Handler: qualitydefinitionv5.ListHandler(qualityDefSvc)},
		{Method: "GET", Path: "/api/v5/qualitydefinition/{id}", Handler: qualitydefinitionv5.ByIDHandler(qualityDefSvc)},
		{Method: "PUT", Path: "/api/v5/qualitydefinition/{id}", Handler: qualitydefinitionv5.UpdateHandler(qualityDefSvc)},
		{Method: "PUT", Path: "/api/v5/qualitydefinition", Handler: qualitydefinitionv5.UpdateManyHandler(qualityDefSvc, qualityDefSvc)},
		// J264 — v5 Wanted endpoint family (4 endpoints; pure path-alias
		// at empty-corpus level — envelope/status/Resource identical to
		// v3, query-param shape divergence is ignored by current stub).
		// EpisodeResource widening (EndTime/GrabDate) deferred per same
		// chain as J258/J259/J261.
		{Method: "GET", Path: "/api/v5/wanted/missing", Handler: wantedv5.ListHandler(wantedRepoLister{episodes: episodesRepo, profiles: qualityProfilesSvc, seriesSvc: seriesSvc, epFiles: episodeFilesSvcPopulated, now: func() time.Time { return time.Now().UTC() }}, wantedv5.ScopeMissing)},
		{Method: "GET", Path: "/api/v5/wanted/missing/{id}", Handler: wantedv5.ByIDHandler(episodesSvc)},
		{Method: "GET", Path: "/api/v5/wanted/cutoff", Handler: wantedv5.ListHandler(wantedRepoLister{episodes: episodesRepo, profiles: qualityProfilesSvc, seriesSvc: seriesSvc, epFiles: episodeFilesSvcPopulated, now: func() time.Time { return time.Now().UTC() }}, wantedv5.ScopeCutoff)},
		{Method: "GET", Path: "/api/v5/wanted/cutoff/{id}", Handler: wantedv5.ByIDHandler(episodesSvc)},
		// J265 — v5 Indexer + IndexerFlag endpoint family (12 endpoints;
		// Resource shape identical to v3 — 9 fields field-for-field. Four
		// status divergences: DELETE/{id} + DELETE/bulk + POST/test all
		// demote 200+`{}` → 204 via v5util.RewriteSuccessTo204; PUT/bulk
		// is the inverse — v3 Go-port emits 202+body, v5 wants 200+body
		// via v5util.RewriteAcceptedToOK. Other 8 endpoints path-alias.
		// v3 Go-port's POST / already emits 201 and PUT/{id} already
		// emits 202 — both happen to match v5 directly).
		{Method: "GET", Path: "/api/v5/indexer", Handler: indexerv5.ListHandler(indexersRepo)},
		{Method: "GET", Path: "/api/v5/indexer/schema", Handler: indexerv5.SchemaHandler()},
		{Method: "GET", Path: "/api/v5/indexer/{id}", Handler: indexerv5.ByIDHandler(indexersRepo)},
		{Method: "POST", Path: "/api/v5/indexer", Handler: indexerv5.CreateHandler(indexersRepo, indexerEmitter)},
		{Method: "POST", Path: "/api/v5/indexer/test", Handler: indexerv5.TestHandler(indexerTester)},
		{Method: "POST", Path: "/api/v5/indexer/testall", Handler: indexerv5.TestAllHandler(indexersRepo, indexerTester)},
		{Method: "POST", Path: "/api/v5/indexer/action/{name}", Handler: indexerv5.RequestActionHandler(indexerTester)},
		{Method: "PUT", Path: "/api/v5/indexer/bulk", Handler: indexerv5.BulkUpdateHandler(indexersRepo, indexerEmitter)},
		{Method: "DELETE", Path: "/api/v5/indexer/bulk", Handler: indexerv5.BulkDeleteHandler(indexersRepo, indexerEmitter)},
		{Method: "PUT", Path: "/api/v5/indexer/{id}", Handler: indexerv5.UpdateHandler(indexersRepo, indexerEmitter)},
		{Method: "DELETE", Path: "/api/v5/indexer/{id}", Handler: indexerv5.DeleteHandler(indexersRepo, indexerEmitter)},
		{Method: "GET", Path: "/api/v5/indexerflag", Handler: indexerv5.FlagsHandler()},
		// J266 — v5 Localization endpoint family (5 endpoints; 143 LOC
		// C# across LanguageController + LocalizationController + 3
		// resources). Resource shapes identical to v3 — pure type
		// aliases. Three list/dict endpoints are path-aliases of v3
		// handlers. Two new v5-specific ByID handlers (v3 Go-port
		// doesn't expose /{id} variants — they're inherited from
		// RestController in C# v5).
		{Method: "GET", Path: "/api/v5/language", Handler: localizationv5.LanguageHandler(langs)},
		{Method: "GET", Path: "/api/v5/language/{id}", Handler: localizationv5.LanguageByIDHandler(langs)},
		{Method: "GET", Path: "/api/v5/localization", Handler: localizationv5.LocalizationHandler(localizationSvc)},
		{Method: "GET", Path: "/api/v5/localization/language", Handler: localizationv5.LocalizationLanguageHandler(configProv.UILanguage)},
		{Method: "GET", Path: "/api/v5/localization/{id}", Handler: localizationv5.LocalizationByIDHandler(localizationSvc)},
		// J267 — v5 Connection endpoint family (9 endpoints; 154 LOC C#).
		// Resource shape identical to v3 NotificationResource — 30
		// fields. Path RENAME: v3 exposed this family as /notification,
		// v5 renames to /connection on the wire (backing service is
		// still NotificationFactory + NotificationDefinition). Two
		// status divergences: DELETE/{id} + POST/test 200+`{}` → 204
		// via v5util.RewriteSuccessTo204. Other 7 endpoints path-alias.
		// NO /bulk endpoints — explicitly [NonAction] in C# v5.
		{Method: "GET", Path: "/api/v5/connection", Handler: connectionv5.ListHandler(notificationsRepo)},
		{Method: "GET", Path: "/api/v5/connection/schema", Handler: connectionv5.SchemaHandler()},
		{Method: "GET", Path: "/api/v5/connection/{id}", Handler: connectionv5.ByIDHandler(notificationsRepo)},
		{Method: "POST", Path: "/api/v5/connection", Handler: connectionv5.CreateHandler(notificationsRepo, notificationRegistry, notificationEmitter)},
		{Method: "POST", Path: "/api/v5/connection/test", Handler: connectionv5.TestHandler(notificationTester)},
		{Method: "POST", Path: "/api/v5/connection/testall", Handler: connectionv5.TestAllHandler(notificationsRepo, notificationTester)},
		{Method: "POST", Path: "/api/v5/connection/action/{name}", Handler: connectionv5.RequestActionHandler(notificationTester)},
		{Method: "PUT", Path: "/api/v5/connection/{id}", Handler: connectionv5.UpdateHandler(notificationsRepo, notificationRegistry, notificationEmitter)},
		{Method: "DELETE", Path: "/api/v5/connection/{id}", Handler: connectionv5.DeleteHandler(notificationsRepo, notificationEmitter)},
		// J268 — v5 Calendar endpoint family (3 endpoints; 184 LOC C#).
		// Pure path-alias slice — same envelope/status/sort as v3 at the
		// empty-corpus level. Sub-resource hydration (includeSeries /
		// includeEpisodeFile / includeImages) is wired via the shared
		// episodesSvcWithEpisodeFile composite (J376, same as v3). v5
		// lowercases the feed URL: /feed/v3/calendar/Sonarr.ics →
		// /feed/v5/calendar/sonarr.ics.
		{Method: "GET", Path: "/api/v5/calendar", Handler: calendarv5.ListHandler(episodesSvcWithEpisodeFile)},
		{Method: "GET", Path: "/api/v5/calendar/{id}", Handler: calendarv5.ByIDHandler(episodesSvc)},
		{Method: "GET", Path: "/feed/v5/calendar/sonarr.ics", Handler: calendarv5.FeedHandler(episodesSvc, seriesSvc)},
		// J269 — v5 Tag + TagDetails endpoint family (7 endpoints; 192
		// LOC C#). Resource shapes identical to v3 (TagResource 2 fields;
		// TagDetailsResource 10 fields). ONE status-only divergence:
		// DELETE/{id} 200+empty → 204+empty via v5util.RewriteSuccessTo204.
		// Other 6 endpoints path-alias. Re-uses tagsSvc + emptyTagDetailLister
		// (production tag-detail aggregator pending — same DEFERRED state
		// as v3 surface per `tag_detail.go:21-25`).
		{Method: "GET", Path: "/api/v5/tag", Handler: tagv5.ListHandler(tagsSvc)},
		{Method: "GET", Path: "/api/v5/tag/{id}", Handler: tagv5.ByIDHandler(tagsSvc)},
		{Method: "POST", Path: "/api/v5/tag", Handler: tagv5.CreateHandler(tagsSvc)},
		{Method: "PUT", Path: "/api/v5/tag/{id}", Handler: tagv5.UpdateHandler(tagsSvc)},
		{Method: "DELETE", Path: "/api/v5/tag/{id}", Handler: tagv5.DeleteHandler(tagsSvc)},
		{Method: "GET", Path: "/api/v5/tag/detail", Handler: tagv5.DetailListHandler(emptyTagDetailLister{})},
		{Method: "GET", Path: "/api/v5/tag/detail/{id}", Handler: tagv5.DetailByIDHandler(emptyTagDetailLister{})},
		// J270 — v5 History endpoint family (6 endpoints; 232 LOC C#).
		// 3 path-aliases (list/since/series) + 2 new v5-specific stub
		// handlers (/season + /episode; v3 has neither, underlying
		// GetBySeason/GetByEpisode service methods are deferred —
		// stubs emit `[]` for empty-corpus parity) + 1 status wrapper
		// (POST /failed/{id} 200+`{}` → 204 via v5util.RewriteSuccessTo204).
		{Method: "GET", Path: "/api/v5/history", Handler: historyv5.ListHandler(historyPagedAdapter{repo: historyRepo, seriesSvc: seriesSvc, qualityProfilesSvc: qualityProfilesSvc, customFormatsSvc: customFormatsSvc, episodesSvc: episodesSvc, upgradable: upgradableSpecForReads})},
		{Method: "GET", Path: "/api/v5/history/since", Handler: historyv5.SinceHandler(historySvcPopulated)},
		{Method: "GET", Path: "/api/v5/history/series", Handler: historyv5.SeriesHandler(historySvcPopulated)},
		{Method: "GET", Path: "/api/v5/history/season", Handler: historyv5.SeasonHandler()},
		{Method: "GET", Path: "/api/v5/history/episode", Handler: historyv5.EpisodeHandler()},
		{Method: "POST", Path: "/api/v5/history/failed/{id}", Handler: historyv5.MarkAsFailedHandler(failedDownloadsSvc)},
		// J271 — v5 ManualImport endpoint family (2 endpoints; 236 LOC C#).
		// Pure path-alias at empty-corpus level. Both endpoints share
		// status (200). v3 Go-port emits `[]` for GET (manual-import
		// service deferred); POST reprocess re-uses v3 logic. v5
		// ReprocessResource adds `relativePath` field — empty-corpus
		// replay never POSTs this field; real v5 frontend will hit
		// v3's DisallowUnknownFields 400 (deferred follow-up).
		{Method: "GET", Path: "/api/v5/manualimport", Handler: manualimportv5.GetHandler(manualImportLister)},
		{Method: "POST", Path: "/api/v5/manualimport", Handler: manualimportv5.ReprocessHandler(seriesSvc, episodesSvc, parsingSvcWithPopulator)},
		// J272 — v5 Command endpoint family (4 endpoints; 247 LOC C#).
		// Resource shape identical to v3 (same fields, same JSON tags,
		// same computed-property semantics). ONE status divergence:
		// DELETE/{id} 200+`{}` → 204 via v5util.RewriteSuccessTo204.
		// Other 3 endpoints path-alias (POST already 201 in v3 Go-port).
		{Method: "GET", Path: "/api/v5/command", Handler: commandv5.ListHandler(cmdSvc)},
		{Method: "GET", Path: "/api/v5/command/{id}", Handler: commandv5.ByIDHandler(cmdSvc)},
		{Method: "POST", Path: "/api/v5/command", Handler: commandv5.CreateHandler(cmdSvc, cmdRegistry)},
		{Method: "DELETE", Path: "/api/v5/command/{id}", Handler: commandv5.DeleteHandler(cmdSvc)},
		// J273 — v5 Log endpoint family (5 endpoints; 286 LOC C#).
		// Pure path-alias at empty-corpus level. All endpoints GET, all
		// 200. v3 Go-port already exposes all 5. LogResource v3 carries
		// `method?` field which v5 omits — invisible at empty-corpus
		// (v3 Go-port returns `records: []`; log DB plumbing deferred).
		// LogFileResource identical 5-field shape between v3 and v5.
		{Method: "GET", Path: "/api/v5/log", Handler: logv5.LogHandler(logReader)},
		{Method: "GET", Path: "/api/v5/log/file", Handler: logv5.FileListHandler(logFileSvc)},
		{Method: "GET", Path: "/api/v5/log/file/update", Handler: logv5.UpdateFileListHandler(logFileSvc)},
		{Method: "GET", Path: "/api/v5/log/file/update/{filename}", Handler: logv5.UpdateFileByNameHandler(logFileSvc)},
		{Method: "GET", Path: "/api/v5/log/file/{filename}", Handler: logv5.FileByNameHandler(logFileSvc)},
		// J274 — v5 System endpoint family (5 endpoints; 169 LOC C#).
		// SystemResource diverges: v5 omits `isContainerized` and adds
		// 3 deployment-info fields (`packageVersion`, `packageAuthor`,
		// `packageUpdateMechanismMessage`). v3 Go-port doesn't wire
		// IDeploymentInfoProvider yet — the 3 added fields emit as
		// empty strings (Wave 1 deferred-fields chain). GET
		// /routes/duplicate emits `{}` (v5 schema) vs v3's `[]`. GET
		// /routes is a v3-port gap — v5 emits empty text/plain (J266
		// pattern: close the v5 wire only). Shutdown + restart are
		// pure path-aliases.
		{Method: "GET", Path: "/api/v5/system/status", Handler: systemv5.StatusHandler(systemv5.StatusContext{V3: systemStatus}, cfg)},
		{Method: "GET", Path: "/api/v5/system/routes", Handler: systemv5.RoutesHandler()},
		{Method: "GET", Path: "/api/v5/system/routes/duplicate", Handler: systemv5.RoutesDuplicateHandler()},
		{Method: "POST", Path: "/api/v5/system/shutdown", Handler: systemv5.ShutdownHandler(lifecycleSvc)},
		{Method: "POST", Path: "/api/v5/system/restart", Handler: systemv5.RestartHandler(lifecycleSvc)},
		// J275 — v5 SystemTask endpoint family (2 endpoints; 79 LOC C#
		// — TaskController + TaskResource under System/Tasks/). ONE
		// wire-shape divergence: v5 adds `lastDuration` (TimeSpan as
		// string, computed as LastExecution - LastStartTime) which v3
		// Go-port omits entirely (J231 redacted from corpus). v5 also
		// emits `lastStartTime` unconditionally (non-nullable per v5
		// openapi schema) — when underlying Task.LastStartTime is nil,
		// surface as "0001-01-01T00:00:00Z" (.NET default(DateTime)
		// parity). v5-specific Resource + handlers (cannot path-alias).
		{Method: "GET", Path: "/api/v5/system/task", Handler: systemtaskv5.ListHandler(taskSvc)},
		{Method: "GET", Path: "/api/v5/system/task/{id}", Handler: systemtaskv5.ByIDHandler(taskSvc)},
		// J276 — v5 SystemBackup endpoint family (4 endpoints; 148 LOC
		// C# across BackupController + BackupResource). 2 divergences
		// from v3: (1) DELETE/{id} → 204 (v5 NoContent) vs v3's
		// 200+`{}` — v5util.RewriteSuccessTo204 wrapper. (2) POST
		// /restore/upload bad-extension path: v5 returns 400 +
		// `{"error":"Invalid extension..."}` vs v3's 415+envelope —
		// v5-specific handler (status+body shape divergence too
		// significant to wrap). GET list + POST /restore/{id} are
		// pure path-aliases.
		{Method: "GET", Path: "/api/v5/system/backup", Handler: systembackupv5.ListHandler(backupAdmin{svc: backupSvc})},
		{Method: "DELETE", Path: "/api/v5/system/backup/{id}", Handler: systembackupv5.DeleteHandler(backupAdmin{svc: backupSvc})},
		{Method: "POST", Path: "/api/v5/system/backup/restore/{id}", Handler: systembackupv5.RestoreHandler(backupAdmin{svc: backupSvc})},
		{Method: "POST", Path: "/api/v5/system/backup/restore/upload", Handler: systembackupv5.RestoreUploadHandler(backupUploader{svc: backupSvc, tempPath: backupTempPath})},
		// J277 — v5 QualityProfile + QualityProfileSchema endpoint
		// family (6 endpoints; 116 LOC C# across QualityProfileController
		// + QualityProfileSchemaController + QualityProfileResource).
		// Resource shape byte-identical to v3 (8 top-level fields +
		// 7-field nested item + 3-field nested format-item). ONE
		// status-only divergence: DELETE/{id} 200+empty → 204+empty
		// via v5util.RewriteSuccessTo204. v3 Go-port already emits
		// POST→201 and PUT→202 matching v5's TypedCreated/TypedAccepted
		// directly — other 5 endpoints pure path-alias. v3 DELETE's
		// 409 on ErrQualityProfileInUse passes through the wrapper
		// unchanged.
		{Method: "GET", Path: "/api/v5/qualityprofile", Handler: qualityprofilev5.ListHandler(qualityProfilesSvc, customFormatsSvc)},
		{Method: "GET", Path: "/api/v5/qualityprofile/{id}", Handler: qualityprofilev5.ByIDHandler(qualityProfilesSvc, customFormatsSvc)},
		{Method: "GET", Path: "/api/v5/qualityprofile/schema", Handler: qualityprofilev5.SchemaHandler(qualityProfilesSvc, customFormatsSvc)},
		{Method: "POST", Path: "/api/v5/qualityprofile", Handler: qualityprofilev5.CreateHandler(qualityProfilesSvc, customFormatsSvc)},
		{Method: "PUT", Path: "/api/v5/qualityprofile/{id}", Handler: qualityprofilev5.UpdateHandler(qualityProfilesSvc, customFormatsSvc)},
		{Method: "DELETE", Path: "/api/v5/qualityprofile/{id}", Handler: qualityprofilev5.DeleteHandler(qualityProfilesSvc)},
		// J278 — v5 ReleaseProfile endpoint family (5 endpoints; 154
		// LOC C# across ReleaseProfileController + ReleaseProfileResource).
		// TWO divergences from v3: (1) field rename + type change
		// `indexerId` (int) → `indexerIds` ([]int) — v5-specific
		// Resource + custom mapper required; v5 GET/POST/PUT all use
		// v5-specific handlers. (2) DELETE/{id} 200+empty → 204+empty
		// via v5util.RewriteSuccessTo204 (J277 pattern). Status codes
		// on the other 4 endpoints already match v3 directly
		// (POST→201, PUT→202, GETs→200).
		{Method: "GET", Path: "/api/v5/releaseprofile", Handler: releaseprofilev5.ListHandler(releaseProfilesSvc)},
		{Method: "GET", Path: "/api/v5/releaseprofile/{id}", Handler: releaseprofilev5.ByIDHandler(releaseProfilesSvc)},
		{Method: "POST", Path: "/api/v5/releaseprofile", Handler: releaseprofilev5.CreateHandler(releaseProfilesSvc)},
		{Method: "PUT", Path: "/api/v5/releaseprofile/{id}", Handler: releaseprofilev5.UpdateHandler(releaseProfilesSvc)},
		{Method: "DELETE", Path: "/api/v5/releaseprofile/{id}", Handler: releaseprofilev5.DeleteHandler(releaseProfilesSvc)},
		// J253 — v5 Metadata endpoint family (9 endpoints; PUT /bulk and
		// DELETE /bulk are [NonAction] in C# v5 so NOT routed here).
		// DELETE/{id} + POST /test wrap v3 via rewriteSuccessTo204 to
		// emit 204; other endpoints reuse v3 handlers verbatim.
		{Method: "GET", Path: "/api/v5/metadata", Handler: metadatav5.ListHandler(metadataRepo)},
		{Method: "GET", Path: "/api/v5/metadata/schema", Handler: metadatav5.SchemaHandler()},
		{Method: "GET", Path: "/api/v5/metadata/{id}", Handler: metadatav5.ByIDHandler(metadataRepo)},
		{Method: "POST", Path: "/api/v5/metadata", Handler: metadatav5.CreateHandler(metadataRepo, metadataEmitter)},
		{Method: "POST", Path: "/api/v5/metadata/test", Handler: metadatav5.TestHandler(metadataTester)},
		{Method: "POST", Path: "/api/v5/metadata/testall", Handler: metadatav5.TestAllHandler(metadataRepo, metadataTester)},
		{Method: "POST", Path: "/api/v5/metadata/action/{name}", Handler: metadatav5.RequestActionHandler(metadataTester)},
		{Method: "PUT", Path: "/api/v5/metadata/{id}", Handler: metadatav5.UpdateHandler(metadataRepo, metadataEmitter)},
		{Method: "DELETE", Path: "/api/v5/metadata/{id}", Handler: metadatav5.DeleteHandler(metadataRepo, metadataEmitter)},
		{Method: "GET", Path: "/api/v3/filesystem", Handler: apiv3.FileSystemHandler()},
		{Method: "GET", Path: "/api/v3/filesystem/type", Handler: apiv3.FileSystemTypeHandler()},
		{Method: "GET", Path: "/api/v3/filesystem/mediafiles", Handler: apiv3.FileSystemMediaFilesHandler()},
		{Method: "GET", Path: "/api/v3/downloadclient", Handler: apiv3.DownloadClientHandler(downloadClientsRepo)},
		{Method: "GET", Path: "/api/v3/downloadclient/schema", Handler: apiv3.DownloadClientSchemaHandler()},
		{Method: "GET", Path: "/api/v3/downloadclient/{id}", Handler: apiv3.DownloadClientByIDHandler(downloadClientsRepo)},
		{Method: "POST", Path: "/api/v3/downloadclient", Handler: apiv3.DownloadClientCreateHandler(downloadClientsRepo, downloadClientEmitter)},
		{Method: "POST", Path: "/api/v3/downloadclient/test", Handler: apiv3.DownloadClientTestHandler(downloadClientTester)},
		{Method: "POST", Path: "/api/v3/downloadclient/testall", Handler: apiv3.DownloadClientTestAllHandler(downloadClientsRepo, downloadClientTester)},
		{Method: "POST", Path: "/api/v3/downloadclient/action/{name}", Handler: apiv3.DownloadClientRequestActionHandler(downloadClientTester)},
		{Method: "PUT", Path: "/api/v3/downloadclient/bulk", Handler: apiv3.DownloadClientBulkUpdateHandler(downloadClientsRepo, downloadClientEmitter)},
		{Method: "DELETE", Path: "/api/v3/downloadclient/bulk", Handler: apiv3.DownloadClientBulkDeleteHandler(downloadClientsRepo, downloadClientEmitter)},
		{Method: "PUT", Path: "/api/v3/downloadclient/{id}", Handler: apiv3.DownloadClientUpdateHandler(downloadClientsRepo, downloadClientEmitter)},
		{Method: "DELETE", Path: "/api/v3/downloadclient/{id}", Handler: apiv3.DownloadClientDeleteHandler(downloadClientsRepo, downloadClientEmitter)},
		{Method: "GET", Path: "/api/v3/episode", Handler: apiv3.EpisodeListHandler(episodesSvcWithEpisodeFile)},
		{Method: "GET", Path: "/api/v3/episode/{id}", Handler: apiv3.EpisodeByIDHandler(episodesSvcWithEpisodeFile)},
		{Method: "PUT", Path: "/api/v3/episode/monitor", Handler: apiv3.EpisodeMonitorHandler(episodesSvc)},
		{Method: "GET", Path: "/api/v3/episodefile", Handler: apiv3.EpisodeFileListHandler(episodeFilesSvcPopulated, seriesSvc)},
		{Method: "GET", Path: "/api/v3/episodefile/{id}", Handler: apiv3.EpisodeFileByIDHandler(episodeFilesSvcPopulated)},
		{Method: "PUT", Path: "/api/v3/episodefile/{id}", Handler: apiv3.EpisodeFileUpdateHandler(episodeFilesSvcPopulated)},
		{Method: "PUT", Path: "/api/v3/episodefile/editor", Handler: apiv3.EpisodeFileEditorHandler(episodeFilesSvcPopulated)},
		{Method: "PUT", Path: "/api/v3/episodefile/bulk", Handler: apiv3.EpisodeFileBulkUpdateHandler(episodeFilesSvcPopulated)},
		{Method: "DELETE", Path: "/api/v3/episodefile/{id}", Handler: apiv3.EpisodeFileDeleteHandler(episodeFileDeleter)},
		{Method: "DELETE", Path: "/api/v3/episodefile/bulk", Handler: apiv3.EpisodeFileBulkDeleteHandler(episodeFileDeleter)},
		{Method: "GET", Path: "/api/v3/health", Handler: apiv3.HealthHandler(healthSvc)},
		{Method: "GET", Path: "/api/v3/history", Handler: apiv3.HistoryHandler(historyPagedAdapter{repo: historyRepo, seriesSvc: seriesSvc, qualityProfilesSvc: qualityProfilesSvc, customFormatsSvc: customFormatsSvc, episodesSvc: episodesSvc, upgradable: upgradableSpecForReads})},
		{Method: "GET", Path: "/api/v3/history/series", Handler: apiv3.HistorySeriesHandler(historySvcPopulated)},
		{Method: "GET", Path: "/api/v3/history/since", Handler: apiv3.HistorySinceHandler(historySvcPopulated)},
		{Method: "POST", Path: "/api/v3/history/failed/{id}", Handler: apiv3.HistoryMarkAsFailedHandler(failedDownloadsSvc)},
		{Method: "GET", Path: "/api/v3/importlist", Handler: apiv3.ImportListHandler(importListsRepo)},
		{Method: "GET", Path: "/api/v3/importlist/schema", Handler: apiv3.ImportListSchemaHandler()},
		{Method: "GET", Path: "/api/v3/importlist/{id}", Handler: apiv3.ImportListByIDHandler(importListsRepo)},
		{Method: "POST", Path: "/api/v3/importlist", Handler: apiv3.ImportListCreateHandler(importListsRepo, importListEmitter)},
		{Method: "POST", Path: "/api/v3/importlist/test", Handler: apiv3.ImportListTestHandler(importListTester)},
		{Method: "POST", Path: "/api/v3/importlist/testall", Handler: apiv3.ImportListTestAllHandler(importListsRepo, importListTester)},
		{Method: "POST", Path: "/api/v3/importlist/action/{name}", Handler: apiv3.ImportListRequestActionHandler(importListTester)},
		{Method: "PUT", Path: "/api/v3/importlist/bulk", Handler: apiv3.ImportListBulkUpdateHandler(importListsRepo, importListEmitter)},
		{Method: "DELETE", Path: "/api/v3/importlist/bulk", Handler: apiv3.ImportListBulkDeleteHandler(importListsRepo, importListEmitter)},
		{Method: "PUT", Path: "/api/v3/importlist/{id}", Handler: apiv3.ImportListUpdateHandler(importListsRepo, importListEmitter)},
		{Method: "DELETE", Path: "/api/v3/importlist/{id}", Handler: apiv3.ImportListDeleteHandler(importListsRepo, importListEmitter)},
		// v5 ImportList — same wire shape as v3, tightened status
		// codes on DELETE /{id} (204), POST /test (204), PUT /bulk (200),
		// DELETE /bulk (204 empty). Audit row C.1.c — closed 5.J223.
		{Method: "GET", Path: "/api/v5/importlist", Handler: importlistv5.ListHandler(importListsRepo)},
		{Method: "GET", Path: "/api/v5/importlist/schema", Handler: importlistv5.SchemaHandler()},
		{Method: "GET", Path: "/api/v5/importlist/{id}", Handler: importlistv5.ByIDHandler(importListsRepo)},
		{Method: "POST", Path: "/api/v5/importlist", Handler: importlistv5.CreateHandler(importListsRepo, importListEmitter)},
		{Method: "POST", Path: "/api/v5/importlist/test", Handler: importlistv5.TestHandler(importListTester)},
		{Method: "POST", Path: "/api/v5/importlist/testall", Handler: importlistv5.TestAllHandler(importListsRepo, importListTester)},
		{Method: "POST", Path: "/api/v5/importlist/action/{name}", Handler: importlistv5.RequestActionHandler(importListTester)},
		{Method: "PUT", Path: "/api/v5/importlist/bulk", Handler: importlistv5.BulkUpdateHandler(importListsRepo, importListEmitter)},
		{Method: "DELETE", Path: "/api/v5/importlist/bulk", Handler: importlistv5.BulkDeleteHandler(importListsRepo, importListEmitter)},
		{Method: "PUT", Path: "/api/v5/importlist/{id}", Handler: importlistv5.UpdateHandler(importListsRepo, importListEmitter)},
		{Method: "DELETE", Path: "/api/v5/importlist/{id}", Handler: importlistv5.DeleteHandler(importListsRepo, importListEmitter)},
		{Method: "GET", Path: "/api/v3/importlistexclusion", Handler: apiv3.ImportListExclusionHandler(importListExclusionsRepo)},
		{Method: "GET", Path: "/api/v3/importlistexclusion/paged", Handler: apiv3.ImportListExclusionPagedHandler(importListExclusionsRepo)},
		{Method: "GET", Path: "/api/v3/importlistexclusion/{id}", Handler: apiv3.ImportListExclusionByIDHandler(importListExclusionsRepo)},
		{Method: "POST", Path: "/api/v3/importlistexclusion", Handler: apiv3.ImportListExclusionCreateHandler(importListExclusionsRepo)},
		{Method: "PUT", Path: "/api/v3/importlistexclusion/{id}", Handler: apiv3.ImportListExclusionUpdateHandler(importListExclusionsRepo)},
		{Method: "DELETE", Path: "/api/v3/importlistexclusion/{id}", Handler: apiv3.ImportListExclusionDeleteHandler(importListExclusionsRepo)},
		{Method: "DELETE", Path: "/api/v3/importlistexclusion/bulk", Handler: apiv3.ImportListExclusionBulkDeleteHandler(importListExclusionsRepo)},
		{Method: "GET", Path: "/api/v3/indexer", Handler: apiv3.IndexerHandler(indexersRepo)},
		{Method: "GET", Path: "/api/v3/indexer/schema", Handler: apiv3.IndexerSchemaHandler()},
		{Method: "GET", Path: "/api/v3/indexer/{id}", Handler: apiv3.IndexerByIDHandler(indexersRepo)},
		{Method: "POST", Path: "/api/v3/indexer", Handler: apiv3.IndexerCreateHandler(indexersRepo, indexerEmitter)},
		{Method: "POST", Path: "/api/v3/indexer/test", Handler: apiv3.IndexerTestHandler(indexerTester)},
		{Method: "POST", Path: "/api/v3/indexer/testall", Handler: apiv3.IndexerTestAllHandler(indexersRepo, indexerTester)},
		{Method: "POST", Path: "/api/v3/indexer/action/{name}", Handler: apiv3.IndexerRequestActionHandler(indexerTester)},
		{Method: "PUT", Path: "/api/v3/indexer/bulk", Handler: apiv3.IndexerBulkUpdateHandler(indexersRepo, indexerEmitter)},
		{Method: "DELETE", Path: "/api/v3/indexer/bulk", Handler: apiv3.IndexerBulkDeleteHandler(indexersRepo, indexerEmitter)},
		{Method: "PUT", Path: "/api/v3/indexer/{id}", Handler: apiv3.IndexerUpdateHandler(indexersRepo, indexerEmitter)},
		{Method: "DELETE", Path: "/api/v3/indexer/{id}", Handler: apiv3.IndexerDeleteHandler(indexersRepo, indexerEmitter)},
		{Method: "GET", Path: "/api/v3/indexerflag", Handler: apiv3.IndexerFlagHandler()},
		{Method: "GET", Path: "/api/v3/language", Handler: apiv3.LanguageHandler(langs)},
		{Method: "GET", Path: "/api/v3/languageprofile", Handler: apiv3.LanguageProfileHandler()},
		{Method: "GET", Path: "/api/v3/languageprofile/schema", Handler: apiv3.LanguageProfileSchemaHandler()},
		{Method: "GET", Path: "/api/v3/languageprofile/{id}", Handler: apiv3.LanguageProfileByIDHandler()},
		{Method: "POST", Path: "/api/v3/languageprofile", Handler: apiv3.LanguageProfileWriteAcceptedHandler()},
		{Method: "PUT", Path: "/api/v3/languageprofile/{id}", Handler: apiv3.LanguageProfileWriteAcceptedHandler()},
		{Method: "DELETE", Path: "/api/v3/languageprofile/{id}", Handler: apiv3.LanguageProfileWriteAcceptedHandler()},
		{Method: "GET", Path: "/api/v3/localization", Handler: apiv3.LocalizationHandler(localizationSvc)},
		{Method: "GET", Path: "/api/v3/log", Handler: apiv3.LogHandler(logReader)},
		{Method: "GET", Path: "/api/v3/log/file", Handler: apiv3.LogFileHandler(logFileSvc)},
		{Method: "GET", Path: "/api/v3/log/file/update", Handler: apiv3.UpdateLogFileHandler(logFileSvc)},
		{Method: "GET", Path: "/api/v3/log/file/update/{filename}", Handler: apiv3.UpdateLogFileByNameHandler(logFileSvc)},
		{Method: "GET", Path: "/api/v3/log/file/{filename}", Handler: apiv3.LogFileByNameHandler(logFileSvc)},
		{Method: "GET", Path: "/api/v3/manualimport", Handler: apiv3.ManualImportHandler(manualImportLister)},
		// J238 / C.1.i-follow-5b: manualimport POST reprocess endpoint.
		{Method: "POST", Path: "/api/v3/manualimport", Handler: apiv3.ManualImportReprocessHandler(seriesSvc, episodesSvc, parsingSvcWithPopulator)},
		{Method: "GET", Path: "/api/v3/mediacover/{seriesId}/{filename}", Handler: apiv3.MediaCoverHandler(mediaCoverSvc)},
		{Method: "GET", Path: "/api/v3/metadata", Handler: apiv3.MetadataListHandler(metadataRepo)},
		{Method: "GET", Path: "/api/v3/metadata/schema", Handler: apiv3.MetadataSchemaHandler()},
		{Method: "GET", Path: "/api/v3/metadata/{id}", Handler: apiv3.MetadataByIDHandler(metadataRepo)},
		{Method: "POST", Path: "/api/v3/metadata", Handler: apiv3.MetadataCreateHandler(metadataRepo, metadataEmitter)},
		{Method: "POST", Path: "/api/v3/metadata/test", Handler: apiv3.MetadataTestHandler(metadataTester)},
		{Method: "POST", Path: "/api/v3/metadata/testall", Handler: apiv3.MetadataTestAllHandler(metadataRepo, metadataTester)},
		{Method: "POST", Path: "/api/v3/metadata/action/{name}", Handler: apiv3.MetadataRequestActionHandler(metadataTester)},
		{Method: "PUT", Path: "/api/v3/metadata/bulk", Handler: apiv3.MetadataBulkUpdateHandler(metadataRepo, metadataEmitter)},
		{Method: "DELETE", Path: "/api/v3/metadata/bulk", Handler: apiv3.MetadataBulkDeleteHandler(metadataRepo, metadataEmitter)},
		{Method: "PUT", Path: "/api/v3/metadata/{id}", Handler: apiv3.MetadataUpdateHandler(metadataRepo, metadataEmitter)},
		{Method: "DELETE", Path: "/api/v3/metadata/{id}", Handler: apiv3.MetadataDeleteHandler(metadataRepo, metadataEmitter)},
		{Method: "GET", Path: "/api/v3/localization/language", Handler: apiv3.LocalizationLanguageHandler(configProv.UILanguage)},
		{Method: "GET", Path: "/api/v3/notification", Handler: apiv3.NotificationHandler(notificationsRepo)},
		{Method: "GET", Path: "/api/v3/notification/schema", Handler: apiv3.NotificationSchemaHandler()},
		{Method: "GET", Path: "/api/v3/notification/{id}", Handler: apiv3.NotificationByIDHandler(notificationsRepo)},
		{Method: "POST", Path: "/api/v3/notification", Handler: apiv3.NotificationCreateHandler(notificationsRepo, notificationRegistry, notificationEmitter)},
		{Method: "POST", Path: "/api/v3/notification/test", Handler: apiv3.NotificationTestHandler(notificationTester)},
		{Method: "POST", Path: "/api/v3/notification/testall", Handler: apiv3.NotificationTestAllHandler(notificationsRepo, notificationTester)},
		{Method: "POST", Path: "/api/v3/notification/action/{name}", Handler: apiv3.NotificationRequestActionHandler(notificationTester)},
		{Method: "PUT", Path: "/api/v3/notification/bulk", Handler: apiv3.NotificationBulkNotSupportedHandler()},
		{Method: "DELETE", Path: "/api/v3/notification/bulk", Handler: apiv3.NotificationBulkNotSupportedHandler()},
		{Method: "PUT", Path: "/api/v3/notification/{id}", Handler: apiv3.NotificationUpdateHandler(notificationsRepo, notificationRegistry, notificationEmitter)},
		{Method: "DELETE", Path: "/api/v3/notification/{id}", Handler: apiv3.NotificationDeleteHandler(notificationsRepo, notificationEmitter)},
		{Method: "GET", Path: "/api/v3/parse", Handler: apiv3.ParseHandler(parsingSvcWithPopulator)},
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
		{Method: "GET", Path: "/api/v3/queue", Handler: apiv3.QueueHandler(queuePagedAdapter{svc: queueSvc, qualityDefs: qualityDefSvc, qualityProfiles: qualityProfilesSvc})},
		{Method: "GET", Path: "/api/v3/queue/details", Handler: apiv3.QueueDetailsHandler(emptyQueueDetailsLister{})},
		{Method: "GET", Path: "/api/v3/queue/status", Handler: apiv3.QueueStatusHandler(queueSvc, pendingCountLister{repo: pendingReleasesRepo}, queueStatusPauser)},
		{Method: "DELETE", Path: "/api/v3/queue/bulk", Handler: apiv3.QueueBulkDeleteHandler(queueRemover, nil)},
		{Method: "DELETE", Path: "/api/v3/queue/{id}", Handler: apiv3.QueueDeleteHandler(queueRemover, nil)},
		// J236 / C.1.i-follow-5c: queue/grab POST endpoints — single + bulk.
		// Routes pending-release → ProcessDecisions dispatch path.
		{Method: "POST", Path: "/api/v3/queue/grab/{id}", Handler: apiv3.QueueGrabHandler(pendingReleasesRepo, seriesSvc, processDownloadDecisions)},
		{Method: "POST", Path: "/api/v3/queue/grab/bulk", Handler: apiv3.QueueGrabBulkHandler(pendingReleasesRepo, seriesSvc, processDownloadDecisions)},
		{Method: "GET", Path: "/api/v3/release", Handler: apiv3.ReleaseHandler(indexerSearchSvc)},
		// J237 / C.1.i-follow-5a: release POST (cache-lookup + dispatch) + Newznab push.
		{Method: "POST", Path: "/api/v3/release", Handler: apiv3.ReleaseDownloadHandler(releaseCache, processDownloadDecisions, seriesSvc, episodesSvc, parsingSvc, episodesSvc)},
		{Method: "POST", Path: "/api/v3/release/push", Handler: apiv3.ReleasePushHandler(downloadDecider, processDownloadDecisions, releaseCache, indexersRepo, downloadClientsRepo)},
		{Method: "GET", Path: "/api/v3/releaseprofile", Handler: apiv3.ReleaseProfileHandler(releaseProfilesSvc)},
		{Method: "GET", Path: "/api/v3/rename", Handler: apiv3.RenameHandler()},
		{Method: "GET", Path: "/api/v3/rename/bulk", Handler: apiv3.RenameBulkHandler()},
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
		{Method: "GET", Path: "/api/v3/series", Handler: apiv3.SeriesListHandler(seriesImgSvc)},
		{Method: "GET", Path: "/api/v3/series/{id}", Handler: apiv3.SeriesByIDHandler(seriesImgSvc)},
		{Method: "GET", Path: "/api/v3/series/{id}/folder", Handler: apiv3.SeriesFolderHandler(seriesSvc, namingConfigSvc)},
		{Method: "GET", Path: "/api/v3/series/lookup", Handler: apiv3.SeriesLookupHandler()},
		{Method: "POST", Path: "/api/v3/series", Handler: apiv3.SeriesCreateHandler(seriesWriter, seriesEmitter)},
		{Method: "PUT", Path: "/api/v3/series/{id}", Handler: apiv3.SeriesUpdateHandler(seriesWriter, seriesEmitter)},
		{Method: "DELETE", Path: "/api/v3/series/{id}", Handler: apiv3.SeriesDeleteHandler(seriesWriter, seriesEmitter)},
		{Method: "PUT", Path: "/api/v3/series/editor", Handler: apiv3.SeriesEditorUpdateHandler(seriesWriter, seriesEmitter)},
		{Method: "DELETE", Path: "/api/v3/series/editor", Handler: apiv3.SeriesEditorDeleteHandler(seriesWriter, seriesEmitter)},
		{Method: "POST", Path: "/api/v3/series/import", Handler: apiv3.SeriesImportHandler(seriesWriter, seriesEmitter)},
		{Method: "POST", Path: "/api/v3/seasonpass", Handler: apiv3.SeasonPassHandler(seriesWriter, seriesEmitter, episodeMonitoredSvc)},
		{Method: "GET", Path: "/api/v3/system/backup", Handler: apiv3.BackupListHandler(backupAdmin{svc: backupSvc})},
		{Method: "DELETE", Path: "/api/v3/system/backup/{id}", Handler: apiv3.BackupDeleteHandler(backupAdmin{svc: backupSvc})},
		{Method: "POST", Path: "/api/v3/system/backup/restore/{id}", Handler: apiv3.BackupRestoreHandler(backupAdmin{svc: backupSvc})},
		{Method: "POST", Path: "/api/v3/system/backup/restore/upload", Handler: apiv3.BackupRestoreUploadHandler(backupUploader{svc: backupSvc, tempPath: backupTempPath})},
		{Method: "POST", Path: "/api/v3/system/restart", Handler: apiv3.SystemRestartHandler(lifecycleSvc)},
		{Method: "GET", Path: "/api/v3/system/routes/duplicate", Handler: apiv3.SystemRoutesDuplicateHandler()},
		{Method: "POST", Path: "/api/v3/system/shutdown", Handler: apiv3.SystemShutdownHandler(lifecycleSvc)},
		{Method: "GET", Path: "/api/v3/system/status", Handler: apiv3.SystemStatusHandler(systemStatus, cfg)},
		{Method: "GET", Path: "/api/v3/system/task", Handler: apiv3.SystemTaskHandler(taskSvc)},
		{Method: "GET", Path: "/api/v3/system/task/{id}", Handler: apiv3.SystemTaskByIDHandler(taskSvc)},
		{Method: "GET", Path: "/api/v3/tag", Handler: apiv3.TagHandler(tagsSvc)},
		{Method: "GET", Path: "/api/v3/tag/{id}", Handler: apiv3.TagByIDHandler(tagsSvc)},
		{Method: "POST", Path: "/api/v3/tag", Handler: apiv3.TagCreateHandler(tagsSvc)},
		{Method: "PUT", Path: "/api/v3/tag/{id}", Handler: apiv3.TagUpdateHandler(tagsSvc)},
		{Method: "DELETE", Path: "/api/v3/tag/{id}", Handler: apiv3.TagDeleteHandler(tagsSvc)},
		{Method: "GET", Path: "/api/v3/tag/detail", Handler: apiv3.TagDetailHandler(emptyTagDetailLister{})},
		{Method: "GET", Path: "/api/v3/tag/detail/{id}", Handler: apiv3.TagDetailByIDHandler(emptyTagDetailLister{})},
		{Method: "GET", Path: "/api/v3/update", Handler: apiv3.UpdateHandler(recentUpdates)},
		{Method: "GET", Path: "/api/v3/wanted/cutoff", Handler: apiv3.WantedHandler(wantedRepoLister{episodes: episodesRepo, profiles: qualityProfilesSvc, seriesSvc: seriesSvc, epFiles: episodeFilesSvcPopulated, now: func() time.Time { return time.Now().UTC() }}, apiv3.WantedCutoff)},
		{Method: "GET", Path: "/api/v3/wanted/cutoff/{id}", Handler: apiv3.EpisodeByIDHandler(episodesSvcWithEpisodeFile)},
		{Method: "GET", Path: "/api/v3/wanted/missing", Handler: apiv3.WantedHandler(wantedRepoLister{episodes: episodesRepo, profiles: qualityProfilesSvc, seriesSvc: seriesSvc, epFiles: episodeFilesSvcPopulated, now: func() time.Time { return time.Now().UTC() }}, apiv3.WantedMissing)},
		{Method: "GET", Path: "/api/v3/wanted/missing/{id}", Handler: apiv3.EpisodeByIDHandler(episodesSvcWithEpisodeFile)},
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
