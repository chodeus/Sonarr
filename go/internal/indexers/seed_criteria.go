package indexers

import "context"

// SeasonPackSeedGoal ports the C# enum at
// `Indexers/SeasonPackSeedGoal.cs:5-11`.
//
// Per-indexer toggle: when fetching a season-pack release (FullSeason
// set by the parser), should the cleanup-gate use the indexer's
// standard SeedRatio/SeedTime — or the season-pack-specific
// SeasonPackSeedRatio/SeasonPackSeedTime? Stored as an int rather
// than a typed enum on the persisted JSON shape because that's what
// the v3 wire surface ships (`FieldType.Select` with int values 0/1)
// — see C# `SeedCriteriaSettings.cs:67`.
//
// Ordinals are wire-stable; new values land at the end of the list.
type SeasonPackSeedGoal int

const (
	// SeasonPackSeedGoalUseStandard means "season-pack releases reuse
	// the same SeedRatio/SeedTime as single-episode releases". C#
	// `SeasonPackSeedGoal.UseStandardSeedGoal = 0`.
	SeasonPackSeedGoalUseStandard SeasonPackSeedGoal = 0

	// SeasonPackSeedGoalUseSeasonPack means "season-pack releases
	// honour the SeasonPackSeedRatio / SeasonPackSeedTime overrides".
	// C# `SeasonPackSeedGoal.UseSeasonPackSeedGoal = 1`.
	SeasonPackSeedGoalUseSeasonPack SeasonPackSeedGoal = 1
)

// FailDownloads ports the C# enum at `Indexers/FailDownloads.cs:5-15`.
//
// Per-indexer policy: which downloadable-payload categories should
// be auto-failed at grab time? Persisted as an int set on the
// indexer's settings JSON; the cleanup pipeline reads it to decide
// when to mark a grab Failed before handing to the download client.
type FailDownloads int

const (
	// FailDownloadsExecutables flags `.exe`/`.bat`/`.ps1`/etc grabs.
	// C# `FailDownloads.Executables = 0`.
	FailDownloadsExecutables FailDownloads = 0

	// FailDownloadsPotentiallyDangerous flags broader categories
	// (e.g. scripts, archive bombs). C# `FailDownloads.PotentiallyDangerous = 1`.
	FailDownloadsPotentiallyDangerous FailDownloads = 1

	// FailDownloadsUserDefinedExtensions consults the
	// download-client's UserDefinedExtensions list. C#
	// `FailDownloads.UserDefinedExtensions = 2`.
	FailDownloadsUserDefinedExtensions FailDownloads = 2
)

// SeedCriteriaSettings ports the C# `SeedCriteriaSettings` POCO at
// `Indexers/SeedCriteriaSettings.cs:59-75`. Carries the per-indexer
// seed-policy overrides that get materialised into a
// `TorrentSeedConfiguration` on each torrent grab.
//
// Pointer fields mirror the C# nullable shape (`double?` / `int?`):
// nil → "no override, fall back to the global default". The Go port
// keeps the same nullable semantics so a deserialised
// SeedCriteriaSettings round-trips through JSON without manifesting
// a 0-valued ratio when the indexer hasn't set one.
//
// JSON tags match the camelCase v3 wire shape (lowercase first letter),
// identical to the already-shipped `torznab.SeedCriteriaSettings` —
// this canonical type can subsume the per-indexer-package duplicates
// in a future indexer-factory consolidation slice. Kept as a value
// type (not pointer) because the C# tree treats it as a sub-record
// embedded in the indexer settings; nil-vs-empty is conveyed by the
// nullable fields, not by the struct pointer itself.
type SeedCriteriaSettings struct {
	SeedRatio           *float64           `json:"seedRatio"`
	SeedTime            *int               `json:"seedTime"`
	SeasonPackSeedGoal  SeasonPackSeedGoal `json:"seasonPackSeedGoal"`
	SeasonPackSeedRatio *float64           `json:"seasonPackSeedRatio"`
	SeasonPackSeedTime  *int               `json:"seasonPackSeedTime"`
}

// CachedIndexerSettings ports the C# class at
// `Indexers/CachedIndexerSettingsProvider.cs:75-79` — the lookup
// value the cached-settings provider hands back.
//
// FailDownloads carries the per-indexer "auto-fail this category"
// set (the C# uses `HashSet<FailDownloads>`; the Go port keeps a
// slice because the lookup is small enough that membership-tests
// don't need a hash, and the persisted JSON shape is an int array).
// SeedCriteriaSettings is non-nil only for torrent indexers; for
// usenet indexers it stays at the zero value (all fields nil),
// matching C# `CachedIndexerSettingsProvider.cs:56-59` where the
// torrent-cast guard skips the assignment.
type CachedIndexerSettings struct {
	FailDownloads        []FailDownloads
	SeedCriteriaSettings *SeedCriteriaSettings
}

// IndexerSettingsLookup ports the C# `ICachedIndexerSettingsProvider`
// interface at `Indexers/CachedIndexerSettingsProvider.cs:12-15`.
//
// Returns the cached settings for a given indexer ID, or (nil, nil)
// for an unknown ID. Callers treat both nil-CachedIndexerSettings
// and nil-SeedCriteriaSettings as "no seed-policy override —
// fall back to the global default", which mirrors the C# null-
// tolerance at `Indexers/SeedConfigProvider.cs:44-50`.
//
// The production binding for this slice is deferred — the full
// CachedIndexerSettingsProvider port needs the IndexerFactory +
// settings deserialiser, which are scoped to a separate later
// slice. Tests inject a stub returning a canned
// CachedIndexerSettings; the wiring slice will swap that for the
// real factory-backed cache.
type IndexerSettingsLookup interface {
	GetSettings(ctx context.Context, indexerID int) (*CachedIndexerSettings, error)
}
