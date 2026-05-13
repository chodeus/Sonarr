package plexrss

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"sonarr.tv/sonarr/internal/importlists"
)

// statusRecorder is the narrow surface the provider needs from the
// orchestrator's importliststatus.Service.
type statusRecorder interface {
	RecordSuccess(ctx context.Context, providerID int) error
	RecordFailure(ctx context.Context, providerID int, minimumBackOff time.Duration) error
}

// PlexRssImport ports the C# `PlexRssImport` concrete import-list at
// `ImportLists/Rss/Plex/PlexRssImport.cs:10-30`.
//
// Goroutine-safety: settings is captured by value at construction
// time. The proxy is stateless.
type PlexRssImport struct {
	def      *importlists.ImportList
	settings Settings
	doer     httpDoer
	status   statusRecorder
	logger   *slog.Logger
}

// Compile-time assertion: *PlexRssImport satisfies importlists.Provider.
var _ importlists.Provider = (*PlexRssImport)(nil)

// New constructs a *PlexRssImport from a persisted [importlists.ImportList]
// row + the cross-provider [importlists.Dependencies] bundle.
func New(def *importlists.ImportList, deps importlists.Dependencies) (importlists.Provider, error) {
	if def == nil {
		return nil, errors.New("plexrss: nil definition")
	}
	settings := DefaultSettings()
	if def.Settings != nil && *def.Settings != "" {
		if err := json.Unmarshal([]byte(*def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("plexrss: unmarshal settings: %w", err)
		}
	}
	doer, _ := deps.HTTPClient.(httpDoer)

	status, _ := deps.StatusService.(statusRecorder)

	logger, _ := deps.Logger.(*slog.Logger)
	if logger == nil {
		logger = slog.Default()
	}

	return &PlexRssImport{
		def:      def,
		settings: settings,
		doer:     doer,
		status:   status,
		logger:   logger,
	}, nil
}

// Name returns the provider's display label. Mirrors C# `:12`.
func (p *PlexRssImport) Name() string { return "Plex Watchlist (RSS)" }

// Definition returns the persisted row.
func (p *PlexRssImport) Definition() *importlists.ImportList { return p.def }

// ListType reports the provider's UI category. Mirrors C# `:13`.
func (p *PlexRssImport) ListType() importlists.ListType { return importlists.ListTypePlex }

// MinRefreshInterval is 6 hours. Mirrors C# `:14`.
func (p *PlexRssImport) MinRefreshInterval() time.Duration { return 6 * time.Hour }

// Fetch executes the per-poll RSS retrieval + parse + stamp + dedup.
// Mirrors C# `RssImportBase.Fetch` → `FetchItems` at `:27-30` +
// `PlexRssImportParser.ProcessItem` at `:21-63`.
//
// Hard failure (transport / non-2xx / XML parse error) records a
// RecordFailure + sets AnyFailure=true; the FetchResult is still
// returned with a nil error so the orchestrator's fan-out stays
// resilient (mirrors C# try/catch swallow at the ImportListBase
// fetch wrapper).
func (p *PlexRssImport) Fetch(ctx context.Context) (importlists.FetchResult, error) {
	out := importlists.FetchResult{}
	body, err := fetchFeed(ctx, p.doer, &p.settings)
	if err != nil {
		out.AnyFailure = true
		p.logger.Debug("plexrss fetch failed", "list", definitionName(p.def), "err", err)
		p.recordFailure(ctx)
		return out, nil
	}
	items, err := parseFeed(body)
	if err != nil {
		out.AnyFailure = true
		p.logger.Debug("plexrss parse failed", "list", definitionName(p.def), "err", err)
		p.recordFailure(ctx)
		return out, nil
	}
	listID := definitionID(p.def)
	listName := definitionName(p.def)
	for i := range items {
		items[i].ImportListID = listID
		items[i].ImportList = listName
	}
	out.Series = importlists.Dedup(items)
	p.recordSuccess(ctx)
	return out, nil
}

// Test verifies the configured URL by attempting a one-off fetch.
// Maps the distinguished sentinels to per-property failures:
//   - ErrUnauthorized => "Url" property failure.
//   - any other transport / non-2xx => "Url" property failure.
//   - Settings.Validate failures pass through directly.
func (p *PlexRssImport) Test(ctx context.Context) ([]importlists.TestFailure, error) {
	var failures []importlists.TestFailure
	if v := p.settings.Validate(); len(v) > 0 {
		failures = append(failures, v...)
		return failures, nil
	}
	body, err := fetchFeed(ctx, p.doer, &p.settings)
	if err != nil {
		if errors.Is(err, ErrUnauthorized) {
			failures = append(failures, importlists.TestFailure{
				PropertyName: "Url",
				ErrorMessage: "Authentication failed for the configured URL.",
			})
			return failures, nil
		}
		failures = append(failures, importlists.TestFailure{
			PropertyName:        "Url",
			ErrorMessage:        fmt.Sprintf("Unable to fetch RSS feed: %v", err),
			DetailedDescription: err.Error(),
		})
		return failures, nil
	}
	if _, err := parseFeed(body); err != nil {
		failures = append(failures, importlists.TestFailure{
			PropertyName:        "Url",
			ErrorMessage:        fmt.Sprintf("Response is not a valid RSS feed: %v", err),
			DetailedDescription: err.Error(),
		})
	}
	return failures, nil
}

// RequestAction is a no-op — PlexRssImport inherits the
// `ImportListBase.RequestAction` default that returns `new {}`.
func (p *PlexRssImport) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return struct{}{}, nil
}

func (p *PlexRssImport) recordSuccess(ctx context.Context) {
	if p.status == nil || p.def == nil || p.def.ID == 0 {
		return
	}
	_ = p.status.RecordSuccess(ctx, p.def.ID)
}

func (p *PlexRssImport) recordFailure(ctx context.Context) {
	if p.status == nil || p.def == nil || p.def.ID == 0 {
		return
	}
	_ = p.status.RecordFailure(ctx, p.def.ID, 0)
}

func definitionID(def *importlists.ImportList) int {
	if def == nil {
		return 0
	}
	return def.ID
}

func definitionName(def *importlists.ImportList) string {
	if def == nil {
		return ""
	}
	return def.Name
}

// init registers the PlexRssImport constructor with the package-global
// [importlists.Default] registry.
func init() {
	importlists.Default().MustRegister(Implementation, ConfigContract, New)
}
