package plex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"sonarr.tv/sonarr/internal/importlists"
)

// minRefreshInterval ports C# `MinRefreshInterval =>
// TimeSpan.FromHours(6)` at `PlexImport.cs:19`.
const minRefreshInterval = 6 * time.Hour

// PageSize ports C# `PageSize => 100` at `PlexImport.cs:21`.
const PageSize = 100

// MaxPages ports C# `PlexListRequestGenerator.MaxPages = 10` at
// `PlexListRequestGenerator.cs:8`. The silent 1000-item cap
// (PageSize * MaxPages) is intentional — see the analysis doc at
// go/analysis/providers/import-lists.md (IL-Q7).
const MaxPages = 10

// statusRecorder is the narrow interface PlexImport needs from
// the deps.StatusService bundle. Same shape as the
// internal/importlists/sonarr.statusRecorder.
type statusRecorder interface {
	RecordSuccess(ctx context.Context, providerID int) error
	RecordFailure(ctx context.Context, providerID int, minimumBackOff time.Duration) error
}

// configClientIdentifier is the narrow interface the package
// queries deps.ConfigProvider against to fetch the per-install
// Plex client GUID. Nil-tolerated — the proxy falls back to
// "sonarr" when the dep is nil or the method returns "".
type configClientIdentifier interface {
	PlexClientIdentifier() string
}

// plexHostsProvider is the optional narrow interface
// deps.ConfigProvider can additionally implement to override the
// three plex.tv host endpoints. Production wiring does not
// implement this (real plex.tv hosts are the defaults); tests
// satisfy it via a stub that points at httptest.Server.
type plexHostsProvider interface {
	PlexHosts() (plexTv, plexTvAuth, plexTvDiscover string)
}

// PlexImport ports the C# `PlexImport` concrete import-list at
// `ImportLists/Plex/PlexImport.cs:15-119`.
//
// The C# class extends `HttpImportListBase<PlexListSettings>` —
// pagination, request-chain execution, and the per-page Parser
// hook live in the base. The Go port inlines the pagination loop
// directly into Fetch since we have only the two concrete
// providers and the pageable-request abstraction would add weight
// without per-family payoff.
//
// Goroutine-safety: settings captured by value at construction
// time. Proxy is stateless. Concurrent Fetch / Test /
// RequestAction calls are safe.
type PlexImport struct {
	def      *importlists.ImportList
	settings Settings
	proxy    *Proxy
	status   statusRecorder
	logger   *slog.Logger
}

// Compile-time assertion: *PlexImport satisfies importlists.Provider.
var _ importlists.Provider = (*PlexImport)(nil)

// New constructs a *PlexImport from a persisted [importlists.ImportList]
// row + the cross-provider [importlists.Dependencies] bundle.
// Mirrors the [importlists.Constructor] signature.
//
// Settings JSON deserialisation: nil-or-empty Settings string
// deserialises into the DefaultSettings shape (mirrors C# `new
// PlexListSettings()`).
//
// HTTPClient dep optional — falls back to http.DefaultClient
// (matches the SonarrImport precedent).
//
// ConfigProvider dep optional — when nil OR when
// PlexClientIdentifier() returns "", the proxy uses the "sonarr"
// fallback (Plex does not strictly validate the value).
//
// StatusService dep optional — when nil, Fetch skips the
// RecordSuccess / RecordFailure calls.
func New(def *importlists.ImportList, deps importlists.Dependencies) (importlists.Provider, error) {
	if def == nil {
		return nil, errors.New("plex-import: nil definition")
	}
	settings := DefaultSettings()
	if def.Settings != nil && *def.Settings != "" {
		if err := json.Unmarshal([]byte(*def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("plex-import: unmarshal settings: %w", err)
		}
	}

	doer, _ := deps.HTTPClient.(httpDoer)

	var clientID string
	if cfg, ok := deps.ConfigProvider.(configClientIdentifier); ok && cfg != nil {
		clientID = cfg.PlexClientIdentifier()
	}

	var opts []ProxyOption
	if cfg, ok := deps.ConfigProvider.(plexHostsProvider); ok && cfg != nil {
		opts = append(opts, WithHosts(cfg.PlexHosts()))
	}
	proxy := NewProxy(doer, clientID, "", "", opts...)

	status, _ := deps.StatusService.(statusRecorder)

	logger, _ := deps.Logger.(*slog.Logger)
	if logger == nil {
		logger = slog.Default()
	}

	return &PlexImport{
		def:      def,
		settings: settings,
		proxy:    proxy,
		status:   status,
		logger:   logger,
	}, nil
}

// Name returns the provider's display label. Mirrors C# `Name`
// at `PlexImport.cs:17` (the C# code looks up
// "ImportListsPlexSettingsWatchlistName" from the localisation
// service; the Go port uses the English template "Plex Watchlist"
// since the localisation service is not yet wired here).
func (p *PlexImport) Name() string { return "Plex Watchlist" }

// Definition returns the persisted row. Mirrors C# `Definition`
// at `ImportListBase.cs:74`.
func (p *PlexImport) Definition() *importlists.ImportList { return p.def }

// ListType reports the provider category. Mirrors C# `ListType =>
// ImportListType.Plex` at `PlexImport.cs:18`.
func (p *PlexImport) ListType() importlists.ListType { return importlists.ListTypePlex }

// MinRefreshInterval returns the smallest sync cadence. Mirrors C#
// `MinRefreshInterval => TimeSpan.FromHours(6)` at
// `PlexImport.cs:19`.
func (p *PlexImport) MinRefreshInterval() time.Duration { return minRefreshInterval }

// Fetch ports C# `PlexImport.Fetch` at `PlexImport.cs:38-43` plus
// the inlined HttpImportListBase.FetchItems pagination behaviour
// the request-chain generator at `PlexListRequestGenerator.cs:21-36`
// expresses.
//
// Pagination loop: requests pages 0..MaxPages-1 with PageSize=100.
// Stops early when an empty page comes back (the C# request-chain
// generator yields all 10 unconditionally, but the FetchItems
// consumer in HttpImportListBase short-circuits on empty pages;
// the Go port surfaces the same effective behaviour via the
// explicit early exit + saves the HTTP round-trips).
//
// Validate-before-fetch: C# `:40` runs Settings.Validate filtered
// to AccessToken before paging. The Go port does the same — a
// missing AccessToken short-circuits with AnyFailure=true (no HTTP
// calls).
func (p *PlexImport) Fetch(ctx context.Context) (importlists.FetchResult, error) {
	if failures := p.settings.Validate(); len(failures) > 0 {
		if p.logger != nil {
			p.logger.Debug("plex-import: fetch aborted, settings invalid",
				slog.String("list", p.def.Name),
				slog.String("reason", failures[0].ErrorMessage))
		}
		if p.status != nil {
			_ = p.status.RecordFailure(ctx, p.def.ID, 0)
		}
		return importlists.FetchResult{Series: []importlists.ItemInfo{}, AnyFailure: true}, nil
	}

	combined := make([]importlists.ItemInfo, 0, PageSize)
	for page := range MaxPages {
		container, err := p.proxy.GetWatchlist(ctx, p.settings.AccessToken, PageSize, page*PageSize)
		if err != nil {
			if p.logger != nil {
				p.logger.Debug("plex-import: page fetch failed",
					slog.String("list", p.def.Name),
					slog.Int("page", page),
					slog.Any("err", err))
			}
			if p.status != nil {
				_ = p.status.RecordFailure(ctx, p.def.ID, 0)
			}
			return importlists.FetchResult{Series: []importlists.ItemInfo{}, AnyFailure: true}, nil
		}
		if len(container.Items) == 0 {
			break
		}
		combined = append(combined, parseWatchlistItems(container.Items)...)
	}

	cleaned := importlists.Dedup(combined)
	for i := range cleaned {
		cleaned[i].ImportListID = p.def.ID
		cleaned[i].ImportList = p.def.Name
	}

	if p.status != nil {
		_ = p.status.RecordSuccess(ctx, p.def.ID)
	}
	return importlists.FetchResult{Series: cleaned, AnyFailure: false}, nil
}

// Test ports the C# `ImportListBase.Test()` wrapper at
// `ImportListBase.cs:98-113`. The Plex C# code does not override
// per-provider Test — it relies on the HttpImportListBase default
// (which issues a sample Fetch and converts any thrown exception
// to a generic failure). The Go port mirrors by validating the
// settings inline + issuing a single-page GetWatchlist; any
// proxy error maps to a generic test failure with PropertyName="".
func (p *PlexImport) Test(ctx context.Context) ([]importlists.TestFailure, error) {
	if failures := p.settings.Validate(); len(failures) > 0 {
		return failures, nil
	}
	if _, err := p.proxy.GetWatchlist(ctx, p.settings.AccessToken, 1, 0); err != nil {
		return []importlists.TestFailure{{
			PropertyName: "",
			ErrorMessage: fmt.Sprintf("Unable to connect to import list: %v", err),
		}}, nil
	}
	return nil, nil
}

// RequestAction ports C# `PlexImport.RequestAction` at
// `PlexImport.cs:55-118`. Handles the 4 OAuth-handshake actions
// the v3 UI invokes through the SignIn button:
//
//   - startOAuth → returns the PinURLResponse the UI POSTs against
//     to receive a pin id + code.
//   - continueOAuth → builds the auth.plex.tv SignInURLResponse
//     the UI redirects the browser to. Requires callbackUrl + id +
//     code query params; missing params return [ErrBadRequest].
//   - pollOAuth → checks the pin redemption status by polling
//     /api/v2/pins/{pinId}; returns {success: bool}. Requires
//     pinId query param.
//   - getOAuthToken → resolves the pinId to its accessToken once
//     redemption completes; returns {accessToken: string}.
//     Requires pinId query param.
//
// Unknown actions return `{}` (mirrors C# `:117`).
//
// C# `:59/65/86/102` invokes Settings.Validate().Filter("ConsumerKey",
// "ConsumerSecret").ThrowOnError(). PlexListSettings does NOT have
// ConsumerKey/ConsumerSecret fields, so this filter never produces
// failures in practice — the Go port omits this no-op rather than
// porting a vestigial guard.
func (p *PlexImport) RequestAction(ctx context.Context, action string, query map[string]string) (any, error) {
	switch action {
	case "startOAuth":
		return p.proxy.GetPinUrl(ctx), nil
	case "continueOAuth":
		callbackURL := query["callbackUrl"]
		id := query["id"]
		code := query["code"]
		if callbackURL == "" {
			return nil, fmt.Errorf("%w: callbackUrl invalid", ErrBadRequest)
		}
		if id == "" {
			return nil, fmt.Errorf("%w: id invalid", ErrBadRequest)
		}
		if code == "" {
			return nil, fmt.Errorf("%w: code invalid", ErrBadRequest)
		}
		pinID, err := strconv.Atoi(id)
		if err != nil {
			return nil, fmt.Errorf("%w: id not an int: %w", ErrBadRequest, err)
		}
		return p.proxy.GetSignInUrl(ctx, callbackURL, pinID, code), nil
	case "pollOAuth":
		pinIDStr := query["pinId"]
		if pinIDStr == "" {
			return nil, fmt.Errorf("%w: pinId invalid", ErrBadRequest)
		}
		pinID, err := strconv.Atoi(pinIDStr)
		if err != nil {
			return nil, fmt.Errorf("%w: pinId not an int: %w", ErrBadRequest, err)
		}
		authToken, err := p.proxy.GetAuthToken(ctx, pinID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"success": authToken != ""}, nil
	case "getOAuthToken":
		pinIDStr := query["pinId"]
		if pinIDStr == "" {
			return nil, fmt.Errorf("%w: pinId invalid", ErrBadRequest)
		}
		pinID, err := strconv.Atoi(pinIDStr)
		if err != nil {
			return nil, fmt.Errorf("%w: pinId not an int: %w", ErrBadRequest, err)
		}
		accessToken, err := p.proxy.GetAuthToken(ctx, pinID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"accessToken": accessToken}, nil
	default:
		return map[string]any{}, nil
	}
}
