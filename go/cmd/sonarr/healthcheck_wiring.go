// Boot-time adapters that bridge the production services into the
// `internal/healthcheck` per-check narrow interfaces. Same posture as
// `decisionengine_wiring.go` / `downloadedimport_wiring.go`: the
// adapter only exists where a real service exists but its surface
// differs from what a check needs.
//
// The healthcheck.Service check list is fixed at construction time
// (see [healthcheck.NewService]); each new check requires a boot-site
// constructor invocation. Adapters live here so main.go's serve()
// stays focused on ordering / dependency wiring.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"sonarr.tv/sonarr/internal/config"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/importlists"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/metadatasource"
	"sonarr.tv/sonarr/internal/namingconfig"
	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/rootfolders"
)

// seriesFolderFormatAdapter bridges *namingconfig.Service to the
// narrow [rootfolders.NamingConfigProvider] which only needs the
// SeriesFolderFormat field. Pulls the singleton row via Service.GetConfig
// (cached per slice 3.A2) and forwards the one field.
//
// Errors propagate verbatim. nil-Config arm is defensive only — the
// Service guarantees a non-nil row whenever GetConfig returns nil err.
type seriesFolderFormatAdapter struct {
	svc *namingconfig.Service
}

func (a seriesFolderFormatAdapter) GetSeriesFolderFormat(ctx context.Context) (string, error) {
	cfg, err := a.svc.GetConfig(ctx)
	if err != nil {
		return "", err
	}
	if cfg == nil {
		return "", nil
	}
	return cfg.SeriesFolderFormat, nil
}

// Compile-time guards.
var (
	_ rootfolders.NamingConfigProvider = seriesFolderFormatAdapter{}
	_ healthcheck.RootFolderResolver   = (*rootfolders.Service)(nil)
)

// enabledNotificationListerAdapter implements
// healthcheck.EnabledNotificationLister — the "list every enabled
// notification provider" projection the slice 4.AI
// NotificationStatusCheck consumes. Wraps notifications.Repo.All and
// applies the C# `NotificationDefinition.Enable` predicate (any
// per-event flag true) at the boundary.
//
// C# parallel: `NotificationStatusCheck.cs:27` —
// `_providerFactory.GetAvailableProviders()` returns the Active()
// subset; the Go port flattens to (ID, Name) since the healthcheck
// doesn't need anything else.
type enabledNotificationListerAdapter struct {
	repo *notifications.Repo
}

// newEnabledNotificationListerAdapter binds the dependency.
func newEnabledNotificationListerAdapter(repo *notifications.Repo) enabledNotificationListerAdapter {
	return enabledNotificationListerAdapter{repo: repo}
}

// EnabledNotifications implements healthcheck.EnabledNotificationLister.
// Applies the same "any per-event flag true" predicate the C#
// `NotificationDefinition.Enable` computed field at
// `Notifications/NotificationDefinition.cs:66` uses.
func (a enabledNotificationListerAdapter) EnabledNotifications(ctx context.Context) ([]healthcheck.EnabledNotification, error) {
	rows, err := a.repo.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("notifications.All: %w", err)
	}
	out := make([]healthcheck.EnabledNotification, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		if !notificationEnabled(row) {
			continue
		}
		out = append(out, healthcheck.EnabledNotification{ID: row.ID, Name: row.Name})
	}
	return out, nil
}

// notificationEnabled mirrors the C# `NotificationDefinition.Enable`
// computed-field at `Notifications/NotificationDefinition.cs:66` —
// the row is enabled iff ANY OnX flag is true.
func notificationEnabled(n *notifications.Notification) bool {
	if n == nil {
		return false
	}
	if n.OnGrab || n.OnDownload || n.OnRename || n.OnImportComplete ||
		n.OnSeriesAdd || n.OnSeriesDelete || n.OnEpisodeFileDelete ||
		n.OnEpisodeFileDeleteForUpgrade || n.OnHealthIssue ||
		n.OnHealthRestored || n.OnApplicationUpdate ||
		n.OnManualInteractionRequired {
		return true
	}
	if n.OnUpgrade != nil && *n.OnUpgrade {
		return true
	}
	return false
}

// importListListerAdapter implements both
// [healthcheck.EnabledImportListLister] and
// [healthcheck.AllImportListLister] — the projections slice 4.AQ
// ImportListStatusCheck + ImportListRootFolderCheck consume. Wraps
// importlists.Repo.All and applies the C# `ImportListDefinition.
// Enable => EnableAutomaticAdd` predicate at the boundary for the
// enabled variant.
//
// C# parallel: `ImportListStatusCheck.cs:27` —
// `_providerFactory.GetAvailableProviders()` returns the Active()
// subset (EnableAutomaticAdd=true) for the status check; the
// root-folder check at `ImportListRootFolderCheck.cs:38` uses
// `_importListFactory.All()` (every row).
type importListListerAdapter struct {
	repo *importlists.Repo
}

// newImportListListerAdapter binds the dependency.
func newImportListListerAdapter(repo *importlists.Repo) importListListerAdapter {
	return importListListerAdapter{repo: repo}
}

// EnabledImportLists implements healthcheck.EnabledImportListLister.
// Applies the "EnableAutomaticAdd is true" predicate (nullable
// *bool, nil-defaults-false per C# `ImportListDefinition.cs:12`).
func (a importListListerAdapter) EnabledImportLists(ctx context.Context) ([]healthcheck.EnabledImportList, error) {
	rows, err := a.repo.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("importlists.All: %w", err)
	}
	out := make([]healthcheck.EnabledImportList, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		if row.EnableAutomaticAdd == nil || !*row.EnableAutomaticAdd {
			continue
		}
		out = append(out, healthcheck.EnabledImportList{
			ID:             row.ID,
			Name:           row.Name,
			RootFolderPath: row.RootFolderPath,
		})
	}
	return out, nil
}

// AllImportLists implements healthcheck.AllImportListLister. No
// filtering — every persisted row maps through.
func (a importListListerAdapter) AllImportLists(ctx context.Context) ([]healthcheck.EnabledImportList, error) {
	rows, err := a.repo.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("importlists.All: %w", err)
	}
	out := make([]healthcheck.EnabledImportList, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		out = append(out, healthcheck.EnabledImportList{
			ID:             row.ID,
			Name:           row.Name,
			RootFolderPath: row.RootFolderPath,
		})
	}
	return out, nil
}

// rootFolderPathListerAdapter implements
// [healthcheck.ImportListRootFolderPathLister] — wraps
// rootfolders.Repo.All and projects to the Path-only slice the
// healthcheck consumes.
type rootFolderPathListerAdapter struct {
	repo *rootfolders.Repo
}

func newRootFolderPathListerAdapter(repo *rootfolders.Repo) rootFolderPathListerAdapter {
	return rootFolderPathListerAdapter{repo: repo}
}

// AllRootFolderPaths implements healthcheck.ImportListRootFolderPathLister.
func (a rootFolderPathListerAdapter) AllRootFolderPaths(ctx context.Context) ([]string, error) {
	rows, err := a.repo.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("rootfolders.All: %w", err)
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Path)
	}
	return out, nil
}

// serviceTimeURL is the C# `Resource("/time")` against
// `ISonarrCloudRequestBuilder.Services` (base
// `https://services.sonarr.tv/v1/`) — see
// `src/NzbDrone.Common/Cloud/SonarrCloudRequestBuilder.cs:13`. Exposed
// as a package-level constant so the adapter and its test share a
// single source of truth.
const serviceTimeURL = "https://services.sonarr.tv/v1/time"

// serviceTimeResponse mirrors C# `ServiceTimeResponse` at
// `src/NzbDrone.Core/HealthCheck/Checks/SystemTimeCheck.cs:56-59`.
// SkyHook/Services emit `{"dateTimeUtc": "<RFC3339>"}`; the
// json.Unmarshal default decoder parses RFC3339 into time.Time UTC.
type serviceTimeResponse struct {
	DateTimeUtc time.Time `json:"dateTimeUtc"`
}

// newServiceTimeFetcher returns a [healthcheck.SystemTimeFetcher]
// bound to the supplied *http.Client and the services.sonarr.tv/v1/time
// endpoint. Slice 4.AW (queue [M5] of Phase 4 focused-subset 5) —
// wires the existing internal/healthcheck/system_time.go check, which
// was ported in an earlier slice but never reached main.go's check
// list. C# parity: `SystemTimeCheck.cs:28-32`.
//
// Errors from the upstream are returned unwrapped — the consuming
// [healthcheck.SystemTimeCheck.Run] swallows them per the C#
// `catch (Exception e)` block at `:47-50` (logs at Warn, falls through
// to OK).
func newServiceTimeFetcher(client *http.Client) healthcheck.SystemTimeFetcher {
	return func(ctx context.Context) (time.Time, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, serviceTimeURL, nil)
		if err != nil {
			return time.Time{}, fmt.Errorf("services time: build request: %w", err)
		}
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return time.Time{}, fmt.Errorf("services time: http: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return time.Time{}, fmt.Errorf("services time: HTTP %d", resp.StatusCode)
		}
		var body serviceTimeResponse
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return time.Time{}, fmt.Errorf("services time: decode: %w", err)
		}
		return body.DateTimeUtc.UTC(), nil
	}
}

// skyHookProberAdapter bridges *metadatasource.Proxy to the
// [healthcheck.SkyHookProber] narrow interface. The proxy surface
// is wider than the check needs (GetSeriesInfo + SearchForNewSeries
// + 4 typed-id wrappers); the adapter narrows it to the single
// "probe known TVDB id" call.
//
// Slice 4.AV (queue [M4] of Phase 4 focused-subset 5).
type skyHookProberAdapter struct {
	proxy *metadatasource.Proxy
}

func newSkyHookProberAdapter(proxy *metadatasource.Proxy) skyHookProberAdapter {
	return skyHookProberAdapter{proxy: proxy}
}

// ProbeKnownSeries implements healthcheck.SkyHookProber by firing
// a GetSeriesInfo against [healthcheck.SkyHookKnownTvdbID]. The
// returned (series, episodes) pair is discarded — only the err
// channel matters for the healthcheck classification (preserves
// the [metadatasource.ErrServiceUnavailable] sentinel chain).
func (a skyHookProberAdapter) ProbeKnownSeries(ctx context.Context) error {
	_, _, err := a.proxy.GetSeriesInfo(ctx, healthcheck.SkyHookKnownTvdbID)
	return err
}

// proxyHostConfigAdapter satisfies [healthcheck.ProxyCheckConfig] by
// reading from the keyvalues-backed `*config.Provider` (which ports
// the C# `IConfigService.ProxyEnabled` + `IConfigService.ProxyHostname`).
// Slice 5.J56 (audit B.5 ProxyCheck closure): replaced the
// proxyHostConfigStub now that the config.Provider keyvalues
// accessors exist.
type proxyHostConfigAdapter struct {
	cfg *config.Provider
}

func (a proxyHostConfigAdapter) ProxyEnabled() bool    { return a.cfg.ProxyEnabled() }
func (a proxyHostConfigAdapter) ProxyHostname() string { return a.cfg.ProxyHostname() }

// newProxyPinger returns a [healthcheck.ProxyCheckPinger] that pings
// `services.sonarr.tv/v1/ping` THROUGH the configured proxy.
// Mirrors C# `ProxyCheck.cs:54-89`: builds the request via the cloud
// request builder + sends through the active HTTP client. The Go
// port uses `net/http` with a `Transport.Proxy` set to the
// configured proxy URL when ProxyEnabled is true; on a clean Sonarr
// install (ProxyEnabled=false) the pinger is never invoked.
//
// Returns the HTTP status code on a 2xx-or-3xx response, or 400
// when the upstream returns BadRequest (the check at C# `:63-75`
// only treats 400 as a hard failure). Other status codes pass
// through transparently — the check ignores non-400 statuses.
//
// Slice 5.J56 (audit B.5 ProxyCheck closure).
func newProxyPinger(cfg *config.Provider) healthcheck.ProxyCheckPinger {
	return func(ctx context.Context) (int, error) {
		proxyURL := buildProxyURL(cfg)
		transport := &http.Transport{}
		if proxyURL != nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
		client := &http.Client{
			Transport: transport,
			Timeout:   10 * time.Second,
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://services.sonarr.tv/v1/ping", nil)
		if err != nil {
			return 0, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return 0, err
		}
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode, nil
	}
}

// buildProxyURL constructs a net/url.URL the http.Transport can
// consume for `http.ProxyURL`. Returns nil when the proxy is
// disabled or has an empty hostname. The C# tree supports HTTP +
// SOCKS5; the Go port currently supports only HTTP/HTTPS — SOCKS5
// would need `golang.org/x/net/proxy` integration, deferred until a
// user reports the gap.
func buildProxyURL(cfg *config.Provider) *url.URL {
	if !cfg.ProxyEnabled() {
		return nil
	}
	host := strings.TrimSpace(cfg.ProxyHostname())
	if host == "" {
		return nil
	}
	scheme := "http"
	if t := strings.ToLower(strings.TrimSpace(cfg.ProxyType())); t == "https" {
		scheme = "https"
	}
	u := &url.URL{
		Scheme: scheme,
		Host:   fmt.Sprintf("%s:%d", host, cfg.ProxyPort()),
	}
	if user := strings.TrimSpace(cfg.ProxyUsername()); user != "" {
		u.User = url.UserPassword(user, cfg.ProxyPassword())
	}
	return u
}

// rssIndexerCountAdapter implements healthcheck.RssEnabledLister.
// Wraps indexers.Repo.All and counts rows with EnableRss=true.
// requireActive currently has no effect — the per-indexer status/
// backoff store hasn't ported yet, so the active count equals the
// enabled count. When the status filter lands, requireActive=true
// will cross-join indexerstatus to drop blocked rows.
type rssIndexerCountAdapter struct {
	repo *indexers.Repo
}

func newRssIndexerCountAdapter(repo *indexers.Repo) rssIndexerCountAdapter {
	return rssIndexerCountAdapter{repo: repo}
}

func (a rssIndexerCountAdapter) RssEnabledCount(ctx context.Context, _ bool) (int, error) {
	rows, err := a.repo.All(ctx)
	if err != nil {
		return 0, fmt.Errorf("indexers.All: %w", err)
	}
	count := 0
	for i := range rows {
		row := &rows[i]
		if row.EnableRss != nil && *row.EnableRss {
			count++
		}
	}
	return count, nil
}

// searchIndexerCountAdapter implements healthcheck.SearchEnabledLister.
// Counts rows by per-flag filters. requireActive is a no-op until the
// indexer status filter ports (parity with rssIndexerCountAdapter).
type searchIndexerCountAdapter struct {
	repo *indexers.Repo
}

func newSearchIndexerCountAdapter(repo *indexers.Repo) searchIndexerCountAdapter {
	return searchIndexerCountAdapter{repo: repo}
}

func (a searchIndexerCountAdapter) AutomaticSearchEnabledCount(ctx context.Context, _ bool) (int, error) {
	rows, err := a.repo.All(ctx)
	if err != nil {
		return 0, fmt.Errorf("indexers.All: %w", err)
	}
	count := 0
	for i := range rows {
		row := &rows[i]
		if row.EnableAutomaticSearch != nil && *row.EnableAutomaticSearch {
			count++
		}
	}
	return count, nil
}

func (a searchIndexerCountAdapter) InteractiveSearchEnabledCount(ctx context.Context) (int, error) {
	rows, err := a.repo.All(ctx)
	if err != nil {
		return 0, fmt.Errorf("indexers.All: %w", err)
	}
	count := 0
	for i := range rows {
		if rows[i].EnableInteractiveSearch {
			count++
		}
	}
	return count, nil
}

// enabledDownloadClientCountAdapter implements
// healthcheck.DownloadClientLister. Counts rows with Enable=true.
// The C# `_downloadClientProvider.GetDownloadClients()` returns the
// LIST of active clients; the Go port flattens to a count since
// DownloadClientCheck only consumes the count today (per-client
// `GetItems()` probing is DEFERRED — see DownloadClientCheck doc).
type enabledDownloadClientCountAdapter struct {
	repo *downloadclients.Repo
}

func newEnabledDownloadClientCountAdapter(repo *downloadclients.Repo) enabledDownloadClientCountAdapter {
	return enabledDownloadClientCountAdapter{repo: repo}
}

func (a enabledDownloadClientCountAdapter) EnabledDownloadClientCount(ctx context.Context) (int, error) {
	rows, err := a.repo.All(ctx)
	if err != nil {
		return 0, fmt.Errorf("downloadclients.All: %w", err)
	}
	count := 0
	for i := range rows {
		if rows[i].Enable {
			count++
		}
	}
	return count, nil
}

// downloadClientStatusEnumeratorAdapter implements
// healthcheck.DownloadClientStatusEnumerator. Walks the
// downloadclients table, filters to enabled rows, constructs each
// row via the registry, and calls Provider.GetStatus per row.
// Mirrors the C# `DownloadClientProvider.GetDownloadClients(true)`
// surface the DC-status family of healthchecks
// (DownloadClientSortingCheck, DownloadClientRemovesCompletedCheck,
// DownloadClientRootFolderCheck) consume. Per-row construction or
// status failures land on the EnumeratedDownloadClient.Err field so
// the calling check can log + skip without aborting the
// enumeration.
//
// Audit fix B.5d. The adapter is constructed at healthSvc-wire time
// (before httpClient and the downloadclients.Dependencies bundle
// exist) with a nil bundle, then has its deps bound late via
// SetDeps once the wiring at main.go:~2320 puts httpClient,
// downloadClientStatusSvc, and remotePathMappingsSvc in scope. Until
// SetDeps fires, EnabledDownloadClients returns rows with Err set
// to "registry not yet wired" — the consuming healthchecks log +
// skip and report OK. After SetDeps, every enabled row is
// constructed + status-fetched on each Run.
type downloadClientStatusEnumeratorAdapter struct {
	repo *downloadclients.Repo
	log  *slog.Logger

	mu       sync.RWMutex
	registry *downloadclients.Registry
	deps     downloadclients.Dependencies
}

func newDownloadClientStatusEnumeratorAdapter(repo *downloadclients.Repo, log *slog.Logger) *downloadClientStatusEnumeratorAdapter {
	if log == nil {
		log = slog.Default()
	}
	return &downloadClientStatusEnumeratorAdapter{repo: repo, log: log}
}

// SetDeps binds the registry + Dependencies bundle. Safe to call
// after the adapter is already in service — the check
// orchestrator's per-cadence Run() acquires the read lock.
func (a *downloadClientStatusEnumeratorAdapter) SetDeps(registry *downloadclients.Registry, deps downloadclients.Dependencies) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.registry = registry
	a.deps = deps
}

func (a *downloadClientStatusEnumeratorAdapter) EnabledDownloadClients(ctx context.Context) ([]healthcheck.EnumeratedDownloadClient, error) {
	a.mu.RLock()
	registry := a.registry
	deps := a.deps
	a.mu.RUnlock()

	rows, err := a.repo.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("downloadclients.All: %w", err)
	}
	out := make([]healthcheck.EnumeratedDownloadClient, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		if !row.Enable {
			continue
		}
		entry := healthcheck.EnumeratedDownloadClient{
			Name:           row.Name,
			Implementation: row.Implementation,
		}
		if registry == nil {
			entry.Err = fmt.Errorf("downloadclients: registry not yet wired")
			out = append(out, entry)
			continue
		}
		provider, err := registry.Construct(row, deps)
		if err != nil {
			entry.Err = err
			out = append(out, entry)
			continue
		}
		status, err := provider.GetStatus(ctx)
		if err != nil {
			entry.Err = err
		} else {
			entry.Status = status
		}
		out = append(out, entry)
	}
	return out, nil
}

// Compile-time guards. Adapter shapes wire into the healthcheck
// constructors via Go's structural typing.
var (
	_ healthcheck.EnabledImportListLister        = importListListerAdapter{}
	_ healthcheck.AllImportListLister            = importListListerAdapter{}
	_ healthcheck.ImportListRootFolderPathLister = rootFolderPathListerAdapter{}
	_ healthcheck.SkyHookProber                  = skyHookProberAdapter{}
	_ healthcheck.ProxyCheckConfig               = proxyHostConfigAdapter{}
	_ healthcheck.RssEnabledLister               = rssIndexerCountAdapter{}
	_ healthcheck.SearchEnabledLister            = searchIndexerCountAdapter{}
	_ healthcheck.DownloadClientLister           = enabledDownloadClientCountAdapter{}
	_ healthcheck.DownloadClientStatusEnumerator = (*downloadClientStatusEnumeratorAdapter)(nil)
)
