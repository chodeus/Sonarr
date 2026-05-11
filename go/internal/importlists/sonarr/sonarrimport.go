package sonarr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"sonarr.tv/sonarr/internal/importlists"
)

// minRefreshInterval ports C# `MinRefreshInterval =>
// TimeSpan.FromMinutes(5)` at `SonarrImport.cs:22`.
const minRefreshInterval = 5 * time.Minute

// statusRecorder is the narrow interface SonarrImport needs from
// the deps.StatusService bundle. The real
// `*importliststatus.Service` satisfies this via structural typing
// (RecordSuccess + RecordFailure methods).
//
// Defined here rather than at the importlists package level
// because the C# call-site at `SonarrImport.cs:83-91` is
// per-provider — each concrete import-list class invokes
// RecordSuccess/RecordFailure inside its Fetch body. Future
// concrete providers will use the same shape; if a third provider
// lands we can promote this interface up.
type statusRecorder interface {
	RecordSuccess(ctx context.Context, providerID int) error
	RecordFailure(ctx context.Context, providerID int, minimumBackOff time.Duration) error
}

// SonarrImport ports the C# `SonarrImport` concrete import-list
// at `ImportLists/Sonarr/SonarrImport.cs:16-176`.
//
// The C# class extends `ImportListBase<SonarrSettings>` and the
// base provides the Test wrapper + CleanupListItems helper. The
// Go port inlines the CleanupListItems behaviour (Dedup + stamp
// ImportListID/ImportList) into Fetch since it's the only call-
// site; if a second concrete provider lands needing the same
// post-processing we'll lift it into an internal helper.
//
// Goroutine-safety: settings is captured by value at construction
// time and not mutated. The proxy is stateless. Concurrent Fetch
// / Test / RequestAction calls are safe.
type SonarrImport struct {
	def      *importlists.ImportList
	settings Settings
	proxy    *Proxy
	status   statusRecorder
	logger   *slog.Logger
}

// Compile-time assertion: *SonarrImport satisfies importlists.Provider.
var _ importlists.Provider = (*SonarrImport)(nil)

// New constructs a *SonarrImport from a persisted [importlists.ImportList]
// row + the cross-provider [importlists.Dependencies] bundle.
// Mirrors the [importlists.Constructor] signature.
//
// Settings JSON deserialisation: the row's Settings column is a
// JSON string the C# Newtonsoft serializer wrote; the Go port
// uses encoding/json with matching lowerCamelCase tags on
// [Settings]. A nil-or-empty Settings string deserialises into the
// DefaultSettings shape (mirrors C# `new SonarrSettings()`).
//
// HTTPClient dep is required — a Sonarr-peer provider with no HTTP
// transport can't poll. Returns a typed error so the registry's
// Construct loop can mark the row as broken-implementation without
// crashing.
//
// StatusService dep is optional — when nil, Fetch skips the
// RecordSuccess / RecordFailure calls. The orchestrator (queue
// [I4]) supplies the real *importliststatus.Service; tests
// constructing the provider directly may pass nil.
func New(def *importlists.ImportList, deps importlists.Dependencies) (importlists.Provider, error) {
	if def == nil {
		return nil, errors.New("sonarr-import: nil definition")
	}
	settings := DefaultSettings()
	if def.Settings != nil && *def.Settings != "" {
		if err := json.Unmarshal([]byte(*def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("sonarr-import: unmarshal settings: %w", err)
		}
	}
	doer, _ := deps.HTTPClient.(httpDoer)
	proxy := NewProxy(doer)

	status, _ := deps.StatusService.(statusRecorder)

	logger, _ := deps.Logger.(*slog.Logger)
	if logger == nil {
		logger = slog.Default()
	}

	return &SonarrImport{
		def:      def,
		settings: settings,
		proxy:    proxy,
		status:   status,
		logger:   logger,
	}, nil
}

// Name returns the provider's display label. Mirrors C# `Name`
// at `SonarrImport.cs:19`.
func (s *SonarrImport) Name() string { return "Sonarr" }

// Definition returns the persisted row backing this provider
// instance. Mirrors C# `Definition` at `ImportListBase.cs:74`.
func (s *SonarrImport) Definition() *importlists.ImportList { return s.def }

// ListType reports the provider category. Mirrors C# `ListType =>
// ImportListType.Program` at `SonarrImport.cs:21`.
func (s *SonarrImport) ListType() importlists.ListType { return importlists.ListTypeProgram }

// MinRefreshInterval returns the smallest sync cadence. Mirrors C#
// `MinRefreshInterval => TimeSpan.FromMinutes(5)` at
// `SonarrImport.cs:22`.
func (s *SonarrImport) MinRefreshInterval() time.Duration { return minRefreshInterval }

// Fetch ports C# `SonarrImport.Fetch` at
// `SonarrImport.cs:35-94`. Walks /api/v3/series on the peer,
// applies the four configured filters
// (ProfileIDs / LanguageProfileIDs / TagIDs / RootFolderPaths) in
// the same order as the C# original, builds the ItemInfo per row,
// optionally carries the per-season monitor hint, and stamps in
// the producing list's id+name.
//
// On hard failure (proxy returns error) the C# code calls
// `RecordFailure(Definition.Id)` and sets `anyFailure=true`. The
// Go port matches: RecordFailure is invoked with a zero
// minimumBackOff (matches C# `RecordFailure(id)` overload that
// defaults TimeSpan to zero).
//
// On success the C# code calls `RecordSuccess(Definition.Id)`. The
// Go port matches.
func (s *SonarrImport) Fetch(ctx context.Context) (importlists.FetchResult, error) {
	remote, err := s.proxy.GetSeries(ctx, &s.settings)
	if err != nil {
		if s.logger != nil {
			s.logger.Debug("sonarr-import: fetch failed",
				slog.String("list", s.def.Name),
				slog.Any("err", err))
		}
		if s.status != nil {
			_ = s.status.RecordFailure(ctx, s.def.ID, 0)
		}
		return importlists.FetchResult{Series: []importlists.ItemInfo{}, AnyFailure: true}, nil
	}

	out := make([]importlists.ItemInfo, 0, len(remote))
	for _, item := range remote {
		if !s.keep(item) {
			continue
		}
		info := importlists.ItemInfo{
			TvdbID: item.TvdbID,
			Title:  item.Title,
		}
		if s.settings.SyncSeasonMonitoring {
			info.Seasons = make([]importlists.Season, 0, len(item.Seasons))
			for _, sn := range item.Seasons {
				info.Seasons = append(info.Seasons, importlists.Season{
					SeasonNumber: sn.SeasonNumber,
					Monitored:    sn.Monitored,
				})
			}
		}
		out = append(out, info)
	}

	cleaned := importlists.Dedup(out)
	for i := range cleaned {
		cleaned[i].ImportListID = s.def.ID
		cleaned[i].ImportList = s.def.Name
	}

	if s.status != nil {
		_ = s.status.RecordSuccess(ctx, s.def.ID)
	}
	return importlists.FetchResult{Series: cleaned, AnyFailure: false}, nil
}

// keep applies the four filter rules from C# `SonarrImport.Fetch`
// at `SonarrImport.cs:45-63`, returning true when the remote series
// passes every configured filter.
//
// Filter semantics:
//
//   - ProfileIDs empty → no filter; else KEEP when
//     item.QualityProfileID is in the user's whitelist.
//   - LanguageProfileIDs empty → no filter; else KEEP when
//     item.LanguageProfileID is in the user's whitelist.
//   - TagIDs empty → no filter; else KEEP when ANY user tag is
//     present in item.Tags.
//   - RootFolderPaths empty → no filter; else KEEP when ANY user
//     path is a case-insensitive substring of item.RootFolderPath.
//     C# uses `ContainsIgnoreCase` (substring, not equality).
func (s *SonarrImport) keep(item Series) bool {
	if len(s.settings.ProfileIDs) > 0 && !containsInt(s.settings.ProfileIDs, item.QualityProfileID) {
		return false
	}
	if len(s.settings.LanguageProfileIDs) > 0 && !containsInt(s.settings.LanguageProfileIDs, item.LanguageProfileID) {
		return false
	}
	if len(s.settings.TagIDs) > 0 && !anyInt(s.settings.TagIDs, item.Tags) {
		return false
	}
	if len(s.settings.RootFolderPaths) > 0 && !anySubstringFold(s.settings.RootFolderPaths, item.RootFolderPath) {
		return false
	}
	return true
}

// Test ports the C# `ImportListBase.Test()` wrapper at
// `ImportListBase.cs:98-113` + the per-provider `Test(failures)`
// body at `SonarrImport.cs:172-175` (which delegates to
// `_sonarrV3Proxy.Test`).
//
// The C# code runs Settings.Validate() before Test() at the
// factory level (`ImportListFactory.cs:80-87`); the Go port runs
// it inline here so a provider-level Test call from a bare
// constructor (no Factory) still surfaces missing-API-key.
func (s *SonarrImport) Test(ctx context.Context) ([]importlists.TestFailure, error) {
	if failures := s.settings.Validate(); len(failures) > 0 {
		return failures, nil
	}
	return s.proxy.Test(ctx, &s.settings), nil
}

// RequestAction ports C# `SonarrImport.RequestAction` at
// `SonarrImport.cs:96-170`. Handles the v3 UI's Select-options
// callbacks for the four filter fields:
//
//   - "getProfiles" → quality profiles, sorted by Name asc (case-
//     insensitive).
//   - "getLanguageProfiles" → language profiles, sorted by Name.
//   - "getTags" → tags, sorted by Label.
//   - "getRootFolders" → root folders, sorted by Path.
//
// Each successful action returns a {"options": [{value, name}, ...]}
// payload the v3 UI consumes. Unknown actions return `{}` (mirrors
// C# `return new { }` at `:169`).
//
// C# short-circuits with `{"devices": []}` when ApiKey is empty
// (`:99-105`); the Go port matches.
//
// C# raises if Settings.Validate filtered on "ApiKey" reports a
// failure (`:107` `ThrowOnError`). The Go port surfaces this as a
// non-nil error return — caller (UI controller) treats this as a
// 400 / form-validation failure.
func (s *SonarrImport) RequestAction(ctx context.Context, action string, _ map[string]string) (any, error) {
	if strings.TrimSpace(s.settings.APIKey) == "" {
		return map[string]any{"devices": []any{}}, nil
	}
	for _, f := range s.settings.Validate() {
		if f.PropertyName == "ApiKey" {
			return nil, fmt.Errorf("sonarr-import: %s: %s", f.PropertyName, f.ErrorMessage)
		}
	}
	switch action {
	case "getProfiles":
		profiles, err := s.proxy.GetQualityProfiles(ctx, &s.settings)
		if err != nil {
			return nil, err
		}
		return profileOptions(profiles), nil
	case "getLanguageProfiles":
		profiles, err := s.proxy.GetLanguageProfiles(ctx, &s.settings)
		if err != nil {
			return nil, err
		}
		return profileOptions(profiles), nil
	case "getTags":
		tags, err := s.proxy.GetTags(ctx, &s.settings)
		if err != nil {
			return nil, err
		}
		sort.SliceStable(tags, func(i, j int) bool {
			return strings.ToLower(tags[i].Label) < strings.ToLower(tags[j].Label)
		})
		opts := make([]map[string]any, 0, len(tags))
		for _, t := range tags {
			opts = append(opts, map[string]any{"value": t.ID, "name": t.Label})
		}
		return map[string]any{"options": opts}, nil
	case "getRootFolders":
		folders, err := s.proxy.GetRootFolders(ctx, &s.settings)
		if err != nil {
			return nil, err
		}
		sort.SliceStable(folders, func(i, j int) bool {
			return strings.ToLower(folders[i].Path) < strings.ToLower(folders[j].Path)
		})
		opts := make([]map[string]any, 0, len(folders))
		for _, f := range folders {
			opts = append(opts, map[string]any{"value": f.Path, "name": f.Path})
		}
		return map[string]any{"options": opts}, nil
	default:
		return map[string]any{}, nil
	}
}

// profileOptions builds the {"options":[{value,name}, ...]} payload
// for the {Quality,Language}Profile actions. Sort is case-insensitive
// on Name to match C# `OrderBy(d => d.Name,
// StringComparer.InvariantCultureIgnoreCase)`.
func profileOptions(profiles []Profile) map[string]any {
	sort.SliceStable(profiles, func(i, j int) bool {
		return strings.ToLower(profiles[i].Name) < strings.ToLower(profiles[j].Name)
	})
	opts := make([]map[string]any, 0, len(profiles))
	for _, p := range profiles {
		opts = append(opts, map[string]any{"value": p.ID, "name": p.Name})
	}
	return map[string]any{"options": opts}
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func anyInt(a, b []int) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

func anySubstringFold(needles []string, haystack string) bool {
	hl := strings.ToLower(haystack)
	for _, n := range needles {
		if strings.Contains(hl, strings.ToLower(n)) {
			return true
		}
	}
	return false
}
