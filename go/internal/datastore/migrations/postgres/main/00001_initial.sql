-- Ports src/NzbDrone.Core/Datastore/Migration/001_initial_setup.cs (MainDbUpgrade) for Postgres.
-- See go/analysis/migrations/ledger.md row 1.
--
-- The shape mirrors what FluentMigrator's Postgres15_0 generator emits,
-- captured from analysis/schema/postgres.sql at version 230 and back-projected
-- to version 1. Differences from the SQLite migration:
--   - identity columns use GENERATED ALWAYS AS IDENTITY (PG10+ syntax)
--   - text instead of TEXT (FluentMigrator emits lowercase)
--   - timestamp without time zone instead of DATETIME
--   - integer instead of INTEGER for Int32, bigint for Int64
--   - boolean instead of integer-as-bool — Sonarr's C# Boolean type maps
--     to boolean on Postgres (vs INTEGER 0/1 on SQLite)
--   - uniques are split: separate ALTER TABLE / ADD CONSTRAINT instead of
--     inline UNIQUE keyword. The captured schema shows both forms in use;
--     we use the inline form here for readability since the cumulative
--     end-state is what matters, not the per-step DDL form.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS "Config" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "Key" text NOT NULL UNIQUE,
    "Value" text NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS "IX_Config_Key" ON "Config" ("Key" ASC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS "RootFolders" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "Path" text NOT NULL UNIQUE
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS "IX_RootFolders_Path" ON "RootFolders" ("Path" ASC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS "Series" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "TvdbId" integer NOT NULL UNIQUE,
    "TvRageId" integer NOT NULL UNIQUE,
    "ImdbId" text NOT NULL UNIQUE,
    "Title" text NOT NULL,
    "TitleSlug" text NOT NULL UNIQUE,
    "CleanTitle" text NOT NULL,
    "Status" integer NOT NULL,
    "Overview" text,
    "AirTime" text,
    "Images" text NOT NULL,
    "Path" text NOT NULL,
    "Monitored" boolean NOT NULL,
    "QualityProfileId" integer NOT NULL,
    "SeasonFolder" boolean NOT NULL,
    "LastInfoSync" timestamp without time zone,
    "LastDiskSync" timestamp without time zone,
    "Runtime" integer NOT NULL,
    "SeriesType" integer NOT NULL,
    "BacklogSetting" integer NOT NULL,
    "Network" text,
    "CustomStartDate" timestamp without time zone,
    "UseSceneNumbering" boolean NOT NULL,
    "FirstAired" timestamp without time zone,
    "NextAiring" timestamp without time zone
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS "IX_Series_TvdbId" ON "Series" ("TvdbId" ASC);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS "IX_Series_TvRageId" ON "Series" ("TvRageId" ASC);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS "IX_Series_ImdbId" ON "Series" ("ImdbId" ASC);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS "IX_Series_TitleSlug" ON "Series" ("TitleSlug" ASC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS "Seasons" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "SeriesId" integer NOT NULL,
    "SeasonNumber" integer NOT NULL,
    "Ignored" boolean NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS "Episodes" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "TvDbEpisodeId" integer NOT NULL UNIQUE,
    "SeriesId" integer NOT NULL,
    "SeasonNumber" integer NOT NULL,
    "EpisodeNumber" integer NOT NULL,
    "Title" text,
    "Overview" text,
    "Ignored" boolean,
    "EpisodeFileId" integer,
    "AirDate" timestamp without time zone,
    "AbsoluteEpisodeNumber" integer,
    "SceneAbsoluteEpisodeNumber" integer,
    "SceneSeasonNumber" integer,
    "SceneEpisodeNumber" integer
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS "IX_Episodes_TvDbEpisodeId" ON "Episodes" ("TvDbEpisodeId" ASC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS "EpisodeFiles" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "SeriesId" integer NOT NULL,
    "Path" text NOT NULL UNIQUE,
    "Quality" text NOT NULL,
    "Size" bigint NOT NULL,
    "DateAdded" timestamp without time zone NOT NULL,
    "SeasonNumber" integer NOT NULL,
    "SceneName" text,
    "ReleaseGroup" text
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS "IX_EpisodeFiles_Path" ON "EpisodeFiles" ("Path" ASC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS "History" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "EpisodeId" integer NOT NULL,
    "SeriesId" integer NOT NULL,
    "NzbTitle" text NOT NULL,
    "Date" timestamp without time zone NOT NULL,
    "Quality" text NOT NULL,
    "Indexer" text NOT NULL,
    "NzbInfoUrl" text,
    "ReleaseGroup" text
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS "Notifications" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "Name" text NOT NULL,
    "OnGrab" boolean NOT NULL,
    "OnDownload" boolean NOT NULL,
    "Settings" text NOT NULL,
    "Implementation" text NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS "ScheduledTasks" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "TypeName" text NOT NULL UNIQUE,
    "Interval" integer NOT NULL,
    "LastExecution" timestamp without time zone NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS "IX_ScheduledTasks_TypeName" ON "ScheduledTasks" ("TypeName" ASC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS "Indexers" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "Enable" boolean NOT NULL,
    "Name" text NOT NULL UNIQUE,
    "Implementation" text NOT NULL,
    "Settings" text
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS "IX_Indexers_Name" ON "Indexers" ("Name" ASC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS "QualityProfiles" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "Name" text NOT NULL UNIQUE,
    "Cutoff" integer NOT NULL,
    "Allowed" text NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS "IX_QualityProfiles_Name" ON "QualityProfiles" ("Name" ASC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS "QualitySizes" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "QualityId" integer NOT NULL UNIQUE,
    "Name" text NOT NULL UNIQUE,
    "MinSize" integer NOT NULL,
    "MaxSize" integer NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS "IX_QualitySizes_QualityId" ON "QualitySizes" ("QualityId" ASC);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS "IX_QualitySizes_Name" ON "QualitySizes" ("Name" ASC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS "SceneMappings" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "CleanTitle" text NOT NULL,
    "SceneName" text NOT NULL,
    "TvdbId" integer NOT NULL,
    "SeasonNumber" integer NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS "NamingConfig" (
    "Id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    "UseSceneName" boolean NOT NULL,
    "Separator" text NOT NULL,
    "NumberStyle" integer NOT NULL,
    "IncludeSeriesTitle" boolean NOT NULL,
    "MultiEpisodeStyle" integer NOT NULL,
    "IncludeEpisodeTitle" boolean NOT NULL,
    "IncludeQuality" boolean NOT NULL,
    "ReplaceSpaces" boolean NOT NULL,
    "SeasonFolderFormat" text NOT NULL
);
-- +goose StatementEnd

-- +goose Down
-- The C# side never implemented Down() — every Sonarr migration throws
-- NotImplementedException on rollback. See go/analysis/migrations/ledger.md.
SELECT 'rollback not supported; restore from backup' WHERE false;
