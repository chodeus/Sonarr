-- Ports src/NzbDrone.Core/Datastore/Migration/001_initial_setup.cs (MainDbUpgrade).
-- See go/analysis/migrations/ledger.md row 1 for intent + reversibility notes.
--
-- The SQL below mirrors what FluentMigrator's SQLite generator emits for the
-- C# migration, captured from a fresh sonarr.db at version 230 and
-- back-projected to version 1 (none of the tables created here have been
-- dropped between 1 and 230 — they have only been altered).
--
-- "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT comes from
-- TableForModel(name); see Migration/Framework/MigrationExtension.cs.
-- TEXT is the FluentMigrator SQLite type for AsString();
-- INTEGER for AsInt32/AsInt64/AsBoolean;
-- DATETIME (which sqlite stores as TEXT) for AsDateTime.
-- Unique columns get a separate CREATE UNIQUE INDEX statement, mirroring
-- the captured production schema.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS "Config" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "Key" TEXT NOT NULL UNIQUE,
    "Value" TEXT NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_Config_Key" ON "Config" ("Key" ASC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS "RootFolders" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "Path" TEXT NOT NULL UNIQUE
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_RootFolders_Path" ON "RootFolders" ("Path" ASC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS "Series" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "TvdbId" INTEGER NOT NULL UNIQUE,
    "TvRageId" INTEGER NOT NULL UNIQUE,
    "ImdbId" TEXT NOT NULL UNIQUE,
    "Title" TEXT NOT NULL,
    "TitleSlug" TEXT NOT NULL UNIQUE,
    "CleanTitle" TEXT NOT NULL,
    "Status" INTEGER NOT NULL,
    "Overview" TEXT,
    "AirTime" TEXT,
    "Images" TEXT NOT NULL,
    "Path" TEXT NOT NULL,
    "Monitored" INTEGER NOT NULL,
    "QualityProfileId" INTEGER NOT NULL,
    "SeasonFolder" INTEGER NOT NULL,
    "LastInfoSync" DATETIME,
    "LastDiskSync" DATETIME,
    "Runtime" INTEGER NOT NULL,
    "SeriesType" INTEGER NOT NULL,
    "BacklogSetting" INTEGER NOT NULL,
    "Network" TEXT,
    "CustomStartDate" DATETIME,
    "UseSceneNumbering" INTEGER NOT NULL,
    "FirstAired" DATETIME,
    "NextAiring" DATETIME
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_Series_TvdbId" ON "Series" ("TvdbId" ASC);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_Series_TvRageId" ON "Series" ("TvRageId" ASC);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_Series_ImdbId" ON "Series" ("ImdbId" ASC);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_Series_TitleSlug" ON "Series" ("TitleSlug" ASC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS "Seasons" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "SeriesId" INTEGER NOT NULL,
    "SeasonNumber" INTEGER NOT NULL,
    "Ignored" INTEGER NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS "Episodes" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "TvDbEpisodeId" INTEGER NOT NULL UNIQUE,
    "SeriesId" INTEGER NOT NULL,
    "SeasonNumber" INTEGER NOT NULL,
    "EpisodeNumber" INTEGER NOT NULL,
    "Title" TEXT,
    "Overview" TEXT,
    "Ignored" INTEGER,
    "EpisodeFileId" INTEGER,
    "AirDate" DATETIME,
    "AbsoluteEpisodeNumber" INTEGER,
    "SceneAbsoluteEpisodeNumber" INTEGER,
    "SceneSeasonNumber" INTEGER,
    "SceneEpisodeNumber" INTEGER
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_Episodes_TvDbEpisodeId" ON "Episodes" ("TvDbEpisodeId" ASC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS "EpisodeFiles" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "SeriesId" INTEGER NOT NULL,
    "Path" TEXT NOT NULL UNIQUE,
    "Quality" TEXT NOT NULL,
    "Size" INTEGER NOT NULL,
    "DateAdded" DATETIME NOT NULL,
    "SeasonNumber" INTEGER NOT NULL,
    "SceneName" TEXT,
    "ReleaseGroup" TEXT
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_EpisodeFiles_Path" ON "EpisodeFiles" ("Path" ASC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS "History" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "EpisodeId" INTEGER NOT NULL,
    "SeriesId" INTEGER NOT NULL,
    "NzbTitle" TEXT NOT NULL,
    "Date" DATETIME NOT NULL,
    "Quality" TEXT NOT NULL,
    "Indexer" TEXT NOT NULL,
    "NzbInfoUrl" TEXT,
    "ReleaseGroup" TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS "Notifications" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "Name" TEXT NOT NULL,
    "OnGrab" INTEGER NOT NULL,
    "OnDownload" INTEGER NOT NULL,
    "Settings" TEXT NOT NULL,
    "Implementation" TEXT NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS "ScheduledTasks" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "TypeName" TEXT NOT NULL UNIQUE,
    "Interval" INTEGER NOT NULL,
    "LastExecution" DATETIME NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_ScheduledTasks_TypeName" ON "ScheduledTasks" ("TypeName" ASC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS "Indexers" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "Enable" INTEGER NOT NULL,
    "Name" TEXT NOT NULL UNIQUE,
    "Implementation" TEXT NOT NULL,
    "Settings" TEXT
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_Indexers_Name" ON "Indexers" ("Name" ASC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS "QualityProfiles" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "Name" TEXT NOT NULL UNIQUE,
    "Cutoff" INTEGER NOT NULL,
    "Allowed" TEXT NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_QualityProfiles_Name" ON "QualityProfiles" ("Name" ASC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS "QualitySizes" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "QualityId" INTEGER NOT NULL UNIQUE,
    "Name" TEXT NOT NULL UNIQUE,
    "MinSize" INTEGER NOT NULL,
    "MaxSize" INTEGER NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_QualitySizes_QualityId" ON "QualitySizes" ("QualityId" ASC);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE UNIQUE INDEX "IX_QualitySizes_Name" ON "QualitySizes" ("Name" ASC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS "SceneMappings" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "CleanTitle" TEXT NOT NULL,
    "SceneName" TEXT NOT NULL,
    "TvdbId" INTEGER NOT NULL,
    "SeasonNumber" INTEGER NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS "NamingConfig" (
    "Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    "UseSceneName" INTEGER NOT NULL,
    "Separator" TEXT NOT NULL,
    "NumberStyle" INTEGER NOT NULL,
    "IncludeSeriesTitle" INTEGER NOT NULL,
    "MultiEpisodeStyle" INTEGER NOT NULL,
    "IncludeEpisodeTitle" INTEGER NOT NULL,
    "IncludeQuality" INTEGER NOT NULL,
    "ReplaceSpaces" INTEGER NOT NULL,
    "SeasonFolderFormat" TEXT NOT NULL
);
-- +goose StatementEnd

-- +goose Down
-- The C# side never implemented Down() — every Sonarr migration throws
-- NotImplementedException on rollback (see analysis/migrations/ledger.md).
-- The Go port preserves this contract: rolling back a Sonarr DB is not a
-- supported operation. If an emergency requires one, restore from backup.
SELECT 'rollback not supported; restore from backup' WHERE 0;
