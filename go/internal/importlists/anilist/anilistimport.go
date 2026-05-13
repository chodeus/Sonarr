package anilist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"sonarr.tv/sonarr/internal/importlists"
)

// statusRecorder is the narrow surface the provider needs from the
// orchestrator's importliststatus.Service.
type statusRecorder interface {
	RecordSuccess(ctx context.Context, providerID int) error
	RecordFailure(ctx context.Context, providerID int, minimumBackOff time.Duration) error
}

// refreshSkew is the renewal window — C# `:78` refreshes when
// Expires < UtcNow.AddMinutes(5).
const refreshSkew = 5 * time.Minute

// pageHardCap caps the number of pages fetched per Fetch call. Sonarr
// has no equivalent cap in C# but a misbehaving AniList endpoint that
// always returns HasNextPage=true could otherwise loop forever. 100
// pages × 50 items = 5000 anime entries, well above any realistic
// user list.
const pageHardCap = 100

// AniListImport ports the C# `AniListImport` concrete import-list at
// `AniList/List/AniListImport.cs:20-216` + the `AniListImportBase`
// fetch/refresh machinery at `AniListImportBase.cs:67-110`.
//
// Goroutine-safety: settings mutate on refresh; settingsMu serialises
// concurrent Fetch / Test access.
type AniListImport struct {
	def        *importlists.ImportList
	settings   Settings
	settingsMu sync.Mutex
	doer       httpDoer
	status     statusRecorder
	logger     *slog.Logger
	now        func() time.Time
}

// Compile-time assertion: *AniListImport satisfies importlists.Provider.
var _ importlists.Provider = (*AniListImport)(nil)

// New constructs an *AniListImport from a persisted [importlists.ImportList]
// row + the cross-provider [importlists.Dependencies] bundle.
func New(def *importlists.ImportList, deps importlists.Dependencies) (importlists.Provider, error) {
	if def == nil {
		return nil, errors.New("anilist: nil definition")
	}
	settings := DefaultSettings()
	if def.Settings != nil && *def.Settings != "" {
		if err := json.Unmarshal([]byte(*def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("anilist: unmarshal settings: %w", err)
		}
	}
	doer, _ := deps.HTTPClient.(httpDoer)
	status, _ := deps.StatusService.(statusRecorder)
	logger, _ := deps.Logger.(*slog.Logger)
	if logger == nil {
		logger = slog.Default()
	}
	return &AniListImport{
		def:      def,
		settings: settings,
		doer:     doer,
		status:   status,
		logger:   logger,
		now:      time.Now,
	}, nil
}

// Name returns the provider's display label. Mirrors C# `:33`.
func (a *AniListImport) Name() string { return "AniList" }

// Definition returns the persisted row.
func (a *AniListImport) Definition() *importlists.ImportList { return a.def }

// ListType reports the provider's UI category. Mirrors C# `:15`.
func (a *AniListImport) ListType() importlists.ListType { return importlists.ListTypeOther }

// MinRefreshInterval is 12 hours. Mirrors C# `:16`.
func (a *AniListImport) MinRefreshInterval() time.Duration { return 12 * time.Hour }

// refreshIfNeeded refreshes the access token if Expires is within the
// 5-minute renewal window. Mirrors C# `:73-109`. Refresh-back-to-DB
// is DEFERRED — in-memory only.
// settingsMu MUST be held by the caller.
func (a *AniListImport) refreshIfNeeded(ctx context.Context) error {
	if a.settings.RefreshToken == "" {
		return nil
	}
	if a.settings.Expires.After(a.now().Add(refreshSkew)) {
		return nil
	}
	tok, err := refreshAuthToken(ctx, a.doer, a.settings.RefreshToken)
	if err != nil {
		return err
	}
	if tok == nil {
		return nil
	}
	a.settings.AccessToken = tok.AccessToken
	a.settings.Expires = a.now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	if tok.RefreshToken != "" {
		a.settings.RefreshToken = tok.RefreshToken
	}
	return nil
}

// Fetch refreshes the token (if needed) then paginates the AniList
// QueryList GraphQL endpoint, page by page, accumulating + filtering
// items until HasNextPage=false.
//
// Per-item filtering rules (matches C# `AniListParser`):
//   - Server-side `status_in` filter on MediaListStatus (the 6
//     ImportCurrent/Planning/Completed/Dropped/Paused/Repeating
//     toggles).
//   - Client-side `media.status` filter on the 5 ImportFinished/
//     Releasing/Unreleased/Cancelled/Hiatus toggles.
//
// Hard failure (transport / non-2xx / unmarshal) records a
// RecordFailure + sets AnyFailure=true; the FetchResult is still
// returned with a nil error.
func (a *AniListImport) Fetch(ctx context.Context) (importlists.FetchResult, error) {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	out := importlists.FetchResult{}
	if err := a.refreshIfNeeded(ctx); err != nil {
		out.AnyFailure = true
		a.logger.Debug("anilist refresh failed", "list", definitionName(a.def), "err", err)
		a.recordFailure(ctx)
		return out, nil
	}
	listID := definitionID(a.def)
	listName := definitionName(a.def)
	var items []importlists.ItemInfo
	for page := 1; page <= pageHardCap; page++ {
		resp, err := fetchPage(ctx, a.doer, &a.settings, page)
		if err != nil {
			out.AnyFailure = true
			a.logger.Debug("anilist fetch failed", "list", listName, "page", page, "err", err)
			a.recordFailure(ctx)
			return out, nil
		}
		for _, item := range resp.Data.Page.MediaList {
			if !a.settings.allowMediaStatus(item.Media.Status) {
				continue
			}
			title := item.Media.Title.UserPreferred
			if title == "" {
				title = item.Media.Title.UserRomaji
			}
			items = append(items, importlists.ItemInfo{
				ImportListID: listID,
				ImportList:   listName,
				Title:        title,
				AniListID:    item.Media.ID,
			})
		}
		if !resp.Data.Page.PageInfo.HasNextPage {
			break
		}
	}
	out.Series = importlists.Dedup(items)
	a.recordSuccess(ctx)
	return out, nil
}

// Test paginates as well — the C# TestConnection at `:159-215` keeps
// fetching pages as long as the page is empty AND HasNextPage=true,
// to handle the case where every entry on the first page is filtered
// out client-side. Returns an empty-results warning when the full
// pagination yields zero entries.
func (a *AniListImport) Test(ctx context.Context) ([]importlists.TestFailure, error) {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	if v := a.settings.Validate(); len(v) > 0 {
		return v, nil
	}
	if err := a.refreshIfNeeded(ctx); err != nil {
		return []importlists.TestFailure{{
			PropertyName:        "SignIn",
			ErrorMessage:        fmt.Sprintf("Unable to refresh token: %v", err),
			DetailedDescription: err.Error(),
		}}, nil
	}
	hasResults := false
	for page := 1; page <= pageHardCap; page++ {
		resp, err := fetchPage(ctx, a.doer, &a.settings, page)
		if err != nil {
			if errors.Is(err, ErrUnauthorized) {
				return []importlists.TestFailure{{PropertyName: "SignIn", ErrorMessage: "Access token is invalid; reauthenticate with AniList"}}, nil
			}
			return []importlists.TestFailure{{
				PropertyName:        "BaseUrl",
				ErrorMessage:        fmt.Sprintf("Unable to fetch list: %v", err),
				DetailedDescription: err.Error(),
			}}, nil
		}
		for _, item := range resp.Data.Page.MediaList {
			if a.settings.allowMediaStatus(item.Media.Status) {
				hasResults = true
				break
			}
		}
		if hasResults || !resp.Data.Page.PageInfo.HasNextPage {
			break
		}
	}
	if !hasResults {
		return []importlists.TestFailure{{
			PropertyName: "",
			ErrorMessage: "No results were returned from your AniList import list, please check your settings.",
		}}, nil
	}
	return nil, nil
}

// RequestAction is a no-op — the OAuth handshake admin path is DEFERRED.
func (a *AniListImport) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return struct{}{}, nil
}

func (a *AniListImport) recordSuccess(ctx context.Context) {
	if a.status == nil || a.def == nil || a.def.ID == 0 {
		return
	}
	_ = a.status.RecordSuccess(ctx, a.def.ID)
}

func (a *AniListImport) recordFailure(ctx context.Context) {
	if a.status == nil || a.def == nil || a.def.ID == 0 {
		return
	}
	_ = a.status.RecordFailure(ctx, a.def.ID, 0)
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

// init registers the AniListImport constructor with the package-global
// [importlists.Default] registry.
func init() {
	importlists.Default().MustRegister(Implementation, ConfigContract, New)
}
