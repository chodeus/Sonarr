package importlists

import (
	"context"
	"time"
)

// Provider is the runtime import-list abstraction concrete
// implementations (SonarrList, PlexList, TraktUserList, etc.)
// satisfy. Mirrors the C# `IImportList` interface at
// `src/NzbDrone.Core/ImportLists/IImportList.cs:6-11` plus the
// abstract C# `ImportListBase<TSettings>` no-op defaults at
// `src/NzbDrone.Core/ImportLists/ImportListBase.cs:31-121` and the
// shared C# `IProvider` base at
// `src/NzbDrone.Core/ThingiProvider/IProvider.cs:7-17`.
//
// Slice 4.AL (sub-arc I2b of queue [I2]) — the Provider interface
// contract + Constructor + Dependencies. Concrete providers
// (SonarrList + PlexList) land in queue [I3]; the per-list sync
// orchestrator (queue [I4] — ImportListSyncService) drives this
// interface.
//
// Method semantics summary (full C# cites on each method):
//
//   - Name, Definition mirror the C# IProvider base contract.
//   - ListType + MinRefreshInterval are the IImportList-specific
//     metadata the orchestrator consults to skip too-recent polls.
//   - Fetch executes the per-poll list retrieval. Mirrors C#
//     `ImportListFetchResult Fetch()` at `IImportList.cs:10`.
//   - Test executes the per-provider connection self-check.
//     Mirrors C# `ValidationResult Test()` at
//     `ImportListBase.cs:98-113`.
//   - RequestAction handles the OAuth handshake exchange some
//     providers (Plex, Trakt) expose to the v3 UI. Mirrors C#
//     `object RequestAction(string action, IDictionary<string,
//     string> query)` at `ImportListBase.cs:76-79`. Default
//     returns nil + nil-error; the OAuth-flow providers override.
//
// Goroutine-safety: implementations MUST tolerate concurrent
// Fetch / Test / RequestAction calls — the orchestrator's per-list
// worker pool may dispatch a manual Test() against one list while
// another fetch is in flight against a different list. The fan-out
// is NOT serialised on a single provider instance.
//
// Error convention: each method returns a single error. A non-nil
// error is logged + recorded via [importliststatus.Recorder] but
// does NOT abort other lists in the fan-out. Providers should NOT
// panic on bad input; return an error instead. Test() additionally
// returns []TestFailure for field-level validation results (empty
// slice = success); an error return is reserved for "test was
// aborted due to an unexpected exception" — mirrors C#
// `ImportListBase.Test()` at `ImportListBase.cs:98-113`.
type Provider interface {
	// Name returns the user-facing display label. Mirrors C#
	// `Name { get; }` at `ImportListBase.cs:40`.
	Name() string

	// Definition returns the persisted ImportLists row backing this
	// provider instance. Mirrors C# `Definition { get; set; }` at
	// `ImportListBase.cs:74`. Nil from a test-constructed provider
	// that hasn't been bound to a row.
	Definition() *ImportList

	// ListType reports the provider's category (Program/Plex/Trakt/
	// Simkl/Other/Advanced). Mirrors C# `ImportListType ListType`
	// at `IImportList.cs:8`. The orchestrator surfaces this to the
	// v3 UI for filter labels + icons.
	ListType() ListType

	// MinRefreshInterval is the smallest sync cadence the
	// orchestrator should allow for this list type. Mirrors C#
	// `TimeSpan MinRefreshInterval` at `IImportList.cs:9`. The
	// orchestrator skips a Fetch call when fewer than this many
	// seconds have elapsed since the last successful poll for the
	// same Definition.Id — see `ImportListSyncService.SyncList`.
	MinRefreshInterval() time.Duration

	// Fetch executes the per-poll list retrieval. Returns a
	// FetchResult carrying the per-poll items + a partial-failure
	// flag (see [FetchResult.AnyFailure]). Mirrors C#
	// `ImportListFetchResult Fetch()` at `IImportList.cs:10`.
	//
	// A non-nil error indicates a hard failure (HTTP transport
	// error, settings invalid, etc.) — the orchestrator records
	// this against [importliststatus.Recorder] via RecordFailure.
	// A nil error with AnyFailure=true is a soft partial failure
	// the orchestrator may opt to retry but does NOT escalate
	// back-off for.
	Fetch(ctx context.Context) (FetchResult, error)

	// Test executes the per-provider connection self-check.
	// Returns the validation failures (empty slice = success).
	// Mirrors C# `ValidationResult Test()` at
	// `ImportListBase.cs:98-113`. The C# code wraps the per-
	// provider `Test(failures)` in a try/catch and converts thrown
	// exceptions into a "Test aborted due to exception" failure;
	// the Go port surfaces those as a non-nil error return so the
	// orchestrator can log + record a connection failure (mirrors
	// the notifications.Provider.Test contract).
	Test(ctx context.Context) ([]TestFailure, error)

	// RequestAction handles the OAuth-style v3-UI handshake some
	// providers (Plex, Trakt) expose. Mirrors C# `object RequestAction(
	// string stage, IDictionary<string, string> query)` at
	// `ImportListBase.cs:76-79`. Default impl returns nil + nil-error;
	// OAuth-flow providers override.
	//
	// Returns an opaque payload the v3 UI deserialises per-provider
	// + per-action contract (e.g. PlexImport's startOAuth returns a
	// pin URL + poll id). The Go port carries this as `any` rather
	// than introducing a per-action variant struct.
	RequestAction(ctx context.Context, action string, query map[string]string) (any, error)
}

// Constructor builds a [Provider] instance from a persisted
// [ImportList] row + the cross-provider [Dependencies] bundle.
// Concrete provider packages (SonarrList, PlexList — queue [I3]
// and follow-ups) register one Constructor per (Implementation,
// ConfigContract) pair at init() time.
//
// Mirrors the C# pattern where `IServiceProvider` resolves
// `IImportList` instances by (Type, Settings) pair at
// `ProviderFactory<TProvider,TProviderDefinition>.GetInstance`
// (in the ThingiProvider base class, outside the ImportLists
// directory).
//
// Returning an error covers the same two failure modes as
// [notifications.Constructor]:
//
//   - Settings JSON couldn't deserialise into the per-provider
//     Settings struct.
//   - A required dependency in [Dependencies] is nil and the
//     concrete provider refuses to construct without it.
type Constructor func(definition *ImportList, deps Dependencies) (Provider, error)

// Dependencies bundles the cross-provider deps every concrete
// import-list constructor receives. Mirrors the parameter pack
// shared across `ImportListBase<TSettings>` derivatives in the C#
// tree — each concrete constructor takes IImportListStatusService
// + IConfigService + IParsingService + ILocalizationService +
// IHttpClient + Logger.
//
// Fields are typed as `any` so the actual cross-package dep types
// (httpclient.Client, *config.Provider, slog.Logger, etc.) don't
// drag this package's import graph through every consumer. Concrete
// provider packages cast to the typed shape they need at
// construction time. Nil values are explicitly tolerated when the
// per-provider constructor doesn't reach for that dep.
//
// The named struct here is the contract; we evolve it additively
// as concrete providers land. Pattern-mirror of
// notifications.Dependencies at
// `internal/notifications/provider.go:255-285`.
type Dependencies struct {
	// HTTPClient is the HTTP transport every list-poller provider
	// uses to reach its REST endpoint. Bound to
	// `*httpclient.Client` in production. Mirrors C# `IHttpClient`
	// injected into every import-list constructor.
	HTTPClient any

	// ConfigProvider is the application-config getter. Bound to
	// `*config.Provider` in production. Several providers
	// (PlexImport for the auth.servarr.com proxy host, Trakt for
	// the OAuth proxy host) read config.
	ConfigProvider any

	// Logger is the slog.Logger handle the concrete provider emits
	// against. Bound to the host-supplied logger in production.
	Logger any

	// StatusService is the [*importliststatus.Service] for back-off
	// escalation. Bound by main.go. The fan-out orchestrator
	// (queue [I4]) calls RecordSuccess / RecordFailure /
	// RecordConnectionFailure on per-provider outcomes; concrete
	// providers do NOT call this directly — except a handful of
	// OAuth-flow providers that explicitly RecordFailure with a
	// 1-hour minimum on proxy errors (per the analysis doc at
	// go/analysis/providers/import-lists.md row 19).
	StatusService any

	// ParsingService is the [*parsingservice.Service] every concrete
	// list uses to translate a per-item Title/Year into a series.
	// Mirrors C# IParsingService injected into the C# constructors.
	// Nil-tolerated when a list doesn't reach for it (e.g. lists
	// that return a TvdbId directly skip the title-parse step).
	ParsingService any

	// LocalizationService backs the localised error strings the
	// Test() wrapper emits when a per-provider Test body throws.
	// Mirrors C# ILocalizationService at `ImportListBase.cs:37`.
	// Nil-tolerated: when nil, the Go port falls back to the
	// English template.
	LocalizationService any

	// ImportListsRepo is the persistence handle the OAuth2 providers
	// (Trakt × 3 variants, Simkl, AniList, MyAnimeList) use to round-
	// trip a refreshed AccessToken / RefreshToken / Expires triple
	// back to the DB row. Bound to `*importlists.Repo` in production.
	// Without this dep, refreshed tokens live in process memory only
	// — next provider construction reads the stale token from DB and
	// refreshes again. Nil is tolerated; providers fall back to in-
	// memory-only updates.
	//
	// Mirrors C# `_importListRepository.UpdateSettings(...)` at
	// `Trakt/TraktImportBase.cs:143` + `AniList/AniListImportBase.cs:101`
	// + `Simkl/SimklImportBase.cs:176` + `MyAnimeList/MyAnimeListImport.cs:111`.
	ImportListsRepo any
}
