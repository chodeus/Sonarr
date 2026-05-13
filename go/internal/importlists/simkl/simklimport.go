package simkl

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

// refreshSkew is the renewal window — C# `SimklImportBase.Fetch:45`
// refreshes when Expires < UtcNow.AddMinutes(5).
const refreshSkew = 5 * time.Minute

// SimklUserImport ports the C# `SimklUserImport` concrete import-list
// at `Simkl/User/SimklUserImport.cs:9-33` + the `SimklImportBase`
// fetch/refresh machinery at `SimklImportBase.cs:39-184`.
//
// Goroutine-safety: settings mutate on refresh; settingsMu serialises
// concurrent Fetch / Test access.
type SimklUserImport struct {
	def        *importlists.ImportList
	settings   Settings
	settingsMu sync.Mutex
	doer       httpDoer
	status     statusRecorder
	logger     *slog.Logger
	now        func() time.Time
}

// Compile-time assertion: *SimklUserImport satisfies importlists.Provider.
var _ importlists.Provider = (*SimklUserImport)(nil)

// New constructs a *SimklUserImport from a persisted [importlists.ImportList]
// row + the cross-provider [importlists.Dependencies] bundle.
func New(def *importlists.ImportList, deps importlists.Dependencies) (importlists.Provider, error) {
	if def == nil {
		return nil, errors.New("simkl: nil definition")
	}
	settings := DefaultSettings()
	if def.Settings != nil && *def.Settings != "" {
		if err := json.Unmarshal([]byte(*def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("simkl: unmarshal settings: %w", err)
		}
	}
	doer, _ := deps.HTTPClient.(httpDoer)
	status, _ := deps.StatusService.(statusRecorder)
	logger, _ := deps.Logger.(*slog.Logger)
	if logger == nil {
		logger = slog.Default()
	}
	return &SimklUserImport{
		def:      def,
		settings: settings,
		doer:     doer,
		status:   status,
		logger:   logger,
		now:      time.Now,
	}, nil
}

// Name returns the provider's display label. Mirrors C# `:22`.
func (s *SimklUserImport) Name() string { return "Simkl User" }

// Definition returns the persisted row.
func (s *SimklUserImport) Definition() *importlists.ImportList { return s.def }

// ListType reports the provider's UI category. Mirrors C# `:17`.
func (s *SimklUserImport) ListType() importlists.ListType { return importlists.ListTypeSimkl }

// MinRefreshInterval is 6 hours. Mirrors C# `:18`.
func (s *SimklUserImport) MinRefreshInterval() time.Duration { return 6 * time.Hour }

// refreshIfNeeded refreshes the access token if both RefreshToken is
// set AND Expires < now+5min. Mirrors C# `:45-48`.
// settingsMu MUST be held by the caller.
func (s *SimklUserImport) refreshIfNeeded(ctx context.Context) error {
	if s.settings.RefreshToken == "" {
		return nil
	}
	if s.settings.Expires.After(s.now().Add(refreshSkew)) {
		return nil
	}
	tok, err := refreshAuthToken(ctx, s.doer, s.settings.RefreshToken)
	if err != nil {
		return err
	}
	if tok == nil {
		return nil
	}
	s.settings.AccessToken = tok.AccessToken
	s.settings.Expires = s.now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	if tok.RefreshToken != "" {
		s.settings.RefreshToken = tok.RefreshToken
	}
	return nil
}

// Fetch refreshes the token (if needed) then walks the Simkl user
// list. Mirrors C# `SimklImportBase.Fetch` at `:39-63` + the
// `SimklParser.ParseResponse` at `SimklParser.cs:21-77`.
//
// Anime entries are filtered to TV-show-like anime_type values
// (tv / ona / ova / special) per C# `:46`. Shows entries pass
// through unconditionally.
//
// The per-list LastInfoSync gate from C# `:50-58` (skip-when-no-
// activity-since-last-sync) is DEFERRED — see package doc.
func (s *SimklUserImport) Fetch(ctx context.Context) (importlists.FetchResult, error) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	out := importlists.FetchResult{}
	if err := s.refreshIfNeeded(ctx); err != nil {
		out.AnyFailure = true
		s.logger.Debug("simkl refresh failed", "list", definitionName(s.def), "err", err)
		s.recordFailure(ctx)
		return out, nil
	}
	body, err := getUserList(ctx, s.doer, &s.settings)
	if err != nil {
		out.AnyFailure = true
		s.logger.Debug("simkl fetch failed", "list", definitionName(s.def), "err", err)
		s.recordFailure(ctx)
		return out, nil
	}
	listID := definitionID(s.def)
	listName := definitionName(s.def)
	var items []importlists.ItemInfo
	if body != nil {
		items = make([]importlists.ItemInfo, 0, len(body.Shows)+len(body.Anime))
		for _, item := range body.Anime {
			if !isSupportedAnimeType(item.AnimeType) {
				continue
			}
			tvdbID, _ := strconv.Atoi(item.Show.IDs.Tvdb)
			malID, _ := strconv.Atoi(item.Show.IDs.Mal)
			if tvdbID <= 0 {
				// Mirrors C# `:46` which gates emission on a positive
				// tvdb id.
				continue
			}
			items = append(items, importlists.ItemInfo{
				ImportListID: listID,
				ImportList:   listName,
				Title:        item.Show.Title,
				TvdbID:       tvdbID,
				ImdbID:       item.Show.IDs.Imdb,
				MalID:        malID,
			})
		}
		for _, item := range body.Shows {
			tvdbID, _ := strconv.Atoi(item.Show.IDs.Tvdb)
			items = append(items, importlists.ItemInfo{
				ImportListID: listID,
				ImportList:   listName,
				Title:        item.Show.Title,
				TvdbID:       tvdbID,
				ImdbID:       item.Show.IDs.Imdb,
			})
		}
	}
	out.Series = importlists.Dedup(items)
	s.recordSuccess(ctx)
	return out, nil
}

// Test refreshes the token (if needed) + verifies access by listing
// the user's items. Maps ErrUnauthorized to a SignIn property failure
// per the C# validator override.
func (s *SimklUserImport) Test(ctx context.Context) ([]importlists.TestFailure, error) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	if v := s.settings.Validate(); len(v) > 0 {
		return v, nil
	}
	if err := s.refreshIfNeeded(ctx); err != nil {
		return []importlists.TestFailure{{
			PropertyName:        "SignIn",
			ErrorMessage:        fmt.Sprintf("Unable to refresh token: %v", err),
			DetailedDescription: err.Error(),
		}}, nil
	}
	if _, err := getUserList(ctx, s.doer, &s.settings); err != nil {
		if errors.Is(err, ErrUnauthorized) {
			return []importlists.TestFailure{{PropertyName: "SignIn", ErrorMessage: "Access token is invalid; reauthenticate with Simkl"}}, nil
		}
		return []importlists.TestFailure{{
			PropertyName:        "BaseUrl",
			ErrorMessage:        fmt.Sprintf("Unable to fetch list: %v", err),
			DetailedDescription: err.Error(),
		}}, nil
	}
	return nil, nil
}

// RequestAction is a no-op — the OAuth handshake admin path is
// DEFERRED.
func (s *SimklUserImport) RequestAction(_ context.Context, _ string, _ map[string]string) (any, error) {
	return struct{}{}, nil
}

func (s *SimklUserImport) recordSuccess(ctx context.Context) {
	if s.status == nil || s.def == nil || s.def.ID == 0 {
		return
	}
	_ = s.status.RecordSuccess(ctx, s.def.ID)
}

func (s *SimklUserImport) recordFailure(ctx context.Context) {
	if s.status == nil || s.def == nil || s.def.ID == 0 {
		return
	}
	_ = s.status.RecordFailure(ctx, s.def.ID, 0)
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

// init registers the SimklUserImport constructor with the
// package-global [importlists.Default] registry.
func init() {
	importlists.Default().MustRegister(Implementation, ConfigContract, New)
}
