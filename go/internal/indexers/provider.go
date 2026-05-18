package indexers

import "context"

// Provider is the minimal per-impl surface a concrete indexer
// implements. Mirrors the subset of C# `IIndexer` at
// `src/NzbDrone.Core/Indexers/IIndexer.cs` that the v3 API test/test-
// all/action endpoints exercise:
//
//   - [Provider.Test] runs the connection self-check. Returns the
//     FluentValidation-style failure list (empty = success).
//   - [Provider.RequestAction] handles the per-impl handshake some
//     indexer settings classes expose (e.g. Newznab category
//     auto-discovery via the `getCategories` action). Mirrors C#
//     `object RequestAction(string action, IDictionary<string,string> query)`
//     at `src/NzbDrone.Core/ThingiProvider/IProvider.cs:15`.
//
// Concrete impls (Newznab, Torznab, BroadcastTheNet, etc.) implement
// this once the per-impl Settings + transport classes port. Today
// the package ships zero registered impls — the registry stays
// empty until the first concrete indexer lands.
//
// Field-fetch surfaces (RSS pull, Search, Categories enumeration)
// aren't part of this v3-boundary interface because they're not
// exercised by the test/test-all/action endpoints. Those land in
// a separate `IndexerFetcher` interface when the fetch path ports.
type Provider interface {
	// Test executes the per-impl connection self-check. Returns
	// validation failures (empty slice = success). An error return
	// is reserved for "test was aborted due to an unexpected
	// exception" — concrete impls should prefer wrapping the
	// upstream failure as a TestFailure where possible.
	Test(ctx context.Context) ([]TestFailure, error)

	// RequestAction handles per-impl actions. Common across the
	// Newznab/Torznab family: getCategories (category enumeration),
	// newznabPresets (preset URL discovery for the React UI's "Add
	// indexer" wizard). Unknown actions return nil per the C#
	// convention at `IndexerBase.cs` (no-op default).
	RequestAction(ctx context.Context, action string, query map[string]string) (any, error)
}

// TestFailure is one FluentValidation-style field-level failure
// returned by [Provider.Test]. Same shape as
// [notifications.TestFailure] and [downloadclients.TestFailure] —
// duplicated rather than shared until a cross-family failure type
// has more than one cross-cutting consumer.
type TestFailure struct {
	// PropertyName is the dotted-path field this failure applies
	// to. Empty string for whole-form failures.
	PropertyName string

	// ErrorMessage is the human-readable failure description. The
	// v3 React UI renders this verbatim next to the field input.
	ErrorMessage string

	// DetailedDescription is the optional long-form explanation
	// some validators attach. Empty when no long-form available.
	DetailedDescription string
}

// Constructor builds a Provider from the persisted row + injected
// deps. Mirrors C# `IIndexerFactory.Construct(IndexerDefinition def)`
// at `src/NzbDrone.Core/Indexers/IndexerFactory.cs:31`.
//
// An error return covers "the concrete impl refuses to construct
// from this row" — typically:
//
//   - Settings JSON couldn't deserialise into the per-impl Settings
//     struct.
//   - A required dependency in [Dependencies] is nil and the
//     concrete impl refuses to construct without it.
type Constructor func(definition *Indexer, deps Dependencies) (Provider, error)

// Dependencies bundles the cross-provider deps every concrete
// indexer constructor receives. Mirrors the C# parameter pack shared
// across `IndexerBase<TSettings>` derivatives (each takes
// IHttpClient + Logger; some take IIndexerStatusService).
//
// Fields are typed as `any` so the actual cross-package dep types
// don't drag this package's import graph through every consumer.
type Dependencies struct {
	// HTTPClient is the HTTP transport every feed-fetch + Test()
	// implementation uses. Bound to *httpclient.Client in production.
	HTTPClient any

	// Logger is the slog.Logger handle the concrete impl emits
	// against. Bound to the host-supplied logger in production.
	Logger any

	// StatusService is the *indexerstatus.Service for failure-back-
	// off escalation. Bound by main.go.
	StatusService any
}
