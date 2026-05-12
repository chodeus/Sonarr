package metadata

import (
	"context"
	"strconv"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/metadatafiles"
	"sonarr.tv/sonarr/internal/series"
)

// SeriesMetadataReason mirrors the C# enum at
// `src/NzbDrone.Core/Extras/Metadata/IMetadata.cs:20-25`. It tells the
// per-series-metadata producer WHY the writer is being asked to emit a
// fresh `tvshow.nfo` (or equivalent) so it can decide whether to short-
// circuit. The C# `XbmcMetadata.GetSeriesMetadata` notably skips the
// write on Scan when an existing valid NFO is detected (see
// XbmcNfoDetector).
//
// Integer values are NOT persisted — this enum lives only on the wire
// between MetadataService and the per-provider implementation. We pin
// explicit ordinals anyway to keep call-site assertions stable; the
// numbering follows the C# default-enum source ordering at the cite
// above.
//
// Slice 4.BA (queue [W1] — METADATA-WRITERS foundation): consumed by
// [Provider.SeriesMetadata].
type SeriesMetadataReason int

const (
	// SeriesMetadataReasonScan is the C# Scan (=0) — periodic disk
	// rescan that may re-emit series metadata.
	SeriesMetadataReasonScan SeriesMetadataReason = 0

	// SeriesMetadataReasonEpisodeFolderCreated is the C# value (=1) —
	// the import pipeline created a new season folder and is asking
	// the writer to populate series-level metadata for it.
	SeriesMetadataReasonEpisodeFolderCreated SeriesMetadataReason = 1

	// SeriesMetadataReasonEpisodesImported is the C# value (=2) —
	// episodes finished importing; refresh series metadata so the
	// episode-count / last-aired fields stay current.
	SeriesMetadataReasonEpisodesImported SeriesMetadataReason = 2
)

// String returns the C# enum name. Unknown values render as
// "SeriesMetadataReason(<n>)" — same convention as [MetadataType].
func (r SeriesMetadataReason) String() string {
	switch r {
	case SeriesMetadataReasonScan:
		return "Scan"
	case SeriesMetadataReasonEpisodeFolderCreated:
		return "EpisodeFolderCreated"
	case SeriesMetadataReasonEpisodesImported:
		return "EpisodesImported"
	default:
		return "SeriesMetadataReason(" + strconv.Itoa(int(r)) + ")"
	}
}

// Season is the per-season slice the metadata-writer consumes when it
// builds per-season artwork URLs. Mirrors the subset of C# `Tv.Season`
// the Xbmc / Roksbox / Plex writers read — only SeasonNumber and the
// Images list are touched at the call sites for season-image emission
// (the C# `XbmcMetadata.SeasonImages` at `XbmcMetadata.cs:417-454`).
//
// The `series.Series` row persists its seasons as a JSON blob string
// (`Series.Seasons`); per-call decode lands in the consumer-facing
// service (queue [W7]) or in the per-writer slice as a helper. Kept
// as a local record rather than adding a typed `series.Season` to the
// series package because no consumer outside the metadata-writers
// reaches for season as a typed value yet.
//
// Slice 4.BA (queue [W1] — METADATA-WRITERS foundation): paired with
// [Provider.SeasonImages].
type Season struct {
	// SeasonNumber is the 0-indexed season ordinal (0 = specials).
	SeasonNumber int

	// Monitored mirrors C# `Season.Monitored`. Not all writers read
	// it (Xbmc does not; future Emby / Jellyfin writers might).
	Monitored bool
}

// Provider is the runtime metadata-writer abstraction concrete
// implementations (XbmcMetadata, PlexMetadata, RoksboxMetadata,
// WdtvMetadata) satisfy. Mirrors the C# `IMetadata` interface at
// `src/NzbDrone.Core/Extras/Metadata/IMetadata.cs:9-18` plus the
// abstract C# `MetadataBase<TSettings>` no-op defaults at
// `src/NzbDrone.Core/Extras/Metadata/MetadataBase.cs:12-58` and the
// shared C# `IProvider` base at
// `src/NzbDrone.Core/ThingiProvider/IProvider.cs:7-17`.
//
// Slice 4.BA (queue [W1] of focused-subset 6 — METADATA-WRITERS
// family): the package-base slice ships the interface contract +
// Constructor + Dependencies + Registry. Concrete providers
// (XbmcMetadata + PlexMetadata + RoksboxMetadata + WdtvMetadata) land
// in queues [W2]-[W6]; the per-event fan-out orchestrator (queue
// [W7] — MetadataService) drives this interface.
//
// Method semantics summary (full C# cites on each method):
//
//   - Name, Definition mirror the C# IProvider base contract.
//   - GetFilenameAfterMove rewrites the metadata-file path after a
//     parent-episode-file rename — every writer's existing `.nfo` /
//     `.tbn` / `.jpg` companion must follow the new name. Default
//     impl in `MetadataBase.GetFilenameAfterMove` swaps just the
//     extension (`MetadataBase.cs:30-37`); some writers override
//     for naming-quirks (e.g. Roksbox image suffix).
//   - FindMetadataFile classifies an existing on-disk file as a
//     known consumer-emitted sidecar (or returns nil for "not one
//     of mine"). Used by the [ExistingMetadataImporter] scan path
//     (queue [W8]) to repopulate the [internal/metadatafiles] table
//     for existing libraries.
//   - SeriesMetadata builds the per-series sidecar contents. Returns
//     a zero-value [MetadataFileResult] (RelativePath=="") when the
//     writer chose not to emit — mirrors the C# `null` return at
//     `IMetadata.cs:13`.
//   - EpisodeMetadata builds the per-episode sidecar contents. Same
//     zero-value convention as SeriesMetadata.
//   - SeriesImages / SeasonImages / EpisodeImages return zero-or-more
//     remote-URL ImageFileResults the consumer-facing service then
//     downloads to disk. Writers do NOT perform HTTP.
//   - Test is the per-provider connection self-check. The C# base at
//     `MetadataBase.cs:25-28` returns an empty ValidationResult —
//     metadata writers never have a real connection to test. The Go
//     port keeps this on the interface for parity with the other
//     ThingiProvider families and for future per-writer overrides.
//   - RequestAction handles the OAuth-style v3-UI handshake. None of
//     the four current metadata writers override; default returns
//     nil + nil-error. Kept on the interface for parity.
//
// Goroutine-safety: implementations MUST tolerate concurrent
// SeriesMetadata / EpisodeMetadata / *Images calls — the consumer-
// facing fan-out (queue [W7]) may dispatch in parallel across
// different writers AND different series. The fan-out is NOT
// serialised on a single provider instance.
//
// Error convention: every method returns a single error. A non-nil
// error is logged but does NOT abort the orchestrator's per-writer
// loop. Providers should NOT panic on bad input; return an error
// instead.
type Provider interface {
	// Name returns the user-facing display label. Mirrors C#
	// `Name { get; }` at `MetadataBase.cs:15`.
	Name() string

	// Definition returns the persisted Metadata row backing this
	// provider instance. Mirrors C# `Definition { get; set; }` at
	// `MetadataBase.cs:23`. Nil from a test-constructed provider
	// that hasn't been bound to a row.
	Definition() *Metadata

	// GetFilenameAfterMove computes the new metadata-file path after
	// the parent episode-file moves/renames. Mirrors C#
	// `string GetFilenameAfterMove(Series, EpisodeFile, MetadataFile)`
	// at `IMetadata.cs:11`. Default behaviour at
	// `MetadataBase.cs:30-37` swaps the extension; per-writer
	// overrides may rewrite the stem (Roksbox image-suffix).
	GetFilenameAfterMove(s *series.Series, episodeFile *episodefiles.File, metadataFile *metadatafiles.MetadataFile) string

	// FindMetadataFile classifies an on-disk file at `path` as a
	// known consumer-emitted sidecar. Returns nil when the file is
	// NOT one of this writer's outputs (the caller iterates over
	// every registered writer, so a nil return is the common case).
	// Mirrors C# `MetadataFile FindMetadataFile(Series, string)` at
	// `IMetadata.cs:12`.
	FindMetadataFile(s *series.Series, path string) (*metadatafiles.MetadataFile, error)

	// SeriesMetadata builds the per-series sidecar contents. Returns
	// a zero-value [MetadataFileResult] (RelativePath == "") when
	// the writer chose to skip (mirrors C# null return at
	// `IMetadata.cs:13`).
	SeriesMetadata(ctx context.Context, s *series.Series, reason SeriesMetadataReason) (MetadataFileResult, error)

	// EpisodeMetadata builds the per-episode sidecar contents. Same
	// zero-value convention as [Provider.SeriesMetadata].
	EpisodeMetadata(ctx context.Context, s *series.Series, episodeFile *episodefiles.File) (MetadataFileResult, error)

	// SeriesImages returns the per-series artwork the writer wants
	// emitted (typically poster + fanart + banner). Mirrors C#
	// `List<ImageFileResult> SeriesImages(Series)` at
	// `IMetadata.cs:15`. Returns an empty slice when the writer
	// chose to skip.
	SeriesImages(ctx context.Context, s *series.Series) ([]ImageFileResult, error)

	// SeasonImages returns the per-season artwork the writer wants
	// emitted. Mirrors C# `List<ImageFileResult> SeasonImages(Series,
	// Season)` at `IMetadata.cs:16`. Returns an empty slice when the
	// writer chose to skip.
	SeasonImages(ctx context.Context, s *series.Series, season Season) ([]ImageFileResult, error)

	// EpisodeImages returns the per-episode artwork the writer wants
	// emitted (typically a thumbnail). Mirrors C#
	// `List<ImageFileResult> EpisodeImages(Series, EpisodeFile)` at
	// `IMetadata.cs:17`.
	EpisodeImages(ctx context.Context, s *series.Series, episodeFile *episodefiles.File) ([]ImageFileResult, error)

	// Test executes the per-provider connection self-check. Returns
	// the validation failures (empty slice = success). Mirrors C#
	// `ValidationResult Test()` at `MetadataBase.cs:25-28`. The C#
	// base impl returns an empty result; the Go port keeps the hook
	// on the interface for parity with notifications.Provider /
	// importlists.Provider and for future per-writer overrides.
	Test(ctx context.Context) ([]TestFailure, error)

	// RequestAction handles the OAuth-style v3-UI handshake. None of
	// the four current metadata writers override; default returns
	// nil + nil-error. Mirrors C# `object RequestAction(string,
	// IDictionary<string, string>)` at `MetadataBase.cs:47-50`.
	RequestAction(ctx context.Context, action string, query map[string]string) (any, error)
}

// TestFailure is the per-field validation result a [Provider.Test]
// call returns. Mirrors the FluentValidation `ValidationFailure` shape
// the C# tree carries through `ValidationResult.Errors`. Both fields
// are user-facing strings the v3 UI surfaces verbatim — PropertyName
// keys the form-field that errored; ErrorMessage is the human-readable
// reason.
//
// Pattern-mirror of notifications.TestFailure and importlists.TestFailure.
type TestFailure struct {
	// PropertyName is the per-Settings field that failed validation
	// (matches the C# `Settings` property name). Empty when the
	// failure is global rather than field-scoped.
	PropertyName string

	// ErrorMessage is the human-readable failure reason.
	ErrorMessage string
}

// Constructor builds a [Provider] instance from a persisted [Metadata]
// row + the cross-provider [Dependencies] bundle. Concrete provider
// packages (XbmcMetadata, PlexMetadata, RoksboxMetadata, WdtvMetadata
// — queues [W2]-[W6]) register one Constructor per (Implementation,
// ConfigContract) pair at init() time.
//
// Mirrors the C# pattern where `IServiceProvider` resolves `IMetadata`
// instances by (Type, Settings) pair at
// `ProviderFactory<TProvider,TProviderDefinition>.GetInstance` (in the
// ThingiProvider base class, outside the Extras/Metadata directory).
//
// Returning an error covers the same two failure modes as
// [notifications.Constructor]:
//
//   - Settings JSON couldn't deserialise into the per-provider
//     Settings struct.
//   - A required dependency in [Dependencies] is nil and the concrete
//     provider refuses to construct without it.
type Constructor func(definition *Metadata, deps Dependencies) (Provider, error)

// Dependencies bundles the cross-provider deps every concrete
// metadata-writer constructor receives. Mirrors the parameter pack
// shared across `MetadataBase<TSettings>` derivatives in the C# tree —
// the four C# writers take IMapCoversToLocal + IDiskProvider +
// IHttpClient + ILocalizationService + Logger.
//
// Fields are typed as `any` so the actual cross-package dep types
// (httpclient.Client, *disk.Provider, slog.Logger, etc.) don't drag
// this package's import graph through every consumer. Concrete
// provider packages cast to the typed shape they need at construction
// time. Nil values are explicitly tolerated when the per-provider
// constructor doesn't reach for that dep.
//
// The named struct here is the contract; we evolve it additively as
// concrete providers land. Pattern-mirror of importlists.Dependencies
// at `internal/importlists/provider.go:154-194`.
type Dependencies struct {
	// HTTPClient is the HTTP transport used for image downloads when
	// a writer chooses to fetch its own URLs (the C# base does NOT —
	// the consumer-facing service downloads; this is here for
	// optional per-writer use). Bound to `*httpclient.Client` in
	// production.
	HTTPClient any

	// DiskProvider is the filesystem abstraction the writers use to
	// inspect existing on-disk files in [Provider.FindMetadataFile].
	// Bound to `*disk.Provider` in production.
	DiskProvider any

	// MapCoversToLocal is the C# `IMapCoversToLocal` shim that
	// rewrites remote SkyHook cover URLs to local-cache paths when
	// the cache has them. Bound by main.go when the cover-cache
	// service lands.
	MapCoversToLocal any

	// Logger is the slog.Logger handle the concrete provider emits
	// against. Bound to the host-supplied logger in production.
	Logger any

	// LocalizationService backs the localised error strings the
	// Test() wrapper emits. Mirrors C# ILocalizationService at
	// `MetadataBase.cs` (via the base class). Nil-tolerated: when
	// nil, the Go port falls back to the English template.
	LocalizationService any
}
