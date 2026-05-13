package custom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"sonarr.tv/sonarr/internal/importlists"
)

// statusRecorder is the narrow surface the provider needs from the
// orchestrator's importliststatus.Service. Mirrors the
// [internal/importlists/sonarr.statusRecorder] precedent.
type statusRecorder interface {
	RecordSuccess(ctx context.Context, providerID int) error
	RecordFailure(ctx context.Context, providerID int, minimumBackOff time.Duration) error
}

// CustomImport ports the C# `CustomImport` concrete import-list at
// `ImportLists/Custom/CustomImport.cs:13-76`.
//
// Goroutine-safety: settings is captured by value at construction
// time; the proxy is stateless. Concurrent Fetch / Test calls are
// safe.
type CustomImport struct {
	def      *importlists.ImportList
	settings Settings
	proxy    *Proxy
	status   statusRecorder
	logger   *slog.Logger
}

// Compile-time assertion: *CustomImport satisfies importlists.Provider.
var _ importlists.Provider = (*CustomImport)(nil)

// New constructs a *CustomImport from a persisted [importlists.ImportList]
// row + the cross-provider [importlists.Dependencies] bundle.
// Mirrors the [importlists.Constructor] signature + the
// [internal/importlists/sonarr].New precedent.
func New(def *importlists.ImportList, deps importlists.Dependencies) (importlists.Provider, error) {
	if def == nil {
		return nil, errors.New("custom-import: nil definition")
	}
	settings := DefaultSettings()
	if def.Settings != nil && *def.Settings != "" {
		if err := json.Unmarshal([]byte(*def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("custom-import: unmarshal settings: %w", err)
		}
	}
	doer, _ := deps.HTTPClient.(httpDoer)
	proxy := NewProxy(doer)

	status, _ := deps.StatusService.(statusRecorder)

	logger, _ := deps.Logger.(*slog.Logger)
	if logger == nil {
		logger = slog.Default()
	}

	return &CustomImport{
		def:      def,
		settings: settings,
		proxy:    proxy,
		status:   status,
		logger:   logger,
	}, nil
}

// Name returns the provider's display label. Mirrors C# `:16`.
func (c *CustomImport) Name() string { return "Custom List" }

// Definition returns the persisted row.
func (c *CustomImport) Definition() *importlists.ImportList { return c.def }

// ListType reports the provider's UI category. Mirrors C# `:20`.
func (c *CustomImport) ListType() importlists.ListType { return importlists.ListTypeAdvanced }

// MinRefreshInterval is 6 hours. Mirrors C# `:18`.
func (c *CustomImport) MinRefreshInterval() time.Duration { return 6 * time.Hour }

// Fetch executes the per-poll list retrieval. Mirrors C#
// `CustomImport.Fetch` at `:33-64`.
//
// Flow:
//  1. Proxy.GetSeries reads the JSON list from BaseURL.
//  2. Each remote Series maps to an [importlists.ItemInfo] — the
//     C# code at `:42-50` titles fall-back to "TvdbId: {id}" when
//     the remote omits Title.
//  3. ImportListID + ImportList stamps mirror C# `CleanupListItems`
//     at `ImportListBase.cs:85-96`.
//  4. RecordSuccess / RecordFailure stamps the status service —
//     mirrors C# `:53 / :60`.
//  5. Dedup runs last per the C# CleanupListItems contract.
//
// A hard failure (proxy returning a non-nil error) records a
// RecordFailure + propagates AnyFailure=true; the C# code SWALLOWS
// the error and still returns a FetchResult — the Go port matches
// that semantic to keep the orchestrator's fan-out resilient.
func (c *CustomImport) Fetch(ctx context.Context) (importlists.FetchResult, error) {
	out := importlists.FetchResult{}
	remote, err := c.proxy.GetSeries(ctx, &c.settings)
	if err != nil {
		out.AnyFailure = true
		c.logger.Debug("custom-import fetch failed", "list", definitionName(c.def), "err", err)
		c.recordFailure(ctx)
		return out, nil
	}

	items := make([]importlists.ItemInfo, 0, len(remote))
	for _, r := range remote {
		title := r.Title
		if title == "" {
			title = "TvdbId: " + strconv.Itoa(r.TvdbID)
		}
		items = append(items, importlists.ItemInfo{
			ImportListID: definitionID(c.def),
			ImportList:   definitionName(c.def),
			Title:        title,
			TvdbID:       r.TvdbID,
			TmdbID:       r.TmdbID,
			ImdbID:       r.ImdbID,
		})
	}
	out.Series = importlists.Dedup(items)
	c.recordSuccess(ctx)
	return out, nil
}

// Test verifies the remote endpoint. Mirrors C# `CustomImport.Test`
// at `:71-74` + the proxy `Test` body at `:41-65`.
//
// Maps the distinguished sentinels to per-property failures:
//   - ErrUnauthorized => "BaseUrl" property failure.
//   - any other transport / non-2xx => "BaseUrl" property failure.
//   - Settings.Validate failures pass through directly.
func (c *CustomImport) Test(ctx context.Context) ([]importlists.TestFailure, error) {
	var failures []importlists.TestFailure
	if v := c.settings.Validate(); len(v) > 0 {
		failures = append(failures, v...)
		return failures, nil
	}
	if _, err := c.proxy.GetSeries(ctx, &c.settings); err != nil {
		if errors.Is(err, ErrUnauthorized) {
			failures = append(failures, importlists.TestFailure{
				PropertyName: "BaseUrl",
				ErrorMessage: "Authentication failed for the configured URL.",
			})
			return failures, nil
		}
		failures = append(failures, importlists.TestFailure{
			PropertyName:        "BaseUrl",
			ErrorMessage:        fmt.Sprintf("Unable to connect to the configured URL: %v", err),
			DetailedDescription: err.Error(),
		})
	}
	return failures, nil
}

// RequestAction is a no-op for Custom — C# returns `new {}` per
// `:66-69`.
func (c *CustomImport) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return struct{}{}, nil
}

// recordSuccess fires the status recorder when wired. Mirrors C#
// `_importListStatusService.RecordSuccess(Definition.Id)` at `:53`.
func (c *CustomImport) recordSuccess(ctx context.Context) {
	if c.status == nil || c.def == nil || c.def.ID == 0 {
		return
	}
	_ = c.status.RecordSuccess(ctx, c.def.ID)
}

// recordFailure fires the status recorder with a zero back-off
// (the orchestrator may escalate). Mirrors C#
// `_importListStatusService.RecordFailure(Definition.Id)` at `:60`.
func (c *CustomImport) recordFailure(ctx context.Context) {
	if c.status == nil || c.def == nil || c.def.ID == 0 {
		return
	}
	_ = c.status.RecordFailure(ctx, c.def.ID, 0)
}

// definitionID returns def.ID safely with nil-tolerance.
func definitionID(def *importlists.ImportList) int {
	if def == nil {
		return 0
	}
	return def.ID
}

// definitionName returns def.Name safely with nil-tolerance.
func definitionName(def *importlists.ImportList) string {
	if def == nil {
		return ""
	}
	return def.Name
}

// init registers the CustomImport constructor with the package-global
// [importlists.Default] registry.
func init() {
	importlists.Default().MustRegister(Implementation, ConfigContract, New)
}

// Compile-time check that the package depends on net/http only via
// the doer interface — keeps the dep graph thin. The unused import
// pin keeps go-imports from removing the surface.
var _ = http.DefaultClient
