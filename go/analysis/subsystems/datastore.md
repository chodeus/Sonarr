# Datastore

**Source:** `src/NzbDrone.Core/Datastore/**`
**Target Go package:** `go/internal/datastore/`
**Status:** drafted

## Purpose

The Datastore subsystem is Sonarr's persistence layer. It owns: connection-string construction for SQLite or Postgres, opening / pooling DB connections, an `IBasicRepository<T>` generic CRUD layer over Dapper with reflection-driven column mapping, a fluent SQL/Where builder that translates LINQ expression trees into parameterised SQL, JSON-blob round-tripping for embedded documents, lazy loading of related entities, and FluentMigrator-driven schema migrations covering both engines from version 1 through the current head. Two logical databases are managed — the **main** DB (`sonarr.db` / `sonarr-main`) and the **log** DB (`logs.db` / `sonarr-log`) — selected by `MigrationType`. Schema mismatches here corrupt user data, so dialect parity is the highest-risk dimension of the Go port.

## ORM layer

### Libraries in use

- **Dapper** (`Dapper.SqlMapper`) — the actual query executor. Every `IDatabase.Query<T>(sql, ...)`, `Execute()`, `ExecuteScalar<T>()`, `QueryMultiple()` call ultimately goes through Dapper. Type-handler customisation (`SqlMapper.AddTypeHandler`) is how all the converters in `Datastore/Converters/` plug in (registered in `TableMapping.RegisterMappers`, `src/NzbDrone.Core/Datastore/TableMapping.cs:179`).
- **Hand-written SQL via Dapper** — `SqlBuilder` (`src/NzbDrone.Core/Datastore/SqlBuilder.cs`) builds the SQL string from named templates (`/**select**/`, `/**join**/`, `/**where**/`, etc.). This is a copy of the public Dapper.SqlBuilder pattern with custom inclusivity behaviour.
- **`WhereBuilder` / `WhereBuilderSqlite` / `WhereBuilderPostgres`** — custom expression-tree visitors (extending `System.Linq.Expressions.ExpressionVisitor`) that translate `Expression<Func<T, bool>>` predicates into parameterised SQL, with dialect divergence (e.g. Postgres uses `ILIKE` and `= ANY (array)`, SQLite uses `LIKE` and `IN (...)`).
- **FluentMigrator** (`FluentMigrator.Runner`, with `AddPostgres15_0()` and the project's custom `AddNzbDroneSQLite()` extension) — schema migrations only. Not used at runtime for CRUD. See `src/NzbDrone.Core/Datastore/Migration/Framework/MigrationController.cs`.
- **MiniProfiler** (`StackExchange.Profiling`) — wraps connections via `MainDatabase.OpenConnection()` so query timings show up in the dev-mode profiler. `LogDatabase` deliberately skips profiling.
- **Polly** — `BasicRepository.RetryStrategy` (`src/NzbDrone.Core/Datastore/BasicRepository.cs:56-71`) retries SQLite `BUSY` writes 3× with exponential backoff + jitter.
- ⚠️ **Marr.Data** is **not** in use in current source despite the brief's mention. There are no `using Marr` references; ORM duties are split between Dapper and the bespoke `BasicRepository` / `SqlBuilder` / `WhereBuilder` triple. The codebase refers to "Marr.Data" only historically.

### `IBasicRepository<T>`

Defined at `src/NzbDrone.Core/Datastore/BasicRepository.cs:19-43`. Surface: `All`, `Count`, `Find(id)`, `Get(id)`, `Get(ids)`, `Insert`, `InsertMany`, `Update`, `UpdateMany`, `Upsert`, `SetFields(model, props…)` (partial update by selector expression), `Delete(model|id)`, `DeleteMany(models|ids)`, `Purge(vacuum?)`, `HasItems`, `Single`, `SingleOrDefault`, `GetPaged(PagingSpec)`. Construction (`BasicRepository.cs:76-92`) reads `TableMapping` to resolve `_table`, `_keyProperty` (always `Id` from `ModelBase`), the included property list (excludes `Ignore(...)` properties + the key), and pre-builds `_insertSql` / `_updateSql` strings, with dialect-specific INSERT (Postgres `RETURNING "Id"`, SQLite `; SELECT last_insert_rowid() id`).

A Go re-implementation has to replicate:
- Reflection-driven column list and exclusion (`TableMapper.IgnoreList`).
- Per-dialect INSERT-with-returned-id.
- Expression-to-SQL `Where` (the `Query(Expression<Func<T,bool>>)` overload at `BasicRepository.cs:100`).
- Eventing — `Insert/Update/Delete` raise `ModelEvent<TModel>` via `IEventAggregator` only when subclasses opt in via `PublishModelEvents` (`BasicRepository.cs:510`).
- Polly-style retry for SQLite BUSY (Postgres path doesn't retry).
- Bulk `InsertMany` / `UpdateMany` inside a single `IsolationLevel.ReadCommitted` transaction.
- `SetFields(model, x => x.Foo, x => x.Bar)` — a partial-update API that uses the C# expression member name. In Go this typically becomes a column-name-list parameter.
- Paging: `GetPaged` injects `OrderBy`, `LIMIT`, `OFFSET`, runs both the records query and a `SELECT COUNT(*)` template (`BasicRepository.cs:432-485`).

### Query construction (`SqlBuilder.cs`, `WhereBuilder*.cs`)

`SqlBuilder` (`src/NzbDrone.Core/Datastore/SqlBuilder.cs`) is a string-template substitutor. Clauses supported:

| Clause | Method | Joiner / Prefix |
|--------|--------|-----------------|
| `/**select**/` | `Select(string)` | `, ` |
| `/**join**/` | `Join(string)` | `\nJOIN ` |
| `/**innerjoin**/` | `InnerJoin(string)` | `\nINNER JOIN ` |
| `/**leftjoin**/` | `LeftJoin(string)` | `\nLEFT JOIN ` |
| `/**rightjoin**/` | `RightJoin(string)` | `\nRIGHT JOIN ` |
| `/**where**/` | `Where(...)` | ` AND ` |
| (or-where, inclusive) | `OrWhere(...)` | groups to `( a OR b )` |
| `/**groupby**/` | `GroupBy` | ` , ` |
| `/**having**/` | `Having` | `\nAND ` |
| `/**orderby**/` | `OrderBy` | ` , ` |
| `/**intersect**/` | `Intersect` | `\nINTERSECT\n` |

`SqlBuilderExtensions` (`src/NzbDrone.Core/Datastore/Extensions/BuilderExtensions.cs`) layers strongly-typed wrappers: `Select(params Type[])` (looks up table names via `TableMapping`), `SelectDistinct`, `SelectCount`, `SelectCountDistinct<T>(prop)`, `Where<T>(Expression<Func<T,bool>>)`, `WherePostgres<T>`, `OrWhere<T>`, `Join<TLeft,TRight>(Expression<Func<TLeft,TRight,bool>>)`, `LeftJoin`, `InnerJoin`, `GroupBy<T>(Expression<Func<T,object>>)`, `AddSelectTemplate`, `AddPageCountTemplate`, `AddDeleteTemplate`. There's also a `LogQuery()` debug hook controlled by `SqlBuilderExtensions.LogSql`.

`WhereBuilder` (abstract, `WhereBuilder.cs`) and concrete `WhereBuilderPostgres` / `WhereBuilderSqlite` walk an `Expression` tree and emit:

- **Binary operators** (`VisitBinary`, `Decode`): `==` → `=` (or `IS` if RHS is null), `!=` → `<>` (or `IS NOT`), `<`, `<=`, `>`, `>=`, `&&`/`&` → `AND`, `||`/`|` → `OR`.
- **Method calls** (`VisitMethodCall`): `Contains` (string-ILIKE / LIKE wildcard, or list `IN (…)` / `= ANY (...)`), `StartsWith` (LIKE/ILIKE prefix), `EndsWith` (LIKE/ILIKE suffix). All other methods throw `NotImplementedException`.
- **Member access** — translated to `"Table"."Column"` if the member is on a mapped table, otherwise treated as a captured parameter.
- **Constants** — added as `@ClauseN_Pn` parameters via Dapper `DynamicParameters`.
- **Dialect deltas:**
  - Postgres uses `ILIKE`, SQLite uses `LIKE`.
  - Postgres uses `= ANY (@list)` / `= ANY ('{1,2,3}')` (literal int-list optimisation to bypass parameter limits, `WhereBuilderPostgres.cs:330-346`); SQLite uses `IN (@list)`.
  - Span/ReadOnlySpan implicit casts are unwrapped (`WhereBuilder.cs:12-23`).

### Lazy loading

`LazyLoaded<T>` (`src/NzbDrone.Core/Datastore/LazyLoaded.cs`) is a typed wrapper holding either a value or a closure that fetches one. Two flavours:

- `LazyLoaded<TChild>` — public type, used as the property type on entities. Implicitly converts to/from `TChild`.
- `LazyLoaded<TParent, TChild>` (internal) — the actual proxy. Holds `_query: Func<IDatabase, TParent, TChild>` and `_condition: Func<TParent, bool>` (e.g. `parent => parent.Id > 0`). Reflectively `Prepare`d with the live `IDatabase` + parent on every load.

Registration: `ColumnMapper<T>.HasOne(prop, idSelector)` for single-row child (`x => x.Id == parentForeignId`), and `ColumnMapper<T>.LazyLoad(prop, query, condition)` for arbitrary collection load. `TableMapping.cs:117-134` shows real registrations: `Series.QualityProfile` (`HasOne`), `EpisodeFile.Series` (`HasOne`), `EpisodeFile.Episodes` (collection via `SqlBuilder.Where<Episode>(c => c.EpisodeFileId == parent.Id)`), `Episode.EpisodeFile` (`HasOne`).

Invocation: every `db.Query<T>(...)` overload in `SqlMapperExtensions` runs `ApplyLazyLoad` against the result rows, which clones the registered `LazyLoadedProperty.LazyLoad` template and calls `Prepare(db, model)`. The actual SQL only fires when consumer code touches `.Value`.

`LazyLoadedConverterFactory` (`src/NzbDrone.Core/Datastore/LazyLoadedConverterFactory.cs`) is the System.Text.Json converter for `LazyLoaded<>` so it serialises as the raw inner value when loaded, `null` when unloaded — critical for the REST API not to trigger thousands of side-queries during JSON serialisation. Used by every controller that returns a model containing `LazyLoaded<>` fields.

## Connection management

- `ConnectionStringFactory` (`src/NzbDrone.Core/Datastore/ConnectionStringFactory.cs`) decides Postgres vs SQLite at startup based on `IConfigFileProvider` + env-var precedence (`PostgresMainDbConnectionString` > `PostgresHost`-vars > SQLite default). Three modes: `Sqlite`, `PostgreSqlVars`, `PostgreSqlConnectionString`.
- SQLite connection-string defaults (`ConnectionStringFactory.cs:57-76`): `CacheSize = -20000` (~20 MB), `DateTimeKind = Utc`, `JournalMode = WAL` on all OSes except OSX (which uses `Truncate` due to a documented OSX SQLite bug — `Full FSync` is also enabled there), `Pooling = true`, `BusyTimeout = 1000`.
- Postgres connection-string is built via `NpgsqlConnectionStringBuilder` with `Enlist = false` (disables System.Transactions ambient enlistment). When using vars: `Database`, `Host`, `Username`, `Password`, `Port`. When using a raw connection string the file's connection string is parsed and `Enlist = false` re-applied.
- `DbFactory.Create(MigrationContext)` (`src/NzbDrone.Core/Datastore/DbFactory.cs:62-109`) decides between SQLite/Postgres factory: returns a `Database` whose factory closure builds either `SQLiteFactory.Instance.CreateConnection()` + connection string, or a `new NpgsqlConnection(connStr)`.
  - Pooling is delegated to the underlying providers: SQLite uses `Pooling=true`; Npgsql does its own pooling internally.
  - Migrations run synchronously inside the factory: `_restoreDatabaseService.Restore()` → `_migrationController.Migrate(...)`. Postgres `SocketException` on connect retries 3× with 5 s sleep before bailing.
  - SQLite errors raise `CorruptDatabaseException` with an OSX-specific FAQ link.
  - For the **log** DB only, an SQLite migration failure deletes `*.db`, `*.db-shm`, `*.db-wal`, `*.db-journal` and retries (auto-recovery; main DB never auto-deletes).
- The static `DbFactory()` ctor sets four SQLite env vars (`No_Expand`, `No_SQLiteXmlConfigFile`, `No_PreLoadSQLite`, `No_SQLiteFunctions`) to skip System.Data.SQLite's slow init path (`DbFactory.cs:37-44`). It also calls `TableMapping.Map()` once.

`MainDatabase` and `LogDatabase` are thin wrappers over `IDatabase` with separate DI lifetimes. `MainDatabase.OpenConnection()` wraps the underlying connection in a MiniProfiler `ProfiledDbConnection` (Postgres path uses the custom `ProfiledImplementations.NpgSqlConnection` subclass).

`ProfiledImplementations.cs` exists solely so that Npgsql's bulk-copy / type-mapping reflection can recognise the wrapped connection as Npgsql-shaped. It's a single-class file: `NpgSqlConnection : ProfiledDbConnection`, no behaviour added. Without this, `Npgsql` features keyed off `connection.GetType()` would break behind the profiler wrapper.

## Embedded documents

Types implementing the marker interface `IEmbeddedDocument` (`src/NzbDrone.Core/Datastore/IEmbeddedDocument.cs`) are auto-registered for JSON serialisation by `TableMapping.RegisterEmbeddedConverter` (`TableMapping.cs:227-249`), which uses `ImplementationsOf<IEmbeddedDocument>()` reflection scan and registers both `T` and `List<T>` handlers. Concrete types (`grep IEmbeddedDocument`):

| Type | File | Stored in (table.column) |
|------|------|--------------------------|
| `Quality` | `Qualities/Quality.cs` | (used inside QualityModel) |
| `QualityModel` | `Qualities/QualityModel.cs` | `EpisodeFiles.Quality`, `History.Quality`, `Blocklist.Quality`, `PendingReleases.ParsedEpisodeInfo`, etc. |
| `MonitoringOptions` | `Tv/MonitoringOptions.cs` | `Series.AddOptions` |
| `Actor` | `Tv/Actor.cs` | (within Series JSON arrays) |
| `Ratings` | `Tv/Ratings.cs` | `Series.Ratings`, `Episodes.Ratings` |
| `Season` | `Tv/Season.cs` | `Series.Seasons` (list-typed) |
| `Language` | `Languages/Language.cs` | `EpisodeFiles.Languages`, `History.Languages`, `Blocklist.Languages`, `SubtitleFiles.LanguageTags` etc. (list) |
| `MediaCover` | `MediaCover/MediaCover.cs` | `Series.Images`, `Episodes.Images` (list) |
| `ProfileFormatItem` | `Profiles/ProfileFormatItem.cs` | `QualityProfiles.FormatItems` (list) |
| `QualityProfileQualityItem` | `Profiles/Qualities/QualityProfileQualityItem.cs` | `QualityProfiles.Items` (list) |
| `MediaInfoModel` | `MediaFiles/MediaInfo/MediaInfoModel.cs` | `EpisodeFiles.MediaInfo` |
| `MediaInfoAudioStreamModel` | `MediaFiles/MediaInfo/MediaInfoAudioStreamModel.cs` | within `MediaInfoModel` |
| `MediaInfoSubtitleStreamModel` | `MediaFiles/MediaInfo/MediaInfoSubtitleStreamModel.cs` | within `MediaInfoModel` |

In addition, ad-hoc converters are wired in `TableMapping.RegisterMappers` (`TableMapping.cs:179-213`) for: `List<int>`, `List<string>`, `Dictionary<string,string>`, `IDictionary<string,string>`, `HashSet<int>`, `KeyValuePair<string,int>`, `List<KeyValuePair<string,int>>`, `ParsedEpisodeInfo`, `ReleaseInfo`, `PendingReleaseAdditionalInfo`, plus `List<ICustomFormatSpecification>` and `List<IAutoTaggingSpecification>` via interface-aware converters. The full per-field JSON shape catalogue belongs in `analysis/embedded-docs/catalogue.md` (separate document per `analysis/README.md` layout).

JSON shape rules (`src/NzbDrone.Core/Datastore/Converters/EmbeddedDocumentConverter.cs:13-29`): `System.Text.Json`, camelCase property + dictionary keys, `WhenWritingNull` ignore for nulls, case-insensitive read, indented write, trailing-comma tolerant. Custom converters for enums (camelCase string), `TimeSpan`, UTC `DateTime`, plus per-handler converters injected via `params JsonConverter[]`.

## Migration system

### FluentMigrator hookup

Single entry point: `MigrationController.Migrate(connectionString, MigrationContext, DatabaseType)` (`src/NzbDrone.Core/Datastore/Migration/Framework/MigrationController.cs:29-88`). Per migration run it builds a fresh `ServiceCollection`, registers `AddNzbDroneSQLite()` or `AddPostgres15_0()` based on `DatabaseType`, scans the executing assembly with namespace filter `NzbDrone.Core.Datastore.Migration`, and invokes `runner.MigrateUp()` (or `MigrateUp(version)` if `MigrationContext.DesiredVersion` is set — used by tests).

Each migration class extends `NzbDroneMigrationBase` (`Datastore/Migration/Framework/NzbDroneMigrationBase.cs`) which gates on `MigrationContext.Current.MigrationType` to call either `MainDbUpgrade()` or `LogDbUpgrade()`. The base also exposes a `BeforeMigration` hook (used by tests to seed pre-migration state).

The companion extension `MigrationExtension.TableForModel(name)` (`Datastore/Migration/Framework/MigrationExtension.cs:20-23`) auto-prepends a `Id INT PK Identity` column on every table — this is why no migration ever defines `Id` explicitly.

### SQLite vs Postgres divergence

Kept in `Datastore/Migration/Framework/`:
- **`NzbDroneSQLiteProcessor`** (`NzbDroneSqliteProcessor.cs`) overrides `AlterColumnExpression`, `AlterDefaultConstraintExpression`, `DeleteColumnExpression`, `RenameColumnExpression`. SQLite has no native `ALTER COLUMN` / `DROP COLUMN` (in old versions), so the processor implements them by: dump schema, rebuild a `_temp` table, `INSERT … SELECT`, drop original, rename temp, recreate every index. (The schema dump is read by `SqliteSchemaDumper`.) This is the single most fragile piece of the C# datastore stack and the one Go's migration tooling has to replicate carefully.
- **`NzbDroneSQLiteTypeMap`** (`NzbDroneSQLiteTypeMap.cs`) — overrides FluentMigrator's `SQLiteTypeMap`. Notably maps `DbType.DateTimeOffset` to `DATETIME` (SQLite) so that the same migration that uses `AsDateTimeOffset()` produces TIMESTAMPTZ on Postgres but plain DATETIME on SQLite (this lets migration 188 work on both engines).
- **`SqliteSchemaDumper`** — reads `sqlite_master` to reconstruct a `TableDefinition` for `ProcessAlterTable` to rewrite.
- **`SqliteSyntaxReader`** — token-level parser for the original `CREATE TABLE` SQL so column constraints survive table rebuilds.
- **`TableDefinition`** — internal DTO for the dumper.
- **`MigrationExtension.AddNzbDroneSQLite()`** — DI wires `NzbDroneSQLiteProcessor`, `NzbDroneSQLiteTypeMap`, and a `binaryGuid: false` quoter (Postgres uses `AddPostgres15_0()` from FluentMigrator out of the box).

Engine-specific SQL is otherwise written as raw `Execute.Sql(...)`. Migration 188 (`postgres_update_timestamp_columns_to_with_timezone`) is the largest dialect-aware schema retrofit — it replaces every `DATETIME` column on time-bearing tables with `DATETIMEOFFSET` (i.e. `TIMESTAMPTZ` on Postgres, no-op on SQLite due to the type-map override).

### Version range

Lowest: `[Migration(0)]` `database_engine_version_check` (`000_database_engine_version_check.cs`). Initial schema is `[Migration(1)]` `InitialSetup`. Highest currently in tree: `[Migration(230)]` `add_mapping_id_to_scene_mappings`. There are 8 missing version numbers (**80, 133, 208, 213, 216, 222, 228, 229**) plus one empty no-op (214, body is `// Replaced with 215`). Total file count is **223**. Version numbers ≠ migration count. (An earlier draft of this card listed 102 and 169 as gaps; both files actually exist — corrected 2026-05-02.)

### Irreversible migrations

`NzbDroneMigrationBase.Down()` always throws `NotImplementedException` (`NzbDroneMigrationBase.cs:61-64`). Therefore **every migration is one-way**. Reversible-by-design migrations don't exist in this codebase. A few migrations are irreversible at the **data** layer too — they delete/coerce state with no recovery path:

- `017_reset_scene_names.cs` — clears scene names.
- `021_drop_seasons_table.cs` — drops the standalone `Seasons` table; data folded into `Series.Seasons` JSON.
- `054_rename_profiles.cs` + `055_drop_old_profile_columns.cs` — schema reshape.
- `057_convert_episode_file_path_to_relative.cs` / `058_drop_epsiode_file_path.cs` — full path → relative path coercion (irreversible if Series moved on disk).
- `061_clear_bad_scene_names.cs` — destructive cleanup.
- `062_convert_quality_models.cs` — quality enum re-numbering, JSON rewrite.
- `068_add_release_restrictions.cs` — copies + deletes `Config` row.
- `073_clear_ratings.cs` — wipes ratings.
- `074_disable_eztv.cs`, `087_remove_eztv.cs`, `090_update_kickass_url.cs`, `096_disable_kickass.cs`, `098_remove_titans_of_tv.cs`, `104_remove_kickass.cs`, `107_remove_wombles.cs`, `116_disable_nyaa.cs`, `121_update_animetosho_url.cs`, `138_remove_bitmetv.cs`, `145_remove_growl.cs`, `152_update_btn_url_to_https.cs`, `161_remove_plex_hometheater.cs`, `173_remove_omg.cs`, `184_remove_invalid_roksbox_metadata_images.cs`, `187_remove_eztv` (removed indexers / disabled providers).
- `103_fix_metadata_file_extensions.cs` / `108_fix_extra_file_extension.cs` / `108_fix_extra_files_config.cs` / `110_fix_extra_files_config.cs` — irreversible filename rewrites.
- `175_language_profiles_to_custom_formats.cs` — drops `LanguageProfiles` table, drops `LanguageProfileId` columns, collapses scalar `Language` into JSON `Languages` array.
- `188_postgres_update_timestamp_columns_to_with_timezone.cs` — `DELETE FROM Commands` then alters every timestamp column.

### Migrations that touch embedded-document JSON

These rewrite the JSON inside columns and need byte-for-byte parity in the Go port:

- `036_update_with_quality_converters.cs` — first quality-model JSON conversion.
- `037_add_configurable_qualities.cs` / `069_quality_proper.cs` / `071_unknown_quality_in_profile.cs` / `084_update_quality_minmax_size.cs` — QualityProfile JSON shape changes.
- `046_fix_nzb_su_url.cs`, `049_fix_dognzb_url.cs`, `082_add_fanzub_settings.cs`, `085_expand_transmission_urlbase.cs`, `088_pushbullet_devices_channels_list.cs`, `090_update_kickass_url.cs`, `108_fix_extra_file_extension.cs`, `121_update_animetosho_url.cs`, `152_update_btn_url_to_https.cs`, `158_cdh_per_downloadclient.cs`, `218_telegram_link_preview.cs`, `219_nzb_su_url_to_nzb_life.cs` — provider `Settings` JSON rewrites.
- `053_add_series_sorttitle.cs`, `166_update_series_sort_title.cs`, `183_update_images_remote_url.cs` — Series JSON or Images JSON.
- `062_convert_quality_models.cs` — table-wide QualityModel JSON shape transition.
- `072_history_grabid.cs` — History data JSON.
- `074_disable_eztv.cs`, `116_disable_nyaa.cs` — toggles flags inside JSON `Settings`.
- `086_pushbullet_device_ids.cs`, `088_pushbullet_devices_channels_list.cs`, `157_email_multiple_addresses.cs`, `201_email_encryption.cs` — notification settings.
- `148_mediainfo_channels.cs` / `163_mediainfo_to_ffmpeg.cs` / `225_mediainfo_multiple_streams.cs` — `EpisodeFiles.MediaInfo` JSON migrations.
- `162_release_profile_to_array.cs` — converts `Required` / `Ignored` from comma-separated text to JSON arrays.
- `171_add_custom_formats.cs`, `175_language_profiles_to_custom_formats.cs`, `224_release_profile_indexer_ids.cs` — release-profile + custom-format JSON shape.
- `181_quality_definition_preferred_size.cs` — QualityDefinitions JSON.
- `182_add_bypass_to_delay_profile.cs`, `205_rename_season_pack_spec.cs` — DelayProfile JSON.
- `194_add_notification_status.cs`, `195_parse_language_tags_from_existing_subtitle_files.cs`, `198_parse_titles_from_existing_subtitle_files.cs` — subtitle-file metadata.
- `212_add_minium_upgrade_format_score_to_quality_profiles.cs`, `220_enable_season_pack_seeding_goal.cs`, `221_add_exclusion_tags_to_release_profiles.cs`, `226_add_air_date_filtering_to_release_profiles.cs`.

The full per-migration rewrite contract is the natural fit for `analysis/migrations/ledger.md`.

## Database restoration / backup hooks

`DatabaseRestorationService` (`src/NzbDrone.Core/Datastore/DatabaseRestorationService.cs`) implements `IRestoreDatabase`. It runs **once per main-DB open** at the start of `DbFactory.CreateMain` (`DbFactory.cs:115`), before migrations. It checks for a file at `IAppFolderInfo.GetDatabaseRestore()` (e.g. `sonarr.db.restore`); if present, it deletes the live DB plus `-shm`/`-wal`/`-journal` and `MoveFile()`s the restore into place. This is how Sonarr's "Restore from Backup" UI works — the BackupService writes a `.restore` sidecar file, and on next startup the restoration kicks in. Failures are logged and rethrown (the app won't start with a partial restore).

It does **not** restore the log DB.

## Tables (list of)

Final tables in a fully-migrated DB (cross-referencing `TableMapping.cs` registrations against `TableForModel("…")` migrations and `Delete.Table` removals). Indexes from `045_add_indexes`, `095_add_additional_episodes_index`, `120_update_series_episodes_history_indexes`. Primary keys are auto-generated `Id INTEGER` (SQLite) / `Id INTEGER GENERATED ALWAYS AS IDENTITY` (Postgres) via `TableForModel`.

| Table | Notes / SQLite type → Postgres type | PK | Notable indexes |
|-------|-------------------------------------|----|-----------------|
| `VersionInfo` | FluentMigrator metadata table; `Version BIGINT`, `AppliedOn DATETIME→TIMESTAMPTZ`, `Description TEXT` | (composite, FluentMigrator-owned) | — |
| `Config` | `Key TEXT UNIQUE`, `Value TEXT` | Id | implicit unique on Key |
| `RootFolders` | `Path TEXT UNIQUE` | Id | unique Path |
| `Series` | wide; many `LazyLoaded`/JSON columns: `Images`, `Seasons`, `Ratings`, `Genres`, `Tags`, `AddOptions`, `LastInfoSync DATETIME→TIMESTAMPTZ`, `LastDiskSync DATETIME→TIMESTAMPTZ`, `FirstAired DATETIME→TIMESTAMPTZ`, `NextAiring DATETIME→TIMESTAMPTZ`, `Added DATETIME→TIMESTAMPTZ`, `LastAired DATETIME→TIMESTAMPTZ` | Id | `Path`, `CleanTitle`, `TvRageId`, `TvdbId` (unique → de-uniqued in 002), `TitleSlug` (unique), `ImdbId` (unique → de-uniqued in 002), `TmdbId`, `TvMazeId` |
| `Episodes` | `SeriesId INT`, `SeasonNumber INT`, `EpisodeNumber INT`, `AbsoluteEpisodeNumber INT?`, `AirDate TEXT`, `AirDateUtc DATETIME→TIMESTAMPTZ?`, `EpisodeFileId INT?`, `LastSearchTime DATETIME→TIMESTAMPTZ?`, `Runtime INT`, `Images JSON`, `Ratings JSON`, `Monitored BOOL`, `FinaleType TEXT?`, `AirDateBefore TEXT?` | Id | `EpisodeFileId`, `SeriesId`, composite (Series, Season, EpisodeNumber) per 095, also (Series, AirDateUtc, …) per 120 |
| `EpisodeFiles` | `Path TEXT` (originally absolute, since 057 relative), `Quality JSON`, `MediaInfo JSON`, `Languages JSON`, `Size BIGINT`, `DateAdded DATETIME→TIMESTAMPTZ`, `OriginalFilePath TEXT`, `RelativePath TEXT` | Id | `SeriesId` |
| `History` | `EpisodeId`, `SeriesId`, `SourceTitle`, `Quality JSON`, `Languages JSON`, `Date DATETIME→TIMESTAMPTZ`, `EventType INT`, `Data JSON`, `DownloadId TEXT?`, `IndexerFlags INT?` | Id | `EpisodeId`, `Date`, `(EpisodeId,Date)` per 120 |
| `Notifications` | `Name`, `OnGrab BOOL`, `OnDownload BOOL`, `OnUpgrade`, `OnRename`, `OnEpisodeFileDelete*`, `OnSeriesAdd`, `OnSeriesDelete`, `OnHealthIssue`, `OnHealthRestored`, `OnApplicationUpdate`, `OnManualInteractionRequired`, `OnImportComplete`, `IncludeHealthWarnings BOOL`, `Implementation TEXT`, `ConfigContract TEXT`, `Settings JSON`, `Tags JSON list<int>` | Id | — |
| `ScheduledTasks` | `TypeName UNIQUE`, `Interval`, `LastExecution DATETIME→TIMESTAMPTZ`, `LastStartTime DATETIME→TIMESTAMPTZ?`, `Duration INT?` (180) | Id | unique TypeName |
| `Indexers` | `Name UNIQUE`, `Implementation`, `ConfigContract`, `Settings JSON`, `EnableRss`, `EnableInteractiveSearch`, `EnableAutomaticSearch`, `Priority`, `DownloadClientId`, `Tags JSON`, `SeasonSearchMaximumSingleEpisodeAge` | Id | unique Name |
| `IndexerStatus` | `IndexerId UNIQUE`, `InitialFailure DATETIMEOFFSET?`, `MostRecentFailure?`, `EscalationLevel`, `DisabledTill?`, `Cookies JSON?`, `CookiesExpirationDate?` | Id | unique IndexerId |
| `DownloadClients` | similar shape, `Settings JSON`, `Priority`, `RemoveCompletedDownloads`, `RemoveFailedDownloads`, `Tags JSON` | Id | unique Name |
| `DownloadClientStatus` | mirrors IndexerStatus | Id | unique on FK |
| `ImportLists` | `Settings JSON`, `EnableAutomaticAdd`, `ShouldMonitor`, `ShouldMonitorExisting`, `ShouldSearch`, `RootFolderPath`, `QualityProfileId`, `Tags JSON`, `SeriesType`, `SeasonFolder` | Id | — |
| `ImportListStatus` | mirrors IndexerStatus | Id | unique FK |
| `ImportListItems` | `ImportListId`, `ServiceProviderId`, `Title`, `ReleaseDate DATETIMEOFFSET?` | Id | — |
| `ImportListExclusions` | `TvdbId UNIQUE`, `Title` | Id | unique TvdbId; `Type` since 192 |
| `Metadata` | `Name`, `Implementation`, `ConfigContract`, `Settings JSON`, `Enable BOOL` | Id | — |
| `MetadataFiles` | `SeriesId`, `Consumer`, `Type`, `RelativePath`, `LastUpdated DATETIMEOFFSET`, `EpisodeFileId?`, `Hash`, `Added DATETIMEOFFSET?`, `Extension` | Id | — |
| `SubtitleFiles` | similar to MetadataFiles, `Language INT` (then `LanguageTags JSON`) | Id | — |
| `ExtraFiles` | similar | Id | — |
| `QualityDefinitions` | `Quality INT UNIQUE`, `Title`, `MinSize REAL?`, `MaxSize REAL?`, `PreferredSize REAL?` | Id | unique Quality |
| `QualityProfiles` | `Name UNIQUE`, `Cutoff INT`, `Items JSON`, `UpgradeAllowed BOOL`, `MinFormatScore INT`, `CutoffFormatScore INT`, `MinUpgradeFormatScore INT`, `FormatItems JSON` | Id | unique Name |
| `CustomFormats` | `Name UNIQUE`, `IncludeCustomFormatWhenRenaming BOOL`, `Specifications JSON` | Id | unique Name |
| `NamingConfig` | `RenameEpisodes BOOL`, `ReplaceIllegalChars BOOL`, `ColonReplacementFormat INT`, `CustomColonReplacementFormat TEXT`, `MultiEpisodeStyle INT`, `StandardEpisodeFormat`, `DailyEpisodeFormat`, `AnimeEpisodeFormat`, `SeriesFolderFormat`, `SeasonFolderFormat`, `SpecialsFolderFormat` | Id | — |
| `Blocklist` | (renamed from `Blacklist` in 160) `SeriesId`, `EpisodeIds JSON`, `SourceTitle`, `Quality JSON`, `Date DATETIMEOFFSET`, `PublishedDate DATETIMEOFFSET?`, `Size BIGINT?`, `Protocol INT`, `Indexer`, `Message`, `TorrentInfoHash`, `Languages JSON`, `IndexerFlags INT`, `Source INT?` (since 223) | Id | `SeriesId` |
| `PendingReleases` | `SeriesId`, `Title`, `Added DATETIMEOFFSET`, `ParsedEpisodeInfo JSON`, `Release JSON`, `Reason INT`, `AdditionalInfo JSON` | Id | — |
| `RemotePathMappings` | `Host`, `RemotePath`, `LocalPath` | Id | — |
| `Tags` | `Label UNIQUE` | Id | unique Label |
| `ReleaseProfiles` | `Required JSON array`, `Ignored JSON array`, `Tags JSON`, `IndexerId INT` (or `IndexerIds JSON` since 224), `Enabled BOOL`, `Name?`, `IncludePreferredWhenRenaming BOOL`, `MinAirDate?`, `MaxAirDate?`, `ExclusionTags JSON` | Id | — |
| `DelayProfiles` | `EnableUsenet`, `EnableTorrent`, `PreferredProtocol`, `UsenetDelay INT`, `TorrentDelay INT`, `Order`, `Tags JSON`, `BypassIfHighestQuality BOOL`, `BypassIfAboveCustomFormatScore BOOL`, `MinimumCustomFormatScore INT` | Id | — |
| `Users` | `Identifier UNIQUE`, `Username UNIQUE`, `Password`, `Salt` (since 174), `Iterations` | Id | unique Username/Identifier |
| `Commands` | `Name`, `Body JSON`, `Priority`, `Status`, `QueuedAt DATETIMEOFFSET`, `StartedAt DATETIMEOFFSET?`, `EndedAt DATETIMEOFFSET?`, `Duration TIMESPAN?`, `Exception?`, `Trigger INT`, `Result INT` (since 186) | Id | — |
| `CustomFilters` | `Type`, `Label`, `Filters JSON` | Id | — |
| `DownloadHistory` | `EventType INT`, `SeriesId`, `DownloadId`, `SourceTitle`, `Date DATETIMEOFFSET`, `Protocol INT`, `IndexerId INT`, `DownloadClientId INT`, `Release JSON`, `Data JSON` | Id | `(SeriesId,Date)`, `(DownloadId)` |
| `UpdateHistory` | (log DB) `Date DATETIMEOFFSET`, `Version`, `EventType` | Id | — |
| `SceneMappings` | `Title`, `ParseTerm`, `SearchTerm`, `Type`, `TvdbId`, `SeasonNumber INT?`, `SceneSeasonNumber INT?`, `FilterRegex?`, `MappingId INT?` (since 230) | Id | — |
| `NotificationStatus` | mirrors IndexerStatus | Id | — |
| `AutoTagging` | `Name UNIQUE`, `Specifications JSON`, `RemoveTagsAutomatically BOOL`, `Tags JSON` | Id | unique Name |
| `Logs` (log DB) | `Message`, `Time DATETIMEOFFSET`, `Logger`, `Method?`, `Exception?`, `ExceptionType?`, `Level` | Id | `Time` (since 006) |

Tables removed by migrations (do **not** exist in fully-migrated DB): `Seasons` (021), `QualitySizes` (037 → `QualityDefinitions`), `LanguageProfiles` (175). Renamed: `Blacklist` → `Blocklist` (160), `Profiles` → `QualityProfiles` (054 + 055), `Restrictions` → `ReleaseProfiles` (127). Migration 175 also dropped `Series.LanguageProfileId` and `ImportLists.LanguageProfileId`.

## Notable quirks / TODOs / HACKs

- `src/NzbDrone.Core/Datastore/Migration/171_add_custom_formats.cs:41` — `// TODO: Kill any references to Preferred in History and Files`.
- `src/NzbDrone.Core/Datastore/Migration/057_convert_episode_file_path_to_relative.cs:15` — `// TODO: Add unique constraint for series ID and Relative Path`.
- `src/NzbDrone.Core/Datastore/Migration/057_convert_episode_file_path_to_relative.cs:16` — `// TODO: Warn if multiple series share the same path`.
- `ConnectionStringFactory.cs:64` — OSX SQLite must use `Truncate` journal mode + `Full FSync` (workaround for an OSX-specific corruption mode; documented via the FAQ link in `DbFactory.cs:124`).
- `DbFactory.cs:39-44` — four `No_*` env vars set to skip System.Data.SQLite slow init paths.
- `DbFactory.cs:131-156` — `SocketException` retry loop on Postgres (3 × 5 s) — the only network-aware behaviour in the file; throws `SonarrStartupException` on exhaustion.
- `DbFactory.cs:178-193` — Log DB auto-recovery deletes the file and retries; main DB never does.
- `BasicRepository.cs:56-71` — Polly retry only handles SQLite `BUSY`. Postgres deadlocks/serialisation failures don't retry.
- `BasicRepository.cs:202-208` — INSERT SQL is dialect-coupled (Postgres `RETURNING "Id"` vs SQLite `last_insert_rowid()`).
- `WhereBuilderPostgres.cs:330-346` — int-list `Contains` compiles to a literal `('{1,2,3}')` ANY-array to bypass parameter limits, **only** when the value is statically resolvable.
- `WhereBuilderPostgres.cs:354-358` — string `Contains` becomes case-insensitive `ILIKE` automatically. Tests must check this is the intended Postgres semantic; SQLite's `LIKE` is case-insensitive only on ASCII by default.
- `NzbDroneSQLiteTypeMap.cs:46` — `DbType.DateTimeOffset` → `DATETIME` is the dialect-bridge that lets `AsDateTimeOffset()` work on both engines.
- `NzbDroneSqliteProcessor.cs:144-171` — table-rebuild dance for ALTER COLUMN / DROP COLUMN / RENAME COLUMN. Index regeneration is manual and depends on `SqliteSchemaDumper` round-tripping the CREATE TABLE SQL through `SqliteSyntaxReader`.
- `NzbDroneMigrationBase.cs:62-64` — `Down()` always throws. Forward-only by policy.
- `MigrationController.cs:71` — `MigrationContext.Current` is a static mutable global used to pass per-migration hooks (e.g. `BeforeMigration`). Race-prone if migrations were ever run concurrently.
- `TableMapping.cs:217-225` — `ProviderSettingConverter` is registered for **every** type implementing `IProviderConfig` via `ImplementationsOf<>`. Adding a new provider settings type without updating mapping registration silently breaks Dapper round-trip.
- `LazyLoaded.cs:113-134` — `LazyLoad()` stores results into `_value` regardless of failure mode; concurrent `.Value` access is not synchronised.

## Test files

Datastore fixtures (under `src/NzbDrone.Core.Test/Datastore/`):

- `BasicRepositoryFixture.cs`
- `DatabaseFixture.cs`
- `DatabaseRelationshipFixture.cs` (lazy-loading scenarios)
- `DatabaseVersionParserFixture.cs`
- `ObjectDatabaseFixture.cs`
- `TableMapperFixture.cs`
- `WhereBuilderPostgresFixture.cs`
- `WhereBuilderSqliteFixture.cs`
- `Converters/CommandConverterFixture.cs`
- `Converters/DictionaryConverterFixture.cs`
- `Converters/GuidConverterFixture.cs`
- `Converters/OsPathConverterFixture.cs`
- `Converters/ProviderSettingConverterFixture.cs`
- `Converters/QualityIntConverterFixture.cs`
- `Converters/TimeSpanConverterFixture.cs`
- `Converters/UtcConverterFixture.cs`
- `SqliteSchemaDumperTests/` (table-definition round-trip)
- `Migration/*` — 40 per-migration fixtures (e.g. `162_release_profile_to_array_Fixture.cs`-style names); collectively the closest thing to a per-migration golden dataset and indispensable for porting verification.

Framework helpers (under `src/NzbDrone.Core.Test/Framework/`):

- `CoreTest.cs`
- `DbTest.cs` (base for DB-using fixtures)
- `DbTestCleanup.cs`
- `DirectDataMapper.cs` (raw row reads bypassing the ORM)
- `MigrationTest.cs` (runs a single migration with a `BeforeMigration` hook)
- `NBuilderExtensions.cs`
- `TestDatabase.cs`

The Go port must keep equivalent coverage of every `WhereBuilder*Fixture.cs` query (these are the dialect-divergence regression net), plus the per-migration fixtures (golden output for the JSON-rewriting migrations).

## Risks for the Go port

### What Marr.Data / Dapper give us that `database/sql` does not

Marr.Data is not actually used; the substantive losses from dropping Dapper are:

- **Strongly-typed parameter expansion.** Dapper turns `IEnumerable<int>` into the right `IN (@a,@b,@c)` expansion (or `= ANY(...)` for Npgsql). Go needs explicit handling — pgx supports `=ANY($1)` natively, but `database/sql` + `modernc.org/sqlite` does not auto-expand slices.
- **Type handlers.** `SqlMapper.AddTypeHandler<T>` registers a single converter that fires for every `T`-typed column read or written, including JSON-blob round-tripping for every `IEmbeddedDocument`. Go's `sql.Scanner` / `driver.Valuer` give similar mechanics but per-type implementation must be explicit.
- **`QueryMultiple` / multi-result mapping.** `BasicRepository.Insert` reads `last_insert_rowid()` from a multi-result set after a single round-trip. Pgx supports this; modernc.org/sqlite does too via row.LastInsertId(), but the unified abstraction is gone.
- **Dynamic parameters & template merging.** `SqlBuilder.Template` rebuilds `DynamicParameters` from multiple clause objects, including anonymous-type capture. Go has nothing equivalent; the port has to compose `[]any` parameter slices and re-number `$N` placeholders for Postgres.
- **Multi-table joined materialisation.** `Query<T1,T2,TReturn>` maps a row across two materialised entities split on `Id`. The Go port has to either generate pairwise scanners or use `pgx`'s `RowToStructByName` and split SQL across distinct prefix groups.

### What FluentMigrator gives us that `pressly/goose` does not

- **Fluent dialect-neutral schema DSL** (`Create.TableForModel("X").WithColumn("Y").AsString().Nullable().Indexed()`). Goose uses raw per-engine SQL files. A Go port would either commit to writing each migration twice (once per engine) or invent a small DSL.
- **Custom processors.** `NzbDroneSQLiteProcessor` overrides ALTER COLUMN / DROP COLUMN / RENAME COLUMN to do the SQLite table-rebuild dance. Goose has no analogue — the port must hand-roll the rebuild or generate the SQL up-front.
- **`Execute.WithConnection(Action<conn,tran>)`** lets a migration run arbitrary code with a typed `IDbConnection` + transaction. Goose supports Go-language migrations via `goose.AddMigration`, which is the equivalent escape hatch but every JSON-rewriting migration becomes a hand-written Go function.
- **Type-map override** (`NzbDroneSQLiteTypeMap` mapping `DbType.DateTimeOffset` to `DATETIME`) is how the same migration source produces `DATETIME` on SQLite and `TIMESTAMPTZ` on Postgres. With Goose, the port must split into two migration files per engine OR generate.
- **Forward-only by base class.** Goose has up/down support; the port can simply leave `Down()` empty.
- **Maintenance migrations** (`IncludeUntaggedMaintenances = true`) — currently unused but available for repeat-on-every-startup tasks.

### What Dapper gives us that we'd lose

Beyond the above: column-name-from-property auto-mapping (Go alternatives like `sqlx`, `pgx.RowToStructByName`, or `scany` cover this); multi-row `Execute` for bulk update with `IList<T>` argument (pgx's `CopyFrom` is the moral equivalent for INSERT but UPDATE/DELETE bulk needs explicit `unnest` queries); first-class `dynamic`-parameter coercion of mixed `object` parameter bags.

### DB-engine-specific SQL the port must handle

- **String matching:** Postgres `ILIKE` vs SQLite `LIKE` (the latter is ASCII-case-insensitive by default; Sonarr relies on this to match `WhereBuilderSqlite` semantics).
- **List membership:** Postgres `= ANY (@list)` / `= ANY ('{1,2,3}')` vs SQLite `IN (@list)`.
- **Timestamp types:** Postgres `TIMESTAMPTZ` (after migration 188) vs SQLite `DATETIME` text. Time-zone handling: Sonarr stores UTC strings in SQLite via `DapperUtcConverter`; pgx returns `time.Time` UTC for TIMESTAMPTZ. Need symmetric coercion at the boundary.
- **Boolean storage:** SQLite stores `INTEGER` 0/1; Postgres stores `BOOLEAN`. Go drivers handle this transparently if columns are typed correctly.
- **Identity / RETURNING:** Postgres `RETURNING "Id"` vs SQLite `last_insert_rowid()`. Two distinct INSERT SQL strings; affects every `Insert` and `InsertMany`.
- **Quoting:** Both engines accept double-quoted identifiers in Sonarr's queries; Postgres is case-sensitive when quoted.
- **JSON columns:** Stored as `TEXT` on both engines (no `JSONB`). The port can keep this for parity, or upgrade Postgres to `JSONB` — but that's a behaviour change that affects every embedded-document migration.
- **Vacuum:** Both support `VACUUM`, but Postgres `VACUUM` cannot run inside a transaction. `BasicRepository.Vacuum` must therefore commit before issuing VACUUM on Postgres.
- **Migration 188's `DateTimeOffset` magic** depends on the type-map override; without an equivalent the port must double-write migrations.

## Open questions

- See `analysis/questions.md`:
  - Q-01: How should the Go port reproduce Dapper's per-type handler registration for `IEmbeddedDocument` types — interface-driven reflection scan at startup, or hand-written `Scanner`/`Valuer` per type?
  - Q-02: Does Sonarr rely on SQLite `LIKE` being ASCII-case-insensitive (`WhereBuilderSqlite` emits `LIKE`, no `COLLATE`), or do existing tests force consistent casing? If the former, modernc.org/sqlite must use the same default `PRAGMA case_sensitive_like = OFF`.
  - Q-03: Are there any Postgres extensions Sonarr requires (e.g. `citext`, `pg_trgm`)? Initial scan shows none, but should be verified before claiming `AddPostgres15_0()` parity is sufficient.
  - Q-04: The `MigrationContext.Current` static is mutated per-migration. Are migrations ever run concurrently in tests? If yes, the port's equivalent needs explicit locking or context-passing.
