package myanimelist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
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

// refreshSkew is the renewal window — C# `MyAnimeListImport.Fetch`
// refreshes when Expires < UtcNow.AddMinutes(5).
const refreshSkew = 5 * time.Minute

// settingsPersister is the narrow Update surface the provider needs
// from the importlists.Repo. Satisfied by *importlists.Repo via its
// embedded repo.Repository[ImportList].Update method.
type settingsPersister interface {
	Update(ctx context.Context, def *importlists.ImportList) error
}

// MyAnimeListImport ports the C# `MyAnimeListImport` concrete
// import-list at `ImportLists/MyAnimeList/MyAnimeListImport.cs:14-121`.
//
// Goroutine-safety: settings mutates on refresh; settingsMu serialises
// concurrent Fetch / Test access.
type MyAnimeListImport struct {
	def        *importlists.ImportList
	settings   Settings
	settingsMu sync.Mutex
	doer       httpDoer
	status     statusRecorder
	persister  settingsPersister
	logger     *slog.Logger
	now        func() time.Time
}

// Compile-time assertion: *MyAnimeListImport satisfies importlists.Provider.
var _ importlists.Provider = (*MyAnimeListImport)(nil)

// New constructs a *MyAnimeListImport from a persisted [importlists.ImportList]
// row + the cross-provider [importlists.Dependencies] bundle.
func New(def *importlists.ImportList, deps importlists.Dependencies) (importlists.Provider, error) {
	if def == nil {
		return nil, errors.New("myanimelist: nil definition")
	}
	settings := DefaultSettings()
	if def.Settings != nil && *def.Settings != "" {
		if err := json.Unmarshal([]byte(*def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("myanimelist: unmarshal settings: %w", err)
		}
	}
	doer, _ := deps.HTTPClient.(httpDoer)
	status, _ := deps.StatusService.(statusRecorder)
	persister, _ := deps.ImportListsRepo.(settingsPersister)
	logger, _ := deps.Logger.(*slog.Logger)
	if logger == nil {
		logger = slog.Default()
	}
	return &MyAnimeListImport{
		def:       def,
		settings:  settings,
		doer:      doer,
		status:    status,
		persister: persister,
		logger:    logger,
		now:       time.Now,
	}, nil
}

// Name returns the provider's display label. Mirrors C# `:20`.
func (m *MyAnimeListImport) Name() string { return "MyAnimeList" }

// Definition returns the persisted row.
func (m *MyAnimeListImport) Definition() *importlists.ImportList { return m.def }

// ListType reports the provider's UI category. Mirrors C# `:21`.
func (m *MyAnimeListImport) ListType() importlists.ListType { return importlists.ListTypeOther }

// MinRefreshInterval is 6 hours. Mirrors C# `:22`.
func (m *MyAnimeListImport) MinRefreshInterval() time.Duration { return 6 * time.Hour }

// refreshIfNeeded refreshes the access token if it's within the
// 5-minute renewal window. Mirrors C# `Fetch:37-40` + `RefreshToken:88-119`.
// After a successful refresh the new triple is round-tripped back to
// the DB via the persister (Dependencies.ImportListsRepo) — mirrors
// C# `_importListRepository.UpdateSettings(...)` at `:111`.
//
// settingsMu MUST be held by the caller.
func (m *MyAnimeListImport) refreshIfNeeded(ctx context.Context) error {
	if m.settings.Expires.After(m.now().Add(refreshSkew)) {
		return nil
	}
	if m.settings.RefreshToken == "" {
		return fmt.Errorf("%w: refresh token is empty", ErrRequestFailed)
	}
	tok, err := refreshAuthToken(ctx, m.doer, m.settings.RefreshToken)
	if err != nil {
		return err
	}
	if tok == nil {
		return nil
	}
	m.settings.AccessToken = tok.AccessToken
	m.settings.Expires = m.now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	if tok.RefreshToken != "" {
		m.settings.RefreshToken = tok.RefreshToken
	}
	m.persistSettingsLocked(ctx)
	return nil
}

// persistSettingsLocked round-trips the in-memory Settings back to
// the DB row. Marshal failures + persister errors are swallowed
// (mirrors C# fire-and-forget UpdateSettings). C# `Definition.Id > 0`
// guard preserved.
//
// settingsMu MUST be held by the caller.
func (m *MyAnimeListImport) persistSettingsLocked(ctx context.Context) {
	if m.persister == nil || m.def == nil || m.def.ID == 0 {
		return
	}
	raw, err := json.Marshal(m.settings)
	if err != nil {
		return
	}
	s := string(raw)
	m.def.Settings = &s
	_ = m.persister.Update(ctx, m.def)
}

// Fetch refreshes the token (if needed) then walks the MAL animelist
// and converts each item into an [importlists.ItemInfo]. Items with
// list_status.Score below Settings.MinimumScore are SKIPPED.
// Mirrors C# `MyAnimeListParser.ParseResponse` at `:15-37`.
//
// Hard failure swallows the error and surfaces AnyFailure=true; the
// status recorder is fired with RecordFailure.
func (m *MyAnimeListImport) Fetch(ctx context.Context) (importlists.FetchResult, error) {
	m.settingsMu.Lock()
	defer m.settingsMu.Unlock()
	out := importlists.FetchResult{}
	if err := m.refreshIfNeeded(ctx); err != nil {
		out.AnyFailure = true
		m.logger.Debug("myanimelist refresh failed", "list", definitionName(m.def), "err", err)
		m.recordFailure(ctx)
		return out, nil
	}
	anime, err := getAnimeList(ctx, m.doer, &m.settings)
	if err != nil {
		out.AnyFailure = true
		m.logger.Debug("myanimelist fetch failed", "list", definitionName(m.def), "err", err)
		m.recordFailure(ctx)
		return out, nil
	}
	items := make([]importlists.ItemInfo, 0, len(anime))
	for _, a := range anime {
		if a.ListStatus.Score < m.settings.MinimumScore {
			continue
		}
		items = append(items, importlists.ItemInfo{
			ImportListID: definitionID(m.def),
			ImportList:   definitionName(m.def),
			Title:        a.Node.Title,
			MalID:        a.Node.ID,
		})
	}
	out.Series = importlists.Dedup(items)
	m.recordSuccess(ctx)
	return out, nil
}

// Test refreshes the token (if needed) + verifies access by listing
// the user's animelist with limit=1000. Maps ErrUnauthorized to a
// SignIn property failure per the C# validator override.
func (m *MyAnimeListImport) Test(ctx context.Context) ([]importlists.TestFailure, error) {
	m.settingsMu.Lock()
	defer m.settingsMu.Unlock()
	if v := m.settings.Validate(); len(v) > 0 {
		return v, nil
	}
	if err := m.refreshIfNeeded(ctx); err != nil {
		return []importlists.TestFailure{{
			PropertyName:        "SignIn",
			ErrorMessage:        fmt.Sprintf("Unable to refresh token: %v", err),
			DetailedDescription: err.Error(),
		}}, nil
	}
	if _, err := getAnimeList(ctx, m.doer, &m.settings); err != nil {
		if errors.Is(err, ErrUnauthorized) {
			return []importlists.TestFailure{{PropertyName: "SignIn", ErrorMessage: "Access token is invalid; reauthenticate with MyAnimeList"}}, nil
		}
		return []importlists.TestFailure{{
			PropertyName:        "BaseUrl",
			ErrorMessage:        fmt.Sprintf("Unable to fetch list: %v", err),
			DetailedDescription: err.Error(),
		}}, nil
	}
	return nil, nil
}

// RequestAction implements the C# `MyAnimeListImport.RequestAction`
// admin path at `:47-73`. Two actions:
//
//   - startOAuth — composes the MAL authorize URL through the
//     servarr cloud proxy with state = callbackUrl and redirect_uri
//     = the cloud's auth callback path. Returns `{OauthUrl: "..."}`
//     (PascalCase per C# `:59`).
//   - getOAuthToken — accepts the redirect-returned (access_token,
//     expires_in, refresh_token) triple and returns
//     `{accessToken, expires, refreshToken}` (NO authUser — MAL
//     doesn't surface a user-handle resolution per C# `:62-69`).
//
// Missing fields / non-int expires_in / zero expires_in / missing
// callbackUrl all return [ErrBadRequest]. Unknown actions return
// `{}` (mirrors C# `:72`).
func (m *MyAnimeListImport) RequestAction(_ context.Context, action string, query map[string]string) (any, error) {
	switch action {
	case "startOAuth":
		callbackURL := query["callbackUrl"]
		if callbackURL == "" {
			return nil, fmt.Errorf("%w: callbackUrl invalid", ErrBadRequest)
		}
		return map[string]any{"OauthUrl": buildOAuthAuthorizeURL(callbackURL)}, nil
	case "getOAuthToken":
		accessToken := query["access_token"]
		expiresIn := query["expires_in"]
		refreshToken := query["refresh_token"]
		if accessToken == "" {
			return nil, fmt.Errorf("%w: access_token invalid", ErrBadRequest)
		}
		if expiresIn == "" {
			return nil, fmt.Errorf("%w: expires_in invalid", ErrBadRequest)
		}
		if refreshToken == "" {
			return nil, fmt.Errorf("%w: refresh_token invalid", ErrBadRequest)
		}
		secs, err := strconv.Atoi(expiresIn)
		if err != nil || secs <= 0 {
			return nil, fmt.Errorf("%w: expires_in not a positive int", ErrBadRequest)
		}
		expires := m.now().Add(time.Duration(secs) * time.Second)
		return map[string]any{
			"accessToken":  accessToken,
			"expires":      expires,
			"refreshToken": refreshToken,
		}, nil
	default:
		return map[string]any{}, nil
	}
}

func (m *MyAnimeListImport) recordSuccess(ctx context.Context) {
	if m.status == nil || m.def == nil || m.def.ID == 0 {
		return
	}
	_ = m.status.RecordSuccess(ctx, m.def.ID)
}

func (m *MyAnimeListImport) recordFailure(ctx context.Context) {
	if m.status == nil || m.def == nil || m.def.ID == 0 {
		return
	}
	_ = m.status.RecordFailure(ctx, m.def.ID, 0)
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

// init registers the MyAnimeListImport constructor with the
// package-global [importlists.Default] registry.
func init() {
	importlists.Default().MustRegister(Implementation, ConfigContract, New)
}
