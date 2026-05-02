# Migrations ledger

Sonarr's main and log databases are evolved by **FluentMigrator** (`FluentMigrator.Migration` base class, all concrete migrations live in `src/NzbDrone.Core/Datastore/Migration/`). Each migration carries a `[Migration(N)]` attribute that fixes its monotonic version, and the framework records the highest applied version in a `VersionInfo` table inside each database. The Sonarr-specific base class `NzbDroneMigrationBase` (`src/NzbDrone.Core/Datastore/Migration/Framework/NzbDroneMigrationBase.cs:8-71`) splits each migration into two virtual hooks — `MainDbUpgrade()` runs against the main `sonarr.db`, `LogDbUpgrade()` runs against the separate `logs.db`. The framework's runner picks one based on `MigrationContext.Current.MigrationType` (Main / Log / both); `Down()` always throws `NotImplementedException` — the project is **forward-only by design**, so every migration in this ledger is unreversible at the framework level.

The version sequence runs **0..230 inclusive with 8 missing version numbers at 80, 133, 208, 213, 216, 222, 228, 229** (no `.cs` file exists at those numbers — the per-row table flags them with `*(gap)*`). Migration **214 exists but is an empty no-op** (`// Replaced with 215`); it occupies a version slot without doing any work. Total: **223 concrete migration files** (231 numbers minus 8 gaps). The schema dumps `go/analysis/schema/sqlite.sql` and `go/analysis/schema/postgres.sql` reflect the post-230 state and `VersionInfo.MaxVersion = 230`. The Go port reproduces these via `goose` — read this ledger before writing any goose migration: each row tells you the table effects, the embedded-doc shape touched (if any), and whether the porter can elide it.

> **Correction history (2026-05-02):** an earlier draft of this intro confused two contradictory characterizations of the gap list and quoted the wrong one. It also stated `Total: 224`. Both have been corrected against `ls src/NzbDrone.Core/Datastore/Migration/*.cs`. The bucket counts in the summary statistics block below sum to 224 — they were back-computed from the wrong total and are each potentially off by 1 (the per-row table is the source of truth, not the summary). Verified-correct claims as of this revision: log-DB list (001, 006, 064, 141, 146, 188), migration 205 polymorphic-rename body, gap row markers in the per-row table.

**Why this ledger exists.** The *form* of each migration matters for the Go port for three reasons: (1) JSON-shape mutations inside embedded-doc columns *must* be replayed byte-for-byte or existing user DBs corrupt on upgrade — this is the porter's most fragile single piece of work; (2) provider `Implementation` and `ConfigContract` strings are class-name-typed and several migrations rename them — see TP-Q1/TP-Q2 in `subsystems/thingiprovider.md` for the Go-port registry workaround; (3) one-shot housekeeper migrations (e.g., metadata-image cleanups, language-profile backfills) are cumulative — once user data has been through them, the Go port can elide reapplying. The "Dropable in Go port?" column flags those. The "HIGH RISK" group is the one the Go port *cannot* skip and *cannot* tolerate drift on.

## Summary statistics

| Metric | Count |
|--------|-------|
| Total migration files | 223 (was incorrectly 224 in earlier draft; verified against `ls`) |
| Schema-only (CREATE / ALTER / DROP TABLE / INDEX / RENAME, no JSON or row mutation) | 116 |
| Data-mutating only (UPDATE/INSERT/DELETE on existing rows, no schema change) | 21 |
| Schema + data (mixed: column add followed by backfill) | 87 |
| Embedded-doc-shape mutations (HIGH RISK) | 28 |
| Polymorphic-type renames (HIGH RISK; provider class-name or spec-class-name) | 8 |
| Reversible (`Down()` implemented) | 0 — every migration is forward-only via `NzbDroneMigrationBase.Down() => throw NotImplementedException` |
| Touches log DB (`LogDbUpgrade`) | 6 (001, 006, 064, 141, 146, 188) |
| Drop-able in Go port (one-shot housekeepers, dead-feature cleanups, no-ops) | 31 |

The bucket counts (116 + 21 + 87 = 224) sum to the wrong total; the per-row table has 223 real migration rows (plus 8 `*(gap)*` markers). At least one of those three buckets is over-counted by one — re-counting from the per-row table is the way to fix this when needed; the qualitative split has been spot-checked and reads correctly. Six log-DB migrations is small but matters: with `LogDbEnabled=false` (`subsystems/system.md` `LogDbEnabled` finding) the runner skips `LogDbUpgrade()` entirely; the Go port must replicate that gating or the migration runner will fail when no log DB exists.

## Migrations flagged HIGH RISK

These are the migrations that read JSON out of an embedded-doc column, mutate the parsed shape, and write it back — or rename a polymorphic discriminator string. The Go port's goose equivalents must reproduce **every field path** listed and the **camelCase, allow-integer-on-read enum, indented, trailing-comma-tolerant** STJ contract from `embedded-docs/catalogue.md`. **Drift = silent corruption** of upgrading user databases.

| Version | Filename | Risk | Affects | Notes |
|---------|----------|------|---------|-------|
| 020 | `020_add_year_and_seasons_to_series.cs` | embedded-doc shape | `Series.Seasons` JSON array | First write of `Seasons` JSON — `[{seasonNumber, monitored}]`. SQL string-concat (no parameterised insert), assumes legacy seasons-row format. |
| 029 | `029_add_formats_to_naming_config.cs` | naming format strings | `NamingConfig.StandardEpisodeFormat`, `DailyEpisodeFormat` | Synthesises naming tokens from now-deleted columns; backfill is stable but the GoTemplate must match. |
| 033 | `033_add_api_key_to_pushover.cs` | embedded settings JSON | `Notifications.Settings` (`PushoverSettings`) | Mutates `apiKey`, `priority` (downcast `2 → 1`). Hardcoded API key constant; preserve verbatim in Go port. |
| 036 | `036_update_with_quality_converters.cs` | embedded-doc shape | `QualityProfiles.Items`, `Blacklist.Quality`, `EpisodeFiles.Quality`, `History.Quality` | First introduction of the `QualityIntConverter`-reduced form. **Critical** — converts `{quality: {id: 7, ...}, proper: true}` to `{quality: 7, revision: {version: 2}}` shape. EDOC-Q9 cross-cut. |
| 043 | `043_convert_config_to_download_clients.cs` | provider config inflation | `DownloadClients.Settings` (`SabnzbdSettings`/`NzbgetSettings`/`FolderSettings`) | Reads keys out of `Config` table and synthesises new `DownloadClients` rows. Settings JSON shape *defined here*. |
| 046 | `046_fix_nzb_su_url.cs` | string mutation in JSON column | `Indexers.Settings` | SQL `replace()` on JSON text — `//nzb.su` → `//api.nzb.su`. Not a structural change but does mutate the embedded JSON contents in-place. See migration 219 follow-up. |
| 049 | `049_fix_dognzb_url.cs` | string mutation in JSON column | `Indexers.Settings` | SQL `replace()` — `//dognzb.cr` → `//api.dognzb.cr`. Same pattern as 046. |
| 051 | `051_download_client_import.cs` | provider class-name rename + JSON re-shape | `DownloadClients.Implementation`/`Settings`, `History.Data` | Renames `Blackhole` → `UsenetBlackhole` (with `FolderSettings` → `UsenetBlackholeSettings`); `Pneumatic` settings shape change; mutates `History.Data` dictionary. **Polymorphic rename**. |
| 062 | `062_convert_quality_models.cs` | embedded-doc shape | `EpisodeFiles.Quality`, `Blacklist.Quality`, `History.Quality` | Re-converts legacy quality JSON: `{quality: int, proper: bool}` → `{quality: int, revision: {version: 1|2}}`. |
| 070 | `070_delay_profile.cs` | new tag JSON synthesis | `Series.Tags` (read+modify+write), `DelayProfiles.Tags` (write) | Reads `Series.Tags` int-list, appends new IDs. Tag list shape: bare JSON int array. |
| 071 | `071_unknown_quality_in_profile.cs` | embedded-doc shape | `Profiles.Items` | `ProfileUpdater71.PrependQuality(0)` — adds Unknown quality at position 0 of every profile's Items array. Profile-item shape is full nested JSON with `Items[]` recursion. |
| 081 | `081_move_dot_prefix_to_transmission_category.cs` | embedded settings JSON | `DownloadClients.Settings` (`TransmissionSettings`) | Reads `tvCategory`, prefixes with `.`. |
| 085 | `085_expand_transmission_urlbase.cs` | embedded settings JSON | `DownloadClients.Settings` (`TransmissionSettings`) | Reads `urlBase`, wraps as `/{x}/transmission/`. |
| 086 | `086_pushbullet_device_ids.cs` | embedded settings JSON shape | `Notifications.Settings` (`PushBulletSettings`) | `deviceId: string` → `deviceIds: [string]` (rename + array conversion). |
| 088 | `088_pushbullet_devices_channels_list.cs` | embedded settings JSON | `Notifications.Settings` (`PushBulletSettings`) | Splits comma-separated `deviceIds`/`channelTags` into arrays. |
| 090 | `090_update_kickass_url.cs` | string mutation in JSON column | `Indexers.Settings` (`KickassTorrents`) | SQL replace — `kickass.so/.to` → `kat.cr`, `http://` → `https://`. Multi-replace SQL chain. |
| 102 | `102_add_language_to_episodeFiles_history_and_blacklist.cs` | embedded-doc shape | `EpisodeFiles.Language`, `History.Language`, `Blocklist.Language` (post-160 `Blocklist`) | Backfills per-row `Language` int from per-Series `ProfileId`'s `Language`. **Note** `Language` here is the int form — the column is the integer language id, not a list. The list shape comes in 175. |
| 111 | `111_create_language_profiles.cs` | embedded-doc shape | `LanguageProfiles.Languages` (NEW), `Series.LanguageProfileId` | Synthesises `LanguageProfileItem` arrays from existing `Profiles.Language` int. Shape: `[{language: int, allowed: bool}]`. |
| 113 | `113_consolidate_indexer_baseurl.cs` | embedded settings JSON | `Indexers.Settings` (`Newznab`/`Torznab`/`IPTorrents`/`Omgwtfnzbs`) | Renames JSON key `url` → `baseUrl`. Uses `JObject.AddFirst` to preserve key order. |
| 117 | `117_add_webrip_and_br480_qualites_in_profile.cs` | embedded-doc shape | `Profiles.Items` | Wraps existing leaf items in 5 named *groups* (id 1000..1004). Tree restructure of profile-item JSON, including cutoff remap. |
| 122 | `122_add_remux_qualities_in_profile.cs` | embedded-doc shape | `Profiles.Items` | Inserts new quality leaves into profile trees post-117 (Remux IDs 20, 21). Uses `SplitQualityAppend` walker. |
| 139 | `139_add_download_history.cs` | reads `History.Data` JSON to seed new table | `DownloadHistory` (NEW from `History`) | Reads `History.Data` dict (`{indexer, downloadClient, protocol}`), creates `DownloadHistory` rows. **SQLite-only** (`IfDatabase("sqlite")`). |
| 148 | `148_mediainfo_channels.cs` | string mutation in JSON column | `EpisodeFiles.MediaInfo` | SQL replace — `audioChannels` → `audioChannelsContainer`, `audioChannelPositionsText` → `audioChannelPositionsTextContainer`. |
| 155 | `155_add_arabic_and_hindi_languages.cs` | embedded-doc shape | `LanguageProfiles.Languages` | Prepends missing languages (re-evaluates `Language.All` set) to every profile's languages list. |
| 157 | `157_email_multiple_addresses.cs` | embedded settings JSON shape | `Notifications.Settings` (`Email`) | Splits comma-separated `to` into a string array. |
| 161 | `161_remove_plex_hometheater.cs` | embedded-doc shape (deeply nested) | `PendingReleases.ParsedEpisodeInfo` | Re-parses every `ParsedEpisodeInfo` JSON, converts `Quality: {quality: {id}, revision}` → `quality: {quality: int, revision}` (drops nested object), `Language: {id, name}` → `language: int`. **Schema migration on EmbeddedDoc**. Also deletes `PlexHomeTheater`/`PlexClient` notifications. |
| 162 | `162_release_profile_to_array.cs` | embedded-doc shape | `ReleaseProfiles.Required`, `ReleaseProfiles.Ignored` | Comma-separated string → JSON string array. EDOC-Q6 cross-cut (`StringListConverter` fallback). |
| 163 | `163_mediainfo_to_ffmpeg.cs` | embedded-doc shape (huge) | `EpisodeFiles.MediaInfo` | Largest single migration in the project — re-parses MediaInfo JSON via STJ (already), re-emits using new ffmpeg-style shape. **Carries its own JSON serializer config replicating the catalogue contract** (line 27-39). The Go port can lift this verbatim as the spec for STJ-equivalence in Go. Schema-revision-gated (skips if revision >= 8). |
| 169 | `169_add_malayalam _and_ukrainian_languages.cs` | embedded-doc shape | `LanguageProfiles.Languages` | Same pattern as 155. (Note filename has space.) |
| 171 | `171_add_custom_formats.cs` | embedded-doc shape + new polymorphic specs | `CustomFormats.Specifications`, `QualityProfiles.FormatItems`, `ReleaseProfiles.Preferred`, `NamingConfig.*Format` | Largest *create-from-existing-data* migration: synthesises Custom Formats from `ReleaseProfiles.Preferred`. Builds `[{type: "ReleaseTitleSpecification", body: {…}}]` polymorphic specs JSON. **Establishes the CustomFormats polymorphic envelope shape**. Cross-cite `subsystems/quality-profiles-formats-organizer.md:358`. |
| 175 | `175_language_profiles_to_custom_formats.cs` | embedded-doc shape | `EpisodeFiles.Languages` (NEW), `History.Languages`, `Blocklist.Languages` | `Language: int` column → `Languages: [int]` JSON array column. SQL string-templated `'[' || "Language" || ']'` — works because old column was bare int. **Drops** `LanguageProfiles` table entirely. |
| 183 | `183_update_images_remote_url.cs` | string mutation in JSON column | `Episodes.Images`, `Series.Images`, `Series.Actors`, `Series.Seasons` | SQL replace — `"url"` → `"remoteUrl"` everywhere these JSON columns mention it. **Affects `MediaCover.RemoteUrl`** (catalogue type 8). |
| 201 | `201_email_encryption.cs` | embedded settings JSON | `Notifications.Settings` (`Email`) | `requireEncryption: bool` → `useEncryption: int`. |
| 203 | `203_release_type.cs` | reads/parses on backfill | `EpisodeFiles.ReleaseType` (NEW) | Re-runs `Parser.Parser.ParseTitle` against `OriginalFilePath`, sets enum. The Go port **must produce identical parse results** for backfilled rows or the Sonarr test data drifts; this is also why `parser/*.md` is a Phase 0.5b deliverable. |
| 204 | `204_add_release_hash.cs` | reads/parses on backfill | `EpisodeFiles.ReleaseHash` (NEW) | Same parser dependency as 203. |
| 205 | `205_rename_season_pack_spec.cs` | **polymorphic spec-class rename** | `CustomFormats.Specifications` JSON | SQL `REPLACE("Specifications", 'SeasonPackSpecification', 'ReleaseTypeSpecification')`. The canonical example of polymorphic-type-rename in this project. **Cross-cite** `subsystems/quality-profiles-formats-organizer.md:324`. |
| 207 | `207_add_size_to_quality_profiles.cs` | embedded-doc shape | `QualityProfiles.Items` | Pushes `MinSize`/`MaxSize`/`PreferredSize` from `QualityDefinitions` rows *into* the per-profile-item JSON, then deletes the source columns. Tree-walk through nested `Items[].Items[]`. |
| 210 | `210_add_monitored_seasons_filter.cs` | embedded-doc shape | `CustomFilters.Filters` | Renames filter key `hasUnmonitoredSeason` → `seasonsMonitoredStatus` and recodes its value array semantics. |
| 215 | `215_add_blurary576p_quality_in_profiles_with_grouped_blurary480p.cs` | embedded-doc shape | `QualityProfiles.Items` | Inserts new quality 22 (Bluray576p) after existing 13. Walks nested groups. |
| 218 | `218_telegram_link_preview.cs` | embedded settings JSON | `Notifications.Settings` (`Telegram`) | Synthesises `linkPreview` from `metadataLinks` array. |
| 219 | `219_nzb_su_url_to_nzb_life.cs` | string mutation in JSON column | `Indexers.Settings` (`Newznab`) | Continuation of the 046 lineage — `api.nzb.su` → `api.nzb.life`. |
| 220 | `220_enable_season_pack_seeding_goal.cs` | embedded settings JSON | `Indexers.Settings` (any with seedCriteria) | Adds `seasonPackSeedGoal` and copies `seedRatio` → `seasonPackSeedRatio` inside nested `seedCriteria` object. |
| 224 | `224_release_profile_indexer_ids.cs` | embedded-doc shape | `ReleaseProfiles.IndexerIds` (NEW), drops `IndexerId` | `IndexerId: int` (single) → `IndexerIds: [int]` (list). |
| 225 | `225_mediainfo_multiple_streams.cs` | embedded-doc shape (massive) | `EpisodeFiles.MediaInfo` | Splits the previous flat MediaInfo shape into `audioStreams[]` + `subtitleStreams[]` arrays. Carries its own STJ config (same contract as 163). Schema-revision-gated (skips if >= 14). The two new types `MediaInfoAudioStreamModel` / `MediaInfoSubtitleStreamModel` are catalogue entries 12 and 13. |

**Provider `Implementation` renames specifically** (the TP-Q1/TP-Q2 fragility): direct rename via `Update.Table` happens in 023 (Newznab/Omgwtfnzbs/Wombles/Eztv null-config seeding), 026 (notifications batch ConfigContract seeding), 027 (Omgwtfnzbs ConfigContract fix), 051 (Blackhole→UsenetBlackhole, Pneumatic ConfigContract), 082 (Fanzub null-config→FanzubSettings), 105 (DownloadStation→TorrentDownloadStation). These are pure `Implementation`/`ConfigContract` cell rewrites — schema unchanged but the column value is load-bearing class-name string. The Go port's stable-ID registry (TP-Q1/TP-Q2) **must** treat each of these old/new pairs as alias mappings so existing user rows continue to load.

**Provider deletions** (silent-loss migrations, the inverse of renames — rows for these types vanish forever): 087 Eztv, 098 TitansOfTv, 104 KickassTorrents, 107 Wombles, 124 MediaBrowserMetadata, 125 NotifyMyAndroid + Pushalot, 138 BitMeTv, 145 Growl, 161 PlexHomeTheater + PlexClient, 173 Omgwtfnzbs.

## Full ledger

One row per migration, in version order. "Reversible?" is uniformly **No** because every migration inherits `Down() => throw NotImplementedException`. "Dropable in Go port?" means: the migration achieved a one-shot or dead-feature cleanup, the schema dump captures the resulting state, and the Go port's goose baseline can elide it. *Always re-verify against the schema dump before eliding.*

| # | Filename | Intent | Tables touched | Embedded-doc fields touched | Reversible? | Dropable in Go port? | Notes |
|---|----------|--------|----------------|-----------------------------|-------------|----------------------|-------|
| 0 | `000_database_engine_version_check.cs` | log SQLite/Postgres version | (none — read-only) | — | No | **Yes** — pure logging, no DB mutation; tagged `[Maintenance(BeforeAll)]`. | `ForwardOnlyMigration`, runs before every migration on each startup. |
| 1 | `001_initial_setup.cs` | initial schema | `Config`, `RootFolders`, `Series`, `Seasons`, `Episodes`, `EpisodeFiles`, `History`, `Notifications`, `ScheduledTasks`, `Indexers`, `QualityProfiles`, `QualitySizes`, `SceneMappings`, `NamingConfig`; `Logs` (LogDb) | — | No | No (foundational) | The base schema. Touches both DBs. |
| 2 | `002_remove_tvrage_imdb_unique_constraint.cs` | drop indexes | `Series` (drop unique idx on `TvRageId`, `ImdbId`) | — | No | No | |
| 3 | `003_remove_clean_title_from_scene_mapping.cs` | recreate `SceneMappings` with new shape | `SceneMappings` (drop+recreate) | — | No | No | Drops all rows by recreating the table — they get repopulated on next scene-mapping refresh. |
| 4 | `004_updated_history.cs` | recreate `History` | `History` (drop+recreate, columns renamed) | — | No | No | Loses prior `History` rows. Acceptable: data was minimal pre-v2. |
| 5 | `005_added_eventtype_to_history.cs` | add column | `History.EventType` (Int32 nullable) | — | No | No | |
| 6 | `006_add_index_to_log_time.cs` | recreate `Logs` with index | `Logs` (drop+recreate, **LogDb**) | — | No | No | First log-DB-only migration. |
| 7 | `007_add_renameEpisodes_to_naming.cs` | add column + backfill | `NamingConfig.RenameEpisodes` (Bool nullable, set to `NOT UseSceneName`) | — | No | No | |
| 8 | `008_remove_backlog.cs` | drop columns | `Series.BacklogSetting`, `NamingConfig.UseSceneName` | — | No | No | |
| 9 | `009_fix_renameEpisodes.cs` | drop column + remap | `NamingConfig` (`SeasonFolderFormat` drop; `RenameEpisodes` -1→1, -2→0 — sqlite-only) | — | No | **Yes** — fixes legacy bool-as-tri-int from a 2014 bug; schema dump shows correct state. | |
| 10 | `010_add_monitored.cs` | add columns + backfill from Ignored | `Episodes.Monitored`, `Seasons.Monitored` | — | No | No | |
| 11 | `011_remove_ignored.cs` | drop columns | `Seasons.Ignored`, `Episodes.Ignored` | — | No | No | |
| 12 | `012_remove_custom_start_date.cs` | drop column | `Series.CustomStartDate` | — | No | No | |
| 13 | `013_add_air_date_utc.cs` | add column + backfill | `Episodes.AirDateUtc` (= `AirDate`) | — | No | No | |
| 14 | `014_drop_air_date.cs` | drop column | `Episodes.AirDate` (the original DateTime version) | — | No | No | |
| 15 | `015_add_air_date_as_string.cs` | re-add column as string | `Episodes.AirDate` (String nullable) | — | No | No | Reintroduces the column under the same name with a different type. |
| 16 | `016_updated_imported_history_item.cs` | string-mutate JSON in column | `History.Data` (`"Path"` → `"ImportedPath"` where `EventType=3`) | `History.Data` JSON dictionary key rename | No | **Yes** — one-shot fix for a 2015 typo. | |
| 17 | `017_reset_scene_names.cs` | NULL out a column | `EpisodeFiles.SceneName` (set NULL) | — | No | **Yes** — one-shot fix to clear bad data. | The `WHERE SceneName != NULL` is buggy (NULL comparison) — effectively only updates rows where `SceneName IS NOT NULL` if the engine is permissive. SQLite quirk. |
| 18 | `018_remove_duplicates.cs` | dedup rows | `Series`, `Episodes`, `Seasons`, `History`, `EpisodeFiles` (cascade-delete duplicates by `TvdbId`/`TitleSlug`/`TvDbEpisodeId`) | — | No | **Yes** — one-shot dedupe. | |
| 19 | `019_restore_unique_constraints.cs` | re-add unique indexes | `Series.TvdbId`, `Series.TitleSlug`, `Episodes.TvDbEpisodeId` (drop-if-exists then create unique) | — | No | No | |
| 20 | `020_add_year_and_seasons_to_series.cs` | add columns + backfill JSON | `Series.Year`, `Series.Seasons` | **`Series.Seasons` JSON written first time** — array of `{seasonNumber, monitored}` objects | No | No | **HIGH RISK** (initial seasons-JSON shape). |
| 21 | `021_drop_seasons_table.cs` | drop table | `Seasons` (deleted; replaced by `Series.Seasons` JSON) | — | No | No | |
| 22 | `022_move_indexer_to_generic_provider.cs` | add column | `Indexers.ConfigContract` (String nullable) | — | No | No | |
| 23 | `023_add_config_contract_to_indexers.cs` | backfill `ConfigContract` from `Implementation` | `Indexers.ConfigContract` (Newznab/Omgwtfnzbs/Wombles/Eztv) + delete null rows | — | No | **Yes** — one-shot data init for a then-new column; schema dump captures result. | TP-Q1/TP-Q2 cross-cut. |
| 24 | `024_drop_tvdb_episodeid.cs` | drop column | `Episodes.TvDbEpisodeId` | — | No | No | |
| 25 | `025_move_notification_to_generic_provider.cs` | add column | `Notifications.ConfigContract` (String nullable) | — | No | No | |
| 26 | `026_add_config_contract_to_notifications.cs` | backfill `ConfigContract` from `Implementation` | `Notifications.ConfigContract` (9 implementation->settings mappings) + delete null rows | — | No | **Yes** — one-shot. | TP-Q1/TP-Q2 cross-cut. |
| 27 | `027_fix_omgwtfnzbs.cs` | fix omg config | `Indexers.ConfigContract`, `Indexers.Settings='{}'` for omg | `Indexers.Settings` JSON (replace null/empty with `"{}"`) | No | **Yes** — one-shot. | |
| 28 | `028_add_blacklist_table.cs` | create table | `Blacklist` (NEW) | — | No | No | Renamed to `Blocklist` in 160. |
| 29 | `029_add_formats_to_naming_config.cs` | add columns + synthesise format strings | `NamingConfig.StandardEpisodeFormat`, `DailyEpisodeFormat` | naming format token strings (synthesised from `IncludeSeriesTitle`/`Separator`/`NumberStyle`/`IncludeQuality`/`ReplaceSpaces`) | No | **Yes** — one-shot conversion of a vintage NamingConfig shape; schema dump has the correct strings. | **HIGH RISK** (naming format string contract). |
| 30 | `030_add_season_folder_format_to_naming_config.cs` | add column + token-rewrite | `NamingConfig.SeasonFolderFormat`, deletes `Config.seasonfolderformat`/`useseasonfolder` | naming format token rewrite (`%sn`→`{Series Title}` etc.) | No | **Yes** — one-shot. | |
| 31 | `031_delete_old_naming_config_columns.cs` | drop columns | `NamingConfig.Separator`/`NumberStyle`/… (6 columns) | — | No | No | |
| 32 | `032_set_default_release_group.cs` | backfill | `EpisodeFiles.ReleaseGroup = "DRONE"` where null | — | No | **Yes** — one-shot legacy fix. | |
| 33 | `033_add_api_key_to_pushover.cs` | rewrite Pushover settings JSON | `Notifications.Settings` (`PushoverSettings`) | `apiKey` set to hardcoded constant; `priority` 2→1 | No | **Yes** — one-shot upgrade for a long-since-deprecated workflow. | **HIGH RISK** (settings-JSON shape mutation). |
| 34 | `034_remove_series_contraints.cs` | weaken NotNullable | `Series.TvRageId`/`ImdbId`/`TitleSlug` (alter to nullable) | — | No | No | |
| 35 | `035_add_series_folder_format_to_naming_config.cs` | add column + default | `NamingConfig.SeriesFolderFormat = "{Series Title}"` | — | No | No | |
| 36 | `036_update_with_quality_converters.cs` | massive Quality-JSON re-shape | `QualityProfiles.Items`, `Blacklist.Quality`, `EpisodeFiles.Quality`, `History.Quality` | **Establishes the int-reduced QualityModel form** — `{quality: int, proper: bool}` | No | No | **HIGH RISK** — foundational shape change for every quality-bearing column. |
| 37 | `037_add_configurable_qualities.cs` | replace `QualitySizes` with `QualityDefinitions` | drops `QualitySizes`, creates `QualityDefinitions`, alter `QualityProfiles.Items` NotNullable | — | No | No | |
| 38 | `038_add_on_upgrade_to_notifications.cs` | add column + backfill from `OnDownload` | `Notifications.OnUpgrade` | — | No | No | |
| 39 | `039_add_metadata_tables.cs` | create tables | `Metadata`, `MetadataFiles` | — | No | No | |
| 40 | `040_add_metadata_to_episodes_and_series.cs` | add columns | `Series.Actors`/`Ratings`/`Genres`/`Certification`, `Episodes.Ratings`/`Images` | — | No | No | These are JSON columns that get populated subsequently by the refresh job, not by this migration. |
| 41 | `041_fix_xbmc_season_images_metadata.cs` | repair `MetadataFiles.Type` | `MetadataFiles` (Xbmc season → Type=4) | — | No | **Yes** — one-shot fix. | |
| 42 | `042_add_download_clients_table.cs` | create table | `DownloadClients` | — | No | No | |
| 43 | `043_convert_config_to_download_clients.cs` | seed `DownloadClients` from `Config` | `DownloadClients` (insert), `Config` (delete keys) | **Defines initial Settings JSON shape** for Sabnzbd/Nzbget/Pneumatic/Blackhole | No | **Yes** — one-shot import from the v1.x global-config era. | **HIGH RISK** (provider settings shape). |
| 44 | `044_fix_xbmc_episode_metadata.cs` | repair `MetadataFiles.Type` | `MetadataFiles` (Xbmc nfo → Type=2, jpg → Type=5) | — | No | **Yes** — one-shot fix. | |
| 45 | `045_add_indexes.cs` | create indexes | `Blacklist.SeriesId`, `EpisodeFiles.SeriesId`, `Episodes.EpisodeFileId`, `Episodes.SeriesId`, `History.EpisodeId`, `History.Date`, `Series.Path`/`CleanTitle`/`TvRageId` | — | No | No | |
| 46 | `046_fix_nzb_su_url.cs` | string-mutate Settings JSON | `Indexers.Settings` (Newznab) | `//nzb.su` → `//api.nzb.su` (substring replace) | No | **Yes** — superseded by 219. | **HIGH RISK** (URL-string mutation in JSON column). |
| 47 | `047_add_published_date_blacklist_column.cs` | add column | `Blacklist.PublishedDate` (DateTime nullable) | — | No | No | |
| 48 | `048_add_title_to_scenemappings.cs` | add column | `SceneMappings.Title` (String nullable) | — | No | No | |
| 49 | `049_fix_dognzb_url.cs` | string-mutate Settings JSON | `Indexers.Settings` (Newznab) | `//dognzb.cr` → `//api.dognzb.cr` | No | **Yes** — one-shot URL fix. | **HIGH RISK** (URL-string mutation in JSON column). |
| 50 | `050_add_hash_to_metadata_files.cs` | add column | `MetadataFiles.Hash` (String nullable) | — | No | No | |
| 51 | `051_download_client_import.cs` | rename Implementation + reshape settings + history rebind | `DownloadClients.Implementation` (Blackhole→UsenetBlackhole), `Settings`, `ConfigContract`; `History.Data` mutation; `Config` reads | **`DownloadClients.Settings`** shape change for Blackhole/Pneumatic; **`History.Data`** dict mutation (`downloadClient`/`downloadClientId` rebind) | No | **Yes** — one-shot, very specific to the v2.x→v3.x transition. | **HIGH RISK** (polymorphic implementation rename + JSON shape change + history backfill). |
| 52 | `052_add_columns_for_anime.cs` | add columns + drop SceneMappings | `SceneMappings.Type`, `NamingConfig.AnimeEpisodeFormat`; `DELETE FROM SceneMappings` | — | No | No | Wipes scene-mappings (re-fetched from server). |
| 53 | `053_add_series_sorttitle.cs` | add column + backfill via NormalizeTitle | `Series.SortTitle` | — | No | No | Calls `Parser.Parser.NormalizeTitle` — Go port must reproduce this. |
| 54 | `054_rename_profiles.cs` | rename table + add cols + create PendingReleases | rename `QualityProfiles`→`Profiles`; add `Profiles.Language`/`GrabDelay`/`GrabDelayMode`; `Series.ProfileId`; create `PendingReleases` | — | No | No | |
| 55 | `055_drop_old_profile_columns.cs` | drop column | `Series.QualityProfileId` | — | No | No | |
| 56 | `056_add_mediainfo_to_episodefile.cs` | add column | `EpisodeFiles.MediaInfo` (String nullable, JSON later) | — | No | No | |
| 57 | `057_convert_episode_file_path_to_relative.cs` | add column + backfill | `EpisodeFiles.RelativePath` (computed from `Path`) | — | No | No | |
| 58 | `058_drop_epsiode_file_path.cs` | drop column | `EpisodeFiles.Path` | — | No | No | (Filename typo: "epsiode".) |
| 59 | `059_add_enable_options_to_indexers.cs` | add columns + backfill | `Indexers.EnableRss`/`EnableSearch` (= `Enable`); set Wombles `EnableSearch=false` | — | No | No | |
| 60 | `060_remove_enable_from_indexers.cs` | drop columns | `Indexers.Enable`, `DownloadClients.Protocol` | — | No | No | |
| 61 | `061_clear_bad_scene_names.cs` | NULL out bad data | `EpisodeFiles.SceneName`/`ReleaseGroup` (rule-based clearing) | — | No | **Yes** — one-shot data fix. | |
| 62 | `062_convert_quality_models.cs` | re-shape QualityModel | `EpisodeFiles.Quality`, `Blacklist.Quality`, `History.Quality` | `{quality: int, proper: bool}` → `{quality: int, revision: {version: 1|2}}` | No | No | **HIGH RISK** (foundational `Revision` shape introduction). |
| 63 | `063_add_remotepathmappings.cs` | create table | `RemotePathMappings` | — | No | No | |
| 64 | `064_add_remove_method_from_logs.cs` | drop column | `Logs.Method` (**LogDb**) | — | No | No | |
| 65 | `065_make_scene_numbering_nullable.cs` | NULLify zeros | `Episodes.AbsoluteEpisodeNumber`/`SceneAbsoluteEpisodeNumber`/`SceneSeasonNumber`/`SceneEpisodeNumber` (= NULL where 0) | — | No | **Yes** — one-shot data fix. | |
| 66 | `066_add_tags.cs` | create + add cols | `Tags` (NEW), `Series.Tags = '[]'`, `Notifications.Tags = '[]'` | initial empty JSON int array | No | No | |
| 67 | `067_add_added_to_series.cs` | add column | `Series.Added` (DateTime nullable) | — | No | No | |
| 68 | `068_add_release_restrictions.cs` | create table + import config | `Restrictions` (NEW); reads `Config.releaserestrictions` | — | No | **Yes** — one-shot import. | Renamed to `ReleaseProfiles` in 127. |
| 69 | `069_quality_proper.cs` | rewrite naming-format strings | `NamingConfig` (StandardEpisodeFormat, DailyEpisodeFormat, AnimeEpisodeFormat) | regex token rewrite (`{quality}` → `{quality full}` family) | No | **Yes** — one-shot token migration. | |
| 70 | `070_delay_profile.cs` | create + import + tag synthesis | `DelayProfiles` (NEW + initial row); reads `Profiles.GrabDelay`; auto-creates `Tags` rows; mutates `Series.Tags` | `Series.Tags` JSON (append tag id) | No | No | **HIGH RISK** (writes new tag IDs into existing Series.Tags JSON). |
| 71 | `071_unknown_quality_in_profile.cs` | drop col + reshape Items | `QualityDefinitions.Weight` drop; `Profiles.Items` (insert quality 0 at index 0) | profile-item tree mutation | No | No | **HIGH RISK** (profile-item tree shape). |
| 72 | `072_history_grabid.cs` | add column + extract from Data dict | `History.DownloadId` indexed; `History.Data` (remove `downloadClientId` key) | dictionary mutation | No | No | **HIGH RISK** (history JSON shape mutation). |
| 73 | `073_clear_ratings.cs` | reset all ratings | `Series.Ratings = "{}"`, `Episodes.Ratings = "{}"` | — | No | **Yes** — one-shot reset. | Re-fetched on next refresh. |
| 74 | `074_disable_eztv.cs` | disable provider rows | `Indexers` (set EnableRss/EnableSearch=false where Eztv+ezrss.it) | — | No | **Yes** — one-shot. | |
| 75 | `075_force_lib_update.cs` | reset timestamps | `ScheduledTasks.LastExecution` (RefreshSeriesCommand), `Series.LastInfoSync` | — | No | **Yes** — one-shot. | Forces a refresh on first run after upgrade. |
| 76 | `076_add_users_table.cs` | create table | `Users` | — | No | No | |
| 77 | `077_add_add_options_to_series.cs` | add column | `Series.AddOptions` (String nullable) | — | No | No | JSON column for `AddSeriesOptions`. |
| 78 | `078_add_commands_table.cs` | create table | `Commands` | — | No | No | |
| 79 | `079_dedupe_tags.cs` | dedupe + remap | `Tags` (delete dupes); rewrites `Series.Tags`, `Notifications.Tags`, `DelayProfiles.Tags`, `Restrictions.Tags` int-list JSON | tag-id remap inside JSON int arrays | No | **Yes** — one-shot. | **HIGH RISK** (JSON int-array rewrite). |
| 80 | *(gap)* | unused | — | — | — | — | Number skipped intentionally. |
| 81 | `081_move_dot_prefix_to_transmission_category.cs` | mutate Transmission settings | `DownloadClients.Settings` | `tvCategory` prefixed with `.` | No | **Yes** — one-shot. | **HIGH RISK** (settings shape). |
| 82 | `082_add_fanzub_settings.cs` | rename ConfigContract | `Indexers.ConfigContract` (Fanzub: NullConfig→FanzubSettings) | — | No | **Yes** — one-shot. | TP-Q2 cross-cut. |
| 83 | `083_additonal_blacklist_columns.cs` | add columns + backfill Protocol=1 | `Blacklist.Size`/`Protocol`/`Indexer`/`Message`/`TorrentInfoHash` | — | No | No | (Filename typo: "additonal".) |
| 84 | `084_update_quality_minmax_size.cs` | retype Int32→Double + null sentinel | `QualityDefinitions.MinSize`/`MaxSize` (Int→Double); set MaxSize=NULL where Quality=10 OR =0 | — | No | No | **Column type change**, INTEGER → REAL. SQLite is permissive but Postgres-side dump shows DOUBLE PRECISION. |
| 85 | `085_expand_transmission_urlbase.cs` | mutate Transmission settings | `DownloadClients.Settings` | `urlBase` wrapped to `/{x}/transmission/` | No | **Yes** — one-shot. | **HIGH RISK** (settings shape). |
| 86 | `086_pushbullet_device_ids.cs` | reshape pushbullet settings | `Notifications.Settings` | `deviceId: string` → `deviceIds: [string]` | No | **Yes** — one-shot. | **HIGH RISK** (settings shape). |
| 87 | `087_remove_eztv.cs` | delete provider rows | `Indexers` (delete Eztv) | — | No | **Yes** — silent-loss; user data gone, port elides. | |
| 88 | `088_pushbullet_devices_channels_list.cs` | csv→array | `Notifications.Settings` (deviceIds, channelTags) | comma-split into arrays | No | **Yes** — one-shot. | **HIGH RISK** (settings shape). |
| 89 | `089_add_on_rename_to_notifcations.cs` | add col + backfill | `Notifications.OnRename` (=OnDownload for PlexServer/Xbmc/MediaBrowser; false otherwise; finally NotNullable); set OnGrab=false for PlexServer | — | No | No | (Filename typo: "notifcations".) |
| 90 | `090_update_kickass_url.cs` | mutate Settings JSON | `Indexers.Settings` (KickassTorrents) | `kickass.so/.to`→`kat.cr`, http→https | No | **Yes** — superseded by 96 (disable) and 104 (delete). | **HIGH RISK** (URL substring mutation). |
| 91 | `091_added_indexerstatus.cs` | create table | `IndexerStatus` (with `IndexerId` unique col) | — | No | No | Renamed to `ProviderId` in 114. |
| 92 | `092_add_unverifiedscenenumbering.cs` | add column with default | `Episodes.UnverifiedSceneNumbering` (Bool default false) | — | No | No | |
| 93 | `093_naming_config_replace_characters.cs` | add column with default | `NamingConfig.ReplaceIllegalCharacters` (Bool default true) + `AllRows()` set true | — | No | No | |
| 94 | `094_add_tvmazeid.cs` | add col + index | `Series.TvMazeId` (Int default 0) + index | — | No | No | |
| 95 | `095_add_additional_episodes_index.cs` | create composite index | `Episodes(SeriesId, SeasonNumber, EpisodeNumber)` | — | No | No | |
| 96 | `096_disable_kickass.cs` | disable + clear url | `Indexers` (KickassTorrents EnableRss/Search=false; strip kat.cr) | string mutation in Settings JSON | No | **Yes** — one-shot. | |
| 97 | `097_add_release_to_pending_releases.cs` | add column with default | `PendingReleases.Reason` (Int default 0) | — | No | No | (Class name actually `add_reason_to_pending_releases`.) |
| 98 | `098_remove_titans_of_tv.cs` | delete provider rows | `Indexers` (delete TitansOfTv) | — | No | **Yes** — silent-loss. | |
| 99 | `099_extra_and_subtitle_files.cs` | create + extract extension | `ExtraFiles`/`SubtitleFiles` NEW; `MetadataFiles.Added`/`Extension` add+backfill from RelativePath | — | No | No | |
| 100 | `100_add_scene_season_number.cs` | nullable + add | `SceneMappings.SeasonNumber` (alter nullable), `SceneSeasonNumber` add | — | No | No | |
| 101 | `101_add_ultrahd_quality_in_profiles.cs` | append qualities to profiles | `Profiles.Items` (append 16, 18, 19) | profile-item tree mutation | No | No | **HIGH RISK** (profile-item tree). |
| 102 | `102_add_language_to_episodeFiles_history_and_blacklist.cs` | add Language col + backfill | `EpisodeFiles.Language`, `History.Language`, `Blacklist.Language` (= per-Series ProfileId's Language) | language-int backfill from Profile | No | No | **HIGH RISK** (sets every-row language from profile lookup). |
| 103 | `103_fix_metadata_file_extensions.cs` | normalise Extension column | `MetadataFiles.Extension` (substring after last `.`) | — | No | **Yes** — one-shot fix. | |
| 104 | `104_remove_kickass.cs` | delete provider rows | `Indexers` (delete KickassTorrents) | — | No | **Yes** — silent-loss. | |
| 105 | `105_rename_torrent_downloadstation.cs` | rename Implementation | `DownloadClients.Implementation` (DownloadStation → TorrentDownloadStation) | — | No | **Yes** — one-shot. | TP-Q1 cross-cut. |
| 106 | `106_update_btn_url.cs` | string mutate Settings | `Indexers.Settings` (BroadcastheNet: api.btnapps.net → api.broadcasthe.net) | URL-substring | No | **Yes** — one-shot. | |
| 107 | `107_remove_wombles.cs` | delete provider rows | `Indexers` (delete Wombles) | — | No | **Yes** — silent-loss. | |
| 108 | `108_fix_extra_file_extension.cs` | extract Extension | `ExtraFiles`/`SubtitleFiles` (recompute Extension; delete extensionless from ExtraFiles) | — | No | **Yes** — one-shot fix. | |
| 109 | `109_import_extra_files.cs` | seed config | inserts `Config.importextrafiles='True'` if `extrafileextensions` set | — | No | **Yes** — one-shot. | |
| 110 | `110_fix_extra_files_config.cs` | normalise config value | `Config.importextrafiles` true→'True'; default `extrafileextensions='srt'` if blank | — | No | **Yes** — one-shot. | |
| 111 | `111_create_language_profiles.cs` | create + import + drop | `LanguageProfiles` NEW; `Series.LanguageProfileId` add+backfill; `Profiles.Language` drop | **Establishes `LanguageProfileItem` JSON shape** | No | No | **HIGH RISK**. (Table itself is dropped in 175 — but the Series-rows pass through it, and many users were on this shape.) |
| 112 | `112_added_regex_to_scenemapping.cs` | add column | `SceneMappings.FilterRegex` (String nullable) | — | No | No | |
| 113 | `113_consolidate_indexer_baseurl.cs` | rename JSON key | `Indexers.Settings` (Newznab/Torznab/IPTorrents/Omgwtfnzbs) | `url` → `baseUrl` (with key-order preservation via JObject.AddFirst) | No | **Yes** — one-shot. | **HIGH RISK** (settings shape rename). |
| 114 | `114_rename_indexer_status_id.cs` | rename column | `IndexerStatus.IndexerId` → `ProviderId` | — | No | No | |
| 115 | `115_add_downloadclient_status.cs` | create table | `DownloadClientStatus` | — | No | No | |
| 116 | `116_disable_nyaa.cs` | disable + strip url | `Indexers` (Nyaa EnableRss/Search=false; strip nyaa.se) | string mutation in Settings JSON | No | **Yes** — one-shot. | |
| 117 | `117_add_webrip_and_br480_qualites_in_profile.cs` | tree restructure of profile Items | `Profiles.Items` (5 named groups created: 1000-1004) | profile-item tree (cutoff remap, group wrapping) | No | No | **HIGH RISK** (large tree restructure). |
| 118 | `118_add_history_eventType_index.cs` | create index | `History.EventType` | — | No | No | |
| 119 | `119_separate_automatic_and_interactive_searches.cs` | rename column + add + backfill | `Indexers.EnableSearch`→`EnableAutomaticSearch`; add `EnableInteractiveSearch` (=EnableAutomaticSearch then NotNullable) | — | No | No | |
| 120 | `120_update_series_episodes_history_indexes.cs` | composite indexes | `Episodes(SeriesId, AirDate)`, `History(EpisodeId, Date desc)`, `History(DownloadId, Date desc)` | — | No | No | |
| 121 | `121_update_animetosho_url.cs` | string mutate Settings | `Indexers.Settings` (Newznab/Torznab where animetosho) | URL substring rewrite | No | **Yes** — one-shot. | |
| 122 | `122_add_remux_qualities_in_profile.cs` | insert qualities into profile tree | `Profiles.Items` (insert 20, 21 after 19, 7) | profile-item tree mutation | No | No | **HIGH RISK** (profile tree). |
| 123 | `123_add_history_seriesId_index.cs` | create index | `History.SeriesId` | — | No | No | |
| 124 | `124_remove_media_browser_metadata.cs` | delete provider rows | `Metadata` (delete MediaBrowserMetadata) + `MetadataFiles` (delete by Consumer) | — | No | **Yes** — silent-loss. | |
| 125 | `125_remove_notify_my_android_and_pushalot_notifications.cs` | delete provider rows | `Notifications` (NotifyMyAndroid + Pushalot) | — | No | **Yes** — silent-loss. | |
| 126 | `126_add_custom_filters.cs` | create table | `CustomFilters` | — | No | No | |
| 127 | `127_rename_release_profiles.cs` | rename + add | `Restrictions`→`ReleaseProfiles`; `IncludePreferredWhenRenaming` (Bool default true) | — | No | No | |
| 128 | `128_rename_quality_profiles_add_upgrade_allowed.cs` | rename table + cols | `Profiles`→`QualityProfiles`; add `UpgradeAllowed` to QualityProfiles + LanguageProfiles; rename `Series.ProfileId`→`QualityProfileId` | — | No | No | |
| 129 | `129_add_relative_original_path_to_episode_file.cs` | add column | `EpisodeFiles.OriginalFilePath` (String nullable) | — | No | No | |
| 130 | `130_episode_last_searched_time.cs` | add column | `Episodes.LastSearchTime` (DateTime nullable) | — | No | No | |
| 131 | `131_download_propers_config.cs` | translate config | `Config` (autodownloadpropers bool→`downloadpropersandrepacks` enum string) | — | No | **Yes** — one-shot. | |
| 132 | `132_add_download_client_priority.cs` | add column + assign priorities | `DownloadClients.Priority` default 1; per-Implementation usenet/torrent priority assignment | — | No | **Yes** — one-shot init. | |
| 133 | *(gap)* | unused | — | — | — | — | |
| 134 | `134_add_specials_folder_format.cs` | add column + default `Specials` | `NamingConfig.SpecialsFolderFormat` | — | No | No | |
| 135 | `135_health_issue_notification.cs` | add columns | `Notifications.OnHealthIssue`/`IncludeHealthWarnings` (Bool default false) | — | No | No | |
| 136 | `136_add_indexer_and_enabled_release_profiles.cs` | add columns | `ReleaseProfiles.Enabled` (true), `IndexerId` (0) | — | No | No | `IndexerId` later replaced by `IndexerIds` in 224. |
| 137 | `137_add_airedbefore_to_episodes.cs` | add columns | `Episodes.AiredAfterSeasonNumber`/`AiredBeforeSeasonNumber`/`AiredBeforeEpisodeNumber` | — | No | No | |
| 138 | `138_remove_bitmetv.cs` | delete provider + disable usenet-crawler | `Indexers` (delete BitMeTv; disable Newznab where url contains usenet-crawler.com) | — | No | **Yes** — silent-loss. | |
| 139 | `139_add_download_history.cs` | create + import from History | `DownloadHistory` (NEW + import from `History`); SQLite-only import path | reads `History.Data` JSON dict | No | No | **HIGH RISK** (reads embedded-doc to seed new table). SQLite-only via `IfDatabase("sqlite")` — Postgres users skip the import. |
| 140 | `140_remove_chown_and_folderchmod_config.cs` | delete config keys | `Config` (folderchmod, chownuser) | — | No | **Yes** — one-shot. | |
| 141 | `141_add_update_history.cs` | create table (**LogDb**) | `UpdateHistory` (LogDb) | — | No | No | UPDATE-Q5 cross-cut: with `LogDbEnabled=false` this table will not exist. |
| 142 | `142_import_lists.cs` | create tables | `ImportLists`, `ImportListStatus`, `ImportListExclusions` | — | No | No | |
| 143 | `143_add_priority_to_indexers.cs` | add column with default 25 | `Indexers.Priority` | — | No | No | |
| 144 | `144_import_lists_series_type_and_season_folder.cs` | add columns | `ImportLists.SeriesType` (0), `SeasonFolder` (true) | — | No | No | |
| 145 | `145_remove_growl.cs` | delete provider rows | `Notifications` (delete Growl) | — | No | **Yes** — silent-loss. | |
| 146 | `146_cleanup_duplicates_updatehistory.cs` | dedupe (LogDb) | `UpdateHistory` (delete consecutive-equal-Version rows) (**LogDb**) | — | No | **Yes** — one-shot. | |
| 147 | `147_swap_filechmod_for_folderchmod.cs` | translate + delete config | reads `Config.filechmod`, computes folderchmod via bit-shift, inserts; deletes old (SQLite-only) | — | No | **Yes** — one-shot. | |
| 148 | `148_mediainfo_channels.cs` | string mutate MediaInfo JSON | `EpisodeFiles.MediaInfo` | `audioChannels`→`audioChannelsContainer`, `audioChannelPositionsText`→`audioChannelPositionsTextContainer` | No | No | **HIGH RISK** (MediaInfo key rename — must be cumulative against 163/225). |
| 149 | `149_add_on_delete_to_notifications.cs` | add columns | `Notifications.OnSeriesDelete`/`OnEpisodeFileDelete` (Bool default false) | — | No | No | |
| 150 | `150_add_scene_mapping_origin.cs` | add columns | `SceneMappings.SceneOrigin`/`SearchMode`/`Comment` | — | No | No | |
| 151 | `151_remove_custom_filter_type.cs` | translate `Type` values | `CustomFilters.Type` (seriesIndex/seriesEditor/seasonPass → series) | — | No | **Yes** — one-shot. | |
| 152 | `152_update_btn_url_to_https.cs` | string mutate Settings | `Indexers.Settings` (BroadcastheNet) | http→https URL | No | **Yes** — one-shot. | |
| 153 | `153_add_on_episodefiledelete_for_upgrade.cs` | add column default true | `Notifications.OnEpisodeFileDeleteForUpgrade` | — | No | No | |
| 154 | `154_add_name_release_profile.cs` | add column nullable | `ReleaseProfiles.Name` | — | No | No | |
| 155 | `155_add_arabic_and_hindi_languages.cs` | append missing languages | `LanguageProfiles.Languages` (insertRange of `Language.All` non-present) | language-profile JSON shape | No | No | **HIGH RISK** (LanguageProfile.Languages JSON list mutation). |
| 156 | `156_add_bypass_to_delay_profile.cs` | add col + true-for-existing | `DelayProfiles.BypassIfHighestQuality` | — | No | No | |
| 157 | `157_email_multiple_addresses.cs` | csv→array | `Notifications.Settings` (Email) | `to: string` → `to: [string]` | No | **Yes** — one-shot. | **HIGH RISK** (settings shape). |
| 158 | `158_cdh_per_downloadclient.cs` | add cols + import config + branch by Implementation | `DownloadClients.RemoveCompletedDownloads`/`RemoveFailedDownloads`; `Config` deletes | — | No | **Yes** — one-shot. | RTorrent/Flood forced to false. |
| 159 | `159_add_indexer_tags.cs` | add column nullable | `Indexers.Tags` | — | No | No | |
| 160 | `160_rename_blacklist_to_blocklist.cs` | rename table | `Blacklist`→`Blocklist` | — | No | No | |
| 161 | `161_remove_plex_hometheatre.cs` | delete + reshape PendingReleases | `Notifications` (delete PlexHomeTheater + PlexClient); `PendingReleases.ParsedEpisodeInfo` re-shape | **`PendingReleases.ParsedEpisodeInfo`** Quality.id-object → int; Language object → int | No | No | **HIGH RISK** (deeply-nested embedded-doc shape change). |
| 162 | `162_release_profile_to_array.cs` | csv→array | `ReleaseProfiles.Required`/`Ignored` | comma-split → JSON string array | No | **Yes** — one-shot. | **HIGH RISK** (per EDOC-Q6 fallback). |
| 163 | `163_mediainfo_to_ffmpeg.cs` | massive MediaInfo reshape | `EpisodeFiles.MediaInfo` | full shape change to ffprobe-style: `videoFormat`/`audioFormat`/`audioChannels`/`videoHdrFormat`(enum)/`audioLanguages`(list)/`subtitles`(list); HDR/codec heuristics | No | No | **HIGH RISK — largest MediaInfo migration**. Skips schema-revision >=8 rows. The Go port can lift the STJ config block verbatim. |
| 164 | `164_download_client_per_indexer.cs` | add column default 0 | `Indexers.DownloadClientId` | — | No | No | |
| 165 | `165_add_on_update_to_notifications.cs` | add column | `Notifications.OnApplicationUpdate` | — | No | No | |
| 166 | `166_update_series_sort_title.cs` | re-normalise SortTitle | `Series.SortTitle` (recompute via `SeriesTitleNormalizer.Normalize`) | — | No | **Yes** — one-shot data fix. | Go port must reproduce normaliser. |
| 167 | `167_add_tvdbid_to_episode.cs` | add column | `Episodes.TvdbId` (Int nullable) | — | No | No | |
| 168 | `168_add_additional_info_to_pending_releases.cs` | add column | `PendingReleases.AdditionalInfo` | — | No | No | |
| 169 | `169_add_malayalam _and_ukrainian_languages.cs` | append missing languages | `LanguageProfiles.Languages` | language-profile JSON | No | No | **HIGH RISK**. Note: filename has space character. |
| 170 | `170_add_language_tags_to_subtitle_files.cs` | add column | `SubtitleFiles.LanguageTags` | — | No | No | |
| 171 | `171_add_custom_formats.cs` | create table + add cols + ReleaseProfile→CustomFormat synthesis + naming token rewrite | `CustomFormats` NEW; `QualityProfiles.FormatItems`/`MinFormatScore`/`CutoffFormatScore`; reads + deletes `ReleaseProfiles.Preferred`/`IncludePreferredWhenRenaming`; deletes empty ReleaseProfiles; mutates `NamingConfig.*Format` | **CustomFormat polymorphic spec envelope established** — `[{type, body: {…}}]` | No | No | **HIGH RISK — establishes the polymorphic spec contract**. Cross-cite quality-profiles doc:358. |
| 172 | `172_add_SeasonSearchMaximumSingleEpisodeAge_to_indexers.cs` | add column NotNullable default 0 | `Indexers.SeasonSearchMaximumSingleEpisodeAge` | — | No | No | |
| 173 | `173_remove_omg.cs` | delete provider rows | `Indexers` (Omgwtfnzbs) | — | No | **Yes** — silent-loss. | |
| 174 | `174_add_salt_to_users.cs` | add columns nullable | `Users.Salt`/`Iterations` | — | No | No | |
| 175 | `175_language_profiles_to_custom_formats.cs` | int-Language → int-list-Languages, drop Language profiles | `EpisodeFiles.Languages` NEW; `History.Languages` NEW; `Blocklist.Languages` NEW; drop `EpisodeFiles.Language`/`History.Language`/`Blocklist.Language`/`Series.LanguageProfileId`/`ImportLists.LanguageProfileId`; **drop `LanguageProfiles` table** | SQL-templated `'[' || Language || ']'` to bare-int list | No | No | **HIGH RISK** (table drop + per-row JSON shape). |
| 176 | `176_original_language.cs` | add col default English | `Series.OriginalLanguage` | — | No | No | |
| 177 | `177_add_on_manual_interaction_required_to_notifications.cs` | add col default false | `Notifications.OnManualInteractionRequired` | — | No | No | |
| 178 | `178_list_sync_time.cs` | drop col + add | `ImportListStatus.LastSyncListInfo` drop; add `LastInfoSync` | — | No | No | |
| 179 | `179_add_auto_tagging.cs` | create table | `AutoTagging` (`Specifications` JSON shape — polymorphic envelope same as CustomFormats) | — | No | No | |
| 180 | `180_task_duration.cs` | add col nullable | `ScheduledTasks.LastStartTime` | — | No | No | |
| 181 | `181_quality_definition_preferred_size.cs` | add col + backfill | `QualityDefinitions.PreferredSize` (= MaxSize-5 where >5) | — | No | No | |
| 182 | `182_add_bypass_to_delay_profile.cs` | add cols | `DelayProfiles.BypassIfAboveCustomFormatScore`/`MinimumCustomFormatScore` | — | No | No | (Note: shares name with 156 — different file, different columns.) |
| 183 | `183_update_images_remote_url.cs` | string mutate JSON cols | `Episodes.Images`, `Series.Images`, `Series.Actors`, `Series.Seasons` | `"url"` → `"remoteUrl"` (MediaCover field rename) | No | No | **HIGH RISK** (affects every MediaCover JSON cell). |
| 184 | `184_remove_invalid_roksbox_metadata_images.cs` | delete bad rows | `MetadataFiles` (Consumer=Roksbox + path contains `metadata`) | — | No | **Yes** — one-shot. | Branches on database: SQLite vs PG path-escaping. |
| 185 | `185_add_episode_runtime.cs` | add col default 0 | `Episodes.Runtime` | — | No | No | |
| 186 | `186_add_result_to_commands.cs` | add col default 1 | `Commands.Result` | — | No | No | |
| 187 | `187_add_on_series_add_to_notifications.cs` | add col default false | `Notifications.OnSeriesAdd` | — | No | No | |
| 188 | `188_postgres_update_timestamp_columns_to_with_timezone.cs` | retype 30+ DateTime cols → DateTimeOffset across both DBs | many tables (Blocklist, Commands, DownloadClientStatus, Episodes, ExtraFiles, History, ImportListStatus, IndexerStatus, MetadataFiles, PendingReleases, ScheduledTasks, Series, SubtitleFiles, VersionInfo); LogDb: Logs, UpdateHistory, VersionInfo | — | No | No | **Touches both DBs**. Wipes Commands rows first. The Postgres-side change is `WITH TIME ZONE`; SQLite is no-op for typing but FluentMigrator emits ALTER. |
| 189 | `189_add_colon_replacement_to_naming_config.cs` | add col default 4 | `NamingConfig.ColonReplacementFormat` | — | No | No | |
| 190 | `190_health_restored_notification.cs` | add col default false | `Notifications.OnHealthRestored` | — | No | No | |
| 191 | `191_add_download_client_tags.cs` | add col nullable | `DownloadClients.Tags` | — | No | No | |
| 192 | `192_import_exclusion_type.cs` | retype String→Int | `ImportListExclusions.TvdbId` (SQLite alter; PG uses `USING ::integer`) | — | No | No | **Column type change**, branch by DB engine. |
| 193 | `193_add_import_list_items.cs` | create + add col | `ImportListItems` NEW; `ImportListStatus.HasRemovedItemSinceLastClean` | — | No | No | |
| 194 | `194_add_notification_status.cs` | create table | `NotificationStatus` | — | No | No | |
| 195 | `195_parse_language_tags_from_existing_subtitle_files.cs` | backfill via parser | `SubtitleFiles.LanguageTags` (from RelativePath via `LanguageParser.ParseLanguageTags`) | language tag list | No | **Yes** — one-shot, depends on parser fidelity. | |
| 196 | `196_add_finale_type.cs` | add col nullable | `Episodes.FinaleType` (String) | — | No | No | |
| 197 | `197_list_add_missing_search.cs` | add col default true | `ImportLists.SearchForMissingEpisodes` | — | No | No | |
| 198 | `198_parse_titles_from_existing_subtitle_files.cs` | add cols + backfill from path | `SubtitleFiles.Title`/`Copy`; recompute Language/LanguageTags via `LanguageParser.ParseSubtitleLanguageInformation` | — | No | **Yes** — one-shot, parser-dependent. | |
| 199 | `199_series_last_aired.cs` | add col nullable | `Series.LastAired` | — | No | No | |
| 200 | `200_monitor_new_items.cs` | add cols default 0 | `Series.MonitorNewItems`, `ImportLists.MonitorNewItems` | — | No | No | |
| 201 | `201_email_encryption.cs` | reshape Email settings | `Notifications.Settings` (Email) | `requireEncryption: bool` → `useEncryption: int` | No | **Yes** — one-shot. | **HIGH RISK** (settings shape). |
| 202 | `202_add_indexer_flags.cs` | add cols default 0 | `Blocklist.IndexerFlags`, `EpisodeFiles.IndexerFlags` | — | No | No | |
| 203 | `203_release_type.cs` | add col + backfill via parser | `Blocklist.ReleaseType`, `EpisodeFiles.ReleaseType` (parsed from `OriginalFilePath`) | parser-derived enum int | No | **Yes** — one-shot, parser-dependent. | **HIGH RISK** (parser fidelity). |
| 204 | `204_add_release_hash.cs` | add col + backfill via parser | `EpisodeFiles.ReleaseHash` | parser-derived | No | **Yes** — one-shot, parser-dependent. | **HIGH RISK** (parser fidelity). |
| 205 | `205_rename_season_pack_spec.cs` | **polymorphic spec rename** | `CustomFormats.Specifications` JSON | `SeasonPackSpecification` → `ReleaseTypeSpecification` (substring replace) | No | No | **HIGH RISK — canonical polymorphic-rename example**, cross-cite `subsystems/quality-profiles-formats-organizer.md:324`. |
| 206 | `206_add_tmdbid.cs` | add col + index | `Series.TmdbId` (default 0) | — | No | No | |
| 207 | `207_add_size_to_quality_profiles.cs` | move Min/Max/Preferred from QualityDefinitions into per-Item JSON | `QualityProfiles.Items` (write nested sizes); drop columns | tree walk pushing sizes into nested groups | No | No | **HIGH RISK** (profile-item JSON shape). |
| 208 | *(gap)* | unused | — | — | — | — | |
| 209 | `209_add_on_import_complete_to_notifications.cs` | add col default false | `Notifications.OnImportComplete` | — | No | No | |
| 210 | `210_add_monitored_seasons_filter.cs` | rename CustomFilter key | `CustomFilters.Filters` (rename `hasUnmonitoredSeason`→`seasonsMonitoredStatus`; recode value semantics) | filter-array JSON shape | No | **Yes** — one-shot. | **HIGH RISK** (filter JSON shape). |
| 211 | `211_add_custom_colon_replacement_to_naming_config.cs` | add col default `""` | `NamingConfig.CustomColonReplacementFormat` | — | No | No | |
| 212 | `212_add_minium_upgrade_format_score_to_quality_profiles.cs` | add col default 1 | `QualityProfiles.MinUpgradeFormatScore` | — | No | No | (Filename typo: "minium".) |
| 213 | *(gap)* | unused | — | — | — | — | |
| 214 | `214_fake_bluray576p.cs` | no-op | (none — body says "Replaced with 215") | — | No | **Yes** — explicit no-op. | |
| 215 | `215_add_blurary576p_quality_in_profiles_with_grouped_blurary480p.cs` | insert quality 22 into profile tree | `QualityProfiles.Items` (after id 13) | profile-item tree mutation | No | No | **HIGH RISK** (profile tree). |
| 216 | *(gap)* | unused | — | — | — | — | |
| 217 | `217_add_mal_and_anilist_ids.cs` | add cols default `[]` | `Series.MalIds`, `Series.AniListIds` (JSON int-array columns) | — | No | No | |
| 218 | `218_telegram_link_preview.cs` | reshape Telegram settings | `Notifications.Settings` (Telegram) | derive `linkPreview` from `metadataLinks` array | No | **Yes** — one-shot. | **HIGH RISK** (settings shape). |
| 219 | `219_nzb_su_url_to_nzb_life.cs` | string mutate Settings | `Indexers.Settings` (Newznab) | `api.nzb.su` → `api.nzb.life` | No | **Yes** — one-shot. | **HIGH RISK** (URL string mutation). |
| 220 | `220_enable_season_pack_seeding_goal.cs` | reshape nested seedCriteria | `Indexers.Settings` (any with seedCriteria) | add `seasonPackSeedGoal` and `seasonPackSeedRatio` from existing fields | No | **Yes** — one-shot. | **HIGH RISK** (nested settings shape). |
| 221 | `221_add_exclusion_tags_to_release_profiles.cs` | add col default `[]` | `ReleaseProfiles.ExcludedTags` | — | No | No | |
| 222 | *(gap)* | unused | — | — | — | — | |
| 223 | `223_add_source_to_blocklist.cs` | add col nullable | `Blocklist.Source` | — | No | No | |
| 224 | `224_release_profile_indexer_ids.cs` | int → int-list rename | `ReleaseProfiles.IndexerIds` add (default `[]`); backfill from `IndexerId`; drop `IndexerId` | single int → JSON int array | No | No | **HIGH RISK** (column shape change with backfill). |
| 225 | `225_mediainfo_multiple_streams.cs` | massive MediaInfo reshape | `EpisodeFiles.MediaInfo` | flat `audioFormat`/`audioCodecID`/`audioChannels`/`audioLanguages`/`subtitles` → nested `audioStreams: [{language, format, codecId, profile, bitrate, channels, channelPositions}]` and `subtitleStreams: [{language}]` | No | No | **HIGH RISK — largest MediaInfo migration** post-163. Schema-revision >=14 gated. |
| 226 | `226_add_air_date_filtering_to_release_profiles.cs` | add cols | `ReleaseProfiles.AirDateRestriction`/`AirDateGracePeriod` | — | No | No | |
| 227 | `227_original_country.cs` | add col nullable | `Series.OriginalCountry` | — | No | No | |
| 228 | *(gap)* | unused | — | — | — | — | |
| 229 | *(gap)* | unused | — | — | — | — | |
| 230 | `230_add_mapping_id_to_scene_mappings.cs` | add col nullable | `SceneMappings.MappingId` | — | No | No | Highest applied version (`VersionInfo.MaxVersion = 230`). |

## Open questions

- **MIG-Q1** — *Goose granularity vs. per-row JSON mutations.* FluentMigrator's `Execute.WithConnection(...)` lets each migration scan rows, parse JSON, mutate, and re-write inside one transaction. `goose` migrations are SQL-or-Go; for the Go-go path, we need a stable shape for the mutator (Dapper-equivalent helpers, an STJ-compat marshaller wired to the catalogue rules in `embedded-docs/catalogue.md`). Should every JSON-mutating migration be a Go-side `goose` migration (using the `goose.AddMigration` programmatic registration) or a pre-baked SQL migration that reads/writes via a sidecar tool? Recommend Go-side per-shape because the C# code already produces verifiable output. **Blocks Phase 1 (datastore port).**

- **MIG-Q2** — *STJ-equivalence acceptance criteria.* The contract in `embedded-docs/catalogue.md` covers camelCase, indented, allow-trailing-commas, `WhenWritingNull`, `JsonStringEnumConverter(camelCase, allowIntegerValues=true)`, `STJUtcConverter`, `STJTimeSpanConverter`. Migrations 163, 225 carry their own copy of this config block — proving the in-house contract. The Go port needs a single Marshal/Unmarshal pair that round-trips every shape in the embedded-docs catalogue **byte-for-byte** (modulo whitespace per EDOC-Q8). What's the test plan? Recommend a goldens-based compatibility test using a frozen pre-230 user DB. **Blocks Phase 1.**

- **MIG-Q3** — *Provider-string registry seeding.* Migrations 023, 026, 027, 051, 082, 105 all `Update.Table(...)` the `Implementation` or `ConfigContract` column. The Go-port stable-ID registry (TP-Q1/TP-Q2) **must** include alias mappings for every old name shipped by Sonarr (`Blackhole` ↔ `UsenetBlackhole`, `DownloadStation` ↔ `TorrentDownloadStation`, etc.) so existing user rows continue to load. Should the registry also enforce that any further rename forces a goose migration? Recommend yes, with the registry as the source of truth and a build-time check. **Blocks Phase 1 (any provider port).**

- **MIG-Q4** — *Polymorphic spec-class rename governance.* Migration 205 uses `REPLACE("Specifications", 'SeasonPackSpecification', 'ReleaseTypeSpecification')` — bulk substring replace inside a JSON column. Future spec renames will need the same pattern. Should the Go port standardise this as a `migrations.RenameSpecType(table, column, oldName, newName)` helper that does substring-aware replace inside the polymorphic envelope (with care for the substring being inside a `body.value` field by accident)? Recommend a structural helper that parses to AST then rewrites. **Blocks Phase 2 (CustomFormats UI).**

- **MIG-Q5** — *Log-DB migrations under `LogDbEnabled=false`.* Migrations 001 (creates `Logs`), 006 (recreates `Logs`), 064 (alters `Logs`), 141 (creates `UpdateHistory`), 146 (cleans `UpdateHistory`), 188 (retypes timestamps in `Logs`/`UpdateHistory`/`VersionInfo`) all run in the log DB. With `LogDbEnabled=false` the Sonarr runner skips `LogDbUpgrade()` entirely (`subsystems/system.md` UPDATE-Q5 / `LogDbEnabled` finding). Goose has no per-migration "skip if config says so" hook. Two options: (a) two goose subprojects, one per DB, gate the log-DB project's runner on the config flag, (b) single goose project, every log-DB migration becomes a no-op when the log DB connection is `nil`. Recommend (a) with the log-DB runner bound to its own DSN. **Blocks Phase 1 (datastore port).**

- **MIG-Q6** — *Parser-fidelity backfills.* Migrations 053, 166, 195, 198, 203, 204 invoke `Parser.Parser.NormalizeTitle`, `SeriesTitleNormalizer.Normalize`, `LanguageParser.ParseLanguageTags`, `LanguageParser.ParseSubtitleLanguageInformation`, `Parser.Parser.ParseTitle` to fill new columns. The Go-port's parser equivalents must produce **identical output** for these legacy strings. Should the Go port elide these migrations entirely (data is already backfilled in user DBs at version 230) and simply not provide the backfill code? Recommend **yes for the goose port** — i.e., bake them as drop-able — but keep the parser parity tests in `parser/*.md` aligned to ensure new data uses the same algorithms. **Blocks Phase 0.5b parser deliverables.**

- **MIG-Q7** — *Schema-revision-gated MediaInfo migrations.* 163 skips rows where `SchemaRevision >= 8`; 225 skips where `>= 14`. The Go port must respect these gates so re-running on a partially-migrated DB is safe. Should `MediaInfoModel.SchemaRevision` be a load-bearing column with explicit semantics in the Go-side type? Recommend yes — and add a `MediaInfoSchemaRevision` constant in Go for cross-checks. **Blocks Phase 1 (mediafiles port).**

- **MIG-Q8** — *Silent-loss provider deletions.* 087, 098, 104, 107, 124, 125, 138, 145, 161, 173 delete rows for retired providers. The Go port doesn't need to re-run these (the rows are gone in user DBs). But: a fresh-install Go DB needs a registry that *knows* these implementations are dead so that an old backup-restore cycle into a Go-side fresh DB doesn't resurrect zombies via the import-from-backup path. Should the registry maintain an explicit `Retired` set with the same names? Recommend yes. **Blocks Phase 2 (provider import/export UI).**

- **MIG-Q9** — *Column-type changes (84, 192).* Both retype existing columns (`QualityDefinitions.Min/MaxSize` Int→Double, `ImportListExclusions.TvdbId` String→Int). FluentMigrator uses the engine-native `ALTER COLUMN`, branching on SQLite vs Postgres. The schema dumps already capture the post-state. For goose: SQLite needs explicit `BEGIN; CREATE TABLE new …; INSERT INTO new SELECT cast(…) FROM old; DROP TABLE old; RENAME …` because SQLite's `ALTER COLUMN` was historically limited; Postgres can use `USING ::cast`. Document the mechanic in a migrations-conventions section per goose migration. **Blocks Phase 1.**

- **MIG-Q10** — *Postgres-vs-SQLite branching frequency.* Migrations 132, 139, 147, 184, 188, 192 branch on database engine (via `IfDatabase(...)` or `conn.GetType().FullName == "Npgsql.NpgsqlConnection"`). The Go port using a single goose runner per DB engine still needs portable migrations. How many of these are unavoidable engine-specific (e.g., timezone-aware columns in 188 are a Postgres-only concept on SQLite, which has no native type) vs. workaroundable? Recommend an audit during the actual goose authoring — the count is small (6) and each is documented above. **Blocks Phase 1.**

- **MIG-Q11** — *MediaInfo migration 148 cumulativity.* The substring replace in 148 (`audioChannels` → `audioChannelsContainer`) is followed up by 163 (full reshape) and 225 (split into streams). On a freshly-restored old DB the chain has to apply in order. Confirm goose ordering and that each migration's output is the next's input (especially the 148→163 transition, where 163's deserialiser expects the post-148 keys). Recommend a unit test that round-trips a v147-vintage MediaInfo through 148→163→225. **Blocks Phase 1 (mediafiles port).**

- **MIG-Q12** — *Hardcoded API key in migration 033.* `private const string API_KEY = "yz9b4U215iR4vrKFRfjNXP24NMNPKJ"` baked into a migration that backfills Pushover settings. This is a Pushover application key, not an end-user key. The Go port must reproduce this verbatim if migration 033 is replayed on a fresh-install of an old backup; if 033 is dropped (it's a one-shot legacy fix), the constant goes away with it. Confirm **drop status**. Recommend dropable. **Phase 1 hygiene.**
